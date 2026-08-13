// Package server exposes ZenFM's versioned single-owner HTTP API.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/state"
)

const sessionCookie = "zenfm_session"

type Config struct {
	Store              *state.Store
	Files              *zenfiles.Root
	StaticFS           fs.FS
	Version            string
	SecureTransport    bool
	SessionIdle        time.Duration
	SessionAbsolute    time.Duration
	PasswordParams     auth.PasswordParams
	UploadDir          string
	MaxUploadBytes     int64
	UploadExpiry       time.Duration
	UploadConcurrency  int
	MaxActiveUploads   int
	HeavyConcurrency   int
	ModeLessFilesystem bool
	Now                func() time.Time
}

type Server struct {
	cfg          Config
	mux          *http.ServeMux
	authSlots    chan struct{}
	loginLimiter *attemptLimiter
	shareLimiter *attemptLimiter
	uploads      *uploadManager
	heavySlots   chan struct{}
	archiveMu    sync.Mutex
	archiveLinks map[string]archiveTicket
	lastActivity atomic.Int64
	lastPrune    atomic.Int64
}

type principalKind uint8

const (
	principalSession principalKind = iota + 1
	principalToken
)

type principal struct {
	kind       principalKind
	rawSession string
	session    state.Session
	token      state.APIToken
}

type contextKey struct{}

func New(cfg Config) (*Server, error) {
	if cfg.Store == nil || cfg.Files == nil {
		return nil, errors.New("state store and filesystem are required")
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.SessionIdle <= 0 {
		cfg.SessionIdle = 2 * time.Hour
	}
	if cfg.SessionAbsolute <= 0 {
		cfg.SessionAbsolute = 12 * time.Hour
	}
	if cfg.PasswordParams == (auth.PasswordParams{}) {
		cfg.PasswordParams = auth.DefaultPasswordParams
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HeavyConcurrency <= 0 {
		cfg.HeavyConcurrency = 2
	}
	if cfg.HeavyConcurrency > 8 {
		cfg.HeavyConcurrency = 8
	}
	if !cfg.Files.Advanced() {
		if err := cfg.Files.ExcludeAbsolute(cfg.Store.DataDir()); err != nil {
			return nil, fmt.Errorf("protect private state: %w", err)
		}
	}
	s := &Server{
		cfg: cfg, mux: http.NewServeMux(), authSlots: make(chan struct{}, 2),
		loginLimiter: newAttemptLimiter(5, time.Minute, cfg.Now),
		shareLimiter: newAttemptLimiter(8, time.Minute, cfg.Now),
		heavySlots:   make(chan struct{}, cfg.HeavyConcurrency),
		archiveLinks: make(map[string]archiveTicket),
	}
	uploads, err := newUploadManager(s, cfg.UploadDir, cfg.MaxUploadBytes, cfg.UploadExpiry, cfg.UploadConcurrency, cfg.MaxActiveUploads)
	if err != nil {
		return nil, fmt.Errorf("initialize uploads: %w", err)
	}
	s.uploads = uploads
	s.lastActivity.Store(cfg.Now().UnixNano())
	s.lastPrune.Store(cfg.Now().UnixNano())
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.securityHeaders(s.mux) }

func (s *Server) Close() { s.uploads.close() }

func (s *Server) LastActivity() time.Time {
	return time.Unix(0, s.lastActivity.Load())
}

func (s *Server) touch() { s.lastActivity.Store(s.cfg.Now().UnixNano()) }

func (s *Server) acquireHeavy(w http.ResponseWriter, r *http.Request) bool {
	select {
	case s.heavySlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many resource-intensive operations")
		return false
	}
}

func (s *Server) releaseHeavy() { <-s.heavySlots }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /api/v1/session", s.login)
	s.mux.Handle("GET /api/v1/session", s.require(false, false, http.HandlerFunc(s.getSession)))
	s.mux.Handle("DELETE /api/v1/session", s.require(false, true, http.HandlerFunc(s.logout)))
	s.mux.Handle("PUT /api/v1/owner/password", s.require(false, true, http.HandlerFunc(s.changePassword)))
	s.mux.Handle("GET /api/v1/tokens", s.require(false, false, http.HandlerFunc(s.listTokens)))
	s.mux.Handle("POST /api/v1/tokens", s.require(false, true, http.HandlerFunc(s.createToken)))
	s.mux.Handle("DELETE /api/v1/tokens/{tokenId}", s.require(false, true, http.HandlerFunc(s.deleteToken)))
	s.mux.Handle("GET /api/v1/settings", s.require(true, false, http.HandlerFunc(s.getSettings)))
	s.mux.Handle("PUT /api/v1/settings", s.require(false, true, http.HandlerFunc(s.putSettings)))
	s.mux.Handle("GET /api/v1/files", s.require(true, false, http.HandlerFunc(s.listFiles)))
	s.mux.Handle("DELETE /api/v1/files", s.require(true, true, http.HandlerFunc(s.deleteFile)))
	s.mux.Handle("POST /api/v1/files/directory", s.require(true, true, http.HandlerFunc(s.createDirectory)))
	s.mux.Handle("GET /api/v1/files/content", s.require(true, false, http.HandlerFunc(s.getFileContent)))
	s.mux.Handle("PUT /api/v1/files/content", s.require(true, true, http.HandlerFunc(s.putFile)))
	s.mux.Handle("POST /api/v1/files/move", s.require(true, true, http.HandlerFunc(s.moveFile)))
	s.mux.Handle("POST /api/v1/files/copy-size", s.require(true, false, http.HandlerFunc(s.copySizes)))
	s.mux.Handle("POST /api/v1/files/copy", s.require(true, true, http.HandlerFunc(s.copyFile)))
	s.mux.Handle("GET /api/v1/files/raw", s.require(true, false, http.HandlerFunc(s.rawFile)))
	s.mux.Handle("GET /api/v1/files/preview", s.require(true, false, http.HandlerFunc(s.previewFile)))
	s.mux.Handle("GET /api/v1/files/checksum", s.require(true, false, http.HandlerFunc(s.checksumFile)))
	s.mux.Handle("POST /api/v1/files/archive", s.require(true, true, http.HandlerFunc(s.archiveFiles)))
	s.mux.Handle("POST /api/v1/files/archive-tickets", s.require(false, true, http.HandlerFunc(s.createArchiveTicket)))
	s.mux.Handle("GET /api/v1/files/archive/{ticket}", s.require(false, false, http.HandlerFunc(s.downloadArchiveTicket)))
	s.mux.Handle("GET /api/v1/search", s.require(true, false, http.HandlerFunc(s.searchFiles)))
	s.mux.Handle("GET /api/v1/usage", s.require(true, false, http.HandlerFunc(s.usage)))
	s.mux.Handle("OPTIONS /api/v1/uploads", s.require(true, false, http.HandlerFunc(s.uploadOptions)))
	s.mux.Handle("POST /api/v1/uploads", s.require(true, true, http.HandlerFunc(s.createUpload)))
	s.mux.Handle("HEAD /api/v1/uploads/{uploadId}", s.require(true, false, http.HandlerFunc(s.headUpload)))
	s.mux.Handle("PATCH /api/v1/uploads/{uploadId}", s.require(true, true, http.HandlerFunc(s.patchUpload)))
	s.mux.Handle("DELETE /api/v1/uploads/{uploadId}", s.require(true, true, http.HandlerFunc(s.cancelUpload)))
	s.mux.Handle("GET /api/v1/shares", s.require(true, false, http.HandlerFunc(s.listShares)))
	s.mux.Handle("POST /api/v1/shares", s.require(true, true, http.HandlerFunc(s.createShare)))
	s.mux.Handle("GET /api/v1/shares/{shareId}", s.require(true, false, http.HandlerFunc(s.getShare)))
	s.mux.Handle("DELETE /api/v1/shares/{shareId}", s.require(true, true, http.HandlerFunc(s.deleteShare)))
	s.mux.HandleFunc("GET /api/v1/public/shares/{secret}", s.publicShare)
	s.mux.HandleFunc("POST /api/v1/public/shares/{secret}", s.unlockShare)
	s.mux.HandleFunc("GET /api/v1/public/shares/{secret}/raw", s.publicRaw)
	s.mux.HandleFunc("/", s.static)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": s.cfg.Version})
}

func (s *Server) require(allowToken, mutation bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r, allowToken)
		if err != nil {
			problem(w, r, http.StatusUnauthorized, "Unauthorized", "missing, expired, or revoked credentials")
			return
		}
		owner, err := s.cfg.Store.Owner()
		if err != nil {
			problem(w, r, http.StatusInternalServerError, "Internal Server Error", "state unavailable")
			return
		}
		if owner.SetupRequired && r.URL.Path != "/api/v1/session" && r.URL.Path != "/api/v1/owner/password" {
			problem(w, r, http.StatusForbidden, "Setup Required", "replace the setup password before using ZenFM")
			return
		}
		if mutation && p.kind == principalSession {
			if !sameOrigin(r) {
				problem(w, r, http.StatusForbidden, "Forbidden", "request origin does not match")
				return
			}
			provided := r.Header.Get("X-ZenFM-CSRF")
			if !constantEqual(provided, p.session.CSRFToken) {
				problem(w, r, http.StatusForbidden, "Forbidden", "CSRF token is invalid")
				return
			}
		}
		s.touch()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, p)))
	})
}

func (s *Server) authenticate(r *http.Request, allowToken bool) (principal, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		if !allowToken || !strings.HasPrefix(header, "Bearer ") || strings.Contains(strings.TrimPrefix(header, "Bearer "), " ") {
			return principal{}, errors.New("bearer token is not allowed")
		}
		raw := strings.TrimPrefix(header, "Bearer ")
		if !strings.HasPrefix(raw, "zfm_pat_") {
			return principal{}, errors.New("invalid bearer token")
		}
		token, err := s.cfg.Store.Token(raw, true)
		if err != nil {
			return principal{}, err
		}
		return principal{kind: principalToken, token: token}, nil
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || !strings.HasPrefix(cookie.Value, "zfm_session_") {
		return principal{}, errors.New("session cookie missing")
	}
	session, err := s.cfg.Store.Session(cookie.Value, s.cfg.SessionIdle, true)
	if err != nil {
		return principal{}, err
	}
	return principal{kind: principalSession, rawSession: cookie.Value, session: session}, nil
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.maybePrune()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) maybePrune() {
	now := s.cfg.Now()
	previous := s.lastPrune.Load()
	if now.Sub(time.Unix(0, previous)) < 15*time.Minute || !s.lastPrune.CompareAndSwap(previous, now.UnixNano()) {
		return
	}
	_ = s.uploads.pruneExpired()
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// SameSite=Strict plus the unpredictable CSRF value protects requests
		// from non-browser clients that legitimately omit Origin.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(u.Scheme, scheme) && equalHost(u.Host, r.Host)
}

func equalHost(a, b string) bool {
	normalize := func(value string) string {
		host, port, err := net.SplitHostPort(value)
		if err == nil {
			if port == "80" || port == "443" {
				return strings.ToLower(host)
			}
		}
		return strings.ToLower(value)
	}
	return normalize(a) == normalize(b)
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if limit <= 0 {
		limit = 64 << 10
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, r *http.Request, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": title, "status": status, "detail": detail, "instance": r.URL.Path,
	})
}

func mapError(w http.ResponseWriter, r *http.Request, err error) {
	status, title, detail := describeError(err)
	problem(w, r, status, title, detail)
}

func describeError(err error) (int, string, string) {
	switch {
	case errors.Is(err, zenfiles.ErrInvalidPath):
		return http.StatusBadRequest, "Invalid Path", "the path is not canonical or is outside the configured root"
	case errors.Is(err, osErrNotExist()):
		return http.StatusNotFound, "Not Found", "the requested path does not exist"
	case errors.Is(err, zenfiles.ErrConflict):
		return http.StatusConflict, "Conflict", err.Error()
	case errors.Is(err, state.ErrConflict):
		return http.StatusConflict, "Conflict", "stored operation state conflicts with the request"
	case errors.Is(err, zenfiles.ErrTooLarge), errors.Is(err, zenfiles.ErrWalkLimit):
		return http.StatusRequestEntityTooLarge, "Too Large", err.Error()
	case errors.Is(err, zenfiles.ErrNotRegular), errors.Is(err, zenfiles.ErrPseudoFile):
		return http.StatusUnsupportedMediaType, "Unsupported File", err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "Timed Out", "the operation exceeded its deadline"
	default:
		return http.StatusInternalServerError, "Internal Server Error", "the operation could not be completed"
	}
}

// osErrNotExist is isolated for tests and keeps platform wrapping behavior.
func osErrNotExist() error { return fs.ErrNotExist }

func constantEqual(a, b string) bool {
	ah, bh := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1 && a != "" && b != ""
}

func parsePositiveInt(value string, fallback, maximum int) int {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 || n > maximum {
		return fallback
	}
	return n
}

type attemptLimiter struct {
	mu        sync.Mutex
	items     map[string][]time.Time
	limit     int
	window    time.Duration
	now       func() time.Time
	maxKeys   int
	lastSweep time.Time
}

func newAttemptLimiter(limit int, window time.Duration, now func() time.Time) *attemptLimiter {
	return &attemptLimiter{items: make(map[string][]time.Time), limit: limit, window: window, now: now, maxKeys: 4096, lastSweep: now()}
}

func (l *attemptLimiter) allowed(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()
	for _, key := range keys {
		l.pruneLocked(key)
		if len(l.items[key]) >= l.limit {
			return false
		}
		if _, exists := l.items[key]; !exists && len(l.items) >= l.maxKeys {
			return false
		}
	}
	return true
}

func (l *attemptLimiter) fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()
	for _, key := range keys {
		l.pruneLocked(key)
		if _, exists := l.items[key]; exists || len(l.items) < l.maxKeys {
			l.items[key] = append(l.items[key], l.now())
		}
	}
}

func (l *attemptLimiter) success(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.items, key)
	}
}

func (l *attemptLimiter) pruneLocked(key string) {
	cutoff := l.now().Add(-l.window)
	values := l.items[key]
	kept := values[:0]
	for _, value := range values {
		if value.After(cutoff) {
			kept = append(kept, value)
		}
	}
	if len(kept) == 0 {
		delete(l.items, key)
	} else {
		l.items[key] = kept
	}
}

func (l *attemptLimiter) sweepLocked() {
	now := l.now()
	if len(l.items) < l.maxKeys && now.Sub(l.lastSweep) < l.window/2 {
		return
	}
	for key := range l.items {
		l.pruneLocked(key)
	}
	l.lastSweep = now
}

func remoteKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) acquireAuth(w http.ResponseWriter, r *http.Request) bool {
	select {
	case s.authSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		problem(w, r, http.StatusTooManyRequests, "Rate Limited", "too many password operations")
		return false
	}
}

func (s *Server) releaseAuth() { <-s.authSlots }

func (s *Server) newSession(w http.ResponseWriter) (state.Session, error) {
	raw, err := auth.RandomToken("zfm_session_", 256)
	if err != nil {
		return state.Session{}, err
	}
	csrf, err := auth.RandomToken("zfm_csrf_", 256)
	if err != nil {
		return state.Session{}, err
	}
	session, err := s.cfg.Store.CreateSession(raw, csrf, s.cfg.SessionIdle, s.cfg.SessionAbsolute)
	if err != nil {
		return state.Session{}, err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: raw, Path: "/", HttpOnly: true, Secure: s.cfg.SecureTransport, SameSite: http.SameSiteStrictMode, MaxAge: int(s.cfg.SessionAbsolute.Seconds())})
	return session, nil
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (s *Server) sessionPayload(v state.Session, setup bool) map[string]any {
	return map[string]any{
		"authenticated": true, "username": state.SetupUsername, "setupRequired": setup,
		"csrfToken": v.CSRFToken, "idleExpiresAt": time.Unix(v.IdleUntil, 0).UTC(), "absoluteExpiresAt": time.Unix(v.AbsoluteEnd, 0).UTC(),
	}
}

func principalFrom(r *http.Request) principal {
	p, _ := r.Context().Value(contextKey{}).(principal)
	return p
}

func validateName(value string, max int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= max && !strings.ContainsRune(value, 0)
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	_ = fmt.Sprintf("%v", err) // Never serialize internal errors or paths.
	problem(w, r, http.StatusInternalServerError, "Internal Server Error", "state unavailable")
}
