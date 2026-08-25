import os
import pathlib
import subprocess
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]


class EngineClusterRecoveryTests(unittest.TestCase):
    def run_engine_library(self, script: str, *, extra_env=None):
        env = os.environ.copy()
        env.update(
            {
                "BOREALIS_ENGINE_LIBRARY_MODE": "1",
                "BOREALIS_TEST_REPO_ROOT": str(REPO_ROOT),
            }
        )
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            ["bash", "-c", f'source "$BOREALIS_TEST_REPO_ROOT/Engine.sh"\n{script}'],
            cwd=REPO_ROOT,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_cnpg_runtime_url_is_preserved_only_for_cluster_service(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            runtime_env = pathlib.Path(temp_dir) / "runtime.env"
            runtime_env.write_text(
                "BOREALIS_DATABASE_URL=postgresql://borealis:test@borealis-postgres-rw:5432/borealis\n",
                encoding="utf-8",
            )
            result = self.run_engine_library(
                r'''
RUNTIME_ENV="$BOREALIS_TEST_RUNTIME_ENV"
cnpg_url="$(runtime_cnpg_database_url)"
[[ "$cnpg_url" == *"@borealis-postgres-rw:5432/borealis" ]]
printf '%s\n' 'BOREALIS_DATABASE_URL=postgresql://borealis:test@postgres-db.borealis.svc:5432/borealis' >"$RUNTIME_ENV"
if runtime_cnpg_database_url >/dev/null 2>&1; then
  exit 70
fi
''',
                extra_env={"BOREALIS_TEST_RUNTIME_ENV": str(runtime_env)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_cnpg_runtime_guard_requires_healthy_primary_and_retired_standalone(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            runtime_env = pathlib.Path(temp_dir) / "runtime.env"
            kubeconfig = pathlib.Path(temp_dir) / "k3s.yaml"
            build_log = pathlib.Path(temp_dir) / "build.log"
            runtime_env.write_text(
                "BOREALIS_DATABASE_URL=postgresql://borealis:test@borealis-postgres-rw:5432/borealis\n",
                encoding="utf-8",
            )
            kubeconfig.write_text("apiVersion: v1\n", encoding="utf-8")
            result = self.run_engine_library(
                r'''
RUNTIME_ENV="$BOREALIS_TEST_RUNTIME_ENV"
K3S_KUBECONFIG="$BOREALIS_TEST_KUBECONFIG"
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
k3s_cluster_installed() { return 0; }
k3s_kubectl() {
  case "$*" in
    *'.status.phase'*) printf '%s' 'Cluster in healthy state' ;;
    *'.status.readyInstances'*) printf '%s' '1' ;;
    *'kubernetes.io/service-name=borealis-postgres-rw'*) printf '%s\n' 'borealis-postgres-1' ;;
    *'statefulset/postgres-db'*) printf '%s' '0' ;;
    *) return 1 ;;
  esac
}
cnpg_url="$(runtime_cnpg_database_url)"
verify_cnpg_cutover_runtime "$cnpg_url"
''',
                extra_env={
                    "BOREALIS_TEST_RUNTIME_ENV": str(runtime_env),
                    "BOREALIS_TEST_KUBECONFIG": str(kubeconfig),
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_cluster_operation_watch_reconnects_without_resubmitting(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            counter = pathlib.Path(temp_dir) / "counter"
            counter.write_text("0\n", encoding="utf-8")
            operation_id = "11111111-1111-4111-8111-111111111111"
            result = self.run_engine_library(
                r'''
sleep() { :; }
cluster_api_request() {
  count="$(cat "$BOREALIS_TEST_COUNTER")"
  count=$((count + 1))
  printf '%s\n' "$count" >"$BOREALIS_TEST_COUNTER"
  if [[ "$count" -le 2 ]]; then
    return 22
  fi
  printf '{"operations":[{"id":"%s","state":"succeeded"}]}\n' "$BOREALIS_TEST_OPERATION_ID"
}
cluster_wait_for_operation "$BOREALIS_TEST_OPERATION_ID"
''',
                extra_env={
                    "BOREALIS_TEST_COUNTER": str(counter),
                    "BOREALIS_TEST_OPERATION_ID": operation_id,
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(counter.read_text(encoding="utf-8").strip(), "3")
            self.assertIn("continues; reconnecting", result.stderr)

    def test_agent_redeploy_reads_running_work_from_cnpg_primary(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            runtime_env = pathlib.Path(temp_dir) / "runtime.env"
            build_log = pathlib.Path(temp_dir) / "build.log"
            command_log = pathlib.Path(temp_dir) / "commands.log"
            runtime_env.write_text("POSTGRES_DB=borealis\n", encoding="utf-8")
            result = self.run_engine_library(
                r'''
RUNTIME_ENV="$BOREALIS_TEST_RUNTIME_ENV"
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
cluster_mode_enabled() { return 0; }
k3s_kubectl() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_COMMAND_LOG"
  case "$*" in
    *'get endpointslice'*'kubernetes.io/service-name=borealis-postgres-rw'*) printf '%s\n' 'borealis-postgres-3' ;;
    *'exec borealis-postgres-3 -c postgres -- psql --dbname=borealis'*) printf '%s\n' '0' ;;
    *) return 1 ;;
  esac
}
agent_redeploy_active_work_count
''',
                extra_env={
                    "BOREALIS_TEST_RUNTIME_ENV": str(runtime_env),
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_COMMAND_LOG": str(command_log),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "0")
            commands = command_log.read_text(encoding="utf-8")
            self.assertIn("get endpointslice", commands)
            self.assertIn("exec borealis-postgres-3 -c postgres", commands)
            self.assertNotIn("postgres-db-0", commands)

    def test_agent_redeploy_reads_worker_registration_from_cnpg_primary(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            runtime_env = pathlib.Path(temp_dir) / "runtime.env"
            build_log = pathlib.Path(temp_dir) / "build.log"
            command_log = pathlib.Path(temp_dir) / "commands.log"
            runtime_env.write_text("POSTGRES_DB=borealis\n", encoding="utf-8")
            result = self.run_engine_library(
                r'''
RUNTIME_ENV="$BOREALIS_TEST_RUNTIME_ENV"
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
cluster_mode_enabled() { return 0; }
k3s_kubectl() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_COMMAND_LOG"
  case "$*" in
    *'get endpointslice'*'kubernetes.io/service-name=borealis-postgres-rw'*) printf '%s\n' 'borealis-postgres-3' ;;
    *'exec -i borealis-postgres-3 -c postgres -- psql --dbname=borealis'*) printf '%s\n' '1' ;;
    *) return 1 ;;
  esac
}
agent_redeploy_wait_for_worker_registration worker-guid site-worker-candidate 2
''',
                extra_env={
                    "BOREALIS_TEST_RUNTIME_ENV": str(runtime_env),
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_COMMAND_LOG": str(command_log),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = command_log.read_text(encoding="utf-8")
            self.assertIn("exec -i borealis-postgres-3 -c postgres", commands)
            self.assertIn("-v worker_guid=worker-guid", commands)
            self.assertIn("-v container_name=site-worker-candidate", commands)
            self.assertNotIn("postgres-db-0", commands)

    def test_agent_redeploy_pauses_and_restores_named_cluster_schedulers(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            build_log = pathlib.Path(temp_dir) / "build.log"
            command_log = pathlib.Path(temp_dir) / "commands.log"
            result = self.run_engine_library(
                r'''
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
cluster_mode_enabled() { return 0; }
k3s_kubectl() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_COMMAND_LOG"
  case "$*" in
    *'get deployments -l app.kubernetes.io/name=job-scheduler,borealis.io/node-workload=true,borealis.io/update-candidate=false'*)
      printf '%s\n' job-scheduler-engine-01 job-scheduler-engine-02
      ;;
    *'get deployment/job-scheduler-engine-01 -o jsonpath={.spec.replicas}'*) printf '%s' '1' ;;
    *'get deployment/job-scheduler-engine-02 -o jsonpath={.spec.replicas}'*) printf '%s' '0' ;;
    *) return 0 ;;
  esac
}
agent_redeploy_pause_schedulers
[[ "$AGENT_REDEPLOY_SCHEDULER_PAUSED" -eq 1 ]]
agent_redeploy_set_scheduler_worker_image borealis-engine/site-worker:sha-target
agent_redeploy_restore_schedulers
[[ "$AGENT_REDEPLOY_SCHEDULER_PAUSED" -eq 0 ]]
''',
                extra_env={
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_COMMAND_LOG": str(command_log),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = command_log.read_text(encoding="utf-8")
            self.assertIn(
                "scale deployment/job-scheduler-engine-01 --replicas=0", commands
            )
            self.assertIn(
                "set env deployment/job-scheduler-engine-01 "
                "BOREALIS_SITE_WORKER_IMAGE=borealis-engine/site-worker:sha-target",
                commands,
            )
            self.assertIn(
                "scale deployment/job-scheduler-engine-01 --replicas=1", commands
            )
            self.assertIn(
                "scale deployment/job-scheduler-engine-02 --replicas=0", commands
            )
            self.assertNotIn("scale deployment/job-scheduler --replicas", commands)
            self.assertNotIn("set env deployment/job-scheduler ", commands)

    def test_agent_redeploy_retries_transient_candidate_health(self):
        result = self.run_engine_library(
            r'''
attempts=0
sleep() { :; }
agent_redeploy_probe_pod() {
  attempts=$((attempts + 1))
  ((attempts >= 3))
}
agent_redeploy_wait_for_pod_health candidate 65001 worker-guid 7 5
[[ "$attempts" -eq 3 ]]
'''
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_cnpg_runtime_guard_rejects_active_standalone_database(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            runtime_env = pathlib.Path(temp_dir) / "runtime.env"
            kubeconfig = pathlib.Path(temp_dir) / "k3s.yaml"
            build_log = pathlib.Path(temp_dir) / "build.log"
            runtime_env.write_text(
                "BOREALIS_DATABASE_URL=postgresql://borealis:test@borealis-postgres-rw:5432/borealis\n",
                encoding="utf-8",
            )
            kubeconfig.write_text("apiVersion: v1\n", encoding="utf-8")
            result = self.run_engine_library(
                r'''
RUNTIME_ENV="$BOREALIS_TEST_RUNTIME_ENV"
K3S_KUBECONFIG="$BOREALIS_TEST_KUBECONFIG"
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
k3s_cluster_installed() { return 0; }
k3s_kubectl() {
  case "$*" in
    *'.status.phase'*) printf '%s' 'Cluster in healthy state' ;;
    *'.status.readyInstances'*) printf '%s' '1' ;;
    *'kubernetes.io/service-name=borealis-postgres-rw'*) printf '%s\n' 'borealis-postgres-1' ;;
    *'statefulset/postgres-db'*) printf '%s' '1' ;;
    *) return 1 ;;
  esac
}
cnpg_url="$(runtime_cnpg_database_url)"
verify_cnpg_cutover_runtime "$cnpg_url"
''',
                extra_env={
                    "BOREALIS_TEST_RUNTIME_ENV": str(runtime_env),
                    "BOREALIS_TEST_KUBECONFIG": str(kubeconfig),
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                },
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("standalone PostgreSQL is not scaled to zero", result.stderr)

    def test_cutover_waits_for_worker_recreation_before_readiness(self):
        workflow = (
            REPO_ROOT / "Data/Engine/K3s/cluster/cluster-node-workflow.sh"
        ).read_text(encoding="utf-8")
        discovery = workflow.index('get "${worker}" >/dev/null 2>&1')
        readiness = workflow.index('wait --for=condition=Ready "${worker}"')
        self.assertLess(discovery, readiness)
        self.assertIn("Site worker %s was not recreated after database cutover", workflow)

    def test_root_cluster_workflow_uses_scoped_pinned_git_revision(self):
        workflow = (
            REPO_ROOT / "Data/Engine/K3s/cluster/cluster-node-workflow.sh"
        ).read_text(encoding="utf-8")
        self.assertIn(
            'git -c "safe.directory=${repo_root}" -C "${repo_root}" rev-parse "${baseline_revision}^{commit}"',
            workflow,
        )
        self.assertIn("Pinned cluster baseline SHA required.", workflow)
        self.assertIn('[[ "${revision}" == "${baseline_revision}" ]]', workflow)
        self.assertNotIn("git config --global", workflow)

    def test_cluster_controller_passes_recorded_baseline_to_enrollment(self):
        controller = (
            REPO_ROOT
            / "Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go"
        ).read_text(encoding="utf-8")
        manager = (
            REPO_ROOT
            / "Data/Engine/Containers/api-backend/cmd/borealis-node-manager/main.go"
        ).read_text(encoding="utf-8")
        self.assertIn(
            '"--target-sha", cleanText(operation.Payload["baseline_sha"])',
            controller,
        )
        self.assertIn('"BOREALIS_CLUSTER_BASELINE_SHA="+baselineSHA', manager)

    def test_pair_approval_notifies_every_joiner(self):
        store = (
            REPO_ROOT
            / "Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster_store.go"
        ).read_text(encoding="utf-8")
        self.assertIn("for _, approvedAdmissionID := range ids", store)
        self.assertIn(
            "insertClusterEvent(ctx, tx, operationID, approvedAdmissionID",
            store,
        )


if __name__ == "__main__":
    unittest.main()
