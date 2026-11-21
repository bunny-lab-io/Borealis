package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshWorkerInventoryNetwork() clusterbootstrap.SourceNetwork {
	return sshSourceNetworkFixture(clusterSSHSourceMember{NodeUID: "44444444-4444-4444-8444-444444444444", BootID: "55555555-5555-4555-8555-555555555555", Name: "engine-original", Address: "192.168.90.10", MachineID: strings.Repeat("a", 32)}, "v1.36.3+k3s1")
}

func sshWorkerInventorySecret(t *testing.T, endpoint string, mutate func(map[string]any)) clusterbootstrap.PreparationRuntimeSecret {
	t.Helper()
	object := operatorInventoryClone(t, sshPreparationSecretFixture())
	data := clusterSSHStorageMap(object, "data")
	data["BOREALIS_OPERATOR_BASE_URL"] = base64.StdEncoding.EncodeToString([]byte(endpoint))
	data["BOREALIS_OPERATOR_SECRET"] = base64.StdEncoding.EncodeToString([]byte("synthetic-custody-secret"))
	if mutate != nil {
		mutate(object)
	}
	raw, _ := json.Marshal(object)
	secret, err := clusterbootstrap.ParsePreparationRuntimeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func sshWorkerInventoryEnvelope(t *testing.T) []byte {
	t.Helper()
	pod, service := operatorInventoryFixture(t, false)
	inventory, err := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{service}, "borealis")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"ok": true, "verb": "ListSiteWorkers", "result": map[string]any{"workers": []any{}, "source_inventory": inventory}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClusterSSHSiteWorkerInventoryClientSourceBinding(t *testing.T) {
	raw := sshWorkerInventoryEnvelope(t)
	for _, mode := range []string{"success", "secret before", "secret after", "excluded after", "url", "token", "redirect", "status", "canceled", "unknown transport"} {
		t.Run(mode, func(t *testing.T) {
			var requests, leaks atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/leak" {
					leaks.Add(1)
					return
				}
				body, _ := io.ReadAll(r.Body)
				var command borealisOperatorCommandRequest
				if r.Method != http.MethodPost || r.URL.Path != "/v1/command" || r.Header.Get(borealisOperatorTokenHeader) != goBorealisOperatorToken([]byte("synthetic-custody-secret")) ||
					json.Unmarshal(body, &command) != nil || command.Verb != "ListSiteWorkers" || len(command.Params) != 0 {
					t.Error("unexpected private command")
				}
				if mode == "redirect" {
					http.Redirect(w, r, "/leak", http.StatusFound)
					return
				}
				if mode == "status" {
					w.WriteHeader(http.StatusAccepted)
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			endpoint := "http://10.43.0.88:8088"
			secret := sshWorkerInventorySecret(t, " "+endpoint+"/ ", nil)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "10.43.0.88:8088" {
					return nil, errors.New("unexpected address")
				}
				return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
			}
			defer transport.CloseIdleConnections()
			client := &borealisOperatorClient{baseURL: endpoint, token: goBorealisOperatorToken([]byte("synthetic-custody-secret")), httpClient: &http.Client{Transport: transport}}
			var proxies atomic.Int64
			transport.Proxy = func(*http.Request) (*url.URL, error) {
				proxies.Add(1)
				return nil, errors.New("borrowed proxy must not run")
			}
			client.httpClient.Timeout = 3 * time.Second
			client.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { t.Error("borrowed redirect policy called"); return nil }
			redirect := reflect.ValueOf(client.httpClient.CheckRedirect).Pointer()
			if mode == "url" {
				client.baseURL += "/other"
			}
			if mode == "token" {
				client.token = "wrong"
			}
			if mode == "unknown transport" {
				client.httpClient.Transport = &cohortMembershipTransport{}
			}
			observations := 0
			observe := func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error) {
				observations++
				if (mode == "secret before" && observations == 1) || (mode == "secret after" && observations == 2) {
					return sshWorkerInventorySecret(t, endpoint, func(o map[string]any) { clusterSSHStorageMap(o, "metadata")["resourceVersion"] = "different" }), nil
				}
				if mode == "excluded after" && observations == 2 {
					return sshWorkerInventorySecret(t, " "+endpoint+"/ ", func(o map[string]any) { clusterSSHStorageMap(o, "data")["EXCLUDED_PRIVATE_SETTING"] = "YQ==" }), nil
				}
				return secret, nil
			}
			read, close, err := newClusterSSHSiteWorkerInventoryRead(client, secret, sshWorkerInventoryNetwork(), observe)
			if mode == "url" || mode == "token" || mode == "unknown transport" {
				if err == nil || read != nil || close != nil || requests.Load() != 0 {
					t.Fatal("invalid constructor binding accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			got, err := read(ctx)
			if mode == "success" {
				if err != nil || got == nil || len(got.Pods) != 1 || observations != 2 {
					t.Fatal("valid private read failed", err)
				}
				got.Pods[0].Metadata.Labels["borealis.io/site-id"] = "999"
				again, err := read(ctx)
				if err != nil || again.Pods[0].Metadata.Labels["borealis.io/site-id"] != "7" {
					t.Fatal("result aliases later read")
				}
				close()
				if _, err := read(ctx); err == nil {
					t.Fatal("closed reader remained usable")
				}
			} else {
				if err != clusterbootstrap.ErrPreparationConfig || got != nil {
					t.Fatal("invalid source read escaped")
				}
				before := requests.Load()
				if _, err := read(context.Background()); err == nil || requests.Load() != before {
					t.Fatal("failure not sticky")
				}
			}
			if leaks.Load() != 0 || proxies.Load() != 0 || client.httpClient.Transport != transport || client.httpClient.Timeout != 3*time.Second ||
				reflect.ValueOf(client.httpClient.CheckRedirect).Pointer() != redirect || transport.Proxy == nil {
				t.Fatal("borrowed client changed or redirected/proxied")
			}
			// Closing owned transport leaves independent clone usable with original TLS roots.
			sibling := transport.Clone()
			sibling.Proxy = nil
			defer sibling.CloseIdleConnections()
			response, err := (&http.Client{Transport: sibling}).Get(endpoint + "/leak")
			if err != nil {
				t.Fatal("sibling TLS transport damaged", err)
			}
			_ = response.Body.Close()
		})
	}
}

func TestClusterSSHSiteWorkerInventoryClientStrictProjection(t *testing.T) {
	valid := sshWorkerInventoryEnvelope(t)
	for _, mode := range []string{"success", "empty", "null", "absent", "version", "duplicate", "trailing", "case alias", "unknown field", "missing field", "null pods", "null services", "bad hash", "duplicate name", "extra inventory", "large", "bad utf8", "string version", "exponent version", "numeric target", "orphan service", "duplicate worker"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			decoder := json.NewDecoder(bytes.NewReader(valid))
			decoder.UseNumber()
			if err := decoder.Decode(&root); err != nil {
				t.Fatal(err)
			}
			result := clusterSSHStorageMap(root, "result")
			inventory := clusterSSHStorageMap(result, "source_inventory")
			pods := inventory["pods"].([]any)
			services := inventory["services"].([]any)
			pod := pods[0].(map[string]any)
			service := services[0].(map[string]any)
			switch mode {
			case "empty":
				inventory["pods"], inventory["services"] = []any{}, []any{}
			case "null":
				result["source_inventory"] = nil
			case "absent":
				delete(result, "source_inventory")
			case "version":
				inventory["version"] = 2
			case "string version":
				inventory["version"] = "1"
			case "exponent version":
				inventory["version"] = json.Number("1e0")
			case "case alias":
				inventory["Version"] = inventory["version"]
				delete(inventory, "version")
			case "unknown field":
				pod["uncertain"] = false
			case "missing field":
				delete(pod, "node_name")
			case "null pods":
				inventory["pods"] = nil
			case "null services":
				inventory["services"] = nil
			case "bad hash":
				pod["object_sha256"] = strings.Repeat("A", 64)
			case "duplicate name":
				inventory["pods"] = append(pods, pod)
			case "extra inventory":
				inventory["endpoints"] = []any{}
			case "numeric target":
				service["ports"].([]any)[0].(map[string]any)["target_port"] = 56001
			case "orphan service":
				inventory["pods"] = []any{}
			case "duplicate worker":
				copy := operatorInventoryClone(t, pod)
				clusterSSHStorageMap(copy, "metadata")["name"] = "site-worker-zzz"
				inventory["pods"] = append(pods, copy)
			}
			raw, _ := json.Marshal(root)
			switch mode {
			case "duplicate":
				raw = bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			case "trailing":
				raw = append(raw, []byte(` {}`)...)
			case "large":
				raw = append(raw, bytes.Repeat([]byte(" "), operatorSiteWorkerInventoryMaxBytes)...)
			case "bad utf8":
				raw = append(raw, 0xff)
			}
			got, err := decodeClusterSSHSiteWorkerInventory(raw)
			want := mode == "success" || mode == "empty" || mode == "orphan service" || mode == "duplicate worker"
			if (err == nil) != want || (!want && got != nil) {
				t.Fatal("projection contract mismatch", mode, err)
			}
		})
	}
}

func TestClusterSSHSiteWorkerInventoryClientActualOperator(t *testing.T) {
	pod, service := operatorInventoryFixture(t, false)
	kube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []any{}
		switch r.URL.Path {
		case "/api/v1/namespaces/borealis/pods":
			items = []any{pod}
		case "/api/v1/namespaces/borealis/services":
			items = []any{service}
		case "/apis/metrics.k8s.io/v1beta1/namespaces/borealis/pods":
			// Existing optional legacy worker summary read; no custody evidence.
		default:
			t.Errorf("unexpected native inventory read: %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, operatorInventoryResponse(r, items))
	}))
	defer kube.Close()
	operator := borealisOperatorTestClient(kube)
	operator.secret = []byte("synthetic-custody-secret")
	server := httptest.NewServer(operator.handler())
	defer server.Close()
	endpoint := "http://10.43.0.88:8088"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "10.43.0.88:8088" {
			return nil, errors.New("unexpected destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	client := &borealisOperatorClient{baseURL: endpoint, token: goBorealisOperatorToken(operator.secret), httpClient: &http.Client{Transport: transport}}
	secret := sshWorkerInventorySecret(t, endpoint, nil)
	read, close, err := newClusterSSHSiteWorkerInventoryRead(client, secret, sshWorkerInventoryNetwork(), func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error) { return secret, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	got, err := read(context.Background())
	want, projectionErr := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{service}, "borealis")
	if err != nil || projectionErr != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("actual authenticated producer incompatible with source client", err, projectionErr)
	}
}

func TestClusterSSHSiteWorkerInventoryClientURLContract(t *testing.T) {
	for _, endpoint := range []string{"http://10.43.0.88:8088", "http://operator.borealis.svc:8088", "https://10.43.0.88:8088", "http://10.43.0.88:9999", "http://10.43.0.88", "http://127.0.0.1:8088", "http://0.0.0.0:8088", "http://224.0.0.1:8088", "http://169.254.0.1:8088", "http://[::1]:8088", "http://user@10.43.0.88:8088", "http://10.43.0.88:8088/path", "http://10.43.0.88:8088?x=1", "http://10.43.0.88:8088#fragment", "http://10.43.0.88:08088", "http://8.8.8.8:8088", "http://10.44.0.88:8088", "http://10.43.0.0:8088", "http://10.43.255.255:8088"} {
		t.Run(endpoint, func(t *testing.T) {
			secret := sshWorkerInventorySecret(t, endpoint, nil)
			client := &borealisOperatorClient{baseURL: endpoint, token: goBorealisOperatorToken([]byte("synthetic-custody-secret")), httpClient: &http.Client{}}
			read, close, err := newClusterSSHSiteWorkerInventoryRead(client, secret, sshWorkerInventoryNetwork(), func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error) { return secret, nil })
			if endpoint == "http://10.43.0.88:8088" {
				if err != nil {
					t.Fatal(err)
				}
				close()
			} else if err == nil || read != nil || close != nil {
				t.Fatal("unsupported private endpoint accepted")
			}
		})
	}
}

func TestClusterSSHSiteWorkerInventoryClientObservedNetwork(t *testing.T) {
	for _, cidr := range []string{"10.43.0.0/16", "10.44.0.0/16", "10.43.0.1/16", "10.43.0.0/016", "10.43.0.0/31", "10.43.0.0/16 ", "0.0.0.0/0", "2001:db8::/64", ""} {
		t.Run(cidr, func(t *testing.T) {
			endpoint := "http://10.43.0.88:8088"
			network := sshWorkerInventoryNetwork()
			network.ServiceCIDR = cidr
			secret := sshWorkerInventorySecret(t, endpoint, nil)
			client := &borealisOperatorClient{baseURL: endpoint, token: goBorealisOperatorToken([]byte("synthetic-custody-secret")), httpClient: &http.Client{}}
			read, close, err := newClusterSSHSiteWorkerInventoryRead(client, secret, network, func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error) {
				t.Error("constructor unexpectedly performed source IO")
				return secret, nil
			})
			if cidr == "10.43.0.0/16" {
				if err != nil {
					t.Fatal(err)
				}
				close()
			} else if err == nil || read != nil || close != nil {
				t.Fatal("unbound or noncanonical source service network accepted")
			}
		})
	}
}

func TestClusterSSHSiteWorkerInventoryClientJoinedCleanup(t *testing.T) {
	for _, mode := range []string{"success", "close error", "blocked cancel", "child timeout", "short client timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			secret := sshWorkerInventorySecret(t, "http://10.43.0.88:8088", nil)
			raw := sshWorkerInventoryEnvelope(t)
			closing, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var active, reads atomic.Int64
			transport := &cohortMembershipTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
				active.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: request, Body: &cohortMembershipBody{ReadCloser: io.NopCloser(bytes.NewReader(raw)), close: func() error {
					defer active.Add(-1)
					switch mode {
					case "close error":
						return errors.New("synthetic close failure")
					case "blocked cancel":
						close(closing)
						<-release
					case "child timeout", "short client timeout":
						deadline, ok := request.Context().Deadline()
						if !ok || time.Until(deadline) > 10*time.Second || (mode == "short client timeout" && time.Until(deadline) > time.Second) {
							t.Error("effective request ceiling lost")
						}
						<-request.Context().Done()
					}
					return nil
				}}}, nil
			}}
			httpClient := &http.Client{Transport: transport, Timeout: 20 * time.Second}
			if mode == "short client timeout" {
				httpClient.Timeout = 40 * time.Millisecond
			}
			client := &borealisOperatorClient{baseURL: "http://10.43.0.88:8088", token: goBorealisOperatorToken([]byte("synthetic-custody-secret")), httpClient: httpClient}
			read, stop := clusterSSHSiteWorkerInventoryReader(client, secret, sshWorkerInventoryNetwork(), func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error) {
				reads.Add(1)
				if active.Load() != 0 {
					t.Error("Secret reread before body joined")
				}
				return secret, nil
			}, transport)
			defer func() { cancel(); once.Do(func() { close(release) }); stop() }()
			var got *operatorSiteWorkerInventory
			var err error
			if mode == "blocked cancel" {
				done := make(chan struct{})
				go func() { got, err = read(ctx); close(done) }()
				select {
				case <-closing:
				case <-ctx.Done():
					t.Fatal("Close barrier missing")
				}
				cancel()
				select {
				case <-done:
					t.Fatal("escaped blocked Close")
				case <-time.After(25 * time.Millisecond):
				}
				once.Do(func() { close(release) })
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("Close not joined")
				}
			} else {
				got, err = read(ctx)
			}
			if (err == nil) != (mode == "success") || (err != nil && got != nil) || active.Load() != 0 {
				t.Fatal("cleanup proof escaped", err)
			}
			if mode == "child timeout" || mode == "short client timeout" {
				if ctx.Err() != nil {
					t.Fatal("outer scope expired instead of child request")
				}
			}
			if err != nil {
				before := reads.Load()
				if _, err := read(context.Background()); err == nil || reads.Load() != before {
					t.Fatal("cleanup failure not sticky")
				}
			}
		})
	}
}
