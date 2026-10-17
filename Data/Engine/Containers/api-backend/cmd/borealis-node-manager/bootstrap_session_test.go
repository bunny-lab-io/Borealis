package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/ed25519"
	"crypto/rand"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestBootstrapHostVerificationRejectsChangedIdentity(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	request := clusterbootstrap.SessionRequest{Protocol: 1, Action: "verify", Nonce: strings.Repeat("c", 64), Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), ManagerSHA256: strings.Repeat("b", 64), AllowQualification: true,
		Binding: clusterbootstrap.SessionBinding{ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333", HolderID: "44444444-4444-4444-8444-444444444444", Generation: 7, OperationAttempt: 2,
			Address: "192.168.3.251", Port: 22, Hostname: "engine-02", MachineID: strings.Repeat("a", 32), HostKeyAlgorithm: key.Type(), HostKeyFingerprint: ssh.FingerprintSHA256(key)}}
	for _, mode := range []string{"valid", "hostname", "machine-id", "public key", "key options", "multiple keys", "missing address", "duplicate address", "executable", "write action", "invalid binding"} {
		t.Run(mode, func(t *testing.T) {
			r := request
			hostname, machine, rawKey, executable := "engine-02.example.invalid", []byte(r.Binding.MachineID+"\n"), ssh.MarshalAuthorizedKey(key), r.ManagerSHA256
			addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr(r.Binding.Address)}
			switch mode {
			case "hostname":
				hostname = "engine-03"
			case "machine-id":
				machine = []byte(strings.Repeat("d", 32))
			case "public key":
				other, _, _ := ed25519.GenerateKey(rand.Reader)
				replacement, _ := ssh.NewPublicKey(other)
				rawKey = ssh.MarshalAuthorizedKey(replacement)
			case "key options":
				rawKey = append([]byte(`command="false" `), rawKey...)
			case "multiple keys":
				rawKey = append(rawKey, rawKey...)
			case "missing address":
				addresses = addresses[:1]
			case "duplicate address":
				addresses = append(addresses, addresses[1])
			case "executable":
				executable = strings.Repeat("d", 64)
			case "write action":
				r.Action = "join"
			case "invalid binding":
				r.Binding.Generation = 0
			}
			err := compareBootstrapHost(r, hostname, machine, rawKey, addresses, executable)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("host verification boundary failed: %v", err)
			}
		})
	}
}

func TestBootstrapPublicFilesRejectUnsafeTypesAndPermissions(t *testing.T) {
	for _, mode := range []string{"valid", "symlink", "parent symlink", "hardlink", "fifo", "directory", "empty", "oversized", "group writable", "world writable", "relative"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, "metadata")
			if err := os.WriteFile(name, []byte("public identity\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				link := filepath.Join(root, "link")
				if err := os.Symlink(name, link); err != nil {
					t.Fatal(err)
				}
				name = link
			case "parent symlink":
				link := root + "-link"
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				name = filepath.Join(link, "metadata")
			case "hardlink":
				if err := os.Link(name, filepath.Join(root, "linked")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				name = filepath.Join(root, "fifo")
				if err := syscall.Mkfifo(name, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				name = root
			case "empty":
				if err := os.Truncate(name, 0); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(name, 129); err != nil {
					t.Fatal(err)
				}
			case "group writable":
				if err := os.Chmod(name, 0o664); err != nil {
					t.Fatal(err)
				}
			case "world writable":
				if err := os.Chmod(name, 0o646); err != nil {
					t.Fatal(err)
				}
			case "relative":
				name = "relative-public-identity"
			}
			raw, err := readBootstrapPublicFile(name, 128)
			if mode == "valid" {
				if err != nil || string(raw) != "public identity\n" {
					t.Fatalf("valid public metadata rejected: %v", err)
				}
			} else if err == nil || raw != nil {
				t.Fatal("unsafe public metadata read")
			}
		})
	}
}
