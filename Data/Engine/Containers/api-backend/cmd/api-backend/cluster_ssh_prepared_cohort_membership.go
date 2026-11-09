package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Three independently observed local identities and matching complete member
// views. This is not quorum availability, partition proof or admission authority.
type clusterSSHPreparedCohortMembershipObservation struct {
	Prepared clusterSSHPreparedObservation
	Views    []clusterSSHMembershipReceipt
}

// Callable only by the original settled-phase controller. No poller, worker
// claim, preparation retry, settlement or admission transition is installed.
func (c *clusterController) observeClusterSSHPreparedCohortMembership(parent context.Context, operation clusterControllerOperation) (clusterSSHPreparedCohortMembershipObservation, error) {
	if c == nil || parent.Err() != nil {
		return clusterSSHPreparedCohortMembershipObservation{}, errClusterConflict
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.controllerHolder != c.holder {
		return clusterSSHPreparedCohortMembershipObservation{}, errClusterConflict
	}
	holder := c.holder
	read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
		if c.holder != holder || c.runner != runner || runner.controllerHolder != holder {
			return clusterSSHPreparedAuthority{}, 0, errClusterConflict
		}
		return c.loadClusterSSHPreparedAuthority(ctx, operation)
	}
	return readClusterSSHPreparedCohortMembership(parent, operation, holder, runner, read)
}

// The supplied reader is the original controller's loadPreparedAuthority. Its
// transactions finish before any native read; tests can exercise the same
// orchestration without manufacturing a worker lease or an alternate producer.
func readClusterSSHPreparedCohortMembership(parent context.Context, operation clusterControllerOperation, holder string,
	runner *kubernetesClusterStepRunner, read func(context.Context) (clusterSSHPreparedAuthority, int64, error)) (clusterSSHPreparedCohortMembershipObservation, error) {
	fail := func() (clusterSSHPreparedCohortMembershipObservation, error) {
		return clusterSSHPreparedCohortMembershipObservation{}, errClusterConflict
	}
	if parent.Err() != nil || holder == "" || runner == nil || runner.kube == nil || runner.kube.httpClient == nil || runner.controllerHolder != holder || read == nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	// Own only response-body cleanup accounting. The shared transport, token,
	// redirects, timeouts and remote Job lifetime retain their existing behavior.
	transport := runner.kube.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cleanup := &clusterSSHMembershipHTTP{transport: transport}
	client, kube, jobs := *runner.kube.httpClient, *runner.kube, *runner
	client.Transport = cleanup
	kube.httpClient, jobs.kube = &client, &kube

	before, started, err := read(ctx)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	// SQL readers may reuse nested allocations. Keep a wholly owned copy of
	// proof, receipts and sealed-credential digests before the first callback.
	frozen := clusterSSHPreparedAuthority{payload: before.payload, baseline: before.baseline, queue: before.queue, version: before.version}
	for _, value := range []struct{ from, to any }{{before.proof, &frozen.proof}, {before.receipt, &frozen.receipt}, {before.authorization, &frozen.authorization}} {
		raw, err := json.Marshal(value.from)
		if err != nil || json.Unmarshal(raw, value.to) != nil {
			return fail()
		}
	}
	before = frozen
	if len(before.receipt.Targets) < 1 || len(before.receipt.Targets) > 2 || len(before.receipt.Targets) != len(before.proof.Cohort.Targets) ||
		len(before.proof.Source.Members)+len(before.receipt.Targets) != 3 || validateClusterSSHInspectionCohort(before.proof.Cohort, before.proof.Source) != nil {
		return fail()
	}
	for _, target := range before.receipt.Targets {
		if target.Prejoin == nil {
			return fail()
		}
	}
	selected := before.receipt.Targets[0].Prejoin.Receipt.Source
	request := clusterbootstrap.MembershipObservationRequest{Source: selected, KubeSystemUID: before.proof.Source.KubeSystemUID, RequireListenerProof: true}
	selectedOriginal := false
	for _, source := range before.proof.Source.Members {
		request.OriginalSourceNodeUIDs = append(request.OriginalSourceNodeUIDs, source.NodeUID)
		selectedOriginal = selectedOriginal || selected.ClusterID == before.proof.Source.ClusterID && selected.SourceID == source.NodeID && selected.SourceUID == source.NodeUID
	}
	if !selectedOriginal {
		return fail()
	}
	for _, target := range before.receipt.Targets {
		request.Prepared = append(request.Prepared, clusterbootstrap.MembershipPreparedTarget{Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt})
	}
	nativeNodes := func(readCtx context.Context) ([]clusterbootstrap.MembershipNodeExpectation, map[string]clusterbootstrap.K3sJoinedNode, error) {
		nodeCtx, nodeCancel := context.WithTimeout(readCtx, clusterSSHJoinedNodeObservationTimeout)
		defer nodeCancel()
		return readClusterSSHPreparedMembershipNodes(nodeCtx, kube.getClusterSSHNodeCohortJSON, before)
	}
	nodes, targets, err := nativeNodes(ctx)
	if err != nil || !cleanup.clean() || len(nodes) != 3 {
		return fail()
	}
	request.Nodes = nodes
	finished := started
	sameAuthority := func(checkCtx context.Context) bool {
		current, observed, err := read(checkCtx)
		if err != nil || checkCtx.Err() != nil || runner.controllerHolder != holder || observed < finished || !reflect.DeepEqual(before, current) {
			return false
		}
		finished = observed
		return true
	}
	check := func(checkCtx context.Context) bool {
		valid := cleanup.clean() && checkCtx.Err() == nil && sameAuthority(checkCtx)
		if valid {
			currentNodes, currentTargets, err := nativeNodes(checkCtx)
			valid = err == nil && cleanup.clean() && reflect.DeepEqual(nodes, currentNodes) && reflect.DeepEqual(targets, currentTargets) && sameAuthority(checkCtx)
		}
		if !valid {
			cleanup.failed.Store(true)
		}
		return valid
	}
	views := make([]clusterSSHMembershipReceipt, 0, 3)
	uids := map[string]bool{}
	jobIDs := [3]map[string]bool{{}, {}, {}}
	localIDs := map[uint64]bool{}
	for _, node := range nodes {
		// Both roles come only from the complete retained partition above and
		// the strict current Node observation. No logical SourceID is invented.
		host := clusterSSHObservationHost{node.Node.NodeUID, node.Node.Hostname, node.Node.Address, node.Node.MachineID, node.Node.BootID}
		networkCtx, networkCancel := context.WithTimeout(ctx, 30*time.Second)
		network, err := jobs.readSSHHostObservation(networkCtx, host, operation.ID, before.version, "", false, func() bool { return check(networkCtx) })
		networkCancel()
		if err != nil || !cleanup.clean() || !retainClusterSSHMembershipJob(jobIDs, network.Nonce, network.JobUID, network.PodUID) {
			return fail()
		}
		request.Network = network.Observation.Network
		request.Observer = &clusterbootstrap.MembershipObserver{NodeUID: node.Node.NodeUID}
		if !request.Valid() {
			return fail()
		}
		membershipCtx, membershipCancel := context.WithTimeout(ctx, 30*time.Second)
		receipt, err := jobs.readSSHPreparedMembership(membershipCtx, operation.ID, request, func() bool { return check(membershipCtx) })
		membershipCancel()
		o := receipt.Observation
		if err != nil || !cleanup.clean() || !o.Valid(request) || !retainClusterSSHMembershipJob(jobIDs, receipt.Nonce, receipt.JobUID, receipt.PodUID) ||
			uids[o.LocalNode.NodeUID] || localIDs[o.Membership.LocalID] ||
			len(views) > 0 && !reflect.DeepEqual(views[0].Observation.Membership.Members, o.Membership.Members) {
			return fail()
		}
		uids[o.LocalNode.NodeUID], localIDs[o.Membership.LocalID] = true, true
		views = append(views, receipt)
	}
	if len(views) != 3 || !check(ctx) || !cleanup.clean() || ctx.Err() != nil {
		return fail()
	}
	sum := sha256.Sum256([]byte(before.payload))
	prepared := clusterSSHPreparedObservation{operation.ID, holder, hex.EncodeToString(sum[:]), before.queue.ClusterID, before.proof.Source.KubeSystemUID, operation.Attempt, finished, targets}
	return clusterSSHPreparedCohortMembershipObservation{prepared, slices.Clone(views)}, nil
}

func retainClusterSSHMembershipJob(seen [3]map[string]bool, nonce, jobUID, podUID string) bool {
	if !clusterbootstrap.ValidSourceReceiptIdentity(nonce, jobUID, podUID) || seen[0][nonce] || seen[1][jobUID] || seen[2][podUID] {
		return false
	}
	seen[0][nonce], seen[1][jobUID], seen[2][podUID] = true, true, true
	return true
}

// New collector owns these wrappers, never the underlying shared transport.
// Close errors are sticky and response bodies must finish before evidence leaves.
// This neither deletes a Job nor proves that a remote producer has stopped.
type clusterSSHMembershipHTTP struct {
	transport http.RoundTripper
	failed    atomic.Bool
	active    atomic.Int64
}

func (c *clusterSSHMembershipHTTP) clean() bool { return !c.failed.Load() && c.active.Load() == 0 }
func (c *clusterSSHMembershipHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	c.active.Add(1)
	response, err := c.transport.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		c.failed.Store(true)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		c.active.Add(-1)
		return nil, errClusterConflict
	}
	response.Body = &clusterSSHMembershipBody{ReadCloser: response.Body, owner: c}
	return response, nil
}

type clusterSSHMembershipBody struct {
	io.ReadCloser
	owner *clusterSSHMembershipHTTP
	once  sync.Once
	err   error
}

func (b *clusterSSHMembershipBody) Close() error {
	b.once.Do(func() {
		if b.ReadCloser.Close() != nil {
			b.owner.failed.Store(true)
			b.err = errClusterConflict
		}
		b.owner.active.Add(-1)
	})
	return b.err
}
