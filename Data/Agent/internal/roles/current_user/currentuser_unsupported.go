//go:build !windows

package currentuser

import (
	"context"
	"runtime"

	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

func (d *Dispatcher) Start(ctx context.Context, configPath string) {}

func RunHelper(ctx context.Context, options HelperOptions) error {
	<-ctx.Done()
	return ctx.Err()
}

func (d *Dispatcher) DispatchCurrentUserQuickJob(ctx context.Context, payload map[string]any) (scripts.Result, bool, string) {
	if runtime.GOOS == "linux" {
		return scripts.Result{}, false, "Linux CURRENTUSER execution is not implemented in the Go agent yet."
	}
	return scripts.Result{}, false, "CURRENTUSER execution is not implemented on this platform."
}

func (d *Dispatcher) SupportsCurrentUserDispatch() bool {
	return false
}

func (d *Dispatcher) RoleHealth() RoleHealth {
	return RoleHealth{
		Status:     "unsupported",
		StatusCode: "unsupported",
		Detail:     "CURRENTUSER helper broker is not implemented on this platform.",
		Details: map[string]any{
			"running_status": "Unsupported",
			"runtime":        "go",
			"broker_mode":    "unsupported",
		},
	}
}
