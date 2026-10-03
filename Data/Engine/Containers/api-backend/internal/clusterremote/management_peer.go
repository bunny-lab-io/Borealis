package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"slices"
	"time"
)

// TargetManagementPeer can only be acquired through the pinned SSH client.
// Local monotonic times are deliberately absent from wire/durable reports.
// The caller still owns whole-cohort and per-target credential/lease authority.
type TargetManagementPeer struct {
	link              TargetManagementLink
	host              HostFacts
	facts             PrivilegedFacts
	started, finished time.Time
}

// ManagementLink rejects observations acquired before this collection round.
// Host expectations come from independently rechecked current cohort authority.
func (value TargetManagementPeer) ManagementLink(notBefore time.Time, hostname, machineID, bootID string,
	target Target, key HostKey, peers []string) (clusterbootstrap.ManagementLink, error) {
	if notBefore.IsZero() || value.started.Before(notBefore) || value.finished.Before(value.started) || value.finished.After(time.Now()) ||
		value.finished.Sub(value.started) > time.Minute || value.host.Hostname != hostname || hostname == "" ||
		value.facts.MachineID != machineID || value.facts.BootID != bootID || !value.facts.NoExistingInstallation() {
		return clusterbootstrap.ManagementLink{}, ErrManagementLink
	}
	return value.link.ManagementLink(value.facts, target, key, peers)
}

// HostFacts exposes only fresh hardware/platform and limited negative
// installation inventory already collected by this native observation. Free
// space is the lower of two readings, not a reservation or storage acceptance.
func (value TargetManagementPeer) HostFacts(notBefore time.Time, hostname, machineID, bootID string,
	target Target, key HostKey, peers []string) (HostFacts, error) {
	if _, err := value.ManagementLink(notBefore, hostname, machineID, bootID, target, key, peers); err != nil ||
		!value.host.SupportedPlatform() || value.host.CPUCount == 0 || value.host.MemoryKiB == 0 || value.host.DiskTotalKiB == 0 ||
		value.host.DiskFreeKiB > value.host.DiskTotalKiB || value.host.BorealisPath != "absent" || value.host.K3sUnit != "not-found" {
		return HostFacts{}, ErrManagementLink
	}
	return value.host, nil
}

// InspectManagementPeer brackets the link observer with independent host and
// privileged reads. check must freshly verify this target's credential/lease as
// well as whole-cohort authority, honor ctx and join any children it starts.
// Calls are synchronous; no callback survives return. This performs no writes.
func (client *Client) InspectManagementPeer(parent context.Context, sudoPassword []byte, targets RouteTargets,
	expected Target, approved HostKey, check func(context.Context) error) (TargetManagementPeer, error) {
	started := time.Now()
	targets.Peers = slices.Clone(targets.Peers)
	slices.Sort(targets.Peers)
	fail := func() (TargetManagementPeer, error) { return TargetManagementPeer{}, ErrManagementLink }
	if client == nil || client.ssh == nil || targets.validate() != nil || targets.Management != expected.Address || expected.Validate() != nil || approved.Validate() != nil ||
		client.target != expected || client.approved.Algorithm != approved.Algorithm || client.approved.Fingerprint != approved.Fingerprint || !bytes.Equal(client.approved.PublicKey, approved.PublicKey) ||
		check == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	boundary := func() bool { return ctx.Err() == nil && check(ctx) == nil && ctx.Err() == nil }
	if !boundary() {
		return fail()
	}
	facts, err := client.InspectPrivileged(ctx, sudoPassword)
	if err != nil || !boundary() || !facts.NoExistingInstallation() {
		return fail()
	}
	host, err := client.Inspect(ctx)
	if err != nil || !boundary() || !host.SupportedPlatform() || host.BorealisPath != "absent" || host.K3sUnit != "not-found" {
		return fail()
	}
	link, err := client.InspectManagementLink(ctx, sudoPassword, targets)
	if err != nil || !boundary() {
		return fail()
	}
	before, err := link.ManagementLink(facts, client.target, client.approved, targets.Peers)
	if err != nil {
		return fail()
	}
	rechecked, err := client.Inspect(ctx)
	if err != nil || !boundary() {
		return fail()
	}
	current, err := client.InspectPrivileged(ctx, sudoPassword)
	if err != nil || !boundary() || !current.NoExistingInstallation() {
		return fail()
	}
	after, err := link.ManagementLink(current, client.target, client.approved, targets.Peers)
	if err != nil || before != after {
		return fail()
	}
	// Free space may change normally during observation. All other reported
	// host fields must remain stable; storage qualification is a separate gate.
	free := min(host.DiskFreeKiB, rechecked.DiskFreeKiB)
	host.DiskFreeKiB, rechecked.DiskFreeKiB = 0, 0
	if host != rechecked {
		return fail()
	}
	host.DiskFreeKiB = free
	return TargetManagementPeer{link: link, host: host, facts: current, started: started, finished: time.Now()}, nil
}
