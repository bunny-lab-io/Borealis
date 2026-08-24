package main

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

type durableSessionTestWireGuard struct{}

func (durableSessionTestWireGuard) serverPublicKey() string { return "server-public" }
func (durableSessionTestWireGuard) buildPeerProfile(agentID string, virtualIP string, allowedPorts []int) map[string]any {
	return map[string]any{"agent_id": agentID, "allowed_ips": []string{virtualIP}, "allowed_ports": allowedPorts}
}
func (durableSessionTestWireGuard) upsertPeer(map[string]any) error       { return nil }
func (durableSessionTestWireGuard) removePeer(string, string) error       { return nil }
func (durableSessionTestWireGuard) reconcilePeers([]map[string]any) error { return nil }
func (durableSessionTestWireGuard) checkListenerHealth(int) map[string]any {
	return map[string]any{"healthy": true}
}
func (durableSessionTestWireGuard) checkPeerHealth(string) map[string]any {
	return map[string]any{"healthy": true, "peer_present": true}
}

func postgresVPNSessionTestService(db *sql.DB) *vpnTunnelService {
	auth := &authService{store: &postgresOperatorStore{db: db}}
	service := &vpnTunnelService{
		auth:            auth,
		scriptSigner:    &agentScriptSigner{},
		wg:              durableSessionTestWireGuard{},
		enginePrefix:    netip.MustParsePrefix(defaultWireGuardEngineIP),
		peerPrefix:      netip.MustParsePrefix(defaultWireGuardPeerCIDR),
		allowPorts:      []int{22, 47002},
		persistent:      true,
		ipLeases:        map[string]string{},
		keyLeases:       map[string]vpnClientKeys{},
		sessionsByAgent: map[string]*vpnSession{},
		sessionsByID:    map[string]*vpnSession{},
	}
	service.ready = sync.NewCond(&service.mu)
	return service
}

func TestVPNSessionStorePostgresReplicaConvergence(t *testing.T) {
	databaseURL := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agentID := "ci-durable-vpn-session"
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.device_vpn_sessions WHERE agent_id=$1`, agentID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.device_vpn_ip_leases WHERE agent_id=$1`, agentID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM engine.device_vpn_key_leases WHERE agent_id=$1`, agentID)
	}
	cleanup()
	defer cleanup()

	first := postgresVPNSessionTestService(db)
	second := postgresVPNSessionTestService(db)
	type connectResult struct {
		payload map[string]any
		err     error
	}
	results := make(chan connectResult, 2)
	for _, service := range []*vpnTunnelService{first, second} {
		go func(runtime *vpnTunnelService) {
			payload, connectErr := runtime.connect(ctx, vpnConnectRequest{AgentID: agentID, OperatorID: "ci-admin", RequiredPorts: []int{5900}})
			results <- connectResult{payload: payload, err: connectErr}
		}(service)
	}
	left := <-results
	right := <-results
	if left.err != nil || right.err != nil {
		t.Fatalf("concurrent connect failed: left=%v right=%v", left.err, right.err)
	}
	if cleanText(left.payload["tunnel_id"]) == "" || cleanText(left.payload["tunnel_id"]) != cleanText(right.payload["tunnel_id"]) {
		t.Fatalf("replicas diverged on tunnel identity: left=%v right=%v", left.payload["tunnel_id"], right.payload["tunnel_id"])
	}
	if cleanText(left.payload["virtual_ip"]) != cleanText(right.payload["virtual_ip"]) {
		t.Fatalf("replicas diverged on virtual IP: left=%v right=%v", left.payload["virtual_ip"], right.payload["virtual_ip"])
	}

	tunnelID := cleanText(left.payload["tunnel_id"])
	ready := second.recordAgentReady(ctx, agentID, tunnelID, []int{22, 5900}, "ci_ready", "running", cleanText(left.payload["virtual_ip"]))
	if ready == nil {
		t.Fatal("second replica could not record readiness")
	}
	loaded, err := first.loadPersistentSession(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastAgentReadyTunnelID != tunnelID || loaded.LastAgentReadyReason != "ci_ready" {
		t.Fatalf("first replica did not observe readiness: %#v", loaded)
	}

	leftCopy, err := first.loadPersistentSession(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	rightCopy, err := second.loadPersistentSession(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	leftCopy.LastAgentReadyReason = "left"
	rightCopy.LastAgentReadyReason = "right"
	if err := first.savePersistentSession(ctx, leftCopy, leftCopy.Generation); err != nil {
		t.Fatal(err)
	}
	if err := second.savePersistentSession(ctx, rightCopy, rightCopy.Generation); !errors.Is(err, errVPNSessionGenerationConflict) {
		t.Fatalf("stale replica update did not conflict: %v", err)
	}

	if err := first.confirmPersistentPeerHealth(ctx, []string{agentID}); err != nil {
		t.Fatal(err)
	}
	confirmed, err := second.loadPersistentSession(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.LastTransportConfirmedAt.IsZero() {
		t.Fatal("edge-owner confirmation did not converge")
	}
}
