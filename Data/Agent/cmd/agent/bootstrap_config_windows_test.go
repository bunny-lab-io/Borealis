//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestBootstrapCLIRejectsInsecureTLSFlag(t *testing.T) {
	for _, arg := range []string{"--insecure", "--insecure-tls-skip-verify"} {
		_, err := parseCLI([]string{arg})
		if err == nil || !strings.Contains(err.Error(), "unsupported Agent.exe argument") {
			t.Fatalf("expected %s to be rejected, got %v", arg, err)
		}
	}
}
