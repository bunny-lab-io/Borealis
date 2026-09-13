package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sshVIPFixture(t *testing.T, replacement bool) (clusterSSHPreparationAuthority, []clusterbootstrap.SourceNetwork, clusterbootstrap.VIPLease) {
	t.Helper()
	_, a, _ := sshBrokerFixture(t)
	a.Source.EdgeVIP = a.Source.ControlPlaneVIP
	if replacement {
		item := a.Cohort.Targets[1]
		a.Source.Members = append(a.Source.Members, clusterSSHSourceMember{NodeID: item.Binding.TargetID, NodeUID: newClusterUUID(), Name: item.Report.Hostname, Address: item.Binding.Address, MachineID: item.Report.MachineID, BootID: item.Report.BootID, SSHFingerprint: item.Key.Fingerprint})
		a.Cohort.Targets = a.Cohort.Targets[:1]
		a.Source.ActiveSize, a.Source.DesiredSize, a.Source.Status = 2, 3, "Degraded Quorum"
	}
	var networks []clusterbootstrap.SourceNetwork
	for _, member := range a.Source.Members {
		networks = append(networks, sshSourceNetworkFixture(member, a.K3sVersion))
	}
	lease := clusterbootstrap.VIPLease{UID: newClusterUUID(), ResourceVersion: "opaque-1", Holder: a.Source.Members[len(a.Source.Members)-1].Name, AcquireTime: "2000-01-01T00:00:00Z", RenewTime: "2000-01-01T00:00:02Z", Transitions: 1, DurationSeconds: 10}
	return a, networks, lease
}

func sshVIPObservation(n clusterbootstrap.SourceNetwork, address string, present bool) clusterSSHSourceVIPObservation {
	vip := clusterbootstrap.VIPAddress{Address: address, Present: present, NetworkNamespace: n.ManagementLink.NetworkNamespace}
	if present {
		vip.Interface = n.ManagementLink.Interface
		vip.Index = n.ManagementLink.Index
		vip.MAC = n.ManagementLink.MAC
	}
	return clusterSSHSourceVIPObservation{value: clusterbootstrap.SourceVIPNetwork{Network: n, VIP: vip}, started: time.Now()}
}

func TestClusterSSHVIPOwnerCompleteFreshScope(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "closed scope", "consumer copy", "input copy", "distinct VIPs", "no owner", "duplicate owners", "wrong actual owner", "foreign holder", "missing sources", "stale observation", "future observation", "read error", "wrong VIP", "MAC drift", "boot drift", "Node drift", "namespace drift", "CIDR drift", "source changed after consume", "authority changed", "authority failed", "lease UID changed", "lease epoch changed", "lease holder changed", "lease acquired changed", "lease read failed", "consumer error", "cancel source", "cancel consume", "ignored input failure", "ignored authority failure"} {
		t.Run(mode, func(t *testing.T) {
			a, networks, lease := sshVIPFixture(t, mode != "expansion")
			original := slices.Clone(networks)
			if mode == "distinct VIPs" {
				a.Source.EdgeVIP = "192.168.90.249"
			}
			if mode == "missing sources" {
				networks = networks[:1]
			}
			var reads, leaseReads, consumed atomic.Int64
			var forceFailure atomic.Bool
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				v := a
				v.Cohort.ObservedAt += leaseReads.Load()
				if reads.Load() > 0 {
					if mode == "authority changed" {
						v.Lease.Generation++
					}
					if mode == "authority failed" {
						return v, errors.New("private SQL")
					}
				}
				return v, nil
			}
			readLease := func(ctx context.Context) (clusterbootstrap.VIPLease, error) {
				count := leaseReads.Add(1)
				v := lease
				v.ResourceVersion = fmt.Sprintf("opaque-%d", count)
				v.RenewTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(count+2) * time.Second).Format(time.RFC3339Nano)
				if mode == "foreign holder" {
					v.Holder = "foreign-node"
				}
				if reads.Load() > 0 {
					switch mode {
					case "lease UID changed":
						v.UID = a.Source.KubeSystemUID
					case "lease epoch changed":
						v.Transitions++
					case "lease holder changed":
						v.Holder = a.Source.Members[0].Name
					case "lease acquired changed":
						v.AcquireTime = "2000-01-01T00:00:01Z"
					case "lease read failed":
						return v, errors.New("private Kubernetes")
					}
				}
				if mode == "ignored authority failure" && forceFailure.Swap(false) {
					return v, errors.New("private failure")
				}
				return v, nil
			}
			readVIP := func(ctx context.Context, member clusterSSHSourceMember) (clusterSSHSourceVIPObservation, error) {
				count := reads.Add(1)
				index := slices.IndexFunc(a.Source.Members, func(m clusterSSHSourceMember) bool { return m == member })
				if index < 0 {
					t.Fatal("foreign source requested")
				}
				present := member.Name == lease.Holder
				if mode == "no owner" {
					present = false
				}
				if mode == "duplicate owners" {
					present = true
				}
				if mode == "wrong actual owner" {
					present = member.Name != lease.Holder
				}
				v := sshVIPObservation(original[index], a.Source.ControlPlaneVIP, present)
				switch mode {
				case "stale observation":
					v.started = time.Now().Add(-time.Minute)
				case "future observation":
					v.started = time.Now().Add(time.Minute)
				case "read error":
					return v, errors.New("private Job")
				case "wrong VIP":
					v.value.VIP.Address = "192.168.90.249"
				case "MAC drift":
					if count > int64(len(original)) {
						v.value.Network.ManagementLink.MAC = "02:00:00:00:00:ff"
						if present {
							v.value.VIP.MAC = v.value.Network.ManagementLink.MAC
						}
					}
				case "boot drift":
					v.value.Network.BootID = newClusterUUID()
				case "Node drift":
					v.value.Network.NodeUID = newClusterUUID()
				case "namespace drift":
					v.value.Network.ManagementLink.NetworkNamespace++
					v.value.VIP.NetworkNamespace++
				case "CIDR drift":
					v.value.Network.PodCIDR = "10.44.0.0/16"
				case "source changed after consume":
					if consumed.Load() > 0 {
						v.value.Network.ManagementLink.Index++
						if present {
							v.value.VIP.Index++
						}
					}
				case "cancel source":
					cancel()
				case "ignored input failure":
					if forceFailure.Swap(false) {
						return v, errors.New("private transient")
					}
				}
				return v, nil
			}
			var saved clusterSSHPreparationChecks
			consume := func(ctx context.Context, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
				consumed.Add(1)
				saved = checks
				expected := a.Source.Members[len(a.Source.Members)-1]
				if reads.Load() != 2*int64(len(original)) || owner.Owner.ID != expected.NodeID || owner.Owner.NodeUID != expected.NodeUID || owner.Owner.MachineID != expected.MachineID || owner.Owner.BootID != expected.BootID || owner.Owner.Link != original[len(original)-1].ManagementLink || owner.LeaseUID != lease.UID || owner.Transitions != lease.Transitions {
					t.Fatal("incomplete or inferred ownership reached consumer")
				}
				if checks.Authority(ctx) != nil {
					t.Fatal("current authority rejected")
				}
				switch mode {
				case "consumer copy":
					owner.Owner.Link.MAC = "02:00:00:00:00:ff"
				case "input copy":
					networks[0].ManagementLink.Index++
				case "consumer error":
					return errors.New("private consumer")
				case "cancel consume":
					cancel()
				case "ignored input failure":
					forceFailure.Store(true)
					if checks.Inputs(context.Background()) == nil {
						t.Fatal("input failure ignored")
					}
				case "ignored authority failure":
					forceFailure.Store(true)
					if checks.Authority(context.Background()) == nil {
						t.Fatal("authority failure ignored")
					}
				}
				return nil
			}
			err := withClusterSSHVIPOwner(ctx, authority, networks, readVIP, readLease, consume)
			wantOK := slices.Contains([]string{"expansion", "replacement", "closed scope", "consumer copy", "input copy"}, mode)
			if (err == nil) != wantOK {
				t.Fatal("VIP scope result", err)
			}
			if err != nil && err != clusterbootstrap.ErrPreparationConfig && err != clusterbootstrap.ErrSessionAuthority {
				t.Fatal("private error escaped")
			}
			if wantOK && (reads.Load() != 3*int64(len(original)) || consumed.Load() != 1) {
				t.Fatal("post-consumption recheck missing")
			}
			canConsume := wantOK || slices.Contains([]string{"source changed after consume", "consumer error", "cancel consume", "ignored input failure", "ignored authority failure"}, mode)
			if !canConsume && consumed.Load() != 0 {
				t.Fatal("failed acquisition reached consumer")
			}
			if !slices.Contains([]string{"distinct VIPs", "missing sources", "foreign holder"}, mode) && reads.Load() == 0 {
				t.Fatal("test failed before intended observation boundary")
			}
			if mode == "closed scope" {
				before := leaseReads.Load()
				if saved.Inputs(context.Background()) == nil || saved.Authority(context.Background()) == nil || leaseReads.Load() != before {
					t.Fatal("VIP authority survived scope")
				}
			}
		})
	}
}

func TestClusterSSHVIPOwnerExpiryAndJoinedCancellation(t *testing.T) {
	for _, mode := range []string{"no renewal", "expired consumer", "blocked lease", "blocked source"} {
		t.Run(mode, func(t *testing.T) {
			a, networks, lease := sshVIPFixture(t, false)
			var calls atomic.Int64
			var entered, joined atomic.Bool
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }
			readLease := func(ctx context.Context) (clusterbootstrap.VIPLease, error) {
				count := calls.Add(1)
				v := lease
				if mode != "no renewal" && count > 1 {
					v.ResourceVersion = "renewed"
					v.RenewTime = "2000-01-01T00:00:04Z"
				}
				if mode == "blocked lease" && entered.Load() {
					<-ctx.Done()
					joined.Store(true)
					return v, ctx.Err()
				}
				return v, nil
			}
			readVIP := func(ctx context.Context, member clusterSSHSourceMember) (clusterSSHSourceVIPObservation, error) {
				if mode == "blocked source" {
					entered.Store(true)
					<-ctx.Done()
					joined.Store(true)
					return clusterSSHSourceVIPObservation{}, ctx.Err()
				}
				return sshVIPObservation(networks[0], a.Source.ControlPlaneVIP, true), nil
			}
			consume := func(ctx context.Context, _ clusterSSHVIPOwner, _ clusterSSHPreparationChecks) error {
				entered.Store(true)
				<-ctx.Done()
				if mode != "blocked lease" {
					joined.Store(true)
				}
				return nil // Cancellation must fail even when consumer ignores it.
			}
			start := time.Now()
			err := withClusterSSHVIPOwner(context.Background(), authority, networks, readVIP, readLease, consume)
			if err == nil || time.Since(start) > 8*time.Second {
				t.Fatal("unbounded or successful expired scope", err)
			}
			if mode == "no renewal" {
				if entered.Load() {
					t.Fatal("first lease sighting granted work")
				}
			} else if !entered.Load() || !joined.Load() {
				t.Fatal("cancellation returned before callback joined")
			}
		})
	}
}

func TestClusterSSHVIPLeaseFixedHTTPBoundary(t *testing.T) {
	_, _, lease := sshVIPFixture(t, false)
	for _, mode := range []string{"success", "redirect", "plaintext", "error", "oversize", "deadline", "wrong lease"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.RequestURI() != clusterbootstrap.VIPLeasePath || r.Header.Get("Authorization") != "Bearer private-fixture" {
					t.Error("unexpected lease request")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "/private")
					w.WriteHeader(302)
					return
				case "error":
					w.WriteHeader(500)
					w.Write([]byte("private diagnostic"))
					return
				case "oversize":
					w.Write([]byte(strings.Repeat(" ", 65537)))
					return
				case "deadline":
					<-r.Context().Done()
					return
				}
				name := "borealis-cluster-vip"
				if mode == "wrong lease" {
					name = "other"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": name, "namespace": "kube-system", "uid": lease.UID, "resourceVersion": lease.ResourceVersion}, "spec": map[string]any{"holderIdentity": lease.Holder, "leaseDurationSeconds": lease.DurationSeconds, "acquireTime": lease.AcquireTime, "renewTime": lease.RenewTime, "leaseTransitions": lease.Transitions}})
			}))
			defer server.Close()
			client := &kubernetesAPIClient{baseURL: server.URL, token: "private-fixture", httpClient: server.Client()}
			if mode == "plaintext" {
				client.baseURL = strings.Replace(server.URL, "https:", "http:", 1)
			}
			start := time.Now()
			got, err := client.readClusterSSHVIPLease(context.Background())
			if mode == "success" {
				if err != nil || got != lease {
					t.Fatal("lease projection", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got != (clusterbootstrap.VIPLease{}) {
				t.Fatal("unsafe lease response", err)
			}
			if calls.Load() > 1 || (mode == "plaintext" && calls.Load() != 0) || time.Since(start) > 2*time.Second {
				t.Fatal("unbounded request/replay")
			}
		})
	}
}

func TestClusterSSHVIPOwnerThroughFreshTLSJobs(t *testing.T) {
	a, networks, lease := sshVIPFixture(t, true)
	authority := func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }
	var mu sync.Mutex
	jobs := map[string]map[string]any{}
	var renewals, posts, consumed atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-fixture" {
			t.Error("missing controller credential")
		}
		if r.Method == "GET" && r.URL.Path == clusterbootstrap.VIPLeasePath {
			count := renewals.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": "borealis-cluster-vip", "namespace": "kube-system", "uid": lease.UID, "resourceVersion": fmt.Sprintf("opaque-%d", count)}, "spec": map[string]any{"holderIdentity": lease.Holder, "leaseDurationSeconds": 10, "acquireTime": lease.AcquireTime, "renewTime": time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(count+2) * time.Second).Format(time.RFC3339Nano), "leaseTransitions": lease.Transitions}})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
			var job map[string]any
			if json.NewDecoder(r.Body).Decode(&job) != nil {
				t.Error("missing Job")
				w.WriteHeader(400)
				return
			}
			meta := sourceActionMap(job, "metadata")
			name, _ := meta["name"].(string)
			if jobs[name] != nil {
				t.Error("reused Job")
			}
			meta["uid"] = newClusterUUID()
			job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			jobs[name] = job
			posts.Add(1)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(job)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/") {
			name := strings.TrimPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/")
			if jobs[name] == nil {
				t.Error("old or foreign Job")
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(jobs[name])
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/namespaces/borealis/pods" {
			for _, job := range jobs {
				uid := sourceActionMap(job, "metadata")["uid"].(string)
				if r.URL.Query().Get("labelSelector") != "batch.kubernetes.io/controller-uid="+uid {
					continue
				}
				spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
				index := slices.IndexFunc(a.Source.Members, func(m clusterSSHSourceMember) bool { return m.Name == spec["nodeName"] })
				if index < 0 {
					t.Error("foreign source")
					w.WriteHeader(400)
					return
				}
				container := anySlice(spec["containers"])[0].(map[string]any)
				args := clusterStringSlice(container["args"])
				if strings.Join(clusterStringSlice(container["command"]), " ") != "/usr/local/bin/borealis-node-manager source-vip-client" || len(args) != 2 || args[1] != a.Source.ControlPlaneVIP {
					t.Error("unexpected source command")
					w.WriteHeader(400)
					return
				}
				podUID := newClusterUUID()
				pod := sourceActionPod(t, job, networks[index], podUID)
				observation := sshVIPObservation(networks[index], args[1], a.Source.Members[index].Name == lease.Holder)
				raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectVIPNetwork", "result": map[string]any{"source_vip_network": observation.value}})
				receipt, err := clusterbootstrap.NewSourceVIPReceipt(raw, args[0], uid, podUID, args[1])
				if err != nil {
					t.Error("VIP fixture", err)
					w.WriteHeader(500)
					return
				}
				status := anySlice(sourceActionMap(pod, "status")["containerStatuses"])[0].(map[string]any)
				sourceActionMap(sourceActionMap(status, "state"), "terminated")["message"] = string(receipt)
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{pod}})
				return
			}
		}
		t.Error("unexpected request", r.Method, r.URL.Path)
		w.WriteHeader(400)
	}))
	defer server.Close()
	kube := &kubernetesAPIClient{baseURL: server.URL, token: "private-fixture", httpClient: server.Client()}
	runner := &kubernetesClusterStepRunner{kube: kube, namespace: "borealis", controllerHolder: a.Lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
	consume := func(ctx context.Context, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
		consumed.Add(1)
		if posts.Load() != 4 || owner.Owner.Hostname != lease.Holder || owner.Owner.Link.MAC != networks[1].ManagementLink.MAC {
			t.Fatal("incomplete actual ownership")
		}
		return checks.Authority(ctx)
	}
	if err := withClusterSSHVIPOwner(context.Background(), authority, networks, runner.newSSHSourceVIPRead(authority), kube.readClusterSSHVIPLease, consume); err != nil || posts.Load() != 6 || consumed.Load() != 1 {
		t.Fatal("TLS Job scope failed", err, posts.Load())
	}
}
