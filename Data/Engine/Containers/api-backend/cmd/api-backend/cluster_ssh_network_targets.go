package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// Each entry is the original independently held claim/envelope. The scope
// neither acquires claims nor loads a sibling credential using an anchor lease.
type clusterSSHNetworkTargetClaim struct {
	Lease  clusterSSHTargetLease
	Sealed sealedClusterSSHCredentials
}

func (clusterSSHNetworkTargetClaim) String() string   { return "network target claim [redacted]" }
func (clusterSSHNetworkTargetClaim) GoString() string { return "network target claim [redacted]" }

type clusterSSHNetworkTargetReaders struct {
	within     func(context.Context, func(context.Context) error) error
	Authority  clusterSSHPreparationAuthorityRead
	Refresh    func(context.Context) error
	Peer       clusterSSHTargetNetworkRead
	Boot       clusterSSHTargetNetworkBootRead
	ARP        clusterSSHTargetARPRead
	Filesystem func(context.Context, clusterSSHInspectedTarget, clusterremote.FilesystemRequest) (clusterremote.TargetFilesystem, error)
}

type clusterSSHNetworkTargetDependencies struct {
	authority func(clusterSSHNetworkTargetClaim) clusterSSHPreparationAuthorityRead
	renew     func(context.Context, clusterSSHNetworkTargetClaim) error
	open      func(context.Context, sealedClusterSSHCredentials, clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error)
	connect   func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (*clusterremote.Client, error)
}

func withClusterSSHNetworkTargets(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	baseline clusterbootstrap.Expected, claims []clusterSSHNetworkTargetClaim,
	consume func(context.Context, clusterSSHNetworkTargetReaders) error) error {
	if store == nil || store.db == nil || aegis == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	dependencies := clusterSSHNetworkTargetDependencies{
		authority: func(claim clusterSSHNetworkTargetClaim) clusterSSHPreparationAuthorityRead {
			return newClusterSSHPreparationAuthorityRead(store, aegis, claim.Lease, baseline, claim.Sealed)
		},
		renew: func(ctx context.Context, claim clusterSSHNetworkTargetClaim) error {
			return store.renewClusterSSHObservationTarget(ctx, claim.Lease, claim.Sealed)
		},
		open: aegis.openClusterSSHCredentials,
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (*clusterremote.Client, error) {
			return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
		},
	}
	return runClusterSSHNetworkTargets(parent, baseline, claims, dependencies, consume)
}

func validateClusterSSHNetworkClaims(a clusterSSHPreparationAuthority, baseline clusterbootstrap.Expected, claims []clusterSSHNetworkTargetClaim) error {
	if a.Baseline != baseline || baseline.Validate() != nil || validateClusterSSHInspectionCohort(a.Cohort, a.Source) != nil ||
		len(claims) != len(a.Cohort.Targets) || len(claims) < 1 || len(claims) > 2 {
		return clusterbootstrap.ErrPreparationConfig
	}
	for i, claim := range claims {
		target := a.Cohort.Targets[i]
		if !validClusterSSHObservationLease(claim.Lease) || claim.Lease.OperationID != a.Cohort.OperationID ||
			claim.Lease.OperationStep != a.Lease.OperationStep || claim.Lease.Step != a.Lease.Step ||
			claim.Lease.OperationAttempt != a.Cohort.Attempt || claim.Lease.ControllerHolder != a.Cohort.ControllerHolder ||
			claim.Lease.TargetID != target.Binding.TargetID || claim.Lease.Generation <= target.Generation ||
			!claim.Sealed.binding.valid() || claim.Sealed.binding != target.Binding || claim.Sealed.generation == "" ||
			claim.Sealed.generation != claims[0].Sealed.generation || len(claim.Sealed.generation) > 16<<10 ||
			!strings.HasPrefix(claim.Sealed.ciphertext, aegisEnvelopePrefix) || len(claim.Sealed.ciphertext) > 256<<10 {
			return clusterbootstrap.ErrPreparationConfig
		}
	}
	return nil
}

// All original adapters participate in each refresh. Their short transactions
// finish before JSON/crypto/SSH. Per-target renewal already compares original
// ciphertext and database-clock ownership after row-lock waits. The common
// source/cohort baseline cannot silently change between independent adapters.
func newClusterSSHNetworkTargetAuthority(baseline clusterbootstrap.Expected, claims []clusterSSHNetworkTargetClaim,
	dependencies clusterSSHNetworkTargetDependencies) clusterSSHPreparationAuthorityRead {
	gate := make(chan struct{}, 1)
	var retained []byte
	var anchor clusterSSHPreparationAuthority
	checks := make([]func(context.Context) error, len(claims))
	for i, claim := range claims {
		read := dependencies.authority(claim)
		checks[i] = newClusterSSHPreparationLeaseCheck(func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			if read == nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			value, err := read(ctx)
			if err != nil || value.Lease != claim.Lease || validateClusterSSHNetworkClaims(value, baseline, claims) != nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			common := value
			common.Lease = clusterSSHTargetLease{}
			common.Cohort.ObservedAt = 0
			raw, err := json.Marshal(common)
			if err != nil || len(raw) > 128<<10 || (retained != nil && !bytes.Equal(retained, raw)) {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			if retained == nil {
				retained = raw
			}
			// Own even the adapter's reused nested key/report allocations.
			raw, err = json.Marshal(value)
			var owned clusterSSHPreparationAuthority
			if err != nil || json.Unmarshal(raw, &owned) != nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
			if i == 0 {
				anchor = owned
			}
			return owned, nil
		}, func(ctx context.Context) error { return dependencies.renew(ctx, claim) })
	}
	return func(parent context.Context) (clusterSSHPreparationAuthority, error) {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
		}
		for _, check := range checks {
			if check(ctx) != nil || ctx.Err() != nil {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
			}
		}
		raw, err := json.Marshal(anchor)
		var owned clusterSSHPreparationAuthority
		if err != nil || json.Unmarshal(raw, &owned) != nil || ctx.Err() != nil {
			return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
		}
		return owned, nil
	}
}

// Reader calls are locally counted and joined. A consumer that ignores a
// reader failure, or returns with work still running, cannot report success.
// Caller callbacks must honor their context; Go cannot forcibly kill them.
type clusterSSHNetworkReaderLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	active int
	closed bool
	failed bool
}

func (l *clusterSSHNetworkReaderLifetime) run(parent context.Context, work func(context.Context) error) (result error) {
	l.mu.Lock()
	if l.closed || l.failed || l.ctx.Err() != nil {
		l.mu.Unlock()
		return clusterbootstrap.ErrPreparationConfig
	}
	l.active++
	l.wg.Add(1)
	l.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	stop := context.AfterFunc(l.ctx, func() { cancel(); close(done) })
	defer func() {
		cancel()
		if !stop() {
			<-done
		}
		l.mu.Lock()
		if result != nil || parent.Err() != nil || l.ctx.Err() != nil {
			result = clusterbootstrap.ErrPreparationConfig
			l.failed = true
			l.cancel()
		}
		l.active--
		l.mu.Unlock()
		l.wg.Done()
	}()
	if ctx.Err() != nil || work(ctx) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

func (l *clusterSSHNetworkReaderLifetime) close() error {
	l.mu.Lock()
	l.closed = true
	if l.active != 0 {
		l.failed = true
	}
	l.mu.Unlock()
	l.cancel()
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

func runClusterSSHNetworkTargets(parent context.Context, baseline clusterbootstrap.Expected, claims []clusterSSHNetworkTargetClaim,
	dependencies clusterSSHNetworkTargetDependencies, consume func(context.Context, clusterSSHNetworkTargetReaders) error) error {
	if parent.Err() != nil || baseline.Validate() != nil || len(claims) < 1 || len(claims) > 2 || consume == nil ||
		dependencies.authority == nil || dependencies.renew == nil || dependencies.open == nil || dependencies.connect == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	claims = slices.Clone(claims)
	authority := newClusterSSHNetworkTargetAuthority(baseline, claims, dependencies)
	check := func(ctx context.Context) error { _, err := authority(ctx); return err }
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	return runClusterSSHPreparationScope(ctx, time.Second, check, func(ctx context.Context) (result error) {
		frozen, err := authority(ctx)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		readerCtx, cancelReaders := context.WithCancel(ctx)
		lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancelReaders}
		defer func() {
			if lifetime.close() != nil {
				result = clusterbootstrap.ErrPreparationConfig
			}
		}()
		readers := clusterSSHNetworkTargetReaders{within: lifetime.run}
		readers.Authority = func(caller context.Context) (clusterSSHPreparationAuthority, error) {
			var value clusterSSHPreparationAuthority
			err := lifetime.run(caller, func(ctx context.Context) error { var err error; value, err = authority(ctx); return err })
			if err != nil {
				return clusterSSHPreparationAuthority{}, err
			}
			return value, nil
		}
		readers.Refresh = func(caller context.Context) error { return lifetime.run(caller, check) }
		read := func(caller context.Context, item clusterSSHInspectedTarget, outer func(context.Context) error,
			valid func(clusterSSHInspectedTarget) bool,
			observe func(context.Context, clusterSSHInspectedTarget, *clusterremote.Client, []byte, func(context.Context) error) error) error {
			item.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
			item.Report.Nodes = slices.Clone(item.Report.Nodes)
			return lifetime.run(caller, func(ctx context.Context) (result error) {
				index := slices.IndexFunc(frozen.Cohort.Targets, func(expected clusterSSHInspectedTarget) bool { return reflect.DeepEqual(expected, item) })
				if index < 0 || valid == nil || observe == nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				item = frozen.Cohort.Targets[index]
				item.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
				item.Report.Nodes = slices.Clone(item.Report.Nodes)
				if !valid(item) {
					return clusterbootstrap.ErrPreparationConfig
				}
				current := func(ctx context.Context) error {
					if check(ctx) != nil || (outer != nil && outer(ctx) != nil) || ctx.Err() != nil {
						return clusterbootstrap.ErrPreparationConfig
					}
					return nil
				}
				if current(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				claim := claims[index]
				envelope, err := dependencies.open(ctx, claim.Sealed, item.Binding)
				if err != nil || envelope.Binding != item.Binding || envelope.validate() != nil || current(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				credential, sudo, err := clusterSSHNetworkCredential(envelope)
				envelope = clusterSSHCredentialEnvelope{}
				if err != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				defer credential.Destroy()
				defer clear(sudo)
				key := item.Key
				key.PublicKey = bytes.Clone(key.PublicKey)
				client, err := dependencies.connect(ctx, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, key, credential)
				if client != nil {
					defer func() {
						if client.Close() != nil {
							result = clusterbootstrap.ErrPreparationConfig
						}
					}()
				}
				if err != nil || client == nil || current(ctx) != nil || observe(ctx, item, client, sudo, current) != nil || current(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				return nil
			})
		}
		readers.Peer = func(caller context.Context, item clusterSSHInspectedTarget, peers []string) (clusterremote.TargetManagementPeer, error) {
			peers = slices.Clone(peers)
			var value clusterremote.TargetManagementPeer
			err := read(caller, item, nil, func(expected clusterSSHInspectedTarget) bool {
				return slices.Equal(peers, clusterSSHNetworkTargetPeers(frozen, expected.Binding.Address))
			}, func(ctx context.Context, expected clusterSSHInspectedTarget, client *clusterremote.Client, sudo []byte, current func(context.Context) error) error {
				var err error
				value, err = readClusterSSHTargetNetwork(ctx, client, sudo, expected, peers, current)
				return err
			})
			if err != nil {
				return clusterremote.TargetManagementPeer{}, err
			}
			return value, nil
		}
		readers.Filesystem = func(caller context.Context, item clusterSSHInspectedTarget, request clusterremote.FilesystemRequest) (clusterremote.TargetFilesystem, error) {
			request.Paths = slices.Clone(request.Paths)
			var value clusterremote.TargetFilesystem
			err := read(caller, item, nil, func(expected clusterSSHInspectedTarget) bool {
				return request.Validate() == nil && request.MachineID == expected.Report.MachineID && request.BootID == expected.Report.BootID
			}, func(ctx context.Context, expected clusterSSHInspectedTarget, client *clusterremote.Client, sudo []byte, current func(context.Context) error) error {
				var err error
				value, err = client.InspectFilesystem(ctx, sudo, clusterremote.Target{Address: expected.Binding.Address, Port: expected.Binding.Port}, expected.Key, request, current)
				return err
			})
			if err != nil {
				return clusterremote.TargetFilesystem{}, err
			}
			return value, nil
		}
		readers.Boot = func(caller context.Context, item clusterSSHInspectedTarget, request clusterremote.NetworkRenderRequest, outer func(context.Context) error) (clusterremote.TargetNetworkBoot, error) {
			request.Targets.Peers = slices.Clone(request.Targets.Peers)
			var value clusterremote.TargetNetworkBoot
			err := read(caller, item, outer, func(expected clusterSSHInspectedTarget) bool {
				return outer != nil && request.Validate() == nil && request.MachineID == expected.Report.MachineID && request.BootID == expected.Report.BootID &&
					request.Targets.Management == expected.Binding.Address && slices.Equal(request.Targets.Peers, clusterSSHNetworkTargetPeers(frozen, expected.Binding.Address))
			}, func(ctx context.Context, expected clusterSSHInspectedTarget, client *clusterremote.Client, sudo []byte, current func(context.Context) error) error {
				var err error
				value, err = client.InspectNetworkBoot(ctx, sudo, clusterremote.Target{Address: expected.Binding.Address, Port: expected.Binding.Port}, expected.Key, request, current)
				return err
			})
			if err != nil {
				return clusterremote.TargetNetworkBoot{}, err
			}
			return value, nil
		}
		readers.ARP = func(caller context.Context, item clusterSSHInspectedTarget, request clusterremote.ARPRequest, outer func(context.Context) error) (clusterremote.TargetARPObservation, error) {
			request.Peers = slices.Clone(request.Peers)
			var value clusterremote.TargetARPObservation
			err := read(caller, item, outer, func(expected clusterSSHInspectedTarget) bool {
				addresses := make([]string, 0, len(request.Peers))
				for _, peer := range request.Peers {
					addresses = append(addresses, peer.Address)
				}
				return outer != nil && request.Validate() == nil && request.MachineID == expected.Report.MachineID && request.BootID == expected.Report.BootID &&
					request.Link.MatchesAddress(expected.Binding.Address) && slices.Equal(addresses, clusterSSHNetworkTargetPeers(frozen, expected.Binding.Address))
			}, func(ctx context.Context, expected clusterSSHInspectedTarget, client *clusterremote.Client, sudo []byte, current func(context.Context) error) error {
				var err error
				value, err = client.ProbeARP(ctx, sudo, clusterremote.Target{Address: expected.Binding.Address, Port: expected.Binding.Port}, expected.Key, request, current)
				return err
			})
			if err != nil {
				return clusterremote.TargetARPObservation{}, err
			}
			return value, nil
		}
		if consume(readerCtx, readers) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return check(ctx)
	})
}

func clusterSSHNetworkTargetPeers(a clusterSSHPreparationAuthority, management string) []string {
	addresses := []string{a.Source.ControlPlaneVIP, a.Source.EdgeVIP}
	for _, member := range a.Source.Members {
		addresses = append(addresses, member.Address)
	}
	for _, target := range a.Cohort.Targets {
		addresses = append(addresses, target.Binding.Address)
	}
	slices.Sort(addresses)
	return slices.DeleteFunc(slices.Compact(addresses), func(address string) bool { return address == management })
}

func clusterSSHNetworkCredential(envelope clusterSSHCredentialEnvelope) (*clusterremote.Credential, []byte, error) {
	password, key, passphrase := []byte(envelope.Password), []byte(envelope.PrivateKey), []byte(envelope.Passphrase)
	defer clear(password)
	defer clear(key)
	defer clear(passphrase)
	var credential *clusterremote.Credential
	var err error
	switch envelope.Method {
	case "password":
		credential, err = clusterremote.PasswordCredential(envelope.Username, password)
	case "private_key":
		credential, err = clusterremote.KeyCredential(envelope.Username, key, passphrase)
	default:
		return nil, nil, clusterbootstrap.ErrPreparationConfig
	}
	if err != nil {
		return nil, nil, clusterbootstrap.ErrPreparationConfig
	}
	return credential, []byte(envelope.SudoPassword), nil
}
