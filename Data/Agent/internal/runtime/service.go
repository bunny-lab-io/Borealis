package agentruntime

import (
	"path/filepath"
)

func ConfigPathForExecutable(exePath string) string {
	return filepath.Join(filepath.Dir(exePath), "config.json")
}
