package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeShellTunnelConnector struct {
	agentID string
	payload map[string]any
	status  int
	err     map[string]any
}

func (c *fakeShellTunnelConnector) connectTunnel(_ context.Context, _ *http.Request, agentID string) (map[string]any, int, map[string]any) {
	c.agentID = agentID
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	if c.err != nil {
		return nil, status, c.err
	}
	if c.payload == nil {
		c.payload = map[string]any{"agent_id": agentID, "tunnel_id": "tunnel-1"}
	}
	return copyMap(c.payload), status, nil
}

func TestRemoteShellEstablishIssuesSessionAndConnectsTunnel(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", "47099")
	auth, store := testAuthServiceWithStore(operatorProfile{ID: 7, Username: "operator", Role: "Admin"})
	siteID := int64(3)
	store.remoteOpsResult = remoteOpsSessionResult{
		Device: remoteOpsSessionDevice{
			GUID:     "00000000-0000-4000-8000-000000000123",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			SiteID:   &siteID,
			SiteName: "Bunny Lab",
		},
		Route: &agentWorkerRoute{
			WorkerGUID:      "worker-1",
			SiteID:          siteID,
			RoutePathPrefix: "/_borealis/site-workers/worker-1",
			Generation:      4,
		},
	}
	connector := &fakeShellTunnelConnector{}
	signer := testAgentJWTSigner(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/shell/establish", strings.NewReader(`{"agent_id":"LAB-OPERATOR-01_SYSTEM","ttl_seconds":60}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	remoteShellEstablishHandler(auth, signer, connector).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if connector.agentID != "LAB-OPERATOR-01_SYSTEM" {
		t.Fatalf("expected resolved agent id, got %q", connector.agentID)
	}
	if store.remoteOpsReq.AgentID != "LAB-OPERATOR-01_SYSTEM" {
		t.Fatalf("unexpected remote ops request %+v", store.remoteOpsReq)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["agent_socket"] != true || payload["shell_port"] != float64(47099) {
		t.Fatalf("unexpected shell payload %#v", payload)
	}
	session := payload["remote_ops_session"].(map[string]any)
	if session["token"] == "" {
		t.Fatalf("remote op token missing in %#v", session)
	}
	claims, err := signer.verifyAccessToken(cleanText(session["token"]))
	if err != nil {
		t.Fatalf("expected valid remote-op JWT, got %v", err)
	}
	caps := claims["capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "remote_shell" {
		t.Fatalf("unexpected token capabilities %+v", caps)
	}
}
