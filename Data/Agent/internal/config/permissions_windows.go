//go:build windows

package config

import "os"

func RestrictParent(path string) error {
	return os.MkdirAll(path, 0o755)
}

func RestrictFile(path string) error {
	return nil
}
