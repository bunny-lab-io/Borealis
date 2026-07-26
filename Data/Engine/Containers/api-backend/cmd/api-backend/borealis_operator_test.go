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
		case "/apis/metrics.k8s.io/v1beta1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
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
	t.Setenv("BOREALIS_SITE_WORKER_RUNTIME_CONFIG_HASH", "runtime-hash-test")
	var createdPod map[string]any
	var createdService map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/services":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/borealis/services":
			if err := json.NewDecoder(r.Body).Decode(&createdService); err != nil {
				t.Fatal(err)
			}
			spec := nestedMap(createdService, "spec")
			if cleanText(spec["type"]) != "ClusterIP" {
				t.Fatalf("site-worker service must be ClusterIP: %#v", spec)
			}
			selector := nestedMap(spec, "selector")
			if cleanText(selector["borealis.io/worker-guid"]) != "worker-safe" {
				t.Fatalf("site-worker service selector must target worker guid: %#v", selector)
			}
			ports, _ := spec["ports"].([]any)
			if len(ports) != 2 {
				t.Fatalf("expected remote ops and remote desktop service ports: %#v", ports)
			}
			if rawSpec, ok := createdService["spec"].(map[string]any); ok {
				rawSpec["clusterIP"] = "10.43.10.7"
			}
			writeJSON(w, http.StatusCreated, createdService)
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
			if spec["hostNetwork"] != false {
				t.Fatalf("site-worker pod must not use host networking after ClusterIP routing: %#v", spec)
			}
			if cleanText(spec["dnsPolicy"]) != "ClusterFirst" {
				t.Fatalf("site-worker pod should use normal cluster DNS: %#v", spec)
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
				"/etc/localtime",
				"/usr/share/zoneinfo",
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
		SiteName:          "Bunny's Lab",
		WorkerGUID:        "worker-safe",
		ImageRef:          "borealis-engine/site-worker:sha-cccccccccccc",
		ResourceProfile:   "small",
		RemoteOpsPort:     56001,
		RemoteDesktopPort: 61001,
	})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("expected site-worker launch accepted status=%d payload=%#v err=%v", status, payload, err)
	}
	if cleanText(nestedMap(createdPod, "metadata")["name"]) != "site-worker-bunnys-lab" {
		t.Fatalf("unexpected site-worker pod name: %#v", nestedMap(createdPod, "metadata"))
	}
	annotations := nestedMap(nestedMap(createdPod, "metadata"), "annotations")
	if cleanText(annotations["borealis.io/site-slug"]) != "bunnys-lab" {
		t.Fatalf("unexpected site-worker site annotations: %#v", annotations)
	}
	if cleanText(annotations["borealis.io/runtime-config-hash"]) != "runtime-hash-test" {
		t.Fatalf("unexpected runtime config hash annotation: %#v", annotations)
	}
	if cleanText(annotations["borealis.io/network-mode"]) != "cluster-ip" {
		t.Fatalf("expected ClusterIP network mode annotation: %#v", annotations)
	}
	spec := nestedMap(createdPod, "spec")
	containers, _ := spec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("expected one site-worker container: %#v", containers)
	}
	container, _ := containers[0].(map[string]any)
	envList, _ := container["env"].([]any)
	envByName := map[string]string{}
	for _, rawEnv := range envList {
		env, _ := rawEnv.(map[string]any)
		envByName[cleanText(env["name"])] = cleanText(env["value"])
	}
	if envByName["BOREALIS_SITE_WORKER_RUNTIME_CONFIG_HASH"] != "runtime-hash-test" {
		t.Fatalf("expected runtime config hash env, got %#v", envByName)
	}
	if envByName["BOREALIS_SITE_WORKER_BIND_HOST"] != "0.0.0.0" {
		t.Fatalf("expected site-worker pod network bind, got %#v", envByName)
	}
	if envByName["BOREALIS_SITE_WORKER_REMOTE_OPS_HOST"] != "10.43.10.7" {
		t.Fatalf("expected service ClusterIP as worker route host, got %#v", envByName)
	}
	if cleanText(payload["service_cluster_ip"]) != "10.43.10.7" {
		t.Fatalf("expected service ClusterIP in launch payload: %#v", payload)
	}
}

func TestBorealisOperatorLaunchSiteWorkerKeepsFullUUIDAsWorkerIdentity(t *testing.T) {
	t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
	var createdPod map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/services":
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/borealis/services":
			var createdService map[string]any
			if err := json.NewDecoder(r.Body).Decode(&createdService); err != nil {
				t.Fatal(err)
			}
			if rawSpec, ok := createdService["spec"].(map[string]any); ok {
				rawSpec["clusterIP"] = "10.43.10.8"
			}
			writeJSON(w, http.StatusCreated, createdService)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			if err := json.NewDecoder(r.Body).Decode(&createdPod); err != nil {
				t.Fatal(err)
			}
			writeJSON(w, http.StatusCreated, createdPod)
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	workerGUID := "11111111-2222-3333-4444-555555555555"
	payload, status, err := operator.launchSiteWorker(context.Background(), borealisOperatorLaunchSiteWorkerRequest{
		SiteID:            1,
		SiteName:          "Bunny Lab",
		WorkerGUID:        workerGUID,
		ImageRef:          "borealis-engine/site-worker:sha-cccccccccccc",
		ResourceProfile:   "standard",
		RemoteOpsPort:     56001,
		RemoteDesktopPort: 61001,
	})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("expected full UUID site-worker launch accepted status=%d payload=%#v err=%v", status, payload, err)
	}
	metadata := nestedMap(createdPod, "metadata")
	if cleanText(metadata["name"]) != "site-worker-bunny-lab" {
		t.Fatalf("unexpected full UUID site-worker pod name: %#v", nestedMap(createdPod, "metadata"))
	}
	labels := nestedMap(metadata, "labels")
	if cleanText(labels["borealis.io/worker-guid"]) != workerGUID {
		t.Fatalf("expected worker guid label to retain identity: %#v", labels)
	}
}

func TestBorealisOperatorListSiteWorkersAttachesKubernetesMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":              "site-worker-bunny-lab",
							"namespace":         "borealis",
							"creationTimestamp": "2026-07-19T03:00:00Z",
							"labels": map[string]any{
								"app.kubernetes.io/component":  "site-worker",
								"app.kubernetes.io/managed-by": "borealis-operator",
								"borealis.io/workload":         "site-worker",
								"borealis.io/worker-guid":      "worker-1",
								"borealis.io/site-id":          "7",
								"borealis.io/resource-profile": "standard",
							},
							"annotations": map[string]any{
								"borealis.io/image-ref":           "borealis-engine/site-worker:sha-cccccccccccc",
								"borealis.io/remote-ops-port":     "56001",
								"borealis.io/remote-desktop-port": "61001",
								"borealis.io/remote-ops-host":     "site-worker-bunny-lab.borealis.svc.cluster.local",
								"borealis.io/network-mode":        "cluster-ip",
							},
						},
						"spec": map[string]any{
							"containers": []any{
								map[string]any{
									"name":  "site-worker",
									"image": "borealis-engine/site-worker:sha-cccccccccccc",
									"resources": map[string]any{
										"limits": map[string]any{
											"cpu":    "1000m",
											"memory": "256Mi",
										},
									},
								},
							},
						},
						"status": map[string]any{
							"phase":     "Running",
							"startTime": "2026-07-19T03:00:00Z",
							"conditions": []any{
								map[string]any{"type": "Ready", "status": "True"},
							},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/services":
			writeJSON(w, http.StatusOK, map[string]any{
				"items": []any{
					map[string]any{
						"metadata": map[string]any{
							"name":      "site-worker-bunny-lab",
							"namespace": "borealis",
							"labels": map[string]any{
								"app.kubernetes.io/component":  "site-worker",
								"app.kubernetes.io/managed-by": "borealis-operator",
								"borealis.io/worker-guid":      "worker-1",
								"borealis.io/site-id":          "7",
							},
							"annotations": map[string]any{
								"borealis.io/network-mode": "cluster-ip",
								"borealis.io/service-dns":  "site-worker-bunny-lab.borealis.svc.cluster.local",
							},
						},
						"spec": map[string]any{
							"type":      "ClusterIP",
							"clusterIP": "10.43.10.9",
							"ports": []any{
								map[string]any{"name": "remote-ops", "port": 56001, "targetPort": "remote-ops", "protocol": "TCP"},
								map[string]any{"name": "remote-desktop", "port": 61001, "targetPort": "remote-desktop", "protocol": "TCP"},
							},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/metrics.k8s.io/v1beta1/namespaces/borealis/pods":
			writeJSON(w, http.StatusOK, map[string]any{
				"items": []any{
					map[string]any{
						"metadata":  map[string]any{"name": "site-worker-bunny-lab", "namespace": "borealis"},
						"timestamp": "2026-07-19T03:01:00Z",
						"window":    "15s",
						"containers": []any{
							map[string]any{
								"name": "site-worker",
								"usage": map[string]any{
									"cpu":    "125m",
									"memory": "64Mi",
								},
							},
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	operator := borealisOperatorTestClient(server)
	workers, err := operator.listSiteWorkers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 {
		t.Fatalf("expected one worker, got %#v", workers)
	}
	stats := schedulerAnyMap(workers[0]["docker_stats"])
	if got := stats["source"]; got != "metrics.k8s.io" {
		t.Fatalf("expected metrics source, got %#v", stats)
	}
	if got := stats["cpu_percent"]; got != 12.5 {
		t.Fatalf("expected cpu percent 12.5, got %#v", stats)
	}
	if got := stats["memory_usage_bytes"]; got != int64(64*1024*1024) {
		t.Fatalf("expected memory usage, got %#v", stats)
	}
	if got := stats["memory_limit_bytes"]; got != int64(256*1024*1024) {
		t.Fatalf("expected memory limit, got %#v", stats)
	}
	if got := stats["memory_percent"]; got != float64(25) {
		t.Fatalf("expected memory percent 25, got %#v", stats)
	}
	metrics := schedulerAnyMap(workers[0]["kubernetes_metrics"])
	if got := metrics["cpu_usage_millicores"]; got != float64(125) {
		t.Fatalf("expected millicore summary, got %#v", metrics)
	}
	if got := workers[0]["service_cluster_ip"]; got != "10.43.10.9" {
		t.Fatalf("expected site-worker Service ClusterIP, got %#v worker=%#v", got, workers[0])
	}
	if got := workers[0]["remote_ops_host"]; got != "10.43.10.9" {
		t.Fatalf("expected service ClusterIP route host, got %#v worker=%#v", got, workers[0])
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/services":
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
	expectedDeletes := map[string]bool{
		"/api/v1/namespaces/borealis/pods/site-worker-worker-safe":     true,
		"/api/v1/namespaces/borealis/services/site-worker-worker-safe": true,
	}
	if len(deleted) != len(expectedDeletes) {
		t.Fatalf("unexpected delete count paths=%#v", deleted)
	}
	for _, path := range deleted {
		if !expectedDeletes[path] {
			t.Fatalf("unexpected delete path %s paths=%#v", path, deleted)
		}
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
