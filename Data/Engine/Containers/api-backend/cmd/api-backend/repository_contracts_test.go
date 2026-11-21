package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRootForContractTest(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "Engine.sh")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root containing Engine.sh was not found")
		}
		directory = parent
	}
}

func readRepositoryContractFile(t *testing.T, relativePath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRootForContractTest(t), filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func engineShellFunctionForContractTest(t *testing.T, source string, start string, end string) string {
	t.Helper()
	parts := strings.SplitN(source, start, 2)
	if len(parts) != 2 {
		t.Fatalf("missing Engine.sh function marker %q", start)
	}
	parts = strings.SplitN(parts[1], end, 2)
	if len(parts) != 2 {
		t.Fatalf("missing Engine.sh function end marker %q", end)
	}
	return parts[0]
}

func assertContractOrder(t *testing.T, source string, values ...string) {
	t.Helper()
	last := -1
	for _, value := range values {
		index := strings.Index(source, value)
		if index < 0 {
			t.Fatalf("missing contract text %q", value)
		}
		if index <= last {
			t.Fatalf("contract text %q is out of order", value)
		}
		last = index
	}
}

func runTraefikEntrypointContractTest(t *testing.T, overrides map[string]string) (string, string, string) {
	t.Helper()
	temporaryRoot := t.TempDir()
	fakeBin := filepath.Join(temporaryRoot, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTraefik := filepath.Join(fakeBin, "traefik")
	if err := os.WriteFile(fakeTraefik, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(temporaryRoot, "runtime")
	entrypoint := filepath.Join(
		repositoryRootForContractTest(t),
		"Data", "Engine", "Containers", "traefik-edge", "entrypoint.sh",
	)
	command := exec.Command("sh", entrypoint)
	commandEnvironment := make([]string, 0, len(os.Environ())+len(overrides)+2)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "BOREALIS_") && !strings.HasPrefix(value, "PATH=") {
			commandEnvironment = append(commandEnvironment, value)
		}
	}
	commandEnvironment = append(commandEnvironment,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BOREALIS_PROJECT_ROOT="+runtimeRoot,
	)
	for key, value := range overrides {
		commandEnvironment = append(commandEnvironment, key+"="+value)
	}
	command.Env = commandEnvironment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Traefik entrypoint failed: %v\n%s", err, output)
	}
	serviceRoot := filepath.Join(runtimeRoot, "Engine", "Services", "traefik-edge")
	read := func(pathParts ...string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(append([]string{serviceRoot}, pathParts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	return read("config", "dynamic", "core.yml"), read("config", "traefik.yml"), read("state", "Settings.json")
}

func TestWebUILeavesAuthCookieManagementToGoBackend(t *testing.T) {
	for _, path := range []string{
		"Data/Engine/Containers/webui-frontend/data/web-interface/src/Login.jsx",
		"Data/Engine/Containers/webui-frontend/data/web-interface/src/app/runtime/bootstrapClientRuntime.js",
	} {
		if source := readRepositoryContractFile(t, path); strings.Contains(source, "document.cookie") {
			t.Fatalf("browser-readable auth cookie write found in %s", path)
		}
	}
}

func TestSiteWorkerEntrypointResetsOnlyPriorContainerDrain(t *testing.T) {
	entrypoint := filepath.Join(repositoryRootForContractTest(t), "Data", "Engine", "Containers", "site-worker", "entrypoint.sh")
	for _, mode := range []string{"absent", "stale", "new drain", "cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "borealis-draining")
			if mode == "cleanup failure" {
				if err := os.Mkdir(marker, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if mode != "absent" {
				if err := os.WriteFile(marker, []byte("previous container"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			maintenance := filepath.Join(root, "maintenance-state")
			if err := os.WriteFile(maintenance, []byte("drained"), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			// Replace Python with a bounded observer; never launch an Engine worker.
			python := `#!/bin/bash
set -euo pipefail
[[ "$#" == 2 && "$1" == -m ]]
case "$2" in
  Data.Engine.services.ansible.collections)
    [[ ! -e "$FIXTURE_ROOT/borealis-draining" ]]
    printf 'collections\n' >> "$FIXTURE_ROOT/started"
    if [[ "$FIXTURE_MODE" == 'new drain' ]]; then
      printf 'current container' > "$FIXTURE_ROOT/borealis-draining"
    fi
    ;;
  Data.Engine.services.job_scheduler.worker)
    if [[ "$FIXTURE_MODE" == 'new drain' ]]; then
      [[ "$(cat "$FIXTURE_ROOT/borealis-draining")" == 'current container' ]]
    else
      [[ ! -e "$FIXTURE_ROOT/borealis-draining" ]]
    fi
    printf 'worker\n' >> "$FIXTURE_ROOT/started"
    ;;
  *) exit 91 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "python"), []byte(python), 0o700); err != nil {
				t.Fatal(err)
			}
			// Source the unchanged entrypoint while mapping its fixed container path
			// into this fixture. No command touches the workstation's /tmp marker.
			command := exec.Command("bash", "-c", `
cd() { builtin cd "$FIXTURE_ROOT"; }
rm() {
  [[ "$#" == 3 && "$1" == -f && "$2" == -- && "$3" == /tmp/borealis-draining ]] || exit 92
  command rm -f -- "$FIXTURE_ROOT/borealis-draining"
}
source "$1"
`, "site-worker-entrypoint-test", entrypoint)
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "BOREALIS_") && !strings.HasPrefix(value, "PATH=") && !strings.HasPrefix(value, "BASH_ENV=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"BOREALIS_PROJECT_ROOT="+root,
				"FIXTURE_ROOT="+root,
				"FIXTURE_MODE="+mode,
			)
			output, err := command.CombinedOutput()
			started, startedErr := os.ReadFile(filepath.Join(root, "started"))
			if mode == "cleanup failure" {
				if err == nil || !os.IsNotExist(startedErr) {
					t.Fatalf("worker must not initialize after failed drain cleanup: err=%v started=%q readErr=%v output=%s", err, started, startedErr, output)
				}
			} else if err != nil || startedErr != nil || string(started) != "collections\nworker\n" {
				t.Fatalf("worker startup failed: err=%v started=%q readErr=%v output=%s", err, started, startedErr, output)
			}
			if mode == "new drain" {
				content, err := os.ReadFile(marker)
				if err != nil || string(content) != "current container" {
					t.Fatalf("current drain changed after startup: content=%q err=%v", content, err)
				}
			}
			content, err := os.ReadFile(maintenance)
			if err != nil || string(content) != "drained" {
				t.Fatalf("startup changed unrelated maintenance state: content=%q err=%v", content, err)
			}
		})
	}
}

func TestEngineAgentBinaryRedeployCommandRemainsExposed(t *testing.T) {
	source := readRepositoryContractFile(t, "Engine.sh")
	for _, expected := range []string{"Engine.sh --redeploy-agent-binaries", "--redeploy-agent-binaries)", "redeploy_agent_binaries"} {
		if !strings.Contains(source, expected) {
			t.Fatalf("Engine.sh lost redeploy contract %q", expected)
		}
	}
}

func TestEngineAgentBinaryRedeployKeepsOldWorkersUntilHealthCutover(t *testing.T) {
	source := readRepositoryContractFile(t, "Engine.sh")
	function := engineShellFunctionForContractTest(t, source, "redeploy_agent_binaries() {", "\nusage() {")
	assertContractOrder(t, function,
		`agent_redeploy_wait_for_pod_health "${candidate}"`,
		`agent_redeploy_patch_service_revision "${service}" "${candidate_revision}"`,
		`agent_redeploy_probe_service "${candidate}"`,
		"AGENT_REDEPLOY_COMMIT_STARTED=1",
		`delete "pod/${pod}" --wait=true`,
		`agent_redeploy_wait_for_worker_registration "${worker_guid}"`,
	)
	for _, expected := range []string{"Traefik route file unchanged", "agent_redeploy_pause_schedulers", "agent_redeploy_restore_schedulers"} {
		if !strings.Contains(function, expected) {
			t.Fatalf("redeploy cutover lost %q", expected)
		}
	}
}

func TestEngineAgentBinaryRedeployHasPrecommitRollback(t *testing.T) {
	source := readRepositoryContractFile(t, "Engine.sh")
	recovery := engineShellFunctionForContractTest(t, source, "agent_redeploy_exit_trap() {", "\nredeploy_agent_binaries() {")
	for _, expected := range []string{
		`AGENT_REDEPLOY_COMMIT_STARTED}" -eq 0`,
		`agent_redeploy_patch_service_revision "${service}" "${old_revision}"`,
		`delete "pod/${candidate}"`,
		"--ignore-not-found=true --wait=true --timeout=90s",
		"agent_redeploy_set_scheduler_worker_image",
		"agent_redeploy_restore_schedulers",
	} {
		if !strings.Contains(recovery, expected) {
			t.Fatalf("redeploy rollback lost %q", expected)
		}
	}
}

func TestClusterCandidateReceivesAegisKeyWithoutEnteringPublicService(t *testing.T) {
	reconciler := readRepositoryContractFile(t, "Data/Engine/K3s/cluster/reconcile-node-workloads.py")
	for _, expected := range []string{
		`labels["borealis.io/aegis-peer"] = "true"`,
		`template_labels["borealis.io/aegis-peer"] = "true"`,
		`f"{base}-candidate" if candidate else base`,
	} {
		if !strings.Contains(reconciler, expected) {
			t.Fatalf("candidate Aegis isolation contract lost %q", expected)
		}
	}
	aegisService := readRepositoryContractFile(t, "Data/Engine/K3s/cluster/aegis-mtls.yaml")
	if !strings.Contains(aegisService, `borealis.io/aegis-peer: "true"`) || strings.Contains(aegisService, "selector:\n    app.kubernetes.io/name: api-backend") {
		t.Fatal("Aegis peer Service no longer selects active and isolated candidate API replicas")
	}
}

func TestHMRLostTargetRecoveryFencesRejoinedTargetBeforeCommit(t *testing.T) {
	source := readRepositoryContractFile(t, "Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go")
	start := strings.Index(source, "func (c *clusterController) reconcileLostHMRNode(")
	end := strings.Index(source, "\nfunc clusterControllerStepTimeout(")
	if start < 0 || end <= start {
		t.Fatal("lost HMR target recovery function unavailable")
	}
	recovery := source[start:end]
	fenceStart := strings.Index(recovery, "targetReady, err := runner.nodeReady")
	if fenceStart < 0 {
		t.Fatal("lost HMR target recovery fence unavailable")
	}
	assertContractOrder(t, recovery[fenceStart:],
		"targetReady, err := runner.nodeReady",
		`"EnterApplicationDrain"`,
		"runner.setNodeRoleEligibility(ctx, targetName, false)",
		"runner.waitNodeEndpointsWithdrawn(ctx, targetName)",
		"runner.waitVIPAndWireGuardOwnersAwayFrom(ctx, targetName)",
		"conn.BeginTx",
	)
}

func TestEngineIPFallbackIsResolvedForEveryNetworkMode(t *testing.T) {
	source := readRepositoryContractFile(t, "Engine.sh")
	resolver := engineShellFunctionForContractTest(t, source, "resolve_engine_ip_fallback() {", "\nvalidate_engine_fqdn() {")
	if !strings.Contains(resolver, `normalize_engine_deployment_profile "${engine_profile}" >/dev/null`) || !strings.Contains(resolver, "detect_engine_ip_fallback") {
		t.Fatal("Engine IP fallback no longer normalizes profile and detects fallback")
	}
	if strings.Contains(resolver, `== "internal-only"`) {
		t.Fatal("Engine IP fallback became restricted to internal-only profile")
	}
	envWriter := engineShellFunctionForContractTest(t, source, "write_compose_env() {", "\ncompute_service_hash() {")
	assertContractOrder(t, envWriter,
		`engine_ip_fallback="$(resolve_engine_ip_fallback "${engine_profile}")"`,
		`if [[ "${engine_profile}" == "internal-only" ]]`,
	)
	if !strings.Contains(source, "BOREALIS_ENGINE_IP_FALLBACK=${engine_ip_fallback}") {
		t.Fatal("Engine environment no longer publishes IP fallback")
	}
}

func TestTraefikEntrypointWritesHostnameAndProxyContracts(t *testing.T) {
	dynamicConfig, staticConfig, _ := runTraefikEntrypointContractTest(t, map[string]string{
		"BOREALIS_PUBLIC_HOSTNAME":           "borealis.example.test",
		"BOREALIS_PUBLIC_HOSTNAME_ALIASES":   "borealis.example.test, alias.example.test",
		"BOREALIS_ACME_EMAIL":                "operator@example.test",
		"BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS": "192.168.5.29/32, 10.42.0.0/16",
	})
	hostRule := "Host(`borealis.example.test`,`alias.example.test`)"
	if count := strings.Count(dynamicConfig, hostRule); count != 4 {
		t.Fatalf("hostname rule occurs %d times, want 4", count)
	}
	if !strings.Contains(dynamicConfig, "      rule: \""+hostRule+"\"") {
		t.Fatal("HTTP redirect lost public hostname rule")
	}
	if strings.Contains(dynamicConfig, "/.well-known/acme-challenge/") {
		t.Fatal("TLS-ALPN ACME flow retained obsolete HTTP challenge route")
	}
	if !strings.Contains(staticConfig, "      tlsChallenge: {}") {
		t.Fatal("public Traefik no longer uses TLS-ALPN ACME challenge")
	}
	if strings.Contains(staticConfig, "httpChallenge:") {
		t.Fatal("public Traefik returned to intercepted HTTP-01 challenge")
	}
	for expected, count := range map[string]int{
		"forwardedHeaders:":           2,
		"proxyProtocol:":              1,
		`        - "192.168.5.29/32"`: 3,
		`        - "10.42.0.0/16"`:    3,
	} {
		if actual := strings.Count(staticConfig, expected); actual != count {
			t.Fatalf("static config contains %q %d times, want %d", expected, actual, count)
		}
	}
	for _, expected := range []string{"providers:", "  file:", "    directory:", "    watch: true"} {
		if !strings.Contains(staticConfig, expected) {
			t.Fatalf("static config lost watched dynamic-directory contract %q", expected)
		}
	}
	if strings.Contains(staticConfig, "    filename:") {
		t.Fatal("static config returned to single-file provider")
	}
}

func TestTraefikEntrypointRoutesWebUIToConfiguredK3sUpstream(t *testing.T) {
	dynamicConfig, _, settings := runTraefikEntrypointContractTest(t, map[string]string{
		"BOREALIS_PUBLIC_HOSTNAME":     "borealis.example.test",
		"BOREALIS_WEBUI_TRAFFIC_OWNER": "k3s",
		"BOREALIS_WEBUI_UPSTREAM_HOST": "10.43.82.247",
		"BOREALIS_WEBUI_UPSTREAM_PORT": "8000",
	})
	if !strings.Contains(dynamicConfig, `url: "http://10.43.82.247:8000"`) {
		t.Fatal("Traefik WebUI route lost configured K3s upstream")
	}
	for _, expected := range []string{`"webui_traffic_owner": "k3s"`, `"webui_upstream_host": "10.43.82.247"`} {
		if !strings.Contains(settings, expected) {
			t.Fatalf("runtime settings lost %q", expected)
		}
	}
}
