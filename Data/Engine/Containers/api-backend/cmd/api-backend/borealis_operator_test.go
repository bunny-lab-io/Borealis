package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBorealisOperatorRequiresInternalToken(t *testing.T) {
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	mux := operator.handler()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/command", strings.NewReader(`{"verb":"GetClusterSummary"}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized command rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBorealisOperatorRejectsUnsupportedAndArbitraryRequests(t *testing.T) {
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	mux := operator.handler()

	for _, body := range []string{
		`{"verb":"ApplyRawYAML"}`,
		`{"verb":"LaunchPod","params":{"image":"docker.io/library/alpine:latest"}}`,
		`{"verb":"GetClusterSummary","raw_yaml":"apiVersion: v1"}`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/command", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(borealisOperatorTokenHeader, goBorealisOperatorToken([]byte("test-secret")))
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected unsupported request rejection, got %d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestBorealisOperatorClusterSummaryUsesReadOnlyNamespaceAPI(t *testing.T) {
	var seenPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("expected Kubernetes bearer token, got %q", r.Header.Get("Authorization"))
		}
		seenPaths = append(seenPaths, r.URL.Path)
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/borealis/deployments":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{
				map[string]any{
					"metadata": map[string]any{
						"name":      "borealis-operator",
						"namespace": "borealis",
						"labels": map[string]any{
							"app.kubernetes.io/managed-by": "Engine.sh",
							"borealis.io/service-key":      "borealis-operator",
						},
					},
					"spec": map[string]any{"replicas": 1},
					"status": map[string]any{
						"readyReplicas":     1,
						"availableReplicas": 1,
						"updatedReplicas":   1,
					},
				},
			}})
		case "/apis/apps/v1/namespaces/borealis/statefulsets":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{
				map[string]any{
					"metadata": map[string]any{
						"name":      "site-worker-worker-1",
						"namespace": "borealis",
						"labels": map[string]any{
							"app.kubernetes.io/component": "site-worker",
							"borealis.io/workload":        "site-worker",
						},
					},
					"status": map[string]any{
						"phase":     "Running",
						"nodeName":  "engine-1",
						"podIP":     "10.42.0.12",
						"startTime": "2026-07-18T19:00:00Z",
						"conditions": []any{map[string]any{
							"type":   "Ready",
							"status": "True",
						}},
						"containerStatuses": []any{map[string]any{
							"restartCount": 0,
						}},
					},
				},
			}})
		case "/api/v1/namespaces/borealis/services":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": "borealis-operator"}},
			}})
		default:
			t.Fatalf("unexpected Kubernetes path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	operator := &borealisOperator{
		secret:    []byte("test-secret"),
		namespace: "borealis",
		kube: &kubernetesAPIClient{
			baseURL:    server.URL,
			token:      "test-token",
			httpClient: server.Client(),
		},
	}
	result, status, err := operator.clusterSummary(context.Background())
	if err != nil || status != http.StatusOK {
		t.Fatalf("expected cluster summary, status=%d err=%v", status, err)
	}
	if result["workload_count"] != 1 {
		t.Fatalf("expected one workload, got %#v", result["workload_count"])
	}
	if result["site_worker_count"] != 1 {
		t.Fatalf("expected one site worker, got %#v", result["site_worker_count"])
	}
	for _, path := range seenPaths {
		if strings.Contains(path, "/secrets") || strings.Contains(path, "/nodes") {
			t.Fatalf("operator should not query secret/node paths, saw %s", path)
		}
	}
}

func TestBorealisOperatorGetWorkloadStatusAllowlist(t *testing.T) {
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	_, status, err := operator.getWorkloadStatus(context.Background(), "api-backend")
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("expected unsupported workload rejection, status=%d err=%v", status, err)
	}
}

func TestBorealisOperatorClientRequiresExplicitBaseURL(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SECRET", "test-secret")
	t.Setenv("BOREALIS_OPERATOR_BASE_URL", "")
	if _, configured := newBorealisOperatorClientFromEnv(); configured {
		t.Fatalf("operator client should not default to cluster DNS from Compose runtime")
	}
}

func TestBorealisOperatorClientHealthcheckUsesHMACToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/command" {
			t.Fatalf("unexpected operator path %q", r.URL.Path)
		}
		if r.Header.Get(borealisOperatorTokenHeader) != goBorealisOperatorToken([]byte("test-secret")) {
			t.Fatalf("missing operator HMAC token")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"verb":   "GetClusterSummary",
			"result": map[string]any{"namespace": "borealis"},
		})
	}))
	defer server.Close()

	t.Setenv("BOREALIS_OPERATOR_SECRET", "test-secret")
	t.Setenv("BOREALIS_OPERATOR_BASE_URL", server.URL)
	if err := runBorealisOperatorClientHealthcheck(context.Background(), gatewayConfig{}); err != nil {
		t.Fatalf("expected operator client healthcheck to pass: %v", err)
	}
}
