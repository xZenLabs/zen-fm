package server

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/xZenLabs/zen-fm/internal/auth"
)

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		problem(w, r, http.StatusNotFound, "Not Found", "API endpoint not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		problem(w, r, http.StatusMethodNotAllowed, "Method Not Allowed", "method is not supported")
		return
	}
	if s.cfg.StaticFS == nil {
		problem(w, r, http.StatusNotFound, "Not Found", "frontend bundle unavailable")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if strings.HasPrefix(name, "../") {
		problem(w, r, http.StatusNotFound, "Not Found", "asset not found")
		return
	}
	data, err := fs.ReadFile(s.cfg.StaticFS, name)
	if err != nil {
		assetRequest := strings.HasPrefix(name, "assets/") || name == "favicon.ico" || name == "manifest.webmanifest" || name == "robots.txt"
		if !errors.Is(err, fs.ErrNotExist) || assetRequest {
			problem(w, r, http.StatusNotFound, "Not Found", "asset not found")
			return
		}
		name = "index.html"
		data, err = fs.ReadFile(s.cfg.StaticFS, name)
		if err != nil {
			problem(w, r, http.StatusNotFound, "Not Found", "frontend bundle unavailable")
			return
		}
	}
	if name == "index.html" {
		nonce, err := auth.RandomToken("", 128)
		if err != nil {
			internalError(w, r, err)
			return
		}
		meta := `<meta name="csp-nonce" content="` + nonce + `" />`
		data = []byte(strings.Replace(string(data), "<head>", "<head>"+meta, 1))
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; frame-src 'self' blob:; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; font-src 'self'")
		w.Header().Set("Cache-Control", "no-cache")
	} else if fingerprinted(name) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func fingerprinted(name string) bool {
	if !strings.HasPrefix(name, "assets/") {
		return false
	}
	base := path.Base(name)
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 || dot == len(base)-1 {
		return false
	}
	stem := base[:dot]
	for index := strings.IndexByte(stem, '-'); index >= 0; {
		candidate := stem[index+1:]
		if len(candidate) >= 8 && viteHash(candidate) {
			return true
		}
		next := strings.IndexByte(stem[index+1:], '-')
		if next < 0 {
			break
		}
		index += next + 1
	}
	return false
}

func viteHash(value string) bool {
	for _, character := range value {
		if character != '-' && character != '_' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
