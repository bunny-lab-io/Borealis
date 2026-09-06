package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNodeManagerTokenPermissionsUnderRestrictiveUmask(t *testing.T) {
	const childMarker = "BOREALIS_TEST_NODE_MANAGER_TOKEN_UMASK"
	if os.Getenv(childMarker) != "1" {
		// umask is process-wide; isolate it from parallel tests and the race lane.
		cmd := exec.Command(os.Args[0], "-test.run=^TestNodeManagerTokenPermissionsUnderRestrictiveUmask$")
		cmd.Env = append(os.Environ(), childMarker+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("restrictive-umask subprocess: %v\n%s", err, output)
		}
		return
	}
	oldMask := syscall.Umask(0o077)
	defer syscall.Umask(oldMask)
	for _, test := range []struct {
		name string
		mode os.FileMode
	}{
		{name: "new"},
		{name: "root_only", mode: 0o600},
		{name: "group_read", mode: 0o640},
		{name: "world_read", mode: 0o644},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node-manager.token")
			var previous []byte
			if test.mode != 0 {
				previous = []byte(strings.Repeat("a", 64) + "\n")
				if err := os.WriteFile(path, previous, test.mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, test.mode); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := ensureNodeManagerToken(path); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o640 {
					t.Fatalf("token mode = %o, want owner read/write and group read only", info.Mode().Perm())
				}
				current, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := hex.DecodeString(strings.TrimSpace(string(current)))
				if err != nil || len(decoded) != 32 {
					t.Fatal("token must contain 32 hex-encoded random bytes")
				}
				if previous != nil && !bytes.Equal(previous, current) {
					t.Fatal("permission reconciliation rotated existing token")
				}
				previous = current
			}
		})
	}
}
