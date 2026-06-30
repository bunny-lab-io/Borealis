package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteRegistryRootsCallsWorkerAndReturnsEntries(t *testing.T) {
	var sawStatus bool
	var sawCall bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			sawStatus = true
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			sawCall = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("worker request body decode failed: %v", err)
			}
			if body["event_name"] != "registry_management_request" {
				t.Fatalf("unexpected call body %#v", body)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "roots" || payload["hostname"] != "LAB-OPERATOR-01" || payload["requested_by"] != "operator" {
				t.Fatalf("unexpected registry payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":            true,
					"platform":      "windows",
					"context_label": "SYSTEM",
					"current_path":  "",
					"entries": []map[string]any{
						{"name": "HKEY_LOCAL_MACHINE", "path": "HKLM", "kind": "hive"},
					},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteRegistryRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/registry/LAB-OPERATOR-01/roots", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawStatus || !sawCall {
		t.Fatalf("expected status and call requests, sawStatus=%v sawCall=%v", sawStatus, sawCall)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["hostname"] != "LAB-OPERATOR-01" || payload["platform"] != "windows" {
		t.Fatalf("unexpected roots payload %#v", payload)
	}
	entries, _ := payload["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %#v", payload["entries"])
	}
}

func TestRemoteRegistryMutateMapsKeyCreate(t *testing.T) {
	var sawPayload map[string]any
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("worker request body decode failed: %v", err)
			}
			sawPayload, _ = body["payload"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok": true,
					"entry": map[string]any{
						"path": "HKLM\\Software\\Borealis",
						"name": "Borealis",
					},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteRegistryRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	body := []byte(`{"parent_path":"HKLM\\Software","name":"Borealis"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/registry/LAB-OPERATOR-01/key/create", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if sawPayload["action"] != "key_create" || sawPayload["parent_path"] != `HKLM\Software` || sawPayload["name"] != "Borealis" {
		t.Fatalf("unexpected payload %#v", sawPayload)
	}
}

func TestRemoteRegistryChildrenRequiresPath(t *testing.T) {
	store := &fakeProcessStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerRemoteRegistryRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/registry/LAB-OPERATOR-01/children", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRemoteRegistryChildrenAcceptsLargeHiveListing(t *testing.T) {
	largeEntries := make([]map[string]any, 0, 5000)
	padding := strings.Repeat("X", 220)
	for i := 0; i < cap(largeEntries); i++ {
		name := fmt.Sprintf("VeryLongClassName%s-%05d", padding, i)
		largeEntries = append(largeEntries, map[string]any{
			"path":         "HKCR\\" + name,
			"parent_path":  "HKCR",
			"name":         name,
			"kind":         "key",
			"has_children": false,
			"editable":     true,
		})
	}

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("worker request body decode failed: %v", err)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "children" || payload["path"] != "HKCR" {
				t.Fatalf("unexpected registry payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":            true,
					"platform":      "windows",
					"context_label": "SYSTEM",
					"current_path":  "HKCR",
					"entries":       largeEntries,
					"values":        []map[string]any{},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteRegistryRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/registry/LAB-OPERATOR-01/children?path=HKCR", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() <= 1<<20 {
		t.Fatalf("test response did not exceed old 1 MiB cap: %d", recorder.Body.Len())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	entries, _ := payload["entries"].([]any)
	if len(entries) != len(largeEntries) {
		t.Fatalf("expected %d entries, got %d", len(largeEntries), len(entries))
	}
}
