//go:build !linux && !android && !darwin

package files

func regularOpenFlags() int { return 0 }
