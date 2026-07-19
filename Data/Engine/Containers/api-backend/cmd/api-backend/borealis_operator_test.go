package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	_, status, err := operator.getWorkloadStatus(context.Background(), "unknown-service")
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("expected unsupported workload rejection, status=%d err=%v", status, err)
	}
}

func TestBorealisOperatorRejectsUnknownLifecycleParams(t *testing.T) {
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	mux := operator.handler()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/command", strings.NewReader(`{"verb":"RolloutKnownWorkload","params":{"service_key":"borealis-operator","image_ref":"borealis-engine/borealis-operator:sha-111111111111","raw_yaml":"apiVersion: v1"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(borealisOperatorTokenHeader, goBorealisOperatorToken([]byte("test-secret")))
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected strict params rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBorealisOperatorRolloutKnownWorkloadPatchesNamedDeployment(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_WORKLOAD_IMAGE_ALLOWLIST", "borealis-operator=borealis-engine/borealis-operator:sha-bbbbbbbbbbbb")
	oldImage := "borealis-engine/borealis-operator:sha-aaaaaaaaaaaa"
	newImage := "borealis-engine/borealis-operator:sha-bbbbbbbbbbbb"
	currentImage := oldImage
	generation := int64(1)
	patchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("expected Kubernetes bearer token, got %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/apis/apps/v1/namespaces/borealis/deployments/borealis-operator" {
			t.Fatalf("unexpected Kubernetes path %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, borealisOperatorTestDeployment(currentImage, generation, generation, 1, 1, 1))
		case http.MethodPatch:
			if !strings.Contains(r.Header.Get("Content-Type"), "strategic-merge-patch") {
				t.Fatalf("expected strategic merge patch, got %q", r.Header.Get("Content-Type"))
			}
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			containers, _ := nestedMap(nestedMap(nestedMap(patch, "spec"), "template"), "spec")["containers"].([]any)
			if len(containers) != 1 || cleanText(containers[0].(map[string]any)["name"]) != "borealis-operator" {
				t.Fatalf("patch must target only borealis-operator container: %#v", patch)
			}
			currentImage = cleanText(containers[0].(map[string]any)["image"])
			generation++
			patchCount++
			writeJSON(w, http.StatusOK, borealisOperatorTestDeployment(currentImage, generation, generation, 1, 1, 1))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	payload, status, err := operator.rolloutKnownWorkload(context.Background(), borealisOperatorRolloutRequest{
		ServiceKey: "borealis-operator",
		ImageRef:   newImage,
	})
	if err != nil || status != http.StatusOK {
		t.Fatalf("expected rollout success status=%d payload=%#v err=%v", status, payload, err)
	}
	if patchCount != 1 || currentImage != newImage {
		t.Fatalf("expected one rollout patch to %s, patchCount=%d current=%s", newImage, patchCount, currentImage)
	}
}

func TestBorealisOperatorRolloutRejectsUnknownServiceAndImage(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_WORKLOAD_IMAGE_ALLOWLIST", "borealis-operator=borealis-engine/borealis-operator:sha-bbbbbbbbbbbb")
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	_, status, err := operator.rolloutKnownWorkload(context.Background(), borealisOperatorRolloutRequest{
		ServiceKey: "not-real",
		ImageRef:   "borealis-engine/borealis-operator:sha-bbbbbbbbbbbb",
	})
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("expected unknown service rejection, status=%d err=%v", status, err)
	}
	_, status, err = operator.rolloutKnownWorkload(context.Background(), borealisOperatorRolloutRequest{
		ServiceKey: "borealis-operator",
		ImageRef:   "docker.io/library/alpine:latest",
	})
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("expected mutable/unallowlisted image rejection, status=%d err=%v", status, err)
	}
}

func TestBorealisOperatorRolloutRollbackOnFailedReadiness(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_WORKLOAD_IMAGE_ALLOWLIST", "borealis-operator=borealis-engine/borealis-operator:sha-bbbbbbbbbbbb")
	oldImage := "borealis-engine/borealis-operator:sha-aaaaaaaaaaaa"
	newImage := "borealis-engine/borealis-operator:sha-bbbbbbbbbbbb"
	currentImage := oldImage
	generation := int64(1)
	rolloutPatched := false
	rollbackPatched := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/borealis/deployments/borealis-operator" {
			t.Fatalf("unexpected Kubernetes path %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			if rollbackPatched {
				writeJSON(w, http.StatusOK, borealisOperatorTestDeployment(currentImage, generation, generation, 1, 1, 1))
				return
			}
			writeJSON(w, http.StatusOK, borealisOperatorTestDeployment(currentImage, generation, generation, 1, 0, 0))
		case http.MethodPatch:
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			containers, _ := nestedMap(nestedMap(nestedMap(patch, "spec"), "template"), "spec")["containers"].([]any)
			currentImage = cleanText(containers[0].(map[string]any)["image"])
			generation++
			if currentImage == newImage {
				rolloutPatched = true
			}
			if currentImage == oldImage {
				rollbackPatched = true
			}
			writeJSON(w, http.StatusOK, borealisOperatorTestDeployment(currentImage, generation, generation, 1, 0, 0))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, status, err := operator.rolloutKnownWorkload(ctx, borealisOperatorRolloutRequest{
		ServiceKey: "borealis-operator",
		ImageRef:   newImage,
	})
	if err == nil || status != http.StatusBadGateway {
		t.Fatalf("expected failed rollout, status=%d err=%v", status, err)
	}
	if !rolloutPatched || !rollbackPatched || currentImage != oldImage {
		t.Fatalf("expected rollback to old image, rolloutPatched=%v rollbackPatched=%v current=%s", rolloutPatched, rollbackPatched, currentImage)
	}
}

func TestBorealisOperatorScaleKnownWorkloadBounds(t *testing.T) {
	operator := &borealisOperator{secret: []byte("test-secret"), namespace: "borealis"}
	_, status, err := operator.scaleKnownWorkload(context.Background(), borealisOperatorScaleRequest{
		ServiceKey: "borealis-operator",
		Replicas:   2,
	})
	if err == nil || status != http.StatusBadRequest {
		t.Fatalf("expected operator scale bound rejection, status=%d err=%v", status, err)
	}
}

func TestBorealisOperatorLaunchSiteWorkerBuildsSafePod(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
	var createdPod map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			if err := json.NewDecoder(r.Body).Decode(&createdPod); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(createdPod)
			text := string(raw)
			for _, forbidden := range []string{"privileged", "serviceAccountName", "/var/run/docker.sock"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("site-worker pod contains forbidden field %q: %s", forbidden, text)
				}
			}
			spec := nestedMap(createdPod, "spec")
			if spec["automountServiceAccountToken"] != false {
				t.Fatalf("site-worker pod must not mount service account token: %#v", spec)
			}
			if spec["hostNetwork"] != true {
				t.Fatalf("site-worker pod must use host loopback bridge during Compose transition: %#v", spec)
			}
			volumes, _ := spec["volumes"].([]any)
			hostPaths := map[string]bool{}
			for _, rawVolume := range volumes {
				volume, _ := rawVolume.(map[string]any)
				hostPath := schedulerAnyMap(volume["hostPath"])
				if len(hostPath) == 0 {
					continue
				}
				hostPaths[cleanText(hostPath["path"])] = true
			}
			for _, expected := range []string{
				"/opt/Borealis/Engine/Services/api-backend/logs/site-workers",
				"/opt/Borealis/Engine/Services/api-backend/cache",
				"/opt/Borealis/Engine/Services/api-backend/config",
				"/opt/Borealis/Engine/Services/api-backend/secrets",
			} {
				if !hostPaths[expected] {
					t.Fatalf("site-worker pod missing fixed hostPath %s in %#v", expected, hostPaths)
				}
			}
			writeJSON(w, http.StatusCreated, createdPod)
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	payload, status, err := operator.launchSiteWorker(context.Background(), borealisOperatorLaunchSiteWorkerRequest{
		SiteID:            7,
		WorkerGUID:        "worker-safe",
		ImageRef:          "borealis-engine/site-worker:sha-cccccccccccc",
		ResourceProfile:   "small",
		RemoteOpsPort:     56001,
		RemoteDesktopPort: 61001,
	})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("expected site-worker launch accepted status=%d payload=%#v err=%v", status, payload, err)
	}
	if cleanText(nestedMap(createdPod, "metadata")["name"]) != "site-worker-worker-safe" {
		t.Fatalf("unexpected site-worker pod name: %#v", nestedMap(createdPod, "metadata"))
	}
}

func TestBorealisOperatorRetireSiteWorkerDeletesOnlyManagedWorkerPods(t *testing.T) {
	deleted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{
				map[string]any{"metadata": map[string]any{"name": "site-worker-worker-safe", "namespace": "borealis", "labels": map[string]any{
					"app.kubernetes.io/component":  "site-worker",
					"app.kubernetes.io/managed-by": "borealis-operator",
					"borealis.io/worker-guid":      "worker-safe",
				}}},
				map[string]any{"metadata": map[string]any{"name": "site-worker-user-owned", "namespace": "borealis", "labels": map[string]any{
					"app.kubernetes.io/component":  "site-worker",
					"app.kubernetes.io/managed-by": "manual",
					"borealis.io/worker-guid":      "worker-safe",
				}}},
			}})
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	payload, status, err := operator.retireSiteWorker(context.Background(), borealisOperatorRetireSiteWorkerRequest{WorkerGUID: "worker-safe", Reason: "test"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("expected retire success status=%d payload=%#v err=%v", status, payload, err)
	}
	if len(deleted) != 1 || deleted[0] != "/api/v1/namespaces/borealis/pods/site-worker-worker-safe" {
		t.Fatalf("unexpected deleted pods: %#v", deleted)
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

func borealisOperatorTestClient(server *httptest.Server) *borealisOperator {
	return &borealisOperator{
		secret:    []byte("test-secret"),
		namespace: "borealis",
		kube: &kubernetesAPIClient{
			baseURL:    server.URL,
			token:      "test-token",
			httpClient: server.Client(),
		},
	}
}

func borealisOperatorTestDeployment(image string, generation int64, observedGeneration int64, replicas int64, ready int64, updated int64) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"name":       "borealis-operator",
			"namespace":  "borealis",
			"generation": generation,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "Engine.sh",
				"borealis.io/service-key":      "borealis-operator",
			},
		},
		"spec": map[string]any{
			"replicas": replicas,
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "borealis-operator", "image": image},
					},
				},
			},
		},
		"status": map[string]any{
			"observedGeneration": observedGeneration,
			"readyReplicas":      ready,
			"availableReplicas":  ready,
			"updatedReplicas":    updated,
		},
	}
}
