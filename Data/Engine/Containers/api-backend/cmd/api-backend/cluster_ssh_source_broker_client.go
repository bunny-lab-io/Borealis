package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var clusterSSHSourceObservationRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

type clusterSSHSourceBrokerClient struct {
	requestCipher, responseCipher cipher.AEAD
	httpClient                    *http.Client
	peers                         func(context.Context) ([]string, error)
}

func newClusterSSHSourceBrokerClientFromEnv() (*clusterSSHSourceBrokerClient, error) {
	return newClusterSSHSourceBrokerClientWithTimeout(clusterSSHSourceSnapshotTimeout)
}

func newClusterSSHSourceBrokerClientWithTimeout(budget time.Duration) (*clusterSSHSourceBrokerClient, error) {
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	requestCipher, err := clusterSSHSourceBrokerCipher(secret, "request")
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	responseCipher, err := clusterSSHSourceBrokerCipher(secret, "response")
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return &clusterSSHSourceBrokerClient{
		requestCipher: requestCipher, responseCipher: responseCipher,
		httpClient: &http.Client{Timeout: budget + 5*time.Second, Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			ResponseHeaderTimeout: budget + 2*time.Second,
		}},
		peers: clusterSSHSourceBrokerPeers,
	}, nil
}

func clusterSSHSourceBrokerPeers(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, clusterSSHSourceBrokerHost)
	if err != nil || len(addresses) == 0 || len(addresses) > 3 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	seen := map[string]bool{}
	var peers []string
	for _, address := range addresses {
		// Cluster is IPv4-only. Discovery cannot select public/loopback endpoints,
		// caller-supplied URLs, alternate ports or proxy configuration.
		if address.IP.To4() == nil || !address.IP.IsPrivate() || address.Zone != "" {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		peer := "http://" + net.JoinHostPort(address.IP.String(), "8090")
		if !seen[peer] {
			seen[peer] = true
			peers = append(peers, peer)
		}
	}
	sort.Strings(peers)
	return peers, nil
}

// One reader remains bound to one worker claim. Freeze the complete Secret
// observation across fresh requests; a process restart must start fresh guarded
// acquisition, never recover a private snapshot from an operation payload.
func (c *clusterSSHSourceBrokerClient) preparationRead(authority clusterSSHPreparationAuthorityRead, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) clusterSSHPreparationRead {
	read := c.snapshotRead(authority, lease, baseline, sealed)
	return func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		value, err := read(ctx)
		return value.Expected, value.Settings, err
	}
}

// Keep the source observations available to the peer consumer. A successful
// old configuration response cannot stand in for fresh source link identity.
func (c *clusterSSHSourceBrokerClient) snapshotRead(authority clusterSSHPreparationAuthorityRead, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) clusterSSHPreparationSnapshotRead {
	return c.snapshotReadFor(authority, lease, baseline, sealed, false)
}

// Joining is selected only by the separate membership preparation scope. It
// never changes the default source reader or qualification/recovery contracts.
func (c *clusterSSHSourceBrokerClient) snapshotReadFor(authority clusterSSHPreparationAuthorityRead, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials, joining bool) clusterSSHPreparationSnapshotRead {
	gate := make(chan struct{}, 1)
	retained := ""
	var retainedSources []clusterbootstrap.SourceNetwork
	var retainedStorage clusterSSHStorageSnapshot
	var retainedSites []int64
	var retainedCapacity []clusterSSHPreparationTargetInstanceCPU
	return func(parent context.Context) (clusterSSHPreparationSnapshot, error) {
		started := time.Now()
		fail := func(check string) (clusterSSHPreparationSnapshot, error) {
			reportClusterSSHPrejoinCheck(parent, "source snapshot "+check)
			if emit, ok := parent.Value(clusterSSHPreparationObserverKey{}).(func(string)); ok {
				emit(fmt.Sprintf("Source snapshot status=failed check=%s observed=rejected elapsed_ms=%d", check, time.Since(started).Milliseconds()))
			}
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		if c == nil || c.peers == nil || c.httpClient == nil || authority == nil || parent.Err() != nil {
			return fail("live_reader")
		}
		budget := clusterSSHSourceSnapshotTimeout
		if extendedClusterSSHSourceRead(lease, joining) {
			budget = clusterSSHReadOnlySnapshotTimeout
		}
		ctx, cancel := context.WithTimeout(parent, budget+15*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail("scope_queue")
		}
		before, err := authority(ctx)
		if err != nil || before.Lease != lease || before.Baseline != baseline {
			return fail("authority_before")
		}
		request := clusterSSHSourceBrokerRequest{Joining: joining, Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35,
			Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}
		if budget == clusterSSHReadOnlySnapshotTimeout {
			request.AcceptBefore = request.ExpiresAt
			request.ExpiresAt += int64((budget - clusterSSHSourceSnapshotTimeout) / time.Second)
		}
		if !request.validSource(time.Now()) {
			return fail("request_binding")
		}
		value, err := c.fetch(ctx, request)
		switch {
		case err != nil:
			return fail("authenticated_response")
		case value.Expected.Validate() != nil:
			return fail("valid_expected_configuration")
		case !clusterSSHSourceObservationRE.MatchString(value.Observation):
			return fail("observation_digest")
		case validateClusterSSHSourceNetworks(before, value.Sources) != nil:
			return fail("source_network_binding")
		case !validClusterSSHStorageSnapshot(value.Storage, before.Source):
			return fail("storage_snapshot_binding")
		case !value.Storage.Requirements.KubeVIP.matchesNetworks(value.Sources):
			return fail("vip_network_binding")
		case !value.Storage.Requirements.csiSocketMatchesNetworks(value.Sources):
			return fail("csi_socket_network_binding")
		case retained != "" && retained != value.Observation:
			return fail("retained_observation_digest")
		case retained != "" && !slices.Equal(retainedSources, value.Sources):
			return fail("retained_networks")
		case retained != "" && !reflect.DeepEqual(retainedStorage, value.Storage):
			return fail("retained_storage")
		case retained != "" && !slices.Equal(retainedSites, value.DesiredWorkerSites):
			return fail("retained_worker_sites")
		}
		if _, _, ok := clusterSSHDesiredWorkerWorkloadBudget(value); !ok {
			return fail("workload_budget")
		}
		approved, capacityErr := clusterSSHPreparationCapacityReadiness(before.payload)
		if approved != nil && approved.Version == 5 || lease.isNetworkQualification() && len(before.Cohort.Targets) == 2 {
			if capacityErr != nil || len(value.QualificationCPU) != 2 || value.CapacitySHA256 != "" || len(value.CapacityTargets) != 0 {
				return fail("pair workload sizing")
			}
			for i, n := range value.QualificationCPU {
				policy := value.Storage.Requirements.LonghornInstanceManagerResources
				percent, valid := clusterSSHInstancePercentage(policy.CPUPercentage)
				cost, ok := clusterSSHInstanceCPURequest(uint64(before.Cohort.Targets[i].Report.CPUCount)*1000, n.OverrideMilli, percent)
				if !valid || !ok || n.RequestMilli != cost || cost > policy.CPULimitMilli || !validClusterSSHPreparationDigest(n.ObservationSHA256) || (n.Present && !clusterUUIDRE.MatchString(n.NodeUID)) || (!n.Present && (n.OverrideMilli != 0 || n.NodeUID != "")) {
					return fail("target Longhorn CPU request")
				}
			}
			if approved != nil {
				report, err := pairReport(before.payload)
				if err != nil || !pairConfigurationMatches(before, value, report.Installation) {
					return fail("approved installation configuration")
				}
			}
		} else if capacityErr != nil || (approved == nil && (value.CapacitySHA256 != "" || value.CapacityTargets != nil || value.Expected.TargetCPU != nil)) ||
			(approved != nil && (value.CapacitySHA256 != approved.Checks[2].SHA256 || len(value.CapacityTargets) != len(approved.Targets) ||
				!clusterSSHPreparationCapacityCurrentValid(*approved.Capacity, value.CapacityTargets, joining) ||
				!clusterSSHPreparationCapacityTransition(retainedCapacity, value.CapacityTargets, joining))) {
			return fail("approved_capacity")
		}
		expected, err := buildClusterSSHPreparationExpected(before.Cohort, before.Source, lease, baseline, before.K3sVersion, value.Expected.PodCIDR, value.Expected.ServiceCIDR, before.payload)
		if err != nil || !reflect.DeepEqual(expected, value.Expected) || value.Sources[0].PodCIDR != expected.PodCIDR || value.Sources[0].ServiceCIDR != expected.ServiceCIDR {
			return fail("expected_network_configuration")
		}
		if _, err := clusterbootstrap.NewPreparationConfiguration(expected, value.Settings); err != nil {
			return fail("configuration_settings")
		}
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil {
			return fail("authority_after")
		}
		before.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
		if !reflect.DeepEqual(before, after) {
			return fail("retained_authority")
		}
		retained = value.Observation
		retainedSources = slices.Clone(value.Sources)
		retainedStorage = value.Storage
		retainedSites = slices.Clone(value.DesiredWorkerSites)
		retainedCapacity = slices.Clone(value.CapacityTargets)
		retainedStorage.Requirements.Applications = slices.Clone(value.Storage.Requirements.Applications)
		retainedStorage.Requirements.SiteWorkers = slices.Clone(value.Storage.Requirements.SiteWorkers)
		retainedStorage.Requirements.Volumes = slices.Clone(value.Storage.Requirements.Volumes)
		retainedStorage.Requirements.ReplicaImages = slices.Clone(value.Storage.Requirements.ReplicaImages)
		retainedStorage.Requirements.LonghornPlugin = slices.Clone(value.Storage.Requirements.LonghornPlugin)
		retainedStorage.Requirements.Policy.Classes = slices.Clone(value.Storage.Requirements.Policy.Classes)
		value.started = started
		return value, nil
	}
}

func (c *clusterSSHSourceBrokerClient) fetch(parent context.Context, request clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
	fail := func() (clusterSSHPreparationSnapshot, error) {
		return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
	}
	value, err := c.exchange(parent, request)
	if err != nil || value.Status != "ok" || value.Snapshot == nil || value.Targets != nil || value.Joined {
		return fail()
	}
	return *value.Snapshot, nil
}

// The controller observes the exact joined cohort under the original claim;
// the API independently brackets transport with current SQL/Aegis authority.
// Only typed identities cross this boundary, never arbitrary Kubernetes reads.
func (c *clusterSSHSourceBrokerClient) joinedTargetsRead(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected) (clusterSSHJoinedTargets, error) {
	fail := func() (clusterSSHJoinedTargets, error) {
		return clusterSSHJoinedTargets{}, clusterbootstrap.ErrSessionAuthority
	}
	if c == nil || c.httpClient == nil || authority == nil || !validClusterSSHPreparationLease(claim.Lease) || baseline.Validate() != nil || parent.Err() != nil {
		return fail()
	}
	// Own this Joined exchange's response lifetime without changing the shared
	// client or the legacy snapshot/recovery paths. Child request expiry and
	// failed/blocked Close remain visible after exchange returns.
	transport := c.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	owned, client := *c, *c.httpClient
	client.Transport, owned.httpClient = cleanup, &client
	ctx, cancel := context.WithTimeout(parent, clusterSSHJoinedNodeObservationTimeout)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || before.Lease != claim.Lease || before.Baseline != baseline || before.IdentitySource == nil || before.Recovery != nil {
		return fail()
	}
	before.Cohort.ObservedAt = 0
	digest, err := clusterSSHPreparationDigest(before)
	if err != nil {
		return fail()
	}
	request := clusterSSHSourceBrokerRequest{Joined: true, Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20,
		Lease: claim.Lease, Baseline: baseline, Binding: claim.Sealed.binding, Generation: claim.Sealed.generation, Ciphertext: claim.Sealed.ciphertext}
	value, err := owned.exchange(ctx, request)
	if err != nil || !cleanup.clean() || ctx.Err() != nil || value.Status != "joined_nodes_ok" || !value.Joined || value.Joining || value.Snapshot != nil || value.Targets == nil ||
		value.Targets.AuthoritySHA256 != digest || len(value.Targets.Nodes) != len(before.Cohort.Targets) {
		return fail()
	}
	seen := map[string]bool{}
	for _, target := range before.Cohort.Targets {
		node, ok := value.Targets.Nodes[target.Binding.TargetID]
		if !ok || !clusterUUIDRE.MatchString(node.NodeUID) || node.NodeUID == "00000000-0000-0000-0000-000000000000" || seen[node.NodeUID] ||
			node.Hostname != target.Report.Hostname || node.MachineID != target.Report.MachineID || node.BootID != target.Report.BootID ||
			node.Address != target.Binding.Address || node.K3sVersion != before.K3sVersion || !node.Ready {
			return fail()
		}
		seen[node.NodeUID] = true
	}
	after, err := authority(ctx)
	if err != nil || !cleanup.clean() || ctx.Err() != nil {
		return fail()
	}
	after.Cohort.ObservedAt = 0
	afterDigest, err := clusterSSHPreparationDigest(after)
	if err != nil || afterDigest != digest {
		return fail()
	}
	return *value.Targets, nil
}

// Recovery Node reads reuse controller discovery and encrypted transport, but
// never import broad preparation snapshots into mutating worker decisions.
func (c *clusterSSHSourceBrokerClient) recoveryNodeRead(authority clusterSSHPreparationAuthorityRead,
	claim clusterSSHIdentityRecoveryClaim) func(context.Context, clusterSSHIdentityRecoveryClaim) error {
	return func(parent context.Context, current clusterSSHIdentityRecoveryClaim) error {
		fail := func() error { return clusterbootstrap.ErrPreparationConfig }
		if c == nil || authority == nil || !claim.valid() || !current.valid() || !reflect.DeepEqual(current, claim) || parent.Err() != nil {
			return fail()
		}
		before, err := authority(parent)
		if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(before, claim) {
			return fail()
		}
		request := clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35,
			Lease: claim.Lease, Baseline: claim.Baseline, Binding: claim.Sealed.binding, Generation: claim.Sealed.generation, Ciphertext: claim.Sealed.ciphertext}
		value, err := c.exchange(parent, request)
		if err != nil || value.Status != "recovery_node_ok" || value.Snapshot != nil || value.Targets != nil || value.Joined {
			return fail()
		}
		after, err := authority(parent)
		if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(after, claim) || parent.Err() != nil {
			return fail()
		}
		return nil
	}
}

func (c *clusterSSHSourceBrokerClient) exchange(parent context.Context, request clusterSSHSourceBrokerRequest) (clusterSSHSourceBrokerResponse, error) {
	started := time.Now()
	fail := func(check, observed string) (clusterSSHSourceBrokerResponse, error) {
		reportClusterSSHPrejoinCheck(parent, "source broker "+check+" "+observed)
		if emit, ok := parent.Value(clusterSSHPreparationObserverKey{}).(func(string)); ok {
			emit(fmt.Sprintf("Source broker status=failed check=%s observed=%s elapsed_ms=%d", check, observed, time.Since(started).Milliseconds()))
		}
		return clusterSSHSourceBrokerResponse{}, clusterbootstrap.ErrPreparationConfig
	}
	if c == nil || c.peers == nil || c.httpClient == nil || !request.validSource(time.Now()) || parent.Err() != nil {
		return fail("request_binding", "invalid_or_closed")
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(request.ExpiresAt, 0))
	defer cancel()
	peers, err := c.peers(ctx)
	if err != nil || len(peers) == 0 || len(peers) > 3 {
		return fail("controller_discovery", "invalid_peer_set")
	}
	wire, err := sealClusterSSHSourceBroker(c.requestCipher, request)
	if err != nil {
		return fail("request_seal", "encoding_rejected")
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterSSHSourceBrokerPath, bytes.NewReader(wire))
		if err != nil {
			return fail("http_request", "construction_failed")
		}
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.Header.Set("Accept", clusterSSHSourceBrokerMedia)
		// No transport replay, even after a lost POST. Only an authenticated
		// not_owner response permits trying another current controller peer.
		req.GetBody = nil
		resp, err := client.Do(req)
		if err != nil {
			observed := "transport_error"
			if errors.Is(err, context.DeadlineExceeded) {
				observed = "deadline_exceeded"
			} else if errors.Is(err, context.Canceled) {
				observed = "canceled"
			}
			return fail("controller_exchange", observed)
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, clusterSSHSourceBrokerLimit+1))
		closeErr := resp.Body.Close()
		if closeErr != nil {
			return fail("response_close", "cleanup_failed") // Failed cleanup cannot even advance to another peer.
		}
		var value clusterSSHSourceBrokerResponse
		switch {
		case readErr != nil:
			return fail("response_body", "read_failed")
		case resp.StatusCode != http.StatusOK:
			return fail("http_status_expected_200", fmt.Sprintf("http_%d", resp.StatusCode))
		case resp.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || resp.Header.Get("Content-Encoding") != "":
			return fail("response_headers", "media_or_encoding_rejected")
		case openClusterSSHSourceBroker(c.responseCipher, raw, &value) != nil:
			return fail("response_envelope", "authentication_or_encoding_rejected")
		case value.Version != 55 || value.ID != request.ID || value.Joining != request.Joining || value.Joined != request.Joined:
			return fail("response_binding", "mismatch")
		case ctx.Err() != nil:
			return fail("response_deadline", "scope_closed")
		}
		if value.Status == "not_owner" && value.Snapshot == nil && value.Targets == nil {
			continue
		}
		return value, nil
	}
	return fail("controller_ownership", "no_matching_owner")
}
