//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

func TestUltraVNCBootstrapConfigEnablesAllDisplays(t *testing.T) {
	programData := t.TempDir()
	t.Setenv("ProgramData", programData)

	configPath, err := ensureUltraVNCBootstrapConfig(BootstrapConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(raw)
	for _, expected := range []string{
		"primary=1",
		"secondary=1",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("bootstrap config missing %q:\n%s", expected, rendered)
		}
	}
}
