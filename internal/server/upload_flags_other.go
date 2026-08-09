//go:build !linux && !android && !darwin

package server

func uploadOpenFlags() int { return 0 }
