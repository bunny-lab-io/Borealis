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
	"path/filepath"
	"regexp"
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
	for _, mode := range []string{"inspect progress", "inspect remote failure", "inspect missing cursor", "reconcile_source_absent", "deliver", "reconcile", "prepare_runtime_identity", "complete_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity", "prepare_prejoin_host", "complete_prejoin_host", "complete_prejoin_firewall", "resume_fresh_prejoin_host", "resume_repository_prejoin_host", "reconcile_prejoin_host", "publish_prejoin_artifacts", "reconcile_prejoin_artifacts", "install_prejoin_k3s", "reconcile_prejoin_k3s", "configure_prejoin_k3s", "reconcile_prejoin_k3s_configuration", "start_prepared_k3s", "reconcile_prepared_k3s_start", "verify_prepared_images", "reconcile_prepared_images", "activate_prejoin_identity", "reconcile_prejoin_identity_activation", "native rejection", "authority rejection", "truncated upload", "producer failure", "remote failure", "stderr overflow", "wrong host pin", "missing inputs"} {
		t.Run(mode, func(t *testing.T) {
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
				if artifactPreparationAction(mode) {
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
				if artifactPreparationAction(mode) {
					serve = clusterbootstrap.ServeArtifactPreparationSession
				}
				err := serve(context.Background(), pipeReader, outWriter, nonce,
					func(_ context.Context, request clusterbootstrap.PreparationSessionRequest) error {
						got, err := request.Expected()
						want := preparationRemoteExpected(server)
						if err != nil || got.Target != want.Target || got.Source != want.Source || request.Session.ManagerSHA256 != digest {
							return errors.New("private binding error")
						}
						if strings.Contains(mode, "prejoin") || artifactPreparationAction(mode) {
							bound := engineidentity.Binding{ClusterID: want.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
							if request.IdentitySource == nil || *request.IdentitySource != bound {
								return errors.New("wrong identity source")
							}
						} else if request.IdentitySource != nil {
							return errors.New("unexpected identity source")
						}
						if artifactPreparationAction(mode) && request.PostgresReference != clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("c", 64) {
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
						if mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(mode)) {
							wantAction = mode
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
				if err != nil || mode == "remote failure" || mode == "inspect remote failure" {
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
				if mode == "authority rejection" {
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
			if mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(mode)) {
				action = mode
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
				if strings.Contains(mode, "prejoin") || artifactPreparationAction(mode) {
					source = &engineidentity.Binding{ClusterID: expected.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
				}
				var progress clusterbootstrap.PreparationProgress
				var result *clusterbootstrap.PreparationProgress
				if strings.HasPrefix(mode, "inspect ") {
					result = &progress
				}
				pg := ""
				if artifactPreparationAction(mode) {
					pg = clusterbootstrap.PostgresImageRepository + "@sha256:" + strings.Repeat("c", 64)
				}
				err = client.transferPreparationArtifactResult(ctx, managerPath, digest, int64(len(manager)), expected, action, source, pg, []byte("private sudo fixture"), check, func(_ context.Context, output io.Writer) error {
					if mode == "producer failure" {
						return errors.New("private producer error")
					}
					_, err := io.Copy(output, bytes.NewReader(payload))
					return err
				}, result)
				if result != nil && (err == nil && progress.Step != "prepare_host" || err != nil && progress != (clusterbootstrap.PreparationProgress{})) {
					t.Fatal("progress escaped remote failure or was lost", progress, err)
				}
			}
			if (err == nil) != (mode == "inspect progress" || mode == "deliver" || mode == "reconcile" || mode == "reconcile_source_absent" || strings.HasSuffix(mode, "runtime_identity") || (strings.Contains(mode, "prejoin") || artifactPreparationAction(mode))) {
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
