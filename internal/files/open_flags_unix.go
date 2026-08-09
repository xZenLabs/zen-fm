//go:build linux || android || darwin

package files

import "syscall"

// O_NOFOLLOW closes the lstat/open race; O_NONBLOCK ensures a raced FIFO or
// device open cannot stall the server before fstat rejects it.
func regularOpenFlags() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }
