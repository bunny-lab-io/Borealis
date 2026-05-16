package localui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokerStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	err := WriteBrokerState(dir, BrokerState{
		URL:   "http://127.0.0.1:12345/",
		Token: "secret",
	})
	if err != nil {
		t.Fatalf("write broker state: %v", err)
	}
	state, err := ReadBrokerState(dir)
	if err != nil {
		t.Fatalf("read broker state: %v", err)
	}
	if state.URL != "http://127.0.0.1:12345" {
		t.Fatalf("unexpected URL: %q", state.URL)
	}
	if state.Token != "secret" {
		t.Fatalf("unexpected token: %q", state.Token)
	}
	if !strings.HasSuffix(BrokerStatePath(dir), filepath.Join(dir, BrokerStateFile)) {
		t.Fatalf("broker state path did not use override dir: %s", BrokerStatePath(dir))
	}
}

func TestDoCommandWithStateSendsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(TokenHeader) != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","detail":"unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","data":{"hostname":"LAB"}}`))
	}))
	defer server.Close()

	response, err := DoCommandWithState(context.Background(), server.Client(), BrokerState{
		URL:   server.URL,
		Token: "secret",
	}, CommandRequest{Command: CommandStatusGet})
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("unexpected status: %s", response.Status)
	}
}

func TestDiagnosticsTextRedactedShape(t *testing.T) {
	text := DiagnosticsText(StatusSnapshot{
		Hostname:       "LAB-OPERATOR-01",
		ServerURL:      "https://borealis.example.test",
		EngineState:    "Online",
		ReleaseChannel: "source",
		Branch:         "feature/demo",
		Roles: []RoleHealth{
			{RoleLabel: "SYSTEM Context", Context: "system", StatusCode: "healthy", Detail: "Ready."},
		},
	})
	if !strings.Contains(text, "LAB-OPERATOR-01") || !strings.Contains(text, "SYSTEM Context") {
		t.Fatalf("diagnostics missing expected safe fields: %s", text)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "private_key", "enrollment_code"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("diagnostics contained forbidden field marker %q: %s", forbidden, text)
		}
	}
}
