package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
		{verb: "RedeployRevision", want: 60 * time.Minute},
		{verb: "RedeployStagedRevision", want: 60 * time.Minute},
		{verb: "PromoteCandidate", want: 60 * time.Minute},
		{verb: "Status", want: 30 * time.Minute},
	}
	for _, test := range tests {
		if got := nodeManagerActionTimeout(test.verb); got != test.want {
			t.Fatalf("nodeManagerActionTimeout(%q)=%s want %s", test.verb, got, test.want)
		}
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
	workloads, err := readyNodeWorkloads([]byte(`{"items":[{"metadata":{"labels":{"app.kubernetes.io/name":"api-backend"}},"spec":{"replicas":1},"status":{"availableReplicas":1,"readyReplicas":1,"updatedReplicas":1}},{"metadata":{"labels":{"app.kubernetes.io/name":"job-scheduler"}},"spec":{"replicas":1},"status":{"availableReplicas":0,"readyReplicas":0,"updatedReplicas":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !workloads["api-backend"] || workloads["job-scheduler"] {
		t.Fatalf("unexpected workload readiness: %#v", workloads)
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
	if got := requiredReason(map[string]any{"reason": "line one\nline two"}); got != "" {
		t.Fatalf("multiline removal reason accepted: %q", got)
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
