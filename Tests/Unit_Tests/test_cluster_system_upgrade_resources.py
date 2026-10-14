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
CERT_BOUNDS = {"requests": {"cpu": "50m", "memory": "32Mi"}, "limits": {"cpu": "0.1", "memory": "64Mi"}}
WEBHOOK_BOUNDS = {"requests": {"cpu": "20m", "memory": "16Mi"}, "limits": {"cpu": "0.075", "memory": "48Mi"}}
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


CERT_MANIFEST = """apiVersion: apps/v1
kind: Deployment
metadata:
  name: cert-manager
  namespace: cert-manager
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: cert-manager-controller
          image: quay.io/jetstack/cert-manager-controller:v1.21.1
      nodeSelector:
        kubernetes.io/os: linux
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cert-manager-webhook
  namespace: cert-manager
spec:
  template:
    spec:
      containers:
        - name: cert-manager-webhook
          image: quay.io/jetstack/cert-manager-webhook:v1.21.1
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cert-manager-cainjector
  namespace: cert-manager
spec:
  template:
    spec:
      containers:
        - name: cert-manager-cainjector
          resources: {}
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

    def test_cert_manager_changes_only_controller_resources(self):
        rendered = list(yaml.safe_load_all(RENDERER.render_cert_manager(CERT_MANIFEST, json.dumps(BOUNDS))))
        resource = rendered[0]["spec"]["template"]["spec"]["containers"][0].pop("resources")
        self.assertEqual(resource, BOUNDS)
        self.assertEqual(rendered, list(yaml.safe_load_all(CERT_MANIFEST)))
        for template in (CERT_MANIFEST.replace("  name: cert-manager\n", "  name: other\n"),
                         CERT_MANIFEST + "\n---\n" + CERT_MANIFEST,
                         CERT_MANIFEST.replace("        - name: cert-manager-controller", "        - name: other"),
                         RENDERER.render_cert_manager(CERT_MANIFEST, json.dumps(BOUNDS))):
            with self.subTest(template=template), self.assertRaises(ValueError):
                RENDERER.render_cert_manager(template, json.dumps(BOUNDS))
        with self.assertRaises(ValueError):
            RENDERER.render_cert_manager(CERT_MANIFEST, '{}')

    def test_webhook_render_preserves_other_objects_and_rejects_drift(self):
        render = RENDERER.render_cert_manager_webhook
        rendered = list(yaml.safe_load_all(render(CERT_MANIFEST, json.dumps(WEBHOOK_BOUNDS))))
        resources = rendered[1]["spec"]["template"]["spec"]["containers"][0].pop("resources")
        self.assertEqual(resources, WEBHOOK_BOUNDS)
        self.assertEqual(rendered, list(yaml.safe_load_all(CERT_MANIFEST)))
        for template in (CERT_MANIFEST.replace("  name: cert-manager-webhook\n", "  name: other\n"),
                         CERT_MANIFEST + "\n---\n" + CERT_MANIFEST,
                         CERT_MANIFEST.replace("        - name: cert-manager-webhook", "        - name: other"),
                         render(CERT_MANIFEST, json.dumps(WEBHOOK_BOUNDS))):
            with self.subTest(template=template), self.assertRaises(ValueError):
                render(template, json.dumps(WEBHOOK_BOUNDS))
        with self.assertRaises(ValueError):
            render(CERT_MANIFEST, '{}')
        with self.assertRaises(ValueError):
            RENDERER.render_cert_manager_deployment(CERT_MANIFEST, json.dumps(BOUNDS), "cert-manager-cainjector")

    def test_workflow_preflight_precedes_mutation(self):
        script = (CLUSTER / "cluster-node-workflow.sh").read_text()
        self.assertIn('"${script_dir}/apply-pinned-dependencies.sh" "${upgrade_resources}" "${cert_resources}" "${webhook_resources}"', script)
        for prefix in ('upgrade_resources="', 'cert_resources="', 'webhook_resources="'):
            assignment = next(line for line in script.splitlines() if line.startswith(prefix))
            self.assertLess(script.index(assignment), script.index('install -d -m 0700'))
            with tempfile.TemporaryDirectory() as temp:
                result = subprocess.run(["bash", "-c", 'set -euo pipefail\nscript_dir="$TEST_CLUSTER"\nrepo_root="$TEST_ROOT"\n' + assignment + '\nprintf reached-mutation'],
                                        env=dict(os.environ, TEST_CLUSTER=str(CLUSTER), TEST_ROOT=temp), capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")

    def test_actual_apply_path_requires_frozen_bounds_and_checksum(self):
        for component in ("system-upgrade-controller", "cert-manager", "cert-manager-webhook"):
            self.check_apply_path(component)

    def check_apply_path(self, component):
        dependency = "system-upgrade-controller" if component == "system-upgrade-controller" else "cert-manager"
        for mode in ("valid", "canonical rewrite", "missing", "invalid", "caller drift", "download drift", "removed during download", "bad checksum", "template drift", "other missing", "other download drift", "legacy caller"):
            with self.subTest(component=component, mode=mode), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                cluster = root / "Data/Engine/K3s/cluster"
                cluster.mkdir(parents=True)
                for name in ("apply-pinned-dependencies.sh", "render-system-upgrade.py"):
                    (cluster / name).write_text((CLUSTER / name).read_text())
                deploy = root / "Engine/Deploy"
                deploy.mkdir(parents=True)
                upgrade_source = deploy / "system-upgrade-resources.json"
                cert_source = deploy / "cert-manager-controller-resources.json"
                upgrade_source.write_text(json.dumps(BOUNDS))
                cert_source.write_text(json.dumps(CERT_BOUNDS))
                webhook_source = deploy / "cert-manager-webhook-resources.json"
                webhook_source.write_text(json.dumps(WEBHOOK_BOUNDS))
                sources = {"system-upgrade-controller": upgrade_source, "cert-manager": cert_source, "cert-manager-webhook": webhook_source}
                source = sources[component]
                other_source = cert_source if component == "cert-manager-webhook" else webhook_source
                template = CERT_MANIFEST if dependency == "cert-manager" else MANIFEST
                payload = template
                if mode == "template drift":
                    payload = template.replace("  name: cert-manager-webhook\n", "  name: other\n") if component == "cert-manager-webhook" else template.replace("name:", "renamed:")
                manifest = root / "manifest.yaml"
                manifest.write_text(payload)
                digest = hashlib.sha256(payload.encode()).hexdigest() if mode != "bad checksum" else "0" * 64
                (cluster / "dependencies.lock").write_text(f"{dependency}|test-version|https://example.invalid/pinned|{digest}\n")
                binaries = root / "bin"
                binaries.mkdir()
                curl = binaries / "curl"
                curl.write_text('''#!/usr/bin/env python3
import json,os,pathlib,sys
args=sys.argv[1:]
pathlib.Path(os.environ['TEST_DOWNLOAD']).touch()
pathlib.Path(args[args.index('-o')+1]).write_bytes(pathlib.Path(os.environ['TEST_MANIFEST']).read_bytes())
p=pathlib.Path(os.environ['TEST_SOURCE'])
mode=os.environ['TEST_MODE']
if mode=='download drift':
 v=json.loads(p.read_text());v['limits']['memory']='256Mi';p.write_text(json.dumps(v))
if mode=='removed during download': p.unlink()
if mode=='canonical rewrite': p.write_text(json.dumps(json.loads(p.read_text()),sort_keys=True,indent=4))
if mode=='other download drift': pathlib.Path(os.environ['TEST_OTHER_SOURCE']).write_text('{}')
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
                if mode == "other missing": other_source.unlink()
                calls, applied = root / "calls", root / "applied.yaml"
                command = ["bash", str(cluster / "apply-pinned-dependencies.sh")]
                command.extend(RENDERER.resources(json.dumps(bounds)) for bounds in (BOUNDS, CERT_BOUNDS, WEBHOOK_BOUNDS))
                if mode == "caller drift": command[{"system-upgrade-controller": 2, "cert-manager": 3, "cert-manager-webhook": 4}[component]] = '{}'
                if mode == "canonical rewrite": command = command[:2]  # Standalone entrypoint snapshots all files.
                if mode == "legacy caller": command = command[:4]
                env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"], BOREALIS_KUBECTL_BIN=str(kubectl),
                           TEST_DOWNLOAD=str(root / "downloaded"), TEST_MANIFEST=str(manifest), TEST_SOURCE=str(source), TEST_OTHER_SOURCE=str(other_source), TEST_MODE=mode, TEST_CALLS=str(calls), TEST_APPLIED=str(applied))
                result = subprocess.run(command, env=env, capture_output=True, text=True)
                valid = mode in ("valid", "canonical rewrite")
                self.assertEqual(result.returncode == 0, valid, result.stderr)
                if valid:
                    docs = list(yaml.safe_load_all(applied.read_text()))
                    expected = ((0, CERT_BOUNDS), (1, WEBHOOK_BOUNDS)) if dependency == "cert-manager" else ((1, BOUNDS),)
                    for index, bounds in expected:
                        container = docs[index]["spec"]["template"]["spec"]["containers"][0]
                        self.assertEqual(container.pop("resources"), bounds)
                    self.assertEqual(docs, list(yaml.safe_load_all(template)))
                    self.assertEqual(calls.read_text().count('apply '), 1)
                else:
                    self.assertFalse(calls.exists(), result.stderr)
                    self.assertFalse(applied.exists())
                    if mode in ("missing", "invalid", "caller drift", "other missing", "legacy caller"):
                        self.assertFalse((root / "downloaded").exists())
