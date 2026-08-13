package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
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
