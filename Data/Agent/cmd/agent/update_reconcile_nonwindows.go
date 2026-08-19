//go:build !windows

package main

import (
	"fmt"

	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func runPostUpdateReconciliation(_ agentruntime.Options) error {
	return fmt.Errorf("post-update host reconciliation is only supported on Windows")
}
