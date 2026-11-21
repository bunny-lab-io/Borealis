package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// These complete, drained Node observations still need independent native
// journal/receipt matching. Custody grants no preparation, reconciliation,
// completion, readiness or admission capability.
type clusterSSHPreparationRecoveryReissuedCustody struct {
	Original clusterSSHPrejoinSourceCustody
	Targets  map[string]clusterbootstrap.K3sJoinedNode
}

func (clusterSSHPreparationRecoveryReissuedCustody) String() string {
	return "reissued preparation source custody [redacted]"
}
func (p clusterSSHPreparationRecoveryReissuedCustody) GoString() string { return p.String() }

type clusterSSHPreparationRecoveryReissuedCustodyRead func(context.Context) (clusterSSHPreparationRecoveryReissuedCustody, error)
type clusterSSHPreparationRecoveryReissuedMaterialConsumer func(context.Context, clusterSSHPreparationRecoveryReissuedCustody, *engineidentity.Material, func(context.Context) error) error

// Controller-only native reader. It proves the selected API Pod's custody;
// the controller does not impersonate that Pod or load its private material.
// Authenticated cross-role transport for reissued custody remains unwired.
func (c *clusterController) preparationRecoveryReissuedCustodyRead(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string,
	source clusterSSHPrejoinSource, check func(context.Context) error) clusterSSHPreparationRecoveryReissuedCustodyRead {
	var runner *kubernetesClusterStepRunner
	if c != nil {
		runner, _ = c.runner.(*kubernetesClusterStepRunner)
	}
	if runner == nil || runner.kube == nil || runner.kube.httpClient == nil || check == nil {
		return unavailableClusterSSHPreparationRecoveryReissuedCustody
	}
	// Own response accounting, never the shared transport. Deferred body Close
	// is synchronous; ignored Close errors or actual child-request expiry during
	// closure still poison the typed reader while its outer authority stays live.
	transport := runner.kube.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	client, kube := *runner.kube.httpClient, *runner.kube
	client.Transport, kube.httpClient = cleanup, &client
	holder := value.Lineage.Original.ControllerHolder
	get := func(ctx context.Context, path string, out any) error {
		current, _ := c.runner.(*kubernetesClusterStepRunner)
		if !clusterControllerEligible() || current != runner || runner.namespace != "borealis" || c.holder != holder || runner.controllerHolder != holder {
			return clusterbootstrap.ErrSessionAuthority
		}
		if path == "/api/v1/nodes" || path == "/api/v1/namespaces/kube-system" {
			return kube.getClusterSSHNodeCohortJSON(ctx, path, out)
		}
		if path != "/api/v1/namespaces/borealis/pods/"+source.Process.PodName {
			return clusterbootstrap.ErrSessionAuthority
		}
		return kube.getJSON(ctx, path, out)
	}
	current := func(ctx context.Context) error {
		if !cleanup.clean() || check(ctx) != nil || !cleanup.clean() {
			return clusterbootstrap.ErrSessionAuthority
		}
		return nil
	}
	return newClusterSSHPreparationRecoveryReissuedCustodyRead(value, payload, source, current, get)
}

func unavailableClusterSSHPreparationRecoveryReissuedCustody(context.Context) (clusterSSHPreparationRecoveryReissuedCustody, error) {
	return clusterSSHPreparationRecoveryReissuedCustody{}, clusterbootstrap.ErrSessionAuthority
}

func freezeClusterSSHPreparationRecoveryReissuedCustody(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string,
	source clusterSSHPrejoinSource) (clusterSSHPreparationRecoveryReissuedProofAuthority, string, string, error) {
	fail := func() (clusterSSHPreparationRecoveryReissuedProofAuthority, string, string, error) {
		return clusterSSHPreparationRecoveryReissuedProofAuthority{}, "", "", clusterbootstrap.ErrSessionAuthority
	}
	raw, err := json.Marshal(value)
	var frozen clusterSSHPreparationRecoveryReissuedProofAuthority
	if err != nil || len(raw) > 256<<10 || json.Unmarshal(raw, &frozen) != nil ||
		!validClusterSSHPreparationRecoveryReissuedSource(frozen, payload) || !source.validIdentity() || source.Binding != frozen.IdentitySource ||
		len(frozen.Source.Members)+len(frozen.Cohort.Targets) != 3 {
		return fail()
	}
	recorded, err := clusterSSHPreparationRecordedSource(payload, clusterSSHParentProof{Version: 1, Cohort: frozen.Cohort, Source: frozen.Source}, frozen.Authorization.ConfirmationSHA256)
	var version string
	if err != nil || recorded != source.Binding || clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version}) != nil || version != clusterbootstrap.K3sPins().Version ||
		!clusterSSHPreparationRecoveryNonzeroUUID(frozen.Source.KubeSystemUID) {
		return fail()
	}
	selected := false
	for _, member := range frozen.Source.Members {
		for _, id := range []string{member.NodeID, member.NodeUID, member.BootID} {
			if !clusterSSHPreparationRecoveryNonzeroUUID(id) {
				return fail()
			}
		}
		if member.NodeID == source.Binding.SourceID && member.NodeUID == source.Binding.SourceUID && member.Name == source.Process.NodeName {
			selected = true
		}
	}
	if !selected {
		return fail()
	}
	stable := frozen
	stable.ExpiresAt = nil // Renewal changes expiry, never retained proof identity.
	digest, err := clusterSSHPreparationDigest(struct {
		Domain, PayloadSHA256 string
		Authority             clusterSSHPreparationRecoveryReissuedProofAuthority
	}{"reissued-preparation-source-custody-v1", clusterSSHPreparationRecoveryPayloadDigest([]byte(payload)), stable})
	if err != nil {
		return fail()
	}
	return frozen, version, digest, nil
}

// Only the typed issued scope supplies check: it verifies every original
// credential, raw SQL payload, full lineage and exact current cohort leases,
// returning its database connection before each native read.
func newClusterSSHPreparationRecoveryReissuedCustodyRead(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string,
	source clusterSSHPrejoinSource, check func(context.Context) error, getJSON func(context.Context, string, any) error) clusterSSHPreparationRecoveryReissuedCustodyRead {
	frozen, version, digest, err := freezeClusterSSHPreparationRecoveryReissuedCustody(value, payload, source)
	if err != nil || check == nil || getJSON == nil {
		return unavailableClusterSSHPreparationRecoveryReissuedCustody
	}
	gate := make(chan struct{}, 1)
	var failed atomic.Bool
	var retained *clusterSSHPreparationRecoveryReissuedCustody
	return func(parent context.Context) (clusterSSHPreparationRecoveryReissuedCustody, error) {
		fail := func() (clusterSSHPreparationRecoveryReissuedCustody, error) {
			failed.Store(true)
			return unavailableClusterSSHPreparationRecoveryReissuedCustody(parent)
		}
		ctx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail()
		}
		current := func() error {
			if failed.Load() || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			return nil
		}
		get := func(ctx context.Context, path string, out any) error {
			if current() != nil || getJSON(ctx, path, out) != nil || current() != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			return nil
		}
		observe := func() (map[string]clusterbootstrap.K3sJoinedNode, error) {
			nodes, err := observeClusterSSHJoinedNodes(ctx, get, frozen.Cohort, frozen.Source, version)
			if err != nil || len(nodes) != len(frozen.Cohort.Targets) {
				return nil, clusterbootstrap.ErrSessionAuthority
			}
			for _, node := range nodes {
				if !node.Ready {
					return nil, clusterbootstrap.ErrSessionAuthority
				}
			}
			return nodes, nil
		}
		if current() != nil {
			return fail()
		}
		nodes, err := observe()
		if err != nil {
			return fail()
		}
		pod := func() (clusterExistingIdentitySourceCustody, error) {
			var raw map[string]any
			if get(ctx, "/api/v1/namespaces/borealis/pods/"+source.Process.PodName, &raw) != nil {
				return clusterExistingIdentitySourceCustody{}, clusterbootstrap.ErrSessionAuthority
			}
			return clusterIdentityPodCustody(raw, frozen.Baseline.SourceSHA, source.Process)
		}
		before, err := pod()
		if err != nil {
			return fail()
		}
		after, err := pod()
		if err != nil || before != after {
			return fail()
		}
		again, err := observe()
		if err != nil || !maps.Equal(nodes, again) || current() != nil {
			return fail()
		}
		result := clusterSSHPreparationRecoveryReissuedCustody{Original: clusterSSHPrejoinSourceCustody{source, before, digest}, Targets: nodes}
		if !result.Original.valid(source) || retained != nil && !reflect.DeepEqual(*retained, result) {
			return fail()
		}
		owned := result
		owned.Targets = maps.Clone(result.Targets)
		retained = &owned
		return result, nil
	}
}

// Worker-only material scope. read must eventually come from an authenticated
// original-controller custody broker; no such reissued wire is enabled here.
// The API worker retains its existing local Consumer and no Kubernetes access.
// The future caller must also own the live typed worker SQL/Aegis scope around
// every read and the complete material callback. Metadata and its digest do not
// supply that authority, and this helper cannot establish it from a callback.
func withClusterSSHPreparationRecoveryReissuedLocalMaterial(parent context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string,
	source clusterSSHPrejoinSource, read clusterSSHPreparationRecoveryReissuedCustodyRead, consume clusterSSHPreparationRecoveryReissuedMaterialConsumer) error {
	return withClusterSSHPreparationRecoveryReissuedMaterial(parent, value, payload, source, processIdentityConsumer, clusterExistingIdentitySourceRoot, read, consume)
}

func withClusterSSHPreparationRecoveryReissuedMaterial(parent context.Context, value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, source clusterSSHPrejoinSource,
	consumer *engineidentity.Consumer, root string, read clusterSSHPreparationRecoveryReissuedCustodyRead,
	consume clusterSSHPreparationRecoveryReissuedMaterialConsumer) error {
	frozen, version, digest, err := freezeClusterSSHPreparationRecoveryReissuedCustody(value, payload, source)
	if err != nil || read == nil || consume == nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	var snapshot clusterSSHPreparationRecoveryReissuedCustody
	var first sync.Once
	project := func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
		if !clusterSSHPrejoinLocalProcessMatches(source) {
			return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		proof, err := read(ctx)
		if err != nil || !proof.Original.valid(source) || proof.Original.AuthoritySHA256 != digest || len(proof.Targets) != len(frozen.Cohort.Targets) {
			return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		seen := map[string]bool{}
		for _, member := range frozen.Source.Members {
			seen[member.NodeUID] = true
		}
		for _, target := range frozen.Cohort.Targets {
			node, ok := proof.Targets[target.Binding.TargetID]
			if !ok || !node.Ready || !clusterSSHPreparationRecoveryNonzeroUUID(node.NodeUID) || seen[node.NodeUID] || node.Hostname != target.Report.Hostname ||
				node.MachineID != target.Report.MachineID || node.BootID != target.Report.BootID || node.Address != target.Binding.Address || node.K3sVersion != version {
				return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
			}
			seen[node.NodeUID] = true
		}
		first.Do(func() {
			snapshot = proof
			snapshot.Targets = maps.Clone(proof.Targets)
		})
		if !reflect.DeepEqual(snapshot, proof) {
			return clusterSSHPrejoinSourceCustody{}, clusterbootstrap.ErrSessionAuthority
		}
		return proof.Original, nil
	}
	return withClusterSSHPrejoinSourceMaterialFor(parent, consumer, root, source, 10*time.Minute, project,
		func(ctx context.Context, material *engineidentity.Material, refresh func(context.Context) error) error {
			owned := snapshot
			owned.Targets = maps.Clone(snapshot.Targets)
			return consume(ctx, owned, material, refresh)
		})
}
