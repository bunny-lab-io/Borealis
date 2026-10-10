package clusterremote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNetworkBootProcess(t *testing.T) {
	for _, mode := range []string{"success", "legacy argv", "wrong PID", "zombie", "stopped", "missing start", "zero start", "overflow start", "wrong argv", "oversized argv", "missing proc", "symlink proc", "symlink stat", "foreign exe", "deleted exe", "writable binary", "nonexecutable binary", "symlink binary", "replaced binary", "final start drift", "final args drift", "final exe drift"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			installNetworkBootFixture(t, root)
			script := persistentNetworkLibraryScript + systemdObservationLibraryScript + networkBootProcessLibraryScript + "\nMODE=" + strconv.Quote(mode) + `
import pathlib
r = pathlib.Path(ROOT)
p = r/"proc/42"
b = r/"usr/lib/systemd/systemd-networkd"
executable = "/usr/lib/systemd/systemd-networkd"
if MODE == "legacy argv":
    executable = "/lib/systemd/systemd-networkd"; (p/"cmdline").write_bytes(executable.encode()+b"\0")
if MODE == "wrong PID": (p/"stat").write_text((p/"stat").read_text().replace("42 (", "43 ("))
if MODE == "zombie": (p/"stat").write_text((p/"stat").read_text().replace(") S ", ") Z "))
if MODE == "stopped": (p/"stat").write_text((p/"stat").read_text().replace(") S ", ") T "))
if MODE == "missing start": (p/"stat").write_text("42 (nested ) name) S 0")
if MODE in ("zero start", "overflow start"):
    (p/"stat").write_text((p/"stat").read_text().replace("123", "0" if MODE == "zero start" else str(2**64)))
if MODE == "wrong argv": (p/"cmdline").write_bytes(executable.encode()+b"\0--foreign\0")
if MODE == "oversized argv": (p/"cmdline").write_bytes(b"x"*4097)
if MODE in ("missing proc", "symlink proc"):
    p.rename(r/"proc/43")
    if MODE == "symlink proc": p.symlink_to(r/"proc/43")
if MODE == "symlink stat":
    (p/"stat").rename(p/"other"); (p/"stat").symlink_to(p/"other")
if MODE in ("foreign exe", "deleted exe"):
    (p/"exe").unlink(); (p/"exe").symlink_to(str(b)+" (deleted)" if MODE == "deleted exe" else p/"stat")
if MODE == "writable binary": b.chmod(0o777)
if MODE == "nonexecutable binary": b.chmod(0o600)
if MODE == "symlink binary":
    b.rename(b.with_name("other")); b.symlink_to(b.with_name("other"))
if MODE == "replaced binary":
    b.rename(b.with_name("old")); b.write_bytes(b"replacement"); b.chmod(0o700)
    (p/"exe").unlink(); (p/"exe").symlink_to(b.with_name("old"))
def observe():
    fd = os.open(ROOT, os.O_RDONLY|os.O_DIRECTORY)
    try:
        before = boot_network_process(fd, 42, executable)
        if MODE == "final start drift": (p/"stat").write_text((p/"stat").read_text().replace("123", "456"))
        if MODE == "final args drift": (p/"cmdline").write_bytes(b"changed\0")
        if MODE == "final exe drift": b.write_bytes(b"changed")
        if boot_network_process(fd, 42, executable) != before: raise ValueError()
        return True
    finally:
        os.close(fd)
main(observe)
`
			script = strings.Replace(script, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script).Output()
			want := mode == "success" || mode == "legacy argv"
			if ctx.Err() != nil || (err == nil) != want || want && !bytes.Equal(bytes.TrimSpace(out), []byte("true")) || !want && len(out) != 0 {
				t.Fatalf("process proof: error=%v timeout=%v output bytes=%d", err, ctx.Err(), len(out))
			}
		})
	}
}
