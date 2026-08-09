//go:build linux || android || darwin

package files

import (
	"os"
	"syscall"
)

func fileDeviceID(info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}

func fileObjectID(info os.FileInfo) (objectID, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return objectID{}, false
	}
	return objectID{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, true
}
