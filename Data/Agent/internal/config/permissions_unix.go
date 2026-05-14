//go:build !windows

package config

import "os"

func RestrictParent(path string) error {
	return os.Chmod(path, 0o700)
}

func RestrictFile(path string) error {
	return os.Chmod(path, 0o600)
}
