package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the production HTTP client and each actual grace loop. A worker
// accepts the first POST before dropping its reply; a replay would be accepted
// too, just as a new unbound direct-operation request would allocate new work.
func TestHostServiceDispatchDoesNotReplayUncertainOperation(t *testing.T) {
	originalGrace, originalPoll := serviceActionSocketGrace, serviceActionSocketPoll
	serviceActionSocketGrace, serviceActionSocketPoll = time.Second, time.Millisecond
	t.Cleanup(func() { serviceActionSocketGrace, serviceActionSocketPoll = originalGrace, originalPoll })

	for _, endpoint := range []string{"service", "software", "patch"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, test := range []struct {
				name       string
				body       string
				status     int
				dropReply  bool
				wantStatus int
			}{
				{"accepted_emit_dropped_reply", "", 0, true, http.StatusServiceUnavailable},
				{"dispatch_uncertain", `{"error":"worker_dispatch_uncertain"}`, 503, false, 503},
				{"admission_uncertain", `{"error":"worker_admission_unaccounted"}`, 503, false, 503},
				{"unexpected_worker_failure", `{"error":"internal_error"}`, 500, false, 500},
				{"missing_queued", `{"emitted":false}`, 200, false, 503},
				{"missing_emitted", `{"queued":false}`, 200, false, 503},
				{"null_emitted", `{"emitted":null,"queued":false}`, 200, false, 503},
				{"coerced_queued", `{"emitted":false,"queued":"false"}`, 200, false, 503},
				{"unknown_receipt", `{"emitted":false,"queued":false,"error":"uncertain"}`, 200, false, 503},
				{"duplicate_emitted", `{"emitted":true,"emitted":false,"queued":false}`, 200, false, 502},
				{"trailing_receipt", `{"emitted":false,"queued":false}{"emitted":true}`, 200, false, 502},
				{"truncated_receipt", `{"emitted":false,"queued":false`, 200, false, 502},
				{"oversized_receipt", `{"emitted":false,"queued":false}` + strings.Repeat(" ", 1<<20), 200, false, 502},
				{"invalid_utf8", "{\"emitted\":false,\"queued\":false,\"message\":\"\xff\"}", 200, false, 502},
			} {
				t.Run(test.name, func(t *testing.T) {
					var attempts atomic.Int32
					worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer r.Body.Close()
						if _, err := io.Copy(io.Discard, r.Body); err != nil {
							t.Errorf("read accepted request: %v", err)
							return
						}
						if attempts.Add(1) > 1 {
							// Finish promptly even against the original unsafe loops.
							_, _ = io.WriteString(w, `{"emitted":true,"queued":false}`)
							return
						}
						if test.dropReply {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Errorf("drop accepted request reply: %v", err)
								return
							}
							_ = conn.Close()
							return
						}
						w.WriteHeader(test.status)
						_, _ = io.WriteString(w, test.body)
					}))
					defer worker.Close()
					response, serviceStore := runHostServiceDispatchRequest(t, endpoint, worker.URL)
					if got := attempts.Load(); got != 1 {
						t.Fatalf("accepted operation replayed: attempts=%d body=%s", got, response.Body.String())
					}
					wantStatus := test.wantStatus
					if endpoint != "service" {
						wantStatus = http.StatusConflict
					}
					if response.Code != wantStatus {
						t.Fatalf("status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
					}
					if serviceStore != nil && serviceStore.persistedServiceHost != "" {
						t.Fatal("uncertain dispatch persisted a successful service action")
					}
					var body map[string]any
					if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == nil {
						t.Fatalf("missing existing error envelope: %v %s", err, response.Body.String())
					}
				})
			}
		})
	}
}

func TestHostServiceDispatchRetriesOnlyExplicitNoDispatch(t *testing.T) {
	originalGrace, originalPoll := serviceActionSocketGrace, serviceActionSocketPoll
	serviceActionSocketGrace, serviceActionSocketPoll = time.Second, time.Millisecond
	t.Cleanup(func() { serviceActionSocketGrace, serviceActionSocketPoll = originalGrace, originalPoll })
	for _, endpoint := range []string{"service", "software", "patch"} {
		t.Run(endpoint, func(t *testing.T) {
			var attempts, admitted atomic.Int32
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					_, _ = io.WriteString(w, `{"emitted":false,"queued":false}`)
					return
				}
				admitted.Add(1)
				_, _ = io.WriteString(w, `{"emitted":true,"queued":false}`)
			}))
			defer worker.Close()
			response, _ := runHostServiceDispatchRequest(t, endpoint, worker.URL)
			if response.Code != http.StatusOK || attempts.Load() != 2 || admitted.Load() != 1 {
				t.Fatalf("explicit no-dispatch retry: status=%d attempts=%d admitted=%d body=%s", response.Code, attempts.Load(), admitted.Load(), response.Body.String())
			}
		})
	}
}

func TestHostServiceDispatchDoesNotFollowRedirects(t *testing.T) {
	for _, endpoint := range []string{"service", "software", "patch"} {
		t.Run(endpoint, func(t *testing.T) {
			for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					var accepted, targetContacts atomic.Int32
					target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetContacts.Add(1)
						t.Errorf("redirect target contacted: method=%s internal_credential=%t cookies=%d", r.Method, r.Header.Get(internalTokenHeader) != "", len(r.Cookies()))
						_, _ = io.WriteString(w, `{"emitted":true,"queued":false}`)
					}))
					defer target.Close()
					worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						if accepted.Add(1) > 1 {
							// Expose status normalization bugs without waiting out grace.
							_, _ = io.WriteString(w, `{"emitted":true,"queued":false}`)
							return
						}
						w.Header().Set("Location", target.URL)
						w.Header().Set("Set-Cookie", "worker_session=synthetic; Path=/")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, `{"emitted":false,"queued":false}`)
					}))
					defer worker.Close()
					response, _ := runHostServiceDispatchRequest(t, endpoint, worker.URL)
					if accepted.Load() != 1 || targetContacts.Load() != 0 || response.Code == http.StatusOK {
						t.Fatalf("redirect replay: accepted=%d target=%d status=%d", accepted.Load(), targetContacts.Load(), response.Code)
					}
				})
			}
		})
	}
}

func TestHostServiceDispatchRetryReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		result map[string]any
		status int
		err    map[string]any
		want   bool
	}{
		{"explicit_unused", map[string]any{"emitted": false, "queued": false}, 200, nil, true},
		{"queued", map[string]any{"emitted": false, "queued": true}, 200, nil, false},
		{"emitted", map[string]any{"emitted": true, "queued": false}, 200, nil, false},
		{"unknown_status", map[string]any{"emitted": false, "queued": false}, 0, nil, false},
		{"error_with_false_receipt", map[string]any{"emitted": false, "queued": false}, 503, map[string]any{"error": "agent_unavailable"}, false},
		{"transport_route_error", nil, 503, map[string]any{"error": "site_worker_unavailable"}, false},
		{"timeout", nil, 504, map[string]any{"error": "timeout"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := workerHostServiceDispatchRetryable(test.result, test.status, test.err); got != test.want {
				t.Fatalf("retry=%v want=%v", got, test.want)
			}
		})
	}
}

func runHostServiceDispatchRequest(t *testing.T, endpoint, workerURL string) (*httptest.ResponseRecorder, *fakeSoftwareIconStore) {
	t.Helper()
	route := routeForTestWorker(t, workerURL)
	mux := http.NewServeMux()
	var serviceStore *fakeSoftwareIconStore
	var path string
	var body []byte
	if endpoint == "service" {
		serviceStore = &fakeSoftwareIconStore{
			profile: operatorProfile{Username: "operator", Role: "Admin"},
			serviceSnapshot: deviceServicesSnapshot{
				Hostname: "LAB-OPERATOR-01", AgentID: "LAB-OPERATOR-01_SYSTEM", Route: route,
				Services: []map[string]any{{"service_id": "spooler", "name": "Spooler", "status_code": "running"}},
			},
		}
		registerSoftwareRoutes(mux, softwareIconTestAuth(serviceStore), http.NotFoundHandler())
		path = "/api/device/services/LAB-OPERATOR-01/action"
		body = []byte(`{"service_name":"Spooler","action":"restart"}`)
	} else {
		store := &fakeProcessStore{profile: operatorProfile{Username: "operator", Role: "Admin"},
			snapshot: deviceProcessContext{Hostname: "LAB-OPERATOR-01", AgentID: "LAB-OPERATOR-01_SYSTEM", Route: route}}
		if endpoint == "software" {
			registerSoftwareRoutes(mux, processTestAuth(store), http.NotFoundHandler())
			path = "/api/device/software/LAB-OPERATOR-01/refresh"
		} else {
			registerPatchRoutes(mux, processTestAuth(store), http.NotFoundHandler())
			path = "/api/device/patches/LAB-OPERATOR-01/refresh"
		}
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response, serviceStore
}
