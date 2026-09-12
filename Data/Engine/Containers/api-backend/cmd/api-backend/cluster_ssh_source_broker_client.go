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
	gate := make(chan struct{}, 1)
	retained := ""
	return func(parent context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		fail := func() (clusterbootstrap.PreparationExpected, map[string]string, error) {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
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
		request := clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35,
			Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}
		if !request.valid(time.Now()) {
			return fail()
		}
		value, err := c.fetch(ctx, request)
		if err != nil || value.Expected.Validate() != nil || !clusterSSHSourceObservationRE.MatchString(value.Observation) ||
			(retained != "" && retained != value.Observation) {
			return fail()
		}
		expected, err := buildClusterSSHPreparationExpected(before.Cohort, before.Source, lease, baseline, before.K3sVersion, value.Expected.PodCIDR, value.Expected.ServiceCIDR)
		if err != nil || !reflect.DeepEqual(expected, value.Expected) {
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
		return expected, value.Settings, nil
	}
}

func (c *clusterSSHSourceBrokerClient) fetch(parent context.Context, request clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
	fail := func() (clusterSSHPreparationSnapshot, error) {
		return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
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
		_ = resp.Body.Close()
		var value clusterSSHSourceBrokerResponse
		if readErr != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia ||
			resp.Header.Get("Content-Encoding") != "" || openClusterSSHSourceBroker(c.responseCipher, raw, &value) != nil ||
			value.Version != 1 || value.ID != request.ID || ctx.Err() != nil {
			return fail()
		}
		if value.Status == "not_owner" && value.Snapshot == nil {
			continue
		}
		if value.Status != "ok" || value.Snapshot == nil {
			return fail()
		}
		return *value.Snapshot, nil
	}
	return fail()
}
