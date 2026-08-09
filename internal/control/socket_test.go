package control

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestControlProtocolAndCleanup(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "zenfm-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "zenfm.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	server := &Server{Path: path, URL: "https://127.0.0.1:8443", Fingerprint: "AABB", Stop: func() { once.Do(cancel) }}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	for deadline := time.Now().Add(time.Second); ; {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		select {
		case err := <-done:
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("sandbox does not permit Unix sockets: %v", err)
			}
			t.Fatalf("control socket failed to start: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if info, _ := os.Lstat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}
	if got := command(t, path, "status\n"); got != "ok running https://127.0.0.1:8443 AABB\n" {
		t.Fatalf("status = %q", got)
	}
	second := &Server{Path: path, Stop: func() {}}
	if err := second.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("second server replaced active socket: %v", err)
	}
	if got := command(t, path, "status\n"); got != "ok running https://127.0.0.1:8443 AABB\n" {
		t.Fatalf("first server identity lost: %q", got)
	}
	if got := command(t, path, "unknown\n"); got != "error unknown-command\n" {
		t.Fatalf("unknown = %q", got)
	}
	if got := command(t, path, "stop\n"); got != "ok stopping\n" {
		t.Fatalf("stop = %q", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control server did not stop")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket was not cleaned: %v", err)
	}
}

func TestControlRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(path, []byte("owner data"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{Path: path, Stop: func() {}}
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("regular file was replaced")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "owner data" {
		t.Fatal("regular file was modified")
	}
}

func command(t *testing.T, path, value string) string {
	t.Helper()
	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(response, "\r\n", "\n")
}
