package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sshNetworkTargetsFixture struct {
	hostWire       func(int, string) string
	filesystemWire func(int, []byte) []byte
	sources        []clusterbootstrap.SourceNetwork
	vipLease       clusterbootstrap.VIPLease
	a              clusterSSHPreparationAuthority
	claims         []clusterSSHNetworkTargetClaim
	deps           clusterSSHNetworkTargetDependencies
	boots          []clusterremote.NetworkRenderRequest
	arps           []clusterremote.ARPRequest
	lost           atomic.Int32
	opens          [2]atomic.Int32
	renews         [2]atomic.Int32
	sessions       []<-chan struct{}
	credentials    []*clusterremote.Credential
}

func newSSHNetworkTargetsFixture(t *testing.T, replacement bool, kind string) *sshNetworkTargetsFixture {
	t.Helper()
	a, sources, lease := sshVIPFixture(t, replacement)
	keys := sshNetworkFixtureKeys(t, &a.Cohort)
	f := &sshNetworkTargetsFixture{a: a, sources: sources, vipLease: lease}
	for _, item := range a.Cohort.Targets {
		claim := clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: sealedClusterSSHCredentials{binding: item.Binding, generation: "fixture-generation", ciphertext: aegisEnvelopePrefix + item.Binding.TargetID}}
		claim.Lease.TargetID, claim.Lease.Holder, claim.Lease.Generation = item.Binding.TargetID, newClusterUUID(), item.Generation+1
		f.claims = append(f.claims, claim)
	}
	f.a.Lease = f.claims[0].Lease
	peers := sshARPFixturePeers(f.a)
	owner := clusterSSHVIPOwner{Address: a.Source.ControlPlaneVIP, Owner: peers[len(a.Source.Members)-1], LeaseUID: lease.UID, AcquireTime: lease.AcquireTime, Transitions: lease.Transitions}
	var err error
	f.boots, err = clusterSSHNetworkRenderRequests(f.a, peers, owner)
	if err != nil {
		t.Fatal(err)
	}
	f.arps, err = clusterSSHARPRequests(f.a, peers, owner)
	if err != nil {
		t.Fatal(err)
	}
	index := func(claim clusterSSHNetworkTargetClaim) int {
		return slices.Index(f.claims, claim)
	}
	f.deps.authority = func(claim clusterSSHNetworkTargetClaim) clusterSSHPreparationAuthorityRead {
		return func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			i := index(claim)
			if i < 0 || f.lost.Load() == int32(i+1) || ctx.Err() != nil {
				return clusterSSHPreparationAuthority{}, errors.New("private authority")
			}
			value := f.a
			value.Lease = claim.Lease
			return value, nil
		}
	}
	f.deps.renew = func(ctx context.Context, claim clusterSSHNetworkTargetClaim) error {
		i := index(claim)
		if i < 0 || f.lost.Load() == int32(i+1) || ctx.Err() != nil {
			return errors.New("private renewal")
		}
		f.renews[i].Add(1)
		return nil
	}
	f.deps.open = func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
		i := slices.IndexFunc(f.claims, func(c clusterSSHNetworkTargetClaim) bool { return c.Sealed == sealed && c.Sealed.binding == binding })
		if i < 0 || ctx.Err() != nil {
			return clusterSSHCredentialEnvelope{}, errors.New("private decrypt")
		}
		f.opens[i].Add(1)
		return clusterSSHCredentialEnvelope{Version: 1, Binding: binding, Username: fmt.Sprintf("target%d", i), Method: "password", Password: fmt.Sprintf("fixture-private-%d", i), SudoPassword: "fixture-sudo"}, nil
	}
	f.deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (*clusterremote.Client, error) {
		i := slices.IndexFunc(f.a.Cohort.Targets, func(item clusterSSHInspectedTarget) bool {
			return item.Binding.Address == target.Address && item.Binding.Port == target.Port
		})
		if i < 0 {
			return nil, errors.New("foreign target")
		}
		item := f.a.Cohort.Targets[i]
		var wire []byte
		if kind == "arp" {
			wire, _ = json.Marshal(struct {
				Version int                      `json:"version"`
				Request clusterremote.ARPRequest `json:"request"`
				Rounds  int                      `json:"rounds"`
			}{1, f.arps[i], 2})
		}
		var edits []func(string) string
		if kind == "filesystem" {
			wire = sshFilesystemFixtureWire(item)
			if f.filesystemWire != nil {
				wire = f.filesystemWire(i, wire)
			}
		}
		if f.hostWire != nil {
			edits = append(edits, func(wire string) string { return f.hostWire(i, wire) })
		}
		transport, done, _ := sshNetworkFixtureTransport(t, item, keys[item.Binding.TargetID], f.boots[i].Link,
			f.boots[i].Targets.Peers, wire, fmt.Sprintf("target%d", i), fmt.Sprintf("fixture-private-%d", i), edits...)
		f.sessions = append(f.sessions, done)
		f.credentials = append(f.credentials, credential)
		return transport.Connect(ctx, target, key, credential)
	}
	return f
}

func (f *sshNetworkTargetsFixture) assertClosed(t *testing.T) {
	t.Helper()
	for _, done := range f.sessions {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("scope retained SSH session")
		}
	}
	// Destroyed credentials must fail before any network dial. Use valid target
	// and pin so this exercises credential lifetime, not input rejection.
	for _, credential := range f.credentials {
		_, err := (clusterremote.Transport{}).Connect(context.Background(), clusterremote.Target{Address: f.a.Cohort.Targets[0].Binding.Address, Port: 22}, f.a.Cohort.Targets[0].Key, credential)
		if err != clusterremote.ErrInvalidAuth {
			t.Fatal("credential survived read", err)
		}
	}
}

func TestClusterSSHNetworkTargetsNativeReaders(t *testing.T) {
	for _, topology := range []string{"expansion", "replacement"} {
		for _, kind := range []string{"peer", "boot", "arp"} {
			t.Run(topology+"/"+kind, func(t *testing.T) {
				f := newSSHNetworkTargetsFixture(t, topology == "replacement", kind)
				var escaped clusterSSHNetworkTargetReaders
				err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
					escaped = readers
					for i, item := range f.a.Cohort.Targets {
						started := time.Now()
						switch kind {
						case "peer":
							value, err := readers.Peer(ctx, item, f.boots[i].Targets.Peers)
							if err != nil {
								return err
							}
							_, err = value.ManagementLink(started, item.Report.Hostname, item.Report.MachineID, item.Report.BootID, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, f.boots[i].Targets.Peers)
							if err != nil {
								return err
							}
						case "boot":
							value, err := readers.Boot(ctx, item, f.boots[i], func(ctx context.Context) error { return ctx.Err() })
							if err != nil || value.Matches(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, f.boots[i]) != nil {
								return errors.New("boot binding")
							}
						case "arp":
							value, err := readers.ARP(ctx, item, f.arps[i], func(ctx context.Context) error { return ctx.Err() })
							if err != nil || value.Matches(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, f.arps[i]) != nil {
								return errors.New("ARP binding")
							}
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal("valid target scope", err)
				}
				f.assertClosed(t)
				for i := range f.claims {
					if f.opens[i].Load() != 1 || f.renews[i].Load() < 2 {
						t.Fatal("original target not independently owned", i)
					}
				}
				if _, err := escaped.Authority(context.Background()); err == nil {
					t.Fatal("escaped authority")
				}
				if escaped.Refresh(context.Background()) == nil {
					t.Fatal("escaped renewal")
				}
				if _, err := escaped.Peer(context.Background(), f.a.Cohort.Targets[0], f.boots[0].Targets.Peers); err == nil {
					t.Fatal("escaped SSH reader")
				}
			})
		}
	}
}

func TestClusterSSHNetworkTargetsRejectIncompleteOriginalClaims(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "order", "foreign", "generation", "ciphertext", "Aegis generation", "controller", "attempt", "binding", "wrong authority lease", "source drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, false, "peer")
			claims := slices.Clone(f.claims)
			switch mode {
			case "missing":
				claims = claims[:1]
			case "duplicate":
				claims[1] = claims[0]
			case "order":
				claims[0], claims[1] = claims[1], claims[0]
			case "foreign":
				claims[1].Lease.TargetID = newClusterUUID()
			case "generation":
				claims[1].Lease.Generation++
			case "ciphertext":
				claims[1].Sealed.ciphertext += "replacement"
			case "Aegis generation":
				claims[1].Sealed.generation = "replacement"
			case "controller":
				claims[1].Lease.ControllerHolder = "replacement"
			case "attempt":
				claims[1].Lease.OperationAttempt++
			case "binding":
				claims[1].Sealed.binding.Port++
			case "wrong authority lease", "source drift":
				original := f.deps.authority
				f.deps.authority = func(claim clusterSSHNetworkTargetClaim) clusterSSHPreparationAuthorityRead {
					read := original(claim)
					return func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
						v, e := read(ctx)
						if claim == f.claims[1] {
							if mode == "source drift" {
								v.K3sVersion = "v1.36.4+k3s1"
							} else {
								v.Lease = f.claims[0].Lease
							}
						}
						return v, e
					}
				}
			}
			consumed := false
			err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, claims, f.deps, func(context.Context, clusterSSHNetworkTargetReaders) error { consumed = true; return nil })
			if err == nil || consumed || f.opens[0].Load() != 0 || f.opens[1].Load() != 0 {
				t.Fatal("invalid complete authority reached consumer")
			}
		})
	}
}

func TestClusterSSHNetworkTargetsFailureCannotBeIgnored(t *testing.T) {
	for _, mode := range []string{"first lost", "sibling lost", "lost during decrypt", "lost after connect", "partial connect", "closed client", "wrong envelope", "wrong password", "wrong target", "wrong peers", "nil outer", "wrong boot", "input alias"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, false, "boot")
			if mode == "lost during decrypt" || mode == "wrong envelope" || mode == "wrong password" {
				original := f.deps.open
				f.deps.open = func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
					v, e := original(ctx, sealed, binding)
					switch mode {
					case "lost during decrypt":
						f.lost.Store(2)
					case "wrong envelope":
						v.Binding = f.claims[1].Sealed.binding
					case "wrong password":
						v.Password = "wrong-fixture"
					}
					return v, e
				}
			}
			if mode == "lost after connect" || mode == "partial connect" || mode == "closed client" {
				original := f.deps.connect
				f.deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (*clusterremote.Client, error) {
					client, err := original(ctx, target, key, credential)
					switch mode {
					case "lost after connect":
						f.lost.Store(2)
					case "partial connect":
						err = errors.New("private partial connection")
					case "closed client":
						if client != nil {
							_ = client.Close()
						}
					}
					return client, err
				}
			}
			err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				item := f.a.Cohort.Targets[0]
				request := f.boots[0]
				request.Targets.Peers = slices.Clone(request.Targets.Peers)
				outer := func(ctx context.Context) error { return ctx.Err() }
				switch mode {
				case "first lost":
					f.lost.Store(1)
				case "sibling lost":
					f.lost.Store(2)
				case "wrong target":
					item.Binding.TargetID = newClusterUUID()
				case "wrong peers":
					request.Targets.Peers[0] = "192.168.90.247"
				case "nil outer":
					outer = nil
				case "wrong boot":
					request.BootID = newClusterUUID()
				case "input alias":
					item.Key.PublicKey = slices.Clone(item.Key.PublicKey)
					outer = func(context.Context) error {
						item.Key.PublicKey[0] ^= 1
						request.Targets.Peers[0] = "192.168.90.247"
						return nil
					}
				}
				started := time.Now()
				value, readErr := readers.Boot(ctx, item, request, outer)
				if mode == "input alias" {
					expected := f.a.Cohort.Targets[0]
					if readErr != nil || value.Matches(started, clusterremote.Target{Address: expected.Binding.Address, Port: expected.Binding.Port}, expected.Key, f.boots[0]) != nil {
						t.Fatal("input ownership", readErr)
					}
				} else if readErr == nil || !reflect.DeepEqual(value, clusterremote.TargetNetworkBoot{}) {
					t.Fatal("unsafe reader result")
				}
				return nil // Scope must latch ignored failures.
			})
			if (err == nil) != (mode == "input alias") {
				t.Fatal("ignored failure escaped", err)
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHNetworkReaderLifetimeJoinsEscapedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &clusterSSHNetworkReaderLifetime{ctx: ctx, cancel: cancel}
	started, cleanup, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- l.run(context.Background(), func(ctx context.Context) error { close(started); <-ctx.Done(); close(cleanup); <-release; return nil })
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- l.close() }()
	select {
	case <-cleanup:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("reader not canceled")
	}
	select {
	case <-closed:
		close(release)
		t.Fatal("scope did not join cleanup")
	default:
	}
	close(release)
	if <-done == nil || <-closed == nil {
		t.Fatal("abandoned work accepted")
	}
	if l.run(context.Background(), func(context.Context) error { t.Fatal("closed reader called work"); return nil }) == nil {
		t.Fatal("scope reused")
	}
}

// Exercise the real reader assembly through the live VIP broker consumer, so
// independently correct callbacks cannot hide incompatible scope lifetimes.
func TestClusterSSHNetworkTargetsVIPComposition(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "sibling lost", "VIP changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHNetworkTargetsFixture(t, mode == "replacement", "boot")
			a := f.a
			var vipChanged atomic.Bool
			var reads atomic.Int64
			broker := newClusterSSHVIPBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
			broker.run = sshVIPBrokerRun(a, f.vipLease, &vipChanged, &reads)
			server := sshVIPBrokerServer(t, broker)
			client := sshVIPBrokerClient(t, server.URL)
			source := func(context.Context) (clusterSSHPreparationSnapshot, error) {
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, f.sources[0].PodCIDR, f.sources[0].ServiceCIDR)
				return clusterSSHPreparationSnapshot{Expected: expected, Observation: strings.Repeat("a", 64), Sources: slices.Clone(f.sources), started: time.Now()}, err
			}
			err := runClusterSSHNetworkTargets(context.Background(), a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
				boot := readers.Boot
				probe := func(ctx context.Context, item clusterSSHInspectedTarget, request clusterremote.NetworkRenderRequest, check func(context.Context) error) (clusterremote.TargetNetworkBoot, error) {
					value, err := boot(ctx, item, request, check)
					if mode == "sibling lost" {
						f.lost.Store(2)
					}
					if mode == "VIP changed" {
						vipChanged.Store(true)
					}
					return value, err
				}
				return client.observeNetworkBoot(ctx, readers.Authority, source, readers.Peer, probe, readers.Refresh, a.Lease, a.Baseline, f.claims[0].Sealed)
			})
			if (err == nil) != (mode == "expansion" || mode == "replacement") {
				t.Fatal("original-claim VIP composition", err)
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHNetworkTargetsOwnClaimsAndAuthority(t *testing.T) {
	f := newSSHNetworkTargetsFixture(t, false, "peer")
	claims := slices.Clone(f.claims)
	err := runClusterSSHNetworkTargets(context.Background(), f.a.Baseline, claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
		claims[1].Lease.Holder = newClusterUUID()
		claims[1].Sealed.ciphertext += "replaced"
		first, err := readers.Authority(ctx)
		if err != nil {
			return err
		}
		first.Cohort.Targets[0].Key.PublicKey[0] ^= 1
		first.Source.Members[0].Address = "192.168.90.247"
		second, err := readers.Authority(ctx)
		if err != nil || !reflect.DeepEqual(second, f.a) {
			return errors.New("caller mutated frozen authority")
		}
		_, err = readers.Peer(ctx, second.Cohort.Targets[1], f.boots[1].Targets.Peers)
		return err
	})
	if err != nil {
		t.Fatal("scope input ownership", err)
	}
	f.assertClosed(t)
}
