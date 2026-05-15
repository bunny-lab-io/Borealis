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

func TestFormatWindowsOperatingSystemUsesCaptionDisplayVersionAndUBR(t *testing.T) {
	got := formatWindowsOperatingSystem("Microsoft Windows 11 Pro", "25H2", "", "26200", 8457, "10.0.26200")
	if got != "Windows 11 Pro 25H2 Build 26200.8457" {
		t.Fatalf("os = %q", got)
	}
	server := formatWindowsOperatingSystem("Microsoft Windows Server 2022 Standard", "21H2", "", "20348", 3198, "10.0.20348")
	if server != "Windows Server 2022 Standard 21H2 Build 20348.3198" {
		t.Fatalf("server os = %q", server)
	}
}

func TestNormalizeWindowsLastUserPrefersDomainUserAndSkipsMachineAccount(t *testing.T) {
	got := normalizeWindowsLastUser(map[string]any{
		"UserName":            "LAB-OPERATOR-01$",
		"LastLoggedOnSAMUser": "BUNNY-LAB\\nicole.rappe",
		"ComputerName":        "LAB-OPERATOR-01",
		"Domain":              "BUNNY-LAB",
		"PartOfDomain":        true,
	})
	if got != "BUNNY-LAB\\nicole.rappe" {
		t.Fatalf("last user = %q", got)
	}
	local := normalizeWindowsLastUser(map[string]any{
		"UserName":     ".\\local_testuser",
		"ComputerName": "LAB-OPERATOR-01",
		"Domain":       "BUNNY-LAB",
		"PartOfDomain": true,
	})
	if local != "LAB-OPERATOR-01\\local_testuser" {
		t.Fatalf("local user = %q", local)
	}
	bareCurrent := normalizeWindowsLastUser(map[string]any{
		"UserName":            "nicole.rappe",
		"LastLoggedOnSAMUser": "BUNNY-LAB\\nicole.rappe",
		"ComputerName":        "LAB-OPERATOR-01",
		"Domain":              "corp.example",
		"PartOfDomain":        true,
	})
	if bareCurrent != "BUNNY-LAB\\nicole.rappe" {
		t.Fatalf("bare current user = %q", bareCurrent)
	}
}

func TestHardwareIdentityHelpers(t *testing.T) {
	platform := platformMetadata{
		Manufacturer:   "Dell Inc.",
		SystemModelRaw: "Latitude 5450",
		SystemModel:    combineManufacturerModel("Dell Inc.", "Latitude 5450"),
		SystemSerial:   "ABC1234",
	}
	cpu := addPlatformHardwareIdentity(map[string]any{"name": "Example CPU"}, platform)
	if cpu["system_model"] != "Dell Inc. Latitude 5450" || cpu["system_serial_number"] != "ABC1234" {
		t.Fatalf("cpu identity = %#v", cpu)
	}
	if normalizeHardwareString("To Be Filled By O.E.M.") != "" {
		t.Fatalf("placeholder hardware string was not removed")
	}
}

func TestFormatBitsPerSecond(t *testing.T) {
	if got := formatBitsPerSecond(1_000_000_000); got != "1 Gbps" {
		t.Fatalf("speed = %q", got)
	}
	if got := formatBitsPerSecond(100_000_000); got != "100 Mbps" {
		t.Fatalf("speed = %q", got)
	}
}

func TestStorageDiskTypeHelpers(t *testing.T) {
	if got := windowsStorageDiskType(3, "SSD"); got != "SSD" {
		t.Fatalf("windows fixed ssd = %q", got)
	}
	if got := windowsStorageDiskType(3, "Fixed hard disk media"); got != "HDD" {
		t.Fatalf("windows hard disk = %q", got)
	}
	if got := windowsStorageDiskType(5, ""); got != "CD-ROM" {
		t.Fatalf("windows cdrom = %q", got)
	}
	if got := linuxBlockDeviceCandidates("nvme0n1p3"); len(got) < 2 || got[1] != "nvme0n1" {
		t.Fatalf("nvme candidates = %#v", got)
	}
	if got := linuxBlockDeviceCandidates("sda2"); len(got) < 2 || got[1] != "sda" {
		t.Fatalf("sda candidates = %#v", got)
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
