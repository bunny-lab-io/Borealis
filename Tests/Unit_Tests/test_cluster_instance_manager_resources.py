"""Offline producer contracts. These tests do not evaluate CEL or qualify admission."""

import copy
import importlib.util
import json
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "Data/Engine/K3s/cluster/render-longhorn-instance-manager.py"
SPEC = importlib.util.spec_from_file_location("instance_resources", SCRIPT)
RENDERER = importlib.util.module_from_spec(SPEC)
sys.dont_write_bytecode = True
SPEC.loader.exec_module(RENDERER)
# Synthetic inputs only. CPU request deliberately absent: Longhorn owns it.
BOUNDS = {"requests": {"memory": "64Mi"}, "limits": {"cpu": "1.5", "memory": "128Mi"}}


class InstanceManagerResourcesTests(unittest.TestCase):
    def test_probe_verifier_requires_admitted_frozen_bounds(self):
        bounds = json.dumps(BOUNDS)
        probe = json.loads(RENDERER.render_probe())
        with self.assertRaises(ValueError):
            RENDERER.verify_probe(bounds, json.dumps(probe))
        resources = probe["spec"]["containers"][0]["resources"]
        resources["requests"]["memory"] = "67108864"
        resources["limits"] = {"cpu": "1500m", "memory": "134217728"}
        self.assertEqual(RENDERER.verify_probe(bounds, json.dumps(probe)), "")
        for section, field, value in (("requests", "cpu", "2m"), ("requests", "memory", "65Mi"),
                                      ("limits", "cpu", "2"), ("limits", "memory", "129Mi")):
            changed = copy.deepcopy(probe)
            changed["spec"]["containers"][0]["resources"][section][field] = value
            with self.subTest(section=section, field=field), self.assertRaises(ValueError):
                RENDERER.verify_probe(bounds, json.dumps(changed))
        for raw in ("{}", "null", "[]", '{"kind":"Pod","kind":"Pod"}', 'x' * (2 * 1024 * 1024 + 1)):
            with self.subTest(raw=raw[:40]), self.assertRaises((ValueError, KeyError, TypeError)):
                RENDERER.verify_probe(bounds, raw)

    def test_config_contract_and_quantities(self):
        self.assertEqual(json.loads(RENDERER.resources(json.dumps(BOUNDS))), BOUNDS)
        bad = ["null", "[]", "{}", '{"requests":{},"requests":{},"limits":{}}']
        for section, fields in (("requests", ["memory"]), ("limits", ["cpu", "memory"])):
            for field in fields:
                for value in (None, True, 1, "", "0", "-1", "1e3", " 1", "1\n", "9999999999999999Ti", '"}] + object.spec.containers'):
                    altered = copy.deepcopy(BOUNDS)
                    altered[section][field] = value
                    bad.append(json.dumps(altered))
                altered = copy.deepcopy(BOUNDS)
                del altered[section][field]
                bad.append(json.dumps(altered))
        for section, field in (("requests", "cpu"), ("requests", "ephemeral-storage"), ("limits", "hugepages-2Mi")):
            altered = copy.deepcopy(BOUNDS)
            altered[section][field] = "1"
            bad.append(json.dumps(altered))
        bad += [json.dumps({**BOUNDS, "unknown": "private"}), json.dumps({**BOUNDS, "requests": {"memory": "129Mi"}})]
        bad.append('{"requests":{"memory":"1Mi","memory":"2Mi"},"limits":{"cpu":"1","memory":"3Mi"}}')
        for raw in bad:
            with self.subTest(raw=raw), self.assertRaises((ValueError, TypeError)):
                RENDERER.resources(raw)
        exact = {"requests": {"memory": "9007199254740991"}, "limits": {"cpu": "1500m", "memory": "9007199254740991"}}
        self.assertEqual(json.loads(RENDERER.resources(json.dumps(exact))), exact)

    def test_policy_scope_and_failure_contract(self):
        bundle = json.loads(RENDERER.render(json.dumps(BOUNDS)))
        self.assertEqual((bundle["apiVersion"], bundle["kind"]), ("v1", "List"))
        policy, binding = bundle["items"]
        self.assertEqual(policy["apiVersion"], "admissionregistration.k8s.io/v1")
        self.assertEqual(policy["kind"], "MutatingAdmissionPolicy")
        self.assertEqual(binding["kind"], "MutatingAdmissionPolicyBinding")
        self.assertEqual(binding["spec"]["policyName"], policy["metadata"]["name"])
        spec = policy["spec"]
        self.assertEqual(spec["failurePolicy"], "Fail")
        self.assertEqual(spec["reinvocationPolicy"], "IfNeeded")
        match = spec["matchConstraints"]
        self.assertEqual(match["matchPolicy"], "Exact")
        self.assertEqual(match["resourceRules"], [{"apiGroups": [""], "apiVersions": ["v1"], "operations": ["CREATE"], "resources": ["pods"], "scope": "Namespaced"}])
        self.assertEqual(match["namespaceSelector"], {"matchLabels": {"kubernetes.io/metadata.name": "longhorn-system"}})
        self.assertEqual(match["objectSelector"], {"matchLabels": {"longhorn.io/component": "instance-manager"}})
        self.assertEqual(binding["spec"]["matchResources"]["namespaceSelector"], match["namespaceSelector"])
        self.assertEqual(spec["matchConditions"], [{"name": "longhorn-producer", "expression": 'request.userInfo.username == "system:serviceaccount:longhorn-system:longhorn-service-account"'}])
        # Resource shape must be checked inside the failing mutation, never a
        # match condition that silently skips an unbounded selected Pod.
        supported = spec["variables"][0]["expression"]
        for required in ('"longhorn.io/data-engine"] == "v1"', '"longhorn.io/instance-manager-type"] == "aio"', 'containers.size() == 1', 'name == "instance-manager"', 'initContainers.size() == 0', '!has(object.spec.resources)', '!has(object.spec.overhead)', 'resizePolicy.size() == 0', 'limits.size() == 2', '.requests.all(k, k in ["cpu", "memory"])'):
            self.assertIn(required, supported)
        self.assertEqual(supported.count(".compareTo("), 4)
        expression = spec["mutations"][0]["jsonPatch"]["expression"]
        self.assertTrue(expression.startswith("variables.supported ? ["))
        self.assertTrue(expression.endswith('] : {}["unsupported instance-manager resource shape or bounds"]'))

    def test_patch_payload_preserves_native_cpu_and_other_pod_fields(self):
        for cpu in ("1m", "1200m", "1"):
            before = {"metadata": {"name": "instance-manager-example", "ownerReferences": [{"uid": "owner"}]}, "spec": {"nodeName": "engine-a", "containers": [{"name": "instance-manager", "image": "reviewed-image", "args": ["instance-manager", "daemon"], "resources": {"requests": {"cpu": cpu}}}]}}
            pod = copy.deepcopy(before)
            policy = json.loads(RENDERER.render(json.dumps(BOUNDS)))["items"][0]
            expression = policy["spec"]["mutations"][0]["jsonPatch"]["expression"]
            # Exercise the emitted literal JSON Patch payload, not a Python CEL
            # imitation. Server-side guard evaluation is a separate required gate.
            patches = re.findall(r'JSONPatch\{op: "add", path: ("[^"]+"), value: (.+)\}', expression)
            self.assertEqual([json.loads(path) for path, _ in patches], ["/spec/containers/0/resources/requests/memory", "/spec/containers/0/resources/limits"])
            for _ in range(2):
                for path, raw in patches:
                    parts = json.loads(path).strip("/").split("/")
                    obj = pod
                    for part in parts[:-1]:
                        obj = obj[int(part)] if isinstance(obj, list) else obj[part]
                    obj[parts[-1]] = json.loads(raw)
                bounds = pod["spec"]["containers"][0]["resources"]
                self.assertEqual(bounds, {"requests": {"cpu": cpu, "memory": "64Mi"}, "limits": BOUNDS["limits"]})
            del bounds["requests"]["memory"]
            del bounds["limits"]
            self.assertEqual(pod, before)

    def test_cli_private_failures_and_bounded_input(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "instance.json"
            cases = [(json.dumps(BOUNDS).encode(), True), (b"private-material", False), (b" " * 4097, False), (b"\xff", False), (b"[" * 1500 + b"]" * 1500, False)]
            for raw, valid in cases:
                path.write_bytes(raw)
                for mode in ("config", "policy"):
                    result = subprocess.run([sys.executable, str(SCRIPT), mode, str(path)], capture_output=True, text=True)
                    self.assertEqual(result.returncode == 0, valid, result.stderr)
                    if valid:
                        json.loads(result.stdout)
                    else:
                        self.assertEqual(result.stdout, "")
                        self.assertNotIn("private-material", result.stderr)
                        self.assertNotIn("Traceback", result.stderr)
            path.unlink()
            for args in (["policy", str(path)], ["apply", str(path)], ["policy", str(path), "unexpected"]):
                result = subprocess.run([sys.executable, str(SCRIPT), *args], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
