package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
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

func preparationRemoteExpected(server *fakeSSH) clusterbootstrap.PreparationExpected {
	return clusterbootstrap.PreparationExpected{
		Source: clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true},
		Target: bootstrapBinding(server), CohortSHA256: strings.Repeat("b", 64), TargetBootID: "55555555-5555-4555-8555-555555555555",
		KubeSystemUID: "66666666-6666-4666-8666-666666666666", ControlPlaneVIP: "192.168.50.5", EdgeVIP: "192.168.50.6",
		ManagementCIDR: "192.168.50.0/24", K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16",
		PeerAddresses: []string{"192.168.50.10", "192.168.50.11", "192.168.50.12"},
	}
}

func TestPreparationPinnedSSH(t *testing.T) {
	manager := bytes.Repeat([]byte("verified helper fixture"), 1000)
	hash := sha256.Sum256(manager)
	digest := hex.EncodeToString(hash[:])
	path := filepath.Join(t.TempDir(), "manager")
	if err := os.WriteFile(path, manager, 0600); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("private configuration fixture"), 2000)
	for _, mode := range []string{"inspect progress", "inspect remote failure", "inspect missing cursor", "reconcile_source_absent", "deliver", "reconcile", "prepare_runtime_identity", "complete_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity", "prepare_prejoin_host", "complete_prejoin_host", "complete_prejoin_firewall", "resume_fresh_prejoin_host", "resume_repository_prejoin_host", "reconcile_prejoin_host", "publish_prejoin_artifacts", "reconcile_prejoin_artifacts", "install_prejoin_k3s", "reconcile_prejoin_k3s", "configure_prejoin_k3s", "reconcile_prejoin_k3s_configuration", "start_prepared_k3s", "reconcile_prepared_k3s_start", "verify_prepared_images", "reconcile_prepared_images", "activate_prejoin_identity", "reconcile_prejoin_identity_activation", "completion remote failure", "completion authority failure", "completion missing", "completion malformed", "native rejection", "authority rejection", "truncated upload", "producer failure", "remote failure", "stderr overflow", "wrong host pin", "missing inputs"} {
		t.Run(mode, func(t *testing.T) {
			phase := mode
			if strings.HasPrefix(mode, "completion ") {
				phase = "reconcile_prejoin_identity_activation"
			}
			var server *fakeSSH
			var consumed atomic.Bool
			server = newFakeSSH(t, "preparation", func(command string, channel ssh.Channel) uint32 {
				var nonce string
				for _, match := range regexp.MustCompile(`'([a-f0-9]{64})'`).FindAllStringSubmatch(command, -1) {
					if match[1] != digest {
						nonce = match[1]
					}
				}
				wantCommand := preparationCommand(int64(len(manager)), digest, nonce)
				if artifactPreparationAction(phase) {
					wantCommand = artifactPreparationCommand(int64(len(manager)), digest, nonce)
				}
				if nonce == "" || command != wantCommand || strings.Contains(command, "private sudo fixture") {
					t.Error("unbound command")
					return 1
				}
				reader := bufio.NewReader(channel)
				upload := make([]byte, len(manager))
				if _, err := io.ReadFull(reader, upload); err != nil || !bytes.Equal(upload, manager) {
					return 1
				}
				if line, err := reader.ReadString('\n'); err != nil || line != "private sudo fixture\n" {
					return 1
				}
				pipeReader, pipeWriter := io.Pipe()
				outReader, outWriter := io.Pipe()
				copied := make(chan struct{})
				outputDone := make(chan struct{})
				go func() { defer close(copied); _, _ = io.Copy(pipeWriter, reader); pipeWriter.Close() }()
				go func() {
					defer close(outputDone)
					_, _ = io.Copy(channel, outReader)
					channel.CloseWrite()
					outReader.Close()
				}()
				defer func() { pipeReader.Close(); outWriter.Close(); <-copied; <-outputDone }()
				serve := clusterbootstrap.ServePreparationSessionWithProgress
				if artifactPreparationAction(phase) {
					serve = func(ctx context.Context, in io.ReadCloser, out io.WriteCloser, nonce string, verify func(context.Context, clusterbootstrap.PreparationSessionRequest) error, receive clusterbootstrap.PreparationSessionProgressReceiver, observe func(time.Time) error) error {
						return clusterbootstrap.ServeArtifactPreparationSessionWithResult(ctx, in, out, nonce, verify, func(ctx context.Context, r clusterbootstrap.PreparationSessionRequest, input io.Reader) (clusterbootstrap.PreparationSessionResult, error) {
							progress, err := receive(ctx, r, input)
							result := clusterbootstrap.PreparationSessionResult{Progress: progress}
							if r.Action == "reconcile_prejoin_identity_activation" && mode != "completion missing" {
								expected, _ := r.Expected()
								receipt := sshPrejoinReceiptFixture(expected, *r.IdentitySource)
								if mode == "completion malformed" {
									receipt.MemberID++
								}
								result.Completion = &receipt
							}
							return result, err
						}, observe)
					}
				}
				err := serve(context.Background(), pipeReader, outWriter, nonce,
					func(_ context.Context, request clusterbootstrap.PreparationSessionRequest) error {
						got, err := request.Expected()
						want := preparationRemoteExpected(server)
						if err != nil || got.Target != want.Target || got.Source != want.Source || request.Session.ManagerSHA256 != digest {
							return errors.New("private binding error")
						}
						if strings.Contains(mode, "prejoin") || artifactPreparationAction(phase) {
							bound := engineidentity.Binding{ClusterID: want.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
							if request.IdentitySource == nil || *request.IdentitySource != bound {
								return errors.New("wrong identity source")
							}
						} else if request.IdentitySource != nil {
							return errors.New("unexpected identity source")
						}
						if artifactPreparationAction(phase) && request.PostgresReference != clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("c", 64) {
							return errors.New("wrong postgres source")
						}
						if mode == "native rejection" {
							return errors.New("private native error")
						}
						return nil
					}, func(_ context.Context, request clusterbootstrap.PreparationSessionRequest, input io.Reader) (clusterbootstrap.PreparationProgress, error) {
						wantAction := "stage_source"
						if strings.HasPrefix(mode, "inspect ") {
							wantAction = "inspect_preparation_progress"
						}
						if mode == "reconcile" {
							wantAction = "reconcile_source"
						}
						if mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(phase)) {
							wantAction = phase
						}
						if request.Action != wantAction {
							return clusterbootstrap.PreparationProgress{}, errors.New("wrong action")
						}
						got, err := io.ReadAll(io.LimitReader(input, int64(len(payload))+1))
						if err != nil || !bytes.Equal(got, payload) {
							return clusterbootstrap.PreparationProgress{}, errors.New("private payload mismatch")
						}
						consumed.Store(true)
						if mode == "stderr overflow" {
							_, _ = channel.Stderr().Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
							return clusterbootstrap.PreparationProgress{}, errors.New("overflow")
						}
						if strings.HasPrefix(mode, "inspect ") && mode != "inspect missing cursor" {
							return clusterbootstrap.PreparationProgress{Step: "prepare_host"}, nil
						}
						return clusterbootstrap.PreparationProgress{}, nil
					}, func(time.Time) error { return nil })
				if err != nil || mode == "remote failure" || mode == "inspect remote failure" || mode == "completion remote failure" {
					return 1
				}
				return 0
			})
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := server.transport.Connect(ctx, server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			expected := preparationRemoteExpected(server)
			check := func(context.Context) error {
				if mode == "authority rejection" || mode == "completion authority failure" && consumed.Load() {
					return errors.New("private authority error")
				}
				return nil
			}
			action := "stage_source"
			if strings.HasPrefix(mode, "inspect ") {
				action = "inspect_preparation_progress"
			}
			if mode == "reconcile" {
				action = "reconcile_source"
			}
			if mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(phase)) {
				action = phase
			}
			if mode == "wrong host pin" {
				expected.Target.HostKeyFingerprint = "SHA256:" + strings.Repeat("A", 43)
			}
			if mode == "wrong host pin" || mode == "missing inputs" {
				err = client.DeliverPreparation(ctx, nil, expected, []byte("private sudo fixture"), check)
			} else {
				managerPath := path
				if mode == "truncated upload" {
					managerPath = filepath.Join(t.TempDir(), "absent")
				}
				var source *engineidentity.Binding
				if strings.Contains(mode, "prejoin") || artifactPreparationAction(phase) {
					source = &engineidentity.Binding{ClusterID: expected.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
				}
				var progress clusterbootstrap.PreparationProgress
				var result *clusterbootstrap.PreparationProgress
				if strings.HasPrefix(mode, "inspect ") {
					result = &progress
				}
				pg := ""
				if artifactPreparationAction(phase) {
					pg = clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("c", 64)
				}
				var completion clusterbootstrap.PrejoinPreparationReceipt
				var completionOut *clusterbootstrap.PrejoinPreparationReceipt
				if phase == "reconcile_prejoin_identity_activation" {
					completionOut = &completion
				}
				err = client.transferPreparationCompletion(ctx, managerPath, digest, int64(len(manager)), expected, action, source, pg, []byte("private sudo fixture"), check, func(_ context.Context, output io.Writer) error {
					if mode == "producer failure" {
						return errors.New("private producer error")
					}
					_, err := io.Copy(output, bytes.NewReader(payload))
					return err
				}, result, completionOut)
				if completionOut != nil && (err == nil && completion.Validate(expected, *source) != nil || err != nil && completion != (clusterbootstrap.PrejoinPreparationReceipt{})) {
					t.Fatal("receipt escaped remote exit or authority failure")
				}
				if result != nil && (err == nil && progress.Step != "prepare_host" || err != nil && progress != (clusterbootstrap.PreparationProgress{})) {
					t.Fatal("progress escaped remote failure or was lost", progress, err)
				}
			}
			if (err == nil) != (!strings.HasPrefix(mode, "completion ") && (mode == "inspect progress" || mode == "deliver" || mode == "reconcile" || mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(phase)))) {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && err != ErrBootstrapVerification {
				t.Fatal("private error escaped", err)
			}
			if err == nil && !consumed.Load() {
				t.Fatal("successful result without complete payload")
			}
		})
	}
}

func TestArtifactPreparationCommandBoundaries(t *testing.T) {
	digest, nonce := strings.Repeat("a", 64), strings.Repeat("b", 64)
	command := artifactPreparationCommand(123, digest, nonce)
	quotedSudo := shellConstant("sudo -k -S -p ''")
	for _, required := range []string{"bootstrap-artifact-session --nonce", "--kill-after=2s 32m", quotedSudo[1 : len(quotedSudo)-1], digest, nonce} {
		if !strings.Contains(command, required) {
			t.Fatal("artifact command missing fixed boundary", required)
		}
	}
	if strings.Count(command, "--kill-after=2s 32m") != 2 || strings.Contains(command, "--kill-after=2s 6m") || strings.Contains(preparationCommand(123, digest, nonce), "32m") {
		t.Fatal("artifact lifetime leaked across command classes")
	}
}

func TestPreparationWrapperOwnsScratchAfterHelperExit(t *testing.T) {
	for _, artifacts := range []bool{false, true} {
		verb := "bootstrap-source-session"
		if artifacts {
			verb = "bootstrap-artifact-session"
		}
		t.Run(verb, func(t *testing.T) {
			for _, mode := range []string{"success", "helper exit", "helper killed", "unowned control", "wrong digest", "unsafe opt", "symlink opt", "missing opt"} {
				t.Run(mode, func(t *testing.T) {
					root := t.TempDir()
					opt, upload := filepath.Join(root, "opt"), filepath.Join(root, "upload")
					for _, dir := range []string{opt, upload} {
						if err := os.Mkdir(dir, 0755); err != nil {
							t.Fatal(err)
						}
					}
					// Similar names from other invocations must never be glob-cleaned.
					retained := filepath.Join(opt, "borealis-bootstrap-root.retained")
					if err := os.Mkdir(retained, 0700); err != nil {
						t.Fatal(err)
					}
					marker := filepath.Join(root, "executed")
					payload := "#!/bin/sh\nset -eu\ntest \"$1\" = " + verb + "\ntest \"$2\" = --nonce\n" +
						"scratch=$(dirname -- \"$0\")/borealis-node-bootstrap-partial\n"
					if mode == "unowned control" {
						// Reproduce the former direct-/opt receiver: helper death leaves
						// scratch outside the wrapper's exact cleanup ownership.
						payload += "scratch=" + shellConstant(filepath.Join(opt, "borealis-node-bootstrap-unowned")) + "\n"
					}
					payload += "mkdir -- \"$scratch\"\nprintf partial > \"$scratch/archive\"\nprintf executed > " + shellConstant(marker) + "\ncat\n"
					switch mode {
					case "helper exit":
						payload += "exit 1\n"
					case "helper killed", "unowned control":
						payload += "kill -KILL $$\n"
					}
					digest := sha256.Sum256([]byte(payload))
					hash := hex.EncodeToString(digest[:])
					if mode == "wrong digest" {
						hash = strings.Repeat("a", 64)
					}
					command := preparationCommand(int64(len(payload)), hash, strings.Repeat("b", 64))
					if artifacts {
						command = artifactPreparationCommand(int64(len(payload)), hash, strings.Repeat("b", 64))
					}
					original := preparationRootScript(artifacts)
					// Only isolated paths, fixture owner UID and sudo selection change.
					// Actual copy/hash, child execution and traps run in both shells.
					fixture := strings.Replace(original, "test \"$(stat -c '%u' -- /opt)\" = 0", "test \"$(stat -c '%u' -- /opt)\" = "+strconv.Itoa(os.Geteuid()), 1)
					fixture = strings.ReplaceAll(fixture, "/opt", opt)
					// The root script is itself quoted inside the outer shell text.
					quotedOriginal, quotedFixture := shellConstant(shellConstant(original)), shellConstant(shellConstant(fixture))
					quotedOriginal, quotedFixture = quotedOriginal[1:len(quotedOriginal)-1], quotedFixture[1:len(quotedFixture)-1]
					if strings.Count(command, quotedOriginal) != 2 {
						t.Fatal("production wrapper composition changed")
					}
					command = strings.ReplaceAll(command, quotedOriginal, quotedFixture)
					command = strings.ReplaceAll(command, "/var/tmp/borealis-", upload+"/borealis-")
					command = strings.ReplaceAll(command, "if [ \"$(id -u)\" = 0 ]; then", "if true; then")
					switch mode {
					case "unsafe opt":
						if err := os.Chmod(opt, 0777); err != nil {
							t.Fatal(err)
						}
					case "symlink opt", "missing opt":
						if err := os.Rename(opt, opt+"-retained"); err != nil {
							t.Fatal(err)
						}
						if mode == "symlink opt" {
							if err := os.Symlink(opt+"-retained", opt); err != nil {
								t.Fatal(err)
							}
						}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					shell := exec.CommandContext(ctx, "/bin/sh", "-c", command)
					shell.Stdin = io.MultiReader(strings.NewReader(payload), strings.NewReader("inert protocol bytes\n"))
					output, err := shell.Output()
					if ctx.Err() != nil || (err == nil) != (mode == "success") {
						t.Fatal("unexpected wrapper result", err, ctx.Err())
					}
					ran := mode == "success" || mode == "helper exit" || mode == "helper killed" || mode == "unowned control"
					_, markerErr := os.Stat(marker)
					if ran != (markerErr == nil) || ran && string(output) != "inert protocol bytes\n" {
						t.Fatal("helper execution or input framing mismatch", markerErr)
					}
					if mode == "symlink opt" || mode == "missing opt" {
						opt += "-retained"
					}
					entries, err := os.ReadDir(opt)
					want := 1
					if mode == "unowned control" {
						want++
					}
					if err != nil || len(entries) != want {
						t.Fatal("wrapper scratch leaked or unrelated scratch removed", entries, err)
					}
					if _, err := os.Stat(filepath.Join(opt, "borealis-bootstrap-root.retained")); err != nil {
						t.Fatal("unrelated invocation removed", err)
					}
					entries, err = os.ReadDir(upload)
					if err != nil || len(entries) != 0 {
						t.Fatal("upload scratch leaked", entries, err)
					}
				})
			}
		})
	}
}
