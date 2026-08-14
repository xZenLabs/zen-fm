package server

import (
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/state"
)

const publicShareCookie = "zenfm_share"

type shareResponse struct {
	ID                string     `json:"id"`
	Path              string     `json:"path"`
	Name              string     `json:"name"`
	URL               string     `json:"url,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	PasswordProtected bool       `json:"passwordProtected"`
	CreatedAt         time.Time  `json:"createdAt"`
}

func sharePayload(v state.Share) shareResponse {
	response := shareResponse{ID: v.ID, Path: v.Path, Name: v.Name, PasswordProtected: v.PasswordHash != "", CreatedAt: time.Unix(v.CreatedAt, 0).UTC()}
	if v.ExpiresAt != 0 {
		expires := time.Unix(v.ExpiresAt, 0).UTC()
		response.ExpiresAt = &expires
	}
	return response
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	values, err := s.cfg.Store.Shares()
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]shareResponse, 0, len(values))
	for _, value := range values {
		if value.ExpiresAt == 0 || value.ExpiresAt > s.cfg.Now().Unix() {
			out = append(out, sharePayload(value))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path             string `json:"path"`
		Name             string `json:"name"`
		Password         string `json:"password"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
	}
	if err := readJSON(w, r, &request, 8<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid share request")
		return
	}
	entry, err := s.publicFiles.Entry(request.Path)
	if err != nil {
		mapError(w, r, err)
		return
	}
	if !entry.Regular && !entry.Directory {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported File", "symlinks and special files cannot be shared")
		return
	}
	if s.publicFiles.Pseudo(request.Path) {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported File", "pseudo-filesystems cannot be shared")
		return
	}
	if entry.Directory {
		scope, scopeErr := s.publicFiles.OpenScope(request.Path)
		if scopeErr != nil {
			mapError(w, r, scopeErr)
			return
		}
		_ = scope.Close()
	} else {
		file, _, openErr := s.publicFiles.OpenRegular(request.Path)
		if openErr != nil {
			mapError(w, r, openErr)
			return
		}
		_ = file.Close()
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		request.Name = entry.Name
	}
	if len(request.Name) > 200 || len(request.Password) > 1024 {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "share name or password is too long")
		return
	}
	if request.ExpiresInSeconds != 0 && (request.ExpiresInSeconds < 60 || request.ExpiresInSeconds > int64((365*24*time.Hour).Seconds())) {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "share lifetime must be from 60 seconds to one year")
		return
	}
	var passwordHash string
	if request.Password != "" {
		if !s.acquireAuth(w, r) {
			return
		}
		passwordHash, err = auth.HashPassword(request.Password, s.cfg.PasswordParams)
		s.releaseAuth()
		if err != nil {
			internalError(w, r, err)
			return
		}
	}
	id, err := auth.RandomToken("", 128)
	if err != nil {
		internalError(w, r, err)
		return
	}
	secret, err := auth.RandomToken("zfm_share_", 256)
	if err != nil {
		internalError(w, r, err)
		return
	}
	digest := auth.TokenDigest(secret)
	now := s.cfg.Now()
	v := state.Share{ID: id, Path: entry.Path, Name: request.Name, SecretDigest: digest[:], PasswordHash: passwordHash, CreatedAt: now.Unix()}
	if request.ExpiresInSeconds != 0 {
		v.ExpiresAt = now.Add(time.Duration(request.ExpiresInSeconds) * time.Second).Unix()
	}
	if err := s.cfg.Store.CreateShare(v); err != nil {
		internalError(w, r, err)
		return
	}
	response := sharePayload(v)
	response.URL = "/s/" + secret
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) getShare(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Store.Share(r.PathValue("shareId"))
	if err != nil {
		shareNotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, sharePayload(v))
}

func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.cfg.Store.DeleteShare(r.PathValue("shareId"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !deleted {
		shareNotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publicShare(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Store.ShareBySecret(r.PathValue("secret"))
	if err != nil {
		shareNotFound(w, r)
		return
	}
	authorized := v.PasswordHash == "" || s.validPublicSession(r, v.ID)
	response, err := s.publicPayload(v, authorized, r.URL.Query().Get("path"))
	if err != nil {
		mapError(w, r, err)
		return
	}
	if authorized {
		s.touch()
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) unlockShare(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Store.ShareBySecret(r.PathValue("secret"))
	if err != nil {
		shareNotFound(w, r)
		return
	}
	keys := []string{"ip:" + remoteKey(r) + "|share:" + v.ID, "share:" + v.ID}
	if !s.shareLimiter.allowed(keys...) {
		w.Header().Set("Retry-After", "60")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many share password attempts")
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &request, 4<<10); err != nil || len(request.Password) > 1024 {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid password request")
		return
	}
	if v.PasswordHash != "" {
		if !s.acquireAuth(w, r) {
			return
		}
		valid := auth.VerifyPassword(v.PasswordHash, request.Password)
		s.releaseAuth()
		if !valid {
			s.shareLimiter.fail(keys...)
			problem(w, r, http.StatusUnauthorized, "Authentication Failed", "credentials were not accepted")
			return
		}
	}
	s.shareLimiter.success(keys...)
	raw, err := auth.RandomToken("zfm_public_", 256)
	if err != nil {
		internalError(w, r, err)
		return
	}
	expires := s.cfg.Now().Add(30 * time.Minute)
	if v.ExpiresAt != 0 && expires.Unix() > v.ExpiresAt {
		expires = time.Unix(v.ExpiresAt, 0)
	}
	if err := s.cfg.Store.CreatePublicSession(raw, state.PublicSession{ShareID: v.ID, ExpiresAt: expires.Unix()}); err != nil {
		internalError(w, r, err)
		return
	}
	cookiePath := "/api/v1/public/shares/" + r.PathValue("secret")
	http.SetCookie(w, &http.Cookie{Name: publicShareCookie, Value: raw, Path: cookiePath, HttpOnly: true, Secure: s.cfg.SecureTransport, SameSite: http.SameSiteStrictMode, MaxAge: int(expires.Sub(s.cfg.Now()).Seconds())})
	response, err := s.publicPayload(v, true, r.URL.Query().Get("path"))
	if err != nil {
		mapError(w, r, err)
		return
	}
	s.touch()
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) publicRaw(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Store.ShareBySecret(r.PathValue("secret"))
	if err != nil {
		shareNotFound(w, r)
		return
	}
	if v.PasswordHash != "" && !s.validPublicSession(r, v.ID) {
		problem(w, r, http.StatusUnauthorized, "Unauthorized", "share password is required")
		return
	}
	base, target, relative, err := shareTarget(v.Path, r.URL.Query().Get("path"))
	if err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Path", "shared descendant path is invalid")
		return
	}
	baseEntry, err := s.publicFiles.Entry(zenfiles.PublicPath(base))
	if err != nil || !baseEntry.Directory && relative != "." {
		shareNotFound(w, r)
		return
	}
	var f *os.File
	var info os.FileInfo
	if baseEntry.Directory {
		scope, scopeErr := s.publicFiles.OpenScope(zenfiles.PublicPath(base))
		if scopeErr != nil {
			mapError(w, r, scopeErr)
			return
		}
		defer scope.Close()
		f, info, err = scope.OpenRegular(zenfiles.PublicPath(relative))
		target = relative
	} else {
		f, info, err = s.publicFiles.OpenRegular(zenfiles.PublicPath(target))
	}
	if err != nil {
		mapError(w, r, err)
		return
	}
	defer f.Close()
	if contentType := mime.TypeByExtension(path.Ext(target)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Name()}))
	s.touch()
	stream := &progressResponseWriter{ResponseWriter: w, timeout: progressTimeout, touch: s.touch}
	http.ServeContent(stream, r, info.Name(), info.ModTime(), f)
}

func (s *Server) validPublicSession(r *http.Request, shareID string) bool {
	cookie, err := r.Cookie(publicShareCookie)
	if err != nil || !strings.HasPrefix(cookie.Value, "zfm_public_") {
		return false
	}
	_, err = s.cfg.Store.PublicSession(cookie.Value, shareID)
	return err == nil
}

func (s *Server) publicPayload(v state.Share, authorized bool, requested string) (map[string]any, error) {
	response := map[string]any{"name": v.Name, "path": "/", "passwordRequired": v.PasswordHash != "" && !authorized}
	if v.ExpiresAt != 0 {
		response["expiresAt"] = time.Unix(v.ExpiresAt, 0).UTC()
	} else {
		response["expiresAt"] = nil
	}
	if !authorized {
		return response, nil
	}
	base, target, relative, err := shareTarget(v.Path, requested)
	if err != nil {
		return nil, err
	}
	baseEntry, err := s.publicFiles.Entry(zenfiles.PublicPath(base))
	if err != nil {
		return nil, err
	}
	if !baseEntry.Directory && relative != "." {
		return nil, zenfiles.ErrInvalidPath
	}
	response["path"] = zenfiles.PublicPath(relative)
	if baseEntry.Directory {
		scope, err := s.publicFiles.OpenScope(zenfiles.PublicPath(base))
		if err != nil {
			return nil, err
		}
		defer scope.Close()
		scopePath := zenfiles.PublicPath(relative)
		entry, err := scope.Entry(scopePath)
		if err != nil {
			return nil, err
		}
		if !entry.Directory {
			return nil, zenfiles.ErrInvalidPath
		}
		listing, err := scope.List(scopePath, true)
		if err != nil {
			return nil, err
		}
		entries := make([]zenfiles.Entry, 0, len(listing.Entries))
		for _, child := range listing.Entries {
			child.Path = zenfiles.PublicPath(path.Join(relative, child.Name))
			entries = append(entries, child)
		}
		response["entries"] = entries
	} else {
		entry, err := s.publicFiles.Entry(zenfiles.PublicPath(target))
		if err != nil {
			return nil, err
		}
		entry.Path = zenfiles.PublicPath(relative)
		response["entry"] = entry
	}
	return response, nil
}

func shareTarget(stored, requested string) (base, target, relative string, err error) {
	base, err = zenfiles.Normalize(stored)
	if err != nil {
		return "", "", "", err
	}
	relative = "."
	if requested != "" && requested != "/" {
		relative, err = zenfiles.Normalize(requested)
		if err != nil {
			return "", "", "", err
		}
	}
	target = base
	if relative != "." {
		target = path.Join(base, relative)
	}
	if base != "." && target != base && !strings.HasPrefix(target, base+"/") {
		return "", "", "", zenfiles.ErrInvalidPath
	}
	return base, target, relative, nil
}

func shareNotFound(w http.ResponseWriter, r *http.Request) {
	problem(w, r, http.StatusNotFound, "Not Found", "share not found")
}
