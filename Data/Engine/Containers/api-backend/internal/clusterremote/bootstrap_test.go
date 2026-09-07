package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func bootstrapBinding(server *fakeSSH) clusterbootstrap.SessionBinding {
	return clusterbootstrap.SessionBinding{ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333", HolderID: "44444444-4444-4444-8444-444444444444", Generation: 7, OperationAttempt: 2,
		Address: server.target.Address, Port: server.target.Port, Hostname: "engine-02", MachineID: strings.Repeat("a", 32), HostKeyAlgorithm: server.key.Algorithm, HostKeyFingerprint: server.key.Fingerprint}
}

// Real SSH transport and stream boundaries; receiver/Git/ELF validation has its
// own real compiled-bundle tests. Only this package-private seam accepts fixture
// bytes instead of a verified Bundle. No fixture executable runs on a lab host.
func TestBootstrapSSHDeliveryAndAuthorityFences(t *testing.T) {
	manager := bytes.Repeat([]byte("immutable executable fixture\x00"), 10000)
	hash := sha256.Sum256(manager)
	digest := hex.EncodeToString(hash[:])
	expected := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	path := filepath.Join(t.TempDir(), "manager")
	if err := os.WriteFile(path, manager, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "initial fence", "ready fence", "challenge fence", "completion fence", "replayed challenge", "wrong binding", "wrong source", "premature completion", "trailing output", "remote failure", "stderr secret", "overflow", "disconnect", "hang"} {
		t.Run(mode, func(t *testing.T) {
			var server *fakeSSH
			var checks atomic.Int32
			// Assign before connecting; fixture starts accepting only after return.
			server = newFakeSSH(t, "bootstrap", func(command string, channel ssh.Channel) uint32 {
				var nonce string
				for _, match := range regexp.MustCompile(`'([a-f0-9]{64})'`).FindAllStringSubmatch(command, -1) {
					if match[1] != digest {
						nonce = match[1]
					}
				}
				if nonce == "" || command != bootstrapCommand(int64(len(manager)), digest, nonce) || strings.Contains(command, "unused-sudo-fixture") {
					t.Error("command changed or included credential")
					return 1
				}
				input := bufio.NewReader(channel)
				received := make([]byte, len(manager))
				if _, err := io.ReadFull(input, received); err != nil || !bytes.Equal(received, manager) {
					return 1
				}
				password, err := input.ReadString('\n')
				if err != nil || password != "unused-sudo-fixture\n" {
					return 1
				}
				preamble, _ := clusterbootstrap.SessionPreamble(nonce)
				if line, err := input.ReadString('\n'); err != nil || line != preamble {
					return 1
				}
				if mode == "hang" {
					_, _ = io.Copy(io.Discard, input)
					return 1
				}
				if mode == "overflow" {
					_, _ = channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
					return 1
				}
				if mode == "disconnect" {
					return 1
				}
				if clusterbootstrap.WriteSessionFrame(channel, clusterbootstrap.SessionMessage{Protocol: 1, State: "ready", Nonce: nonce}) != nil {
					return 1
				}
				var request clusterbootstrap.SessionRequest
				if clusterbootstrap.ReadSessionFrame(input, &request) != nil {
					return 1
				}
				if request.Validate() != nil || request.Binding != bootstrapBinding(server) || request.SourceSHA != expected.SourceSHA || request.ManagerSHA256 != digest {
					t.Error("request lost exact binding")
					return 1
				}
				if mode != "premature completion" {
					for n := 0; n < 2; n++ {
						challenge := strings.Repeat("d", 64)
						if n == 1 && mode != "replayed challenge" {
							challenge = strings.Repeat("e", 64)
						}
						if clusterbootstrap.WriteSessionFrame(channel, clusterbootstrap.SessionMessage{Protocol: 1, State: "challenge", Nonce: challenge}) != nil {
							return 1
						}
						var heartbeat clusterbootstrap.SessionMessage
						if clusterbootstrap.ReadSessionFrame(input, &heartbeat) != nil {
							return 1
						}
						if heartbeat != (clusterbootstrap.SessionMessage{Protocol: 1, State: "heartbeat", Nonce: challenge}) {
							t.Error("invalid heartbeat")
							return 1
						}
					}
				}
				binding := request.Binding
				if mode == "wrong binding" {
					binding.Generation++
				}
				message := clusterbootstrap.SessionMessage{Protocol: 1, State: "verified", Nonce: nonce, Binding: &binding, SourceSHA: expected.SourceSHA, ManagerSHA256: digest}
				if mode == "wrong source" {
					message.SourceSHA = strings.Repeat("b", 40)
				}
				if clusterbootstrap.WriteSessionFrame(channel, message) != nil {
					return 1
				}
				if mode == "trailing output" {
					_, _ = channel.Write([]byte("unexpected"))
				}
				if mode == "stderr secret" {
					_, _ = channel.Stderr().Write(server.password)
					return 1
				}
				if mode == "remote failure" {
					return 1
				}
				return 0
			})
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := server.transport.Connect(ctx, server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if mode == "hang" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			check := func(context.Context) error {
				n := checks.Add(1)
				if (mode == "initial fence" && n == 1) || (mode == "ready fence" && n == 2) || (mode == "challenge fence" && n == 3) || (mode == "completion fence" && n == 5) {
					return errors.New("private ownership diagnostic")
				}
				return nil
			}
			secret := []byte("unused-sudo-fixture")
			result, err := client.verifyBootstrapExecutable(ctx, path, expected, digest, int64(len(manager)), bootstrapBinding(server), secret, check)
			if mode == "normal" {
				if err != nil || result.Binding == nil || *result.Binding != bootstrapBinding(server) || checks.Load() != 5 {
					t.Fatalf("valid session failed: %v checks=%d", err, checks.Load())
				}
			} else if err != ErrBootstrapVerification || result != (clusterbootstrap.SessionMessage{}) {
				t.Fatalf("unsafe completion accepted: %v", err)
			}
			if string(secret) != "unused-sudo-fixture" {
				t.Error("caller secret changed")
			}
			if mode == "initial fence" && server.execCalls.Load() != 0 {
				t.Error("lost owner executed SSH command")
			}
		})
	}
}

func TestBootstrapPublicDeliveryRequiresVerifiedBundle(t *testing.T) {
	server := newFakeSSH(t, "normal")
	credential, _ := PasswordCredential("operator", server.password)
	defer credential.Destroy()
	client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, bundle := range []*clusterbootstrap.Bundle{nil, {}} {
		if _, err := client.VerifyBootstrap(context.Background(), bundle, bootstrapBinding(server), nil, func(context.Context) error { t.Error("unverified bundle reached authority check"); return nil }); err != ErrBootstrapVerification {
			t.Fatal("unverified bundle accepted")
		}
	}
	if server.execCalls.Load() != 0 {
		t.Fatal("unverified bundle reached remote execution")
	}
}

func TestBootstrapRootCopyChecksBeforeExecutingAndCleansScratch(t *testing.T) {
	for _, mode := range []string{"valid", "wrong digest", "symlink", "wrong size", "child failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "executed")
			payload := "#!/bin/sh\nset -eu\ntest \"$1\" = bootstrap-session\ntest \"$2\" = --nonce\ntest \"$3\" = " + strings.Repeat("c", 64) + "\nprintf invoked > '" + marker + "'\ncat\n"
			if mode == "child failure" {
				payload += "exit 1\n"
			}
			path := filepath.Join(root, "source")
			if err := os.WriteFile(path, []byte(payload), 0o500); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(payload))
			digest, size := hex.EncodeToString(hash[:]), int64(len(payload))
			if mode == "wrong digest" {
				digest = strings.Repeat("a", 64)
			}
			if mode == "wrong size" {
				size++
			}
			if mode == "symlink" {
				target := filepath.Join(root, "link")
				if err := os.Symlink(path, target); err != nil {
					t.Fatal(err)
				}
				path = target
			}
			script := strings.ReplaceAll(bootstrapRootScript, "/var/tmp/borealis-bootstrap-root.XXXXXXXXXX", root+"/scratch.XXXXXXXXXX")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", "-c", script, "bootstrap-root", path, digest, strings.Repeat("c", 64), strconv.FormatInt(size, 10))
			input := "$(touch '" + filepath.Join(root, "injected") + "')\nprivate stdin\n"
			command.Stdin = strings.NewReader(input)
			out, err := command.Output()
			if mode == "valid" && (err != nil || string(out) != input) {
				t.Fatalf("fixed root copy failed: %v", err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("invalid copy/child accepted")
			}
			_, markerErr := os.Stat(marker)
			if (mode == "valid" || mode == "child failure") != (markerErr == nil) {
				t.Fatal("executable ran before proof or failed to run")
			}
			if _, err := os.Stat(filepath.Join(root, "injected")); !os.IsNotExist(err) {
				t.Fatal("stdin executed as shell source")
			}
			remaining, err := filepath.Glob(root + "/scratch.*")
			if err != nil || len(remaining) != 0 {
				t.Fatal("private scratch leaked")
			}
		})
	}
}

func TestBootstrapFixedShellPreservesBinaryAndPasswordBoundaries(t *testing.T) {
	root := t.TempDir()
	// Root selection alone is replaced so portable tests run without sudo. Both
	// production shell texts, checksum/copy steps and stdin routing stay intact.
	payload := []byte("#!/bin/sh\nset -eu\ntest \"$1\" = bootstrap-session\ntest \"$2\" = --nonce\ncat\n")
	hash := sha256.Sum256(payload)
	nonce := strings.Repeat("c", 64)
	command := bootstrapCommand(int64(len(payload)), hex.EncodeToString(hash[:]), nonce)
	command = strings.ReplaceAll(command, "if [ \"$(id -u)\" = 0 ]; then", "if true; then")
	command = strings.ReplaceAll(command, "/var/tmp/borealis-", root+"/borealis-")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shell := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	preamble, _ := clusterbootstrap.SessionPreamble(nonce)
	tail := "$(touch '" + filepath.Join(root, "injected") + "'); inert password\n" + preamble
	shell.Stdin = io.MultiReader(bytes.NewReader(payload), strings.NewReader(tail))
	out, err := shell.Output()
	if err != nil || string(out) != tail {
		t.Fatalf("fixed binary/password stream lost framing: %v", err)
	}
	remaining, err := os.ReadDir(root)
	if err != nil || len(remaining) != 0 {
		t.Fatal("scratch leaked or stdin executed")
	}
}
