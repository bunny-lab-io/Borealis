//go:build !windows

package agentruntime

import "path/filepath"

func logPathForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "Logs", "Agent", "agent.log")
}
