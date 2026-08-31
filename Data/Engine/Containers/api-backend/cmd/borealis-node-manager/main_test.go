package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNodeManagerActionTimeoutAllowsBoundedBootstrapAndRedeploy(t *testing.T) {
	tests := []struct {
		verb string
		want time.Duration
	}{
		{verb: "EnrollCluster", want: 90 * time.Minute},
		{verb: "StageRevisionImages", want: 60 * time.Minute},
		{verb: "RedeployRevision", want: 60 * time.Minute},
		{verb: "RedeployStagedRevision", want: 60 * time.Minute},
		{verb: "PromoteCandidate", want: 60 * time.Minute},
		{verb: "RunSchemaPhase", want: 20 * time.Minute},
		{verb: "Status", want: 30 * time.Minute},
	}
	for _, test := range tests {
		if got := nodeManagerActionTimeout(test.verb); got != test.want {
			t.Fatalf("nodeManagerActionTimeout(%q)=%s want %s", test.verb, got, test.want)
		}
	}
}

func TestNodeActionPodsActiveWaitsForRunningOrUnknownWork(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty", raw: `{"items":[]}`, want: false},
		{name: "completed", raw: `{"items":[{"status":{"phase":"Succeeded"}},{"status":{"phase":"Failed"}}]}`, want: false},
		{name: "running", raw: `{"items":[{"status":{"phase":"Running"}}]}`, want: true},
		{name: "pending", raw: `{"items":[{"status":{"phase":"Pending"}}]}`, want: true},
		{name: "unknown fails closed", raw: `{"items":[{"status":{"phase":"Unknown"}}]}`, want: true},
		{name: "missing phase fails closed", raw: `{"items":[{"status":{}}]}`, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nodeActionPodsActive([]byte(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("nodeActionPodsActive()=%v want %v", got, test.want)
			}
		})
	}
	if _, err := nodeActionPodsActive([]byte(`{"items":`)); err == nil {
		t.Fatal("invalid pod JSON accepted")
	}
}

func TestWaitForActiveNodeManagerExecutableRequiresRunningInodeAndSocket(t *testing.T) {
	tempDir := t.TempDir()
	systemctl := filepath.Join(tempDir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$BOREALIS_TEST_MAIN_PID\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(tempDir, "node-manager.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BOREALIS_TEST_MAIN_PID", strconv.Itoa(os.Getpid()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForActiveNodeManagerExecutable(ctx, "test.service", executable, socketPath); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForNodeActionIdleRequiresTwoIdleObservations(t *testing.T) {
	tempDir := t.TempDir()
	counter := filepath.Join(tempDir, "counter")
	if err := os.WriteFile(counter, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k3s := filepath.Join(tempDir, "k3s")
	script := `#!/bin/sh
count=$(cat "$BOREALIS_TEST_COUNTER")
count=$((count + 1))
printf '%s\n' "$count" > "$BOREALIS_TEST_COUNTER"
if [ "$count" -eq 1 ]; then
  printf '%s\n' '{"items":[{"status":{"phase":"Running"}}]}'
else
  printf '%s\n' '{"items":[]}'
fi
`
	if err := os.WriteFile(k3s, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BOREALIS_TEST_COUNTER", counter)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForNodeActionIdleWithInterval(ctx, "engine-1", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "3" {
		t.Fatalf("node action idle observations=%q want 3", strings.TrimSpace(string(raw)))
	}
}

func TestRequiredSchemaPhaseAcceptsOnlyFixedValues(t *testing.T) {
	for _, phase := range []string{"expand", "finalize"} {
		if got := requiredSchemaPhase(map[string]any{"schema_phase": phase}); got != phase {
			t.Fatalf("requiredSchemaPhase(%q)=%q", phase, got)
		}
	}
	for _, phase := range []string{"", "contract", "expand; touch /tmp/unsafe"} {
		if got := requiredSchemaPhase(map[string]any{"schema_phase": phase}); got != "" {
			t.Fatalf("unsafe schema phase %q accepted as %q", phase, got)
		}
	}
}

func TestAgentPathProbeRequiresExpectedAuthenticationBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/api/agent/heartbeat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, request)
	}))
	defer server.Close()
	address := server.Listener.Addr().(*net.TCPAddr)
	if err := probeHTTPExpectedStatus(context.Background(), address.IP.String(), address.Port, http.MethodPost, "/api/agent/heartbeat", http.StatusUnauthorized); err != nil {
		t.Fatalf("Agent path probe failed: %v", err)
	}
	if err := probeHTTPExpectedStatus(context.Background(), address.IP.String(), address.Port, http.MethodGet, "/missing", http.StatusUnauthorized); err == nil {
		t.Fatal("missing Agent path accepted as healthy")
	}
}

func TestNodeHealthParsersRequireReadyNodeAndWorkloads(t *testing.T) {
	if !nodeReady([]byte(`{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`)) {
		t.Fatal("expected Ready=True node")
	}
	if nodeReady([]byte(`{"status":{"conditions":[{"type":"Ready","status":"False"}]}}`)) {
		t.Fatal("expected Ready=False node rejection")
	}
	if !nodeLabelTrue([]byte(`{"metadata":{"labels":{"borealis.io/edge-eligible":"true"}}}`), "borealis.io/edge-eligible") {
		t.Fatal("expected edge eligibility label")
	}
	if got := nodeLabelValue([]byte(`{"metadata":{"labels":{"borealis.io/application-state":"drained"}}}`), "borealis.io/application-state"); got != "drained" {
		t.Fatalf("application state=%q want drained", got)
	}
	workloads, err := readyNodeWorkloads([]byte(`{"items":[{"metadata":{"labels":{"app.kubernetes.io/name":"api-backend"}},"spec":{"replicas":1},"status":{"availableReplicas":1,"readyReplicas":1,"updatedReplicas":1}},{"metadata":{"labels":{"app.kubernetes.io/name":"job-scheduler"}},"spec":{"replicas":1},"status":{"availableReplicas":0,"readyReplicas":0,"updatedReplicas":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !workloads["api-backend"] || workloads["job-scheduler"] {
		t.Fatalf("unexpected workload readiness: %#v", workloads)
	}
}

func TestControlVIPEligibilityFollowsApplicationMaintenanceState(t *testing.T) {
	eligibility, err := controlVIPEligibilityByNode([]byte(`{"items":[{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"active"}}},{"metadata":{"name":"engine-2","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"drained"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if eligibility["engine-1"] != "true" || eligibility["engine-2"] != "false" {
		t.Fatalf("unexpected Control VIP eligibility: %#v", eligibility)
	}
	for _, raw := range []string{
		`{"items":[]}`,
		`{"items":[{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"unknown"}}}]}`,
		`{"items":[{"metadata":{"name":"invalid_name","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"active"}}}]}`,
	} {
		if _, err := controlVIPEligibilityByNode([]byte(raw)); err == nil {
			t.Fatalf("unsafe Control VIP eligibility payload accepted: %s", raw)
		}
	}
}

func TestReconcileVIPPlacementLabelsNodesBeforeApplyingControlSelector(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "k3s.log")
	k3s := filepath.Join(tempDir, "k3s")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$BOREALIS_TEST_K3S_LOG"
case "$*" in
  "kubectl get nodes -l borealis.io/engine-node=true -o json")
    printf '%s\n' '{"items":[{"metadata":{"name":"engine-2","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"drained"}}},{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true","borealis.io/application-state":"active"}}}]}'
    ;;
  "kubectl label node engine-1 borealis.io/control-plane-eligible=true --overwrite"|"kubectl label node engine-2 borealis.io/control-plane-eligible=false --overwrite")
    ;;
  "kubectl -n kube-system patch daemonset/kube-vip-borealis-control --type=merge -p "*)
    ;;
  "kubectl -n kube-system rollout status daemonset/kube-vip-borealis-control --timeout=3m")
    ;;
  *)
    printf 'unexpected k3s arguments: %s\n' "$*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(k3s, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BOREALIS_TEST_K3S_LOG", logPath)
	manager := &manager{nodeName: "engine-1"}
	result, err := manager.reconcileVIPPlacement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result["control_vip_selector"] != "borealis.io/control-plane-eligible=true" {
		t.Fatalf("unexpected VIP reconciliation result: %#v", result)
	}
	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(rawLog)
	activeLabel := strings.Index(log, "kubectl label node engine-1 borealis.io/control-plane-eligible=true --overwrite")
	drainedLabel := strings.Index(log, "kubectl label node engine-2 borealis.io/control-plane-eligible=false --overwrite")
	selectorPatch := strings.Index(log, "kubectl -n kube-system patch daemonset/kube-vip-borealis-control")
	rolloutWait := strings.Index(log, "kubectl -n kube-system rollout status daemonset/kube-vip-borealis-control --timeout=3m")
	if activeLabel < 0 || drainedLabel < 0 || selectorPatch < 0 || rolloutWait < 0 || activeLabel > selectorPatch || drainedLabel > selectorPatch || selectorPatch > rolloutWait {
		t.Fatalf("unsafe Control VIP selector migration order: %s", log)
	}
}

func TestPrepareApplicationRestoreIsRetrySafeAfterActivation(t *testing.T) {
	tempDir := t.TempDir()
	k3s := filepath.Join(tempDir, "k3s")
	script := `#!/bin/sh
case "$*" in
  "kubectl get node engine-1 -o json")
    printf '{"metadata":{"labels":{"borealis.io/application-state":"%s"}}}\n' "$BOREALIS_TEST_APPLICATION_STATE"
    ;;
  "kubectl -n borealis get deployments "*)
    printf '%s\n' '{"items":[]}'
    ;;
  *)
    printf 'unexpected k3s arguments: %s\n' "$*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(k3s, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	manager := &manager{nodeName: "engine-1"}
	for _, state := range []string{"drained", "active"} {
		t.Setenv("BOREALIS_TEST_APPLICATION_STATE", state)
		result, err := manager.prepareApplicationRestore(context.Background())
		if err != nil {
			t.Fatalf("prepare restore rejected %s retry state: %v", state, err)
		}
		if result["application_state"] != state || result["already_active"] != (state == "active") {
			t.Fatalf("unexpected %s restore result: %#v", state, result)
		}
	}
	t.Setenv("BOREALIS_TEST_APPLICATION_STATE", "standby")
	if _, err := manager.prepareApplicationRestore(context.Background()); err == nil {
		t.Fatal("prepare restore accepted unsupported application state")
	}
}

func TestShutdownHandoffRunsOnlyWhileSystemIsStopping(t *testing.T) {
	if !systemIsStopping([]byte("stopping\n")) {
		t.Fatal("system stopping state was not recognized")
	}
	for _, state := range []string{"running", "degraded", "maintenance", "", "stopping now"} {
		if systemIsStopping([]byte(state)) {
			t.Fatalf("unsafe system state %q enabled shutdown handoff", state)
		}
	}
}

func TestShutdownHandoffParsersRequireReadyEnginePeerAndLeaseHolder(t *testing.T) {
	raw := []byte(`{"items":[{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"engine-2","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"engine-3","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"False"}]}},{"metadata":{"name":"worker-1","labels":{}},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`)
	peers, err := readyEnginePeers(raw, "engine-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(peers, ",") != "engine-2" {
		t.Fatalf("ready Engine peers=%v want [engine-2]", peers)
	}
	if _, err := readyEnginePeers([]byte(`{"items":`), "engine-1"); err == nil {
		t.Fatal("invalid node list JSON accepted")
	}
	holder, err := leaseHolder([]byte(`{"spec":{"holderIdentity":"engine-2"}}`))
	if err != nil || holder != "engine-2" {
		t.Fatalf("lease holder=%q err=%v", holder, err)
	}
	if _, err := leaseHolder([]byte(`{"spec":`)); err == nil {
		t.Fatal("invalid lease JSON accepted")
	}
}

func TestPerformShutdownHandoffWithdrawsEngineNodeAndWaitsForBothVIPs(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "k3s.log")
	k3s := filepath.Join(tempDir, "k3s")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$BOREALIS_TEST_K3S_LOG"
case "$*" in
  "kubectl get nodes -o json")
    printf '%s\n' '{"items":[{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"engine-2","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}'
    ;;
  "kubectl label node engine-1 borealis.io/engine-node=false --overwrite")
    printf '%s\n' 'node/engine-1 labeled'
    ;;
  "kubectl -n kube-system get lease borealis-control-vip -o json"|"kubectl -n kube-system get lease borealis-edge-vip -o json")
    printf '%s\n' '{"spec":{"holderIdentity":"engine-2"}}'
    ;;
  *)
    exit 64
    ;;
esac
`
	if err := os.WriteFile(k3s, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BOREALIS_TEST_K3S_LOG", logPath)
	manager := &manager{nodeName: "engine-1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := manager.performShutdownHandoff(ctx, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result["handed_off"] != true {
		t.Fatalf("unexpected shutdown handoff result: %#v", result)
	}
	rawLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(rawLog)
	for _, command := range []string{
		"kubectl label node engine-1 borealis.io/engine-node=false --overwrite",
		"kubectl -n kube-system get lease borealis-control-vip -o json",
		"kubectl -n kube-system get lease borealis-edge-vip -o json",
	} {
		if !strings.Contains(log, command) {
			t.Fatalf("shutdown handoff missed %q: %s", command, log)
		}
	}
}

func TestPerformShutdownHandoffSkipsWithoutReadyEnginePeer(t *testing.T) {
	tempDir := t.TempDir()
	k3s := filepath.Join(tempDir, "k3s")
	script := `#!/bin/sh
if [ "$*" = "kubectl get nodes -o json" ]; then
  printf '%s\n' '{"items":[{"metadata":{"name":"engine-1","labels":{"borealis.io/engine-node":"true"}},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}'
  exit 0
fi
exit 64
`
	if err := os.WriteFile(k3s, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	manager := &manager{nodeName: "engine-1"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := manager.performShutdownHandoff(ctx, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result["handed_off"] != false || result["reason"] != "no_ready_engine_peer" {
		t.Fatalf("unexpected one-node shutdown result: %#v", result)
	}
}

func TestNodeHealthParsersRequireNodeScopedReadyEndpoint(t *testing.T) {
	host, port, err := readyAPIEndpoint([]byte(`{"items":[{"ports":[{"port":5001}],"endpoints":[{"addresses":["10.42.1.8"],"nodeName":"engine-1","conditions":{"ready":true}},{"addresses":["10.42.2.8"],"nodeName":"engine-2","conditions":{"ready":false}}]}]}`), "engine-1")
	if err != nil || host != "10.42.1.8" || port != 5001 {
		t.Fatalf("unexpected endpoint result host=%q port=%d err=%v", host, port, err)
	}
	if _, _, err := readyAPIEndpoint([]byte(`{"items":[]}`), "engine-1"); err == nil {
		t.Fatal("expected missing endpoint rejection")
	}
}

func TestNodeHealthParsersRequireReadyPostgresAndValidService(t *testing.T) {
	if !podListHasReadyPod([]byte(`{"items":[{"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`)) {
		t.Fatal("expected ready PostgreSQL pod")
	}
	if podListHasReadyPod([]byte(`{"items":[]}`)) {
		t.Fatal("expected empty PostgreSQL list rejection")
	}
	host, port, err := apiServiceAddress([]byte(`{"spec":{"clusterIP":"10.43.0.8","ports":[{"port":5001}]}}`))
	if err != nil || host != "10.43.0.8" || port != 5001 {
		t.Fatalf("unexpected service result host=%q port=%d err=%v", host, port, err)
	}
}

func TestClusterEdgeVIPRequiresPrivateIPv4(t *testing.T) {
	got, err := clusterEdgeVIP([]byte(`{"spec":{"edgeVIP":"192.168.3.248"}}`))
	if err != nil || got != "192.168.3.248" {
		t.Fatalf("private edge VIP rejected got=%q err=%v", got, err)
	}
	for _, payload := range []string{`{"spec":{"edgeVIP":"8.8.8.8"}}`, `{"spec":{"edgeVIP":"2001:db8::1"}}`, `{}`} {
		if _, err := clusterEdgeVIP([]byte(payload)); err == nil {
			t.Fatalf("unsafe edge VIP accepted: %s", payload)
		}
	}
}

func TestCandidateHealthParsersRequireReadyAndServiceIsolation(t *testing.T) {
	workloads, allReady, err := readyCandidateWorkloads([]byte(`{"items":[{"metadata":{"labels":{"app.kubernetes.io/name":"api-backend-candidate","borealis.io/update-candidate":"true"}},"spec":{"replicas":1},"status":{"availableReplicas":1,"readyReplicas":1,"updatedReplicas":1}},{"metadata":{"labels":{"app.kubernetes.io/name":"job-scheduler-candidate","borealis.io/update-candidate":"true"}},"spec":{"replicas":1},"status":{"availableReplicas":1,"readyReplicas":1,"updatedReplicas":1}}]}`))
	if err != nil || !allReady || !workloads["api-backend-candidate"] || !workloads["job-scheduler-candidate"] {
		t.Fatalf("unexpected candidate readiness workloads=%#v allReady=%v err=%v", workloads, allReady, err)
	}
	address, port, err := readyCandidateAPIEndpoint([]byte(`{"items":[{"metadata":{"labels":{"app.kubernetes.io/name":"api-backend-candidate","borealis.io/update-candidate":"true"}},"spec":{"containers":[{"ports":[{"name":"http","containerPort":5001}]}]},"status":{"podIP":"10.42.3.7","conditions":[{"type":"Ready","status":"True"}]}}]}`))
	if err != nil || address != "10.42.3.7" || port != 5001 {
		t.Fatalf("unexpected candidate endpoint address=%q port=%d err=%v", address, port, err)
	}
	isolatedSlices := []byte(`{"items":[{"endpoints":[{"addresses":["10.42.1.8"]}]}]}`)
	if endpointSliceContainsAddress(isolatedSlices, address) {
		t.Fatal("candidate should remain absent from shared Service")
	}
	leakedSlices := []byte(`{"items":[{"endpoints":[{"addresses":["10.42.3.7"]}]}]}`)
	if !endpointSliceContainsAddress(leakedSlices, address) {
		t.Fatal("candidate Service leak was not detected")
	}
}

func TestSupportedUbuntuReleaseRequiresUbuntu2404OrNewer(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "ubuntu 24.04", content: "ID=ubuntu\nVERSION_ID=\"24.04\"\n", want: true},
		{name: "ubuntu 26.04", content: "ID='ubuntu'\nVERSION_ID='26.04'\n", want: true},
		{name: "ubuntu 22.04", content: "ID=ubuntu\nVERSION_ID=22.04\n", want: false},
		{name: "debian", content: "ID=debian\nVERSION_ID=24\n", want: false},
		{name: "missing version", content: "ID=ubuntu\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := supportedUbuntuRelease([]byte(test.content)); got != test.want {
				t.Fatalf("supportedUbuntuRelease()=%v want %v", got, test.want)
			}
		})
	}
}

func TestK3sRegistryMirrorsCoverBorealisAndPinnedDependencies(t *testing.T) {
	config := string(k3sRegistryMirrorsConfig())
	for _, registry := range []string{"docker.io:", "ghcr.io:", "quay.io:", "registry.k8s.io:"} {
		if !strings.Contains(config, "  "+registry) {
			t.Fatalf("K3s registry mirror config missing %s", registry)
		}
	}
	if strings.Contains(config, `"*":`) {
		t.Fatal("K3s registry mirror config must use explicit source registries")
	}
}

func TestJoinedK3sRuntimeLabelsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "20-borealis-cluster-join.yaml")
	initial := "server: \"https://192.168.3.249:6443\"\nnode-label:\n  - borealis.io/engine-node=true\n  - borealis.io/application-state=drained\n  - borealis.io/control-plane-eligible=false\n  - borealis.io/edge-eligible=false\n  - borealis.io/scheduler-eligible=false\n  - borealis.io/postgres-primary-eligible=false\n"
	if err := os.WriteFile(path, []byte(initial), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := persistJoinedK3sRuntimeLabels(path, "active"); err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{
		"borealis.io/application-state=active",
		"borealis.io/control-plane-eligible=true",
		"borealis.io/edge-eligible=true",
		"borealis.io/scheduler-eligible=true",
		"borealis.io/postgres-primary-eligible=true",
	} {
		if !strings.Contains(string(active), label) {
			t.Fatalf("active joined config missing %q: %s", label, string(active))
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("joined config mode=%v, want 0600", info.Mode().Perm())
	}
	if err := persistJoinedK3sRuntimeLabels(path, "drained"); err != nil {
		t.Fatal(err)
	}
	drained, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(drained), "borealis.io/application-state=drained") || strings.Count(string(drained), "-eligible=false") != 4 {
		t.Fatalf("drained joined config is unsafe: %s", string(drained))
	}
}

func TestJoinedK3sRuntimeLabelsRejectMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "20-borealis-cluster-join.yaml")
	if err := os.WriteFile(path, []byte("node-label:\n  - borealis.io/application-state=drained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persistJoinedK3sRuntimeLabels(path, "active"); err == nil {
		t.Fatal("joined config missing eligibility labels was accepted")
	}
	if err := persistJoinedK3sRuntimeLabels(filepath.Join(t.TempDir(), "missing.yaml"), "active"); err != nil {
		t.Fatalf("foundational node missing joined config should be accepted: %v", err)
	}
	if err := persistJoinedK3sRuntimeLabels(path, "unknown"); err == nil {
		t.Fatal("invalid application state was accepted")
	}
}

func TestJoinedK3sRuntimeLabelsMigratesMissingControlVIPEligibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "20-borealis-cluster-join.yaml")
	legacy := "server: \"https://192.0.2.10:6443\"\nnode-label:\n  - borealis.io/engine-node=true\n  - borealis.io/application-state=active\n  - borealis.io/edge-eligible=true\n  - borealis.io/scheduler-eligible=true\n  - borealis.io/postgres-primary-eligible=true\nnode-taint:\n  - example.invalid/test=true:NoSchedule\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persistJoinedK3sRuntimeLabels(path, "drained"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Count(text, "borealis.io/control-plane-eligible=false") != 1 {
		t.Fatalf("legacy joined config did not receive one safe Control VIP label: %s", text)
	}
	if strings.Index(text, "borealis.io/control-plane-eligible=false") > strings.Index(text, "node-taint:") {
		t.Fatalf("Control VIP label escaped node-label block: %s", text)
	}
}

func TestNormalizePeerCIDRsRequiresBoundedPrivateIPv4Networks(t *testing.T) {
	got, err := normalizePeerCIDRs("192.168.10.2/24, 10.0.0.8/32,192.168.10.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if got != "192.168.10.0/24,10.0.0.8/32" {
		t.Fatalf("normalized peer CIDRs=%q", got)
	}
	for _, value := range []string{"", "192.168.10.2", "8.8.8.8/32", "10.0.0.0/7", "2001:db8::/64", strings.Repeat("10.0.0.1/32,", 17)} {
		if normalized, err := normalizePeerCIDRs(value); err == nil {
			t.Fatalf("unsafe peer CIDRs accepted as %q from %q", normalized, value)
		}
	}
}

func TestEnvironmentWithValueReplacesInheritedPeerAllowlist(t *testing.T) {
	got := environmentWithValue([]string{"PATH=/usr/bin", "BOREALIS_K3S_PEER_CIDRS=10.0.0.1/32"}, "BOREALIS_K3S_PEER_CIDRS", "192.168.10.1/32")
	if strings.Join(got, "\n") != "PATH=/usr/bin\nBOREALIS_K3S_PEER_CIDRS=192.168.10.1/32" {
		t.Fatalf("unexpected prepared environment: %#v", got)
	}
}

func TestMemberRemovalFenceUsesPersistentNarrowMarkerPath(t *testing.T) {
	if memberFencePath != "/etc/borealis/k3s-member-removal-fence.json" {
		t.Fatalf("unexpected member fence path %q", memberFencePath)
	}
	if memberFenceDropIn != "/etc/systemd/system/k3s.service.d/90-borealis-member-removal-fence.conf" {
		t.Fatalf("unexpected member fence drop-in %q", memberFenceDropIn)
	}
	if filepath.Dir(memberFenceDropIn) != "/etc/systemd/system/k3s.service.d" {
		t.Fatalf("unexpected member fence drop-in directory %q", filepath.Dir(memberFenceDropIn))
	}
	if got := requiredReason(map[string]any{"reason": "line one\nline two"}); got != "" {
		t.Fatalf("multiline removal reason accepted: %q", got)
	}
	if got := strings.Join(memberRemovalFenceSystemctlArgs(), " "); got != "disable k3s.service" {
		t.Fatalf("member removal fence must disable restart without stopping K3s: %q", got)
	}
}

func TestK3sConformanceVerbRequiresStableVersion(t *testing.T) {
	if got := requiredK3sVersion(map[string]any{"k3s_version": "v1.36.3+k3s1"}); got != "v1.36.3+k3s1" {
		t.Fatalf("stable K3s version rejected: %q", got)
	}
	for _, value := range []string{"", "v1.36.3-rc1+k3s1", "latest", "v1.36.3+k3s1\nnext"} {
		if got := requiredK3sVersion(map[string]any{"k3s_version": value}); got != "" {
			t.Fatalf("unsafe K3s version accepted: %q", got)
		}
	}
}

func TestEtcdLeadershipMetricsRequireExactBooleanGauge(t *testing.T) {
	leader, observed := parseEtcdLeadership([]byte("# HELP etcd_server_is_leader Whether local member is leader.\netcd_server_is_leader 1\n"))
	if !observed || !leader {
		t.Fatal("etcd leader gauge was not recognized")
	}
	leader, observed = parseEtcdLeadership([]byte("etcd_server_is_leader 0\n"))
	if !observed || leader {
		t.Fatal("etcd follower gauge was not recognized")
	}
	if _, observed := parseEtcdLeadership([]byte("etcd_server_is_leader NaN\n")); observed {
		t.Fatal("invalid etcd leadership gauge accepted")
	}
}

func TestTruncateDiagnosticPreservesFailureContext(t *testing.T) {
	value := "command start:" + strings.Repeat("x", 100) + ":fatal tail"
	got := truncateDiagnostic(value, 48)
	if len(got) != 48 {
		t.Fatalf("unexpected diagnostic length %d", len(got))
	}
	if !strings.HasPrefix(got, "command") || !strings.HasSuffix(got, ":fatal tail") {
		t.Fatalf("diagnostic lost head or tail: %q", got)
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("diagnostic lacks truncation marker: %q", got)
	}
}

func TestRunGitScopesSafeDirectoryToSelectedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), root, "init"); err != nil {
		t.Fatal(err)
	}
	output, err := runGit(context.Background(), root, "config", "--get", "safe.directory")
	if err != nil || strings.TrimSpace(output) != root {
		t.Fatalf("safe.directory=%q err=%v, want %q", strings.TrimSpace(output), err, root)
	}
	if _, err := runGit(context.Background(), "", "status"); err == nil {
		t.Fatal("empty Git worktree path accepted")
	}
}

func TestEngineChildScopesGitSafeDirectoryWithoutGlobalMutation(t *testing.T) {
	got := gitSafeDirectoryEnvironment("/opt/Borealis")
	want := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=/opt/Borealis",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected Engine child Git environment: %#v", got)
	}
}

func TestEngineChildUsesManagerOwnedWritableHome(t *testing.T) {
	root := t.TempDir()
	manager := &manager{repoRoot: root}
	got, err := manager.engineChildEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "Engine", "Deploy", "node-manager-home")
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("Engine child home missing: %v", err)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "HOME="+home) || !strings.Contains(joined, "XDG_CACHE_HOME="+filepath.Join(home, ".cache")) {
		t.Fatalf("unexpected Engine child environment: %#v", got)
	}
}

func TestStagedProductionRestoreBuildsIsolatedCandidate(t *testing.T) {
	root := "/opt/Borealis"
	got := strings.Join(stagedRedeployEnvironment([]string{"HOME=/tmp/test"}, root), "\n")
	for _, required := range []string{
		"BOREALIS_ENGINE_HOST_ROOT=" + root,
		"BOREALIS_ENGINE_RUNTIME_ROOT=" + filepath.Join(root, "Engine"),
		"BOREALIS_CLUSTER_DEPLOYMENT_MODE=candidate",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("staged restore environment missing %q: %s", required, got)
		}
	}
}

func TestEnsureSecureDirectoryCorrectsExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "borealis")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureSecureDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("directory mode=%#o, want 0750", got)
	}
}

func TestReplaceExecutableUsesAtomicInodeReplacement(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "borealis-node-manager")
	if err := os.WriteFile(destination, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	openOld, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer openOld.Close()
	oldInfo, err := openOld.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(destination, []byte("new")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "new" {
		t.Fatalf("replacement content=%q err=%v", string(content), err)
	}
	newInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldInfo, newInfo) || newInfo.Mode().Perm() != 0o750 {
		t.Fatalf("executable was not atomically replaced with mode 0750: %#o", newInfo.Mode().Perm())
	}
}
