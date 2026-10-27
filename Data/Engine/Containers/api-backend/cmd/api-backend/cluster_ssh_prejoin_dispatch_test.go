package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type sshPrejoinDispatchTestClient struct {
	inspect      func(context.Context) (clusterbootstrap.PreparationProgress, error)
	absent       func(context.Context) error
	complete     func(context.Context) error
	hostComplete func(context.Context) error
	run          func(context.Context, string, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	close        func() error
}

func (c *sshPrejoinDispatchTestClient) InspectPreparationProgress(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
	if err := check(ctx); err != nil {
		return clusterbootstrap.PreparationProgress{}, err
	}
	if c.inspect != nil {
		return c.inspect(ctx)
	}
	return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
}

func (c *sshPrejoinDispatchTestClient) Close() error { return c.close() }
func (c *sshPrejoinDispatchTestClient) DeliverPreparation(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "source", p, e, engineidentity.Binding{}, nil, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) ReconcilePreparationAbsent(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	if err := check(ctx); err != nil {
		return err
	}
	if c.absent != nil {
		return c.absent(ctx)
	}
	return nil
}
func (c *sshPrejoinDispatchTestClient) ReconcilePreparation(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "source proof", p, e, engineidentity.Binding{}, nil, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) PrepareRuntimeIdentity(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "account", p, e, engineidentity.Binding{}, nil, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) CompleteRuntimeIdentity(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) error {
	if err := check(ctx); err != nil {
		return err
	}
	if c.complete != nil {
		return c.complete(ctx)
	}
	return clusterbootstrap.ErrMutationFreshClaim
}
func (c *sshPrejoinDispatchTestClient) ReconcileRuntimeIdentity(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "account proof", p, e, engineidentity.Binding{}, nil, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) PreparePrejoinIdentityRoot(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "directory", p, e, source, keys, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) ReconcilePrejoinIdentityRoot(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "directory proof", p, e, source, keys, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) InstallPrejoinIdentity(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "keys", p, e, source, keys, sudo, check)
}
func (c *sshPrejoinDispatchTestClient) ReconcilePrejoinIdentity(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "keys proof", p, e, source, keys, sudo, check)
}

func (c *sshPrejoinDispatchTestClient) PreparePrejoinHost(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "host", p, e, source, keys, sudo, check)
}

func (c *sshPrejoinDispatchTestClient) CompletePrejoinHost(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, check func(context.Context) error) error {
	if err := check(ctx); err != nil {
		return err
	}
	if c.hostComplete != nil {
		return c.hostComplete(ctx)
	}
	return clusterbootstrap.ErrMutationFreshClaim
}

func (c *sshPrejoinDispatchTestClient) ReconcilePrejoinHost(ctx context.Context, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected, source engineidentity.Binding, keys *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
	return c.run(ctx, "host proof", p, e, source, keys, sudo, check)
}

func TestClusterSSHPrejoinDispatchOrderingAndUncertainty(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	inputs := &clusterbootstrap.PreparationInputs{} // Transport seam only; native methods reject this inert fixture.
	sequence := []string{"source", "source proof", "account", "account proof", "directory", "directory proof", "keys", "keys proof", "host", "host proof"}
	for _, mode := range []string{"success", "write uncertain", "already closed", "connection lost", "connection with client", "nil connection", "proof failed", "close failed", "ignored authority failure", "cancelled"} {
		for stage := 0; stage < 5; stage++ {
			t.Run(fmt.Sprintf("%s/%d", mode, stage), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				opened, closed, calls := 0, 0, []string{}
				failed := false
				current := func(context.Context) error {
					if failed {
						return errors.New("private revoked controller")
					}
					return nil
				}
				var escaped func(context.Context) error
				sudoOriginal := []byte("private test sudo")
				connect := func(context.Context) (clusterSSHPrejoinClient, error) {
					index := opened - 1 // First connection only inspects progress.
					opened++
					client := &sshPrejoinDispatchTestClient{}
					client.close = func() error {
						closed++
						if mode == "close failed" && index == stage*2 {
							return errors.New("private close failed")
						}
						if mode == "already closed" && index == stage*2 {
							return fmt.Errorf("closed protocol connection: %w", net.ErrClosed)
						}
						return nil
					}
					client.run = func(ctx context.Context, action string, p *clusterbootstrap.PreparationInputs, e clusterbootstrap.PreparationExpected,
						s engineidentity.Binding, k *engineidentity.Material, sudo []byte, check func(context.Context) error) error {
						calls = append(calls, action)
						phaseIndex := index
						if mode == "proof failed" && (stage == 1 || stage == 4) && index == stage*2+3 {
							phaseIndex = stage*2 + 1
						}
						escaped = check
						if action != sequence[phaseIndex] || p != inputs || !reflect.DeepEqual(e, expected) || string(sudo) != string(sudoOriginal) || check(ctx) != nil {
							t.Fatal("dispatch binding/order changed")
						}
						if phaseIndex >= 4 {
							if s != source.Binding || !k.Equal(keys) {
								t.Fatal("source identity changed")
							}
						} else if s != (engineidentity.Binding{}) || k != nil {
							t.Fatal("source/account session received identity envelope")
						}
						// Adapters cannot redefine retained expected/sudo for later sessions.
						e.PeerAddresses[0] = "changed"
						sudo[0] = 'X'
						if index == stage*2 {
							switch mode {
							case "write uncertain", "already closed":
								return errors.New("private lost write acknowledgement")
							case "ignored authority failure":
								failed = true
								_ = check(context.Background())
								failed = false // Restoring proof must not erase failed boundary.
							case "cancelled":
								cancel()
							}
						}
						if mode == "proof failed" && phaseIndex == stage*2+1 {
							return errors.New("private partial effect cannot be proven")
						}
						return nil
					}
					if index == stage*2 {
						switch mode {
						case "connection lost":
							return nil, errors.New("private connection failed")
						case "nil connection":
							return nil, nil
						case "connection with client":
							return client, errors.New("private connection failed")
						}
					}
					return client, nil
				}
				err := dispatchClusterSSHPrejoinIdentity(ctx, connect, inputs, expected, source.Binding, keys, sudoOriginal, current)
				success := slices.Contains([]string{"success", "write uncertain", "already closed", "connection lost", "connection with client", "nil connection"}, mode)
				if (err == nil) != success || err != nil && err != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("wrong uncertain dispatch result", err)
				}
				wantOpened := 11
				if mode == "proof failed" {
					wantOpened = stage*2 + 3
					if stage == 0 {
						wantOpened++
					}
					if stage == 1 || stage == 4 {
						wantOpened += 2
					}
				} else if !success {
					wantOpened = stage*2 + 2
				}
				wantClosed := wantOpened
				if mode == "connection lost" || mode == "nil connection" {
					wantClosed--
				}
				if opened != wantOpened || closed != wantClosed || string(sudoOriginal) != "private test sudo" {
					t.Fatal("unsafe progression, cleanup or caller mutation", opened, closed, calls)
				}
				if escaped != nil && escaped(context.Background()) != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("dispatch check escaped lifetime")
				}
			})
		}
	}
}

func TestClusterSSHPrejoinDispatchAuthorityBinding(t *testing.T) {
	for _, mode := range []string{"valid", "source", "phase", "holder", "baseline", "boot", "namespace", "network", "peer", "sealed target", "key", "cohort"} {
		t.Run(mode, func(t *testing.T) {
			a, source, _ := sshPrejoinSourceFixture(t, false)
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding}}
			switch mode {
			case "source":
				source.Binding.SourceUID = newClusterUUID()
			case "phase":
				a.Lease.Step = "qualify"
			case "holder":
				claim.Lease.Holder = newClusterUUID()
			case "baseline":
				a.Baseline.Release = "2026.09.999-rc.2"
			case "boot":
				expected.TargetBootID = newClusterUUID()
			case "namespace":
				expected.KubeSystemUID = newClusterUUID()
			case "network":
				expected.ControlPlaneVIP = "192.168.3.100"
			case "peer":
				expected.PeerAddresses[0] = "192.168.3.100"
			case "sealed target":
				claim.Sealed.binding.TargetID = newClusterUUID()
			case "key":
				a.Cohort.Targets[0].Key.PublicKey = []byte("bad-key")
			case "cohort":
				a.Cohort.Targets[1].Report.BootID = newClusterUUID()
			}
			key, err := clusterSSHPrejoinDispatchKey(a, source, claim, expected.Source, expected)
			if (err == nil) != (mode == "valid") {
				t.Fatal("incorrect dispatch binding decision", err)
			}
			if err == nil {
				key.PublicKey[0] ^= 1
				if a.Cohort.Targets[0].Key.Validate() != nil {
					t.Fatal("returned pin aliases authority")
				}
			}
		})
	}
}

func TestClusterSSHPrejoinDispatchRejectsInvalidBoundary(t *testing.T) {
	for _, mode := range []string{"inputs", "expected", "material", "empty material", "current", "source cluster", "source target", "source UID", "sudo", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a, source, _ := sshPrejoinSourceFixture(t, false)
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			keys := sshIdentitySourceMaterial(t, t.TempDir())
			inputs := &clusterbootstrap.PreparationInputs{}
			current := func(context.Context) error { t.Error("invalid input reached authority"); return nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var sudo []byte
			switch mode {
			case "inputs":
				inputs = nil
			case "expected":
				expected.TargetBootID = ""
			case "material":
				keys = nil
			case "empty material":
				keys = &engineidentity.Material{}
			case "current":
				current = nil
			case "source cluster":
				source.Binding.ClusterID = newClusterUUID()
			case "source target":
				source.Binding.SourceID = expected.Target.TargetID
			case "source UID":
				source.Binding.SourceUID = "00000000-0000-0000-0000-000000000000"
			case "sudo":
				sudo = []byte("secret\ncommand")
			case "cancelled":
				cancel()
			}
			err = dispatchClusterSSHPrejoinIdentity(ctx, func(context.Context) (clusterSSHPrejoinClient, error) {
				t.Error("invalid boundary reached SSH")
				return nil, nil
			}, inputs, expected, source.Binding, keys, sudo, current)
			if err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("invalid boundary accepted", err)
			}
		})
	}
}

func TestClusterSSHPrejoinDispatchWithinMaterialScope(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprint(drift), func(t *testing.T) {
			a, source, pod := sshPrejoinSourceFixture(t, false)
			expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
			if err != nil {
				t.Fatal(err)
			}
			p, err := clusterIdentityPodCustody(pod, a.Baseline.SourceSHA, source.Process)
			if err != nil {
				t.Fatal(err)
			}
			proof := clusterSSHPrejoinSourceCustody{Source: source, Pod: p, AuthoritySHA256: strings.Repeat("a", 64)}
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			opened, closed := 0, 0
			read := func(context.Context) (clusterSSHPrejoinSourceCustody, error) { return proof, nil }
			err = withClusterSSHPrejoinSourceMaterial(context.Background(), consumer, root, source, read,
				func(ctx context.Context, keys *engineidentity.Material, refresh func(context.Context) error) error {
					connect := func(context.Context) (clusterSSHPrejoinClient, error) {
						opened++
						return &sshPrejoinDispatchTestClient{close: func() error { closed++; return nil },
							run: func(ctx context.Context, action string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected,
								_ engineidentity.Binding, material *engineidentity.Material, _ []byte, check func(context.Context) error) error {
								if check(ctx) != nil {
									return clusterbootstrap.ErrSessionAuthority
								}
								if material != nil && !material.Equal(want) {
									t.Fatal("scope changed source material")
								}
								if drift && action == "keys" {
									sshIdentitySourceMaterial(t, root)
								}
								return nil
							}}, nil
					}
					return dispatchClusterSSHPrejoinIdentity(ctx, connect, &clusterbootstrap.PreparationInputs{}, expected, source.Binding, keys, nil, refresh)
				})
			wantSessions := 11
			if drift {
				wantSessions = 8
			}
			if (err == nil) == drift || opened != wantSessions || closed != opened {
				t.Fatal("source drift progressed or leaked session", err, opened, closed)
			}
		})
	}
}

func TestClusterSSHPrejoinDispatchResume(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	sequence := []string{"source", "source proof", "account", "account proof", "directory", "directory proof", "keys", "keys proof", "host", "host proof"}
	for index, step := range []string{"empty", "stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host"} {
		for _, mode := range []string{"valid", "query error", "query close", "invalid cursor", "proof error", "proof close"} {
			t.Run(step+"/"+mode, func(t *testing.T) {
				calls := []string{}
				proofSession := 1
				if index == 0 {
					proofSession = 2
				}
				opened, closed := 0, 0
				connect := func(context.Context) (clusterSSHPrejoinClient, error) {
					session := opened
					opened++
					return &sshPrejoinDispatchTestClient{
						absent: func(context.Context) error { calls = append(calls, "source absent proof"); return nil },
						hostComplete: func(context.Context) error {
							calls = append(calls, "host completion")
							return clusterbootstrap.ErrMutationFreshClaim
						},
						complete: func(context.Context) error {
							calls = append(calls, "account completion")
							return clusterbootstrap.ErrMutationFreshClaim
						},
						inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
							calls = append(calls, "inspect")
							if session != 0 {
								t.Fatal("inspection repeated")
							}
							cursor := clusterbootstrap.PreparationProgress{Step: step}
							if mode == "query error" {
								return cursor, errors.New("unreadable journal")
							}
							if mode == "invalid cursor" {
								cursor.Step = "join_cluster"
							}
							return cursor, nil
						},
						run: func(ctx context.Context, action string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, check func(context.Context) error) error {
							calls = append(calls, action)
							if check(ctx) != nil {
								t.Fatal("authority absent")
							}
							if mode == "proof error" && strings.HasSuffix(action, "proof") {
								return errors.New("partial effect")
							}
							return nil
						},
						close: func() error {
							closed++
							if mode == "query close" && session == 0 || mode == "proof close" && session == proofSession {
								return errors.New("cleanup uncertain")
							}
							return nil
						},
					}, nil
				}
				err := dispatchClusterSSHPrejoinIdentity(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, expected, source.Binding, keys, nil, func(context.Context) error { return nil })
				want := []string{"inspect"}
				if mode == "valid" || mode == "proof error" || mode == "proof close" {
					start := 0
					if index > 0 {
						start = (index-1)*2 + 1
					}
					if mode == "valid" {
						want = append(want, sequence[start:]...)
					} else if index == 0 {
						want = append(want, "source", "source proof")
					} else {
						want = append(want, sequence[start])
					}
				}
				if mode == "proof error" && index <= 1 {
					want = append(want, "source absent proof")
				}
				if mode == "proof error" && index == 2 {
					want = append(want, "account completion", "account proof")
				}
				if mode == "proof error" && index == 5 {
					want = append(want, "host completion", "host proof")
				}
				if (err == nil) != (mode == "valid") || !reflect.DeepEqual(calls, want) || closed != opened || opened != len(want) {
					t.Fatalf("unsafe resume: err=%v calls=%v want=%v connections=%d/%d", err, calls, want, opened, closed)
				}
			})
		}
	}
}

func TestClusterSSHPrejoinDispatchProvenAbsence(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"valid", "absence changed", "cleanup failed", "authority lost"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			opened, closed := 0, 0
			lost := false
			current := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				session := opened
				opened++
				return &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						calls = append(calls, "inspect")
						return clusterbootstrap.PreparationProgress{Step: "source_absent"}, nil
					},
					absent: func(context.Context) error {
						calls = append(calls, "absence proof")
						if mode == "absence changed" {
							return errors.New("destination occupied")
						}
						if mode == "authority lost" {
							lost = true
						}
						return nil
					},
					run: func(ctx context.Context, action string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, check func(context.Context) error) error {
						calls = append(calls, action)
						return check(ctx)
					},
					close: func() error {
						closed++
						if mode == "cleanup failed" && session == 1 {
							return errors.New("uncertain cleanup")
						}
						return nil
					},
				}, nil
			}
			err := dispatchClusterSSHPrejoinIdentity(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, expected, source.Binding, keys, nil, current)
			want := []string{"inspect", "absence proof"}
			if mode == "valid" {
				want = append(want, "source", "source proof", "account", "account proof", "directory", "directory proof", "keys", "keys proof", "host", "host proof")
			}
			if (err == nil) != (mode == "valid") || !reflect.DeepEqual(calls, want) || opened != closed || opened != len(want) {
				t.Fatal("unsafe source retry", calls, opened, closed, err)
			}
		})
	}
}

func TestClusterSSHPrejoinDispatchAccountContinuation(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"complete", "lost acknowledgement", "not group only", "proof drift", "completion cleanup", "authority lost", "initial proof cleanup"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			opened, closed, proofs := 0, 0, 0
			ready, lost := false, false
			current := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				session := opened
				opened++
				return &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						calls = append(calls, "inspect")
						return clusterbootstrap.PreparationProgress{Step: "prepare_runtime_identity"}, nil
					},
					complete: func(context.Context) error {
						calls = append(calls, "complete missing user")
						if mode == "not group only" {
							return clusterbootstrap.ErrMutationUnknown
						}
						ready = true
						if mode == "authority lost" {
							lost = true
						}
						if mode == "lost acknowledgement" {
							return errors.New("unknown delivery")
						}
						return nil
					},
					run: func(ctx context.Context, action string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, check func(context.Context) error) error {
						calls = append(calls, action)
						if action == "account proof" {
							proofs++
							if !ready || mode == "proof drift" {
								return clusterbootstrap.ErrMutationUnknown
							}
						}
						return check(ctx)
					},
					close: func() error {
						closed++
						if mode == "completion cleanup" && session == 2 || mode == "initial proof cleanup" && session == 1 {
							return errors.New("unknown cleanup")
						}
						return nil
					},
				}, nil
			}
			err := dispatchClusterSSHPrejoinIdentity(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, expected, source.Binding, keys, nil, current)
			want := []string{"inspect", "account proof"}
			if mode != "initial proof cleanup" {
				want = append(want, "complete missing user")
			}
			if mode != "initial proof cleanup" && mode != "completion cleanup" && mode != "authority lost" {
				want = append(want, "account proof")
			}
			success := mode == "complete" || mode == "lost acknowledgement"
			if success {
				want = append(want, "directory", "directory proof", "keys", "keys proof", "host", "host proof")
			}
			if (err == nil) != success || !reflect.DeepEqual(calls, want) || opened != closed || opened != len(want) {
				t.Fatal("unsafe account continuation", calls, opened, closed, proofs, err)
			}
		})
	}
}

func TestClusterSSHPrejoinDispatchHostContinuation(t *testing.T) {
	a, source, _ := sshPrejoinSourceFixture(t, false)
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	keys := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"complete", "lost acknowledgement", "not configured", "proof drift", "completion cleanup", "authority lost", "initial proof cleanup"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			opened, closed, proofs := 0, 0, 0
			ready, lost := false, false
			current := func(context.Context) error {
				if lost {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			connect := func(context.Context) (clusterSSHPrejoinClient, error) {
				session := opened
				opened++
				return &sshPrejoinDispatchTestClient{
					inspect: func(context.Context) (clusterbootstrap.PreparationProgress, error) {
						calls = append(calls, "inspect")
						return clusterbootstrap.PreparationProgress{Step: "prepare_host"}, nil
					},
					hostComplete: func(context.Context) error {
						calls = append(calls, "complete configured host")
						if mode == "not configured" {
							return clusterbootstrap.ErrMutationUnknown
						}
						ready = true
						if mode == "authority lost" {
							lost = true
						}
						if mode == "lost acknowledgement" {
							return errors.New("unknown delivery")
						}
						return nil
					},
					run: func(ctx context.Context, action string, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ engineidentity.Binding, _ *engineidentity.Material, _ []byte, check func(context.Context) error) error {
						calls = append(calls, action)
						if action == "host proof" {
							proofs++
							if !ready || mode == "proof drift" {
								return clusterbootstrap.ErrMutationUnknown
							}
						}
						return check(ctx)
					},
					close: func() error {
						closed++
						if mode == "completion cleanup" && session == 2 || mode == "initial proof cleanup" && session == 1 {
							return errors.New("unknown cleanup")
						}
						return nil
					},
				}, nil
			}
			err := dispatchClusterSSHPrejoinIdentity(context.Background(), connect, &clusterbootstrap.PreparationInputs{}, expected, source.Binding, keys, nil, current)
			want := []string{"inspect", "host proof"}
			if mode != "initial proof cleanup" {
				want = append(want, "complete configured host")
			}
			if mode != "initial proof cleanup" && mode != "completion cleanup" && mode != "authority lost" {
				want = append(want, "host proof")
			}
			success := mode == "complete" || mode == "lost acknowledgement"
			if (err == nil) != success || !reflect.DeepEqual(calls, want) || opened != closed || opened != len(want) {
				t.Fatal("unsafe host continuation", calls, opened, closed, proofs, err)
			}
		})
	}
}
