package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
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
		httpClient: &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			ResponseHeaderTimeout: 32 * time.Second,
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
	return func(parent context.Context) (clusterSSHPreparationSnapshot, error) {
		started := time.Now()
		fail := func() (clusterSSHPreparationSnapshot, error) {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		if c == nil || c.peers == nil || c.httpClient == nil || authority == nil || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 45*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail()
		}
		before, err := authority(ctx)
		if err != nil || before.Lease != lease || before.Baseline != baseline {
			return fail()
		}
		request := clusterSSHSourceBrokerRequest{Joining: joining, Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35,
			Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}
		if !request.valid(time.Now()) {
			return fail()
		}
		value, err := c.fetch(ctx, request)
		if err != nil || value.Expected.Validate() != nil || !clusterSSHSourceObservationRE.MatchString(value.Observation) ||
			validateClusterSSHSourceNetworks(before, value.Sources) != nil ||
			!validClusterSSHStorageSnapshot(value.Storage, before.Source) || !value.Storage.Requirements.KubeVIP.matchesNetworks(value.Sources) || !value.Storage.Requirements.csiSocketMatchesNetworks(value.Sources) ||
			(retained != "" && (retained != value.Observation || !slices.Equal(retainedSources, value.Sources) || !reflect.DeepEqual(retainedStorage, value.Storage) || !slices.Equal(retainedSites, value.DesiredWorkerSites))) {
			return fail()
		}
		if _, _, ok := clusterSSHDesiredWorkerWorkloadBudget(value); !ok {
			return fail()
		}
		expected, err := buildClusterSSHPreparationExpected(before.Cohort, before.Source, lease, baseline, before.K3sVersion, value.Expected.PodCIDR, value.Expected.ServiceCIDR, before.payload)
		if err != nil || !reflect.DeepEqual(expected, value.Expected) || value.Sources[0].PodCIDR != expected.PodCIDR || value.Sources[0].ServiceCIDR != expected.ServiceCIDR {
			return fail()
		}
		if _, err := clusterbootstrap.NewPreparationConfiguration(expected, value.Settings); err != nil {
			return fail()
		}
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil {
			return fail()
		}
		before.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
		if !reflect.DeepEqual(before, after) {
			return fail()
		}
		retained = value.Observation
		retainedSources = slices.Clone(value.Sources)
		retainedStorage = value.Storage
		retainedSites = slices.Clone(value.DesiredWorkerSites)
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
	fail := func() (clusterSSHSourceBrokerResponse, error) {
		return clusterSSHSourceBrokerResponse{}, clusterbootstrap.ErrPreparationConfig
	}
	if c == nil || c.peers == nil || c.httpClient == nil || !request.validSource(time.Now()) || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(request.ExpiresAt, 0))
	defer cancel()
	peers, err := c.peers(ctx)
	if err != nil || len(peers) == 0 || len(peers) > 3 {
		return fail()
	}
	wire, err := sealClusterSSHSourceBroker(c.requestCipher, request)
	if err != nil {
		return fail()
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterSSHSourceBrokerPath, bytes.NewReader(wire))
		if err != nil {
			return fail()
		}
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.Header.Set("Accept", clusterSSHSourceBrokerMedia)
		// No transport replay, even after a lost POST. Only an authenticated
		// not_owner response permits trying another current controller peer.
		req.GetBody = nil
		resp, err := client.Do(req)
		if err != nil {
			return fail()
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, clusterSSHSourceBrokerLimit+1))
		closeErr := resp.Body.Close()
		if request.Joined && closeErr != nil {
			return fail() // Failed cleanup cannot even advance to another peer.
		}
		var value clusterSSHSourceBrokerResponse
		if readErr != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia ||
			resp.Header.Get("Content-Encoding") != "" || openClusterSSHSourceBroker(c.responseCipher, raw, &value) != nil ||
			value.Version != 54 || value.ID != request.ID || value.Joining != request.Joining || value.Joined != request.Joined || ctx.Err() != nil {
			return fail()
		}
		if value.Status == "not_owner" && value.Snapshot == nil && value.Targets == nil {
			continue
		}
		return value, nil
	}
	return fail()
}
