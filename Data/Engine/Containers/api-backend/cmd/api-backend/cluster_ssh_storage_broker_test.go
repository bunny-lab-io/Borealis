package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sshStorageSnapshotFixture(t *testing.T, a clusterSSHPreparationAuthority) clusterSSHStorageSnapshot {
	t.Helper()
	f := newSSHStorageFixtureForAuthority(t, a)
	value, err := observeClusterSSHStorage(context.Background(), a.Source, f.get)
	if err != nil {
		t.Fatal(err)
	}
	r := value.Requirements
	observation := r.observation
	r.observation = ""
	snapshot := clusterSSHStorageSnapshot{Requirements: r, Observation: observation}
	if !validClusterSSHStorageSnapshot(snapshot, a.Source) {
		t.Fatal("invalid storage fixture")
	}
	return snapshot
}

func TestClusterSSHStorageBrokerProjection(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		f := newSSHStorageFixture(t, replacement)
		value := sshStorageSnapshotFixture(t, f.a)
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded clusterSSHStorageSnapshot
		if json.Unmarshal(raw, &decoded) != nil || !reflect.DeepEqual(decoded, value) || !validClusterSSHStorageSnapshot(decoded, f.a.Source) {
			t.Fatal("projection round trip failed")
		}
		cases := map[string]func(*clusterSSHStorageSnapshot){
			"missing receipt":             func(v *clusterSSHStorageSnapshot) { v.Observation = "" },
			"bad receipt":                 func(v *clusterSSHStorageSnapshot) { v.Observation = strings.Repeat("z", 64) },
			"private provenance imported": func(v *clusterSSHStorageSnapshot) { v.Requirements.observation = v.Observation },
			"no artifact capacity":        func(v *clusterSSHStorageSnapshot) { v.Requirements.ArtifactReplicaBytes = 0 },
			"PG overflow":                 func(v *clusterSSHStorageSnapshot) { v.Requirements.PostgresInstanceBytes = uint64(math.MaxInt64) + 1 },
			"wrong instances":             func(v *clusterSSHStorageSnapshot) { v.Requirements.ConfiguredPostgresInstances = 2 },
			"no volumes":                  func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes = nil },
			"unknown role":                func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].Role = "free" },
			"artifact renamed":            func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].Claim = "aaa" },
			"artifact size":               func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].Bytes++ },
			"artifact location":           func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].Node = f.a.Source.Members[0].Name },
			"artifact detached":           func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].State = "detached" },
			"PG location":                 func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].Node = "foreign" },
			"PG capacity":                 func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].Bytes++ },
			"PG nonlocal":                 func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].DataLocality = "disabled" },
			"PG unready":                  func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].Robustness = "degraded" },
			"wrong class":                 func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].StorageClass = "" },
			"claimed free":                func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[len(v.Requirements.Volumes)-1].Bytes = 0 },
			"zero UID": func(v *clusterSSHStorageSnapshot) {
				v.Requirements.Volumes[0].PVUID = "00000000-0000-0000-0000-000000000000"
			},
			"bad UID":      func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[0].ClaimUID = "other" },
			"duplicate PV": func(v *clusterSSHStorageSnapshot) { v.Requirements.Volumes[1].PV = v.Requirements.Volumes[0].PV },
			"duplicate volume UID": func(v *clusterSSHStorageSnapshot) {
				v.Requirements.Volumes[1].VolumeUID = v.Requirements.Volumes[0].VolumeUID
			},
			"reordered": func(v *clusterSSHStorageSnapshot) { slices.Reverse(v.Requirements.Volumes) },
			"duplicated": func(v *clusterSSHStorageSnapshot) {
				v.Requirements.Volumes = append(v.Requirements.Volumes, v.Requirements.Volumes[0])
			},
		}
		for name, change := range cases {
			t.Run(name, func(t *testing.T) {
				next := value
				next.Requirements.Volumes = slices.Clone(value.Requirements.Volumes)
				change(&next)
				if validClusterSSHStorageSnapshot(next, f.a.Source) {
					t.Fatal("invalid storage accepted")
				}
			})
		}
		if replacement {
			changed := value
			changed.Requirements.Volumes = slices.Clone(value.Requirements.Volumes)
			changed.Requirements.Volumes[2].StorageClass = "other"
			if validClusterSSHStorageSnapshot(changed, f.a.Source) {
				t.Fatal("inconsistent PG class accepted")
			}
		}
		// The encrypted decoder also requires all exact canonical field names/types.
		requestCipher, _ := clusterSSHSourceBrokerCipher(sshBrokerTestSecret, "response")
		for _, bad := range []string{
			strings.Replace(string(raw), `"observation_sha256":`, `"Observation_sha256":`, 1),
			strings.Replace(string(raw), `"artifact_replica_bytes":4294967296`, `"artifact_replica_bytes":4294967296,"artifact_replica_bytes":4294967296`, 1),
			strings.Replace(string(raw), `"replicas":1`, `"replicas":1.0`, 1),
		} {
			wire := requestCipher.Seal(nil, nil, []byte(bad), []byte(clusterSSHSourceBrokerPath))
			var out clusterSSHStorageSnapshot
			if openClusterSSHSourceBroker(requestCipher, wire, &out) != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("ambiguous encrypted storage accepted")
			}
		}
	}
}

func TestClusterSSHStorageBrokerControllerComposition(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "storage changed during Job", "storage changed during Secret", "cancel during storage", "changed receipt on reread"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHStorageFixture(t, mode == "replacement")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var job map[string]any
			jobs := 0
			kubernetes := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.mu.Lock()
				defer f.mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer synthetic-private-token" {
					t.Error("missing Kubernetes authentication")
				}
				if r.Method == http.MethodGet {
					path := r.URL.RequestURI()
					if object, ok := f.objects[path]; ok {
						f.calls[path]++
						if mode == "cancel during storage" && path == clusterSSHStoragePostgresPath {
							cancel()
						}
						_ = json.NewEncoder(w).Encode(object)
						return
					}
					if path == clusterSSHRuntimeSecretPath {
						if mode == "storage changed during Secret" {
							clusterSSHStorageMap(f.volume(0), "metadata")["resourceVersion"] = "2"
						}
						_ = json.NewEncoder(w).Encode(sshPreparationSecretFixture())
						return
					}
				}
				if r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
					jobs++
					job = nil
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("invalid source Job")
						w.WriteHeader(500)
						return
					}
					clusterSSHStorageMap(job, "metadata")["uid"] = newClusterUUID()
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					if mode == "storage changed during Job" {
						clusterSSHStorageMap(f.volume(1), "status")["currentNodeID"] = "foreign"
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method == http.MethodGet && job != nil {
					if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
						_ = json.NewEncoder(w).Encode(job)
						return
					}
					if r.URL.Path == "/api/v1/namespaces/borealis/pods" {
						node := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(job, "spec"), "template"), "spec")["nodeName"]
						for _, member := range f.a.Source.Members {
							if member.Name == node {
								network := sshSourceNetworkFixture(member, f.a.K3sVersion)
								// The source action requires the same Pod identity across rereads.
								podUID := "77777777-7777-4777-8777-777777777777"
								_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sourceActionPod(t, job, network, podUID)}})
								return
							}
						}
					}
				}
				t.Error("unexpected Kubernetes request")
				w.WriteHeader(500)
			}))
			defer kubernetes.Close()
			runner := &kubernetesClusterStepRunner{namespace: "borealis", controllerHolder: f.a.Lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond,
				kube: &kubernetesAPIClient{baseURL: kubernetes.URL, token: "synthetic-private-token", httpClient: kubernetes.Client()}}
			broker := newClusterSSHSourceBroker(nil, nil, f.a.Lease.ControllerHolder, sshBrokerTestSecret)
			broker.read = func(ctx context.Context, _ clusterSSHSourceBrokerRequest) (clusterSSHPreparationSnapshot, error) {
				return runner.readSSHStoragePreparationSnapshot(ctx, f.authority)
			}
			server := httptest.NewServer(http.HandlerFunc(broker.handle))
			defer server.Close()
			client := sshBrokerClient(t, server.URL)
			client.httpClient.Timeout = 8 * time.Second
			sealed := sealedClusterSSHCredentials{binding: f.a.Cohort.Targets[0].Binding, generation: "synthetic-generation", ciphertext: aegisEnvelopePrefix + "synthetic-ciphertext"}
			read := client.snapshotRead(f.authority, f.a.Lease, f.a.Baseline, sealed)
			value, err := read(ctx)
			good := mode == "expansion" || mode == "replacement" || mode == "changed receipt on reread"
			if good {
				if err != nil || !validClusterSSHStorageSnapshot(value.Storage, f.a.Source) {
					t.Fatalf("source/storage broker composition failed %v", err)
				}
				if f.calls[clusterSSHStoragePostgresPath] != 3 || jobs != 2*len(f.a.Source.Members) {
					t.Fatal("fresh full source/storage observations missing")
				}
				if mode == "changed receipt on reread" {
					f.mu.Lock()
					clusterSSHStorageMap(f.volume(0), "metadata")["resourceVersion"] = "2"
					f.mu.Unlock()
				}
				next, err := read(ctx)
				if mode == "changed receipt on reread" {
					if err == nil || next.Settings != nil {
						t.Fatal("changed storage receipt returned")
					}
				} else if err != nil || !reflect.DeepEqual(value.Storage, next.Storage) || jobs != 4*len(f.a.Source.Members) {
					t.Fatal("fresh equivalent broker read failed")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || value.Settings != nil || value.Storage.Requirements.Volumes != nil {
				t.Fatal("unsafe storage response consumed")
			}
		})
	}
}
