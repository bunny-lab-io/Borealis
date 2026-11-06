package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"sync/atomic"
	"time"
)

// Controller-only waiting-phase reader. It lends no private configuration or
// worker capability and has no production caller. Every invocation creates
// fresh native source Jobs; a previous receipt cannot satisfy this gate.
func (c *clusterController) preparationConfigurationRead(snapshot clusterSSHPreparationApprovalSnapshot) func(context.Context, string) (string, error) {
	wire, wireErr := json.Marshal(snapshot.work)
	var work clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &work)
	credentials := slices.Clone(snapshot.credentials)
	operation := clusterControllerOperation{ID: work.Authorization.OperationID, Attempt: work.Authorization.Attempt,
		Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	check := func(parent context.Context) error {
		if c == nil || c.store == nil || c.store.db == nil || wireErr != nil || decodeErr != nil || len(work.Claims) != 0 || c.holder != work.Authorization.ControllerHolder {
			return clusterbootstrap.ErrPreparationConfig
		}
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		current, _, err := c.loadClusterSSHPreparationApproval(ctx, operation)
		actual, encodeErr := json.Marshal(current.work)
		if err != nil || encodeErr != nil || !bytes.Equal(wire, actual) || !slices.Equal(credentials, current.credentials) || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	var runner *kubernetesClusterStepRunner
	if c != nil {
		runner, _ = c.runner.(*kubernetesClusterStepRunner)
	}
	return newClusterSSHPreparationConfigurationNativeRead(work, check, runner)
}

// Scope response cleanup to this reader. Shared transports and the original
// runner remain borrowed; the copied client owns no idle connections or Jobs.
func newClusterSSHPreparationConfigurationNativeRead(work clusterSSHPreparationWork, check func(context.Context) error,
	runner *kubernetesClusterStepRunner) func(context.Context, string) (string, error) {
	if runner == nil || runner.kube == nil || runner.kube.httpClient == nil || check == nil {
		return newClusterSSHPreparationConfigurationRead(work, nil, nil, nil)
	}
	transport := runner.kube.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	client, kube, jobs := *runner.kube.httpClient, *runner.kube, *runner
	client.Transport = cleanup
	kube.httpClient, jobs.kube = &client, &kube
	current := func(ctx context.Context) error {
		if !cleanup.clean() || runner.namespace != "borealis" || runner.controllerHolder != work.Authorization.ControllerHolder ||
			ctx.Err() != nil || check(ctx) != nil || !cleanup.clean() || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	get := func(ctx context.Context, path string, out any) error {
		if !cleanup.clean() || runner.namespace != "borealis" || runner.controllerHolder != work.Authorization.ControllerHolder {
			return clusterbootstrap.ErrPreparationConfig
		}
		if path == "/api/v1/nodes" || path == "/api/v1/namespaces/kube-system" {
			return kube.getClusterSSHNodeCohortJSON(ctx, path, out)
		}
		return kube.getClusterSSHPreparationJSON(ctx, path, out)
	}
	var version string
	_ = clusterBootstrapObject([]byte(work.Payload), map[string]any{"source_k3s_version": &version})
	network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
		if current(ctx) != nil {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		value, err := jobs.readSSHSourceObservation(ctx, member, work.Authorization.OperationID, version, "", false, func() bool { return current(ctx) == nil })
		return value.Network, err
	}
	// The existing serial reader checks current after every synchronous native
	// read and once more before returning. Close failures remain sticky even if
	// an underlying reader ignores them; blocked closure joins before SQL recheck.
	return newClusterSSHPreparationConfigurationRead(work, current, get, network)
}

// Private requests have their own shorter deadline. A successful body Close
// cannot erase its expiry while the enclosing 30-second proof is still live.
// The existing tracker owns closure and records this error permanently.
type clusterSSHPreparationConfigurationHTTP struct{ http.RoundTripper }

func (t clusterSSHPreparationConfigurationHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(request)
	if response != nil && response.Body != nil {
		response.Body = &clusterSSHPreparationConfigurationBody{ReadCloser: response.Body, ctx: request.Context()}
	}
	return response, err
}

type clusterSSHPreparationConfigurationBody struct {
	io.ReadCloser
	ctx context.Context
}

func (b *clusterSSHPreparationConfigurationBody) Close() error {
	err := b.ReadCloser.Close()
	if err != nil || b.ctx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

// Private composition seam. Production supplies the waiting-proposal checker,
// fixed private Kubernetes reader and fresh Job transport above. Only digest
// equality survives a call; all source and Secret evidence is reread each time.
func newClusterSSHPreparationConfigurationRead(work clusterSSHPreparationWork, check func(context.Context) error,
	getJSON func(context.Context, string, any) error, network clusterSSHSourceNetworkRead) func(context.Context, string) (string, error) {
	wire, wireErr := json.Marshal(work)
	var owned clusterSSHPreparationWork
	decodeErr := json.Unmarshal(wire, &owned)
	plan, planErr := clusterSSHPreparationPlanDigest(owned.Authorization)
	var version string
	versionErr := clusterBootstrapObject([]byte(owned.Payload), map[string]any{"source_k3s_version": &version})
	serial := make(chan struct{}, 1)
	var failed atomic.Bool
	var retained string
	return func(parent context.Context, requested string) (string, error) {
		fail := func() (string, error) { failed.Store(true); return "", clusterbootstrap.ErrPreparationConfig }
		if failed.Load() || parent.Err() != nil || wireErr != nil || decodeErr != nil || planErr != nil || versionErr != nil || requested != plan ||
			len(owned.Claims) != 0 || check == nil || getJSON == nil || network == nil || owned.Baseline.Validate() != nil || version != clusterbootstrap.K3sPins().Version ||
			validateClusterSSHInspectionCohort(owned.Proof.Cohort, owned.Proof.Source) != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return fail()
		}
		if failed.Load() || check(ctx) != nil {
			return fail()
		}
		get := func(ctx context.Context, path string, out any) error {
			if check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			err := getJSON(ctx, path, out)
			if err != nil || check(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		readSource := func() error {
			actual, err := observeClusterSSHSourceCohort(ctx, get, owned.Proof.Source)
			if err != nil || !reflect.DeepEqual(actual, owned.Proof.Source) {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		readNetworks := func() ([]clusterbootstrap.SourceNetwork, error) {
			values := make([]clusterbootstrap.SourceNetwork, 0, len(owned.Proof.Source.Members))
			for _, member := range owned.Proof.Source.Members {
				if check(ctx) != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				value, err := network(ctx, member)
				if err != nil || check(ctx) != nil || ctx.Err() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				values = append(values, value)
			}
			if validateClusterSSHPlannedSourceNetworks(owned.Proof.Cohort, owned.Proof.Source, version, values) != nil {
				return nil, clusterbootstrap.ErrPreparationConfig
			}
			return values, nil
		}
		readSecret := func() (clusterbootstrap.PreparationRuntimeSecret, error) {
			var raw json.RawMessage
			if get(ctx, clusterSSHRuntimeSecretPath, &raw) != nil {
				return clusterbootstrap.PreparationRuntimeSecret{}, clusterbootstrap.ErrPreparationConfig
			}
			defer clear(raw)
			return clusterbootstrap.ParsePreparationRuntimeSecret(raw)
		}
		if readSource() != nil {
			return fail()
		}
		sources, err := readNetworks()
		if err != nil {
			return fail()
		}
		secret, err := readSecret()
		if err != nil {
			return fail()
		}
		digest, err := clusterSSHPreparationConfigurationReceipt(owned.Baseline, owned.Proof, plan, version, secret, sources)
		if err != nil {
			return fail()
		}
		again, err := readNetworks()
		if err != nil || !slices.Equal(sources, again) || readSource() != nil {
			return fail()
		}
		current, err := readSecret()
		if err != nil || !secret.SameObservation(current) || check(ctx) != nil || ctx.Err() != nil {
			return fail()
		}
		rechecked, err := clusterSSHPreparationConfigurationReceipt(owned.Baseline, owned.Proof, plan, version, current, sources)
		if err != nil || rechecked != digest {
			return fail()
		}
		if err != nil || ctx.Err() != nil || (retained != "" && retained != digest) || failed.Load() {
			return fail()
		}
		retained = digest
		return digest, nil
	}
}

// Pure equality receipt shared by the waiting gate and separately fenced
// recovery reader. Secret identity/revision/content and full runtime values are
// retained; equal selected values cannot replace an original observation.
func clusterSSHPreparationConfigurationReceipt(baseline clusterbootstrap.Expected, proof clusterSSHParentProof, plan, version string,
	secret clusterbootstrap.PreparationRuntimeSecret, sources []clusterbootstrap.SourceNetwork) (string, error) {
	if baseline.Validate() != nil || validateClusterSSHInspectionCohort(proof.Cohort, proof.Source) != nil ||
		validateClusterSSHPlannedSourceNetworks(proof.Cohort, proof.Source, version, sources) != nil {
		return "", clusterbootstrap.ErrPreparationConfig
	}
	projection := clusterbootstrap.PreparationPlan{Source: baseline, PlanSHA256: plan, KubeSystemUID: proof.Source.KubeSystemUID, K3sVersion: version,
		ManagementCIDR: proof.Cohort.Targets[0].Report.ConnectedPrefix, PodCIDR: sources[0].PodCIDR, ServiceCIDR: sources[0].ServiceCIDR,
		ControlPlaneVIP: proof.Source.ControlPlaneVIP, EdgeVIP: proof.Source.EdgeVIP}
	for _, member := range proof.Source.Members {
		projection.PeerAddresses = append(projection.PeerAddresses, member.Address)
	}
	for _, target := range proof.Cohort.Targets {
		projection.PeerAddresses = append(projection.PeerAddresses, target.Binding.Address)
		projection.TargetAddresses = append(projection.TargetAddresses, target.Binding.Address)
	}
	sort.Strings(projection.PeerAddresses)
	configuration, err := clusterbootstrap.PreparationPlanConfigurationDigest(projection, secret.Settings())
	if err != nil {
		return "", err
	}
	return clusterSSHPreparationDigest(struct {
		Version                     int
		Plan, Configuration, Secret string
		Sources                     []clusterbootstrap.SourceNetwork
	}{1, plan, configuration, secret.ObservationSHA256(), sources})
}
