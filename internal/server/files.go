package server

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
)

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	settings, err := s.cfg.Store.Settings()
	if err != nil {
		internalError(w, r, err)
		return
	}
	includeHidden, err := hiddenPreference(r, settings.ShowHidden)
	if err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "hidden must be true or false")
		return
	}
	listing, err := s.cfg.Files.List(r.URL.Query().Get("path"), includeHidden)
	if err != nil {
		mapError(w, r, err)
		return
	}
	response := struct {
		Path         string           `json:"path"`
		Entries      []zenfiles.Entry `json:"entries"`
		AdvancedMode bool             `json:"advancedMode"`
		Disk         *zenfiles.Usage  `json:"disk,omitempty"`
	}{Path: listing.Path, Entries: listing.Entries, AdvancedMode: listing.AdvancedMode}
	if usage, err := s.cfg.Files.Usage(); err == nil {
		response.Disk = &usage
	}
	writeJSON(w, http.StatusOK, response)
}

func hiddenPreference(r *http.Request, fallback bool) (bool, error) {
	value, present := r.URL.Query()["hidden"]
	if !present {
		return fallback, nil
	}
	if len(value) != 1 || value[0] != "true" && value[0] != "false" {
		return false, errors.New("invalid hidden preference")
	}
	return value[0] == "true", nil
}

func (s *Server) getFileContent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	extension := strings.ToLower(path.Ext(name))
	contentType := strings.SplitN(mime.TypeByExtension(extension), ";", 2)[0]
	if !isTextPreview(extension, contentType) {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported File", "only bounded text source can be edited")
		return
	}
	data, err := s.cfg.Files.ReadContent(name)
	if err != nil {
		mapError(w, r, err)
		return
	}
	if !utf8.Valid(data) {
		problem(w, r, http.StatusUnsupportedMediaType, "Unsupported Text", "text source is not valid UTF-8")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(data)
}

func (s *Server) createDirectory(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if err := readJSON(w, r, &request, 8<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid directory request")
		return
	}
	entry, err := s.cfg.Files.Mkdir(request.Path)
	if err != nil {
		mapError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	if r.ContentLength > s.uploads.maxLength {
		problem(w, r, http.StatusRequestEntityTooLarge, "Too Large", "upload exceeds the configured maximum")
		return
	}
	if r.ContentLength > 0 {
		if err := s.uploads.ensureDisk(uint64(r.ContentLength)); err != nil {
			mapError(w, r, err)
			return
		}
	}
	if !s.uploads.acquire() {
		w.Header().Set("Retry-After", "1")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many concurrent uploads")
		return
	}
	defer s.uploads.release()
	_, existingErr := s.cfg.Files.Entry(name)
	created := errors.Is(existingErr, fs.ErrNotExist)
	overwrite := r.Header.Get("If-None-Match") != "*"
	r.Body = http.MaxBytesReader(w, r.Body, s.uploads.maxLength+1)
	progress := &progressReader{writer: w, reader: r.Body, timeout: progressTimeout, context: r.Context(), touch: s.touch}
	reader := &diskCheckedReader{manager: s.uploads, reader: progress}
	_, err := s.cfg.Files.WriteContext(r.Context(), name, reader, overwrite)
	if err != nil {
		mapError(w, r, err)
		return
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	recursive := r.URL.Query().Get("recursive") == "true"
	if err := s.cfg.Files.Delete(name, recursive); err != nil {
		mapError(w, r, err)
		return
	}
	clean, _ := zenfiles.Normalize(name)
	if _, err := s.cfg.Store.DeleteSharesAtOrBelow(zenfiles.PublicPath(clean)); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type transferRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (s *Server) moveFile(w http.ResponseWriter, r *http.Request) {
	if !s.acquireHeavy(w, r) {
		return
	}
	defer s.releaseHeavy()
	var request transferRequest
	if err := readJSON(w, r, &request, 16<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid move request")
		return
	}
	if _, err := s.cfg.Files.MoveWithProgress(r.Context(), request.Source, request.Destination, false, s.touch); err != nil {
		mapError(w, r, err)
		return
	}
	clean, _ := zenfiles.Normalize(request.Source)
	if _, err := s.cfg.Store.DeleteSharesAtOrBelow(zenfiles.PublicPath(clean)); err != nil {
		internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) copyFile(w http.ResponseWriter, r *http.Request) {
	if !s.acquireHeavy(w, r) {
		return
	}
	defer s.releaseHeavy()
	var request transferRequest
	if err := readJSON(w, r, &request, 16<<10); err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "invalid copy request")
		return
	}
	if _, err := s.cfg.Files.CopyWithProgress(r.Context(), request.Source, request.Destination, false, s.touch); err != nil {
		mapError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("path")
	f, info, err := s.cfg.Files.OpenRegular(name)
	if err != nil {
		mapError(w, r, err)
		return
	}
	defer f.Close()
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Name()}))
	stream := &progressResponseWriter{ResponseWriter: w, timeout: progressTimeout, touch: s.touch}
	http.ServeContent(stream, r, info.Name(), info.ModTime(), f)
}

func (s *Server) checksumFile(w http.ResponseWriter, r *http.Request) {
	if !s.acquireHeavy(w, r) {
		return
	}
	defer s.releaseHeavy()
	algorithm := r.URL.Query().Get("algorithm")
	if algorithm == "" {
		algorithm = "sha256"
	}
	f, _, err := s.cfg.Files.OpenRegular(r.URL.Query().Get("path"))
	if err != nil {
		mapError(w, r, err)
		return
	}
	defer f.Close()
	var digest string
	switch algorithm {
	case "sha256":
		h := sha256.New()
		if _, err := copyWithContext(r.Context(), h, &activityReader{reader: f, touch: s.touch}); err != nil {
			mapError(w, r, err)
			return
		}
		digest = hex.EncodeToString(h.Sum(nil))
	case "sha512":
		h := sha512.New()
		if _, err := copyWithContext(r.Context(), h, &activityReader{reader: f, touch: s.touch}); err != nil {
			mapError(w, r, err)
			return
		}
		digest = hex.EncodeToString(h.Sum(nil))
	default:
		problem(w, r, http.StatusBadRequest, "Invalid Request", "checksum algorithm must be sha256 or sha512")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"algorithm": algorithm, "value": digest})
}

func (s *Server) searchFiles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if len(query) == 0 || len(query) > 256 {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "search query must contain 1 to 256 characters")
		return
	}
	settings, err := s.cfg.Store.Settings()
	if err != nil {
		internalError(w, r, err)
		return
	}
	includeHidden, err := hiddenPreference(r, settings.ShowHidden)
	if err != nil {
		problem(w, r, http.StatusBadRequest, "Invalid Request", "hidden must be true or false")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	result, err := s.cfg.Files.Search(ctx, r.URL.Query().Get("path"), query, includeHidden, parsePositiveInt(r.URL.Query().Get("limit"), 250, 1000))
	if err != nil {
		mapError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	usage, err := s.cfg.Files.Usage()
	if err != nil {
		mapError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
