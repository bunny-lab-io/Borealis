//go:build !windows

package main

import (
	"fmt"

	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func runAgentWatchdog(options agentruntime.Options) error {
	return fmt.Errorf("local watchdog task is only supported on Windows; systemd Restart=always covers Linux agent process recovery")
}
