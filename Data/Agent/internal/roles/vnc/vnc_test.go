package vnc

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewUsesUltraVNCLogCategoryPath(t *testing.T) {
	dir := t.TempDir()
	manager := New(nil, "LAB-01", "system", filepath.Join(dir, "config.json"))

	if got := manager.logPath; got != filepath.Join(dir, "Logs", "UltraVNC", "vnc.log") {
		t.Fatalf("unexpected log path: %s", got)
	}
}

func TestUltraVNCPasswordHashMatchesStoredFormat(t *testing.T) {
	got, err := ultraVNCPasswordHash("password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "DBD83CFD727A145800" {
		t.Fatalf("unexpected password hash: %s", got)
	}

	got, err = ultraVNCPasswordHash("bootpass")
	if err != nil {
		t.Fatal(err)
	}
	if got != "E82E982EF7C0723800" {
		t.Fatalf("unexpected bootpass hash: %s", got)
	}
}

func TestNormalizeFirewallRemoteRequiresSingleHost(t *testing.T) {
	if got := normalizeFirewallRemote("10.255.0.1/32"); got != "10.255.0.1/32" {
		t.Fatalf("unexpected normalized host: %s", got)
	}
	if got := normalizeFirewallRemote("10.255.0.1"); got != "10.255.0.1/32" {
		t.Fatalf("unexpected normalized bare host: %s", got)
	}
	if got := normalizeFirewallRemote("not-an-ip"); got != "" {
		t.Fatalf("expected invalid host to be rejected, got %s", got)
	}
}

func TestUltraVNCConfigIncludesSecurityAndCaptureSettings(t *testing.T) {
	settings := ultraVNCSettings(5901, "DBD83CFD727A145800", true, "")
	rendered := renderUltraVNCConfig(settings)
	for _, expected := range []string{
		"[UltraVNC]",
		"UseRegistry=0",
		"AuthRequired=1",
		"PortNumber=5901",
		"SocketConnect=1",
		"AllowLoopback=1",
		"RemoveWallpaper=1",
		"passwd=DBD83CFD727A145800",
		"passwd2=",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("config missing %q:\n%s", expected, rendered)
		}
	}
}
