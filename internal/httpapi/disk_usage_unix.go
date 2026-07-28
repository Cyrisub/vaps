//go:build !windows

package httpapi

import "golang.org/x/sys/unix"

type diskUsage struct {
	TotalBytes uint64
	FreeBytes  uint64
}

func dataDiskUsage(path string) (diskUsage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return diskUsage{}, err
	}
	return diskUsage{
		TotalBytes: uint64(stat.Blocks) * uint64(stat.Bsize),
		FreeBytes:  uint64(stat.Bavail) * uint64(stat.Bsize),
	}, nil
}
