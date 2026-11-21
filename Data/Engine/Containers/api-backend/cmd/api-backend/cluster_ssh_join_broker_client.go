package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type clusterSSHJoinBrokerClient struct{ clusterSSHSourceBrokerClient }

func newClusterSSHJoinBrokerClient() (*clusterSSHJoinBrokerClient, error) {
	base, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	base.requestCipher, err = clusterExistingIdentityScopedCipher(secret, "request", "ssh-k3s-join")
	if err != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	base.responseCipher, err = clusterExistingIdentityScopedCipher(secret, "response", "ssh-k3s-join")
	if err != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	base.httpClient.Timeout = 90 * time.Second
	base.httpClient.Transport.(*http.Transport).ResponseHeaderTimeout = 85 * time.Second
	return &clusterSSHJoinBrokerClient{*base}, nil
}

type clusterSSHJoinMaterialConsumer func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error

// Worker keeps unlocked Aegis/current-claim checks independent of controller.
// This capability owns material through synchronous consumption and closes it
// before return. Transport deadline never becomes material/lease lifetime.
func (c *clusterSSHJoinBrokerClient) withMaterial(parent context.Context, expected clusterbootstrap.PreparationExpected,
	source engineidentity.Binding, network clusterbootstrap.SourceNetwork, lease clusterSSHTargetLease, baseline clusterbootstrap.Expected,
	sealed sealedClusterSSHCredentials, authority clusterSSHPreparationAuthorityRead, preparation func(context.Context) error,
	consume clusterSSHJoinMaterialConsumer) error {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if c == nil || c.httpClient == nil || c.peers == nil || authority == nil || preparation == nil || consume == nil || parent.Err() != nil {
		return fail
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	request := clusterSSHJoinBrokerRequest{Request: clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 85,
		Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}, Expected: expected, Source: source, Network: network}
	scope, cancel := context.WithCancel(parent)
	defer cancel()
	checkAuthority, err := newClusterSSHJoinAuthorityCheck(scope, request, authority)
	if err != nil {
		return fail
	}
	gate := make(chan struct{}, 1)
	check := func(ctx context.Context) error {
		ctx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail
		}
		if scope.Err() != nil || ctx.Err() != nil || preparation(ctx) != nil || checkAuthority(ctx) != nil || scope.Err() != nil || ctx.Err() != nil {
			return fail
		}
		return nil
	}
	if check(scope) != nil {
		return fail
	}
	material, err := c.acquire(scope, request, check)
	if err != nil {
		return fail
	}
	defer material.Close()
	readerCtx, cancelReaders := context.WithCancel(scope)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancelReaders}
	defer lifetime.close()
	refresh := func(ctx context.Context) error {
		if lifetime.run(ctx, check) != nil {
			return fail
		}
		return nil
	}
	if check(scope) != nil {
		return fail
	}
	consumeErr := consume(readerCtx, material, refresh)
	// Existing borrowed-reader contract cancels and joins in-flight checks;
	// returning from consumer with a check still active fails acknowledgement.
	if lifetime.close() != nil || consumeErr != nil || check(scope) != nil {
		return fail
	}
	return nil
}

func (c *clusterSSHJoinBrokerClient) acquire(parent context.Context, request clusterSSHJoinBrokerRequest, check func(context.Context) error) (*clusterbootstrap.K3sJoinMaterial, error) {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if c == nil || c.httpClient == nil || c.peers == nil || check == nil || !request.valid(time.Now()) || check(parent) != nil {
		return nil, fail
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(request.Request.ExpiresAt, 0))
	defer cancel()
	peers, err := c.peers(ctx)
	if err != nil || len(peers) < 1 || len(peers) > 3 {
		return nil, fail
	}
	wire, err := sealClusterSSHVIP(c.requestCipher, clusterSSHJoinBrokerPath, request, clusterSSHSourceBrokerLimit)
	defer clear(wire)
	if err != nil {
		return nil, fail
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		ip := strings.TrimSuffix(strings.TrimPrefix(peer, "http://"), ":8090")
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil || !parsed.IsPrivate() || peer != "http://"+net.JoinHostPort(parsed.String(), "8090") || check(ctx) != nil {
			return nil, fail
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterSSHJoinBrokerPath, bytes.NewReader(wire))
		if err != nil {
			return nil, fail
		}
		req.GetBody = nil
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		res, err := client.Do(req)
		if err != nil {
			return nil, fail
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, clusterSSHSourceBrokerLimit+1))
		closeErr := res.Body.Close()
		var response clusterSSHJoinBrokerResponse
		err = openClusterSSHVIP(c.responseCipher, clusterSSHJoinBrokerPath, raw, &response, clusterSSHSourceBrokerLimit)
		clear(raw)
		if readErr != nil || closeErr != nil || err != nil || res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia ||
			res.Header.Get("Content-Encoding") != "" || response.Version != 1 || response.ID != request.Request.ID || ctx.Err() != nil || !request.valid(time.Now()) {
			clear(response.Frame)
			return nil, fail
		}
		if response.Status == "not_owner" && len(response.Frame) == 0 {
			continue
		}
		if response.Status != "observed" || len(response.Frame) == 0 {
			clear(response.Frame)
			return nil, fail
		}
		input := bytes.NewReader(response.Frame)
		material, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, request.Expected, request.Source, input, check)
		clear(response.Frame)
		if err != nil {
			return nil, fail
		}
		if input.Len() != 0 || check(ctx) != nil {
			material.Close()
			return nil, fail
		}
		return material, nil
	}
	return nil, fail
}
