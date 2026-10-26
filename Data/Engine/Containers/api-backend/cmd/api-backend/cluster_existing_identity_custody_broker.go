package main

import (
	"bytes"
	"context"
	"crypto/cipher"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const clusterExistingIdentityCustodyPath = "/internal/cluster/existing-identity-custody"

type clusterExistingIdentityCustodyRequest struct {
	Version   int
	ID        string
	ExpiresAt int64
	Lease     clusterSSHTargetLease
	Process   clusterExistingIdentitySourceProcess
}

func (r clusterExistingIdentityCustodyRequest) valid(now time.Time) bool {
	l := r.Lease
	for _, id := range []string{r.ID, l.OperationID, l.TargetID, l.Holder, r.Process.PodUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return r.Version == 1 && r.ExpiresAt > now.Unix() && r.ExpiresAt <= now.Unix()+25 &&
		l.Generation > 0 && l.Generation < 1<<53 && l.OperationAttempt == 1 && l.OperationKind == clusterExistingIdentityKind &&
		l.OperationStep == clusterExistingIdentityConfirmed && l.Step == clusterExistingIdentityInspect && len(l.ControllerHolder) > 0 && len(l.ControllerHolder) <= 1024 &&
		len(r.Process.PodName) <= 253 && clusterSSHStorageNameRE.MatchString(r.Process.PodName) && len(validateClusterNodeName("node", r.Process.NodeName)) == 0
}

type clusterExistingIdentityCustodyResponse struct {
	Version               int
	ID, Status, Authority string
	Proof                 *clusterExistingIdentitySourceCustody
}

// Only the original controller can bind API process placement to the exact
// current read-only claim. No credential or identity bytes cross this broker.
func (c *clusterController) observeClusterExistingIdentityCustody(ctx context.Context, r clusterExistingIdentityCustodyRequest, getJSON func(context.Context, string, any) error) (clusterExistingIdentitySourceCustody, string, error) {
	fail := func() (clusterExistingIdentitySourceCustody, string, error) {
		return clusterExistingIdentitySourceCustody{}, "", errClusterConflict
	}
	if c == nil || c.store == nil || r.Lease.ControllerHolder != c.holder || !r.valid(time.Now()) {
		return fail()
	}
	w, err := readClusterExistingIdentityWork(ctx, c.store.db, r.Lease.OperationID, r.Lease.TargetID)
	if err != nil || !w.claim.valid() || w.claim.Lease != r.Lease || !r.Process.valid(w.claim.Plan) {
		return fail()
	}
	before, err := c.store.loadClusterExistingIdentityInspection(ctx, w.claim)
	if err != nil {
		return fail()
	}
	proof, err := readClusterExistingIdentitySourceCustody(ctx, getJSON, w.claim.Plan, r.Process)
	if err != nil {
		return fail()
	}
	after, err := c.store.loadClusterExistingIdentityInspection(ctx, w.claim)
	if err != nil || before != after || ctx.Err() != nil {
		return fail()
	}
	return proof, before, nil
}

type clusterExistingIdentityCustodyBroker struct {
	holder                        string
	requestCipher, responseCipher cipher.AEAD
	read                          func(context.Context, clusterExistingIdentityCustodyRequest) (clusterExistingIdentitySourceCustody, string, error)
	mu                            sync.Mutex
	seen                          map[string]int64
	active                        int
}

func newClusterExistingIdentityCustodyBroker(c *clusterController, runner *kubernetesClusterStepRunner, secret string) *clusterExistingIdentityCustodyBroker {
	b := &clusterExistingIdentityCustodyBroker{seen: make(map[string]int64)}
	b.requestCipher, _ = clusterExistingIdentityScopedCipher(secret, "request", "existing-identity-custody")
	b.responseCipher, _ = clusterExistingIdentityScopedCipher(secret, "response", "existing-identity-custody")
	if c != nil {
		b.holder = c.holder
	}
	b.read = func(ctx context.Context, r clusterExistingIdentityCustodyRequest) (clusterExistingIdentitySourceCustody, string, error) {
		if !clusterControllerEligible() || c == nil || runner == nil || runner.kube == nil || runner.controllerHolder != c.holder {
			return clusterExistingIdentitySourceCustody{}, "", errClusterConflict
		}
		return c.observeClusterExistingIdentityCustody(ctx, r, runner.kube.getJSON)
	}
	return b
}

func (b *clusterExistingIdentityCustodyBroker) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(code int) { http.Error(w, "identity custody unavailable", code) }
	if r.Method != http.MethodPost || r.URL.Path != clusterExistingIdentityCustodyPath || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || r.Header.Get("Content-Encoding") != "" {
		fail(400)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	wire, err := io.ReadAll(io.LimitReader(r.Body, clusterExistingIdentityBrokerLimit+1))
	var request clusterExistingIdentityCustodyRequest
	if err != nil || b == nil || openClusterExistingIdentityScopedBroker(b.requestCipher, wire, &request, clusterExistingIdentityCustodyPath) != nil || !request.valid(time.Now()) {
		fail(401)
		return
	}
	reply := func(status, authority string, proof *clusterExistingIdentitySourceCustody) {
		wire, err := sealClusterExistingIdentityScopedBroker(b.responseCipher, clusterExistingIdentityCustodyResponse{Version: 1, ID: request.ID, Status: status, Authority: authority, Proof: proof}, clusterExistingIdentityCustodyPath)
		if err != nil {
			fail(503)
			return
		}
		w.Header().Set("Content-Type", clusterSSHSourceBrokerMedia)
		_, _ = w.Write(wire)
	}
	if request.Lease.ControllerHolder != b.holder {
		reply("not_owner", "", nil)
		return
	}
	b.mu.Lock()
	for id, expiry := range b.seen {
		if expiry <= time.Now().Unix() {
			delete(b.seen, id)
		}
	}
	_, replay := b.seen[request.ID]
	accepted := b.seen != nil && !replay && len(b.seen) < 128 && b.active < 2 && b.read != nil
	if accepted {
		b.seen[request.ID] = request.ExpiresAt
		b.active++
	}
	b.mu.Unlock()
	if !accepted {
		reply("unavailable", "", nil)
		return
	}
	defer func() { b.mu.Lock(); b.active--; b.mu.Unlock() }()
	ctx, cancel := context.WithDeadline(r.Context(), time.Unix(request.ExpiresAt, 0))
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	proof, authority, err := b.read(ctx, request)
	if err != nil || ctx.Err() != nil || proof.Process != request.Process || !validClusterSSHPreparationDigest(authority) {
		reply("unavailable", "", nil)
		return
	}
	reply("observed", authority, &proof)
}

type clusterExistingIdentityCustodyClient struct {
	clusterExistingIdentityBrokerClient
}

func newClusterExistingIdentityCustodyClient() (*clusterExistingIdentityCustodyClient, error) {
	transport, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		return nil, errClusterUnavailable
	}
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	req, err := clusterExistingIdentityScopedCipher(secret, "request", "existing-identity-custody")
	if err != nil {
		return nil, errClusterUnavailable
	}
	res, err := clusterExistingIdentityScopedCipher(secret, "response", "existing-identity-custody")
	if err != nil {
		return nil, errClusterUnavailable
	}
	return &clusterExistingIdentityCustodyClient{clusterExistingIdentityBrokerClient{requestCipher: req, responseCipher: res, httpClient: transport.httpClient, peers: transport.peers}}, nil
}

func (c *clusterExistingIdentityCustodyClient) observe(ctx context.Context, claim clusterExistingIdentityInspectionClaim, process clusterExistingIdentitySourceProcess, authority func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error) {
	fail := func() (clusterExistingIdentitySourceCustody, error) {
		return clusterExistingIdentitySourceCustody{}, errClusterConflict
	}
	if c == nil || c.httpClient == nil || c.peers == nil || !claim.valid() || !process.valid(claim.Plan) || authority == nil || ctx.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !validClusterSSHPreparationDigest(before) {
		return fail()
	}
	r := clusterExistingIdentityCustodyRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Lease: claim.Lease, Process: process}
	peers, err := c.peers(ctx)
	if err != nil || len(peers) == 0 || len(peers) > 3 {
		return fail()
	}
	wire, err := sealClusterExistingIdentityScopedBroker(c.requestCipher, r, clusterExistingIdentityCustodyPath)
	if err != nil {
		return fail()
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+clusterExistingIdentityCustodyPath, bytes.NewReader(wire))
		if err != nil {
			return fail()
		}
		req.Header.Set("Content-Type", clusterSSHSourceBrokerMedia)
		req.GetBody = nil
		res, err := client.Do(req)
		if err != nil {
			return fail()
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, clusterExistingIdentityBrokerLimit+1))
		_ = res.Body.Close()
		var result clusterExistingIdentityCustodyResponse
		if err != nil || res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != clusterSSHSourceBrokerMedia || res.Header.Get("Content-Encoding") != "" ||
			openClusterExistingIdentityScopedBroker(c.responseCipher, raw, &result, clusterExistingIdentityCustodyPath) != nil || result.Version != 1 || result.ID != r.ID || ctx.Err() != nil {
			return fail()
		}
		if result.Status == "not_owner" && result.Proof == nil && result.Authority == "" {
			continue
		}
		if result.Status != "observed" || result.Proof == nil || !result.Proof.valid(claim.Plan) || result.Proof.Process != process || result.Authority != before {
			return fail()
		}
		after, err := authority(ctx)
		if err != nil || before != after || ctx.Err() != nil {
			return fail()
		}
		return *result.Proof, nil
	}
	return fail()
}
