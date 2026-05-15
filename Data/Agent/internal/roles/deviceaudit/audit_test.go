package deviceaudit

import (
	"context"
	"testing"
)

func TestParseProcMemInfo(t *testing.T) {
	total, available := parseProcMemInfo("MemTotal:       16384 kB\nMemAvailable:    4096 kB\n")
	if total != 16777216 {
		t.Fatalf("total = %d", total)
	}
	if available != 4194304 {
		t.Fatalf("available = %d", available)
	}
}

func TestParseProcCPUInfo(t *testing.T) {
	got := parseProcCPUInfo("processor\t: 0\nmodel name\t: Example CPU\ncpu cores\t: 4\ncpu MHz\t\t: 2500.000\nprocessor\t: 1\n")
	if got["name"] != "Example CPU" {
		t.Fatalf("name = %#v", got)
	}
	if got["physical_cores"] != 4 {
		t.Fatalf("physical_cores = %#v", got)
	}
	if got["logical_cores"] != 2 {
		t.Fatalf("logical_cores = %#v", got)
	}
	if got["base_clock_ghz"] != 2.5 {
		t.Fatalf("base_clock_ghz = %#v", got)
	}
}

func TestParseDFOutputSkipsPseudoFilesystems(t *testing.T) {
	got := parseDFOutput("Filesystem     Type 1B-blocks Used Available Use% Mounted on\n/dev/sda1      ext4 1000 400 600 40% /\ntmpfs          tmpfs 200 1 199 1% /run\n")
	if len(got) != 1 {
		t.Fatalf("disk count = %d: %#v", len(got), got)
	}
	if got[0]["drive"] != "/" || got[0]["total"] != int64(1000) || got[0]["used"] != int64(400) {
		t.Fatalf("disk payload = %#v", got[0])
	}
}

func TestAuditorCollectProducesShape(t *testing.T) {
	snapshot := NewAuditor().Collect(context.Background())
	if snapshot.Inventory == nil {
		t.Fatalf("inventory missing")
	}
	for _, key := range []string{"memory", "storage", "network", "cpu"} {
		if _, ok := snapshot.Inventory[key]; !ok {
			t.Fatalf("inventory key %s missing: %#v", key, snapshot.Inventory)
		}
	}
	if snapshot.Health.StatusCode == "" {
		t.Fatalf("health missing: %#v", snapshot.Health)
	}
}
