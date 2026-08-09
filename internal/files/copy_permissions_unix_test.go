//go:build linux || android || darwin

package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGHSA_jj2rCopiedEntriesAreOwnerOnlyWithPermissiveUmask(t *testing.T) {
	oldMask := unix.Umask(0)
	defer unix.Umask(oldMask)

	r, directory := testRoot(t, Options{})
	source := filepath.Join(directory, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "document.txt"), []byte("private"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tool"), []byte("executable"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Copy(context.Background(), "/source", "/copy", false); err != nil {
		t.Fatal(err)
	}

	for name, expected := range map[string]os.FileMode{
		"copy":              0o700,
		"copy/nested":       0o700,
		"copy/document.txt": 0o600,
		"copy/tool":         0o700,
	} {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if actual := info.Mode().Perm(); actual != expected {
			t.Errorf("%s mode = %04o, want %04o", name, actual, expected)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s retained group/other access: %04o", name, info.Mode().Perm())
		}
	}
}
