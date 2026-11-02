package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func operatorWorkerPolicyObject(name, uid, guid string) map[string]any {
	return map[string]any{"metadata": map[string]any{
		"name": name, "namespace": "borealis", "uid": uid,
		"labels": map[string]any{"app.kubernetes.io/component": "site-worker", "app.kubernetes.io/managed-by": "borealis-operator", "borealis.io/worker-guid": guid, "borealis.io/site-id": "7"},
	}}
}

func TestOperatorSiteWorkerClientRejectsIncompleteInventory(t *testing.T) {
	for _, body := range []string{
		``, `null`, `{`, `[]`, `{}`, `{"ok":true,"verb":"ListSiteWorkers","result":null}`,
		`{"ok":false,"verb":"ListSiteWorkers","result":{"workers":[]}}`,
		`{"ok":true,"verb":"other","result":{"workers":[]}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":null}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":{}}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":[{},null]}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":[{},1]}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":[],"workers":[]}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"Workers":[]}}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":[]}} {}`,
		`{"ok":true,"verb":"ListSiteWorkers","result":{"workers":[]}}` + strings.Repeat(" ", 4<<20),
	} {
		t.Run(fmt.Sprintf("invalid-%d", len(body))+body[:min(len(body), 60)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			client := &borealisOperatorClient{baseURL: server.URL, httpClient: server.Client()}
			if workers, err := client.listSiteWorkers(context.Background()); err == nil || workers != nil {
				t.Fatalf("incomplete inventory accepted: %#v %v", workers, err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": map[string]any{"workers": []any{}}})
	}))
	defer server.Close()
	client := &borealisOperatorClient{baseURL: server.URL, httpClient: server.Client()}
	if workers, err := client.listSiteWorkers(context.Background()); err != nil || workers == nil || len(workers) != 0 {
		t.Fatalf("complete empty inventory rejected: %#v %v", workers, err)
	}
}

func TestOperatorSiteWorkerLaunchPreservesUncertainCustody(t *testing.T) {
	setOperatorWorkerLimitsFixture(t, workerLimitsFixture())
	t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
	for _, mode := range []string{"existing site Pod", "existing site Service", "Pod reply lost", "Service reply lost", "Pod rejected", "malformed inventory", "partial inventory"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			pods, services := []any{}, []any{}
			creates, deletes := 0, 0
			if mode == "existing site Pod" {
				pods = append(pods, operatorWorkerPolicyObject("site-worker-old-name", "old-pod", "another-guid"))
			}
			if mode == "existing site Service" {
				services = append(services, operatorWorkerPolicyObject("site-worker-old-name", "old-service", "another-guid"))
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodGet:
					if mode == "malformed inventory" {
						_, _ = w.Write([]byte(`{"items":[null]}`))
						return
					}
					if mode == "partial inventory" {
						_, _ = w.Write([]byte(`{"metadata":{"continue":"next"},"items":[]}`))
						return
					}
					items := pods
					if strings.HasSuffix(r.URL.Path, "/services") {
						items = services
					}
					writeJSON(w, http.StatusOK, map[string]any{"items": items})
				case http.MethodPost:
					creates++
					var object map[string]any
					if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
						t.Error(err)
					}
					if strings.HasSuffix(r.URL.Path, "/services") {
						services = append(services, object)
						if mode == "Service reply lost" {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						writeJSON(w, http.StatusCreated, object)
						return
					}
					if mode != "Pod rejected" {
						pods = append(pods, object)
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.WriteHeader(http.StatusBadGateway)
				case http.MethodDelete:
					deletes++
					w.WriteHeader(http.StatusInternalServerError)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()
			operator := borealisOperatorTestClient(server)
			request := borealisOperatorLaunchSiteWorkerRequest{SiteID: 7, SiteName: "Renamed Site", WorkerGUID: "new-guid", ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", RemoteOpsPort: 56001, RemoteDesktopPort: 61001}
			for attempt := 0; attempt < 2; attempt++ {
				if _, _, err := operator.launchSiteWorker(context.Background(), request); err == nil {
					t.Fatal("uncertain or occupied site accepted")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if deletes != 0 || creates > 2 {
				t.Fatalf("uncertain custody deleted or redelivered: creates=%d deletes=%d", creates, deletes)
			}
			if strings.Contains(mode, "reply lost") || mode == "Pod rejected" {
				if len(services) != 1 {
					t.Fatalf("Service custody lost: %#v", services)
				}
			} else if creates != 0 {
				t.Fatalf("preflight failure mutated Kubernetes: %d", creates)
			}
		})
	}
}

func TestOperatorSiteWorkerRetirementRetainsReplacementCustody(t *testing.T) {
	for _, mode := range []string{"retired", "delayed retry", "candidate", "candidate false", "duplicate GUID", "duplicate site", "missing UID", "wrong namespace", "Pod UID replaced", "Pod still terminating", "Pod replaced after delete", "candidate after delete", "absence read failed", "Service UID replaced", "duplicate Service", "malformed inventory", "duplicate JSON", "partial inventory"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			pod := operatorWorkerPolicyObject("site-worker-old", "pod-uid", "worker-safe")
			service := operatorWorkerPolicyObject("site-worker-old", "service-uid", "worker-safe")
			pods, services := []any{pod}, []any{service}
			deletes, serviceDeletes := 0, 0
			switch mode {
			case "candidate":
				clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["borealis.io/redeploy-agent-candidate"] = "true"
			case "candidate false":
				clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")["borealis.io/redeploy-agent-candidate"] = "false"
			case "duplicate GUID":
				pods = append(pods, operatorWorkerPolicyObject("site-worker-next", "next-uid", "worker-safe"))
			case "duplicate site":
				pods = append(pods, operatorWorkerPolicyObject("site-worker-next", "next-uid", "other-guid"))
			case "duplicate Service":
				services = append(services, operatorWorkerPolicyObject("site-worker-other", "other-service", "worker-safe"))
			case "missing UID":
				delete(clusterSSHStorageMap(pod, "metadata"), "uid")
			case "wrong namespace":
				clusterSSHStorageMap(pod, "metadata")["namespace"] = "other"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet {
					if mode == "malformed inventory" {
						_, _ = w.Write([]byte(`{"items":[null]}`))
						return
					}
					if mode == "duplicate JSON" {
						_, _ = w.Write([]byte(`{"items":[],"items":[]}`))
						return
					}
					if mode == "partial inventory" {
						_, _ = w.Write([]byte(`{"metadata":{"remainingItemCount":1},"items":[]}`))
						return
					}
					if mode == "absence read failed" && deletes > 0 {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					items := pods
					if strings.HasSuffix(r.URL.Path, "/services") {
						items = services
					}
					writeJSON(w, http.StatusOK, map[string]any{"items": items})
					return
				}
				if r.Method != http.MethodDelete {
					t.Errorf("unexpected mutation %s", r.Method)
					return
				}
				deletes++
				var options map[string]any
				if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
					t.Error(err)
				}
				if strings.Contains(r.URL.Path, "/services/") {
					serviceDeletes++
					if nestedMap(options, "preconditions")["uid"] != "service-uid" {
						t.Error("Service deletion lacks original UID")
					}
					if mode == "Service UID replaced" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					services = []any{}
				} else {
					if nestedMap(options, "preconditions")["uid"] != "pod-uid" {
						t.Error("Pod deletion lacks original UID")
					}
					if mode == "Pod UID replaced" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					switch mode {
					case "Pod still terminating", "delayed retry":
						clusterSSHStorageMap(pod, "metadata")["deletionTimestamp"] = "2026-11-02T14:00:00Z"
					case "Pod replaced after delete":
						pods = []any{operatorWorkerPolicyObject("site-worker-old", "new-uid", "other-guid")}
					case "candidate after delete":
						pods = []any{operatorWorkerPolicyObject("site-worker-next", "new-uid", "worker-safe")}
					default:
						pods = []any{}
					}
				}
				writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
			}))
			defer server.Close()
			_, _, err := borealisOperatorTestClient(server).retireSiteWorker(context.Background(), borealisOperatorRetireSiteWorkerRequest{WorkerGUID: "worker-safe"})
			if mode == "delayed retry" {
				mu.Lock()
				if err == nil || deletes != 1 || len(services) != 1 {
					t.Error("terminating Pod did not retain Service custody")
				}
				pods = []any{} // Kubernetes later completes graceful termination.
				mu.Unlock()
				_, _, err = borealisOperatorTestClient(server).retireSiteWorker(context.Background(), borealisOperatorRetireSiteWorkerRequest{WorkerGUID: "worker-safe"})
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "retired" || mode == "delayed retry" {
				if err != nil || len(pods) != 0 || len(services) != 0 || deletes != 2 {
					t.Fatalf("retirement failed: %v deletes=%d", err, deletes)
				}
			} else if err == nil || len(services) != 1 && mode != "duplicate Service" || serviceDeletes > 0 && mode != "Service UID replaced" {
				t.Fatalf("uncertain retirement lost custody: err=%v deletes=%d serviceDeletes=%d", err, deletes, serviceDeletes)
			}
		})
	}
}

func TestOperatorSiteWorkerRetirementConcurrentUIDReplacement(t *testing.T) {
	for _, resource := range []string{"pods", "services"} {
		t.Run(resource, func(t *testing.T) {
			var mu sync.Mutex
			pod := operatorWorkerPolicyObject("site-worker-old", "pod-original", "worker-safe")
			service := operatorWorkerPolicyObject("site-worker-old", "service-original", "worker-safe")
			pods := []any{pod}
			aboutToDelete, replaced := make(chan struct{}), make(chan struct{})
			protectedDeletes := 0
			go func() {
				<-aboutToDelete
				mu.Lock()
				object := pod
				if resource == "services" {
					object = service
				}
				clusterSSHStorageMap(object, "metadata")["uid"] = "replacement-uid"
				mu.Unlock()
				close(replaced)
			}()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/"+resource+"/") {
					close(aboutToDelete)
					<-replaced
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodGet {
					items := pods
					if strings.HasSuffix(r.URL.Path, "/services") {
						items = []any{service}
					}
					writeJSON(w, http.StatusOK, map[string]any{"items": items})
					return
				}
				var options map[string]any
				if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
					t.Error(err)
				}
				object := pod
				if strings.Contains(r.URL.Path, "/services/") {
					object = service
				}
				if nestedMap(options, "preconditions")["uid"] != nestedMap(object, "metadata")["uid"] {
					protectedDeletes++
					w.WriteHeader(http.StatusConflict)
					return
				}
				pods = []any{}
				writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
			}))
			defer server.Close()
			_, _, err := borealisOperatorTestClient(server).retireSiteWorker(context.Background(), borealisOperatorRetireSiteWorkerRequest{WorkerGUID: "worker-safe"})
			if err == nil {
				t.Fatal("replacement deleted with stale custody")
			}
			mu.Lock()
			defer mu.Unlock()
			if protectedDeletes != 1 {
				t.Fatalf("expected exactly one UID conflict, got %d", protectedDeletes)
			}
		})
	}
}

func TestOperatorSiteWorkerActualRotationPreservesCustody(t *testing.T) {
	operator := &borealisOperator{namespace: "borealis"}
	profile, _ := borealisOperatorSiteWorkerResourceProfile("standard")
	const guid = "11111111-2222-3333-4444-555555555555"
	original := operator.siteWorkerPodManifest("site-worker-bunny-lab", "site-worker-bunny-lab", "10.43.0.7", 7, "Bunny Lab", "bunny-lab", guid, "borealis-engine/site-worker:sha-cccccccccccc", "standard", profile, 56001, 61001)
	// Round-trip the fixed Go producer before feeding the real Bash/Python
	// replacement renderer, exactly as Kubernetes JSON would represent it.
	raw, err := json.Marshal(original)
	if err != nil || json.Unmarshal(raw, &original) != nil {
		t.Fatal("invalid producer fixture", err)
	}
	clusterSSHStorageMap(original, "metadata")["uid"] = "old-pod-uid"
	clusterSSHStorageMap(original, "spec")["nodeName"] = "old-node"
	candidate := sshWorkerRotationCandidate(t, original)
	meta := clusterSSHStorageMap(candidate, "metadata")
	labels := clusterSSHStorageMap(meta, "labels")
	if labels["borealis.io/worker-guid"] != guid || labels["borealis.io/site-id"] != "7" || labels["borealis.io/redeploy-agent-candidate"] != "true" || clusterSSHStorageMap(meta, "annotations")["borealis.io/service-name"] != "site-worker-bunny-lab" || clusterSSHStorageMap(candidate, "spec")["nodeName"] != nil {
		t.Fatal("real replacement renderer changed worker custody")
	}
	meta["uid"] = "candidate-uid"
	pods := []any{original, candidate}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("rotation guard mutated Kubernetes: %s", r.Method)
		}
		items := []any{}
		if strings.HasSuffix(r.URL.Path, "/pods") {
			items = pods
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}))
	defer server.Close()
	operator = borealisOperatorTestClient(server)
	if _, _, err := operator.siteWorkerRetirementInventory(context.Background(), guid); err == nil {
		t.Fatal("real old/candidate overlap accepted for retirement")
	}
	pods = []any{candidate}
	if _, _, err := operator.siteWorkerRetirementInventory(context.Background(), guid); err == nil {
		t.Fatal("unfinished sole candidate accepted for retirement")
	}
	delete(labels, "borealis.io/redeploy-agent-candidate")
	if selected, _, err := operator.siteWorkerRetirementInventory(context.Background(), guid); err != nil || len(selected) != 1 {
		t.Fatalf("completed real replacement rejected: %v", err)
	}
}

func TestOperatorSiteWorkerSummaryUsesAssignedNode(t *testing.T) {
	for _, assigned := range []string{"engine-assigned", ""} {
		t.Run(assigned, func(t *testing.T) {
			pod := operatorWorkerPolicyObject("site-worker-old", "pod-uid", "worker-safe")
			pod["spec"] = map[string]any{"nodeName": assigned}
			pod["status"] = map[string]any{"nodeName": "forged-status-node"}
			clusterSSHStorageMap(pod, "metadata")["deletionTimestamp"] = "2026-11-02T16:00:00Z"
			summary := summarizeBorealisSiteWorkerPod(pod)
			if summary["node_name"] != assigned || summary["uid"] != "pod-uid" || summary["deletion_timestamp"] != "2026-11-02T16:00:00Z" {
				t.Fatalf("native worker custody lost: %#v", summary)
			}
		})
	}
}

func TestOperatorSiteWorkerPublicStatusPreservesPrivateCustody(t *testing.T) {
	worker := map[string]any{"worker_guid": "worker-safe", "site_id": 7, "node_name": "assigned-node", "uid": "private-uid", "deletion_timestamp": "2026-11-02T16:00:00Z"}
	public := borealisOperatorPublicSiteWorkers([]any{worker})
	if len(public) != 1 || public[0]["uid"] != nil || public[0]["deletion_timestamp"] != nil || public[0]["node_name"] != "assigned-node" || worker["uid"] != "private-uid" || worker["deletion_timestamp"] == nil {
		t.Fatal("public projection changed internal snapshot or exposed custody", public, worker)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request borealisOperatorCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		result := map[string]any{}
		switch request.Verb {
		case "GetClusterSummary":
			result["site_worker_count"] = 1
		case "ListWorkloads":
			result["workloads"] = []any{}
		case "ListSiteWorkers":
			result["workers"] = []any{worker}
		default:
			t.Error("unexpected operator command", request.Verb)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verb": request.Verb, "result": result})
	}))
	defer server.Close()
	t.Setenv("BOREALIS_OPERATOR_SECRET", "test-secret")
	t.Setenv("BOREALIS_OPERATOR_BASE_URL", server.URL)
	client, _ := newBorealisOperatorClientFromEnv()
	internal, err := client.listSiteWorkers(context.Background())
	if err != nil || len(internal) != 1 || internal[0]["uid"] != "private-uid" || internal[0]["deletion_timestamp"] == nil {
		t.Fatal("internal lifecycle lost custody", internal, err)
	}
	auth, _ := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	request := httptest.NewRequest(http.MethodGet, "/api/server/k3s/operator", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	recorder := httptest.NewRecorder()
	borealisOperatorStatusHandler(auth).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "private-uid") || strings.Contains(recorder.Body.String(), "deletion_timestamp") || !strings.Contains(recorder.Body.String(), "assigned-node") {
		t.Fatalf("public status shape changed: %d %s", recorder.Code, recorder.Body.String())
	}
}
