//go:build windows

package config

import (
	"fmt"
	"os"
	"os/exec"
)

func RestrictParent(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return applyACL(path)
}

func RestrictFile(path string) error {
	return applyACL(path)
}

func applyACL(path string) error {
	systemGrant := "*S-1-5-18:F"
	adminGrant := "*S-1-5-32-544:F"
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		systemGrant = "*S-1-5-18:(OI)(CI)F"
		adminGrant = "*S-1-5-32-544:(OI)(CI)F"
	}
	commands := [][]string{
		{"icacls.exe", path, "/inheritance:r"},
		{"icacls.exe", path, "/grant:r", systemGrant, adminGrant},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %w: %s", args[0], err, string(output))
		}
	}
	return nil
}
