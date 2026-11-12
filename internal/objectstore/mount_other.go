//go:build !linux

package objectstore

import "errors"

func checkSMBMount(string, string) error {
	return errors.New("mount_source verification is supported only on Linux")
}
