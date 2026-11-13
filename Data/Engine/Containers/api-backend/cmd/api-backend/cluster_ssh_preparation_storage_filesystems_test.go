package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

type sshPreparationFilesystemTestClient struct {
	*clusterremote.Client
	inspect    func(context.Context, clusterremote.PreparationProposal, func(context.Context) error) (clusterremote.PreparationProposalProof, error)
	filesystem func(context.Context, []byte, clusterremote.Target, clusterremote.HostKey, clusterremote.FilesystemRequest, func(context.Context) error) (clusterremote.TargetFilesystem, error)
	closed     func() error
}

func (c *sshPreparationFilesystemTestClient) InspectPreparationProposal(ctx context.Context, _ []byte, request clusterremote.PreparationProposal, check func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
	return c.inspect(ctx, request, check)
}
func (c *sshPreparationFilesystemTestClient) InspectFilesystem(ctx context.Context, sudo []byte, target clusterremote.Target, key clusterremote.HostKey, request clusterremote.FilesystemRequest, check func(context.Context) error) (clusterremote.TargetFilesystem, error) {
	return c.filesystem(ctx, sudo, target, key, request, check)
}
func (c *sshPreparationFilesystemTestClient) Close() error {
	return errors.Join(c.Client.Close(), c.closed())
}

// The clean proposal seam remains the existing protocol double. Filesystem
// evidence comes through the real pinned SSH client and its opaque accessor.
func sshPreparationFilesystemConnect(t *testing.T, snapshot clusterSSHPreparationApprovalSnapshot, keys map[string]ssh.Signer,
	envelopes []clusterSSHCredentialEnvelope, wire func(int, []byte) []byte, opened func(context.Context, int, *sshPreparationFilesystemTestClient)) clusterSSHPreparationProposalConnect {
	t.Helper()
	return func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
		index := slices.IndexFunc(snapshot.work.Proof.Cohort.Targets, func(item clusterSSHInspectedTarget) bool {
			return item.Binding.Address == target.Address && item.Binding.Port == target.Port
		})
		if index < 0 {
			return nil, errClusterConflict
		}
		item := snapshot.work.Proof.Cohort.Targets[index]
		observation := bytes.Replace(sshFilesystemFixtureWire(item), []byte(`"version":3`), []byte(`"version":4`), 1)
		if wire != nil {
			observation = wire(index, observation)
		}
		link := clusterbootstrap.ManagementLink{Interface: "ens18", Index: 2, Address: item.Binding.Address + "/24", MAC: "02:00:00:00:00:01", NetworkNamespace: 1234}
		username, password, sudo := "operator", "synthetic-password", "fixture-sudo"
		if envelopes != nil {
			username, password, sudo = envelopes[index].Username, envelopes[index].Password, envelopes[index].SudoPassword
		}
		transport, _, _ := sshNetworkFixtureTransportWithSudo(t, item, keys[item.Binding.TargetID], link, nil, observation, username, password, sudo)
		client, err := transport.Connect(ctx, target, key, credential)
		if err != nil {
			return nil, err
		}
		value := &sshPreparationFilesystemTestClient{Client: client,
			inspect: func(ctx context.Context, request clusterremote.PreparationProposal, check func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
				if request.Validate() != nil || check(ctx) != nil {
					return clusterremote.PreparationProposalProof{}, errClusterConflict
				}
				return sshPreparationProposalTestProof(request, strings.Repeat("a", 64)), nil
			}, filesystem: client.InspectFilesystem, closed: func() error { return nil }}
		if opened != nil {
			opened(ctx, index, value)
		}
		return value, nil
	}
}

func TestClusterSSHPreparationStorageFilesystemsNative(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "copy", "free varies", "bad paths", "mount drift", "old native", "boot drift", "clean drift", "zero native", "cached native", "close error", "authority lost", "cancel", "lower next availability"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, aegis := sshPreparationCleanTargetsFixture(t, mode == "replacement")
			keys := sshNetworkFixtureKeys(t, &snapshot.work.Proof.Cohort)
			for i, target := range snapshot.work.Proof.Cohort.Targets {
				snapshot.credentials[i].binding = target.Binding
			}
			open := aegis.open
			aegis.open = func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
				value, err := open(ctx, sealed, binding)
				value.SudoPassword = "fixture-sudo"
				return value, err
			}
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			paths := []string{"/opt/Borealis", "/var/lib/longhorn"}
			if mode == "bad paths" {
				paths = []string{"/opt/../Borealis"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var connections, closes, cleanReads, fsReads atomic.Int32
			var lost atomic.Bool
			check := func(ctx context.Context) error {
				if lost.Load() {
					return errClusterConflict
				}
				return ctx.Err()
			}
			cached := map[int]clusterremote.TargetFilesystem{}
			cohort := len(snapshot.credentials)
			connect := sshPreparationFilesystemConnect(t, snapshot, keys, nil, func(index int, raw []byte) []byte {
				round := int(connections.Load()) / cohort
				if mode == "mount drift" && round == 1 {
					raw = bytes.ReplaceAll(raw, []byte(`"mount_id":29`), []byte(`"mount_id":30`))
				}
				if mode == "free varies" && round%2 == 1 || mode == "lower next availability" && round >= 2 {
					raw = bytes.Replace(raw, []byte(`"available_bytes":700000`), []byte(`"available_bytes":600000`), 1)
				}
				if mode == "old native" {
					raw = bytes.Replace(raw, []byte(`"version":4`), []byte(`"version":3`), 1)
				}
				if mode == "boot drift" {
					raw = bytes.ReplaceAll(raw, []byte(snapshot.work.Proof.Cohort.Targets[index].Report.BootID), []byte(newClusterUUID()))
				}
				return raw
			}, func(_ context.Context, index int, client *sshPreparationFilesystemTestClient) {
				connections.Add(1)
				inspect := client.inspect
				localReads := 0
				client.inspect = func(ctx context.Context, request clusterremote.PreparationProposal, check func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
					cleanReads.Add(1)
					localReads++
					value, err := inspect(ctx, request, check)
					if mode == "clean drift" && localReads == 2 {
						value.ObservationSHA256 = strings.Repeat("b", 64)
					}
					return value, err
				}
				filesystem := client.filesystem
				client.filesystem = func(ctx context.Context, sudo []byte, target clusterremote.Target, key clusterremote.HostKey, request clusterremote.FilesystemRequest, check func(context.Context) error) (clusterremote.TargetFilesystem, error) {
					fsReads.Add(1)
					if !request.RequirePersistent || !slices.Equal(request.Paths, []string{"/opt/Borealis", "/var/lib/longhorn"}) {
						t.Error("filesystem escaped fixed persistent request")
					}
					if mode == "zero native" {
						return clusterremote.TargetFilesystem{}, nil
					}
					if value, exists := cached[index]; mode == "cached native" && exists {
						return value, nil
					}
					value, err := filesystem(ctx, sudo, target, key, request, check)
					cached[index] = value
					if mode == "authority lost" {
						lost.Store(true)
					}
					if mode == "cancel" {
						cancel()
					}
					if mode == "copy" {
						request.Paths[0] = "/foreign"
					}
					return value, err
				}
				client.closed = func() error {
					closes.Add(1)
					if mode == "close error" {
						return errors.New("synthetic SSH close error")
					}
					return nil
				}
			})
			read := newClusterSSHPreparationTargetsFilesystemRead(snapshot, check, aegis, connect, paths)
			if mode == "copy" {
				paths[0] = "/foreign"
			}
			value, err := read(ctx, plan)
			positive := mode == "pair" || mode == "replacement" || mode == "copy" || mode == "free varies" || mode == "lower next availability"
			if (err == nil) != positive || err != nil && (value.Digest != "" || len(value.Filesystems) != 0) {
				t.Fatal("unexpected filesystem observation", err)
			}
			if positive {
				if !validClusterSSHPreparationFilesystems(snapshot, []string{"/opt/Borealis", "/var/lib/longhorn"}, value.Filesystems) || fsReads.Load() != int32(2*cohort) || cleanReads.Load() != int32(4*cohort) {
					t.Fatal("whole cohort or clean bracketing missing")
				}
				available := uint64(700000)
				if mode == "free varies" {
					available = 600000
				}
				if value.Filesystems[0].Storage.Filesystems[0].AvailableBytes != available {
					t.Fatal("availability did not retain conservative minimum")
				}
				value.Filesystems[0].Storage.Filesystems[0].AvailableBytes = 1
				value.Filesystems[0].Storage.Paths[0].Path = "/foreign"
				again, err := read(ctx, plan)
				if mode == "lower next availability" {
					available = 600000
				}
				if err != nil || again.Filesystems[0].Storage.Filesystems[0].AvailableBytes != available || again.Filesystems[0].Storage.Paths[0].Path != "/opt/Borealis" {
					t.Fatal("caller mutated retained filesystem proof", err)
				}
			} else {
				before := connections.Load()
				if _, err := read(context.Background(), plan); err == nil || connections.Load() != before {
					t.Fatal("failed filesystem reader revived")
				}
			}
			if connections.Load() != closes.Load() {
				t.Fatal("filesystem acquisition retained SSH")
			}
		})
	}
}

func TestClusterSSHPreparationStorageFilesystemsBrokerBinding(t *testing.T) {
	for _, mode := range []string{"success", "missing", "wrong target", "wrong paths", "wrong request", "mount drift", "lower free", "lower inodes", "close failure"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := sshPreparationCleanBrokerSnapshot(t)
			plan, _, _ := clusterSSHPreparationCleanBrokerBinding(snapshot)
			paths := []string{"/opt/Borealis", "/var/lib/longhorn"}
			var calls int
			var client *clusterSSHPreparationCleanBrokerClient
			client = sshPreparationCleanBrokerTestClient(t, bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(request.Body)
				_ = request.Body.Close()
				var input clusterSSHPreparationCleanBrokerRequest
				if openClusterSSHVIP(client.requestCipher, clusterSSHPreparationCleanBrokerPath, raw, &input, clusterSSHPreparationCleanBrokerLimit) != nil || !slices.Equal(input.FilesystemPaths, paths) {
					return nil, errClusterConflict
				}
				binding, _ := clusterSSHPreparationDigest(input)
				response := clusterSSHPreparationCleanBrokerResponse{Version: clusterSSHPreparationCleanBrokerVersion, ID: input.ID, RequestSHA256: binding, Targets: sshPreparationTargetCPUFixture(snapshot)}
				response.Digest, _ = clusterSSHPreparationTargetCPUDigest(snapshot, response.Targets)
				for _, target := range snapshot.work.Proof.Cohort.Targets {
					var parsed struct {
						Evidence clusterremote.FilesystemEvidence `json:"evidence"`
					}
					_ = json.Unmarshal(sshFilesystemFixtureWire(target), &parsed)
					response.Filesystems = append(response.Filesystems, clusterSSHTargetFilesystemEvidence{TargetID: target.Binding.TargetID, Storage: parsed.Evidence})
				}
				switch mode {
				case "missing":
					response.Filesystems = nil
				case "wrong target":
					response.Filesystems[0].TargetID = response.Filesystems[1].TargetID
				case "wrong paths":
					response.Filesystems[0].Storage.Paths[0].Path = "/foreign"
				case "wrong request":
					response.RequestSHA256 = strings.Repeat("f", 64)
				case "mount drift":
					if calls > 1 {
						response.Filesystems[0].Storage.Paths[0].MountID++
					}
				case "lower free":
					if calls > 1 {
						response.Filesystems[0].Storage.Filesystems[0].AvailableBytes--
					}
				case "lower inodes":
					if calls > 1 {
						response.Filesystems[0].Storage.Filesystems[0].AvailableInodes--
					}
				}
				raw, err := sealClusterSSHVIP(client.responseCipher, clusterSSHPreparationCleanBrokerPath, response, clusterSSHPreparationCleanBrokerLimit)
				if err != nil {
					return nil, err
				}
				body := &sshPreparationCleanBrokerBody{Reader: bytes.NewReader(raw), close: func() error {
					if mode == "close failure" {
						return errClusterConflict
					}
					return nil
				}}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{clusterSSHSourceBrokerMedia}}, Body: body, Request: request}, nil
			}))
			read := client.preparationTargetsFilesystemRead(snapshot, func(ctx context.Context) error { return ctx.Err() }, paths)
			value, err := read(context.Background(), plan)
			positive := mode == "success" || mode == "mount drift" || mode == "lower free" || mode == "lower inodes"
			if (err == nil) != positive {
				t.Fatal("wrong first bound result", err)
			}
			if positive {
				value.Filesystems[0].Storage.Paths[0].Path = "/foreign"
				value.Filesystems[0].Storage.Filesystems[0].AvailableBytes = 1
			}
			again, err := read(context.Background(), plan)
			if mode == "success" || mode == "lower free" || mode == "lower inodes" {
				available := uint64(700000)
				if mode == "lower free" {
					available--
				}
				if err != nil || again.Filesystems[0].Storage.Paths[0].Path != paths[0] || again.Filesystems[0].Storage.Filesystems[0].AvailableBytes != available {
					t.Fatal("bound result alias", err)
				}
				if mode == "lower inodes" {
					var parsed struct {
						Evidence clusterremote.FilesystemEvidence `json:"evidence"`
					}
					_ = json.Unmarshal(sshFilesystemFixtureWire(snapshot.work.Proof.Cohort.Targets[0]), &parsed)
					if again.Filesystems[0].Storage.Filesystems[0].AvailableInodes != parsed.Evidence.Filesystems[0].AvailableInodes-1 {
						t.Fatal("decreased inode minimum lost")
					}
				}
			} else if err == nil || again.Digest != "" {
				t.Fatal("failed/drifting response accepted")
			}
		})
	}
}

func TestClusterSSHPreparationStorageFilesystemsProjectionCopies(t *testing.T) {
	snapshot := sshPreparationCleanBrokerSnapshot(t)
	plan, _, _ := clusterSSHPreparationCleanBrokerBinding(snapshot)
	value := clusterSSHPreparationCleanObservation{Targets: sshPreparationTargetCPUFixture(snapshot)}
	value.Digest, _ = clusterSSHPreparationTargetCPUDigest(snapshot, value.Targets)
	for _, target := range snapshot.work.Proof.Cohort.Targets {
		var parsed struct {
			Evidence clusterremote.FilesystemEvidence `json:"evidence"`
		}
		_ = json.Unmarshal(sshFilesystemFixtureWire(target), &parsed)
		value.Filesystems = append(value.Filesystems, clusterSSHTargetFilesystemEvidence{TargetID: target.Binding.TargetID, Storage: parsed.Evidence})
	}
	clean, projection := clusterSSHPreparationTargetCPUReaderPair(snapshot, func(context.Context, string) (clusterSSHPreparationCleanObservation, error) { return value, nil })
	if _, err := clean(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	value.Filesystems[0].Storage.Paths[0].Path = "/caller-change"
	first, err := projection(context.Background(), plan)
	if err != nil || len(first.Filesystems) != len(snapshot.credentials) || first.Filesystems[0].Storage.Paths[0].Path != "/opt/Borealis" {
		t.Fatal("reader retained caller filesystem slices", err)
	}
	first.Filesystems[0].Storage.Filesystems[0].AvailableBytes = 1
	second, err := projection(context.Background(), plan)
	if err != nil || second.Filesystems[0].Storage.Filesystems[0].AvailableBytes != 700000 {
		t.Fatal("projection shared filesystem slices", err)
	}
}
