package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sshClaimedCapacityFixture(t *testing.T, replacement bool, nodeMode string) (*sshWaitingCapacityFixture, clusterSSHPreparationAuthority, clusterSSHPreparationSnapshot, clusterSSHPreparationReadinessReceipt) {
	t.Helper()
	f := newSSHWaitingCapacityFixture(t, replacement, nodeMode)
	gate, approval := f.readers()
	digest, err := gate(context.Background(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := approval(context.Background(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	r := clusterSSHPreparationReadiness(f.plan, [6]string{strings.Repeat("a", 64), clean.Digest, digest, strings.Repeat("b", 64), clean.Capacity.ConfigurationSHA256, strings.Repeat("c", 64)})
	r.Version, r.Targets, r.Capacity = 3, clean.Targets, clean.Capacity
	a := f.storage.a
	target := a.Cohort.Targets[0]
	a.Lease = clusterSSHTargetLease{TargetID: target.Binding.TargetID, OperationID: a.Cohort.OperationID, Holder: newClusterUUID(), Generation: target.Generation + 1,
		Step: "stage_source", ControllerHolder: a.Cohort.ControllerHolder, OperationAttempt: a.Cohort.Attempt, OperationStep: clusterSSHPreparationOperationStep, OperationKind: "ssh_onboarding"}
	a.payload = marshalClusterJSON(map[string]any{"ssh_preparation_readiness": r, "ssh_preparation_authorization": f.snapshot.work.Authorization})
	a.PayloadSHA256, _ = clusterSSHPreparationDigest(a.payload)
	member := a.Source.Members[0]
	a.IdentitySource = &engineidentity.Binding{ClusterID: a.Source.ClusterID, SourceID: member.NodeID, SourceUID: member.NodeUID}
	expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.storage.objects[clusterSSHRuntimeSecretPath])
	secret, err := clusterbootstrap.ParsePreparationRuntimeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := observeClusterSSHStorage(context.Background(), a.Source, f.get)
	if err != nil {
		t.Fatal(err)
	}
	observation := storage.Requirements.observation
	storage.Requirements.observation = ""
	value := clusterSSHPreparationSnapshot{Expected: expected, Settings: secret.Settings(), Observation: secret.ObservationSHA256(), Storage: clusterSSHStorageSnapshot{storage.Requirements, observation}, DesiredWorkerSites: slices.Clone(f.sites)}
	for _, member := range a.Source.Members {
		value.Sources = append(value.Sources, sshSourceNetworkFixture(member, a.K3sVersion))
	}
	return f, a, value, r
}

func TestClusterSSHPreparationCapacityClaimedRechecks(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "source Secret", "source storage", "worker census", "native CPU", "own override", "own Node replacement", "denied target", "legacy CPU receipt"} {
		t.Run(mode, func(t *testing.T) {
			f, a, value, receipt := sshClaimedCapacityFixture(t, mode == "replacement", "override")
			get := f.get
			switch mode {
			case "source Secret":
				value.Observation = strings.Repeat("f", 64)
			case "source storage":
				value.Storage.Observation = strings.Repeat("f", 64)
			case "worker census":
				value.DesiredWorkerSites = []int64{9}
			case "native CPU":
				cpu := *value.Expected.TargetCPU
				cpu.MemoryKiB++
				value.Expected.TargetCPU = &cpu
			case "own override":
				clusterSSHStorageMap(f.storage.objects[clusterSSHInstanceNodePrefix+a.Cohort.Targets[0].Report.Hostname], "spec")["instanceManagerCPURequest"] = 1001
			case "own Node replacement":
				clusterSSHStorageMap(f.storage.objects[clusterSSHInstanceNodePrefix+a.Cohort.Targets[0].Report.Hostname], "metadata")["uid"] = newClusterUUID()
			case "denied target":
				get = func(ctx context.Context, path string, out any) error {
					if clusterSSHPreparationLonghornNodePath(path) {
						return errClusterConflict
					}
					return f.get(ctx, path, out)
				}
			case "legacy CPU receipt":
				receipt.Version, receipt.Capacity = 2, nil
				a.payload = marshalClusterJSON(map[string]any{"ssh_preparation_readiness": receipt, "ssh_preparation_authorization": f.snapshot.work.Authorization})
			}
			digest, targets, err := recheckClusterSSHPreparationCapacity(context.Background(), a, value, false, get)
			valid := mode == "pair" || mode == "replacement"
			if (err == nil) != valid || !valid && (digest != "" || targets != nil) {
				t.Fatalf("claimed capacity: %v", err)
			}
			if valid && (digest != receipt.Checks[2].SHA256 || !clusterSSHPreparationCapacityCurrentValid(*receipt.Capacity, targets, false)) {
				t.Fatal("claimed capacity lost approved binding")
			}
		})
	}
}

func TestClusterSSHPreparationCapacityJoiningCreation(t *testing.T) {
	for _, mode := range []string{"approved creation", "before joining", "missing registration", "wrong boot", "active target", "missing identity source", "override drift", "present UID replacement"} {
		t.Run(mode, func(t *testing.T) {
			nodeMode := "absent"
			if mode == "present UID replacement" {
				nodeMode = "override"
			}
			f, a, value, receipt := sshClaimedCapacityFixture(t, false, nodeMode)
			list := f.storage.objects["/api/v1/nodes"]
			list["apiVersion"], list["kind"] = "v1", "NodeList"
			for _, raw := range list["items"].([]any) {
				node := raw.(map[string]any)
				node["apiVersion"], node["kind"] = "v1", "Node"
				clusterSSHStorageMap(node, "metadata")["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
			}
			target := a.Cohort.Targets[0]
			node := sshJoinedNodeFixture(target, a.K3sVersion, newClusterUUID())
			if mode == "wrong boot" {
				clusterSSHStorageMap(clusterSSHStorageMap(node, "status"), "nodeInfo")["bootID"] = newClusterUUID()
			}
			if mode == "active target" {
				clusterSSHStorageMap(clusterSSHStorageMap(node, "metadata"), "labels")["borealis.io/application-state"] = "active"
			}
			if mode != "missing registration" {
				list["items"] = append(list["items"].([]any), node)
			}
			longhorn := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", target.Report.Hostname)
			override := 0
			if mode == "override drift" {
				override = 1
			}
			if mode == "present UID replacement" {
				override = 1000
			}
			longhorn["spec"] = map[string]any{"instanceManagerCPURequest": override}
			f.storage.objects[clusterSSHInstanceNodePrefix+target.Report.Hostname] = longhorn
			if mode == "missing identity source" {
				a.IdentitySource = nil
			}
			digest, observed, err := recheckClusterSSHPreparationCapacity(context.Background(), a, value, mode != "before joining", f.get)
			if (err == nil) != (mode == "approved creation") {
				t.Fatalf("joining target policy: %v", err)
			}
			if err == nil {
				if digest != receipt.Checks[2].SHA256 || !observed[0].Present || observed[0].OverrideMilli != 0 || !clusterSSHPreparationCapacityCurrentValid(*receipt.Capacity, observed, true) {
					t.Fatal("created Node lost bounded original policy")
				}
				changed := slices.Clone(observed)
				changed[0].NodeUID = newClusterUUID()
				if clusterSSHPreparationCapacityTransition(observed, changed, true) {
					t.Fatal("created Longhorn Node replacement accepted")
				}
				changed = slices.Clone(observed)
				changed[0] = receipt.Capacity.Targets[0].Capacity.LonghornCPU
				if clusterSSHPreparationCapacityTransition(observed, changed, true) {
					t.Fatal("created Longhorn Node disappearance accepted")
				}
			}
		})
	}
}

func TestClusterSSHPreparationCapacityClaimedNativeBroker(t *testing.T) {
	for _, mode := range []string{"fresh", "own override drift", "target Close", "Secret Close", "Job Close", "cancel during Close", "blocked Close"} {
		t.Run(mode, func(t *testing.T) {
			f, authority, _, receipt := sshClaimedCapacityFixture(t, false, "override")
			runner, posts := sshPreparationConfigurationNativeFixture(t, f.snapshot.work)
			originalClient, originalKube := runner.kube.httpClient, runner.kube
			underlying := originalClient.Transport
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var targetReads atomic.Int64
			var fired atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			transport := &cohortMembershipTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
				path := request.URL.RequestURI()
				var response *http.Response
				var err error
				if request.Method == http.MethodGet && f.storage.objects[path] != nil {
					var raw json.RawMessage
					if err = f.get(request.Context(), path, &raw); err != nil {
						return nil, err
					}
					if clusterSSHPreparationLonghornNodePath(path) && strings.HasSuffix(path, authority.Cohort.Targets[0].Report.Hostname) {
						n := targetReads.Add(1)
						if mode == "own override drift" && n > 1 {
							var node map[string]any
							_ = json.Unmarshal(raw, &node)
							clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = 1001
							raw, _ = json.Marshal(node)
						}
					}
					response = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}
				} else {
					response, err = underlying.RoundTrip(request)
				}
				if err != nil {
					return response, err
				}
				selected := mode == "Secret Close" && path == clusterSSHRuntimeSecretPath || mode == "Job Close" && request.Method == http.MethodPost ||
					(mode == "target Close" || mode == "cancel during Close" || mode == "blocked Close") && path == clusterSSHInstanceNodePrefix+authority.Cohort.Targets[0].Report.Hostname
				if selected && fired.CompareAndSwap(false, true) {
					response.Body = &cohortMembershipBody{ReadCloser: response.Body, close: func() error {
						if mode == "cancel during Close" {
							cancel()
							return nil
						}
						if mode == "blocked Close" {
							close(entered)
							<-release
							return nil
						}
						return errClusterConflict
					}}
				}
				return response, nil
			}}
			originalClient.Transport = transport
			type result struct {
				value clusterSSHPreparationSnapshot
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := runner.readSSHStoragePreparationSnapshot(ctx, func(ctx context.Context) (clusterSSHPreparationAuthority, error) { return authority, ctx.Err() }, func(ctx context.Context, window int64) ([]int64, error) { return slices.Clone(f.sites), ctx.Err() })
				done <- result{value, err}
			}()
			if mode == "blocked Close" {
				select {
				case <-entered:
				case value := <-done:
					t.Fatalf("never reached target cleanup: %v", value.err)
				case <-time.After(5 * time.Second):
					t.Fatal("target Close not reached")
				}
				select {
				case <-done:
					t.Fatal("capacity proof escaped blocked Close")
				default:
				}
				cancel()
				close(release)
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("claimed capacity reader did not join")
			}
			if (got.err == nil) != (mode == "fresh") {
				t.Fatalf("native claimed capacity: %v", got.err)
			}
			if got.err == nil && (got.value.CapacitySHA256 != receipt.Checks[2].SHA256 || !clusterSSHPreparationCapacityCurrentValid(*receipt.Capacity, got.value.CapacityTargets, false) || posts.Load() != 2 || targetReads.Load() != 2) {
				t.Fatal("claimed native reader lost complete capacity rechecks")
			}
			if got.err != nil && (got.value.CapacitySHA256 != "" || got.value.CapacityTargets != nil) {
				t.Fatal("failed claimed proof leaked projection")
			}
			if runner.kube != originalKube || runner.kube.httpClient != originalClient || originalClient.Transport != transport || transport.idleCalls.Load() != 0 {
				t.Fatal("claimed proof changed shared client/transport ownership")
			}
		})
	}
}

func TestClusterSSHPreparationCapacityClientBindingAndCleanup(t *testing.T) {
	for _, mode := range []string{"fresh", "missing digest", "missing target policy", "changed policy", "Node replacement", "response Close", "follower Close", "late Close"} {
		t.Run(mode, func(t *testing.T) {
			f, a, value, receipt := sshClaimedCapacityFixture(t, false, "override")
			var err error
			value.CapacitySHA256, value.CapacityTargets, err = recheckClusterSSHPreparationCapacity(context.Background(), a, value, false, f.get)
			if err != nil {
				t.Fatal(err)
			}
			client := sshBrokerClient(t, "http://first.invalid", "http://second.invalid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "late Close" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
				defer stop()
			}
			requests, authorities := 0, 0
			client.httpClient.Transport = bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
				requests++
				wire, err := io.ReadAll(request.Body)
				if err != nil {
					return nil, err
				}
				var input clusterSSHSourceBrokerRequest
				if openClusterSSHSourceBroker(client.requestCipher, wire, &input) != nil {
					return nil, errClusterConflict
				}
				copy := value
				copy.CapacityTargets = slices.Clone(value.CapacityTargets)
				switch mode {
				case "missing digest":
					copy.CapacitySHA256 = ""
				case "missing target policy":
					copy.CapacityTargets = nil
				case "changed policy":
					copy.CapacityTargets[0].OverrideMilli++
				case "Node replacement":
					copy.CapacityTargets[0].NodeUID = newClusterUUID()
				}
				response := clusterSSHSourceBrokerResponse{Version: 55, ID: input.ID, Status: "ok", Snapshot: &copy}
				if mode == "follower Close" {
					response.Status, response.Snapshot = "not_owner", nil
				}
				wire, err = sealClusterSSHSourceBroker(client.responseCipher, response)
				if err != nil {
					return nil, err
				}
				body := &cohortMembershipBody{ReadCloser: io.NopCloser(strings.NewReader(string(wire))), close: func() error {
					if mode == "response Close" || mode == "follower Close" {
						return errClusterConflict
					}
					if mode == "late Close" {
						<-ctx.Done()
					}
					return nil
				}}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{clusterSSHSourceBrokerMedia}}, Body: body}, nil
			})
			sealed := sealedClusterSSHCredentials{binding: a.Cohort.Targets[0].Binding, generation: "private-test-generation", ciphertext: aegisEnvelopePrefix + "private-test-credential"}
			read := client.snapshotRead(func(ctx context.Context) (clusterSSHPreparationAuthority, error) { authorities++; return a, ctx.Err() }, a.Lease, a.Baseline, sealed)
			got, err := read(ctx)
			if (err == nil) != (mode == "fresh") {
				t.Fatalf("capacity client: %v", err)
			}
			if mode != "fresh" {
				if got.CapacitySHA256 != "" || got.CapacityTargets != nil || requests != 1 || authorities != 1 {
					t.Fatal("invalid capacity/cleanup advanced authority or peer", requests, authorities)
				}
				return
			}
			if got.CapacitySHA256 != receipt.Checks[2].SHA256 {
				t.Fatal("capacity receipt lost")
			}
			got.CapacityTargets[0].NodeUID = newClusterUUID()
			if _, err := read(ctx); err != nil {
				t.Fatal("caller mutated retained target policy", err)
			}
		})
	}
}
