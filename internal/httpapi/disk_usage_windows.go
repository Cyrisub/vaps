//go:build windows

package httpapi

import "golang.org/x/sys/windows"

type diskUsage struct {
	TotalBytes uint64
	FreeBytes  uint64
}

func dataDiskUsage(path string) (diskUsage, error) {
	directoryName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return diskUsage{}, err
	}
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(
		directoryName,
		&freeBytesAvailable,
		&totalBytes,
		&totalFreeBytes,
	); err != nil {
		return diskUsage{}, err
	}
	return diskUsage{
		TotalBytes: totalBytes,
		FreeBytes:  freeBytesAvailable,
	}, nil
}
