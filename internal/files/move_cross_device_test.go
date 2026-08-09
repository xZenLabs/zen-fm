package files

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func forceCrossDeviceMove(r *Root) {
	r.renameForMove = func(*os.Root, string, *os.Root, string, bool) error {
		return syscall.EXDEV
	}
}

func TestCrossDeviceMoveStagesPublishesAndRemovesSource(t *testing.T) {
	r, directory := testRoot(t, Options{MaxWriteBytes: 1 << 20})
	forceCrossDeviceMove(r)
	source := filepath.Join(directory, "source")
	if err := os.Mkdir(source, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o777); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(source, "tool")
	if err := os.WriteFile(tool, []byte("private"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tool, 0o777); err != nil {
		t.Fatal(err)
	}
	progress := 0
	if _, err := r.MoveWithProgress(context.Background(), "/source", "/destination", false, func() { progress++ }); err != nil {
		t.Fatal(err)
	}
	if progress < 2 {
		t.Fatalf("move reported %d progress events", progress)
	}
	if _, err := os.Lstat(source); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source survived successful move: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "destination", "tool"))
	if err != nil || string(data) != "private" {
		t.Fatalf("destination content = %q, %v", data, err)
	}
	for name, expected := range map[string]os.FileMode{"destination": 0o700, "destination/tool": 0o700} {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if actual := info.Mode().Perm(); actual != expected {
			t.Errorf("%s mode = %04o, want %04o", name, actual, expected)
		}
	}
}

func TestCrossDeviceMoveCancellationCleansStageAndKeepsSource(t *testing.T) {
	r, directory := testRoot(t, Options{MaxWriteBytes: 1 << 20})
	forceCrossDeviceMove(r)
	payload := bytes.Repeat([]byte("z"), 512<<10)
	if err := os.WriteFile(filepath.Join(directory, "source.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	progress := 0
	_, err := r.MoveWithProgress(ctx, "/source.bin", "/destination.bin", false, func() {
		progress++
		if progress == 2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled move returned %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(directory, "source.bin"))
	if readErr != nil || !bytes.Equal(data, payload) {
		t.Fatalf("cancellation damaged source: %d bytes, %v", len(data), readErr)
	}
	if _, err := os.Lstat(filepath.Join(directory, "destination.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancellation published destination: %v", err)
	}
	assertNoMoveStages(t, directory)
}

func TestCrossDeviceMoveHonorsByteLimit(t *testing.T) {
	r, directory := testRoot(t, Options{MaxWriteBytes: 4})
	forceCrossDeviceMove(r)
	if err := os.WriteFile(filepath.Join(directory, "source.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MoveWithProgress(context.Background(), "source.txt", "destination.txt", false, nil); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize cross-device move returned %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "source.txt")); err != nil || string(data) != "12345" {
		t.Fatalf("oversize move damaged source: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "destination.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("oversize move published destination: %v", err)
	}
	assertNoMoveStages(t, directory)
}

func TestCrossDeviceDirectoryMoveRejectsSkippedSymlinkWithoutDeletingSource(t *testing.T) {
	r, directory := testRoot(t, Options{})
	forceCrossDeviceMove(r)
	if err := os.Mkdir(filepath.Join(directory, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "source", "kept.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("kept.txt", filepath.Join(directory, "source", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MoveWithProgress(context.Background(), "source", "destination", false, nil); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("cross-device symlink move returned %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "source", "link")); err != nil {
		t.Fatalf("rejected move damaged source: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(directory, "destination")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("rejected move published destination: %v", err)
	}
	assertNoMoveStages(t, directory)
}

func assertNoMoveStages(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".zenfm-move-") {
			t.Fatalf("move stage leaked: %s", entry.Name())
		}
	}
}
