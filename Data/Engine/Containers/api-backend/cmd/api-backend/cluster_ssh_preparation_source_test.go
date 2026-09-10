package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sshPreparationSecretFixture() map[string]any {
	data := map[string]string{}
	for k, v := range sshPreparationRuntimeFixture() {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	data["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("/not-transferred"))
	return map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": map[string]any{"name": "borealis-api-backend-runtime-env", "namespace": "borealis", "uid": "44444444-4444-4444-8444-444444444444", "resourceVersion": "100"}, "data": data}
}

func TestClusterSSHPreparationSourceFreshAuthorityAndPrivateReceipt(t *testing.T) {
	for _, mode := range []string{"success", "replacement", "inconsistent source networks", "authority lost", "claim drift", "source release drift", "source boot drift", "network UID", "network version", "missing network", "Secret changed during read", "Secret replaced later", "Secret revision later", "Secret content later", "secret missing setting", "secret error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, baseline := sshPreparationFixture(t)
			if mode == "replacement" || mode == "inconsistent source networks" {
				target := cohort.Targets[1]
				source.Members = append(source.Members, clusterSSHSourceMember{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: target.Report.Hostname, Address: target.Binding.Address, MachineID: target.Report.MachineID, BootID: target.Report.BootID})
				source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
				cohort.Targets = cohort.Targets[:1]
			}
			current := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authorityCalls, secretCalls := 0, 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				authorityCalls++
				raw, _ := json.Marshal(current)
				var copy clusterSSHPreparationAuthority
				_ = json.Unmarshal(raw, &copy)
				copy.Cohort.ObservedAt += int64(authorityCalls)
				if mode == "authority lost" {
					return copy, errors.New("synthetic-private-authority")
				}
				if authorityCalls > 1 {
					if mode == "claim drift" {
						copy.Lease.Generation++
					}
					if mode == "source release drift" {
						copy.Baseline.SourceSHA = strings.Repeat("b", 40)
					}
				}
				return copy, nil
			}
			secret := sshPreparationSecretFixture()
			get := func(ctx context.Context, path string, out any) error {
				var object any
				switch path {
				case "/api/v1/namespaces/kube-system":
					object = map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}}
				case "/api/v1/nodes":
					node := sshSourceKubernetesFixture(source.Members[0])
					if mode == "source boot drift" {
						node["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
					}
					items := []any{node}
					for _, member := range source.Members[1:] {
						items = append(items, sshSourceKubernetesFixture(member))
					}
					object = map[string]any{"items": items}
				case clusterSSHRuntimeSecretPath:
					secretCalls++
					if mode == "secret error" {
						return errors.New("synthetic-private-kubernetes")
					}
					if mode == "cancel" {
						cancel()
					}
					if mode == "Secret changed during read" && secretCalls > 1 {
						secret["metadata"].(map[string]any)["resourceVersion"] = "101"
					}
					if mode == "secret missing setting" {
						delete(secret["data"].(map[string]string), "BOREALIS_OPERATOR_SECRET")
					}
					object = secret
				default:
					t.Fatal("unexpected source GET")
				}
				raw, _ := json.Marshal(object)
				return json.Unmarshal(raw, out)
			}
			network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				value := clusterbootstrap.SourceNetwork{NodeUID: member.NodeUID, Hostname: member.Name, MachineID: member.MachineID, BootID: member.BootID, K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"}
				if mode == "network UID" {
					value.NodeUID = newClusterUUID()
				}
				if mode == "network version" {
					value.K3sVersion = "v1.36.4+k3s1"
				}
				if mode == "missing network" {
					value.PodCIDR = ""
				}
				if mode == "inconsistent source networks" && member.Name != source.Members[0].Name {
					value.PodCIDR = "10.44.0.0/16"
				}
				return value, nil
			}
			read := newClusterSSHPreparationSourceRead(authority, get, network)
			expected, settings, err := read(ctx)
			later := strings.HasSuffix(mode, "later")
			if mode == "success" || mode == "replacement" || later {
				if err != nil || expected.Validate() != nil || !reflect.DeepEqual(settings, sshPreparationRuntimeFixture()) || authorityCalls != 2 || secretCalls != 2 {
					t.Fatalf("source read failed: %v", err)
				}
				if mode == "Secret replaced later" {
					secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
				}
				if mode == "Secret revision later" {
					secret["metadata"].(map[string]any)["resourceVersion"] = "101"
				}
				if mode == "Secret content later" {
					secret["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("/changed"))
				}
				next, nextSettings, nextErr := read(ctx)
				if later {
					if nextErr == nil || nextSettings != nil {
						t.Fatal("changed source receipt retained")
					}
				} else if nextErr != nil || !reflect.DeepEqual(expected, next) || !reflect.DeepEqual(settings, nextSettings) {
					t.Fatal("fresh equivalent read failed")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || settings != nil {
				t.Fatal("unsafe source read accepted or private error returned")
			}
		})
	}
}

func TestClusterSSHPreparationPrivateKubernetesHTTPBoundary(t *testing.T) {
	var requests, redirected atomic.Int32
	var mode atomic.Value
	mode.Store("success")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/leak" {
			redirected.Add(1)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != clusterSSHRuntimeSecretPath || r.Header.Get("Authorization") != "Bearer synthetic-private-token" {
			t.Error("unexpected private request")
		}
		switch mode.Load().(string) {
		case "redirect":
			http.Redirect(w, r, "/leak", http.StatusFound)
		case "failure":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "synthetic-private-response")
		case "large":
			_, _ = io.WriteString(w, strings.Repeat("s", (2<<20)+1))
		default:
			_ = json.NewEncoder(w).Encode(sshPreparationSecretFixture())
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	kube := &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-private-token", httpClient: &http.Client{Transport: transport}}
	var raw json.RawMessage
	if err := kube.getClusterSSHPreparationJSON(context.Background(), clusterSSHRuntimeSecretPath, &raw); err != nil {
		t.Fatal(err)
	}
	if _, err := clusterbootstrap.ParsePreparationRuntimeSecret(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"redirect", "failure", "large"} {
		mode.Store(bad)
		raw = nil
		if err := kube.getClusterSSHPreparationJSON(context.Background(), clusterSSHRuntimeSecretPath, &raw); err != clusterbootstrap.ErrPreparationConfig || raw != nil {
			t.Fatal("private response boundary failed")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("credential-bearing redirect followed")
	}
	before := requests.Load()
	if err := kube.getClusterSSHPreparationJSON(context.Background(), "/api/v1/secrets", &raw); err == nil || requests.Load() != before {
		t.Fatal("unbounded secret path accepted")
	}
	kube.baseURL = "http://127.0.0.1"
	if err := kube.getClusterSSHPreparationJSON(context.Background(), clusterSSHRuntimeSecretPath, &raw); err == nil {
		t.Fatal("plaintext source accepted")
	}
	kube.baseURL = server.URL
	kube.httpClient = &http.Client{}
	if err := kube.getClusterSSHPreparationJSON(context.Background(), clusterSSHRuntimeSecretPath, &raw); err == nil {
		t.Fatal("untrusted source TLS accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := kube.getClusterSSHPreparationJSON(ctx, clusterSSHRuntimeSecretPath, &raw); err == nil {
		t.Fatal("cancelled private read accepted")
	}
}

func TestClusterSSHPreparationSourceWaitHonorsCancellation(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
		close(entered)
		<-release
		return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
	}
	read := newClusterSSHPreparationSourceRead(authority, func(context.Context, string, any) error { return nil }, func(context.Context, clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
		return clusterbootstrap.SourceNetwork{}, nil
	})
	go func() { defer close(done); _, _, _ = read(context.Background()) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, _, err := read(ctx); result <- err }()
	select {
	case err := <-result:
		if err != clusterbootstrap.ErrPreparationConfig {
			t.Error("cancelled reader did not fail closed")
		}
	case <-time.After(time.Second):
		t.Error("cancelled reader blocked behind another observation")
	}
	close(release)
	<-done
}
