package clusterremote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func installNetworkCloudFixture(t *testing.T, root string) {
	t.Helper()
	for _, unit := range []string{"cloud-init-local.service", "cloud-init.service", "cloud-config.service", "cloud-final.service", "cloud-init-hotplugd.service", "cloud-init-hotplugd.socket", "cloud-init.target", "cloud-config.target"} {
		data := "[Unit]\nConditionPathExists=!/etc/cloud/cloud-init.disabled\nConditionKernelCommandLine=!cloud-init=disabled\n"
		if unit == "cloud-config.target" {
			data = "[Unit]\nDescription=Cloud-config availability\n"
		}
		if err := os.WriteFile(filepath.Join(root, "usr/lib/systemd/system", unit), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "etc/cloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/cloud/cloud-init.disabled"), []byte("disabled by operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkBootCloudFiles(t *testing.T) {
	for _, mode := range []string{"disabled", "missing marker", "symlink marker", "writable marker", "hardlink marker", "writable directory", "unknown unit", "unknown timer", "partial units", "alias", "dropin", "unit override", "missing condition", "reset condition", "trigger condition", "service text condition"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			installNetworkBootFixture(t, root)
			installNetworkCloudFixture(t, root)
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + networkBootLibraryScript + "\nimport pathlib\nMODE=" + strconv.Quote(mode) + `
r=pathlib.Path(ROOT)
marker=r/"etc/cloud/cloud-init.disabled"
unit=r/"usr/lib/systemd/system/cloud-init-local.service"
if MODE == "missing marker": marker.unlink()
if MODE == "symlink marker": marker.unlink(); marker.symlink_to("/dev/null")
if MODE == "writable marker": marker.chmod(0o666)
if MODE == "hardlink marker": os.link(marker,r/"duplicate")
if MODE == "writable directory": marker.parent.chmod(0o777)
if MODE == "unknown unit": (unit.parent/"cloud-init-foreign.service").write_text(unit.read_text())
if MODE == "unknown timer": (unit.parent/"cloud-init-foreign.timer").write_text(unit.read_text())
if MODE == "partial units": (unit.parent/"cloud-init-hotplugd.service").unlink()
if MODE == "alias": (unit.parent/"foreign.service").symlink_to("cloud-init-local.service")
if MODE == "dropin":
    area=r/"etc/systemd/system/cloud-.service.d"; area.mkdir(parents=True); (area/"override.conf").write_text("[Unit]\nConditionPathExists=\n")
if MODE == "unit override": (r/"etc/systemd/system/cloud-init-local.service").write_text(unit.read_text())
if MODE == "missing condition": unit.write_text("[Unit]\nDescription=unprotected\n")
if MODE == "reset condition": unit.write_text(unit.read_text()+"ConditionPathExists=\n")
if MODE == "trigger condition": unit.write_text(unit.read_text().replace("=!/", "=|!/"))
if MODE == "service text condition": unit.write_text("[Service]\nExecStart=/bin/echo \\\n[Unit] \\\nConditionPathExists=!/etc/cloud/cloud-init.disabled\n")
main(lambda: bool(boot_files(os.open(ROOT,os.O_RDONLY|os.O_DIRECTORY))))
`
			script = strings.Replace(script, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script).Output()
			want := mode == "disabled"
			if ctx.Err() != nil || (err == nil) != want || want && !bytes.Equal(bytes.TrimSpace(out), []byte("true")) || !want && len(out) != 0 {
				t.Fatalf("cloud-init file outcome: error=%v timeout=%v output bytes=%d", err, ctx.Err(), len(out))
			}
		})
	}
}

func TestNetworkBootCloudNative(t *testing.T) {
	for _, mode := range []string{"boot cloud disabled", "boot cloud unloaded", "boot cloud active", "boot cloud job", "boot cloud marker drift", "boot cloud condition", "boot cloud condition type", "boot cloud trigger", "boot cloud process", "boot cloud orphan", "boot cloud reload", "boot cloud loaded dropin", "boot cloud unknown", "boot cloud duplicate", "boot cloud removed files"} {
		t.Run(mode, func(t *testing.T) { testNetworkRenderCorrespondence(t, mode, true) })
	}
}
