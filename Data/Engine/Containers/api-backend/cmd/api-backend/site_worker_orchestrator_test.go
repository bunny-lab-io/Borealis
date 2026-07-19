package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteWorkerOrchestratorRequiresInternalToken(t *testing.T) {
	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: "/bin/true", projectRoot: t.TempDir()}
	mux := orchestrator.handler()

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/health", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized healthcheck, got %d", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(internalTokenHeader, goInternalToken([]byte("test-secret")))
	mux.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected authorized healthcheck, got %d body=%s", authorized.Code, authorized.Body.String())
	}
}

func TestSiteWorkerOrchestratorRejectsUnknownLaunchFields(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:test")
	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: "/bin/true", projectRoot: t.TempDir()}
	mux := orchestrator.handler()

	for _, field := range []string{"privileged", "devices", "cap_add", "volumes", "entrypoint", "command", "env"} {
		body := `{"worker_guid":"worker-1","site_id":7,"image":"borealis-engine/site-worker:test",` + jsonQuote(field) + `:true}`
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/site-workers/launch", strings.NewReader(body))
		request.Header.Set(internalTokenHeader, goInternalToken([]byte("test-secret")))
		request.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("field %s expected HTTP 400, got %d body=%s", field, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "unknown field") {
			t.Fatalf("field %s should fail strict decoding, got body=%s", field, recorder.Body.String())
		}
	}
}

func TestSiteWorkerOrchestratorRejectsArbitraryImage(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:allowed")
	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: "/bin/true", projectRoot: t.TempDir()}
	mux := orchestrator.handler()

	body := `{"worker_guid":"worker-1","site_id":7,"image":"docker.io/library/alpine:latest","remote_ops_port":56001,"remote_desktop_port":61001}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/site-workers/launch", strings.NewReader(body))
	request.Header.Set(internalTokenHeader, goInternalToken([]byte("test-secret")))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected arbitrary image rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSiteWorkerOrchestratorLaunchBuildsSafeDockerPayload(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "docker-args.txt")
	dockerPath := filepath.Join(tmp, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(capturePath) + "\nprintf 'container-id\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(tmp, "compose.env")
	if err := os.WriteFile(envPath, []byte("BOREALIS_TEST=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_RUNTIME_ENV_FILE", envPath)
	t.Setenv("BOREALIS_SITE_WORKER_IMAGE", "borealis-engine/site-worker:test")

	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: dockerPath, projectRoot: tmp}
	payload, status, err := orchestrator.launchSiteWorker(context.Background(), orchestratorLaunchRequest{
		WorkerGUID:        "worker-safe",
		SiteID:            7,
		ContainerName:     "site-worker-worker-safe",
		Image:             "borealis-engine/site-worker:test",
		RemoteOpsPort:     56001,
		RemoteDesktopPort: 61001,
	})
	if err != nil || status != http.StatusAccepted || payload["launched"] != true {
		t.Fatalf("unexpected launch status=%d payload=%#v err=%v", status, payload, err)
	}
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(strings.Split(strings.TrimSpace(string(raw)), "\n"), "\n") + "\n"
	for _, expected := range []string{
		"\nrun\n",
		"\n--network\nhost\n",
		"\n--user\n64646:64646\n",
		"\n--security-opt\nno-new-privileges:true\n",
		"\n--cap-drop\nALL\n",
		"\n--read-only\n",
		"\n--tmpfs\n/tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777\n",
		"\n--memory\n256m\n",
		"\n--cpus\n1.00\n",
		"\n--pids-limit\n128\n",
		"\n--label\nborealis.role=site-worker\n",
		"\n--label\nborealis.site_id=7\n",
		"\n--label\nborealis.worker_guid=worker-safe\n",
		"\n--label\nborealis.created_by=site-worker-orchestrator\n",
		"\n-e\nBOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0\n",
		"\n-e\nHOME=/tmp\n",
		"\nborealis-engine/site-worker:test\n",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("docker args missing %q:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{"\n--privileged\n", "\n--device\n", "\n--cap-add\n", "\n--pid\n", "\n--ipc\n", "\n--entrypoint\n", "\n/var/run/docker.sock:/var/run/docker.sock\n"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("site-worker launch contains forbidden docker option %q:\n%s", forbidden, joined)
		}
	}
	if strings.Contains(joined, "Engine/Services/traefik-edge") {
		t.Fatalf("site-worker launch must not mount Traefik config:\n%s", joined)
	}
}

func TestSiteWorkerOrchestratorStopRejectsNonBorealisAndGUIDMismatch(t *testing.T) {
	tmp := t.TempDir()
	actionPath := filepath.Join(tmp, "docker-action.txt")
	dockerPath := filepath.Join(tmp, "docker")
	inspectJSON := `[
  {"Name":"/site-worker-worker-1","Config":{"Labels":{"borealis.role":"database","borealis.worker_guid":"worker-2"}}}
]`
	script := "#!/bin/sh\nif [ \"$1\" = inspect ]; then cat <<'JSON'\n" + inspectJSON + "\nJSON\nexit 0\nfi\nprintf '%s\\n' \"$@\" > " + shellQuote(actionPath) + "\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: dockerPath, projectRoot: tmp}
	_, status, err := orchestrator.siteWorkerContainerAction(context.Background(), orchestratorContainerRequest{WorkerGUID: "worker-1", ContainerName: "site-worker-worker-1"}, "stop")
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("expected non-site-worker rejection, status=%d err=%v", status, err)
	}
	if _, err := os.Stat(actionPath); !os.IsNotExist(err) {
		t.Fatalf("docker stop should not have run for non-site-worker")
	}

	inspectJSON = `[
  {"Name":"/site-worker-worker-1","Config":{"Labels":{"borealis.role":"site-worker","borealis.worker_guid":"worker-2"}}}
]`
	script = "#!/bin/sh\nif [ \"$1\" = inspect ]; then cat <<'JSON'\n" + inspectJSON + "\nJSON\nexit 0\nfi\nprintf '%s\\n' \"$@\" > " + shellQuote(actionPath) + "\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, status, err = orchestrator.siteWorkerContainerAction(context.Background(), orchestratorContainerRequest{WorkerGUID: "worker-1", ContainerName: "site-worker-worker-1"}, "stop")
	if err == nil || status != http.StatusConflict {
		t.Fatalf("expected guid mismatch rejection, status=%d err=%v", status, err)
	}
}

func TestSiteWorkerOrchestratorServiceActionAllowlist(t *testing.T) {
	tmp := t.TempDir()
	capturePath := filepath.Join(tmp, "docker-args.txt")
	dockerPath := filepath.Join(tmp, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(capturePath) + "\nprintf 'helper-id\\n'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_JOB_SCHEDULER_IMAGE", "borealis-engine/job-scheduler:test")

	orchestrator := &siteWorkerOrchestrator{secret: []byte("test-secret"), dockerBin: dockerPath, projectRoot: "/opt/Borealis"}
	if err := orchestrator.runServiceAction(context.Background(), orchestratorServiceActionRequest{ServiceKey: "webui-frontend", Action: "rebuild", Mode: "prod"}); err != nil {
		t.Fatalf("valid service action rejected: %v", err)
	}
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(strings.Split(strings.TrimSpace(string(raw)), "\n"), "\n") + "\n"
	for _, expected := range []string{
		"\n--security-opt\nno-new-privileges:true\n",
		"\n--cap-drop\nALL\n",
		"\n--read-only\n",
		"\n--tmpfs\n/tmp:rw,noexec,nosuid,nodev,size=128m,mode=1777\n",
		"\n--memory\n512m\n",
		"\n--cpus\n1.00\n",
		"\n--pids-limit\n160\n",
		"\n-e\nHOME=/tmp\n",
		"\nborealis-engine/job-scheduler:test\n",
		"Engine.sh",
		"--service",
		"webui-frontend",
		"rebuild",
		"prod",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("helper docker args missing %q:\n%s", expected, joined)
		}
	}
	for _, forbidden := range []string{"\n--privileged\n", "\n--device\n", "\n--cap-add\n", "\n--pid\n", "\n--ipc\n"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("service action helper contains forbidden docker option %q:\n%s", forbidden, joined)
		}
	}
	if err := orchestrator.runServiceAction(context.Background(), orchestratorServiceActionRequest{ServiceKey: "webui-frontend", Action: "rebuild"}); err == nil {
		t.Fatalf("rebuild without mode should be rejected")
	}
	if err := orchestrator.runServiceAction(context.Background(), orchestratorServiceActionRequest{ServiceKey: "site-worker-orchestrator", Action: "restart"}); err == nil {
		t.Fatalf("orchestrator restart should not be operator-allowlisted")
	}
}

func TestComposeJobSchedulerDoesNotMountDockerSocket(t *testing.T) {
	content, err := os.ReadFile("../../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schedulerBlock := composeServiceBlock(string(content), "job-scheduler")
	if strings.Contains(schedulerBlock, "/var/run/docker.sock") {
		t.Fatalf("job-scheduler must not mount Docker socket:\n%s", schedulerBlock)
	}
	for _, forbidden := range []string{
		"Engine/Services/api-backend:/opt/Borealis/Engine/Services/api-backend",
		"Engine/Services/wireguard-tunnel",
		"Engine/Services/traefik-edge/config:/opt/Borealis/Engine/Services/traefik-edge/config",
	} {
		if strings.Contains(schedulerBlock, forbidden) {
			t.Fatalf("job-scheduler has overbroad mount %q:\n%s", forbidden, schedulerBlock)
		}
	}
	orchestratorBlock := composeServiceBlock(string(content), "site-worker-orchestrator")
	if !strings.Contains(orchestratorBlock, "BOREALIS_DOCKER_SOCKET_PATH") || !strings.Contains(orchestratorBlock, ":/var/run/docker.sock") {
		t.Fatalf("site-worker-orchestrator should own Docker socket mount:\n%s", orchestratorBlock)
	}
	for _, forbidden := range []string{
		"Engine/Services/api-backend:/opt/Borealis/Engine/Services/api-backend",
		"Engine/Services/site-worker-orchestrator:/opt/Borealis/Engine/Services/site-worker-orchestrator",
		"Engine/Services/traefik-edge",
	} {
		if strings.Contains(orchestratorBlock, forbidden) {
			t.Fatalf("site-worker-orchestrator has overbroad mount %q:\n%s", forbidden, orchestratorBlock)
		}
	}
	webuiBlock := composeServiceBlock(string(content), "webui-frontend")
	if webuiBlock != "" {
		t.Fatalf("Compose webui-frontend service should be retired after K3s cutover:\n%s", webuiBlock)
	}
}

func composeServiceBlock(content string, service string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for idx, line := range lines {
		if line == "  "+service+":" {
			start = idx
			break
		}
	}
	if start < 0 {
		return ""
	}
	out := []string{lines[start]}
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "" {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func jsonQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
