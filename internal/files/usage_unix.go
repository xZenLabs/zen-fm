//go:build linux || android || darwin

package files

import "syscall"

func filesystemUsage(name string) (Usage, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(name, &stat); err != nil {
		return Usage{}, err
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	available := stat.Bavail * blockSize
	used := uint64(0)
	if total > available {
		used = total - available
	}
	return Usage{Used: used, Total: total}, nil
}
