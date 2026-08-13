//go:build linux

package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdvancedPseudoGuardFollowsFilesystemIdentityThroughAlias(t *testing.T) {
	if _, err := os.Stat("/proc/version"); err != nil {
		t.Skipf("procfs unavailable: %v", err)
	}
	r, err := Open("/", Options{MaxWalkEntries: 2_000})
	if err != nil {
		t.Skipf("literal root unavailable: %v", err)
	}
	defer r.Close()
	dir := t.TempDir()
	alias := filepath.Join(dir, "proc-alias")
	target, err := filepath.Rel(dir, "/proc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	aliasPath := "/" + strings.TrimPrefix(filepath.ToSlash(alias), "/")
	parentPath := "/" + strings.TrimPrefix(filepath.ToSlash(dir), "/")
	listing, err := r.List(parentPath, true)
	if err != nil || len(listing.Entries) != 1 || !listing.Entries[0].Symlink {
		t.Fatalf("pseudo alias was not safely listed: %+v %v", listing, err)
	}
	if !r.Pseudo(aliasPath + "/version") {
		t.Fatal("pseudo content alias was not identified by filesystem identity")
	}
	if _, err := r.ReadContent(aliasPath + "/version"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("pseudo content alias bypassed guard: %v", err)
	}
	result, err := r.Search(context.Background(), aliasPath, "version", true, 10)
	if len(result.Entries) != 0 || !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("recursive pseudo alias was traversed: %+v %v", result, err)
	}
}
