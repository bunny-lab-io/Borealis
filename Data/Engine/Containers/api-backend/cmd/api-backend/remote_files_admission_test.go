package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteFilePrivateDispatchDoesNotFollowRedirects(t *testing.T) {
	for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, kind := range []string{"upload", "download", "status", "content", "rpc"} {
			t.Run(fmt.Sprintf("%s/%d", kind, code), func(t *testing.T) {
				var accepted, redirected atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					redirected.Add(1)
					_, _ = w.Write([]byte(`{"transfer_id":"replayed"}`))
				}))
				defer target.Close()
				worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					accepted.Add(1)
					if r.Header.Get(internalTokenHeader) == "" {
						t.Error("missing private caller authentication")
					}
					http.Redirect(w, r, target.URL, code)
				}))
				defer worker.Close()
				auth := processTestAuth(&fakeProcessStore{})
				route := routeForTestWorker(t, worker.URL)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var status int
				var failure map[string]any
				switch kind {
				case "upload":
					_, status, failure = remoteFilePostWorkerUpload(ctx, auth, route, remoteFileUploadWorkerRequest{Hostname: "LAB", DeviceGUID: "11111111-2222-4333-8444-555555555555", AgentID: "LAB_SYSTEM"})
				case "download":
					_, status, failure = remoteFilePostWorkerJSON(ctx, auth, route, "/remote-files/transfers/download", map[string]any{"hostname": "LAB"}, time.Second)
				case "rpc":
					_, status, failure = callWorkerHostServiceEvent(ctx, auth, route, map[string]any{"hostname": "LAB", "event_name": "file_management_request", "payload": map[string]any{"action": "delete"}}, time.Second)
				case "status":
					_, status, failure = remoteFileGetWorkerJSON(ctx, auth, route, "/remote-files/transfers/id/status", time.Second)
				case "content":
					response, code, err := remoteFileWorkerContentResponse(ctx, auth, route, "/remote-files/transfers/id/content", time.Second)
					if response != nil {
						_ = response.Body.Close()
					}
					status, failure = code, err
				}
				if accepted.Load() != 1 || redirected.Load() != 0 || status != http.StatusServiceUnavailable || failure == nil {
					t.Fatalf("uncertain request replayed: original=%d redirect=%d status=%d failure=%v", accepted.Load(), redirected.Load(), status, failure)
				}
			})
		}
	}
}

func TestRemoteFileUploadPrivateContextPreservesUTF8Identity(t *testing.T) {
	hostname, agentID := "LAB-\u00c4", "LAB-\u00c4_aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee_SYSTEM"
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for header, value := range map[string]string{"X-Borealis-Upload-Hostname-B64": hostname, "X-Borealis-Upload-Agent-Id-B64": agentID} {
			raw := r.Header.Get(header)
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil || string(decoded) != value || base64.RawURLEncoding.EncodeToString(decoded) != raw {
				t.Errorf("identity header %s did not preserve UTF-8: %q %v", header, decoded, err)
			}
		}
		if r.Header.Get("X-Borealis-Upload-Device-Guid") != "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE" {
			t.Error("device GUID was not canonical")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("hostname") != hostname || r.FormValue("agent_id") != agentID || r.FormValue("device_guid") != r.Header.Get("X-Borealis-Upload-Device-Guid") {
			t.Error("multipart identity differs from pre-body context")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"transfer_id":"accepted"}`))
	}))
	defer worker.Close()
	_, status, failure := remoteFilePostWorkerUpload(context.Background(), processTestAuth(&fakeProcessStore{}), routeForTestWorker(t, worker.URL), remoteFileUploadWorkerRequest{Hostname: hostname, DeviceGUID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", AgentID: agentID})
	if status != http.StatusAccepted || failure != nil {
		t.Fatalf("upload context failed: %d %v", status, failure)
	}
}
