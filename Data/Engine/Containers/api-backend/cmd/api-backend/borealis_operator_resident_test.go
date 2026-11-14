package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBorealisOperatorConcurrentSiteLaunchReservesOneResident(t *testing.T) {
	setOperatorWorkerLimitsFixture(t, workerLimitsFixture())
	t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
	for _, mode := range []string{"created", "Service reply lost", "Pod reply lost", "foreign reservation conflict"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			objects := map[string]map[string]map[string]any{"pods": {}, "services": {}}
			if mode == "foreign reservation conflict" {
				objects["services"]["site-worker-site-7"] = map[string]any{"metadata": map[string]any{"name": "site-worker-site-7", "uid": "foreign", "labels": map[string]any{}}}
			}
			serviceReads, servicePosts, podPosts, deletes := 0, 0, 0, 0
			preflightComplete := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resource := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/borealis/")
				if resource != "pods" && resource != "services" {
					t.Errorf("unexpected resource %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				if r.Method == http.MethodGet && resource == "services" && serviceReads < 2 {
					serviceReads++
					if serviceReads == 2 {
						close(preflightComplete)
					}
					mu.Unlock()
					// Both independent operators observed an empty inventory before
					// either CREATE; list preflight alone cannot exclude this race.
					select {
					case <-preflightComplete:
						writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
					case <-ctx.Done():
						w.WriteHeader(http.StatusGatewayTimeout)
					}
					return
				}
				defer mu.Unlock()
				switch r.Method {
				case http.MethodGet:
					items := make([]any, 0, len(objects[resource]))
					for _, object := range objects[resource] {
						items = append(items, object)
					}
					writeJSON(w, http.StatusOK, map[string]any{"items": items})
				case http.MethodPost:
					if resource == "services" {
						servicePosts++
					} else {
						podPosts++
					}
					var object map[string]any
					if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					meta := nestedMap(object, "metadata")
					name := cleanText(meta["name"])
					if _, exists := objects[resource][name]; exists {
						writeJSON(w, http.StatusConflict, map[string]any{"reason": "AlreadyExists"})
						return
					}
					meta["uid"] = newClusterUUID()
					object["metadata"] = meta
					objects[resource][name] = object
					if (resource == "services" && mode == "Service reply lost") || (resource == "pods" && mode == "Pod reply lost") {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					writeJSON(w, http.StatusCreated, object)
				case http.MethodDelete:
					deletes++
					w.WriteHeader(http.StatusInternalServerError)
				default:
					t.Errorf("unexpected method %s", r.Method)
				}
			}))
			defer server.Close()
			requests := []borealisOperatorLaunchSiteWorkerRequest{
				{SiteID: 7, SiteName: "Original Site", WorkerGUID: schedulerK3sSiteWorkerGUID(7), ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", RemoteOpsPort: 56001, RemoteDesktopPort: 61001},
				{SiteID: 7, SiteName: "Renamed Site", WorkerGUID: newClusterUUID(), ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", RemoteOpsPort: 56002, RemoteDesktopPort: 61002},
			}
			results := make(chan error, len(requests))
			for _, request := range requests {
				go func(request borealisOperatorLaunchSiteWorkerRequest) {
					_, _, err := borealisOperatorTestClient(server).launchSiteWorker(ctx, request)
					results <- err
				}(request)
			}
			accepted := 0
			for range requests {
				if err := <-results; err == nil {
					accepted++
				}
			}
			wantAccepted, wantPods := 0, 0
			if mode == "created" {
				wantAccepted = 1
			}
			if mode == "created" || mode == "Pod reply lost" {
				wantPods = 1
			}
			mu.Lock()
			if accepted != wantAccepted || servicePosts != 2 || podPosts != wantPods || len(objects["services"]) != 1 || len(objects["pods"]) != wantPods || deletes != 0 {
				t.Errorf("resident reservation failed: accepted=%d ServicePOSTs=%d PodPOSTs=%d Services=%d Pods=%d deletes=%d", accepted, servicePosts, podPosts, len(objects["services"]), len(objects["pods"]), deletes)
			}
			service := objects["services"]["site-worker-site-7"]
			if service == nil {
				t.Error("missing immutable site reservation")
			}
			for _, pod := range objects["pods"] {
				if operatorSiteWorkerGUID(pod) != operatorSiteWorkerGUID(service) || nestedMap(nestedMap(pod, "metadata"), "annotations")["borealis.io/service-name"] != "site-worker-site-7" {
					t.Error("Pod did not retain its winning Service custody")
				}
			}
			mu.Unlock()
			// An ambiguous CREATE cannot be repaired by a new name/GUID or by
			// deleting the retained reservation. Preflight must preserve it.
			retry := requests[0]
			retry.SiteName, retry.WorkerGUID = "Third Site Name", newClusterUUID()
			if _, _, err := borealisOperatorTestClient(server).launchSiteWorker(ctx, retry); err == nil {
				t.Error("occupied or uncertain reservation accepted another launch")
			}
			mu.Lock()
			defer mu.Unlock()
			wantServicePosts := 2
			if mode == "foreign reservation conflict" {
				wantServicePosts++ // Unmanaged object remains protected by CREATE conflict.
			}
			if servicePosts != wantServicePosts || podPosts != wantPods || deletes != 0 {
				t.Fatalf("retry changed custody: ServicePOSTs=%d PodPOSTs=%d deletes=%d", servicePosts, podPosts, deletes)
			}
		})
	}
}

func TestBorealisOperatorDelayedPodCreateCannotBypassReplacement(t *testing.T) {
	setOperatorWorkerLimitsFixture(t, workerLimitsFixture())
	t.Setenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST", "borealis-engine/site-worker:sha-cccccccccccc")
	first := borealisOperatorLaunchSiteWorkerRequest{SiteID: 7, SiteName: "Original Site", WorkerGUID: schedulerK3sSiteWorkerGUID(7), ImageRef: "borealis-engine/site-worker:sha-cccccccccccc", RemoteOpsPort: 56001, RemoteDesktopPort: 61001}
	replacement := first
	replacement.SiteName, replacement.WorkerGUID = "Renamed Site", newClusterUUID()
	var mu sync.Mutex
	objects := map[string]map[string]map[string]any{"pods": {}, "services": {}}
	var serviceUIDs []string
	serviceDeletes := 0
	podPending, releasePod := make(chan struct{}), make(chan struct{})
	var release sync.Once
	var launched sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/borealis/"), "/")
		resource := parts[0]
		if resource != "pods" && resource != "services" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body map[string]any
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		if r.Method == http.MethodPost && resource == "pods" && operatorSiteWorkerGUID(body) == first.WorkerGUID {
			// Service creation succeeded, but the original Pod request has not
			// yet taken effect. Retirement cannot join this old producer.
			close(podPending)
			select {
			case <-releasePod:
			case <-ctx.Done():
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			items := make([]any, 0, len(objects[resource]))
			for _, object := range objects[resource] {
				items = append(items, object)
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost:
			metadata := nestedMap(body, "metadata")
			name := cleanText(metadata["name"])
			if _, exists := objects[resource][name]; exists {
				writeJSON(w, http.StatusConflict, map[string]any{"reason": "AlreadyExists"})
				return
			}
			uid := newClusterUUID()
			metadata["uid"] = uid
			body["metadata"] = metadata
			objects[resource][name] = body
			if resource == "services" {
				serviceUIDs = append(serviceUIDs, uid)
			}
			writeJSON(w, http.StatusCreated, body)
		case http.MethodDelete:
			if len(parts) != 2 || resource != "services" {
				t.Error("unexpected Pod deletion in pending-create fixture")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			current := objects[resource][parts[1]]
			if current == nil || nestedMap(current, "metadata")["uid"] != nestedMap(body, "preconditions")["uid"] {
				writeJSON(w, http.StatusConflict, map[string]any{"reason": "UID conflict"})
				return
			}
			delete(objects[resource], parts[1])
			serviceDeletes++
			writeJSON(w, http.StatusOK, map[string]any{"status": "Success"})
		}
	}))
	defer func() {
		release.Do(func() { close(releasePod) })
		cancel()
		launched.Wait()
		server.Close()
	}()
	firstResult := make(chan error, 1)
	launched.Add(1)
	go func() {
		defer launched.Done()
		_, _, err := borealisOperatorTestClient(server).launchSiteWorker(ctx, first)
		firstResult <- err
	}()
	select {
	case <-podPending:
	case <-ctx.Done():
		t.Fatal("original Pod CREATE never reached pending boundary")
	}
	operator := borealisOperatorTestClient(server)
	if _, status, err := operator.retireSiteWorker(ctx, borealisOperatorRetireSiteWorkerRequest{WorkerGUID: first.WorkerGUID}); err != nil || status != http.StatusOK {
		t.Fatal("original Service-only retirement failed", status, err)
	}
	if _, status, err := operator.launchSiteWorker(ctx, replacement); err != nil || status != http.StatusAccepted {
		t.Fatal("replacement failed before releasing delayed Pod CREATE", status, err)
	}
	release.Do(func() { close(releasePod) })
	if err := <-firstResult; err == nil {
		mu.Lock()
		t.Errorf("delayed original CREATE bypassed replacement reservation: Pods=%d Services=%d", len(objects["pods"]), len(objects["services"]))
		mu.Unlock()
	}
	if _, _, err := operator.retireSiteWorker(ctx, borealisOperatorRetireSiteWorkerRequest{WorkerGUID: first.WorkerGUID}); err != nil {
		t.Fatal("obsolete identity retirement failed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(objects["pods"]) != 1 || len(objects["services"]) != 1 || len(serviceUIDs) != 2 || serviceUIDs[0] == serviceUIDs[1] || serviceDeletes != 1 {
		t.Fatalf("replacement custody changed: Pods=%d Services=%d UIDs=%v ServiceDeletes=%d", len(objects["pods"]), len(objects["services"]), serviceUIDs, serviceDeletes)
	}
	name := "site-worker-site-7"
	if operatorSiteWorkerGUID(objects["pods"][name]) != replacement.WorkerGUID || operatorSiteWorkerGUID(objects["services"][name]) != replacement.WorkerGUID || nestedMap(objects["services"][name], "metadata")["uid"] != serviceUIDs[1] {
		t.Fatal("delayed original request replaced new Pod/Service owner")
	}
}
