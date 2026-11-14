package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Start with genuine settled preparation custody, then use the normal controller
// for every admission transition. Only external Kubernetes/native observations
// are doubled; SQL, action manifests, quorum parsing, soak and settlement execute
// their production implementations.
func TestClusterSSHPreparedAdmissionWorkflowPostgres(t *testing.T) {
	for _, mode := range []string{"pair completes", "second target fails after first activation", "endpoints fail after both activations", "authority drifts after both activations"} {
		t.Run(mode, func(t *testing.T) {
			lateFailure := mode == "endpoints fail after both activations" || mode == "authority drifts after both activations"
			f, source, targets := newSSHPreparedAuthorityFixture(t, false)
			f = recoveryTransportSQLExercise(t, f)
			for _, target := range f.targets {
				preparedAdmissionCompletionMemberCleanup(t, f, target.Binding.TargetID)
			}
			original, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			n := newPreparedMembershipNativeForProof(t, source, original, targets, "", clusterbootstrap.MembershipQuorumProofVersion)
			n.before = func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
			n.closeHook = func(ctx context.Context, _ int) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
			base := n.runner.kube.httpClient.Transport
			nodes := sshMembershipNativeFixture(source, targets, "/api/v1/nodes")
			nodesByName, deployments := map[string]map[string]any{}, map[string]map[string]any{}
			for _, item := range anySlice(nodes["items"]) {
				node := item.(map[string]any)
				metadata := clusterSSHStorageMap(node, "metadata")
				name := cleanText(metadata["name"])
				metadata["resourceVersion"] = "1"
				clusterSSHStorageMap(metadata, "labels")["borealis.io/etcd-leader"] = fmt.Sprint(name == source.Source.Members[0].Name)
				nodesByName[name] = node
			}
			for _, target := range original.receipt.Targets {
				name := target.Prejoin.Receipt.Node.Hostname
				deployments[name] = map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{
					"name": "wireguard-tunnel-" + name, "namespace": "borealis", "uid": newClusterUUID(), "resourceVersion": "1", "generation": 1,
					"labels": map[string]any{"app.kubernetes.io/name": "wireguard-tunnel", "borealis.io/engine-node": name, "borealis.io/node-workload": "true"},
				}, "spec": map[string]any{"replicas": 0}, "status": map[string]any{"observedGeneration": 1, "availableReplicas": 1, "readyReplicas": 1, "updatedReplicas": 1}}
			}
			var mu sync.Mutex
			jobs, pods := map[string]map[string]any{}, map[string]map[string]any{}
			deleted := map[string]bool{}
			var actions []string
			var operationObject map[string]any
			var activated, failed, reconciled, artifactPatches, cnpgPatches, endpointReads int
			artifactReplicas := int64(1)
			const volume = "pvc-admission-artifacts"
			const operationCollection = "/apis/engine.borealis.io/v1alpha1/namespaces/borealis/borealisclusteroperations"
			const jobCollection = "/apis/batch/v1/namespaces/borealis/jobs"
			const deploymentCollection = "/apis/apps/v1/namespaces/borealis/deployments"
			respond := func(request *http.Request, code int, value any) (*http.Response, error) {
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: request}, nil
			}
			n.runner.kube.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (response *http.Response, err error) {
				defer func() {
					if err != nil {
						t.Error("external admission boundary", request.Method, request.URL.Path, err)
					}
				}()
				if err := recoveryProgressSQLPoolAvailable(request.Context(), f); err != nil {
					return nil, err
				}
				mu.Lock()
				defer mu.Unlock()
				path, method := request.URL.Path, request.Method
				var submitted map[string]any
				if request.Body != nil {
					raw, err := io.ReadAll(request.Body)
					closeErr := request.Body.Close()
					if err != nil || closeErr != nil {
						return nil, errors.Join(err, closeErr)
					}
					request.Body = io.NopCloser(bytes.NewReader(raw))
					if err := json.Unmarshal(raw, &submitted); err != nil {
						return nil, err
					}
				}
				switch {
				case path == "/api/v1/nodes" && method == http.MethodGet:
					if request.URL.Query().Get("labelSelector") == "borealis.io/etcd-leader=true" {
						return respond(request, http.StatusOK, map[string]any{"items": []any{nodesByName[source.Source.Members[0].Name]}})
					}
					return respond(request, http.StatusOK, nodes)
				case strings.HasPrefix(path, "/api/v1/nodes/"):
					name := strings.TrimPrefix(path, "/api/v1/nodes/")
					node := nodesByName[name]
					if node == nil {
						return nil, fmt.Errorf("foreign Node %q", name)
					}
					if method == http.MethodPatch {
						metadata := clusterSSHStorageMap(node, "metadata")
						patch := nestedMap(submitted, "metadata")
						if deployments[name] == nil || patch["uid"] != metadata["uid"] || patch["resourceVersion"] != metadata["resourceVersion"] {
							return nil, errors.New("Node mutation lost exact target UID/version")
						}
						for key, value := range nestedMap(patch, "labels") {
							clusterSSHStorageMap(metadata, "labels")[key] = value
						}
						metadata["resourceVersion"] = fmt.Sprint(coerceInt64(metadata["resourceVersion"]) + 1)
					} else if method != http.MethodGet {
						return nil, errors.New("unexpected Node method")
					}
					return respond(request, http.StatusOK, node)
				case path == jobCollection && method == http.MethodPost:
					container := anySlice(nestedMap(nestedMap(nestedMap(submitted, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					args := clusterStringSlice(container["args"])
					if len(args) != 5 || args[2] != clusterbootstrap.PreparedAdmissionVerb {
						return base.RoundTrip(request)
					}
					command, err := clusterbootstrap.DecodePreparedAdmissionRequest(args[4])
					if err != nil {
						return nil, err
					}
					name := command.Receipt.Node.Hostname
					var exact bool
					for _, target := range original.receipt.Targets {
						exact = exact || reflect.DeepEqual(command.Expected, target.Prejoin.Expected) && reflect.DeepEqual(command.Receipt, target.Prejoin.Receipt)
					}
					if !exact || command.StartBefore <= time.Now().Unix() || nestedMap(nestedMap(nestedMap(submitted, "spec"), "template"), "spec")["nodeName"] != name {
						return nil, errors.New("native action lost exact settled identity or deadline")
					}
					actions = append(actions, name+":"+command.Action)
					metadata := clusterSSHStorageMap(submitted, "metadata")
					jobName := cleanText(metadata["name"])
					if jobs[jobName] != nil {
						return nil, errors.New("native action replayed deterministic Job")
					}
					metadata["uid"] = newClusterUUID()
					submitted["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					if mode == "second target fails after first activation" && activated == 1 && command.Action == "RedeployRevision" {
						failed++
						submitted["status"] = map[string]any{"failed": 1, "conditions": []any{map[string]any{"type": "Failed", "status": "True"}}}
					}
					labels := clusterSSHStorageMap(clusterSSHStorageMap(nodesByName[name], "metadata"), "labels")
					if command.Action == "ExitApplicationDrain" {
						for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
							if labels["borealis.io/"+role+"-eligible"] != "true" {
								return nil, errors.New("native activation preceded role readiness")
							}
						}
						labels["borealis.io/application-state"] = "active"
						activated++
					} else if command.Action == "EnterApplicationDrain" {
						labels["borealis.io/application-state"] = "drained"
					} else if command.Action == "ReconcilePreparedAdmissionStopped" {
						reconciled++
					}
					jobs[jobName] = submitted
					pods[cleanText(metadata["uid"])] = sshJoinSourcePod(t, submitted, newClusterUUID(), "")
					return respond(request, http.StatusCreated, submitted)
				case strings.HasPrefix(path, jobCollection+"/"):
					name := strings.TrimPrefix(path, jobCollection+"/")
					job := jobs[name]
					if job == nil {
						return base.RoundTrip(request)
					}
					if method == http.MethodDelete {
						if nestedMap(submitted, "preconditions")["uid"] != nestedMap(job, "metadata")["uid"] || submitted["propagationPolicy"] != "Foreground" {
							return nil, errors.New("failed Job cleanup lost exact UID")
						}
						deleted[name] = true
						delete(pods, cleanText(nestedMap(job, "metadata")["uid"]))
						return respond(request, http.StatusOK, map[string]any{})
					}
					if deleted[name] {
						return respond(request, http.StatusNotFound, map[string]any{"kind": "Status", "code": 404})
					}
					return respond(request, http.StatusOK, job)
				case path == "/api/v1/namespaces/borealis/pods":
					uid := strings.TrimPrefix(request.URL.Query().Get("labelSelector"), "batch.kubernetes.io/controller-uid=")
					for name, job := range jobs {
						if nestedMap(job, "metadata")["uid"] == uid {
							items := []any{}
							if !deleted[name] {
								items = append(items, pods[uid])
							}
							return respond(request, http.StatusOK, map[string]any{"items": items})
						}
					}
					return base.RoundTrip(request)
				case path == "/api/v1/namespaces/kube-system":
					return base.RoundTrip(request)
				case path == operationCollection || strings.HasPrefix(path, operationCollection+"/"+f.op.ID):
					if method == http.MethodPost {
						operationObject = submitted
						metadata := clusterSSHStorageMap(operationObject, "metadata")
						metadata["uid"], metadata["resourceVersion"], metadata["generation"] = newClusterUUID(), "1", 1
						return respond(request, http.StatusCreated, operationObject)
					}
					if operationObject == nil {
						return respond(request, http.StatusNotFound, map[string]any{"kind": "Status", "code": 404})
					}
					if method == http.MethodPatch {
						for key, value := range submitted {
							operationObject[key] = value
						}
					}
					return respond(request, http.StatusOK, operationObject)
				case path == "/apis/postgresql.cnpg.io/v1/namespaces/borealis/clusters/borealis-postgres":
					if method == http.MethodPatch {
						if coerceInt64(nestedMap(submitted, "spec")["instances"]) != 3 || nestedMap(nestedMap(nestedMap(submitted, "spec"), "postgresql"), "synchronous")["dataDurability"] != "required" {
							return nil, errors.New("database scaling lost required durability")
						}
						cnpgPatches++
					}
					return respond(request, http.StatusOK, map[string]any{"spec": map[string]any{"instances": 3}, "status": map[string]any{"instances": 3, "readyInstances": 3}})
				case path == "/api/v1/namespaces/borealis/persistentvolumeclaims/"+clusterSharedArtifactPVCName:
					return respond(request, http.StatusOK, map[string]any{"spec": map[string]any{"volumeName": volume, "accessModes": []any{"ReadWriteMany"}}, "status": map[string]any{"phase": "Bound"}})
				case path == "/api/v1/persistentvolumes/"+volume:
					return respond(request, http.StatusOK, map[string]any{"spec": map[string]any{"claimRef": map[string]any{"namespace": "borealis", "name": clusterSharedArtifactPVCName}, "accessModes": []any{"ReadWriteMany"}, "csi": map[string]any{"driver": clusterLonghornCSIDriver, "volumeHandle": volume}}})
				case path == "/apis/longhorn.io/v1beta2/namespaces/"+clusterLonghornNamespace+"/volumes/"+volume:
					if method == http.MethodPatch {
						artifactReplicas = coerceInt64(nestedMap(submitted, "spec")["numberOfReplicas"])
						artifactPatches++
					}
					return respond(request, http.StatusOK, map[string]any{"spec": map[string]any{"numberOfReplicas": artifactReplicas, "accessMode": "rwx"}, "status": map[string]any{"robustness": "healthy", "kubernetesStatus": map[string]any{"namespace": "borealis", "pvcName": clusterSharedArtifactPVCName, "pvName": volume}}})
				case path == "/apis/longhorn.io/v1beta2/namespaces/"+clusterLonghornNamespace+"/replicas":
					var replicas []any
					for name := range nodesByName {
						replicas = append(replicas, map[string]any{"spec": map[string]any{"nodeID": name, "healthyAt": "2026-11-12T00:00:00Z", "desireState": "running"}, "status": map[string]any{"currentState": "running"}})
					}
					return respond(request, http.StatusOK, map[string]any{"items": replicas})
				case path == deploymentCollection:
					for name, deployment := range deployments {
						if strings.Contains(request.URL.Query().Get("labelSelector"), "borealis.io/engine-node="+name+",") {
							return respond(request, http.StatusOK, map[string]any{"items": []any{deployment}})
						}
					}
					return nil, errors.New("deployment selection lost exact target")
				case strings.HasPrefix(path, deploymentCollection+"/wireguard-tunnel-"):
					name := strings.TrimPrefix(path, deploymentCollection+"/wireguard-tunnel-")
					deployment := deployments[name]
					if deployment == nil {
						return nil, errors.New("foreign deployment mutation")
					}
					if method == http.MethodPatch {
						replicas := coerceInt64(nestedMap(submitted, "spec")["replicas"])
						if replicas == 0 && (nestedMap(submitted, "metadata")["uid"] != nestedMap(deployment, "metadata")["uid"] || nestedMap(submitted, "metadata")["resourceVersion"] != nestedMap(deployment, "metadata")["resourceVersion"]) {
							return nil, errors.New("containment lost Deployment UID/version")
						}
						clusterSSHStorageMap(deployment, "spec")["replicas"] = replicas
					}
					return respond(request, http.StatusOK, deployment)
				case path == "/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices":
					endpointReads++
					if mode == "authority drifts after both activations" {
						f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, original.receipt.Targets[0].TargetID)
					}
					if mode == "endpoints fail after both activations" {
						return respond(request, http.StatusServiceUnavailable, map[string]any{"kind": "Status", "code": 503})
					}
					return respond(request, http.StatusOK, map[string]any{"items": []any{map[string]any{"endpoints": []any{map[string]any{"conditions": map[string]any{"ready": true}}}}}})
				}
				if method != http.MethodGet {
					return nil, fmt.Errorf("unexpected mutation %s %s", method, path)
				}
				return respond(request, http.StatusServiceUnavailable, map[string]any{"kind": "Status", "code": 503})
			})
			n.runner.soak = 2 * time.Millisecond
			n.runner.recordOperationSoak = f.c.recordOperationSoak
			f.c.runner = n.runner
			f.c.maxIdleConnections = 1
			f.c.store.db.SetMaxOpenConns(1)
			for _, expectedStep := range []string{"apply_membership", "verify_quorum", "complete"} {
				if err := f.c.runOnce(f.ctx); err != nil {
					t.Fatal("normal controller", expectedStep, err)
				}
				var state, step, reason, pointer, payload string
				var attempt int64
				if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,COALESCE(o.error_text,''),COALESCE(s.active_operation_id,''),o.payload_json,o.attempt FROM engine.cluster_operations o CROSS JOIN engine.cluster_state s WHERE o.id=$1 AND s.id=1`, f.op.ID).Scan(&state, &step, &reason, &pointer, &payload, &attempt); err != nil {
					t.Fatal(err)
				}
				if payload != original.payload || attempt != f.op.Attempt {
					t.Fatal("normal path changed original operation custody")
				}
				if mode != "pair completes" && expectedStep == "verify_quorum" {
					wantActivated, wantFailed := 1, 1
					if lateFailure {
						wantActivated, wantFailed = 2, 0
					}
					if state != "running" || step != "apply_membership" || reason != clusterSSHPreparedAdmissionBlocked || pointer != f.op.ID || activated != wantActivated || failed != wantFailed || reconciled != wantFailed || len(deleted) != wantFailed {
						t.Fatal("partial activation not durably contained", state, step, reason, activated, failed, reconciled, len(deleted), actions)
					}
					posts, actionCount, events := n.posts.Load(), len(actions), f.events(t)
					if err := f.c.runOnce(f.ctx); err != nil || n.posts.Load() != posts || len(actions) != actionCount || f.events(t) != events {
						t.Fatal("blocked controller replayed admission", err)
					}
					break
				}
				if step != expectedStep || reason != "" || expectedStep != "complete" && (state != "running" || pointer != f.op.ID) || expectedStep == "complete" && (state != "succeeded" || pointer != "") {
					t.Fatal("normal controller stopped before expected transition", expectedStep, state, step, reason, actions)
				}
			}
			for _, target := range original.receipt.Targets {
				name := target.Prejoin.Receipt.Node.Hostname
				labels := nestedMap(nestedMap(nodesByName[name], "metadata"), "labels")
				wantApplication, wantRole, wantReplicas := "active", "true", int64(1)
				if mode != "pair completes" {
					wantApplication, wantRole, wantReplicas = "drained", "false", 0
				}
				if labels["borealis.io/application-state"] != wantApplication || coerceInt64(nestedMap(deployments[name], "spec")["replicas"]) != wantReplicas {
					t.Fatal("pair application/workload state inconsistent", name, labels)
				}
				for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
					if labels["borealis.io/"+role+"-eligible"] != wantRole {
						t.Fatal("pair role state inconsistent", name, labels)
					}
				}
			}
			var expectedActions []string
			for _, target := range original.receipt.Targets {
				expectedActions = append(expectedActions, target.Prejoin.Receipt.Node.Hostname+":RunK3sProbeConformance")
			}
			for index, target := range original.receipt.Targets {
				name := target.Prejoin.Receipt.Node.Hostname
				if mode == "second target fails after first activation" && index == 1 {
					expectedActions = append(expectedActions, name+":RedeployRevision", name+":ReconcilePreparedAdmissionStopped")
					for _, contained := range original.receipt.Targets {
						expectedActions = append(expectedActions, contained.Prejoin.Receipt.Node.Hostname+":EnterApplicationDrain")
					}
					break
				}
				for _, action := range []string{"RedeployRevision", "InspectCandidateHealth", "InspectCandidateHealth", "PromoteCandidate", "InspectHealth", "InspectHealth", "InspectHealth", "InspectHealth", "ExitApplicationDrain"} {
					expectedActions = append(expectedActions, name+":"+action)
				}
			}
			if lateFailure {
				for _, target := range original.receipt.Targets {
					expectedActions = append(expectedActions, target.Prejoin.Receipt.Node.Hostname+":EnterApplicationDrain")
				}
			}
			if !reflect.DeepEqual(actions, expectedActions) {
				t.Fatal("production action order lost paired barrier, health/soak or stopped-producer containment", actions, expectedActions)
			}
			var members, joined, credentials, soakStarts, soakElapsed int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active'),(SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND state='joined' AND current_step='complete'),(SELECT count(*) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=$1),(SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='operation_soak_started'),(SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='operation_soak_elapsed')`, f.op.ID).Scan(&members, &joined, &credentials, &soakStarts, &soakElapsed); err != nil {
				t.Fatal(err)
			}
			if mode == "pair completes" {
				if members != 3 || joined != 2 || credentials != 0 || activated != 2 || failed != 0 || reconciled != 0 || n.posts.Load() != 36 || endpointReads != 2 || soakStarts != 6 || soakElapsed != 6 {
					t.Fatal("complete operation lacks full admission/soak/custody", members, joined, credentials, activated, failed, reconciled, n.posts.Load(), endpointReads, soakStarts, soakElapsed, actions)
				}
			} else {
				wantSoaks := 3
				if lateFailure {
					wantSoaks = 6
				}
				if members != 1 || joined != 0 || credentials != 2 || soakStarts != wantSoaks || soakElapsed != wantSoaks || n.posts.Load() != 24 {
					t.Fatal("partial operation released custody or claimed completion", members, joined, credentials, soakStarts, soakElapsed, n.posts.Load())
				}
			}
			if artifactPatches != 1 || artifactReplicas != 3 || cnpgPatches != 1 || n.active.Load() != 0 || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("admission lost storage scaling or HTTP/SQL cleanup", artifactPatches, artifactReplicas, cnpgPatches, n.active.Load(), f.c.store.db.Stats().InUse)
			}
		})
	}
}
