package server

import (
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/state"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		problem(w, r, http.StatusForbidden, "Forbidden", "request origin does not match")
		return
	}
	keys := []string{"ip:" + remoteKey(r), "account:owner"}
	if !s.loginLimiter.allowed(keys...) {
		w.Header().Set("Retry-After", "60")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many login attempts")
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &request, 4<<10); err != nil || len(request.Password) > 1024 {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid login request")
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer s.releaseAuth()
	owner, err := s.cfg.Store.Owner()
	if err != nil {
		internalError(w, r, err)
		return
	}
	validPassword := auth.VerifyPassword(owner.PasswordHash, request.Password)
	if !validPassword {
		s.loginLimiter.fail(keys...)
		problem(w, r, http.StatusUnauthorized, "Authentication Failed", "credentials were not accepted")
		return
	}
	s.loginLimiter.success(keys...)
	session, err := s.newSession(w)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.touch()
	writeJSON(w, http.StatusOK, s.sessionPayload(session, owner.SetupRequired))
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	owner, err := s.cfg.Store.Owner()
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sessionPayload(principalFrom(r).session, owner.SetupRequired))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if err := s.cfg.Store.DeleteSession(p.rawSession); err != nil {
		internalError(w, r, err)
		return
	}
	clearCookie(w, sessionCookieName(s.cfg.SecureTransport), s.cfg.SecureTransport)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := readJSON(w, r, &request, 4<<10); err != nil || len(request.CurrentPassword) > 1024 || !utf8.ValidString(request.NewPassword) || utf8.RuneCountInString(request.NewPassword) < 7 || len(request.NewPassword) > 1024 {
		problem(w, r, http.StatusBadRequest, "Invalid Password", "new password must contain at least 7 characters and not exceed 1024 bytes")
		return
	}
	if constantEqual(request.CurrentPassword, request.NewPassword) {
		problem(w, r, http.StatusBadRequest, "Invalid Password", "new password must differ from the current password")
		return
	}
	if !s.acquireAuth(w, r) {
		return
	}
	defer s.releaseAuth()
	owner, err := s.cfg.Store.Owner()
	if err != nil {
		internalError(w, r, err)
		return
	}
	if request.CurrentPassword == "" {
		if !owner.SetupRequired {
			problem(w, r, http.StatusUnauthorized, "Authentication Failed", "credentials were not accepted")
			return
		}
		if auth.VerifyPassword(owner.PasswordHash, request.NewPassword) {
			problem(w, r, http.StatusBadRequest, "Invalid Password", "new password must differ from the current password")
			return
		}
	} else if !auth.VerifyPassword(owner.PasswordHash, request.CurrentPassword) {
		problem(w, r, http.StatusUnauthorized, "Authentication Failed", "credentials were not accepted")
		return
	}
	hash, err := auth.HashPassword(request.NewPassword, s.cfg.PasswordParams)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.cfg.Store.ReplacePassword(hash, false); err != nil {
		internalError(w, r, err)
		return
	}
	clearCookie(w, sessionCookieName(s.cfg.SecureTransport), s.cfg.SecureTransport)
	session, err := s.newSession(w)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sessionPayload(session, false))
}

type tokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	Token      string     `json:"token,omitempty"`
}

func tokenPayload(v state.APIToken) tokenResponse {
	response := tokenResponse{ID: v.ID, Name: v.Name, CreatedAt: time.Unix(v.CreatedAt, 0).UTC(), ExpiresAt: time.Unix(v.ExpiresAt, 0).UTC()}
	if v.LastUsedAt != 0 {
		last := time.Unix(v.LastUsedAt, 0).UTC()
		response.LastUsedAt = &last
	}
	return response
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	values, err := s.cfg.Store.Tokens()
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]tokenResponse, 0, len(values))
	for _, value := range values {
		out = append(out, tokenPayload(value))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name             string `json:"name"`
		ExpiresInSeconds int64  `json:"expiresInSeconds"`
	}
	if err := readJSON(w, r, &request, 4<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid token request")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if !validateName(request.Name, 100) {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "token name must contain 1 to 100 characters")
		return
	}
	if request.ExpiresInSeconds == 0 {
		request.ExpiresInSeconds = int64((30 * 24 * time.Hour).Seconds())
	}
	if request.ExpiresInSeconds < 60 || request.ExpiresInSeconds > int64((365*24*time.Hour).Seconds()) {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "token lifetime must be from 60 seconds to one year")
		return
	}
	raw, err := auth.RandomToken("zfm_pat_", 256)
	if err != nil {
		internalError(w, r, err)
		return
	}
	id, err := auth.RandomToken("", 128)
	if err != nil {
		internalError(w, r, err)
		return
	}
	now := s.cfg.Now()
	v := state.APIToken{ID: id, Name: request.Name, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(request.ExpiresInSeconds) * time.Second).Unix()}
	if err := s.cfg.Store.CreateToken(raw, v); err != nil {
		internalError(w, r, err)
		return
	}
	response := tokenPayload(v)
	response.Token = raw
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("tokenId")
	if !validateName(id, 128) {
		problem(w, r, http.StatusNotFound, "Not Found", "token not found")
		return
	}
	deleted, err := s.cfg.Store.DeleteToken(id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !deleted {
		problem(w, r, http.StatusNotFound, "Not Found", "token not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingsResponse struct {
	state.Settings
	FavoriteTypes   map[string]string `json:"favoriteTypes,omitempty"`
	AdvancedMode    bool              `json:"advancedMode"`
	Root            string            `json:"root"`
	SecureTransport bool              `json:"secureTransport"`
	Version         string            `json:"version"`
}

func (s *Server) settingsResponse(settings state.Settings) settingsResponse {
	settings.StartupDirectory = s.startupDirectory()
	settings.StartupDirectoryInitialized = false
	response := settingsResponse{Settings: settings, AdvancedMode: s.cfg.Files.Advanced(), Root: s.cfg.Files.Name(), SecureTransport: s.cfg.SecureTransport, Version: s.cfg.Version}
	response.FavoriteTypes = make(map[string]string, len(settings.Favorites))
	for _, favorite := range settings.Favorites {
		if entry, err := s.cfg.Files.Entry(favorite); err == nil && (entry.Type == "file" || entry.Type == "directory") {
			response.FavoriteTypes[favorite] = entry.Type
		}
	}
	return response
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.cfg.Store.Settings()
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(settings))
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Theme                *string            `json:"theme"`
		Locale               *string            `json:"locale"`
		ShowHidden           *bool              `json:"showHidden"`
		ClientTimeoutSeconds *int               `json:"clientTimeoutSeconds"`
		StartupDirectory     *string            `json:"startupDirectory"`
		Favorites            *[]string          `json:"favorites"`
		FavoriteLabels       *map[string]string `json:"favoriteLabels"`
	}
	if err := readJSON(w, r, &request, 8<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid preference request")
		return
	}
	settings, err := s.cfg.Store.Settings()
	if err != nil {
		internalError(w, r, err)
		return
	}
	if request.Theme != nil {
		switch *request.Theme {
		case "light", "dark", "system":
			settings.Theme = *request.Theme
		default:
			problem(w, r, http.StatusBadRequest, "Invalid Request", "theme is invalid")
			return
		}
	}
	if request.Locale != nil {
		locale := strings.TrimSpace(*request.Locale)
		if len(locale) < 2 || len(locale) > 32 || strings.ContainsAny(locale, " /\\\x00") {
			problem(w, r, http.StatusBadRequest, "Invalid Request", "locale is invalid")
			return
		}
		settings.Locale = locale
	}
	if request.ShowHidden != nil {
		settings.ShowHidden = *request.ShowHidden
	}
	if request.ClientTimeoutSeconds != nil {
		if *request.ClientTimeoutSeconds < 0 || *request.ClientTimeoutSeconds > 86400 {
			problem(w, r, http.StatusBadRequest, "Invalid Request", "client timeout is invalid")
			return
		}
		settings.ClientTimeoutSeconds = *request.ClientTimeoutSeconds
	}
	if request.StartupDirectory != nil {
		if s.cfg.Files.Advanced() || !strings.HasPrefix(*request.StartupDirectory, "/") {
			problem(w, r, http.StatusBadRequest, "Invalid Request", "startup directory is invalid")
			return
		}
		clean, err := zenfiles.Normalize(*request.StartupDirectory)
		if err != nil {
			problem(w, r, http.StatusBadRequest, "Invalid Request", "startup directory is invalid")
			return
		}
		directory := zenfiles.PublicPath(clean)
		entry, err := s.cfg.Files.Entry(directory)
		if err != nil || !entry.Directory {
			problem(w, r, http.StatusBadRequest, "Invalid Request", "startup directory must be an accessible folder")
			return
		}
		settings.StartupDirectory = directory
	}
	if request.Favorites != nil {
		favorites := make([]string, 0, len(*request.Favorites))
		for _, favorite := range *request.Favorites {
			clean, err := zenfiles.Normalize(favorite)
			if err != nil || !strings.HasPrefix(favorite, "/") {
				problem(w, r, http.StatusBadRequest, "Invalid Request", "favorite path is invalid")
				return
			}
			favorite = zenfiles.PublicPath(clean)
			if !slices.Contains(settings.Favorites, favorite) {
				entry, err := s.cfg.Files.Entry(favorite)
				if err != nil || entry.Type != "directory" && entry.Type != "file" {
					problem(w, r, http.StatusBadRequest, "Invalid Request", "favorite must be an accessible file or folder")
					return
				}
			}
			if !slices.Contains(favorites, favorite) {
				favorites = append(favorites, favorite)
			}
		}
		settings.Favorites = favorites
	}
	if request.FavoriteLabels != nil {
		labels := make(map[string]string, len(*request.FavoriteLabels))
		for path, label := range *request.FavoriteLabels {
			label = strings.TrimSpace(label)
			if !slices.Contains(settings.Favorites, path) || !utf8.ValidString(label) || utf8.RuneCountInString(label) > 200 || strings.ContainsAny(label, "\x00\r\n") {
				problem(w, r, http.StatusBadRequest, "Invalid Request", "favorite label is invalid")
				return
			}
			if label != "" {
				labels[path] = label
			}
		}
		settings.FavoriteLabels = labels
	}
	for path := range settings.FavoriteLabels {
		if !slices.Contains(settings.Favorites, path) {
			delete(settings.FavoriteLabels, path)
		}
	}
	if err := s.cfg.Store.SaveSettings(settings); err != nil {
		internalError(w, r, err)
		return
	}
	if request.StartupDirectory != nil {
		s.startupDir.Store(settings.StartupDirectory)
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(settings))
}
