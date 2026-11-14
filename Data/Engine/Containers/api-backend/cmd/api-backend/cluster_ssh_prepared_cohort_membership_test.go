package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshCohortMembershipResultFixture(request clusterbootstrap.MembershipObservationRequest) clusterbootstrap.MembershipObservation {
	result := sshMembershipResultFixture(request)
	observer := *request.Observer
	result.Observer = &observer
	if request.RequireListenerProof {
		result.ListenerProofVersion = clusterbootstrap.MembershipListenerProofVersion
	}
	if request.RequiredListenerProofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion {
		result.ListenerProofVersion = clusterbootstrap.MembershipLocalEtcdProofVersion
	}
	for _, expected := range request.Nodes {
		if expected.Node.NodeUID == request.ObserverUID() {
			result.LocalNode = expected.Node
			for _, member := range result.Membership.Members {
				if member.Address == expected.Node.Address {
					result.Membership.LocalID = member.ID
				}
			}
		}
	}
	return result
}

func sshCohortMembershipPod(t *testing.T, job map[string]any, request clusterbootstrap.MembershipObservationRequest, result clusterbootstrap.MembershipObservation, podUID string) map[string]any {
	t.Helper()
	pod := sourceActionPod(t, job, request.Network, podUID)
	container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
	args := clusterStringSlice(container["args"])
	encoded, err := clusterbootstrap.EncodeMembershipObservationResult(result, request)
	if err != nil {
		t.Error("invalid observer result fixture", err)
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": encoded}})
	receipt, err := clusterbootstrap.NewMembershipObservationReceipt(raw, request, args[0], sourceActionMap(job, "metadata")["uid"].(string), podUID)
	if err != nil {
		t.Error("invalid observer receipt fixture", err)
		return nil
	}
	status := anySlice(sourceActionMap(pod, "status")["containerStatuses"])[0].(map[string]any)
	sourceActionMap(sourceActionMap(status, "state"), "terminated")["message"] = string(receipt)
	return pod
}

// Simulate an older installed daemon despite a current immutable Job client.
// Retain the new request digest and all identities: only proof/version differs.
func stripCohortMembershipListenerProof(t *testing.T, pod map[string]any) {
	replaceCohortMembershipProof(t, pod, 0, 0)
}

func replaceCohortMembershipProof(t *testing.T, pod map[string]any, proof uint8, version int) {
	t.Helper()
	status := anySlice(sourceActionMap(pod, "status")["containerStatuses"])[0].(map[string]any)
	terminated := sourceActionMap(sourceActionMap(status, "state"), "terminated")
	// Preserve typed canonical field order and exact uint64 IDs. Receipt2 alone
	// must not upgrade a membership-only native result to listener proof.
	var receipt struct {
		Version     int                                    `json:"version"`
		Nonce       string                                 `json:"nonce"`
		JobUID      string                                 `json:"job_uid"`
		PodUID      string                                 `json:"pod_uid"`
		Observation clusterbootstrap.MembershipObservation `json:"observation"`
	}
	if json.Unmarshal([]byte(terminated["message"].(string)), &receipt) != nil {
		t.Fatal("invalid receipt fixture")
	}
	receipt.Observation.ListenerProofVersion = proof
	if version != 0 {
		receipt.Version = version
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	terminated["message"] = string(raw)
}

type cohortMembershipTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	idleCalls atomic.Int64
}

func (t *cohortMembershipTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.roundTrip(r)
}
func (t *cohortMembershipTransport) CloseIdleConnections() { t.idleCalls.Add(1) }

type cohortMembershipBody struct {
	io.ReadCloser
	close func() error
}

func (b *cohortMembershipBody) Close() error {
	err := b.ReadCloser.Close()
	if closeErr := b.close(); closeErr != nil {
		return closeErr
	}
	return err
}

// Real fixed HTTPS Job/read/receipt transport; only SQL's validated snapshot
// reader is supplied. Separate PostgreSQL acceptance covers that producer.
func TestClusterSSHPreparedCohortMembership(t *testing.T) {
	modes := []string{"pair", "replacement", "old native producer", "network failure", "last network failure", "last member failure", "member disagreement", "wrong observer", "cross-kind Job replay", "cross-kind Pod replay", "membership Job replay", "membership Pod replay", "network Job replay", "SQL before", "SQL during", "SQL final", "proof alias", "receipt alias", "authorization alias", "clock backwards", "namespace late", "Node late", "annotation late", "per-Job Node drift", "close error", "close cancellation", "cancel before", "cancel after", "missing receipt", "missing prejoin"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			a, authority, targetUIDs := sshMembershipAuthorityFixture(t, mode == "replacement")
			authority.payload, authority.baseline = `{"settled":"fixture"}`, a.Baseline
			authority.queue.ClusterID = a.Source.ClusterID
			authority.authorization.Targets = []clusterSSHPreparationAuthorizedTarget{{TargetID: a.Lease.TargetID, CredentialSHA256: strings.Repeat("a", 64)}}
			op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
			get := func(ctx context.Context, path string, out any) error {
				raw, _ := json.Marshal(sshMembershipNativeFixture(a, targetUIDs, path))
				return json.Unmarshal(raw, out)
			}
			nodes, _, err := readClusterSSHPreparedMembershipNodes(context.Background(), get, authority)
			if err != nil {
				t.Fatal(err)
			}
			networks := map[string]clusterbootstrap.SourceNetwork{}
			for _, node := range nodes {
				n := node.Node
				networks[n.Hostname] = sshSourceNetworkFixture(clusterSSHSourceMember{NodeUID: n.NodeUID, Name: n.Hostname, Address: n.Address, MachineID: n.MachineID, BootID: n.BootID}, authority.version)
			}
			originalSource := authority.receipt.Targets[0].Prejoin.Receipt.Source
			roster := []string{}
			for _, source := range authority.proof.Source.Members {
				roster = append(roster, source.NodeUID)
			}
			if mode == "missing receipt" {
				authority.receipt.Targets = authority.receipt.Targets[:1]
			}
			if mode == "missing prejoin" {
				authority.receipt.Targets[0].Prejoin = nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			var mu sync.Mutex
			posts, reads, finalNodes := 0, 0, 0
			closedFinal := false
			var job map[string]any
			jobUIDs, podUIDs, nonces, hosts := []string{}, []string{}, []string{}, []string{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer synthetic-cohort" {
					t.Error("existing controller authentication changed")
				}
				if r.URL.Path == "/api/v1/nodes" || r.URL.Path == "/api/v1/namespaces/kube-system" {
					if r.Method != http.MethodGet || r.URL.RawQuery != "" {
						t.Error("non-observation Node request")
					}
					object := sshMembershipNativeFixture(a, targetUIDs, r.URL.Path)
					if r.URL.Path == "/api/v1/nodes" && closedFinal {
						finalNodes++
					}
					if r.URL.Path == "/api/v1/namespaces/kube-system" && mode == "namespace late" && closedFinal {
						sourceActionMap(object, "metadata")["uid"] = newClusterUUID()
					}
					if r.URL.Path == "/api/v1/nodes" {
						for _, item := range anySlice(object["items"]) {
							metadata := sourceActionMap(item.(map[string]any), "metadata")
							if mode == "Node late" && closedFinal {
								metadata["uid"] = newClusterUUID()
							}
							if metadata["uid"] == originalSource.SourceUID && (mode == "annotation late" && closedFinal || mode == "per-Job Node drift" && posts == 2) {
								sourceActionMap(metadata, "annotations")["etcd.k3s.cattle.io/node-name"] = metadata["name"].(string) + "-87654321"
							}
						}
					}
					_ = json.NewEncoder(w).Encode(object)
					return
				}
				if r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
					posts++
					job = nil
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("Job body")
						w.WriteHeader(500)
						return
					}
					spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
					container := anySlice(spec["containers"])[0].(map[string]any)
					command, args := clusterStringSlice(container["command"]), clusterStringSlice(container["args"])
					want := "source-network-client"
					if posts%2 == 0 {
						want = "source-membership-client"
					}
					if posts > 6 || len(command) != 2 || command[0] != "/usr/local/bin/borealis-node-manager" || command[1] != want || spec["nodeName"] != nodes[(posts-1)/2].Node.Hostname {
						t.Error("Job escaped exact retained observer route or fixed commands")
						w.WriteHeader(500)
						return
					}
					if spec["activeDeadlineSeconds"] != float64(20) || sourceActionMap(job, "spec")["activeDeadlineSeconds"] != float64(25) || sourceActionMap(job, "spec")["ttlSecondsAfterFinished"] != float64(60) {
						t.Error("remote Job bounds changed")
					}
					nonces, hosts = append(nonces, args[0]), append(hosts, spec["nodeName"].(string))
					jobUID, podUID := newClusterUUID(), newClusterUUID()
					if mode == "cross-kind Job replay" && posts == 2 || mode == "network Job replay" && posts == 3 {
						jobUID = jobUIDs[0]
					}
					if mode == "cross-kind Pod replay" && posts == 2 {
						podUID = podUIDs[0]
					}
					if mode == "membership Job replay" && posts == 4 {
						jobUID = jobUIDs[1]
					}
					if mode == "membership Pod replay" && posts == 4 {
						podUID = podUIDs[1]
					}
					jobUIDs, podUIDs = append(jobUIDs, jobUID), append(podUIDs, podUID)
					sourceActionMap(job, "metadata")["uid"] = jobUID
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					if posts == 2 {
						switch mode {
						case "proof alias":
							authority.proof.Source.Members[0].SSHFingerprint = "changed"
						case "receipt alias":
							authority.receipt.Targets[0].Prejoin.Receipt.Steps[0].InputSHA256 = strings.Repeat("f", 64)
						case "authorization alias":
							authority.authorization.Targets[0].CredentialSHA256 = strings.Repeat("f", 64)
						}
					}
					if mode == "network failure" && posts == 1 || mode == "last network failure" && posts == 5 || mode == "last member failure" && posts == 6 {
						w.WriteHeader(502)
						return
					}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/") && job != nil {
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/borealis/pods" || job == nil {
					t.Error("new transport path or mutation")
					w.WriteHeader(500)
					return
				}
				network := networks[hosts[posts-1]]
				pod := sourceActionPod(t, job, network, podUIDs[posts-1])
				if posts%2 == 0 {
					container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					request, err := clusterbootstrap.DecodeMembershipObservationRequest(clusterStringSlice(container["args"])[1])
					if err != nil || !request.RequireListenerProof || request.RequiredListenerProofVersion != 0 || request.Source != originalSource || request.Observer == nil || request.ObserverUID() != network.NodeUID || request.Network != network || !reflect.DeepEqual(request.OriginalSourceNodeUIDs, roster) || len(request.Prepared) != len(a.Cohort.Targets) {
						t.Error("lost original source, complete partition or observer binding", err)
						w.WriteHeader(500)
						return
					}
					if mode == "wrong observer" && posts == 6 {
						request.Observer = &clusterbootstrap.MembershipObserver{NodeUID: nodes[0].Node.NodeUID}
						request.Network = networks[nodes[0].Node.Hostname]
					}
					result := sshCohortMembershipResultFixture(request)
					if mode == "member disagreement" && posts == 6 {
						for i, member := range result.Membership.Members {
							if member.Address == a.Source.Members[0].Address {
								result.Membership.Members[i].ID--
							}
						}
					}
					pod = sshCohortMembershipPod(t, job, request, result, podUIDs[posts-1])
					if mode == "old native producer" && posts == 6 {
						stripCohortMembershipListenerProof(t, pod)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}, "metadata": map[string]any{}})
			}))
			defer server.Close()
			closed, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			var active, closes atomic.Int64
			client := server.Client()
			base := client.Transport
			transport := &cohortMembershipTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if response != nil && response.Body != nil {
					active.Add(1)
					mu.Lock()
					last := posts == 6 && r.URL.Path == "/api/v1/namespaces/borealis/pods"
					mu.Unlock()
					response.Body = &cohortMembershipBody{response.Body, func() error {
						defer active.Add(-1)
						closes.Add(1)
						if last {
							mu.Lock()
							closedFinal = true
							mu.Unlock()
							if mode == "close cancellation" {
								close(closed)
								<-release
							}
							if mode == "cancel after" {
								cancel()
							}
							if mode == "close error" || mode == "close cancellation" {
								return errors.New("synthetic close error")
							}
						}
						return nil
					}}
				}
				return response, err
			}}
			client.Transport = transport
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-cohort", httpClient: client}, namespace: "borealis", controllerHolder: a.Lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			read := func(readCtx context.Context) (clusterSSHPreparedAuthority, int64, error) {
				mu.Lock()
				defer mu.Unlock()
				reads++
				if active.Load() != 0 {
					t.Error("SQL comparison before local HTTP cleanup joined")
				}
				if readCtx.Err() != nil || mode == "SQL before" && reads == 2 {
					return clusterSSHPreparedAuthority{}, 0, errClusterConflict
				}
				current, now := authority, int64(100)
				if mode == "SQL during" && posts >= 3 || mode == "SQL final" && finalNodes >= 2 {
					current.payload += "changed"
				}
				if mode == "clock backwards" && posts >= 2 {
					now--
				}
				return current, now, nil
			}
			var got clusterSSHPreparedCohortMembershipObservation
			if mode == "close cancellation" {
				done := make(chan struct{})
				go func() {
					got, err = readClusterSSHPreparedCohortMembership(ctx, op, a.Lease.ControllerHolder, runner, read)
					close(done)
				}()
				defer func() {
					cancel()
					unblock.Do(func() { close(release) })
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("collector survived test cleanup")
					}
				}()
				select {
				case <-closed:
				case <-done:
					t.Fatal("collector returned before close gate", err)
				case <-ctx.Done():
					t.Fatal("collector never reached close gate")
				}
				cancel()
				select {
				case <-done:
					t.Fatal("cancellation escaped blocked local Close")
				case <-time.After(25 * time.Millisecond):
				}
				unblock.Do(func() { close(release) })
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("collector failed to join released Close")
				}
			} else {
				got, err = readClusterSSHPreparedCohortMembership(ctx, op, a.Lease.ControllerHolder, runner, read)
			}
			valid := mode == "pair" || mode == "replacement"
			if (err == nil) != valid || !valid && !reflect.DeepEqual(got, clusterSSHPreparedCohortMembershipObservation{}) {
				t.Fatal("incomplete or changed cohort escaped", err, "posts", posts)
			}
			if active.Load() != 0 || client.Transport != transport || runner.kube.httpClient != client || transport.idleCalls.Load() != 0 {
				t.Fatal("shared client changed or local HTTP cleanup escaped")
			}
			if valid {
				if posts != 6 || len(got.Views) != 3 || finalNodes != 2 || got.Prepared.OperationID != op.ID || got.Prepared.Controller != a.Lease.ControllerHolder || len(got.Prepared.Nodes) != len(targetUIDs) || closes.Load() == 0 {
					t.Fatal("incomplete independently observed cohort", posts, finalNodes)
				}
				seen := map[string]bool{}
				for i, view := range got.Views {
					if view.Observation.Source != originalSource || view.Observation.LocalNode != nodes[i].Node || view.Observation.Observer == nil || view.Observation.Observer.NodeUID != nodes[i].Node.NodeUID || seen[view.Observation.LocalNode.NodeUID] || !reflect.DeepEqual(view.Observation.Membership.Members, got.Views[0].Observation.Membership.Members) {
						t.Fatal("borrowed local identity or mismatched full membership")
					}
					seen[view.Observation.LocalNode.NodeUID] = true
				}
				for _, values := range [][]string{nonces, jobUIDs, podUIDs} {
					seen := map[string]bool{}
					for _, value := range values {
						if seen[value] {
							t.Fatal("six Jobs reused receipt identity")
						}
						seen[value] = true
					}
				}
			}
			if (mode == "missing receipt" || mode == "missing prejoin" || mode == "SQL before" || mode == "cancel before") && posts != 0 {
				t.Fatal("invalid starting custody created Job")
			}
		})
	}
}

func TestClusterSSHPreparedCohortJobIdentity(t *testing.T) {
	for _, mode := range []string{"fresh", "nonce replay", "Job replay", "Pod replay", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			seen := [3]map[string]bool{{}, {}, {}}
			first := [3]string{newClusterUUID(), newClusterUUID(), newClusterUUID()}
			if !retainClusterSSHMembershipJob(seen, first[0], first[1], first[2]) {
				t.Fatal("fresh identity rejected")
			}
			next := [3]string{newClusterUUID(), newClusterUUID(), newClusterUUID()}
			switch mode {
			case "nonce replay":
				next[0] = first[0]
			case "Job replay":
				next[1] = first[1]
			case "Pod replay":
				next[2] = first[2]
			case "malformed":
				next[0] = "invalid"
			}
			if retainClusterSSHMembershipJob(seen, next[0], next[1], next[2]) != (mode == "fresh") {
				t.Fatal("receipt identity reuse accepted")
			}
		})
	}
}

func TestClusterSSHPreparedCohortHTTPCleanup(t *testing.T) {
	for _, mode := range []string{"success", "close error", "roundtrip error", "nil body"} {
		t.Run(mode, func(t *testing.T) {
			closes := 0
			underlying := &cohortMembershipTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				response := &http.Response{Body: &cohortMembershipBody{io.NopCloser(strings.NewReader("{}")), func() error {
					closes++
					if mode == "close error" {
						return errors.New("synthetic close error")
					}
					return nil
				}}}
				if mode == "roundtrip error" {
					return response, errors.New("synthetic transport error")
				}
				if mode == "nil body" {
					response.Body = nil
				}
				return response, nil
			}}
			tracker := &clusterSSHMembershipHTTP{transport: underlying}
			response, err := tracker.RoundTrip(httptest.NewRequest("GET", "https://example.invalid/", nil))
			if err == nil {
				if tracker.clean() {
					t.Fatal("in-flight response considered closed")
				}
				_ = response.Body.Close()
				_ = response.Body.Close()
			}
			if tracker.clean() != (mode == "success") || tracker.active.Load() != 0 || underlying.idleCalls.Load() != 0 || mode != "nil body" && closes != 1 {
				t.Fatal("ignored Close error or incorrect cleanup ownership")
			}
		})
	}
}

// The private reader has a 5s child deadline, and the shared HTTP client may
// impose a shorter one. Both must remain authoritative through Body.Close,
// even while the collector's outer scope and original SQL authority stay live.
func TestClusterSSHPreparedCohortMembershipCloseExpiry(t *testing.T) {
	for _, mode := range []string{"request deadline", "client deadline"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, targets := sshMembershipAuthorityFixture(t, false)
			authority.payload, authority.baseline, authority.queue.ClusterID = `{"settled":"cleanup"}`, a.Baseline, a.Source.ClusterID
			op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
			n := newPreparedMembershipNativeForProof(t, a, authority, targets, "", 0)
			client := n.runner.kube.httpClient
			transport := client.Transport
			client.Timeout = 7 * time.Second
			if mode == "client deadline" {
				client.Timeout = time.Second
			}
			timeout := client.Timeout
			outer, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			expired := false
			n.closeHook = func(ctx context.Context, post int) error {
				if post == 6 {
					<-ctx.Done()
					expired = ctx.Err() == context.DeadlineExceeded
					if outer.Err() != nil {
						t.Error("fixture expired outer scope instead of request")
					}
				}
				return nil
			}
			read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
				return authority, 100, ctx.Err()
			}
			got, err := readClusterSSHPreparedCohortMembership(outer, op, a.Lease.ControllerHolder, n.runner, read)
			if !expired || outer.Err() != nil || err != errClusterConflict || !reflect.DeepEqual(got, clusterSSHPreparedCohortMembershipObservation{}) || n.posts.Load() != 6 {
				t.Fatal("child deadline during Close escaped collector", err, expired, n.posts.Load())
			}
			// A failed private wrapper cannot poison the original shared client or an
			// independently owned sibling collection using that same client.
			got, err = readClusterSSHPreparedCohortMembership(outer, op, a.Lease.ControllerHolder, n.runner, read)
			if err != nil || len(got.Views) != 3 || n.posts.Load() != 12 || n.active.Load() != 0 || n.runner.kube.httpClient != client || client.Transport != transport || client.Timeout != timeout {
				t.Fatal("failed cleanup poisoned sibling or shared client", err, n.posts.Load())
			}
		})
	}
}
