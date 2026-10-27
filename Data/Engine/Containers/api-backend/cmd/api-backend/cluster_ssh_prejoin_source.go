package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"time"
)

// Explicit source selection comes from controller-owned preparation work, not
// member order, local environment or target provisioning identity. This is a
// proof contract; it neither selects the source nor grants a preparation lease.
type clusterSSHPrejoinSource struct {
	Binding engineidentity.Binding
	Process clusterExistingIdentitySourceProcess
}

func (s clusterSSHPrejoinSource) validIdentity() bool {
	for _, id := range []string{s.Binding.ClusterID, s.Binding.SourceID, s.Binding.SourceUID, s.Process.PodUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return len(s.Process.PodName) > 0 && len(s.Process.PodName) <= 253 && clusterSSHStorageNameRE.MatchString(s.Process.PodName) &&
		len(validateClusterNodeName("node", s.Process.NodeName)) == 0
}

func (s clusterSSHPrejoinSource) valid(a clusterSSHPreparationAuthority) bool {
	if !s.validIdentity() || a.IdentitySource == nil || *a.IdentitySource != s.Binding || !validClusterSSHPreparationLease(a.Lease) || a.Recovery != nil || a.Baseline.Validate() != nil ||
		validateClusterSSHInspectionCohort(a.Cohort, a.Source) != nil || s.Binding.ClusterID != a.Source.ClusterID ||
		a.Lease.ControllerHolder != a.Cohort.ControllerHolder || a.Lease.OperationID != a.Cohort.OperationID || a.Lease.OperationAttempt != a.Cohort.Attempt ||
		!validClusterSSHPreparationDigest(a.PayloadSHA256) || s.Binding.SourceID == a.Lease.TargetID {
		return false
	}
	if a.Source.KubeSystemUID == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for _, member := range a.Source.Members {
		for _, id := range []string{member.NodeID, member.NodeUID, member.BootID} {
			if id == "00000000-0000-0000-0000-000000000000" {
				return false
			}
		}
	}
	claimed := false
	for _, target := range a.Cohort.Targets {
		if target.Binding.TargetID == a.Lease.TargetID && a.Lease.Generation > target.Generation {
			claimed = true
		}
	}
	if !claimed {
		return false
	}
	for _, member := range a.Source.Members {
		if member.NodeID == s.Binding.SourceID && member.NodeUID == s.Binding.SourceUID && member.Name == s.Process.NodeName {
			return true
		}
	}
	return false
}

type clusterSSHPrejoinSourceCustody struct {
	Source          clusterSSHPrejoinSource
	Pod             clusterExistingIdentitySourceCustody
	AuthoritySHA256 string
}

func (p clusterSSHPrejoinSourceCustody) valid(source clusterSSHPrejoinSource) bool {
	return source.validIdentity() && p.Source == source && p.Pod.Process == source.Process &&
		p.Pod.validRuntime() && validClusterSSHPreparationDigest(p.AuthoritySHA256)
}

// Only original controller's native reader supplies getJSON. Short authority
// reads must return database connections before Pod/Node IO; no private keys
// are loaded or transmitted here. Both native observations and complete durable
// authority must remain exact across the read, except its observation clock.
func readClusterSSHPrejoinSourceCustody(parent context.Context, source clusterSSHPrejoinSource, authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error) (clusterSSHPrejoinSourceCustody, error) {
	fail := func() (clusterSSHPrejoinSourceCustody, error) {
		return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
	}
	if authority == nil || getJSON == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !source.valid(before) {
		return fail()
	}
	// Own the snapshot before external reads; callbacks cannot mutate slices in
	// the retained authority and silently redefine the comparison baseline.
	before.Cohort.ObservedAt = 0
	raw, err := json.Marshal(before)
	if err != nil || len(raw) > 256<<10 {
		return fail()
	}
	var frozen clusterSSHPreparationAuthority
	if json.Unmarshal(raw, &frozen) != nil {
		return fail()
	}
	digest, err := clusterSSHPreparationDigest(frozen)
	if err != nil {
		return fail()
	}
	observeNodes := func() bool {
		observed, err := observeClusterSSHSourceCohort(ctx, func(ctx context.Context, path string, out any) error {
			if path != "/api/v1/nodes" {
				return getJSON(ctx, path, out)
			}
			var list map[string]any
			if getJSON(ctx, path, &list) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			metadata := clusterSSHStorageMap(list, "metadata")
			if value, ok := metadata["continue"]; ok && value != "" {
				return clusterbootstrap.ErrSessionAuthority
			}
			items := anySlice(list["items"])
			if len(items) != len(frozen.Source.Members) {
				return clusterbootstrap.ErrSessionAuthority
			}
			for _, item := range items {
				node, ok := item.(map[string]any)
				labels := clusterSSHStorageMap(clusterSSHStorageMap(node, "metadata"), "labels")
				if !ok || labels["borealis.io/engine-node"] != "true" || labels["borealis.io/application-state"] != "active" {
					return clusterbootstrap.ErrSessionAuthority
				}
			}
			wire, err := json.Marshal(list)
			if err != nil || json.Unmarshal(wire, out) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			return nil
		}, frozen.Source)
		if err != nil || !reflect.DeepEqual(observed, frozen.Source) {
			return false
		}
		// The shared observer reads namespace before its Node snapshot. Bracket
		// that snapshot here too; replacement after the last list cannot pass.
		var namespace map[string]any
		return getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace) == nil && ctx.Err() == nil &&
			clusterSSHStorageMap(namespace, "metadata")["uid"] == frozen.Source.KubeSystemUID
	}
	if !observeNodes() {
		return fail()
	}
	readPod := func() (clusterExistingIdentitySourceCustody, error) {
		var pod map[string]any
		if getJSON(ctx, "/api/v1/namespaces/borealis/pods/"+source.Process.PodName, &pod) != nil || ctx.Err() != nil {
			return clusterExistingIdentitySourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		return clusterIdentityPodCustody(pod, frozen.Baseline.SourceSHA, source.Process)
	}
	pod, err := readPod()
	if err != nil {
		return fail()
	}
	afterPod, err := readPod()
	if err != nil || pod != afterPod || !observeNodes() {
		return fail()
	}
	after, err := authority(ctx)
	if err != nil || !source.valid(after) || ctx.Err() != nil {
		return fail()
	}
	after.Cohort.ObservedAt = 0
	afterDigest, err := clusterSSHPreparationDigest(after)
	if err != nil || digest != afterDigest {
		return fail()
	}
	return clusterSSHPrejoinSourceCustody{Source: source, Pod: pod, AuthoritySHA256: digest}, nil
}

// The worker's source-directory consumer stays locked for this synchronous
// scope. Refresh is a synchronous, scope-owned capability for transport
// challenges: it re-proves custody AND the actual held-directory material.
// Failed or escaped refreshes cannot authorize acknowledgement. The consumer
// must honor cancellation and join its transport before returning.
func withClusterSSHPrejoinSourceMaterial(parent context.Context, consumer *engineidentity.Consumer, root string, source clusterSSHPrejoinSource,
	read func(context.Context) (clusterSSHPrejoinSourceCustody, error), consume func(context.Context, *engineidentity.Material, func(context.Context) error) error) error {
	if consumer == nil || read == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	before, err := read(ctx)
	if err != nil || !before.valid(source) || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	keys, err := consumer.LoadFrom(root)
	if err != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	after, err := read(ctx)
	if err != nil || after != before || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	current, err := consumer.LoadFrom(root)
	if err != nil || !keys.Equal(current) || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	// Serializing native reads also bounds contention with concurrent challenge
	// callers. Each challenge inherits the existing three-second transport
	// budget; no caller can replace the enclosing scope with Background().
	gate := make(chan struct{}, 1)
	check := func(parent context.Context) error {
		bound, stop := context.WithTimeout(parent, 3*time.Second)
		defer stop()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-bound.Done():
			return clusterbootstrap.ErrSessionAuthority
		}
		for i := 0; i < 2; i++ {
			proof, err := read(bound)
			if err != nil || proof != before || bound.Err() != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			current, err := consumer.LoadFrom(root)
			if err != nil || !keys.Equal(current) || bound.Err() != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
		}
		return nil
	}
	readerCtx, cancelReaders := context.WithCancel(ctx)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancelReaders}
	defer lifetime.close()
	refresh := func(caller context.Context) error {
		if lifetime.run(caller, check) != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		return nil
	}
	consumeErr := consume(readerCtx, keys, refresh)
	// Invalidate the capability and join active reads before final proof. A
	// consumer returning with a read in flight fails even if that read succeeds.
	if lifetime.close() != nil || consumeErr != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	return nil
}

// Production assembly never accepts a caller-chosen filesystem root or local
// Pod identity. Environment is only a match check; native broker observation
// and current SQL/Aegis authority independently prove the selected process.
func clusterSSHPrejoinLocalProcessMatches(source clusterSSHPrejoinSource) bool {
	return source.validIdentity() && source.Process == (clusterExistingIdentitySourceProcess{
		PodName: os.Getenv("BOREALIS_ENGINE_POD_NAME"), PodUID: os.Getenv("BOREALIS_ENGINE_POD_UID"), NodeName: os.Getenv("BOREALIS_ENGINE_NODE_NAME"),
	}) && os.Getenv("BOREALIS_API_BACKGROUND_LOOPS") == "1"
}

func withClusterSSHPrejoinProcessIdentity(ctx context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	consume func(context.Context, *engineidentity.Material, func(context.Context) error) error) error {
	if processIdentityConsumer == nil || !clusterSSHPrejoinLocalProcessMatches(source) || !validClusterSSHPreparationLease(claim.Lease) {
		return clusterbootstrap.ErrSessionAuthority
	}
	client, err := newClusterSSHPrejoinCustodyClient()
	if err != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	authority := newClusterSSHPreparationAuthorityRead(store, aegis, claim.Lease, baseline, claim.Sealed)
	read := func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
		if !clusterSSHPrejoinLocalProcessMatches(source) {
			return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		return client.observe(ctx, source, claim.Lease, baseline, claim.Sealed, authority)
	}
	return withClusterSSHPrejoinSourceMaterial(ctx, processIdentityConsumer, clusterExistingIdentitySourceRoot, source, read, consume)
}
