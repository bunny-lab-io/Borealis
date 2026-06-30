package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteFileRootsCallsWorkerAndReturnsEntries(t *testing.T) {
	var sawStatus bool
	var sawCall bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			sawStatus = true
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			sawCall = true
			if body["event_name"] != "file_management_request" {
				t.Fatalf("unexpected call body %#v", body)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "roots" || payload["hostname"] != "LAB-OPERATOR-01" || payload["requested_by"] != "operator" {
				t.Fatalf("unexpected file payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":            true,
					"platform":      "windows",
					"context_label": "SYSTEM",
					"current_path":  "C:\\",
					"entries": []map[string]any{
						{"name": "C", "path": "C:\\", "kind": "drive"},
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
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/files/LAB-OPERATOR-01/roots", nil)
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

func TestRemoteFileUploadConflictsNormalizesItems(t *testing.T) {
	var sawItems []any
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "upload_conflicts" {
				t.Fatalf("unexpected file payload %#v", payload)
			}
			sawItems, _ = payload["items"].([]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":          true,
					"target_path": "C:\\Temp",
					"conflicts":   []map[string]any{{"client_key": "one.txt"}},
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
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	body := []byte(`{"target_path":"C:\\Temp","items":[{"name":"..\\one.txt","client_key":"one.txt","relative_path":"folder/../one.txt","size":42},{"name":" "}]}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/files/LAB-OPERATOR-01/upload/conflicts", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(sawItems) != 1 {
		t.Fatalf("expected one normalized item, got %#v", sawItems)
	}
	item, _ := sawItems[0].(map[string]any)
	if item["name"] != "one.txt" || item["relative_path"] != "folder/one.txt" || item["size"].(float64) != 42 {
		t.Fatalf("unexpected normalized item %#v", item)
	}
}

func TestRemoteFileUploadStartsWorkerTransfer(t *testing.T) {
	var sawConflictCheck bool
	var sawUpload bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("worker request body decode failed: %v", err)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "upload_conflicts" || payload["target_path"] != "C:\\Temp" {
				t.Fatalf("unexpected conflict payload %#v", payload)
			}
			sawConflictCheck = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":          true,
					"target_path": "C:\\Temp",
					"conflicts":   []any{},
				},
			})
		case "/remote-files/transfers/upload":
			sawUpload = true
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("multipart parse failed: %v", err)
			}
			if r.FormValue("hostname") != "LAB-OPERATOR-01" || r.FormValue("device_guid") == "" || r.FormValue("target_path") != "C:\\Temp" {
				t.Fatalf("unexpected upload form host=%q guid=%q target=%q", r.FormValue("hostname"), r.FormValue("device_guid"), r.FormValue("target_path"))
			}
			if files := r.MultipartForm.File["files"]; len(files) != 1 || files[0].Filename != "one.txt" {
				t.Fatalf("unexpected upload files %#v", files)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"transfer_id":    "transfer-1",
				"direction":      "upload",
				"status":         "pending",
				"hostname":       "LAB-OPERATOR-01",
				"bytes_complete": 0,
				"bytes_total":    3,
				"item_count":     1,
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			GUID:     "00000000-0000-4000-8000-000000000123",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("target_path", "C:\\Temp"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("files", "one.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/files/LAB-OPERATOR-01/upload", &body)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawConflictCheck || !sawUpload {
		t.Fatalf("expected conflict check and upload, sawConflictCheck=%v sawUpload=%v", sawConflictCheck, sawUpload)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["transfer_id"] != "transfer-1" || payload["direction"] != "upload" {
		t.Fatalf("unexpected upload response %#v", payload)
	}
}

func TestRemoteFileTransferContentProxiesWorkerStream(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		switch r.URL.Path {
		case "/remote-files/transfers/transfer-1/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"transfer_id":    "transfer-1",
				"direction":      "download",
				"status":         "completed",
				"hostname":       "LAB-OPERATOR-01",
				"download_ready": true,
			})
		case "/remote-files/transfers/transfer-1/content":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="one.txt"`)
			_, _ = w.Write([]byte("artifact"))
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			GUID:     "00000000-0000-4000-8000-000000000123",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/files/LAB-OPERATOR-01/transfer/transfer-1/content", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != "artifact" {
		t.Fatalf("unexpected content body %q", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Disposition") != `attachment; filename="one.txt"` {
		t.Fatalf("unexpected content disposition %q", recorder.Header().Get("Content-Disposition"))
	}
}

func TestRemoteFileTransferStatusAcceptsLargeWorkerPayload(t *testing.T) {
	largeDetail := strings.Repeat("x", (2<<20)+1024)
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		switch r.URL.Path {
		case "/remote-files/transfers/transfer-large/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"transfer_id": "transfer-large",
				"direction":   "download",
				"status":      "completed",
				"hostname":    "LAB-OPERATOR-01",
				"detail":      largeDetail,
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			GUID:     "00000000-0000-4000-8000-000000000123",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/files/LAB-OPERATOR-01/transfer/transfer-large/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() <= 2<<20 {
		t.Fatalf("expected response over old 2 MiB cap, got %d bytes", recorder.Body.Len())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["transfer_id"] != "transfer-large" || payload["detail"] != largeDetail {
		t.Fatalf("unexpected transfer payload %#v", payload)
	}
}

func TestRemoteFileReadTextRequiresPath(t *testing.T) {
	store := &fakeProcessStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerRemoteFileRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/files/LAB-OPERATOR-01/text", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
