//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
)

func maybeRunBootstrap(args []string) (int, bool) {
	if hasRuntimeFlag(args) {
		return 0, false
	}
	cli, err := parseCLI(args)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2, true
	}
	isService, err := svc.IsWindowsService()
	if err == nil && isService {
		return runBootstrapWindowsService(cli), true
	}
	return runBootstrapConsole(cli), true
}

func hasRuntimeFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--system-service", "--helper", "--config-path", "--install-service", "--uninstall-service", "--update-check", "--finalize-update", "--watchdog", "--once", "--version":
			return true
		}
	}
	return false
}
