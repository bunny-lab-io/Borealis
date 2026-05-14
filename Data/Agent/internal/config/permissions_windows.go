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
	commands := [][]string{
		{"icacls.exe", path, "/inheritance:r"},
		{"icacls.exe", path, "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %w: %s", args[0], err, string(output))
		}
	}
	return nil
}
