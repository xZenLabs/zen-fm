package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunVersionAndUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != version {
		t.Fatalf("version: %d %q %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"unknown"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("unknown: %d %q", code, stderr.String())
	}
}

func TestServeRejectsNonPositiveSessionLifetimes(t *testing.T) {
	for _, arguments := range [][]string{
		{"serve", "--session-idle", "0s"},
		{"serve", "--session-absolute", "-1s"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(arguments, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "invalid serve arguments") {
			t.Fatalf("run(%v): %d %q", arguments, code, stderr.String())
		}
	}
}

func TestServeDebugFlagControlsDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		wantDebug bool
	}{
		{name: "disabled"},
		{name: "enabled", arguments: []string{"--debug"}, wantDebug: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			missingRoot := filepath.Join(t.TempDir(), "missing")
			arguments := append([]string{"serve", "--root", missingRoot, "--data-dir", t.TempDir()}, test.arguments...)
			if code := run(arguments, &stdout, &stderr); code != 1 {
				t.Fatalf("run: %d %q", code, stderr.String())
			}
			if got := strings.Contains(stderr.String(), "debug: server setup started"); got != test.wantDebug {
				t.Fatalf("debug output present = %v, want %v: %q", got, test.wantDebug, stderr.String())
			}
		})
	}
}

func TestListenNetworkUsesIPv4ForIPv4Addresses(t *testing.T) {
	for address, want := range map[string]string{
		"0.0.0.0:8443":      "tcp4",
		"192.168.1.50:8443": "tcp4",
		"[::]:8443":         "tcp",
		"example.test:8443": "tcp",
	} {
		if got := listenNetwork(address); got != want {
			t.Errorf("listenNetwork(%q) = %q, want %q", address, got, want)
		}
	}
}

type pipeAddress string

func (a pipeAddress) Network() string { return "pipe" }
func (a pipeAddress) String() string  { return string(a) }

type pipeListener struct {
	addr        net.Addr
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newPipeListener(address string) *pipeListener {
	return &pipeListener{addr: pipeAddress(address), connections: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

func (l *pipeListener) Dial() (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.connections <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

func TestHTTPOnHTTPSPortRedirectsToSameLocation(t *testing.T) {
	listener := newPipeListener("kindle.local:53241")
	_, plainListener, dispatcher := splitProtocols(listener, nil)
	defer dispatcher.Close()
	redirectServer := &http.Server{Handler: httpsRedirectHandler()}
	defer redirectServer.Close()
	go func() { _ = redirectServer.Serve(plainListener) }()

	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return listener.Dial() }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get("http://kindle.local:53241/files/a%20b?view=grid")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	want := "https://kindle.local:53241/files/a%20b?view=grid"
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != want {
		t.Fatalf("redirect = %d %q, want %d %q", response.StatusCode, response.Header.Get("Location"), http.StatusTemporaryRedirect, want)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", response.Header.Get("Cache-Control"))
	}
}

func TestProtocolDispatcherPreservesTLSHandshakeBytes(t *testing.T) {
	listener := newPipeListener("kindle.local:53241")
	var logs bytes.Buffer
	tlsListener, _, dispatcher := splitProtocols(listener, log.New(&logs, "", 0))
	defer dispatcher.Close()
	client, err := listener.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	want := []byte{0x16, 0x03, 0x03, 0, 1, 0}
	if _, err := client.Write(want); err != nil {
		t.Fatal(err)
	}
	connection, err := tlsListener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(connection, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("TLS bytes = %x, want %x", got, want)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful protocol detection produced diagnostics: %q", logs.String())
	}
}

func TestRequestDiagnosticsReportFailuresWithoutSecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unsupported", http.StatusUnsupportedMediaType)
	}), logger)
	request, err := http.NewRequest(http.MethodGet, "http://zenfm.test/api/v1/public/shares/share-secret/raw?path=/private.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.RemoteAddr = "192.0.2.10:4242"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	got := logs.String()
	for _, expected := range []string{"remote=192.0.2.10", "method=GET", "path=/api/v1/public/shares/[redacted]", "status=415"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("request diagnostics %q missing %q", got, expected)
		}
	}
	if strings.Contains(got, "share-secret") || strings.Contains(got, "private.txt") {
		t.Fatalf("request diagnostics exposed a secret: %q", got)
	}
	for path, want := range map[string]string{
		"/s/browser-secret/Nested":              "/s/[redacted]",
		"/share/legacy-secret/Nested":           "/share/[redacted]",
		"/api/v1/files/archive/download-ticket": "/api/v1/files/archive/[redacted]",
		"/api/v1/uploads/upload-secret":         "/api/v1/uploads/[redacted]",
		"/api/v1/files/content":                 "/api/v1/files/content",
	} {
		if got := diagnosticPath(path); got != want {
			t.Errorf("diagnosticPath(%q) = %q, want %q", path, got, want)
		}
	}
	logs.Reset()
	request, err = http.NewRequest(http.MethodGet, "http://zenfm.test/api/v1/files", nil)
	if err != nil {
		t.Fatal(err)
	}
	successHandler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), logger)
	successHandler.ServeHTTP(httptest.NewRecorder(), request)
	if logs.Len() != 0 {
		t.Fatalf("successful API request produced diagnostics: %q", logs.String())
	}

	request, err = http.NewRequest(http.MethodGet, "http://zenfm.test/assets/app.js", nil)
	if err != nil {
		t.Fatal(err)
	}
	successHandler.ServeHTTP(httptest.NewRecorder(), request)
	if logs.Len() != 0 {
		t.Fatalf("successful static request produced diagnostics: %q", logs.String())
	}
}

func TestResetLoginCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dataDir := filepath.Join(t.TempDir(), "state")
	if code := run([]string{"reset-login", "--data-dir", dataDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("reset: %d %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "setup-only") {
		t.Fatalf("output: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"reset-login", "--data-dir", dataDir, "--mode-less-filesystem"}, &stdout, &stderr); code != 0 {
		t.Fatalf("mode-less reset: %d %q", code, stderr.String())
	}
}

type fakeActivity struct{ value atomic.Int64 }

func (f *fakeActivity) LastActivity() time.Time { return time.Unix(0, f.value.Load()) }

func TestWatchIdleStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	activity := &fakeActivity{}
	activity.value.Store(time.Now().Add(-time.Second).UnixNano())
	stopped := make(chan struct{})
	var called atomic.Bool
	go watchIdle(ctx, func() { called.Store(true); close(stopped) }, activity, 50*time.Millisecond)
	select {
	case <-stopped:
		if !called.Load() {
			t.Fatal("stop not called")
		}
	case <-time.After(time.Second):
		t.Fatal("idle watcher did not stop")
	}
}

func TestWatchIdleStaysAliveWhileProgressContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	activity := &fakeActivity{}
	activity.value.Store(time.Now().UnixNano())
	stopped := make(chan struct{})
	go watchIdle(ctx, func() { close(stopped) }, activity, 80*time.Millisecond)
	for range 5 {
		time.Sleep(30 * time.Millisecond)
		activity.value.Store(time.Now().UnixNano())
		select {
		case <-stopped:
			t.Fatal("active transfer triggered auto-stop")
		default:
		}
	}
	select {
	case <-stopped:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("idle watcher did not stop after progress ended")
	}
}
