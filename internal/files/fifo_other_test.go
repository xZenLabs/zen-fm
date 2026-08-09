//go:build !darwin && !linux && !android && !freebsd && !openbsd

package files

import "errors"

func makeFIFO(string) error { return errors.New("unsupported") }
