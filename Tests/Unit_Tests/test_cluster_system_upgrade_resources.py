import copy
import hashlib
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
SPEC = importlib.util.spec_from_file_location("render_system_upgrade", CLUSTER / "render-system-upgrade.py")
RENDERER = importlib.util.module_from_spec(SPEC)
sys.dont_write_bytecode = True
SPEC.loader.exec_module(RENDERER)

# Synthetic bounds, never production defaults or sizing recommendations.
BOUNDS = {"requests": {"cpu": "100m", "memory": "64Mi"}, "limits": {"cpu": "0.2", "memory": "128Mi"}}
MANIFEST = """apiVersion: v1
kind: Namespace
metadata:
  name: system-upgrade
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: system-upgrade-controller
  namespace: system-upgrade
spec:
  strategy:
    type: Recreate
  template:
    spec:
      containers:
      - image: rancher/system-upgrade-controller:v0.20.1
        name: system-upgrade-controller
      serviceAccountName: system-upgrade
"""


class SystemUpgradeResourceTests(unittest.TestCase):
    def test_render_preserves_pinned_objects_except_resources(self):
        for cpu, memory in (("100m", "128Mi"), ("0.125", "256M"), ("8", "2Gi")):
            value = copy.deepcopy(BOUNDS)
            value["limits"] = {"cpu": cpu, "memory": memory}
            with self.subTest(cpu=cpu, memory=memory):
                rendered = list(yaml.safe_load_all(RENDERER.render(MANIFEST, json.dumps(value))))
                resources = rendered[1]["spec"]["template"]["spec"]["containers"][0].pop("resources")
                self.assertEqual(resources, value)
                self.assertEqual(rendered, list(yaml.safe_load_all(MANIFEST)))

    def test_rejects_invalid_bounds(self):
        cases = ["{}", "null", "[]", '{"requests":{},"requests":{}}', json.dumps({**BOUNDS, "unknown": {}})]
        for group in ("requests", "limits"):
            for field in ("cpu", "memory"):
                for bad in (None, True, 1, "", "0", "-1", "1e3", " 1", "1\n", "9999999999999999Gi", "$(id)"):
                    value = copy.deepcopy(BOUNDS)
                    value[group][field] = bad
                    cases.append(json.dumps(value))
                value = copy.deepcopy(BOUNDS)
                del value[group][field]
                cases.append(json.dumps(value))
        for field, value in (("cpu", "1"), ("memory", "129Mi")):
            changed = copy.deepcopy(BOUNDS)
            changed["requests"][field] = value
            cases.append(json.dumps(changed))
        for raw in cases:
            with self.subTest(raw=raw), self.assertRaises((ValueError, TypeError)):
                RENDERER.resources(raw)

    def test_rejects_template_drift(self):
        for template in (MANIFEST.replace("        name:", "        renamed:"), MANIFEST + MANIFEST,
                         MANIFEST + "        resources: {}\n"):
            with self.assertRaises(ValueError):
                RENDERER.render(template, json.dumps(BOUNDS))

    def test_cli_bounds_and_private_failure(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "resources.json"
            for data, valid in ((json.dumps(BOUNDS), True), ("private-material", False), (" " * 4097, False)):
                path.write_text(data)
                result = subprocess.run([sys.executable, str(CLUSTER / "render-system-upgrade.py"), "config", str(path)], capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, valid)
                if valid:
                    self.assertEqual(json.loads(result.stdout), BOUNDS)
                else:
                    self.assertEqual(result.stdout, "")
                    self.assertNotIn("private-material", result.stderr)
            path.unlink()
            with self.assertRaises(OSError):
                RENDERER.read_resources(path)

    def test_workflow_preflight_precedes_mutation(self):
        script = (CLUSTER / "cluster-node-workflow.sh").read_text()
        assignment = next(line for line in script.splitlines() if line.startswith('upgrade_resources="'))
        self.assertLess(script.index(assignment), script.index('install -d -m 0700'))
        self.assertIn('"${script_dir}/apply-pinned-dependencies.sh" "${upgrade_resources}"', script)
        with tempfile.TemporaryDirectory() as temp:
            result = subprocess.run(["bash", "-c", 'set -euo pipefail\nscript_dir="$TEST_CLUSTER"\nrepo_root="$TEST_ROOT"\n' + assignment + '\nprintf reached-mutation'],
                                    env=dict(os.environ, TEST_CLUSTER=str(CLUSTER), TEST_ROOT=temp), capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(result.stdout, "")

    def test_actual_apply_path_requires_frozen_bounds_and_checksum(self):
        for mode in ("valid", "canonical rewrite", "missing", "invalid", "caller drift", "download drift", "removed during download", "bad checksum", "template drift"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                cluster = root / "Data/Engine/K3s/cluster"
                cluster.mkdir(parents=True)
                for name in ("apply-pinned-dependencies.sh", "render-system-upgrade.py"):
                    (cluster / name).write_text((CLUSTER / name).read_text())
                deploy = root / "Engine/Deploy"
                deploy.mkdir(parents=True)
                source = deploy / "system-upgrade-resources.json"
                source.write_text(json.dumps(BOUNDS))
                payload = MANIFEST if mode != "template drift" else MANIFEST.replace("        name:", "        renamed:")
                manifest = root / "manifest.yaml"
                manifest.write_text(payload)
                digest = hashlib.sha256(payload.encode()).hexdigest() if mode != "bad checksum" else "0" * 64
                (cluster / "dependencies.lock").write_text(f"system-upgrade-controller|v0.20.1|https://example.invalid/pinned|{digest}\n")
                binaries = root / "bin"
                binaries.mkdir()
                curl = binaries / "curl"
                curl.write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
pathlib.Path(args[args.index('-o')+1]).write_bytes(pathlib.Path(os.environ['TEST_MANIFEST']).read_bytes())
p=pathlib.Path(os.environ['TEST_SOURCE'])
mode=os.environ['TEST_MODE']
if mode=='download drift':
 v=json.loads(p.read_text());v['limits']['memory']='256Mi';p.write_text(json.dumps(v))
if mode=='removed during download': p.unlink()
if mode=='canonical rewrite': p.write_text(json.dumps(json.loads(p.read_text()),sort_keys=True,indent=4))
''')
                kubectl = binaries / "kubectl"
                kubectl.write_text('''#!/usr/bin/env python3
import os,pathlib,sys
args=sys.argv[1:]
with open(os.environ['TEST_CALLS'],'a') as stream: stream.write(' '.join(args)+'\\n')
if args[0]=='apply': pathlib.Path(os.environ['TEST_APPLIED']).write_bytes(pathlib.Path(args[args.index('-f')+1]).read_bytes())
''')
                curl.chmod(0o755)
                kubectl.chmod(0o755)
                if mode == "missing": source.unlink()
                if mode == "invalid": source.write_text('{}')
                calls, applied = root / "calls", root / "applied.yaml"
                command = ["bash", str(cluster / "apply-pinned-dependencies.sh")]
                if mode == "caller drift": command.append('{}')
                env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"], BOREALIS_KUBECTL_BIN=str(kubectl),
                           TEST_MANIFEST=str(manifest), TEST_SOURCE=str(source), TEST_MODE=mode, TEST_CALLS=str(calls), TEST_APPLIED=str(applied))
                result = subprocess.run(command, env=env, capture_output=True, text=True)
                valid = mode in ("valid", "canonical rewrite")
                self.assertEqual(result.returncode == 0, valid, result.stderr)
                if valid:
                    container = list(yaml.safe_load_all(applied.read_text()))[1]["spec"]["template"]["spec"]["containers"][0]
                    self.assertEqual(container["resources"], BOUNDS)
                    self.assertEqual(calls.read_text().count('apply '), 1)
                else:
                    self.assertFalse(calls.exists(), result.stderr)
                    self.assertFalse(applied.exists())
