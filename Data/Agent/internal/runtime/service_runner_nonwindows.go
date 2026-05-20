//go:build !windows

package agentruntime

import (
	"context"
	"log"
)

func RunServiceIfNeeded(logger *log.Logger, run func(context.Context) error) (int, bool) {
	return 0, false
}
