package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshPreparationConfigurationFixture(t *testing.T, replacement bool) clusterSSHPreparationWork {
	t.Helper()
	cohort, source := sshInspectionCohortFixture(t)
	if replacement {
		target := cohort.Targets[1]
		source.Members = append(source.Members, clusterSSHSourceMember{NodeID: target.Binding.TargetID, NodeUID: newClusterUUID(), Name: target.Report.Hostname, Address: target.Binding.Address, MachineID: target.Report.MachineID, BootID: target.Report.BootID})
		source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
		cohort.Targets = cohort.Targets[:1]
	}
	a := clusterSSHPreparationAuthorization{Version: 1, OperationID: cohort.OperationID, Attempt: cohort.Attempt, ControllerHolder: cohort.ControllerHolder,
		InspectionSHA256: strings.Repeat("a", 64), QualificationSHA256: strings.Repeat("b", 64), SourceSHA256: strings.Repeat("c", 64)}
	for _, target := range cohort.Targets {
		a.Targets = append(a.Targets, clusterSSHPreparationAuthorizedTarget{target.Binding.TargetID, target.Generation + 1, strings.Repeat("d", 64)})
	}
	return clusterSSHPreparationWork{Authorization: a, Baseline: clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true},
		Proof: clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}, Payload: `{"source_k3s_version":"v1.36.3+k3s1"}`}
}

func sshPreparationConfigurationSecret() map[string]any {
	secret := sshPreparationSecretFixture()
	secret["data"].(map[string]string)["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = base64.StdEncoding.EncodeToString([]byte("4096Mi"))
	return secret
}

func TestClusterSSHPreparationConfigurationFreshPlan(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "wrong plan", "claimed work", "authority", "source drift", "Secret drift", "excluded Secret drift", "profile drift", "missing cap", "network drift", "source networks disagree", "target prefix disagrees", "source error", "network error", "cancel", "timeout", "copied input", "changed next read"} {
		t.Run(mode, func(t *testing.T) {
			work := sshPreparationConfigurationFixture(t, mode == "replacement" || mode == "source networks disagree")
			if mode == "target prefix disagrees" {
				work.Proof.Cohort.Targets[1].Report.ConnectedPrefix = "192.168.91.0/24"
			}
			plan, _ := clusterSSHPreparationPlanDigest(work.Authorization)
			source := work.Proof.Source
			secret := sshPreparationConfigurationSecret()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 5*time.Millisecond)
				defer stop()
			}
			checks, reads, networks := 0, 0, 0
			check := func(ctx context.Context) error {
				checks++
				if mode == "authority" && checks > 4 {
					return errClusterConflict
				}
				return ctx.Err()
			}
			get := func(ctx context.Context, path string, out any) error {
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					value = map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}}
				case "/api/v1/nodes":
					if mode == "source error" {
						return errClusterConflict
					}
					items := []any{}
					for _, member := range source.Members {
						node := sshSourceKubernetesFixture(member)
						if mode == "source drift" && reads > 0 {
							node["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
						}
						items = append(items, node)
					}
					value = map[string]any{"items": items}
				case clusterSSHRuntimeSecretPath:
					reads++
					if mode == "Secret drift" && reads > 1 {
						secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
					}
					if mode == "excluded Secret drift" && reads > 1 {
						secret["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("changed"))
					}
					if mode == "profile drift" {
						secret["data"].(map[string]string)["BOREALIS_DB_POOL_SIZE"] = base64.StdEncoding.EncodeToString([]byte("999999"))
					}
					if mode == "missing cap" {
						delete(secret["data"].(map[string]string), "BOREALIS_POSTGRES_DB_MEMORY_LIMIT")
					}
					value = secret
				default:
					return errClusterConflict
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			network := func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				networks++
				if mode == "network error" {
					return clusterbootstrap.SourceNetwork{}, errClusterConflict
				}
				if mode == "cancel" {
					cancel()
				}
				if mode == "timeout" {
					<-ctx.Done()
					return clusterbootstrap.SourceNetwork{}, ctx.Err()
				}
				n := sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version)
				if mode == "source networks disagree" && member.NodeUID == source.Members[1].NodeUID {
					n.ServiceCIDR = "10.44.0.0/16"
				}
				if mode == "network drift" && networks > len(source.Members) {
					n.ServiceCIDR = "10.44.0.0/16"
				}
				return n, ctx.Err()
			}
			if mode == "claimed work" {
				work.Claims = append(work.Claims, clusterSSHNetworkTargetClaim{})
			}
			read := newClusterSSHPreparationConfigurationRead(work, check, get, network)
			if mode == "copied input" {
				work.Proof.Cohort.Targets[0].Report.BootID = newClusterUUID()
			}
			requested := plan
			if mode == "wrong plan" {
				requested = strings.Repeat("f", 64)
			}
			digest, err := read(ctx, requested)
			valid := mode == "pair" || mode == "replacement" || mode == "copied input" || mode == "changed next read"
			if (err == nil) != valid || (err != nil && digest != "") {
				t.Fatal(mode, err)
			}
			if valid {
				if networks != 2*len(source.Members) || reads != 2 {
					t.Fatal("missing fresh source rounds", networks, reads)
				}
				if mode == "changed next read" {
					secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
				}
				again, err := read(ctx, plan)
				if mode == "changed next read" {
					if err == nil {
						t.Fatal("prior digest accepted changed Secret")
					}
				} else if err != nil || again != digest || networks != 4*len(source.Members) || reads != 4 {
					t.Fatal("fresh repeat failed", err)
				}
			} else {
				before := checks
				if _, err := read(context.Background(), plan); err == nil || checks != before {
					t.Fatal("ignored failure did not latch")
				}
			}
		})
	}
}

// Exercise the exact native factory through fixed HTTPS Job/receipt readers.
// Only original proposal SQL is supplied; the existing SQL test covers it.
func sshPreparationConfigurationNativeFixture(t *testing.T, work clusterSSHPreparationWork) (*kubernetesClusterStepRunner, *atomic.Int64) {
	t.Helper()
	source := work.Proof.Source
	var mu sync.Mutex
	jobs, pods := map[string]map[string]any{}, map[string]map[string]any{}
	posts := &atomic.Int64{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var value any
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs":
			var job map[string]any
			if json.NewDecoder(r.Body).Decode(&job) != nil {
				t.Error("invalid source Job body")
				w.WriteHeader(400)
				return
			}
			metadata := sourceActionMap(job, "metadata")
			name, _ := metadata["name"].(string)
			if jobs[name] != nil {
				t.Error("source Job reused")
			}
			metadata["uid"] = newClusterUUID()
			job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			node, _ := nestedMap(nestedMap(nestedMap(job, "spec"), "template"), "spec")["nodeName"].(string)
			var member clusterSSHSourceMember
			for _, candidate := range source.Members {
				if candidate.Name == node {
					member = candidate
				}
			}
			if member.Name == "" {
				t.Error("foreign source Job")
				w.WriteHeader(400)
				return
			}
			pod := sourceActionPod(t, job, sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), newClusterUUID())
			jobs[name], pods[metadata["uid"].(string)] = job, pod
			posts.Add(1)
			w.WriteHeader(http.StatusCreated)
			value = job
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/kube-system":
			value = map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "kube-system", "uid": source.KubeSystemUID, "resourceVersion": "1"}}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes":
			items := []any{}
			for _, member := range source.Members {
				node := sshSourceKubernetesFixture(member)
				node["apiVersion"], node["kind"] = "v1", "Node"
				node["metadata"].(map[string]any)["resourceVersion"] = "1"
				items = append(items, node)
			}
			value = map[string]any{"apiVersion": "v1", "kind": "NodeList", "metadata": map[string]any{}, "items": items}
		case r.Method == http.MethodGet && r.URL.Path == clusterSSHRuntimeSecretPath:
			value = sshPreparationConfigurationSecret()
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/"):
			value = jobs[strings.TrimPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/")]
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
			value = map[string]any{"items": []any{pods[strings.TrimPrefix(r.URL.Query().Get("labelSelector"), "batch.kubernetes.io/controller-uid=")]}}
		default:
			t.Error("unexpected native path", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	return &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client(), token: "synthetic-preclaim"}, namespace: "borealis", controllerHolder: work.Authorization.ControllerHolder,
		actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}, posts
}

func TestClusterSSHPreparationConfigurationNativeCleanup(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "namespace close", "Node close", "Secret close", "Job close", "Pod close", "last Secret close", "cancel during Close", "authority after Close", "sibling isolation", "request timeout during Close"} {
		t.Run(mode, func(t *testing.T) {
			work := sshPreparationConfigurationFixture(t, mode == "replacement")
			runner, posts := sshPreparationConfigurationNativeFixture(t, work)
			client, originalKube := runner.kube.httpClient, runner.kube
			underlying := client.Transport
			client.Timeout = 7 * time.Second
			var active, requests, secrets, checksAfter atomic.Int64
			var fired, lost atomic.Bool
			closed, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			transport := &cohortMembershipTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				response, err := underlying.RoundTrip(r)
				if err != nil || response == nil || response.Body == nil {
					return response, err
				}
				requests.Add(1)
				secret := r.URL.Path == clusterSSHRuntimeSecretPath
				lastSecret := secret && secrets.Add(1)%2 == 0
				match := mode == "namespace close" && r.URL.Path == "/api/v1/namespaces/kube-system" ||
					mode == "Node close" && r.URL.Path == "/api/v1/nodes" ||
					(mode == "Secret close" || mode == "sibling isolation") && secret ||
					mode == "Job close" && r.Method == http.MethodPost ||
					mode == "Pod close" && r.URL.Path == "/api/v1/namespaces/borealis/pods" ||
					(mode == "last Secret close" || mode == "cancel during Close" || mode == "authority after Close" || mode == "request timeout during Close") && lastSecret
				active.Add(1)
				response.Body = &cohortMembershipBody{ReadCloser: response.Body, close: func() error {
					defer active.Add(-1)
					if match && fired.CompareAndSwap(false, true) {
						if mode == "cancel during Close" {
							close(closed)
							<-release
						}
						if mode == "request timeout during Close" {
							<-r.Context().Done()
							return nil
						}
						if mode == "authority after Close" {
							lost.Store(true)
							return nil
						}
						return errors.New("synthetic owned response Close failure")
					}
					return nil
				}}
				return response, nil
			}}
			client.Transport = transport
			check := func(checkCtx context.Context) error {
				if active.Load() != 0 {
					t.Error("proposal check before owned response Close joined")
				}
				if secrets.Load() >= 2 {
					checksAfter.Add(1)
				}
				if lost.Load() {
					return errClusterConflict
				}
				return checkCtx.Err()
			}
			read := newClusterSSHPreparationConfigurationNativeRead(work, check, runner)
			plan, _ := clusterSSHPreparationPlanDigest(work.Authorization)
			var digest string
			var err error
			if mode == "cancel during Close" {
				done := make(chan struct{})
				go func() { digest, err = read(ctx, plan); close(done) }()
				defer func() {
					cancel()
					unblock.Do(func() { close(release) })
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("configuration reader survived test cleanup")
					}
				}()
				select {
				case <-closed:
				case <-done:
					t.Fatal("reader returned before owned Close barrier", err)
				case <-ctx.Done():
					t.Fatal("reader never reached owned Close barrier")
				}
				cancel()
				select {
				case <-done:
					t.Fatal("cancellation escaped blocked owned Close")
				case <-time.After(25 * time.Millisecond):
				}
				unblock.Do(func() { close(release) })
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("reader did not join released Close")
				}
			} else {
				digest, err = read(ctx, plan)
			}
			if mode == "request timeout during Close" && ctx.Err() != nil {
				t.Fatal("fixture canceled outer scope instead of child request")
			}
			valid := mode == "pair" || mode == "replacement"
			if (err == nil) != valid || valid && !validClusterSSHPreparationDigest(digest) || !valid && digest != "" {
				t.Fatal("unconfirmed native cleanup exported configuration digest", mode, err)
			}
			if valid && (posts.Load() != int64(2*len(work.Proof.Source.Members)) || checksAfter.Load() == 0) {
				t.Fatal("fresh Job or post-cleanup proposal check missing")
			}
			if !valid {
				before := requests.Load()
				if !fired.Load() {
					t.Fatal("fixture did not reach intended cleanup boundary")
				}
				if value, err := read(context.Background(), plan); err == nil || value != "" || requests.Load() != before {
					t.Fatal("failed native reader was not permanently poisoned")
				}
			}
			if mode == "sibling isolation" {
				sibling := newClusterSSHPreparationConfigurationNativeRead(work, check, runner)
				if value, err := sibling(ctx, plan); err != nil || !validClusterSSHPreparationDigest(value) {
					t.Fatal("reader cleanup poisoned shared transport or sibling", err)
				}
			}
			if active.Load() != 0 || client.Transport != transport || runner.kube != originalKube || runner.kube.httpClient != client || client.Timeout != 7*time.Second || transport.idleCalls.Load() != 0 {
				t.Fatal("native cleanup escaped or changed shared client ownership")
			}
		})
	}
}
