package platform

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

func TestModeChangeError(t *testing.T) {
	if err := ModeChangeError(syscall.EPERM, true); err != nil {
		t.Fatalf("explicit mode-less filesystem rejected EPERM: %v", err)
	}
	if err := ModeChangeError(syscall.EACCES, true); err != nil {
		t.Fatalf("explicit mode-less filesystem rejected EACCES: %v", err)
	}
	if err := ModeChangeError(syscall.EPERM, false); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("strict mode did not preserve permission error: %v", err)
	}
	if err := ModeChangeError(syscall.EIO, true); !errors.Is(err, syscall.EIO) {
		t.Fatalf("mode-less filesystem hid non-permission error: %v", err)
	}
}
