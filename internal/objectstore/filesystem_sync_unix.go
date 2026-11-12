//go:build !windows

package objectstore

import (
	"errors"
	"os"
	"syscall"
)

func syncObjectDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	err = f.Sync()
	// SMB servers may not implement directory fsync; both files were flushed.
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}
