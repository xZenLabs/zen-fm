//go:build linux || android

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

func pseudoFilesystemFile(file *os.File) bool {
	var stat unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &stat) != nil {
		return false
	}
	switch uint64(stat.Type) {
	case 0x9fa0, // procfs
		0x62656572, // sysfs
		0x1cd1,     // devpts
		0x64626720, // debugfs
		0x74726163, // tracefs
		0x73636673, // securityfs
		0x27e0eb,   // cgroup v1
		0x63677270, // cgroup v2
		0xcafe4a11, // bpf
		0x62656570: // configfs
		return true
	default:
		return false
	}
}
