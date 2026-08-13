package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func rejectChmod(t *testing.T, err error) {
	t.Helper()
	previous := chmodFile
	chmodFile = func(*os.File, os.FileMode) error { return err }
	t.Cleanup(func() { chmodFile = previous })
}

func TestOpenInternalDirectoryContinuesWhenChmodIsNotPermitted(t *testing.T) {
	rejectChmod(t, syscall.EPERM)
	r, _ := testRoot(t, Options{})

	directory, _, err := r.OpenInternalDirectory(".zenfm-internal-uploads")
	if err != nil {
		t.Fatal(err)
	}
	directory.Close()
}

func TestOpenInternalDirectoryStillReturnsUnexpectedChmodErrors(t *testing.T) {
	rejectChmod(t, syscall.EIO)
	r, root := testRoot(t, Options{})

	if _, _, err := r.OpenInternalDirectory(".zenfm-internal-uploads"); !errors.Is(err, syscall.EIO) {
		t.Fatalf("internal directory error = %v, want %v", err, syscall.EIO)
	}
	if _, err := os.Lstat(filepath.Join(root, ".zenfm-internal-uploads")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed internal directory leaked: %v", err)
	}
}

func TestCopyContinuesWhenChmodIsNotPermitted(t *testing.T) {
	r, root := testRoot(t, Options{})
	if err := os.Mkdir(filepath.Join(root, "source"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "book.epub"), []byte("book"), 0o600); err != nil {
		t.Fatal(err)
	}
	rejectChmod(t, syscall.EPERM)

	if _, err := r.Copy(context.Background(), "/source", "/copy", false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "copy", "book.epub")); err != nil || string(data) != "book" {
		t.Fatalf("copied content = %q, %v", data, err)
	}
}

func TestCopyStillReturnsUnexpectedChmodErrors(t *testing.T) {
	r, root := testRoot(t, Options{})
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("book"), 0o600); err != nil {
		t.Fatal(err)
	}
	rejectChmod(t, syscall.EIO)

	if _, err := r.Copy(context.Background(), "/source", "/copy", false); !errors.Is(err, syscall.EIO) {
		t.Fatalf("copy error = %v, want %v", err, syscall.EIO)
	}
	if _, err := os.Lstat(filepath.Join(root, "copy")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed copy published destination: %v", err)
	}
}
