//go:build !windows

package agentruntime

func RunWatchdogCheck(configPath string) error {
	return nil
}
