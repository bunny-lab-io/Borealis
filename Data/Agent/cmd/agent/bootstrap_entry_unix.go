//go:build !windows

package main

func maybeRunBootstrap(args []string) (int, bool) {
	return 0, false
}
