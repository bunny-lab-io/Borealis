//go:build windows

package main

import "testing"

func TestRuntimeFlagsUseServiceAndWatchdog(t *testing.T) {
	if !hasRuntimeFlag([]string{"--service"}) {
		t.Fatal("--service should bypass bootstrap")
	}
	if !hasRuntimeFlag([]string{"--watchdog-check"}) {
		t.Fatal("--watchdog-check should bypass bootstrap")
	}
	if hasRuntimeFlag([]string{"--system-service"}) {
		t.Fatal("--system-service should not be accepted")
	}
}
