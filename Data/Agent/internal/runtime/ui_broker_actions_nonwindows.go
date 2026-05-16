//go:build !windows

package agentruntime

import "fmt"

func restartLocalAgent() error {
	return fmt.Errorf("local tray restart is only supported on Windows")
}
