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
	for _, mode := range []string{"deliver", "reconcile", "prepare_runtime_identity", "reconcile_runtime_identity", "prepare_prejoin_identity_root", "reconcile_prejoin_identity_root", "install_prejoin_identity", "reconcile_prejoin_identity", "prepare_prejoin_host", "reconcile_prejoin_host", "native rejection", "authority rejection", "truncated upload", "producer failure", "remote failure", "stderr overflow", "wrong host pin", "missing inputs"} {
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
				if nonce == "" || command != preparationCommand(int64(len(manager)), digest, nonce) || strings.Contains(command, "private sudo fixture") {
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
				err := clusterbootstrap.ServePreparationSession(context.Background(), pipeReader, outWriter, nonce,
					func(_ context.Context, request clusterbootstrap.PreparationSessionRequest) error {
						got, err := request.Expected()
						want := preparationRemoteExpected(server)
						if err != nil || got.Target != want.Target || got.Source != want.Source || request.Session.ManagerSHA256 != digest {
							return errors.New("private binding error")
						}
						if strings.Contains(mode, "prejoin") {
							bound := engineidentity.Binding{ClusterID: want.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
							if request.IdentitySource == nil || *request.IdentitySource != bound {
								return errors.New("wrong identity source")
							}
						} else if request.IdentitySource != nil {
							return errors.New("unexpected identity source")
						}
						if mode == "native rejection" {
							return errors.New("private native error")
						}
						return nil
					}, func(_ context.Context, request clusterbootstrap.PreparationSessionRequest, input io.Reader) error {
						wantAction := "stage_source"
						if mode == "reconcile" {
							wantAction = "reconcile_source"
						}
						if strings.HasSuffix(mode, "runtime_identity") || strings.Contains(mode, "prejoin") {
							wantAction = mode
						}
						if request.Action != wantAction {
							return errors.New("wrong action")
						}
						got, err := io.ReadAll(io.LimitReader(input, int64(len(payload))+1))
						if err != nil || !bytes.Equal(got, payload) {
							return errors.New("private payload mismatch")
						}
						consumed.Store(true)
						if mode == "stderr overflow" {
							_, _ = channel.Stderr().Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
							return errors.New("overflow")
						}
						return nil
					}, func(time.Time) error { return nil })
				if err != nil || mode == "remote failure" {
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
			if mode == "reconcile" {
				action = "reconcile_source"
			}
			if strings.HasSuffix(mode, "runtime_identity") || strings.Contains(mode, "prejoin") {
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
				if strings.Contains(mode, "prejoin") {
					source = &engineidentity.Binding{ClusterID: expected.Target.ClusterID, SourceID: "77777777-7777-4777-8777-777777777777", SourceUID: "88888888-8888-4888-8888-888888888888"}
				}
				err = client.transferPreparationWithIdentity(ctx, managerPath, digest, int64(len(manager)), expected, action, source, []byte("private sudo fixture"), check, func(_ context.Context, output io.Writer) error {
					if mode == "producer failure" {
						return errors.New("private producer error")
					}
					_, err := io.Copy(output, bytes.NewReader(payload))
					return err
				})
			}
			if (err == nil) != (mode == "deliver" || mode == "reconcile" || strings.HasSuffix(mode, "runtime_identity") || strings.Contains(mode, "prejoin")) {
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
