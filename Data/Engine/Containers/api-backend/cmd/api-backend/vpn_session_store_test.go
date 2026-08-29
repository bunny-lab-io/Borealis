package main

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestVPNSessionStoreRoundTripFields(t *testing.T) {
	now := time.Date(2026, 8, 24, 20, 0, 0, 123, time.UTC)
	row := vpnSessionRow{
		agentID:                        "agent-1",
		tunnelID:                       "tunnel-1",
		virtualIP:                      "10.255.0.2/32",
		endpointHost:                   "192.168.3.249",
		allowedPortsJSON:               `[22,3389,5900]`,
		operatorsJSON:                  `["operator-b","operator-a"]`,
		state:                          "active",
		createdAt:                      now.Format(time.RFC3339Nano),
		expiresAt:                      now.Add(5 * time.Minute).Format(time.RFC3339Nano),
		lastActivityAt:                 now.Add(time.Second).Format(time.RFC3339Nano),
		lastTransportProbeAt:           sql.NullString{String: now.Add(2 * time.Second).Format(time.RFC3339Nano), Valid: true},
		lastTransportConfirmedAt:       sql.NullString{String: now.Add(3 * time.Second).Format(time.RFC3339Nano), Valid: true},
		lastAgentReadyAt:               sql.NullString{String: now.Add(4 * time.Second).Format(time.RFC3339Nano), Valid: true},
		lastAgentReadyTunnelID:         "tunnel-1",
		lastAgentReadyAllowedPortsJSON: `[22,5900]`,
		lastAgentReadyReason:           "agent_callback",
		lastAgentReadyServiceState:     "running",
		generation:                     7,
		updatedAt:                      now.Add(5 * time.Second).Format(time.RFC3339Nano),
		clientPrivateKey:               "private",
		clientPublicKey:                "public",
	}

	session, err := decodeVPNSessionRow(row)
	if err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.AgentID != row.agentID || session.TunnelID != row.tunnelID || session.Generation != 7 {
		t.Fatalf("identity mismatch: %#v", session)
	}
	if !reflect.DeepEqual(session.AllowedPorts, []int{22, 3389, 5900}) {
		t.Fatalf("allowed ports mismatch: %#v", session.AllowedPorts)
	}
	if !reflect.DeepEqual(session.LastAgentReadyAllowedPorts, []int{22, 5900}) {
		t.Fatalf("ready ports mismatch: %#v", session.LastAgentReadyAllowedPorts)
	}
	if len(session.Operators) != 2 {
		t.Fatalf("operator count mismatch: %d", len(session.Operators))
	}
	if !session.LastTransportConfirmedAt.Equal(now.Add(3 * time.Second)) {
		t.Fatalf("transport confirmation mismatch: %s", session.LastTransportConfirmedAt)
	}
}

func TestVPNSessionStoreEncodingIsStable(t *testing.T) {
	session := &vpnSession{
		AllowedPorts:               []int{5900, 22, 5900},
		LastAgentReadyAllowedPorts: []int{5900, 22},
		Operators: map[string]struct{}{
			"operator-z": {},
			"operator-a": {},
		},
	}

	allowed, operators, ready, err := encodeVPNSessionLists(session)
	if err != nil {
		t.Fatalf("encode session: %v", err)
	}
	if string(allowed) != `[22,5900]` {
		t.Fatalf("allowed ports not normalized: %s", allowed)
	}
	if string(operators) != `["operator-a","operator-z"]` {
		t.Fatalf("operators not sorted: %s", operators)
	}
	if string(ready) != `[22,5900]` {
		t.Fatalf("ready ports not normalized: %s", ready)
	}
}

func TestVPNSessionStoreRejectsMalformedSharedState(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := decodeVPNSessionRow(vpnSessionRow{
		agentID:                        "agent-1",
		tunnelID:                       "tunnel-1",
		virtualIP:                      "10.255.0.2/32",
		allowedPortsJSON:               `not-json`,
		operatorsJSON:                  `[]`,
		state:                          "active",
		createdAt:                      now,
		expiresAt:                      now,
		lastActivityAt:                 now,
		lastAgentReadyAllowedPortsJSON: `[]`,
	})
	if err == nil {
		t.Fatal("malformed shared state accepted")
	}
}

func TestCloneVPNSessionDoesNotShareMutableCollections(t *testing.T) {
	original := &vpnSession{
		AllowedPorts: []int{22},
		Operators:    map[string]struct{}{"operator-a": {}},
		Token:        map[string]any{"tunnel_id": "one"},
	}
	clone := cloneVPNSession(original)
	clone.AllowedPorts[0] = 5900
	clone.Operators["operator-b"] = struct{}{}
	clone.Token["tunnel_id"] = "two"

	if original.AllowedPorts[0] != 22 || len(original.Operators) != 1 || cleanText(original.Token["tunnel_id"]) != "one" {
		t.Fatalf("clone mutated original: %#v", original)
	}
}

func TestSharedOwnerTransportConfirmationIsBounded(t *testing.T) {
	t.Setenv("BOREALIS_CLUSTER_EDGE_VIP", "192.168.3.249")
	service := &vpnTunnelService{persistent: true}
	now := time.Now().UTC()
	session := &vpnSession{LastTransportConfirmedAt: now.Add(-10 * time.Second)}
	if !service.sharedOwnerTransportConfirmed(session, now) {
		t.Fatal("fresh edge-owner confirmation rejected")
	}
	session.LastTransportConfirmedAt = now.Add(-sharedVPNConfirmationMaxAge - time.Second)
	if service.sharedOwnerTransportConfirmed(session, now) {
		t.Fatal("stale edge-owner confirmation accepted")
	}
}
