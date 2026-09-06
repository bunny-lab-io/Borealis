import os
import base64
import json
import pathlib
import socket
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

    def test_cluster_runtime_hydration_precedes_fresh_preparation(self):
        for case in ("fresh", "stale", "invalid_key", "multiline", "missing_peer", "missing_hostname", "prepare_failure"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp_dir:
                root = pathlib.Path(temp_dir)
                temporary = root / "temporary"
                temporary.mkdir()
                secret_text = "test-only-$(touch SHOULD_NOT_EXIST)-`false`-$literal=tail"
                values = {
                    "BOREALIS_ENGINE_NETWORK_MODE": "public",
                    "BOREALIS_K3S_PEER_CIDRS": "192.168.50.0/24",
                    "BOREALIS_PUBLIC_HOSTNAME": "cluster.example.test",
                    "POSTGRES_PASSWORD": secret_text,
                    "BOREALIS_PROJECT_ROOT": "/original-node",
                }
                if case == "invalid_key":
                    values["not-a-valid-key"] = "value"
                elif case == "multiline":
                    values["POSTGRES_PASSWORD"] = "first\nsecond"
                elif case == "missing_peer":
                    del values["BOREALIS_K3S_PEER_CIDRS"]
                elif case == "missing_hostname":
                    del values["BOREALIS_PUBLIC_HOSTNAME"]
                (root / "secret.json").write_text(json.dumps({
                    "data": {key: base64.b64encode(value.encode()).decode() for key, value in values.items()}
                }))
                compose = root / "compose.env"
                if case == "stale":
                    compose.write_text("BOREALIS_PUBLIC_HOSTNAME=stale.example.test\nPOSTGRES_PASSWORD=stale\n")
                result = self.run_engine_library(
                    r'''
COMPOSE_ENV="$BOREALIS_TEST_STATE/compose.env"
RUNTIME_ENV="$BOREALIS_TEST_STATE/runtime.env"
BUILD_LOG="$BOREALIS_TEST_STATE/build.log"
unset BOREALIS_PUBLIC_HOSTNAME BOREALIS_ENGINE_NETWORK_MODE
k3s_kubectl() { cat "$BOREALIS_TEST_STATE/secret.json"; }
run_privileged() { "$@"; }
ensure_engine_runtime_identity() { :; }
ensure_service_tree() { [[ "$BOREALIS_TEST_CASE" != prepare_failure ]] || return 71; }
seed_webui_runtime_source() { :; }
prune_empty_legacy_runtime_paths() { :; }
load_existing_image_tags() { :; }
resolve_acme_email() { printf 'admin@example.test\n'; }
resolve_traefik_trusted_proxy_ips() { printf '192.168.50.0/24\n'; }
write_compose_env() {
  [[ "$1" == prod && "$2" == cluster.example.test ]] || return 72
  local password="$(read_env_value POSTGRES_PASSWORD)"
  printf 'BOREALIS_PROJECT_ROOT=/joining-node\nPOSTGRES_PASSWORD=%s\n' "$password" >"$RUNTIME_ENV"
  cp "$RUNTIME_ENV" "$COMPOSE_ENV"
  chmod 0600 "$RUNTIME_ENV" "$COMPOSE_ENV"
}
prepare_cluster_node_runtime
''',
                    extra_env={"BOREALIS_TEST_STATE": str(root), "BOREALIS_TEST_CASE": case, "TMPDIR": str(temporary)},
                )
                self.assertEqual(list(temporary.iterdir()), [], "temporary Secret leaked")
                self.assertFalse((REPO_ROOT / "SHOULD_NOT_EXIST").exists())
                if case in ("fresh", "stale"):
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn("BOREALIS_PROJECT_ROOT=/joining-node", compose.read_text())
                    self.assertIn("POSTGRES_PASSWORD=" + secret_text, compose.read_text())
                    runtime = root / "runtime.env"
                    self.assertIn("BOREALIS_PROJECT_ROOT=/original-node", runtime.read_text())
                    self.assertIn("BOREALIS_PUBLIC_HOSTNAME=cluster.example.test", runtime.read_text())
                    for path in (compose, runtime):
                        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse((root / "runtime.env").exists())
                    if case in ("invalid_key", "multiline", "missing_peer"):
                        self.assertFalse(compose.exists())

    def test_engine_redeploy_preserves_newer_installed_k3s(self):
        result = self.run_engine_library(
            r'''
K3S_INSTALL_VERSION="v1.36.3+k3s1"
k3s_cluster_installed() { return 0; }
k3s() { printf 'k3s version v1.36.4+k3s1\n'; }
log_status() { :; }
run_privileged() { printf 'UNEXPECTED INSTALLER\n'; return 99; }
if install_k3s_if_missing; then
    exit 88
else
    result=$?
    [[ "$result" -eq 1 ]] || exit "$result"
fi
k3s --version
'''
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("v1.36.4+k3s1", result.stdout)
        self.assertNotIn("UNEXPECTED INSTALLER", result.stdout)

    def test_longhorn_multipath_guard_fails_closed_for_genuine_map(self):
        result = self.run_engine_library(
            r'''
systemd_unit_file_exists() { return 0; }
run_privileged() { return 0; }
classify_longhorn_multipath_maps() {
  [[ "$1" == "other" ]] && printf '%s\n' 'production-san'
}
ensure_longhorn_multipath_compatibility
'''
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("multipathd manages non-Longhorn map(s): production-san", result.stderr)
        self.assertIn("Configure Longhorn device blacklist", result.stderr)

    def test_longhorn_multipath_guard_disables_only_fixed_units_without_maps(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            state = pathlib.Path(temp_dir) / "disabled"
            calls = pathlib.Path(temp_dir) / "calls"
            build_log = pathlib.Path(temp_dir) / "build.log"
            result = self.run_engine_library(
                r'''
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
systemd_unit_file_exists() { return 0; }
classify_longhorn_multipath_maps() { return 0; }
run_privileged() {
  if [[ "$1" == "systemctl" && "$2" == "disable" && "$3" == "--now" ]]; then
    printf '%s\n' "$*" >"$BOREALIS_TEST_CALLS"
    : >"$BOREALIS_TEST_STATE"
    return 0
  fi
  if [[ "$1" == "systemctl" && ("$2" == "is-active" || "$2" == "is-enabled") ]]; then
    [[ ! -e "$BOREALIS_TEST_STATE" ]]
    return
  fi
  return 70
}
ensure_longhorn_multipath_compatibility
''',
                extra_env={
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_CALLS": str(calls),
                    "BOREALIS_TEST_STATE": str(state),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                calls.read_text(encoding="utf-8").strip(),
                "systemctl disable --now multipathd.service multipathd.socket",
            )

    def test_debian_longhorn_dependency_install_waits_for_apt_lock(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            calls = root / "calls"
            installed = root / "installed"
            build_log = root / "build.log"
            result = self.run_engine_library(
                r'''
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
detect_distro() { DISTRO_ID=ubuntu; }
command_exists() {
  [[ "$1" == "iscsiadm" && -e "$BOREALIS_TEST_INSTALLED" ]]
}
run_privileged() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_CALLS"
  if [[ "$*" == *"install -y open-iscsi" ]]; then
    : >"$BOREALIS_TEST_INSTALLED"
  fi
}
ensure_longhorn_iscsi_package
''',
                extra_env={
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_CALLS": str(calls),
                    "BOREALIS_TEST_INSTALLED": str(installed),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                calls.read_text(encoding="utf-8").splitlines(),
                [
                    "apt-get -o DPkg::Lock::Timeout=300 update -qq",
                    "apt-get -o DPkg::Lock::Timeout=300 install -y open-iscsi",
                ],
            )

    def test_longhorn_probe_guard_waits_for_csi_daemonset_creation(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            calls = root / "calls"
            build_log = root / "build.log"
            result = self.run_engine_library(
                r'''
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
K3S_LONGHORN_NAMESPACE=longhorn-system
K3S_LONGHORN_ROLLOUT_TIMEOUT=17s
log_status() { :; }
k3s_kubectl() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_CALLS"
}
ensure_longhorn_csi_probe_guard
''',
                extra_env={
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_CALLS": str(calls),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = calls.read_text(encoding="utf-8").splitlines()
            creation = commands.index(
                "-n longhorn-system wait --for=create daemonset/longhorn-csi-plugin --timeout=17s"
            )
            patch = next(
                index
                for index, command in enumerate(commands)
                if "patch daemonset/longhorn-csi-plugin" in command
            )
            rollout = commands.index(
                "-n longhorn-system rollout status daemonset/longhorn-csi-plugin --timeout=17s"
            )
            self.assertLess(creation, patch)
            self.assertLess(patch, rollout)

    def test_sudo_checkout_owner_requires_matching_nonroot_account(self):
        result = self.run_engine_library(
            r'''
id() {
  case "$1" in
    -u) printf '%s\n' '1234' ;;
    -g) printf '%s\n' '2345' ;;
    *) return 64 ;;
  esac
}
SUDO_USER=operator
SUDO_UID=1234
SUDO_GID=2345
[[ "$(sudo_invoking_owner)" == "1234:2345" ]]
SUDO_UID=9999
if sudo_invoking_owner >/dev/null 2>&1; then
  exit 70
fi
'''
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_engine_release_version_uses_clean_commit_backed_development_identity(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            repo = pathlib.Path(temp_dir) / "repo"
            repo.mkdir()
            subprocess.run(["git", "init"], cwd=repo, check=True, capture_output=True)
            subprocess.run(
                ["git", "config", "user.email", "borealis-tests@example.invalid"],
                cwd=repo,
                check=True,
            )
            subprocess.run(
                ["git", "config", "user.name", "Borealis Tests"],
                cwd=repo,
                check=True,
            )
            tracked = repo / "tracked.txt"
            tracked.write_text("clean\n", encoding="utf-8")
            subprocess.run(["git", "add", "tracked.txt"], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-m", "test baseline"], cwd=repo, check=True, capture_output=True)
            sha = subprocess.run(
                ["git", "rev-parse", "HEAD"],
                cwd=repo,
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()

            result = self.run_engine_library(
                'SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"\nengine_release_version',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), f"dev-{sha[:12]}")

            subprocess.run(["git", "tag", "2026.09.1-rc.2"], cwd=repo, check=True)
            result = self.run_engine_library(
                'SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"\nengine_release_version',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "2026.09.1-rc.2")

            subprocess.run(["git", "tag", "2026.09.1"], cwd=repo, check=True)
            result = self.run_engine_library(
                'SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"\nengine_release_version',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "2026.09.1")

            tracked.write_text("dirty\n", encoding="utf-8")
            result = self.run_engine_library(
                'SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"\nengine_release_version',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "")

            result = self.run_engine_library(
                r'''git() {
  if [[ "$*" == *" status --porcelain --untracked-files=normal" ]]; then
    return 1
  fi
  command git "$@"
}
SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"
engine_release_version''',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "")

            tracked.write_text("clean\n", encoding="utf-8")
            subprocess.run(["git", "tag", "-d", "2026.09.1", "2026.09.1-rc.2"], cwd=repo, check=True, capture_output=True)
            tracked.write_text("dirty\n", encoding="utf-8")
            result = self.run_engine_library(
                'SCRIPT_DIR="$BOREALIS_TEST_GIT_ROOT"\nengine_release_version',
                extra_env={"BOREALIS_TEST_GIT_ROOT": str(repo)},
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "")

    def test_repo_sync_reowns_source_but_prunes_runtime_state(self):
        engine = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
        owner_start = engine.index("reconcile_install_checkout_owner() {")
        owner_end = engine.index("\nvalidate_numeric_id()", owner_start)
        owner_function = engine[owner_start:owner_end]
        for runtime_root in ("Engine", "Engine.old", "Agent"):
            self.assertIn(f' -path "${{install_root}}/{runtime_root}"', owner_function)
        self.assertIn("-prune -o", owner_function)
        self.assertNotIn("chown -R", owner_function)

        sync_start = engine.index("sync_repo() {")
        sync_end = engine.index("\nsource_available()", sync_start)
        sync_function = engine[sync_start:sync_end]
        clean = sync_function.index("git -C \"${INSTALL_DIR}\" clean")
        reconcile = sync_function.index(
            'reconcile_install_checkout_owner "${INSTALL_DIR}"'
        )
        selinux = sync_function.index(
            'restore_selinux_context_if_needed "${INSTALL_DIR}"'
        )
        self.assertLess(clean, reconcile)
        self.assertLess(reconcile, selinux)

        main_start = engine.index("main() {")
        main_end = engine.index(
            '\nif [[ "${BOREALIS_ENGINE_LIBRARY_MODE:-0}" != "1" ]]', main_start
        )
        main_function = engine[main_start:main_end]
        sync = main_function.index("  sync_and_reexec_if_needed")
        local_reconcile = main_function.index(
            '  reconcile_install_checkout_owner "${SCRIPT_DIR}"'
        )
        self.assertLess(sync, local_reconcile)
        self.assertEqual(
            main_function.count('  reconcile_install_checkout_owner "${SCRIPT_DIR}"'),
            2,
        )
        final_reconcile = main_function.rindex(
            '  reconcile_install_checkout_owner "${SCRIPT_DIR}"'
        )
        self.assertGreater(final_reconcile, main_function.rindex("  esac"))

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

    def test_hmr_guard_reports_cluster_api_failure_without_json_traceback(self):
        result = self.run_engine_library(
            r'''
cluster_hmr_membership_status() { return 0; }
cluster_api_request() { return 22; }
cluster_hmr_guard prod
'''
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Cluster API unavailable; HMR state was not changed.", result.stderr)
        self.assertNotIn("Traceback", result.stderr)

    def test_hmr_guard_reports_failed_mutation_without_json_traceback(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            counter = pathlib.Path(temp_dir) / "counter"
            counter.write_text("0\n", encoding="utf-8")
            result = self.run_engine_library(
                r'''
cluster_hmr_membership_status() { return 0; }
CLUSTER_NON_HA_ACKNOWLEDGED=1
cluster_api_request() {
  count="$(cat "$BOREALIS_TEST_COUNTER")"
  count=$((count + 1))
  printf '%s\n' "$count" >"$BOREALIS_TEST_COUNTER"
  if [[ "$count" -eq 1 ]]; then
    printf '%s\n' '{"nodes":[{"id":"11111111-1111-4111-8111-111111111111","node_name":"test-node"}],"hmr":{"state":"active"}}'
    return 0
  fi
  return 22
}
cluster_hmr_guard prod all
''',
                extra_env={
                    "BOREALIS_TEST_COUNTER": str(counter),
                    "BOREALIS_CLUSTER_NODE_NAME": "test-node",
                },
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(
                "Cluster HMR request failed; inspect Cluster Management before retrying.",
                result.stderr,
            )
            self.assertNotIn("Traceback", result.stderr)

    def test_clustered_dev_gate_rejects_membership_and_unknown_state(self):
        for case in ("one", "three", "transitional", "lookup_failure", "instance_failure", "missing_config"):
            for service in ("all", "webui-frontend"):
                with self.subTest(case=case, service=service), tempfile.TemporaryDirectory() as temp_dir:
                    config = pathlib.Path(temp_dir) / "kubeconfig"
                    if case != "missing_config":
                        config.write_text("test-only\n", encoding="utf-8")
                    result = self.run_engine_library(
                        r'''
K3S_KUBECONFIG="$BOREALIS_TEST_CONFIG"
k3s_cluster_installed() { return 0; }
k3s_kubectl() {
  [[ "$BOREALIS_TEST_CASE" != lookup_failure ]] || return 1
  case "$*" in
    *"get crd"*) printf 'customresourcedefinition.apiextensions.k8s.io/borealisclusters.borealis.io\n' ;;
    *) [[ "$BOREALIS_TEST_CASE" != instance_failure ]] || return 1
       printf 'borealiscluster.borealis.io/borealis\n' ;;
  esac
}
cluster_api_request() { printf 'UNEXPECTED API MUTATION\n' >&2; return 99; }
CLUSTER_NON_HA_ACKNOWLEDGED=1
cluster_hmr_guard dev "$BOREALIS_TEST_SERVICE"
''',
                        extra_env={"BOREALIS_TEST_CONFIG": str(config), "BOREALIS_TEST_CASE": case, "BOREALIS_TEST_SERVICE": service},
                    )
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertRegex(result.stderr, "New clustered HMR entry is disabled|Cluster membership unavailable")
                    self.assertNotIn("UNEXPECTED API MUTATION", result.stderr)

    def test_dev_dispatch_blocks_before_runtime_preparation(self):
        for command in ("deploy_engine dev", "service_action webui-frontend rebuild dev", "service_action webui-frontend restart dev", "service_action api-backend rebuild dev", "service_action remote-desktop-guacd restart dev", "service_action traefik-edge reload dev"):
            with self.subTest(command=command):
                result = self.run_engine_library(
                    r'''
cluster_hmr_membership_status() { return 0; }
ensure_engine_dependencies() { printf 'UNEXPECTED RUNTIME\n' >&2; exit 99; }
prepare_runtime() { printf 'UNEXPECTED RUNTIME\n' >&2; exit 99; }
''' + command
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("New clustered HMR entry is disabled", result.stderr)
                self.assertNotIn("UNEXPECTED RUNTIME", result.stderr)

    def test_standalone_dev_gate_accepts_confirmed_absence(self):
        for case in ("fresh", "no_crd", "no_cluster"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temp_dir:
                config = pathlib.Path(temp_dir) / "kubeconfig"
                if case != "fresh":
                    config.write_text("test-only\n", encoding="utf-8")
                result = self.run_engine_library(
                    r'''
K3S_KUBECONFIG="$BOREALIS_TEST_CONFIG"
k3s_cluster_installed() { [[ "$BOREALIS_TEST_CASE" != fresh ]]; }
k3s_kubectl() {
  if [[ "$*" == *"exec postgres-db-0"* ]]; then
    printf 'f\n'
    return 0
  fi
  if [[ "$BOREALIS_TEST_CASE" == no_cluster && "$*" == *"get crd"* ]]; then
    printf 'customresourcedefinition.apiextensions.k8s.io/borealisclusters.borealis.io\n'
  fi
  return 0
}
cluster_api_request() { printf 'UNEXPECTED API CALL\n' >&2; return 99; }
cluster_hmr_guard dev all || exit "$?"
cluster_hmr_guard dev webui-frontend
''',
                    extra_env={"BOREALIS_TEST_CONFIG": str(config), "BOREALIS_TEST_CASE": case},
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertNotIn("UNEXPECTED API CALL", result.stderr)

    def test_missing_cluster_resource_requires_standalone_database_proof(self):
        for resource in ("no_crd", "no_cluster"):
            for state in ("enabling", "failed_conversion", "unavailable", "invalid"):
                with self.subTest(resource=resource, state=state), tempfile.TemporaryDirectory() as temp_dir:
                    config = pathlib.Path(temp_dir) / "kubeconfig"
                    config.write_text("test-only\n", encoding="utf-8")
                    result = self.run_engine_library(
                        r'''
K3S_KUBECONFIG="$BOREALIS_TEST_CONFIG"
k3s_cluster_installed() { return 0; }
k3s_kubectl() {
  if [[ "$*" == *"exec postgres-db-0"* ]]; then
    [[ "$*" == *"--request-timeout=10s"* && "$*" == *"statement_timeout=5000"* && "$*" == *"default_transaction_read_only=on"* ]] || return 99
    [[ "${!#}" == 'SELECT EXISTS (SELECT 1 FROM engine.cluster_state);' ]] || return 99
    case "$BOREALIS_TEST_STATE" in
      enabling|failed_conversion) printf 't\n' ;;
      unavailable) return 1 ;;
      invalid) printf 'unexpected\n' ;;
    esac
  elif [[ "$BOREALIS_TEST_RESOURCE" == no_cluster && "$*" == *"get crd"* ]]; then
    printf 'customresourcedefinition.apiextensions.k8s.io/borealisclusters.borealis.io\n'
  fi
  return 0
}
cluster_api_request() { printf 'UNEXPECTED API CALL\n' >&2; return 99; }
prepare_runtime() { printf 'UNEXPECTED RUNTIME\n' >&2; exit 99; }
CLUSTER_NON_HA_ACKNOWLEDGED=1
deploy_engine dev
''',
                        extra_env={"BOREALIS_TEST_CONFIG": str(config), "BOREALIS_TEST_RESOURCE": resource, "BOREALIS_TEST_STATE": state},
                    )
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    expected = "New clustered HMR entry is disabled" if state in ("enabling", "failed_conversion") else "Cluster membership unavailable"
                    self.assertIn(expected, result.stderr)
                    self.assertNotIn("UNEXPECTED", result.stderr)

    def test_existing_hmr_production_restore_survives_entry_gate(self):
        for state in ("active", "restore_failed"):
            with self.subTest(state=state):
                result = self.run_engine_library(
                    r'''
cluster_hmr_membership_status() { return 0; }
cluster_api_request() {
  if [[ "$1" == GET ]]; then
    printf '{"nodes":[{"id":"11111111-1111-4111-8111-111111111111","node_name":"test-node"}],"hmr":{"state":"%s","node_id":"11111111-1111-4111-8111-111111111111"}}\n' "$BOREALIS_TEST_HMR_STATE"
  else
    [[ "$2" == /api/server/cluster/hmr/exit && "$3" == '{"confirmation":"EXIT HMR"}' ]] || return 99
    printf '{"operation_id":"22222222-2222-4222-8222-222222222222"}\n'
  fi
}
cluster_wait_for_operation() { [[ "$1" == 22222222-2222-4222-8222-222222222222 ]]; }
cluster_hmr_guard prod webui-frontend
''',
                    extra_env={"BOREALIS_TEST_HMR_STATE": state, "BOREALIS_CLUSTER_NODE_NAME": "test-node"},
                )
                self.assertEqual(result.returncode, 10, result.stderr)
                self.assertIn("Pinned production release restored", result.stdout)

    def run_wireguard_healthcheck(self, ip_script: str):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            fake_bin = root / "bin"
            fake_bin.mkdir()
            control_client = fake_bin / "borealis-wireguard-control-client"
            control_client.write_text(
                "#!/bin/sh\nprintf '%s\\n' '{\"returncode\":0,\"stdout\":\"standby\",\"stderr\":\"\"}'\n",
                encoding="utf-8",
            )
            control_client.chmod(0o755)
            fake_ip = fake_bin / "ip"
            fake_ip.write_text(ip_script, encoding="utf-8")
            fake_ip.chmod(0o755)
            socket_path = root / "control.sock"
            listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            listener.bind(str(socket_path))
            try:
                env = os.environ.copy()
                env.update(
                    {
                        "PATH": f"{fake_bin}{os.pathsep}{env['PATH']}",
                        "BOREALIS_WIREGUARD_CONTROL_SOCKET": str(socket_path),
                        "BOREALIS_CLUSTER_EDGE_VIP": "192.168.50.20",
                    }
                )
                return subprocess.run(
                    [
                        "sh",
                        str(
                            REPO_ROOT
                            / "Data/Engine/Containers/wireguard-tunnel/healthcheck.sh"
                        ),
                    ],
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
            finally:
                listener.close()

    def test_wireguard_readiness_accepts_clean_cluster_standby(self):
        result = self.run_wireguard_healthcheck(
            """#!/bin/sh
case "$*" in
  "-o -4 address show") printf '%s\\n' '2: ens18 inet 192.168.50.21/24 scope global ens18' ;;
  "link show dev borealis-wg") exit 1 ;;
  *) exit 70 ;;
esac
"""
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_wireguard_readiness_rejects_stale_standby_interface(self):
        result = self.run_wireguard_healthcheck(
            """#!/bin/sh
case "$*" in
  "-o -4 address show") printf '%s\\n' '2: ens18 inet 192.168.50.21/24 scope global ens18' ;;
  "link show dev borealis-wg") exit 0 ;;
  *) exit 70 ;;
esac
"""
        )
        self.assertNotEqual(result.returncode, 0)

    def test_wireguard_rollout_failure_collects_pod_diagnostics(self):
        engine = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
        diagnostics_start = engine.index(
            "append_k3s_wireguard_rollout_diagnostics() {"
        )
        diagnostics_end = engine.index(
            "\nrender_k3s_wireguard_tunnel_manifest()", diagnostics_start
        )
        diagnostics = engine[diagnostics_start:diagnostics_end]
        for marker in (
            "describe deployment/wireguard-tunnel",
            'describe "pod/${pod_name}"',
            'logs "pod/${pod_name}" -c wireguard-tunnel --tail=240',
            'logs "pod/${pod_name}" -c wireguard-tunnel --previous --tail=240',
            "Services/wireguard-tunnel/logs/control.log",
        ):
            self.assertIn(marker, diagnostics)
        self.assertGreaterEqual(
            engine.count("    append_k3s_wireguard_rollout_diagnostics"), 2
        )

    def test_wireguard_runtime_directories_allow_control_group_writes(self):
        engine = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
        ownership_start = engine.index("apply_runtime_service_ownership() {")
        ownership_end = engine.index(
            "\napply_deploy_env_file_permissions()", ownership_start
        )
        ownership = engine[ownership_start:ownership_end]
        for directory in ("secrets", "config"):
            self.assertIn(
                f'chmod 0770 "${{RUNTIME_ROOT}}/Services/wireguard-tunnel/{directory}"',
                ownership,
            )
            self.assertNotIn(
                f'chmod 0750 "${{RUNTIME_ROOT}}/Services/wireguard-tunnel/{directory}"',
                ownership,
            )
        self.assertIn(
            'find "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" "${RUNTIME_ROOT}/Services/wireguard-tunnel/config"',
            ownership,
        )
        self.assertIn("-type f -exec chmod 0640 {} +", ownership)

    def test_wireguard_retry_resets_stale_progress_deadline(self):
        engine = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
        detector_start = engine.index(
            "k3s_wireguard_progress_deadline_exceeded() {"
        )
        detector_end = engine.index(
            "\nappend_k3s_wireguard_rollout_diagnostics()", detector_start
        )
        detector = engine[detector_start:detector_end]
        self.assertIn('get deployment/wireguard-tunnel', detector)
        self.assertIn('== "ProgressDeadlineExceeded"', detector)

        reconcile_start = engine.index("ensure_k3s_wireguard_tunnel() {")
        reconcile_end = engine.index(
            "\nk3s_traefik_edge_healthcheck()", reconcile_start
        )
        reconcile = engine[reconcile_start:reconcile_end]
        self.assertIn('"runtime_permissions=dirs-0770-files-0640"', reconcile)
        current = reconcile.index("if k3s_manifest_config_current")
        failed = reconcile.index("if ! k3s_wireguard_progress_deadline_exceeded")
        restart = reconcile.index(
            'rollout restart "deployment/wireguard-tunnel"'
        )
        wait = reconcile.index('rollout status "deployment/wireguard-tunnel"')
        self.assertLess(current, failed)
        self.assertLess(failed, restart)
        self.assertLess(restart, wait)

    def test_public_traefik_uses_tls_alpn_challenge(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            executable_dir = root / "bin"
            executable_dir.mkdir()
            traefik = executable_dir / "traefik"
            traefik.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            traefik.chmod(0o755)
            env = os.environ.copy()
            env.update(
                {
                    "PATH": f"{executable_dir}:{env['PATH']}",
                    "BOREALIS_PROJECT_ROOT": str(root),
                    "BOREALIS_PUBLIC_HOSTNAME": "engine.example.test",
                    "BOREALIS_PUBLIC_HOSTNAME_ALIASES": "engine.example.test",
                    "BOREALIS_ENGINE_DEPLOYMENT_PROFILE": "externally-accessible",
                    "BOREALIS_ACME_EMAIL": "operator@example.test",
                }
            )
            result = subprocess.run(
                [
                    "sh",
                    str(
                        REPO_ROOT
                        / "Data/Engine/Containers/traefik-edge/entrypoint.sh"
                    ),
                ],
                cwd=REPO_ROOT,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            service_root = root / "Engine/Services/traefik-edge"
            static_config = (service_root / "config/traefik.yml").read_text(
                encoding="utf-8"
            )
            self.assertIn("tlsChallenge: {}", static_config)
            self.assertNotIn("httpChallenge:", static_config)
            dynamic_config = (
                service_root / "config/dynamic/core.yml"
            ).read_text(encoding="utf-8")
            self.assertNotIn("/.well-known/acme-challenge/", dynamic_config)

    def test_public_tls_readiness_requires_trusted_hostname_certificate(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = pathlib.Path(temp_dir)
            calls = root / "calls"
            build_log = root / "build.log"
            traefik_log = root / "traefik.log"
            traefik_log.write_text("", encoding="utf-8")
            result = self.run_engine_library(
                r'''
BUILD_LOG="$BOREALIS_TEST_BUILD_LOG"
RUNTIME_ROOT="$BOREALIS_TEST_RUNTIME_ROOT"
read_env_value() {
  case "$1" in
    BOREALIS_ENGINE_DEPLOYMENT_PROFILE) printf '%s\n' externally-accessible ;;
    BOREALIS_ACME_EMAIL) printf '%s\n' operator@example.test ;;
    BOREALIS_PUBLIC_HOSTNAME) printf '%s\n' engine.example.test ;;
  esac
}
generic_k3s_workload_replicas() { printf '%s\n' 1; }
log_status() { :; }
sleep() { :; }
curl() {
  printf '%s\n' "$*" >>"$BOREALIS_TEST_CALLS"
  [[ "$(wc -l <"$BOREALIS_TEST_CALLS")" -ge 3 ]]
}
wait_for_public_tls_certificate
''',
                extra_env={
                    "BOREALIS_TEST_BUILD_LOG": str(build_log),
                    "BOREALIS_TEST_CALLS": str(calls),
                    "BOREALIS_TEST_RUNTIME_ROOT": str(root),
                },
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = calls.read_text(encoding="utf-8").splitlines()
            self.assertEqual(len(commands), 3)
            for command in commands:
                self.assertIn(
                    "--resolve engine.example.test:443:127.0.0.1", command
                )
                self.assertIn("https://engine.example.test/", command)

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

    def test_cluster_redeploy_refreshes_host_node_manager_after_candidate_exists(self):
        engine = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
        function_start = engine.index("cluster_node_redeploy() {")
        function_end = engine.index("\nrender_cluster_schema_phase_job_manifest()", function_start)
        function = engine[function_start:function_end]
        reconcile = function.index('python3 "${K3S_CLUSTER_ASSET_DIR}/reconcile-node-workloads.py"')
        refresh = function.index(
            'schedule_borealis_node_manager_refresh "${CLUSTER_TARGET_REVISION}"'
        )
        self.assertLess(reconcile, refresh)
        self.assertIn('"${staged_binary}" activate-update', engine)
        self.assertIn('--source-root "${SCRIPT_DIR}"', engine)
        self.assertIn("--on-active=5s", engine)
        self.assertNotIn('/bin/sh -c', engine[engine.index("schedule_borealis_node_manager_refresh() {"):function_start])

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
