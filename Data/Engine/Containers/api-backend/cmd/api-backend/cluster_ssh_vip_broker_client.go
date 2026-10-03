package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/cipher"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type clusterSSHVIPBrokerClient struct {
	startRequest, startResponse, checkRequest, checkResponse cipher.AEAD
	httpClient                                               *http.Client
	peers                                                    func(context.Context) ([]string, error)
}

func newClusterSSHVIPBrokerClientFromEnv() (*clusterSSHVIPBrokerClient, error) {
	base, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	c := &clusterSSHVIPBrokerClient{httpClient: base.httpClient, peers: base.peers}
	c.startRequest, _ = clusterSSHVIPCipher(secret, "start-request")
	c.startResponse, _ = clusterSSHVIPCipher(secret, "start-response")
	c.checkRequest, _ = clusterSSHVIPCipher(secret, "check-request")
	c.checkResponse, _ = clusterSSHVIPCipher(secret, "check-response")
	return c, nil
}

// Production worker entrypoint retains actual unlocked Aegis and original
// credential checks. Caller still owns/refreshes each participating target's
// claim; constructing this read-only bridge does not claim preparation work.
func withClusterSSHPreparationVIP(ctx context.Context, store *postgresOperatorStore, aegis *goAegisService,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials, sources []clusterbootstrap.SourceNetwork,
	consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
	client, err := newClusterSSHVIPBrokerClientFromEnv()
	if err != nil {
		return err
	}
	return client.withOwner(ctx, newClusterSSHPreparationAuthorityRead(store, aegis, lease, baseline, sealed), lease, baseline, sealed, sources, consume)
}
func (c *clusterSSHVIPBrokerClient) post(ctx context.Context, peer, path string, wire []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, "POST", peer+path, bytes.NewReader(wire))
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	request.GetBody = nil // Unknown POST outcomes are never replayed.
	request.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
	request.Header.Set("Accept", clusterSSHSourceBrokerMedia)
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || response.Header.Get("Content-Encoding") != "" {
		response.Body.Close()
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return response, nil
}
func (c *clusterSSHVIPBrokerClient) withOwner(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials, sources []clusterbootstrap.SourceNetwork,
	consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
	if c == nil || c.httpClient == nil || c.peers == nil || authority == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	initial, err := authority(ctx)
	if err != nil || initial.Lease != lease || initial.Baseline != baseline || validateClusterSSHSourceNetworks(initial, sources) != nil || initial.Source.ControlPlaneVIP != initial.Source.EdgeVIP {
		return clusterbootstrap.ErrPreparationConfig
	}
	sources = slices.Clone(sources)
	// Freeze nested authority and peer values before any asynchronous work.
	raw, err := json.Marshal(initial)
	var expected clusterSSHPreparationAuthority
	if err != nil || json.Unmarshal(raw, &expected) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	expected.Cohort.ObservedAt = 0
	frozen, _ := json.Marshal(expected)
	authorityGate := make(chan struct{}, 1)
	checkLocal := func(ctx context.Context) error {
		select {
		case authorityGate <- struct{}{}:
			defer func() { <-authorityGate }()
		case <-ctx.Done():
			return clusterbootstrap.ErrPreparationConfig
		}
		current, err := authority(ctx)
		if err != nil || ctx.Err() != nil || validateClusterSSHInspectionCohort(current.Cohort, current.Source) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		current.Cohort.ObservedAt = 0
		wire, err := json.Marshal(current)
		if err != nil || !bytes.Equal(frozen, wire) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	start := clusterSSHVIPStart{Request: clusterSSHSourceBrokerRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 35, Lease: lease, Baseline: baseline, Binding: sealed.binding, Generation: sealed.generation, Ciphertext: sealed.ciphertext}, Sources: sources}
	if !validClusterSSHVIPStart(start) {
		return clusterbootstrap.ErrPreparationConfig
	}
	wire, err := sealClusterSSHVIP(c.startRequest, clusterSSHVIPStartPath, start, clusterSSHSourceBrokerLimit)
	if err != nil {
		return err
	}
	peers, err := c.peers(ctx)
	if err != nil || len(peers) < 1 || len(peers) > 3 {
		return clusterbootstrap.ErrPreparationConfig
	}
	var response *http.Response
	var ready clusterSSHVIPMessage
	var peer string
	for _, candidate := range peers {
		response, err = c.post(ctx, candidate, clusterSSHVIPStartPath, wire)
		if err != nil {
			return err
		}
		ready, err = readClusterSSHVIPFrame(response.Body, c.startResponse)
		if err != nil || ready.Version != 1 || ready.ID != start.Request.ID || ctx.Err() != nil {
			response.Body.Close()
			return clusterbootstrap.ErrPreparationConfig
		}
		if ready.Status == "not_owner" && ready.Scope == "" && ready.Owner == nil {
			response.Body.Close()
			response = nil
			continue
		}
		peer = candidate
		break
	}
	if response == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	defer response.Body.Close()
	if ready.Status != "ready" || !vipScopeUUID(ready.Scope) || ready.Owner == nil || !validClusterSSHVIPOwner(*ready.Owner, initial, sources) {
		return clusterbootstrap.ErrPreparationConfig
	}
	var finishing atomic.Value
	finishing.Store("")
	done := make(chan struct{})
	var streamErr error
	go func() {
		defer close(done)
		final, err := readClusterSSHVIPFrame(response.Body, c.startResponse)
		finishID := finishing.Load().(string)
		var extra [1]byte
		if err == nil {
			n, tailErr := response.Body.Read(extra[:])
			if n != 0 || tailErr != io.EOF {
				err = clusterbootstrap.ErrPreparationConfig
			}
		}
		if err != nil || finishID == "" || final.Version != 1 || final.ID != finishID || final.Scope != ready.Scope || final.Status != "complete" || final.Owner != nil || ctx.Err() != nil {
			streamErr = clusterbootstrap.ErrPreparationConfig
			cancel()
		}
	}()
	defer func() { cancel(); response.Body.Close(); <-done }()
	control := func(parent context.Context, step string) error {
		fail := func() error { cancel(); return clusterbootstrap.ErrPreparationConfig }
		duration := time.Second
		if step == "inputs" {
			duration = 30 * time.Second
		}
		requestCtx, stop := context.WithTimeout(parent, duration)
		defer stop()
		end := context.AfterFunc(ctx, stop)
		defer end()
		if requestCtx.Err() != nil || checkLocal(requestCtx) != nil {
			return fail()
		}
		request := clusterSSHVIPControl{Version: 1, ID: newClusterUUID(), Start: start.Request.ID, Scope: ready.Scope, Step: step}
		if step == "finish" {
			finishing.Store(request.ID)
		}
		wire, err := sealClusterSSHVIP(c.checkRequest, clusterSSHVIPCheckPath, request, clusterSSHVIPFrameLimit)
		if err != nil {
			return fail()
		}
		reply, err := c.post(requestCtx, peer, clusterSSHVIPCheckPath, wire)
		if err != nil {
			return fail()
		}
		raw, err := io.ReadAll(io.LimitReader(reply.Body, clusterSSHVIPFrameLimit+1))
		reply.Body.Close()
		var result clusterSSHVIPMessage
		if err != nil || openClusterSSHVIP(c.checkResponse, clusterSSHVIPCheckPath, raw, &result, clusterSSHVIPFrameLimit) != nil || result.Version != 1 || result.ID != request.ID || result.Scope != ready.Scope || result.Status != "ok" || result.Owner != nil || checkLocal(requestCtx) != nil || requestCtx.Err() != nil || ctx.Err() != nil {
			return fail()
		}
		return nil
	}
	check := func(ctx context.Context) error { return control(ctx, "authority") }
	err = runClusterSSHPreparationScope(ctx, 500*time.Millisecond, check, func(scope context.Context) error {
		bound := func(input func(context.Context) error) func(context.Context) error {
			boundary := clusterSSHPreparationBoundary(scope, check, input)
			return func(ctx context.Context) error {
				err := boundary(ctx)
				if err != nil {
					cancel()
				}
				return err
			}
		}
		checks := clusterSSHPreparationChecks{Authority: bound(func(context.Context) error { return nil }), Inputs: bound(func(ctx context.Context) error { return control(ctx, "inputs") })}
		return consume(scope, *ready.Owner, checks)
	})
	// Local heartbeat and consumer children are joined before finish. Complete
	// arrives only after controller final inputs/heartbeat/control joins as well.
	if err != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	if control(ctx, "finish") != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	select {
	case <-done:
		if streamErr == nil && ctx.Err() == nil && checkLocal(ctx) == nil {
			return nil
		}
	case <-ctx.Done():
	}
	return clusterbootstrap.ErrPreparationConfig
}

// Compose the actual peer collector with the live bridge. The callback receives
// only copied public identities; full checks reobserve peers and source VIPs.
// refresh must still own every target's original credential/claim independently.
func (c *clusterSSHVIPBrokerClient) withNetworkOwner(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, refresh func(context.Context) error,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials,
	consume func(context.Context, []clusterSSHManagementPeer, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
	if consume == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return withClusterSSHNetworkInputs(parent, authority, source, target, refresh, func(ctx context.Context, peers []clusterSSHManagementPeer, sources []clusterbootstrap.SourceNetwork, peerChecks clusterSSHPreparationChecks) error {
		return c.withOwner(ctx, authority, lease, baseline, sealed, sources, func(ctx context.Context, owner clusterSSHVIPOwner, vipChecks clusterSSHPreparationChecks) error {
			scope, cancel := context.WithCancel(ctx)
			defer cancel()
			combine := func(full bool) func(context.Context) error {
				return func(caller context.Context) error {
					bound, stop := context.WithCancel(caller)
					defer stop()
					end := context.AfterFunc(scope, stop)
					defer end()
					fail := func() error { cancel(); return clusterbootstrap.ErrPreparationConfig }
					if scope.Err() != nil || bound.Err() != nil {
						return fail()
					}
					if full {
						if peerChecks.Inputs(bound) != nil || vipChecks.Inputs(bound) != nil {
							return fail()
						}
					} else if peerChecks.Authority(bound) != nil || vipChecks.Authority(bound) != nil {
						return fail()
					}
					if peerChecks.Authority(bound) != nil || scope.Err() != nil || bound.Err() != nil {
						return fail()
					}
					return nil
				}
			}
			checks := clusterSSHPreparationChecks{Inputs: combine(true), Authority: combine(false)}
			if checks.Inputs(scope) != nil || consume(scope, slices.Clone(peers), owner, checks) != nil || scope.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return checks.Inputs(scope)
		})
	})
}
