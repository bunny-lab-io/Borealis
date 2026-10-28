package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const clusterExistingIdentityBrokerPath = "/internal/cluster/existing-identity-plan"
const clusterExistingIdentityBrokerLimit = 64 << 10

// API replicas attest Admin identity; controller alone observes native Nodes
// and commits reservation. This is not a preparation or target-worker lease.
type clusterExistingIdentityBrokerRequest struct {
	Version    int                              `json:"version"`
	ID         string                           `json:"id"`
	ExpiresAt  int64                            `json:"expires_at"`
	Controller string                           `json:"controller"`
	Input      clusterExistingIdentityPlanInput `json:"input"`
	TargetID   string                           `json:"target_id"`
	Actor      string                           `json:"actor"`
}

func (r clusterExistingIdentityBrokerRequest) valid(now time.Time) bool {
	raw, err := json.Marshal(r.Input)
	_, inputErr := parseClusterExistingIdentityPlanInput(raw)
	return err == nil && inputErr == nil && r.Version == 1 && clusterUUIDRE.MatchString(r.ID) &&
		r.ID != "00000000-0000-0000-0000-000000000000" && clusterUUIDRE.MatchString(r.TargetID) && r.TargetID != r.Input.SourceID &&
		r.TargetID != "00000000-0000-0000-0000-000000000000" && r.ExpiresAt > now.Unix() && r.ExpiresAt <= now.Unix()+25 &&
		len(r.Controller) > 0 && len(r.Controller) <= 1024 && utf8.ValidString(r.Controller) && validClusterExistingIdentityActor(r.Actor)
}

func (r clusterExistingIdentityBrokerRequest) matches(p clusterExistingIdentityPlan) bool {
	return p.valid() && p.OperationID == r.Input.RequestID && p.MaintenanceID == r.Input.MaintenanceID && p.SourceID == r.Input.SourceID && p.TargetID == r.TargetID && p.Controller == r.Controller
}

type clusterExistingIdentityBrokerResponse struct {
	Version int                          `json:"version"`
	ID      string                       `json:"id"`
	Status  string                       `json:"status"`
	Plan    *clusterExistingIdentityPlan `json:"plan"`
}

// Separate key derivation and AAD prevent substitution with observation brokers.
func clusterExistingIdentityBrokerCipher(secret, direction string) (cipher.AEAD, error) {
	return clusterExistingIdentityScopedCipher(secret, direction, "existing-identity-plan")
}

func clusterExistingIdentityScopedCipher(secret, direction, scope string) (cipher.AEAD, error) {
	if len(secret) < 32 || len(secret) > 4096 || (direction != "request" && direction != "response") {
		return nil, errClusterUnavailable
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("borealis/cluster/" + scope + "/v1/" + direction))
	key := mac.Sum(nil)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errClusterUnavailable
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func sealClusterExistingIdentityBroker(aead cipher.AEAD, value any) ([]byte, error) {
	return sealClusterExistingIdentityScopedBroker(aead, value, clusterExistingIdentityBrokerPath)
}

func sealClusterExistingIdentityScopedBroker(aead cipher.AEAD, value any, path string) ([]byte, error) {
	if aead == nil {
		return nil, errClusterUnavailable
	}
	raw, err := json.Marshal(value)
	defer clear(raw)
	if err != nil || len(raw)+aead.Overhead() > clusterExistingIdentityBrokerLimit {
		return nil, errClusterUnavailable
	}
	return aead.Seal(nil, nil, raw, []byte(path)), nil
}

func openClusterExistingIdentityBroker(aead cipher.AEAD, wire []byte, out any) error {
	return openClusterExistingIdentityScopedBroker(aead, wire, out, clusterExistingIdentityBrokerPath)
}

func openClusterExistingIdentityScopedBroker(aead cipher.AEAD, wire []byte, out any, path string) error {
	if aead == nil || len(wire) > clusterExistingIdentityBrokerLimit {
		return errClusterUnavailable
	}
	raw, err := aead.Open(nil, nil, wire, []byte(path))
	defer clear(raw)
	if err != nil || !utf8.Valid(raw) || json.Unmarshal(raw, out) != nil {
		return errClusterUnavailable
	}
	canonical, err := json.Marshal(out)
	defer clear(canonical)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return errClusterUnavailable
	}
	return nil
}

type clusterExistingIdentityBroker struct {
	holder                        string
	requestCipher, responseCipher cipher.AEAD
	reserve                       func(context.Context, clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error)
	mu                            sync.Mutex
	seen                          map[string]int64
	active                        int
}

func newClusterExistingIdentityBroker(c *clusterController, runner *kubernetesClusterStepRunner, secret string) *clusterExistingIdentityBroker {
	b := &clusterExistingIdentityBroker{seen: make(map[string]int64)}
	b.requestCipher, _ = clusterExistingIdentityBrokerCipher(secret, "request")
	b.responseCipher, _ = clusterExistingIdentityBrokerCipher(secret, "response")
	if c != nil {
		b.holder = c.holder
	}
	b.reserve = func(ctx context.Context, r clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error) {
		if !clusterControllerEligible() || c == nil || runner == nil || runner.kube == nil || runner.controllerHolder != c.holder || r.Controller != c.holder {
			return clusterExistingIdentityPlan{}, errClusterConflict
		}
		return c.reserveClusterExistingIdentity(ctx, r.Input.RequestID, r.Input.MaintenanceID, r.Input.SourceID, r.TargetID, r.Actor, runner.kube.getJSON)
	}
	return b
}

func (b *clusterExistingIdentityBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int) { http.Error(w, "identity plan unavailable", status) }
	if r.Method != http.MethodPost || r.URL.Path != clusterExistingIdentityBrokerPath || r.URL.RawQuery != "" || r.URL.ForceQuery ||
		r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(http.StatusBadRequest)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterExistingIdentityBrokerLimit+1))
	var request clusterExistingIdentityBrokerRequest
	if err != nil || b == nil || openClusterExistingIdentityBroker(b.requestCipher, wire, &request) != nil || !request.valid(time.Now()) {
		fail(http.StatusUnauthorized)
		return
	}
	reply := func(status string, plan *clusterExistingIdentityPlan) {
		wire, err := sealClusterExistingIdentityBroker(b.responseCipher, clusterExistingIdentityBrokerResponse{Version: 1, ID: request.ID, Status: status, Plan: plan})
		if err != nil {
			fail(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
		_, _ = w.Write(wire)
	}
	if request.Controller != b.holder {
		reply("not_owner", nil)
		return
	}
	b.mu.Lock()
	now := time.Now().Unix()
	for id, expiry := range b.seen {
		if expiry <= now {
			delete(b.seen, id)
		}
	}
	_, replay := b.seen[request.ID]
	accepted := b.seen != nil && !replay && len(b.seen) < 128 && b.active < 2 && b.reserve != nil
	if accepted {
		b.seen[request.ID] = request.ExpiresAt
		b.active++
	}
	b.mu.Unlock()
	if !accepted {
		reply("unavailable", nil)
		return
	}
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(request.ExpiresAt, 0))
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	plan, err := b.reserve(ctx, request)
	if err != nil || ctx.Err() != nil || !request.matches(plan) {
		reply("unavailable", nil)
		return
	}
	reply("reserved", &plan)
}

type clusterExistingIdentityBrokerClient struct {
	requestCipher, responseCipher cipher.AEAD
	httpClient                    *http.Client
	peers                         func(context.Context) ([]string, error)
}

func newClusterExistingIdentityBrokerClient() (*clusterExistingIdentityBrokerClient, error) {
	// Reuse only fixed private discovery and bounded HTTP transport, not its
	// observation message, key derivation or lease authorization contract.
	transport, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, errClusterUnavailable
	}
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	request, err := clusterExistingIdentityBrokerCipher(secret, "request")
	if err != nil {
		return nil, errClusterUnavailable
	}
	response, err := clusterExistingIdentityBrokerCipher(secret, "response")
	if err != nil {
		return nil, errClusterUnavailable
	}
	return &clusterExistingIdentityBrokerClient{requestCipher: request, responseCipher: response, httpClient: transport.httpClient, peers: transport.peers}, nil
}

func (c *clusterExistingIdentityBrokerClient) reserve(parent context.Context, request clusterExistingIdentityBrokerRequest) (clusterExistingIdentityPlan, error) {
	fail := func() (clusterExistingIdentityPlan, error) {
		return clusterExistingIdentityPlan{}, errClusterUnavailable
	}
	if c == nil || c.peers == nil || c.httpClient == nil || !request.valid(time.Now()) || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(request.ExpiresAt, 0))
	defer cancel()
	peers, err := c.peers(ctx)
	if err != nil || len(peers) < 1 || len(peers) > 3 {
		return fail()
	}
	wire, err := sealClusterExistingIdentityBroker(c.requestCipher, request)
	if err != nil {
		return fail()
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterExistingIdentityBrokerPath, bytes.NewReader(wire))
		if err != nil {
			return fail()
		}
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.GetBody = nil // A lost POST is never replayed to another controller.
		resp, err := client.Do(req)
		if err != nil {
			return fail()
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, clusterExistingIdentityBrokerLimit+1))
		_ = resp.Body.Close()
		var value clusterExistingIdentityBrokerResponse
		if readErr != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || resp.Header.Get("Content-Encoding") != "" ||
			openClusterExistingIdentityBroker(c.responseCipher, raw, &value) != nil || value.Version != 1 || value.ID != request.ID || ctx.Err() != nil {
			return fail()
		}
		if value.Status == "not_owner" && value.Plan == nil {
			continue
		}
		if value.Status != "reserved" || value.Plan == nil || !request.matches(*value.Plan) {
			return fail()
		}
		return *value.Plan, nil
	}
	return fail()
}
