//go:build !windows

package config

import "os"

func replaceConfigFile(staged, path string) error {
	return os.Rename(staged, path)
}
