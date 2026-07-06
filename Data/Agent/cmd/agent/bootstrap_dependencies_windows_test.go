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
	cfg := BootstrapConfig{InstallDir: t.TempDir()}
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()

	configPath, err := ensureUltraVNCBootstrapConfig(cfg, logger)
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
		"FileTransferEnabled=0",
	} {
		if !bootstrapSectionContains(rendered, "admin", expected) {
			t.Fatalf("bootstrap admin section missing %q:\n%s", expected, rendered)
		}
	}
	if !bootstrapSectionContains(rendered, "UltraVNC", "passwd=") {
		t.Fatalf("bootstrap UltraVNC section missing password:\n%s", rendered)
	}
	if !bootstrapSectionContains(rendered, "poll", "PollFullScreen=1") {
		t.Fatalf("bootstrap poll section missing PollFullScreen=1:\n%s", rendered)
	}
}

func bootstrapSectionContains(content string, section string, expected string) bool {
	header := "[" + section + "]"
	start := strings.Index(content, header)
	if start < 0 {
		return false
	}
	sectionText := content[start+len(header):]
	if next := strings.Index(sectionText, "\n["); next >= 0 {
		sectionText = sectionText[:next]
	}
	return strings.Contains(sectionText, expected)
}
