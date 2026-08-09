package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xZenLabs/zen-fm/internal/state"
)

func serveTestRequest(a *testAPI, request *http.Request) *httptest.ResponseRecorder {
	a.t.Helper()
	request.RemoteAddr = "192.0.2.1:1234"
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, request)
	return recorder
}

func tusCreate(t *testing.T, a *testAPI, cookie *http.Cookie, csrf, target string, length int, overwrite bool) *httptest.ResponseRecorder {
	t.Helper()
	metadata := "path " + base64.StdEncoding.EncodeToString([]byte(target)) + ",overwrite " + base64.StdEncoding.EncodeToString([]byte(map[bool]string{true: "true", false: "false"}[overwrite]))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", nil)
	request.AddCookie(cookie)
	request.Header.Set("X-ZenFM-CSRF", csrf)
	request.Header.Set("Tus-Resumable", tusVersion)
	request.Header.Set("Upload-Length", strconv.Itoa(length))
	request.Header.Set("Upload-Metadata", metadata)
	return serveTestRequest(a, request)
}

func tusPatch(t *testing.T, a *testAPI, cookie *http.Cookie, csrf, location string, offset int, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, location, strings.NewReader(body))
	request.AddCookie(cookie)
	request.Header.Set("X-ZenFM-CSRF", csrf)
	request.Header.Set("Tus-Resumable", tusVersion)
	request.Header.Set("Upload-Offset", strconv.Itoa(offset))
	request.Header.Set("Content-Type", "application/offset+octet-stream")
	return serveTestRequest(a, request)
}

func TestGHSA_ffv3DeclaredLengthOffsetAndAtomicTUSFlow(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	created := tusCreate(t, a, cookie, csrf, "/resume.txt", 3, false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	location := created.Header().Get("Location")
	if location == "" || created.Header().Get("Tus-Resumable") != tusVersion {
		t.Fatalf("TUS headers: %#v", created.Header())
	}
	tooLong := tusPatch(t, a, cookie, csrf, location, 0, "toolong")
	if tooLong.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize chunk: %d %s", tooLong.Code, tooLong.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, location, nil)
	head.AddCookie(cookie)
	head.Header.Set("Tus-Resumable", tusVersion)
	headResponse := serveTestRequest(a, head)
	if headResponse.Code != http.StatusOK || headResponse.Header().Get("Upload-Offset") != "0" {
		t.Fatalf("offset changed after rejection: %d %#v", headResponse.Code, headResponse.Header())
	}
	first := tusPatch(t, a, cookie, csrf, location, 0, "ze")
	if first.Code != http.StatusNoContent || first.Header().Get("Upload-Offset") != "2" {
		t.Fatalf("first chunk: %d %#v %s", first.Code, first.Header(), first.Body.String())
	}
	wrong := tusPatch(t, a, cookie, csrf, location, 0, "n")
	if wrong.Code != http.StatusConflict || wrong.Header().Get("Upload-Offset") != "2" {
		t.Fatalf("offset conflict: %d %#v", wrong.Code, wrong.Header())
	}
	last := tusPatch(t, a, cookie, csrf, location, 2, "n")
	if last.Code != http.StatusNoContent || last.Header().Get("Upload-Offset") != "3" {
		t.Fatalf("last chunk: %d %#v %s", last.Code, last.Header(), last.Body.String())
	}
	data, err := a.files.ReadContent("/resume.txt")
	if err != nil || string(data) != "zen" {
		t.Fatalf("final file: %q %v", data, err)
	}
	head = httptest.NewRequest(http.MethodHead, location, nil)
	head.AddCookie(cookie)
	head.Header.Set("Tus-Resumable", tusVersion)
	if response := serveTestRequest(a, head); response.Code != http.StatusNotFound {
		t.Fatalf("completed metadata survived: %d", response.Code)
	}
}

func TestGHSA_79pfUploadDeletionRequiresCSRFAndActiveLimit(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	a.server.uploads.maxActive = 1
	created := tusCreate(t, a, cookie, csrf, "/one.bin", 10, false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if second := tusCreate(t, a, cookie, csrf, "/two.bin", 10, false); second.Code != http.StatusTooManyRequests {
		t.Fatalf("active limit: %d %s", second.Code, second.Body.String())
	}
	location := created.Header().Get("Location")
	request := httptest.NewRequest(http.MethodDelete, location, nil)
	request.AddCookie(cookie)
	request.Header.Set("Tus-Resumable", tusVersion)
	if response := serveTestRequest(a, request); response.Code != http.StatusForbidden {
		t.Fatalf("delete without CSRF: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodDelete, location, nil)
	request.AddCookie(cookie)
	request.Header.Set("Tus-Resumable", tusVersion)
	request.Header.Set("X-ZenFM-CSRF", csrf)
	if response := serveTestRequest(a, request); response.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if len(a.server.uploads.locks) != 0 {
		t.Fatalf("per-upload lock leaked: %d", len(a.server.uploads.locks))
	}
}

func TestTUSZeroLengthFinalizesAtCreation(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	created := tusCreate(t, a, cookie, csrf, "/empty.txt", 0, false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create zero upload: %d %s", created.Code, created.Body.String())
	}
	data, err := a.files.ReadContent("empty.txt")
	if err != nil || len(data) != 0 {
		t.Fatalf("zero upload was not finalized: %q %v", data, err)
	}
	if uploads, err := a.store.Uploads(); err != nil || len(uploads) != 0 {
		t.Fatalf("zero upload metadata remained: %+v %v", uploads, err)
	}
}

func TestTUSExplicitOverwriteContract(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	if _, err := a.files.Write("existing.txt", strings.NewReader("old"), false); err != nil {
		t.Fatal(err)
	}
	if response := tusCreate(t, a, cookie, csrf, "/existing.txt", 3, false); response.Code != http.StatusConflict {
		t.Fatalf("silent overwrite allowed: %d", response.Code)
	}
	created := tusCreate(t, a, cookie, csrf, "/existing.txt", 3, true)
	if created.Code != http.StatusCreated {
		t.Fatalf("explicit replace create: %d %s", created.Code, created.Body.String())
	}
	if response := tusPatch(t, a, cookie, csrf, created.Header().Get("Location"), 0, "new"); response.Code != http.StatusNoContent {
		t.Fatalf("explicit replace patch: %d %s", response.Code, response.Body.String())
	}
	data, _ := a.files.ReadContent("existing.txt")
	if string(data) != "new" {
		t.Fatalf("replace content = %q", data)
	}
}

func TestTUSFinalizationConflictKeepsResumableOffset(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	created := tusCreate(t, a, cookie, csrf, "/race.txt", 3, false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if _, err := a.files.Write("race.txt", strings.NewReader("other"), false); err != nil {
		t.Fatal(err)
	}
	location := created.Header().Get("Location")
	response := tusPatch(t, a, cookie, csrf, location, 0, "zen")
	if response.Code != http.StatusConflict {
		t.Fatalf("finalization conflict: %d %s", response.Code, response.Body.String())
	}
	head := httptest.NewRequest(http.MethodHead, location, nil)
	head.AddCookie(cookie)
	head.Header.Set("Tus-Resumable", tusVersion)
	if status := serveTestRequest(a, head); status.Header().Get("Upload-Offset") != "0" {
		t.Fatalf("unpublished full offset was exposed: %#v", status.Header())
	}
	if err := a.files.Delete("race.txt", false); err != nil {
		t.Fatal(err)
	}
	response = tusPatch(t, a, cookie, csrf, location, 0, "zen")
	if response.Code != http.StatusNoContent {
		t.Fatalf("resumed final chunk: %d %s", response.Code, response.Body.String())
	}
	data, _ := a.files.ReadContent("race.txt")
	if string(data) != "zen" {
		t.Fatalf("retried file = %q", data)
	}
}

func TestTUSFinalizationStopsWhenFinalPATCHIsCanceled(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	payload := bytes.Repeat([]byte("z"), 512<<10)
	created := tusCreate(t, a, cookie, csrf, "/cancel-final.txt", len(payload), false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.server.uploads.finalizeReader = func(reader io.Reader) io.Reader {
		return &cancelAfterFirstFinalizeRead{reader: reader, cancel: cancel}
	}
	request := httptest.NewRequest(http.MethodPatch, created.Header().Get("Location"), bytes.NewReader(payload)).WithContext(ctx)
	request.AddCookie(cookie)
	request.Header.Set("X-ZenFM-CSRF", csrf)
	request.Header.Set("Tus-Resumable", tusVersion)
	request.Header.Set("Upload-Offset", "0")
	request.Header.Set("Content-Type", "application/offset+octet-stream")
	response := serveTestRequest(a, request)
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled finalization: %d %s", response.Code, response.Body.String())
	}
	if _, err := a.files.Entry("cancel-final.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("canceled finalization published destination: %v", err)
	}
	head := httptest.NewRequest(http.MethodHead, created.Header().Get("Location"), nil)
	head.AddCookie(cookie)
	head.Header.Set("Tus-Resumable", tusVersion)
	status := serveTestRequest(a, head)
	if status.Code != http.StatusOK || status.Header().Get("Upload-Offset") != "0" {
		t.Fatalf("canceled finalization changed resumable offset: %d %#v", status.Code, status.Header())
	}
	id := strings.TrimPrefix(created.Header().Get("Location"), "/api/v1/uploads/")
	partial, err := a.server.uploads.root.Lstat(partialName(id))
	if err != nil {
		t.Fatalf("canceled finalization removed partial: %v", err)
	}
	if partial.Size() != 0 {
		t.Fatalf("canceled finalization partial size = %d, want 0", partial.Size())
	}
}

type cancelAfterFirstFinalizeRead struct {
	reader io.Reader
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAfterFirstFinalizeRead) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && !r.done {
		r.done = true
		r.cancel()
	}
	return n, err
}

func TestGHSA_m9f5ExpiredUploadCleanupIsRooted(t *testing.T) {
	a := newTestAPI(t)
	cookie, csrf := a.finishSetup()
	created := tusCreate(t, a, cookie, csrf, "/expired.bin", 10, false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	id := strings.TrimPrefix(created.Header().Get("Location"), "/api/v1/uploads/")
	*a.now = a.now.Add(defaultUploadExpiry + time.Second)
	if err := a.server.uploads.pruneExpired(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Upload(id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("metadata survived: %v", err)
	}
	if _, err := a.server.uploads.root.Lstat(partialName(id)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial survived: %v", err)
	}
}
