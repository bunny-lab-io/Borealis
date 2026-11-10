package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Shared fixed HTTPS producer for pure and real-SQL scope tests. Each collection
// still executes the production six-Job collector and its native receipt parser.
type preparedMembershipScopeNative struct {
	runner    *kubernetesClusterStepRunner
	posts     atomic.Int32
	active    atomic.Int32
	closes    atomic.Int32
	phase     atomic.Int32
	before    func(context.Context) error
	closeHook func(context.Context, int) error
}

func newPreparedMembershipScopeNative(t *testing.T, a clusterSSHPreparationAuthority, authority clusterSSHPreparedAuthority, targets map[string]string, mode string) *preparedMembershipScopeNative {
	t.Helper()
	n := &preparedMembershipScopeNative{}
	get := func(_ context.Context, path string, out any) error {
		raw, _ := json.Marshal(sshMembershipNativeFixture(a, targets, path))
		return json.Unmarshal(raw, out)
	}
	nodes, _, err := readClusterSSHPreparedMembershipNodes(context.Background(), get, authority)
	if err != nil {
		t.Fatal(err)
	}
	networks := map[string]clusterbootstrap.SourceNetwork{}
	for _, expected := range nodes {
		node := expected.Node
		networks[node.Hostname] = sshSourceNetworkFixture(clusterSSHSourceMember{NodeUID: node.NodeUID, Name: node.Hostname, Address: node.Address, MachineID: node.MachineID, BootID: node.BootID}, authority.version)
	}
	var mu sync.Mutex
	var job map[string]any
	var firstJobs, firstPods []string
	var podUID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.before != nil && n.before(r.Context()) != nil {
			t.Error("SQL connection retained during native IO")
			w.WriteHeader(500)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/namespaces/kube-system" || r.URL.Path == "/api/v1/nodes") {
			raw, _ := json.Marshal(sshMembershipNativeFixture(a, targets, r.URL.Path))
			if n.phase.Load() > 0 && mode == "Node drift" && r.URL.Path == "/api/v1/nodes" {
				raw = []byte(strings.ReplaceAll(string(raw), nodes[0].Node.NodeUID, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
			if json.NewDecoder(r.Body).Decode(&job) != nil {
				t.Error("invalid Job")
				w.WriteHeader(500)
				return
			}
			post := int(n.posts.Add(1))
			position := (post - 1) % 6
			spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
			container := anySlice(spec["containers"])[0].(map[string]any)
			command := clusterStringSlice(container["command"])
			want := "source-network-client"
			if position%2 == 1 {
				want = "source-membership-client"
			}
			if len(command) != 2 || command[0] != "/usr/local/bin/borealis-node-manager" || command[1] != want || spec["nodeName"] != nodes[position/2].Node.Hostname {
				t.Error("scope escaped fixed collection")
				w.WriteHeader(500)
				return
			}
			if post > 6 && (mode == "native read failure" || mode == "ignored input failure") {
				w.WriteHeader(502)
				return
			}
			jobUID := newClusterUUID()
			podUID = newClusterUUID()
			if post <= 6 {
				firstJobs = append(firstJobs, jobUID)
				firstPods = append(firstPods, podUID)
			} else {
				if mode == "Job replay" || mode == "network Job replay" && position%2 == 0 {
					jobUID = firstJobs[position]
				}
				if mode == "cross-kind Job replay" && position == 0 {
					jobUID = firstJobs[1]
				}
				if mode == "cross-kind Pod replay" && position == 0 {
					podUID = firstPods[1]
				}
				if mode == "Pod replay" || mode == "network Pod replay" && position%2 == 0 {
					podUID = firstPods[position]
				}
			}
			sourceActionMap(job, "metadata")["uid"] = jobUID
			job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/") && job != nil {
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/borealis/pods" || job == nil {
			t.Error("unexpected native path")
			w.WriteHeader(500)
			return
		}
		post := int(n.posts.Load())
		spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
		network := networks[spec["nodeName"].(string)]
		if post > 6 && mode == "process drift" {
			network.Kubelet.StartTicks++
		}
		pod := sourceActionPod(t, job, network, podUID)
		if post%2 == 0 {
			container := anySlice(spec["containers"])[0].(map[string]any)
			request, err := clusterbootstrap.DecodeMembershipObservationRequest(clusterStringSlice(container["args"])[1])
			if err != nil || !request.RequireListenerProof || request.Network != network {
				t.Error("scope lost current listener capability", err)
				w.WriteHeader(500)
				return
			}
			value := sshCohortMembershipResultFixture(request)
			if post > 6 && mode == "member drift" {
				for i, member := range value.Membership.Members {
					if member.Address == a.Source.Members[0].Address {
						value.Membership.Members[i].ID--
						if value.Membership.LocalID == member.ID {
							value.Membership.LocalID--
						}
					}
				}
			}
			pod = sshCohortMembershipPod(t, job, request, value, podUID)
			if post > 6 && mode == "old capability" {
				stripCohortMembershipListenerProof(t, pod)
			}
		}
		w.Header().Set("X-Scope-Post", fmt.Sprint(post))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}, "metadata": map[string]any{}})
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	base := client.Transport
	transport := &cohortMembershipTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(r)
		if response != nil && response.Body != nil {
			n.active.Add(1)
			post := 0
			_, _ = fmt.Sscan(response.Header.Get("X-Scope-Post"), &post)
			response.Body = &cohortMembershipBody{response.Body, func() error {
				defer n.active.Add(-1)
				n.closes.Add(1)
				if n.closeHook != nil {
					return n.closeHook(r.Context(), post)
				}
				return nil
			}}
		}
		return response, err
	}}
	client.Transport = transport
	n.runner = &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-scope", httpClient: client}, namespace: "borealis", controllerHolder: a.Lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
	t.Cleanup(func() {
		if n.active.Load() != 0 || transport.idleCalls.Load() != 0 {
			t.Error("scope escaped cleanup or closed shared transport", n.active.Load(), transport.idleCalls.Load())
		}
	})
	return n
}

func TestClusterSSHPreparedMembershipScope(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "explicit refresh", "consumer copy", "native read failure", "Node drift", "member drift", "process drift", "old capability", "Job replay", "Pod replay", "network Job replay", "network Pod replay", "cross-kind Job replay", "cross-kind Pod replay", "payload drift", "receipt alias", "source alias", "credential alias", "clock backwards", "controller drift", "image drift", "endpoint drift", "token drift", "consumer error", "ignored input failure", "ignored authority failure", "canceled caller", "final close error"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, targets := sshMembershipAuthorityFixture(t, mode == "replacement")
			authority.payload, authority.baseline, authority.queue.ClusterID = `{"settled":"scope"}`, a.Baseline, a.Source.ClusterID
			authority.authorization.Targets = []clusterSSHPreparationAuthorizedTarget{{TargetID: a.Lease.TargetID, CredentialSHA256: strings.Repeat("a", 64)}}
			op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
			n := newPreparedMembershipScopeNative(t, a, authority, targets, mode)
			var mu sync.Mutex
			read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
				mu.Lock()
				defer mu.Unlock()
				now := int64(100)
				// Mutate runner metadata inside the serialized authority read so
				// the regression tests drift without introducing a fixture race.
				if n.phase.Load() > 0 && mode == "controller drift" {
					n.runner.controllerHolder = "other"
				}
				if n.phase.Load() > 0 && mode == "endpoint drift" {
					n.runner.kube.baseURL += "/changed"
				}
				if n.phase.Load() > 0 && mode == "token drift" {
					n.runner.kube.token = "other"
				}
				if n.phase.Load() > 0 && mode == "image drift" {
					n.runner.actionImage = "registry.example/api@sha256:" + strings.Repeat("b", 64)
				}
				if n.phase.Load() > 0 && mode == "clock backwards" {
					now--
				}
				if ctx.Err() != nil {
					return clusterSSHPreparedAuthority{}, 0, ctx.Err()
				}
				return authority, now, nil
			}
			if mode == "final close error" {
				n.closeHook = func(_ context.Context, post int) error {
					if post == 12 {
						return errors.New("private cleanup error")
					}
					return nil
				}
			}
			var escaped clusterSSHPreparedMembershipChecks
			consumed := false
			consume := func(ctx context.Context, value clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
				consumed = true
				escaped = checks
				if n.posts.Load() != 6 || len(value.Views) != 3 {
					t.Error("redundant initial collection or missing proof")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 60*time.Second {
					t.Error("scope deadline enlarged")
				}
				n.phase.Store(1)
				mu.Lock()
				switch mode {
				case "payload drift", "ignored authority failure":
					authority.payload += "changed"
				case "receipt alias":
					authority.receipt.Targets[0].Prejoin.Receipt.Steps[0].InputSHA256 = strings.Repeat("f", 64)
				case "source alias":
					authority.proof.Source.Members[0].SSHFingerprint = "changed"
				case "credential alias":
					authority.authorization.Targets[0].CredentialSHA256 = strings.Repeat("b", 64)
				}
				mu.Unlock()
				if mode == "receipt alias" || mode == "source alias" || mode == "credential alias" {
					if checks.Authority(ctx) == nil {
						t.Error("nested authority alias accepted")
					}
					return errClusterConflict
				}
				if mode == "consumer copy" {
					value.Prepared.Nodes["foreign"] = clusterbootstrap.K3sJoinedNode{}
					value.Views[0].Observation.Membership.Members[0].ID++
					value.Views[0].Observation.Observer.NodeUID = "changed"
				}
				if mode == "consumer error" {
					return errors.New("private consumer error")
				}
				if mode == "controller drift" || mode == "image drift" || mode == "endpoint drift" || mode == "token drift" {
					return checks.Authority(ctx)
				}
				if mode == "ignored authority failure" {
					_ = checks.Authority(context.Background())
					mu.Lock()
					authority.payload = `{"settled":"scope"}`
					mu.Unlock()
					return nil
				}
				if mode == "ignored input failure" {
					if checks.Inputs(context.Background()) == nil {
						t.Error("failed native refresh accepted")
					}
					if checks.Authority(context.Background()) == nil {
						t.Error("failed refresh revived")
					}
					return nil
				}
				if mode == "canceled caller" {
					c, stop := context.WithCancel(context.Background())
					stop()
					_ = checks.Inputs(c)
					if checks.Authority(context.Background()) == nil {
						t.Error("failed scope revived")
					}
					return nil
				}
				if mode == "explicit refresh" || mode == "consumer copy" {
					return checks.Inputs(context.Background())
				}
				return nil
			}
			err := withClusterSSHPreparedMembershipScope(context.Background(), op, a.Lease.ControllerHolder, n.runner, read, consume)
			valid := mode == "pair" || mode == "replacement" || mode == "explicit refresh" || mode == "consumer copy"
			if !consumed || (err == nil) != valid || err != nil && err != errClusterConflict {
				t.Fatal("wrong scoped result", err, consumed)
			}
			if valid {
				want := int32(12)
				if mode == "explicit refresh" || mode == "consumer copy" {
					want = 18
				}
				if n.posts.Load() != want {
					t.Fatal("incorrect collection count", n.posts.Load(), want)
				}
			}
			before := n.posts.Load()
			if escaped.Inputs(context.Background()) == nil || escaped.Authority(context.Background()) == nil || n.posts.Load() != before {
				t.Fatal("escaped callbacks revived closed evidence")
			}
		})
	}
}

func TestClusterSSHPreparedMembershipScopeJoinsEscapedCheck(t *testing.T) {
	a, authority, targets := sshMembershipAuthorityFixture(t, false)
	authority.payload, authority.baseline, authority.queue.ClusterID = `{"settled":"scope"}`, a.Baseline, a.Source.ClusterID
	op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
	n := newPreparedMembershipScopeNative(t, a, authority, targets, "")
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	n.closeHook = func(ctx context.Context, post int) error {
		if post == 8 {
			close(entered)
			<-release
		}
		return nil
	}
	read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
		return authority, 100, ctx.Err()
	}
	checkDone, scopeDone := make(chan error, 1), make(chan error, 1)
	go func() {
		scopeDone <- withClusterSSHPreparedMembershipScope(context.Background(), op, a.Lease.ControllerHolder, n.runner, read, func(_ context.Context, _ clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
			go func() { checkDone <- checks.Inputs(context.Background()) }()
			select {
			case <-entered:
				return nil
			case <-time.After(5 * time.Second):
				return errors.New("check never reached cleanup")
			}
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned HTTP cleanup not entered")
	}
	select {
	case <-scopeDone:
		t.Fatal("scope escaped active body cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	for _, done := range []chan error{checkDone, scopeDone} {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("unjoined escaped check succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("scope did not join cleanup")
		}
	}
	if n.posts.Load() != 8 {
		t.Fatal("failed escaped check allowed final collection", n.posts.Load())
	}
}

func TestClusterSSHPreparedMembershipScopeRejectsEntry(t *testing.T) {
	if (*clusterController)(nil).withClusterSSHPreparedMembership(context.Background(), clusterControllerOperation{}, nil) != errClusterConflict {
		t.Fatal("nil controller accepted")
	}
	if withClusterSSHPreparedMembershipScope(context.Background(), clusterControllerOperation{}, "", nil, nil, nil) != errClusterConflict {
		t.Fatal("invalid scope accepted")
	}
}

func TestClusterSSHPreparedMembershipScopeSharedDeadline(t *testing.T) {
	a, authority, targets := sshMembershipAuthorityFixture(t, false)
	authority.payload, authority.baseline, authority.queue.ClusterID = `{"settled":"scope"}`, a.Baseline, a.Source.ClusterID
	op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
	n := newPreparedMembershipScopeNative(t, a, authority, targets, "")
	parent, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	deadline, _ := parent.Deadline()
	n.closeHook = func(ctx context.Context, post int) error {
		if got, ok := ctx.Deadline(); !ok || got.After(deadline) {
			t.Error("native round extended parent ceiling")
		}
		if post == 8 {
			<-ctx.Done()
		}
		return nil
	}
	var escaped clusterSSHPreparedMembershipChecks
	err := withClusterSSHPreparedMembershipScope(parent, op, a.Lease.ControllerHolder, n.runner, func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
		return authority, 100, ctx.Err()
	}, func(ctx context.Context, _ clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
		escaped = checks
		if got, ok := ctx.Deadline(); !ok || got != deadline {
			t.Error("scope reset parent deadline")
		}
		// Background cannot extend explicit refresh beyond the original scope.
		return checks.Inputs(context.Background())
	})
	if err != errClusterConflict || n.posts.Load() != 8 || parent.Err() == nil || escaped.Authority == nil || escaped.Authority(context.Background()) == nil {
		t.Fatal("expired refresh revived evidence", err, n.posts.Load())
	}
}
