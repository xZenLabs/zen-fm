package platform

import (
	"errors"
	"io/fs"
)

// ModeChangeError permits filesystems without Unix mode support only when the
// caller explicitly opted in. Other I/O failures remain fatal.
func ModeChangeError(err error, modeLessFilesystem bool) error {
	if modeLessFilesystem && errors.Is(err, fs.ErrPermission) {
		return nil
	}
	return err
}
