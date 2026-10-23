package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/crypto/ssh"
)

func nodeManagerIdentityMaterial(t *testing.T) *engineidentity.Material {
	t.Helper()
	_, jwt, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, script, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private := func(key ed25519.PrivateKey) []byte {
		raw, marshalErr := x509.MarshalPKCS8PrivateKey(key)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	}
	public, err := x509.MarshalPKIXPublicKey(script.Public())
	if err != nil {
		t.Fatal(err)
	}
	material, err := engineidentity.ParseFiles(map[string][]byte{
		engineidentity.EngineSecretPath: []byte(strings.Repeat("s", 48)), engineidentity.AgentJWTPath: private(jwt),
		engineidentity.ScriptKeyPath: private(script), engineidentity.ScriptPublicPath: public,
	})
	if err != nil {
		t.Fatal(err)
	}
	return material
}

func TestBootstrapSessionIdentityRecoveryComposesReadOnlyEvidence(t *testing.T) {
	request := identityRuntimeRequestFixture()
	request.Action = "verify_identity_recovery"
	request.IdentityRecovery = &clusterbootstrap.IdentityRecoveryRequest{
		SourceID: "55555555-5555-4555-8555-555555555555", SourceNodeUID: "66666666-6666-4666-8666-666666666666", TargetNodeUID: "77777777-7777-4777-8777-777777777777",
	}
	for _, mode := range []string{"valid", "host", "first pods", "runtime", "second pods", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			var order []string
			podCalls := 0
			observers := bootstrapSessionObservers{
				host: func(context.Context, clusterbootstrap.SessionRequest) error {
					order = append(order, "host")
					if mode == "host" {
						return errors.New("private host failure")
					}
					return nil
				},
				consumers: func(_ context.Context, target identityConsumerTarget) error {
					podCalls++
					order = append(order, "pods")
					if target.NodeName != request.Binding.Hostname || target.NodeUID != request.IdentityRecovery.TargetNodeUID {
						t.Fatal("target Node binding changed")
					}
					if mode == "first pods" && podCalls == 1 || mode == "second pods" && podCalls == 2 {
						return errors.New("private Pod failure")
					}
					return nil
				},
				runtime: func(_ context.Context, observed clusterbootstrap.SessionRequest) error {
					order = append(order, "runtime")
					if observed.IdentityRecovery == nil || *observed.IdentityRecovery != *request.IdentityRecovery {
						t.Fatal("runtime lost recovery binding")
					}
					if mode == "runtime" {
						return errors.New("private runtime failure")
					}
					return nil
				},
			}
			err := verifyBootstrapSessionRequestWith(ctx, request, observers)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("identity recovery evidence boundary failed: %v", err)
			}
			if mode == "valid" && strings.Join(order, ",") != "host,pods,runtime,pods" {
				t.Fatalf("identity evidence not bracketed: %v", order)
			}
		})
	}

	ordinary := request
	ordinary.Action, ordinary.IdentityRecovery = "verify", nil
	calls := 0
	err := verifyBootstrapSessionRequestWith(context.Background(), ordinary, bootstrapSessionObservers{
		host: func(context.Context, clusterbootstrap.SessionRequest) error { calls++; return nil },
		consumers: func(context.Context, identityConsumerTarget) error {
			t.Fatal("ordinary verification inspected recovery Pods")
			return nil
		},
		runtime: func(context.Context, clusterbootstrap.SessionRequest) error {
			t.Fatal("ordinary verification inspected identity runtime")
			return nil
		},
	})
	if err != nil || calls != 1 {
		t.Fatal("ordinary host verification changed", err)
	}
}

func TestBootstrapIdentityReceiverRemainsMemoryOnly(t *testing.T) {
	request := identityRuntimeRequestFixture()
	request.Action = "deliver_identity_recovery"
	request.IdentityRecovery = &clusterbootstrap.IdentityRecoveryRequest{SourceID: "55555555-5555-4555-8555-555555555555", SourceNodeUID: "66666666-6666-4666-8666-666666666666", TargetNodeUID: "77777777-7777-4777-8777-877777777777"}
	material := nodeManagerIdentityMaterial(t)
	if err := receiveBootstrapIdentityMaterial(context.Background(), request, material); err != nil {
		t.Fatal(err)
	}
	request.Action = "verify_identity_recovery"
	if receiveBootstrapIdentityMaterial(context.Background(), request, material) == nil || receiveBootstrapIdentityMaterial(context.Background(), identityRuntimeRequestFixture(), nil) == nil {
		t.Fatal("identity receiver accepted non-delivery action or missing material")
	}
	if files := material.Files(); len(files) != 4 || files[engineidentity.EngineSecretPath] == nil {
		t.Fatal("receiver changed in-memory material")
	}
}

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
