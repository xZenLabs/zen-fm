package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/xZenLabs/zen-fm/internal/auth"
	zenfiles "github.com/xZenLabs/zen-fm/internal/files"
	"github.com/xZenLabs/zen-fm/internal/state"
)

var fastPassword = auth.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16}

type testAPI struct {
	t       *testing.T
	now     *time.Time
	handler http.Handler
	server  *Server
	store   *state.Store
	files   *zenfiles.Root
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state", "zenfm.db"), state.Options{Now: func() time.Time { return now }, PasswordParams: fastPassword})
	if err != nil {
		t.Fatal(err)
	}
	root, err := zenfiles.Open(filepath.Join(dir), zenfiles.Options{MaxWriteBytes: 1 << 20, MaxContentBytes: 1 << 20})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	server, err := New(Config{Store: store, Files: root, Version: "test", Now: func() time.Time { return now }, PasswordParams: fastPassword})
	if err != nil {
		root.Close()
		store.Close()
		t.Fatal(err)
	}
	api := &testAPI{t: t, now: &now, handler: server.Handler(), server: server, store: store, files: root}
	t.Cleanup(func() { server.Close(); _ = root.Close(); _ = store.Close() })
	return api
}

func (a *testAPI) request(method, target string, body io.Reader, cookie *http.Cookie, csrf, bearer string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "192.0.2.1:1234"
	if body != nil && method != http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-ZenFM-CSRF", csrf)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeMap(t *testing.T, r *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %d %q: %v", r.Code, r.Body.String(), err)
	}
	return value
}

func responseCookie(t *testing.T, r *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, cookie := range r.Result().Cookies() {
		if cookie.Name == name && cookie.Value != "" && cookie.MaxAge >= 0 {
			copy := *cookie
			found = &copy
		}
	}
	if found == nil {
		t.Fatalf("%s cookie missing: %v", name, r.Header().Values("Set-Cookie"))
	}
	return found
}

func (a *testAPI) login(password string) (*http.Cookie, string, bool) {
	a.t.Helper()
	r := a.request(http.MethodPost, "/api/v1/session", strings.NewReader(`{"username":"koreader","password":"`+password+`"}`), nil, "", "")
	if r.Code != http.StatusOK {
		a.t.Fatalf("login: %d %s", r.Code, r.Body.String())
	}
	payload := decodeMap(a.t, r)
	return responseCookie(a.t, r, sessionCookie), payload["csrfToken"].(string), payload["setupRequired"].(bool)
}

func (a *testAPI) finishSetup() (*http.Cookie, string) {
	a.t.Helper()
	cookie, csrf, setup := a.login(state.SetupPassword)
	if !setup {
		a.t.Fatal("initial login did not require setup")
	}
	body := `{"currentPassword":"` + state.SetupPassword + `","newPassword":"a secure owner password"}`
	r := a.request(http.MethodPut, "/api/v1/owner/password", strings.NewReader(body), cookie, csrf, "")
	if r.Code != http.StatusOK {
		a.t.Fatalf("password change: %d %s", r.Code, r.Body.String())
	}
	payload := decodeMap(a.t, r)
	return responseCookie(a.t, r, sessionCookie), payload["csrfToken"].(string)
}

func TestHealthIsRedactedAndHardened(t *testing.T) {
	a := newTestAPI(t)
	r := a.request(http.MethodGet, "/healthz", nil, nil, "", "")
	if r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("response: %d %#v", r.Code, r.Header())
	}
	payload := decodeMap(t, r)
	if len(payload) != 2 || payload["status"] != "ok" || payload["version"] != "test" {
		t.Fatalf("health leaked data: %#v", payload)
	}
}

func TestWalkLimitMapsToBoundedResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/search", nil)
	mapError(recorder, request, zenfiles.ErrWalkLimit)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("walk limit status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestAuthenticatedRawResponsesBlockCrossOriginEmbedding(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	if _, err := a.files.Write("protected.js", strings.NewReader("globalThis.stolen = true"), false); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files/raw?path=/protected.js", nil, cookie, "", "")
	if response.Code != http.StatusOK || response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("raw response policy: %d %#v", response.Code, response.Header())
	}
}

func TestStreamingProgressRefreshesServerActivity(t *testing.T) {
	a := newTestAPI(t)
	initial := a.server.LastActivity()
	*a.now = a.now.Add(10 * time.Minute)
	writer := &progressResponseWriter{ResponseWriter: httptest.NewRecorder(), timeout: time.Second, touch: a.server.touch}
	if _, err := writer.Write([]byte("download progress")); err != nil {
		t.Fatal(err)
	}
	if !a.server.LastActivity().After(initial) {
		t.Fatal("download progress did not refresh activity")
	}
	writerActivity := a.server.LastActivity()
	*a.now = a.now.Add(10 * time.Minute)
	reader := &progressReader{reader: strings.NewReader("upload progress"), context: context.Background(), touch: a.server.touch}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	if !a.server.LastActivity().After(writerActivity) {
		t.Fatal("upload progress did not refresh activity")
	}
}

func TestHeavyOperationCeilingRejectsExcessWork(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	if _, err := a.files.Write("large.txt", strings.NewReader("data"), false); err != nil {
		t.Fatal(err)
	}
	for range cap(a.server.heavySlots) {
		a.server.heavySlots <- struct{}{}
	}
	defer func() {
		for range cap(a.server.heavySlots) {
			<-a.server.heavySlots
		}
	}()
	response := a.request(http.MethodGet, "/api/v1/files/checksum?path=%2Flarge.txt", nil, cookie, "", "")
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("heavy ceiling: %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
}

func TestSetupGatePasswordRotationAndSessionExpiry(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf, _ := a.login(state.SetupPassword)
	r := a.request(http.MethodGet, "/api/v1/files?path=%2F", nil, cookie, "", "")
	if r.Code != http.StatusForbidden {
		t.Fatalf("setup session accessed files: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodPut, "/api/v1/owner/password", strings.NewReader(`{"currentPassword":"wrong","newPassword":"a secure owner password"}`), cookie, csrf, "")
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: %d", r.Code)
	}
	cookie, csrf = a.finishSetup()
	r = a.request(http.MethodGet, "/api/v1/files?path=%2F", nil, cookie, "", "")
	if r.Code != http.StatusOK {
		t.Fatalf("files after setup: %d %s", r.Code, r.Body.String())
	}
	*a.now = a.now.Add(2*time.Hour + time.Second)
	r = a.request(http.MethodGet, "/api/v1/session", nil, cookie, "", "")
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("expired session accepted: %d", r.Code)
	}
}

func TestCSRFOriginAndPersonalTokenScope(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	r := a.request(http.MethodPost, "/api/v1/files/directory", strings.NewReader(`{"path":"/Books"}`), cookie, "", "")
	if r.Code != http.StatusForbidden {
		t.Fatalf("mutation without CSRF: %d", r.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/directory", strings.NewReader(`{"path":"/Books"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-ZenFM-CSRF", csrf)
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin mutation: %d", recorder.Code)
	}
	r = a.request(http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"phone","expiresInSeconds":3600}`), cookie, csrf, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", r.Code, r.Body.String())
	}
	token := decodeMap(t, r)["token"].(string)
	r = a.request(http.MethodPost, "/api/v1/files/directory", strings.NewReader(`{"path":"/Books"}`), nil, "", token)
	if r.Code != http.StatusCreated {
		t.Fatalf("bearer file mutation: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodPut, "/api/v1/settings", strings.NewReader(`{"theme":"dark"}`), nil, "", token)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("bearer changed settings: %d", r.Code)
	}
	r = a.request(http.MethodPut, "/api/v1/owner/password", strings.NewReader(`{}`), nil, "", token)
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("bearer accessed owner security: %d", r.Code)
	}
}

func TestFileWorkflowAndTraversalRegression(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	if r := a.request(http.MethodPost, "/api/v1/files/directory", strings.NewReader(`{"path":"/Books"}`), cookie, csrf, ""); r.Code != http.StatusCreated {
		t.Fatalf("mkdir: %d %s", r.Code, r.Body.String())
	}
	fileURL := "/api/v1/files/content?" + url.Values{"path": {"/Books/hello.txt"}}.Encode()
	r := a.request(http.MethodPut, fileURL, strings.NewReader("zen"), cookie, csrf, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("write: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, "/api/v1/files?"+url.Values{"path": {"/Books"}}.Encode(), nil, cookie, "", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "hello.txt") || strings.Contains(r.Body.String(), `"regular"`) {
		t.Fatalf("list: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, "/api/v1/search?"+url.Values{"path": {"/"}, "q": {"hello"}}.Encode(), nil, cookie, "", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "hello.txt") {
		t.Fatalf("search: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, "/api/v1/files/checksum?"+url.Values{"path": {"/Books/hello.txt"}, "algorithm": {"sha512"}}.Encode(), nil, cookie, "", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"algorithm":"sha512"`) {
		t.Fatalf("checksum: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, "/api/v1/files/raw?"+url.Values{"path": {"/Books/hello.txt"}}.Encode(), nil, cookie, "", "")
	if r.Code != http.StatusOK || r.Body.String() != "zen" {
		t.Fatalf("raw: %d %q", r.Code, r.Body.String())
	}
	r = a.request(http.MethodPost, "/api/v1/files/copy", strings.NewReader(`{"source":"/Books/hello.txt","destination":"/Books/copy.txt"}`), cookie, csrf, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("copy: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodPost, "/api/v1/files/move", strings.NewReader(`{"source":"/Books/copy.txt","destination":"/Books/moved.txt"}`), cookie, csrf, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodDelete, "/api/v1/files?"+url.Values{"path": {"/Books"}, "recursive": {"true"}}.Encode(), nil, cookie, csrf, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, "/api/v1/files/raw?"+url.Values{"path": {"/../etc/passwd"}}.Encode(), nil, cookie, "", "")
	if r.Code != http.StatusBadRequest {
		t.Fatalf("traversal response: %d %s", r.Code, r.Body.String())
	}
}

func TestTransferRequiresAndHonorsExplicitDirectoryReplacement(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	for _, name := range []string{"incoming", "installed"} {
		if _, err := a.files.Mkdir(name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.files.Write("incoming/VERSION", strings.NewReader("new"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("installed/VERSION", strings.NewReader("old"), false); err != nil {
		t.Fatal(err)
	}

	request := `{"source":"/incoming","destination":"/installed"}`
	if response := a.request(http.MethodPost, "/api/v1/files/copy", strings.NewReader(request), cookie, csrf, ""); response.Code != http.StatusConflict {
		t.Fatalf("implicit replacement: %d %s", response.Code, response.Body.String())
	}
	request = `{"source":"/incoming","destination":"/installed","overwrite":true}`
	if response := a.request(http.MethodPost, "/api/v1/files/copy", strings.NewReader(request), cookie, csrf, ""); response.Code != http.StatusNoContent {
		t.Fatalf("explicit replacement: %d %s", response.Code, response.Body.String())
	}
	if data, err := a.files.ReadContent("installed/VERSION"); err != nil || string(data) != "new" {
		t.Fatalf("installed replacement = %q, %v", data, err)
	}
}

func TestHiddenOverrideAndExactTextSource(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	if _, err := a.files.Write(".hidden.txt", strings.NewReader("hidden"), false); err != nil {
		t.Fatal(err)
	}
	html := `<p>exact</p><script>sourceMustRemain()</script>`
	if _, err := a.files.Write("page.html", strings.NewReader(html), false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("image.svg", strings.NewReader(`<svg/>`), false); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files?path=%2F&hidden=false", nil, cookie, "", "")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), ".hidden.txt") {
		t.Fatalf("hidden=false listing: %d %s", response.Code, response.Body.String())
	}
	response = a.request(http.MethodGet, "/api/v1/files?path=%2F&hidden=true", nil, cookie, "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), ".hidden.txt") {
		t.Fatalf("hidden=true listing: %d %s", response.Code, response.Body.String())
	}
	response = a.request(http.MethodGet, "/api/v1/search?path=%2F&q=hidden&hidden=true", nil, cookie, "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), ".hidden.txt") {
		t.Fatalf("hidden search: %d %s", response.Code, response.Body.String())
	}
	response = a.request(http.MethodGet, "/api/v1/files/content?path=%2Fpage.html", nil, cookie, "", "")
	if response.Code != http.StatusOK || response.Body.String() != html || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("exact source: %d %q %#v", response.Code, response.Body.String(), response.Header())
	}
	response = a.request(http.MethodGet, "/api/v1/files/content?path=%2Fimage.svg", nil, cookie, "", "")
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("active SVG source accepted: %d", response.Code)
	}
	if _, err := a.files.Write("invalid.txt", bytes.NewReader([]byte{0xff, 0xfe}), false); err != nil {
		t.Fatal(err)
	}
	response = a.request(http.MethodGet, "/api/v1/files/content?path=%2Finvalid.txt", nil, cookie, "", "")
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("invalid UTF-8 source accepted: %d", response.Code)
	}
}

func TestPublicDirectoryNavigationCannotEscapeThroughSymlink(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	for _, directory := range []string{"shared", "shared/nested", "private"} {
		if _, err := a.files.Mkdir(directory); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.files.Write("shared/nested/book.txt", strings.NewReader("book"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.files.Write("private/secret.txt", strings.NewReader("outside-secret"), false); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../private", filepath.Join(a.files.Name(), "shared", "link")); err != nil {
		t.Fatal(err)
	}
	created := a.request(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/shared"}`), cookie, csrf, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create directory share: %d %s", created.Code, created.Body.String())
	}
	secret := strings.TrimPrefix(decodeMap(t, created)["url"].(string), "/s/")
	publicPath := "/api/v1/public/shares/" + secret
	listing := a.request(http.MethodGet, publicPath+"?path=%2Fnested", nil, nil, "", "")
	if listing.Code != http.StatusOK || !strings.Contains(listing.Body.String(), `"path":"/nested/book.txt"`) || strings.Contains(listing.Body.String(), a.files.Name()) {
		t.Fatalf("nested listing: %d %s", listing.Code, listing.Body.String())
	}
	raw := a.request(http.MethodGet, publicPath+"/raw?path=%2Fnested%2Fbook.txt", nil, nil, "", "")
	if raw.Code != http.StatusOK || raw.Body.String() != "book" {
		t.Fatalf("nested raw: %d %q", raw.Code, raw.Body.String())
	}
	escape := a.request(http.MethodGet, publicPath+"/raw?path=%2Flink%2Fsecret.txt", nil, nil, "", "")
	if escape.Code == http.StatusOK || strings.Contains(escape.Body.String(), "outside-secret") {
		t.Fatalf("share escaped through symlink: %d %q", escape.Code, escape.Body.String())
	}
}

func TestNormalModeStateExclusionCannotBeBypassedThroughSymlink(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	if err := os.Symlink("state", filepath.Join(a.files.Name(), "state-alias")); err != nil {
		t.Fatal(err)
	}
	response := a.request(http.MethodGet, "/api/v1/files/raw?path=%2Fstate-alias%2Fzenfm.db", nil, cookie, "", "")
	if response.Code == http.StatusOK {
		t.Fatal("private database was exposed through an intermediate symlink")
	}
}

func TestPublicRootSharePreservesPrivateStateExclusion(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	created := a.request(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/","name":"root"}`), cookie, csrf, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create root share: %d %s", created.Code, created.Body.String())
	}
	secret := strings.TrimPrefix(decodeMap(t, created)["url"].(string), "/s/")
	publicPath := "/api/v1/public/shares/" + secret
	listing := a.request(http.MethodGet, publicPath, nil, nil, "", "")
	if listing.Code != http.StatusOK || strings.Contains(listing.Body.String(), `"name":"state"`) || strings.Contains(listing.Body.String(), "zenfm.db") {
		t.Fatalf("root share exposed state listing: %d %s", listing.Code, listing.Body.String())
	}
	raw := a.request(http.MethodGet, publicPath+"/raw?path=%2Fstate%2Fzenfm.db", nil, nil, "", "")
	if raw.Code == http.StatusOK {
		t.Fatal("root share exposed private database content")
	}
}

func TestPasswordProtectedShareAndRevocation(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	fileURL := "/api/v1/files/content?" + url.Values{"path": {"/shared.txt"}}.Encode()
	if r := a.request(http.MethodPut, fileURL, strings.NewReader("public data"), cookie, csrf, ""); r.Code != http.StatusCreated {
		t.Fatalf("write: %d %s", r.Code, r.Body.String())
	}
	r := a.request(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/shared.txt","password":"share password"}`), cookie, csrf, "")
	if r.Code != http.StatusCreated {
		t.Fatalf("create share: %d %s", r.Code, r.Body.String())
	}
	payload := decodeMap(t, r)
	encodedURL := payload["url"].(string)
	secret := strings.TrimPrefix(encodedURL, "/s/")
	if secret == encodedURL || secret == "" || strings.Contains(r.Body.String(), "passwordHash") || strings.Contains(r.Body.String(), "secretDigest") {
		t.Fatalf("unsafe share response: %s", r.Body.String())
	}
	id := payload["id"].(string)
	publicPath := "/api/v1/public/shares/" + secret
	r = a.request(http.MethodGet, publicPath, nil, nil, "", "")
	challenge := decodeMap(t, r)
	if r.Code != http.StatusOK || challenge["passwordRequired"] != true || challenge["path"] != "/" {
		t.Fatalf("challenge: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodPost, publicPath, strings.NewReader(`{"password":"wrong"}`), nil, "", "")
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("wrong share password: %d", r.Code)
	}
	r = a.request(http.MethodPost, publicPath, strings.NewReader(`{"password":"share password"}`), nil, "", "")
	if r.Code != http.StatusOK {
		t.Fatalf("unlock: %d %s", r.Code, r.Body.String())
	}
	unlocked := decodeMap(t, r)
	entry := unlocked["entry"].(map[string]any)
	if entry["path"] != "/" {
		t.Fatalf("public capability leaked owner path: %s", r.Body.String())
	}
	publicCookie := responseCookie(t, r, publicShareCookie)
	r = a.request(http.MethodGet, publicPath+"/raw", nil, publicCookie, "", "")
	if r.Code != http.StatusOK || r.Body.String() != "public data" {
		t.Fatalf("public raw: %d %q", r.Code, r.Body.String())
	}
	r = a.request(http.MethodDelete, "/api/v1/shares/"+id, nil, cookie, csrf, "")
	if r.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.Code, r.Body.String())
	}
	r = a.request(http.MethodGet, publicPath+"/raw", nil, publicCookie, "", "")
	if r.Code != http.StatusNotFound {
		t.Fatalf("revoked share remained usable: %d", r.Code)
	}
}

func TestSharePasswordThrottleAppliesAcrossClientAddresses(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	if _, err := a.files.Write("shared.txt", strings.NewReader("content"), false); err != nil {
		t.Fatal(err)
	}
	created := a.request(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/shared.txt","password":"correct password"}`), cookie, csrf, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create share: %d %s", created.Code, created.Body.String())
	}
	secret := strings.TrimPrefix(decodeMap(t, created)["url"].(string), "/s/")
	publicPath := "/api/v1/public/shares/" + secret
	for index := range 8 {
		request := httptest.NewRequest(http.MethodPost, publicPath, strings.NewReader(`{"password":"wrong"}`))
		request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", index+1)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		a.handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, publicPath, strings.NewReader(`{"password":"correct password"}`))
	request.RemoteAddr = "198.51.100.20:1234"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("distributed share throttle bypassed: %d %s", response.Code, response.Body.String())
	}
}

func TestSetupPasswordMustActuallyChangeAndCountsUnicodeCharacters(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf, _ := a.login(state.SetupPassword)
	same := `{"currentPassword":"` + state.SetupPassword + `","newPassword":"` + state.SetupPassword + `"}`
	response := a.request(http.MethodPut, "/api/v1/owner/password", strings.NewReader(same), cookie, csrf, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("setup credential retained: %d %s", response.Code, response.Body.String())
	}
	shortUnicode := `{"currentPassword":"` + state.SetupPassword + `","newPassword":"` + strings.Repeat("🔒", 11) + `"}`
	response = a.request(http.MethodPut, "/api/v1/owner/password", strings.NewReader(shortUnicode), cookie, csrf, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("11-character Unicode password accepted: %d", response.Code)
	}
}

func TestGHSA_43mmUniformLoginFailureAndDistributedAccountLimit(t *testing.T) {
	a := newTestAPI(t)
	wrongUser := a.request(http.MethodPost, "/api/v1/session", strings.NewReader(`{"username":"someone","password":"koreader123456789"}`), nil, "", "")
	a.loginLimiterForTestReset()
	wrongPassword := a.request(http.MethodPost, "/api/v1/session", strings.NewReader(`{"username":"koreader","password":"incorrect"}`), nil, "", "")
	if wrongUser.Code != http.StatusUnauthorized || wrongPassword.Code != http.StatusUnauthorized || wrongUser.Body.String() != wrongPassword.Body.String() {
		t.Fatalf("non-uniform failures: %d %q / %d %q", wrongUser.Code, wrongUser.Body.String(), wrongPassword.Code, wrongPassword.Body.String())
	}
	a = newTestAPI(t)
	for index := range 5 {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader(`{"username":"koreader","password":"wrong"}`))
		request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", index+1)
		response := httptest.NewRecorder()
		a.handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d", index, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader(`{"username":"koreader","password":"koreader123456789"}`))
	request.RemoteAddr = "198.51.100.8:1234"
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("distributed account throttle bypassed: %d", response.Code)
	}
}

func (a *testAPI) loginLimiterForTestReset() {
	a.server.loginLimiter = newAttemptLimiter(5, time.Minute, func() time.Time { return *a.now })
}

func TestGHSA_4mh3RepeatedLeadingSlashRejected(t *testing.T) {
	a := newTestAPI(t)
	cookie, _ := a.finishSetup()
	response := a.request(http.MethodGet, "/api/v1/files/raw?"+url.Values{"path": {"//etc/passwd"}}.Encode(), nil, cookie, "", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("repeated leading slash accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestGHSA_pp88MoveRevokesDescendantShares(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	_, _ = a.files.Mkdir("folder")
	_, _ = a.files.Write("folder/book.txt", strings.NewReader("book"), false)
	created := a.request(http.MethodPost, "/api/v1/shares", strings.NewReader(`{"path":"/folder/book.txt"}`), cookie, csrf, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("share: %d %s", created.Code, created.Body.String())
	}
	secret := strings.TrimPrefix(decodeMap(t, created)["url"].(string), "/s/")
	moved := a.request(http.MethodPost, "/api/v1/files/move", strings.NewReader(`{"source":"/folder","destination":"/renamed"}`), cookie, csrf, "")
	if moved.Code != http.StatusNoContent {
		t.Fatalf("move: %d %s", moved.Code, moved.Body.String())
	}
	public := a.request(http.MethodGet, "/api/v1/public/shares/"+secret, nil, nil, "", "")
	if public.Code != http.StatusNotFound {
		t.Fatalf("share survived move: %d %s", public.Code, public.Body.String())
	}
}

func TestStaticCSPAndCaching(t *testing.T) {
	a := newTestAPI(t)
	server, err := New(Config{Store: a.store, Files: a.files, StaticFS: fstest.MapFS{
		"index.html":               &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")},
		"assets/app-abcdef12.js":   &fstest.MapFile{Data: []byte("ok")},
		"assets/index-BZIWcyhl.js": &fstest.MapFile{Data: []byte("vite")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	r := httptest.NewRecorder()
	server.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/", nil))
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "csp-nonce") || strings.Contains(r.Header().Get("Content-Security-Policy"), "unsafe-inline") || !strings.Contains(r.Header().Get("Content-Security-Policy"), "frame-src 'self' blob:") {
		t.Fatalf("index security: %d %#v %s", r.Code, r.Header(), r.Body.String())
	}
	r = httptest.NewRecorder()
	server.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/assets/app-abcdef12.js", nil))
	if r.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache: %q", r.Header().Get("Cache-Control"))
	}
	r = httptest.NewRecorder()
	server.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/assets/index-BZIWcyhl.js", nil))
	if r.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("Vite asset cache: %q", r.Header().Get("Cache-Control"))
	}
}
