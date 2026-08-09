//go:build darwin || linux || android || freebsd || openbsd

package files

import "syscall"

func makeFIFO(name string) error { return syscall.Mkfifo(name, 0o600) }
