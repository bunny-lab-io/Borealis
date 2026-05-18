//go:build !windows

package patchmanagement

import (
	"context"
	"fmt"
)

type unsupportedAdapter struct{}

func defaultWUAAdapter() WUAAdapter {
	return unsupportedAdapter{}
}

func (unsupportedAdapter) Scan(context.Context) ([]Update, error) {
	return nil, fmt.Errorf("Windows Update Agent is unavailable on this platform")
}

func (unsupportedAdapter) Install(context.Context, []Update) (InstallSummary, error) {
	return InstallSummary{}, fmt.Errorf("Windows Update Agent is unavailable on this platform")
}

func (unsupportedAdapter) PendingReboot(context.Context) (bool, error) {
	return false, fmt.Errorf("Windows Update Agent is unavailable on this platform")
}

func (unsupportedAdapter) Reboot(context.Context, int, string) error {
	return fmt.Errorf("Windows reboot is unavailable on this platform")
}
