package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPreparedMembershipPostgresAuthority(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "controller lost", "attempt drift", "payload drift", "credential drift", "source row drift", "lease expired", "namespace drift", "source UID drift", "target UID drift", "member annotation drift", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, a, uids := newSSHPreparedAuthorityFixture(t, mode == "replacement")
			before, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			selected := before.receipt.Targets[0].Prejoin.Receipt.Source
			var source clusterSSHSourceMember
			for _, member := range a.Source.Members {
				if member.NodeID == selected.SourceID {
					source = member
				}
			}
			network := sshSourceNetworkFixture(source, a.K3sVersion)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.c.store.db.SetMaxOpenConns(1)
			var posts, podReads atomic.Int32
			var currentJob map[string]any
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("SQL connection held during native HTTP")
				}
				if r.URL.Path == "/api/v1/nodes" || r.URL.Path == "/api/v1/namespaces/kube-system" {
					object := sshMembershipNativeFixture(a, uids, r.URL.Path)
					if posts.Load() == 2 {
						if r.URL.Path == "/api/v1/namespaces/kube-system" && mode == "namespace drift" {
							sourceActionMap(object, "metadata")["uid"] = newClusterUUID()
						}
						if r.URL.Path == "/api/v1/nodes" {
							items := anySlice(object["items"])
							if mode == "source UID drift" {
								sourceActionMap(items[0].(map[string]any), "metadata")["uid"] = newClusterUUID()
							}
							if mode == "target UID drift" {
								sourceActionMap(items[len(items)-1].(map[string]any), "metadata")["uid"] = newClusterUUID()
							}
							if mode == "member annotation drift" {
								metadata := sourceActionMap(items[0].(map[string]any), "metadata")
								sourceActionMap(metadata, "annotations")["etcd.k3s.cattle.io/node-name"] = metadata["name"].(string) + "-87654321"
							}
						}
					}
					_ = json.NewEncoder(w).Encode(object)
					return
				}
				if r.Method == http.MethodPost {
					posts.Add(1)
					currentJob = nil
					if r.URL.Path != "/apis/batch/v1/namespaces/borealis/jobs" || json.NewDecoder(r.Body).Decode(&currentJob) != nil {
						t.Error("foreign write")
						w.WriteHeader(500)
						return
					}
					sourceActionMap(currentJob, "metadata")["uid"] = newClusterUUID()
					currentJob["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(currentJob)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
					_ = json.NewEncoder(w).Encode(currentJob)
					return
				}
				if r.URL.Path != "/api/v1/namespaces/borealis/pods" || currentJob == nil {
					t.Error("foreign read")
					w.WriteHeader(500)
					return
				}
				podReads.Add(1)
				container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(currentJob, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
				command := clusterStringSlice(container["command"])
				var pod map[string]any
				if command[1] == "source-network-client" {
					if posts.Load() != 1 {
						t.Error("network Job order")
					}
					pod = sourceActionPod(t, currentJob, network, newClusterUUID())
				} else {
					if command[1] != "source-membership-client" || posts.Load() != 2 {
						t.Error("membership Job order")
					}
					args := clusterStringSlice(container["args"])
					request, err := clusterbootstrap.DecodeMembershipObservationRequest(args[1])
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					if request.Source != selected || request.Network != network {
						t.Error("original source replaced")
					}
					pod = sshMembershipPod(t, currentJob, request, newClusterUUID())
					query := ""
					var argsSQL []any
					switch mode {
					case "controller lost":
						query = `UPDATE engine.cluster_application_leases SET holder='other' WHERE name=$1`
						argsSQL = []any{clusterControllerLeaseName}
					case "attempt drift":
						query = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`
						argsSQL = []any{f.op.ID}
					case "payload drift":
						query = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`
						argsSQL = []any{f.op.ID}
					case "credential drift":
						query = `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`
						argsSQL = []any{a.Cohort.Targets[0].Binding.TargetID}
					case "source row drift":
						query = `UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`
						argsSQL = []any{source.NodeID}
					case "lease expired":
						query = `UPDATE engine.cluster_application_leases SET expires_at=extract(epoch FROM clock_timestamp())-1 WHERE name=$1`
						argsSQL = []any{clusterControllerLeaseName}
					case "cancel":
						cancel()
					}
					if query != "" {
						if _, err := f.c.store.db.ExecContext(ctx, query, argsSQL...); err != nil {
							t.Error(err)
						}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}, "metadata": map[string]any{}})
			}))
			defer server.Close()
			f.c.runner = &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}, namespace: "borealis", controllerHolder: f.c.holder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			events := f.events(t)
			got, err := f.c.observeClusterSSHPreparedMembership(ctx, f.op)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("wrong observation", err, "posts", posts.Load(), "pods", podReads.Load())
			}
			if success {
				if posts.Load() != 2 || podReads.Load() != 2 || got.Prepared.OperationID != f.op.ID || got.Prepared.Controller != f.c.holder || got.Prepared.Attempt != f.op.Attempt || len(got.Prepared.Nodes) != len(before.receipt.Targets) || got.Native.Source != selected || len(got.Native.Membership.Members) != 3 || got.JobUID == "" || got.PodUID == "" || got.Nonce == "" {
					t.Fatal("unbound result")
				}
				after, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("observation wrote authority", err)
				}
				if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
					t.Fatal("historical receipt regained claim")
				}
			} else if !reflect.DeepEqual(got, clusterSSHPreparedMembershipObservation{}) {
				t.Fatal("partial proof escaped")
			}
			if f.events(t) != events {
				t.Fatal("observation wrote event")
			}
		})
	}
}
