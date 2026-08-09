package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func directUploadRequest(a *testAPI, cookie *http.Cookie, csrf, target, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/api/v1/files/content?"+url.Values{"path": {target}}.Encode(), strings.NewReader(body))
	request.AddCookie(cookie)
	request.Header.Set("X-ZenFM-CSRF", csrf)
	request.Header.Set("Content-Type", "application/octet-stream")
	return request
}

func TestDirectUploadConflictIsExplicitAndAtomic(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	if _, err := a.files.Write("existing.txt", strings.NewReader("old"), false); err != nil {
		t.Fatal(err)
	}
	request := directUploadRequest(a, cookie, csrf, "/existing.txt", "new")
	request.Header.Set("If-None-Match", "*")
	if response := serveTestRequest(a, request); response.Code != http.StatusConflict {
		t.Fatalf("initial-only conflict: %d %s", response.Code, response.Body.String())
	}
	data, _ := a.files.ReadContent("existing.txt")
	if string(data) != "old" {
		t.Fatalf("conflict modified destination: %q", data)
	}
	request = directUploadRequest(a, cookie, csrf, "/existing.txt", "new")
	if response := serveTestRequest(a, request); response.Code != http.StatusNoContent {
		t.Fatalf("explicit replacement request: %d %s", response.Code, response.Body.String())
	}
	data, _ = a.files.ReadContent("existing.txt")
	if string(data) != "new" {
		t.Fatalf("replacement = %q", data)
	}
}

func TestDirectUploadLimitConcurrencyAndCancellation(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	tooLarge := directUploadRequest(a, cookie, csrf, "/large.bin", "")
	tooLarge.ContentLength = a.server.uploads.maxLength + 1
	if response := serveTestRequest(a, tooLarge); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared oversize: %d %s", response.Code, response.Body.String())
	}
	if _, err := a.files.Entry("large.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("oversize target exists: %v", err)
	}
	for range cap(a.server.uploads.slots) {
		a.server.uploads.slots <- struct{}{}
	}
	blocked := directUploadRequest(a, cookie, csrf, "/blocked.bin", "data")
	if response := serveTestRequest(a, blocked); response.Code != http.StatusTooManyRequests {
		t.Fatalf("concurrency ceiling: %d", response.Code)
	}
	for range cap(a.server.uploads.slots) {
		<-a.server.uploads.slots
	}
	cancelled := directUploadRequest(a, cookie, csrf, "/cancelled.bin", "data")
	ctx, cancel := context.WithCancel(cancelled.Context())
	cancel()
	cancelled = cancelled.WithContext(ctx)
	if response := serveTestRequest(a, cancelled); response.Code != http.StatusRequestTimeout {
		t.Fatalf("cancelled upload: %d %s", response.Code, response.Body.String())
	}
	if _, err := a.files.Entry("cancelled.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelled target exists: %v", err)
	}
}
