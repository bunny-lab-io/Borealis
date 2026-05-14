package currentuser

import (
	"context"
	"runtime"
)

type Dispatcher struct{}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{}
}

func (d *Dispatcher) DispatchCurrentUserQuickJob(ctx context.Context, payload map[string]any) (bool, string) {
	if runtime.GOOS == "linux" {
		return false, "Linux CURRENTUSER execution is not implemented in the Go agent yet."
	}
	return false, "Windows CURRENTUSER helper broker is not fully ported to the Go agent yet."
}

func (d *Dispatcher) SupportsCurrentUserDispatch() bool {
	return false
}
