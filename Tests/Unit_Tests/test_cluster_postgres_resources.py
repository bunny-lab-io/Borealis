import copy
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

import yaml


ROOT = pathlib.Path(__file__).resolve().parents[2]
CLUSTER = ROOT / "Data/Engine/K3s/cluster"
SPEC = importlib.util.spec_from_file_location("render_postgres", CLUSTER / "render-postgres.py")
RENDERER = importlib.util.module_from_spec(SPEC)
sys.dont_write_bytecode = True
SPEC.loader.exec_module(RENDERER)


class ClusterPostgresResourceTests(unittest.TestCase):
    @classmethod
    def engine_source(cls, cpu=2, memory=8192, rank=0, memory_override="", cpu_override=""):
        script = r'''
set -euo pipefail
source "$TEST_ROOT/Engine.sh"
load_profile_tuning "$TEST_CPU" "$TEST_MEMORY" "" ""
[[ "$PROFILE_RANK" == "$TEST_RANK" ]]
# Only resource calculation/rendering is real; unrelated environment is inert.
read_env_value() { printf 'fixture'; }
borealis_postgres_runtime_secret_data() { printf '  runtime.env: Zml4dHVyZQ==\n'; }
k3s_timezone_env_entries() { :; }
k3s_timezone_volume_mount_entries() { :; }
k3s_timezone_volumes() { :; }
generic_k3s_workload_replicas() { printf '1'; }
render_k3s_postgres_statefulset_manifest postgres:fixture fixture 999 999 \
  "$(format_k3s_memory_quantity "${TEST_MEMORY_OVERRIDE:-$PROFILE_POSTGRES_DB_MEMORY_LIMIT}")" \
  "$(format_k3s_cpu_quantity "${TEST_CPU_OVERRIDE:-$PROFILE_POSTGRES_DB_CPU_LIMIT}")" \
  borealis-longhorn 20Gi k3s
'''
        env = dict(os.environ, BOREALIS_ENGINE_LIBRARY_MODE="1", TEST_ROOT=str(ROOT), TEST_CPU=str(cpu),
                   TEST_MEMORY=str(memory), TEST_RANK=str(rank), TEST_MEMORY_OVERRIDE=memory_override, TEST_CPU_OVERRIDE=cpu_override)
        result = subprocess.run(["bash", "-c", script], env=env, capture_output=True, text=True, cwd=ROOT)
        if result.returncode:
            raise AssertionError(result.stderr)
        return next(obj for obj in yaml.safe_load_all(result.stdout) if obj["kind"] == "StatefulSet")

    @classmethod
    def setUpClass(cls):
        cls.source = cls.engine_source()
        cls.template = (CLUSTER / "postgres.yaml").read_text()

    def test_engine_profile_and_override_parity(self):
        cases = [(2, 8192, 0, "", ""), (8, 16384, 1, "", ""), (16, 32768, 2, "", ""),
                 (24, 65536, 3, "", ""), (2, 8192, 0, "1536m", "0.125"), (24, 65536, 3, "3Gi", "750m")]
        for case in cases:
            source = self.engine_source(*case)
            resources = source["spec"]["template"]["spec"]["containers"][0]["resources"]
            for size in (1, 3):
                with self.subTest(case=case, size=size):
                    original = copy.deepcopy(source)
                    docs = list(yaml.safe_load_all(RENDERER.render(self.template, source, size)))
                    cluster = next(obj for obj in docs if obj["kind"] == "Cluster")
                    self.assertEqual(cluster["spec"]["resources"], resources)
                    self.assertEqual(resources["requests"], {"cpu": "100m", "memory": "256Mi"})
                    self.assertEqual(cluster["spec"]["instances"], size)
                    pg = cluster["spec"]["postgresql"]
                    if size == 1:
                        self.assertNotIn("synchronous", pg)
                    else:
                        self.assertEqual(pg["synchronous"], {"method": "any", "number": 1, "dataDurability": "required"})
                    # All other template fields/objects survive rendering unchanged.
                    cluster["spec"].pop("resources")
                    cluster["spec"]["instances"] = 3
                    pg.pop("synchronous", None)
                    self.assertEqual(docs, list(yaml.safe_load_all(self.template)))
                    self.assertEqual(source, original)
            if case[3]:
                self.assertEqual(resources["limits"], {"memory": "1536Mi" if case[3] == "1536m" else "3Gi", "cpu": "125m" if case[4] == "0.125" else "750m"})

    def test_rejects_missing_foreign_or_unbounded_source(self):
        for mode in ("missing source", "wrong kind", "wrong namespace", "wrong name", "wrong owner", "extra container", "missing resources", "missing request", "extra resource", "zero", "CPU precision", "overflow", "above cap", "wrong type"):
            with self.subTest(mode=mode):
                source = copy.deepcopy(self.source)
                c = source["spec"]["template"]["spec"]["containers"][0]
                r = c["resources"]
                if mode == "missing source": source = {}
                elif mode == "wrong kind": source["kind"] = "Deployment"
                elif mode == "wrong namespace": source["metadata"]["namespace"] = "other"
                elif mode == "wrong name": source["metadata"]["name"] = "other"
                elif mode == "wrong owner": source["metadata"]["labels"]["app.kubernetes.io/managed-by"] = "other"
                elif mode == "extra container": source["spec"]["template"]["spec"]["containers"].append(c)
                elif mode == "missing resources": del c["resources"]
                elif mode == "missing request": del r["requests"]["cpu"]
                elif mode == "extra resource": r["requests"]["example.com/gpu"] = "1"
                elif mode == "zero": r["limits"]["cpu"] = "0"
                elif mode == "CPU precision": r["limits"]["cpu"] = "0.0001"
                elif mode == "overflow": r["limits"]["memory"] = "9007199254740992"
                elif mode == "above cap": r["requests"]["memory"] = "99Gi"
                elif mode == "wrong type": r["limits"]["cpu"] = 2
                result = self.run_cli(source)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertNotIn("fixture", result.stderr)

    def run_cli(self, source, size=1, template=None):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "postgres.yaml"
            path.write_text(self.template if template is None else template)
            return subprocess.run(["python3", str(CLUSTER / "render-postgres.py"), str(path), str(size)],
                                  input=json.dumps(source), capture_output=True, text=True)

    def test_template_and_size_fail_closed(self):
        for marker in ("  instances: 3\n", "  # BOREALIS_POSTGRES_RESOURCES", "    # BOREALIS_SYNCHRONOUS_CONFIGURATION"):
            for template in (self.template.replace(marker, ""), self.template + marker):
                self.assertNotEqual(self.run_cli(self.source, template=template).returncode, 0)
        self.assertNotEqual(self.run_cli(self.source, size=2).returncode, 0)
        self.assertEqual(self.run_cli(self.source).returncode, 0)

    def test_workflow_preflight_precedes_mutation_and_rechecks_before_credentials(self):
        workflow = (CLUSTER / "cluster-node-workflow.sh").read_text()
        initial = workflow.index('postgres_manifest="$(render_postgres_manifest)"')
        mutation = workflow.index('install -d -m 0700 "${cluster_config_dir}"')
        recheck = workflow.index('current_postgres_manifest="$(render_postgres_manifest)"')
        self.assertLess(initial, mutation)
        self.assertLess(mutation, recheck)
        self.assertLess(recheck, workflow.index('postgres_user="$(awk'))
        self.assertIn('get statefulset/postgres-db -o json', workflow)
        # Execute only production preflight function/assignment against a failed
        # read. No root, kubectl, stored environment, or workflow mutation runs.
        start = workflow.index('render_postgres_manifest() {')
        end = workflow.index('\n\n', initial)
        script = 'set -euo pipefail\nnamespace=borealis\nactive_size=1\nscript_dir="$TEST_CLUSTER"\nk3s() { return 1; }\n'
        result = subprocess.run(["bash", "-c", script + workflow[start:end] + '\nprintf reached-mutation'],
                                env=dict(os.environ, TEST_CLUSTER=str(CLUSTER)), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_workflow_final_resource_recheck_rejects_drift_and_failed_reads(self):
        workflow = (CLUSTER / "cluster-node-workflow.sh").read_text()
        start = workflow.index('render_postgres_manifest() {')
        initial = workflow.index('postgres_manifest="$(render_postgres_manifest)"')
        end = workflow.index('\n\n', initial)
        recheck = workflow.index('current_postgres_manifest="$(render_postgres_manifest)"')
        recheck_end = workflow.index('\n\n', recheck)
        with tempfile.TemporaryDirectory() as temp:
            source = pathlib.Path(temp) / "source.json"
            changed = pathlib.Path(temp) / "changed.json"
            source.write_text(json.dumps(self.source))
            value = copy.deepcopy(self.source)
            value["spec"]["template"]["spec"]["containers"][0]["resources"]["limits"]["memory"] = "8Gi"
            changed.write_text(json.dumps(value))
            for mode in ("same", "drift", "failed read with valid output"):
                with self.subTest(mode=mode):
                    script = 'set -euo pipefail\nnamespace=borealis\nactive_size=1\nscript_dir="$TEST_CLUSTER"\nk3s() { cat "$TEST_SOURCE"; return "${TEST_EXIT:-0}"; }\n'
                    script += workflow[start:end] + '\n'
                    if mode == "drift": script += 'TEST_SOURCE="$TEST_CHANGED"\n'
                    if mode == "failed read with valid output": script += 'TEST_EXIT=1\n'
                    script += workflow[recheck:recheck_end] + '\nprintf reached-apply'
                    result = subprocess.run(["bash", "-c", script], capture_output=True, text=True,
                                            env=dict(os.environ, TEST_CLUSTER=str(CLUSTER), TEST_SOURCE=str(source), TEST_CHANGED=str(changed)))
                    self.assertEqual(result.returncode == 0, mode == "same")
                    self.assertEqual(result.stdout, "reached-apply" if mode == "same" else "")
