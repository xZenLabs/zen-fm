//go:build linux || android || darwin

package server

import "syscall"

func uploadOpenFlags() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }
