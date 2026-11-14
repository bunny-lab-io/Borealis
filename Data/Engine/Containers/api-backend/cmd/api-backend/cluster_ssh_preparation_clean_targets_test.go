package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type sshPreparationProposalTestAegis struct {
	open   func(context.Context, sealedClusterSSHCredentials, clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error)
	verify func(context.Context, string) error
}

func (a sshPreparationProposalTestAegis) openClusterSSHCredentials(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
	return a.open(ctx, sealed, binding)
}
func (a sshPreparationProposalTestAegis) verifyClusterSSHGeneration(ctx context.Context, generation string) error {
	return a.verify(ctx, generation)
}

type sshPreparationProposalTestClient struct {
	inspect func(context.Context, []byte, clusterremote.PreparationProposal, func(context.Context) error) (clusterremote.PreparationProposalProof, error)
	close   func() error
}

func (c *sshPreparationProposalTestClient) InspectPreparationProposal(ctx context.Context, sudo []byte, request clusterremote.PreparationProposal, check func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
	return c.inspect(ctx, sudo, request, check)
}
func (c *sshPreparationProposalTestClient) Close() error { return c.close() }

func sshPreparationCleanTargetsFixture(t *testing.T, replacement bool) (clusterSSHPreparationApprovalSnapshot, sshPreparationProposalTestAegis) {
	t.Helper()
	snapshot := clusterSSHPreparationApprovalSnapshot{work: sshPreparationConfigurationFixture(t, replacement)}
	for i, target := range snapshot.work.Proof.Cohort.Targets {
		sealed := sealedClusterSSHCredentials{binding: target.Binding, generation: "synthetic-generation", ciphertext: aegisEnvelopePrefix + "synthetic-" + target.Binding.TargetID}
		snapshot.credentials = append(snapshot.credentials, sealed)
		snapshot.work.Authorization.Targets[i].CredentialSHA256, _ = clusterSSHPreparationDigest([]string{sealed.generation, sealed.ciphertext})
	}
	aegis := sshPreparationProposalTestAegis{
		open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
			if sealed.binding != binding {
				return clusterSSHCredentialEnvelope{}, errClusterConflict
			}
			return clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Method: "password", Username: "operator", Password: "synthetic-password", SudoPassword: "synthetic-sudo"}, ctx.Err()
		},
		verify: func(ctx context.Context, generation string) error {
			if generation != "synthetic-generation" {
				return errClusterConflict
			}
			return ctx.Err()
		},
	}
	return snapshot, aegis
}

func sshPreparationProposalTestProof(request clusterremote.PreparationProposal, digest string) clusterremote.PreparationProposalProof {
	hash, _ := request.RequestSHA256()
	selection, err := clusterbootstrap.SelectHostPackages([]byte("apt\tamd64\tinstall ok installed\t2.7.14\ndpkg\tamd64\tinstall ok installed\t1.22.6\nbase-files\tamd64\tinstall ok installed\t13\n"))
	if err != nil {
		panic(err)
	}
	return clusterremote.PreparationProposalProof{Packages: &selection, Version: clusterremote.PreparationProposalVersion, RequestSHA256: hash, Nonce: request.Nonce, ObservationSHA256: digest, CPU: clusterbootstrap.PreparationTargetCPU{Version: 1, PlanSHA256: request.CPUPlanSHA256, OnlineCPUs: "0-15", AllocatableMilli: 16000, MemoryKiB: 33554432, ObservationSHA256: strings.Repeat("e", 64)}}
}

func sshPreparationTargetPackagesFixture(targets []clusterSSHPreparationTargetCPU) []clusterSSHPreparationTargetPackages {
	selection, err := clusterbootstrap.SelectHostPackages([]byte("apt\tamd64\tinstall ok installed\t2.7.14\ndpkg\tamd64\tinstall ok installed\t1.22.6\nbase-files\tamd64\tinstall ok installed\t13\n"))
	if err != nil {
		panic(err)
	}
	values := make([]clusterSSHPreparationTargetPackages, len(targets))
	for i, target := range targets {
		values[i] = clusterSSHPreparationTargetPackages{TargetID: target.TargetID, ObservationSHA256: target.ObservationSHA256, Selection: selection.Clone()}
	}
	return values
}

func TestClusterSSHPreparationCleanTargetsFreshCohort(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "copied inputs", "wrong plan", "claimed work", "missing credential", "wrong credential", "wrong generation", "wrong target", "controller mismatch", "open error", "open binding", "Aegis locked", "connect error", "close error", "native error", "wrong nonce", "wrong request", "bad digest", "changed cohort round", "changed next read", "missing packages", "null missing", "unknown package", "duplicate package", "wrong package lock", "changed package round", "changed package next read", "authority during native", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, aegis := sshPreparationCleanTargetsFixture(t, mode == "replacement")
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			expected := slices.Clone(snapshot.work.Proof.Cohort.Targets)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				cancel()
			}
			reads, connections, closes := 0, 0, 0
			lost := false
			check := func(ctx context.Context) error {
				if lost {
					return errClusterConflict
				}
				return ctx.Err()
			}
			originalOpen := aegis.open
			aegis.open = func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
				value, err := originalOpen(ctx, sealed, binding)
				if mode == "open error" {
					return value, errClusterConflict
				}
				if mode == "open binding" {
					value.Binding.TargetID = newClusterUUID()
				}
				return value, err
			}
			if mode == "Aegis locked" {
				aegis.verify = func(context.Context, string) error { return errClusterConflict }
			}
			switch mode {
			case "claimed work":
				snapshot.work.Claims = []clusterSSHNetworkTargetClaim{{}}
			case "missing credential":
				snapshot.credentials = nil
			case "wrong credential":
				snapshot.credentials[0].ciphertext += "changed"
			case "wrong generation":
				snapshot.work.Authorization.Targets[0].QualificationGeneration = expected[0].Generation
			case "wrong target":
				snapshot.work.Authorization.Targets[0].TargetID = newClusterUUID()
			case "controller mismatch":
				snapshot.work.Authorization.ControllerHolder = "other-controller"
			}
			nonces := map[string]bool{}
			var ownedSudo [][]byte
			connect := func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
				connections++
				item := expected[(connections-1)%len(expected)]
				if target.Address != item.Binding.Address || target.Port != item.Binding.Port || key.Fingerprint != item.Key.Fingerprint || credential == nil {
					t.Fatal("connection changed original target")
				}
				client := &sshPreparationProposalTestClient{
					close: func() error {
						closes++
						if mode == "close error" {
							return errors.New("synthetic close failure")
						}
						return nil
					},
					inspect: func(ctx context.Context, sudo []byte, request clusterremote.PreparationProposal, current func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
						reads++
						ownedSudo = append(ownedSudo, sudo)
						if string(sudo) != "synthetic-sudo" || request.Validate() != nil || request.TargetID != item.Binding.TargetID || request.PlanSHA256 != plan ||
							request.QualificationGeneration != item.Generation+1 || request.HostKeyFingerprint != item.Key.Fingerprint ||
							request.Hostname != item.Report.Hostname || request.MachineID != item.Report.MachineID || request.BootID != item.Report.BootID ||
							nonces[request.Nonce] || current(ctx) != nil {
							t.Fatal("request lost original proposal binding")
						}
						nonces[request.Nonce] = true
						digest := strings.Repeat("a", 64)
						if mode == "changed cohort round" && reads > len(expected) || mode == "changed next read" && reads > 2*len(expected) {
							digest = strings.Repeat("b", 64)
						}
						proof := sshPreparationProposalTestProof(request, digest)
						switch mode {
						case "missing packages":
							proof.Packages = nil
						case "null missing":
							proof.Packages.Missing = nil
						case "unknown package":
							proof.Packages.Missing = []string{"foreign"}
						case "duplicate package":
							proof.Packages.Missing = []string{proof.Packages.Missing[0], proof.Packages.Missing[0]}
						case "wrong package lock":
							proof.Packages.PlanSHA256 = strings.Repeat("f", 64)
						case "changed package round", "changed package next read":
							threshold := len(expected)
							if mode == "changed package next read" {
								threshold *= 2
							}
							if reads > threshold {
								proof.Packages.Missing = proof.Packages.Missing[1:]
							}
						case "native error":
							return proof, errClusterConflict
						case "wrong nonce":
							proof.Nonce = strings.Repeat("f", 64)
						case "wrong request":
							proof.RequestSHA256 = strings.Repeat("f", 64)
						case "bad digest":
							proof.ObservationSHA256 = strings.Repeat("0", 64)
						case "authority during native":
							lost = true
						case "cancel":
							cancel()
						}
						return proof, nil
					},
				}
				if mode == "connect error" {
					return client, errClusterConflict
				}
				return client, nil
			}
			read := newClusterSSHPreparationCleanTargetsRead(snapshot, check, aegis, connect)
			if mode == "copied inputs" {
				snapshot.work.Proof.Cohort.Targets[0].Report.BootID = newClusterUUID()
				snapshot.credentials[0].ciphertext = "changed"
				snapshot.work.Authorization.Targets[0].TargetID = newClusterUUID()
			}
			requested := plan
			if mode == "wrong plan" {
				requested = strings.Repeat("f", 64)
			}
			digest, err := read(ctx, requested)
			positive := mode == "pair" || mode == "replacement" || mode == "copied inputs" || mode == "changed next read" || mode == "changed package next read"
			if (err == nil) != positive || err != nil && digest != "" {
				t.Fatal("unexpected proof result", err)
			}
			if positive {
				if !validClusterSSHPreparationDigest(digest) || reads != 2*len(expected) {
					t.Fatal("incomplete fresh cohort rounds", reads)
				}
				again, nextErr := read(ctx, plan)
				if mode == "changed next read" || mode == "changed package next read" {
					if nextErr == nil || again != "" {
						t.Fatal("changed observation retained old proof")
					}
				} else if nextErr != nil || again != digest || reads != 4*len(expected) {
					t.Fatal("same plan did not reobserve fresh cohort", nextErr, reads)
				}
			} else {
				before := connections
				lost = false
				if value, err := read(context.Background(), plan); err == nil || value != "" || connections != before {
					t.Fatal("failed reader revived")
				}
			}
			if closes != connections {
				t.Fatal("SSH client not closed", closes, connections)
			}
			for _, sudo := range ownedSudo {
				if slices.ContainsFunc(sudo, func(b byte) bool { return b != 0 }) {
					t.Fatal("sudo buffer retained after scope")
				}
			}
		})
	}
}

func TestClusterSSHPreparationCleanTargetsSerial(t *testing.T) {
	snapshot, aegis := sshPreparationCleanTargetsFixture(t, true)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	var active atomic.Int64
	var once sync.Once
	entered, release := make(chan struct{}), make(chan struct{})
	connect := func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationProposalClient, error) {
		if active.Add(1) != 1 {
			t.Error("concurrent original-target connections")
		}
		return &sshPreparationProposalTestClient{
			close: func() error { active.Add(-1); return nil },
			inspect: func(ctx context.Context, _ []byte, request clusterremote.PreparationProposal, _ func(context.Context) error) (clusterremote.PreparationProposalProof, error) {
				once.Do(func() { close(entered); <-release })
				return sshPreparationProposalTestProof(request, strings.Repeat("a", 64)), ctx.Err()
			},
		}, nil
	}
	read := newClusterSSHPreparationCleanTargetsRead(snapshot, func(ctx context.Context) error { return ctx.Err() }, aegis, connect)
	results := make(chan error, 2)
	go func() { _, err := read(context.Background(), plan); results <- err }()
	<-entered
	go func() { _, err := read(context.Background(), plan); results <- err }()
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if active.Load() != 0 {
		t.Fatal("connection outlived read")
	}
}

func TestClusterSSHPreparationCleanTargetsMissingDependencies(t *testing.T) {
	var controller *clusterController
	read := controller.preparationCleanTargetsRead(clusterSSHPreparationApprovalSnapshot{}, nil)
	if value, err := read(context.Background(), strings.Repeat("a", 64)); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || value != "" {
		t.Fatal("missing scope returned proof", err)
	}
	// Invalid key rejects before any dial. Failed native connection must not
	// become a typed-nil interface scheduled for Close.
	client, err := connectClusterSSHPreparationProposal(context.Background(), clusterremote.Target{}, clusterremote.HostKey{}, nil)
	if err == nil || client != nil {
		t.Fatal("failed connection retained a client interface")
	}
}
