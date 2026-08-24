from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest


REPO_ROOT = Path(__file__).resolve().parents[2]
MODULE_PATH = REPO_ROOT / "Data/Engine/K3s/cluster/reconcile-node-workloads.py"
SPEC = importlib.util.spec_from_file_location("borealis_cluster_workloads", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError("cluster workload reconciler module unavailable")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ClusterWorkloadReconcilerTests(unittest.TestCase):
    def test_cluster_workloads_mount_shared_wireguard_keys(self) -> None:
        for base in ("api-backend", "wireguard-tunnel"):
            pod_spec = {"volumes": [{"name": "wireguard-secrets", "hostPath": {"path": "/host"}}]}
            container = {"volumeMounts": [{"name": "wireguard-secrets", "mountPath": "/old"}]}
            MODULE.mount_shared_wireguard_keys(base, pod_spec, container)
            volume_name = "wireguard-secrets" if base == "api-backend" else "wireguard-server-keys"
            volume = next(item for item in pod_spec["volumes"] if item["name"] == volume_name)
            mount = next(item for item in container["volumeMounts"] if item["name"] == volume_name)
            self.assertEqual(volume["secret"]["secretName"], "borealis-wireguard-server-keys")
            self.assertEqual(mount["mountPath"], "/opt/Borealis/Engine/Services/wireguard-tunnel/secrets")
            self.assertTrue(mount["readOnly"])

    def test_cluster_edge_environment_replaces_stale_value(self) -> None:
        container = {"env": [{"name": "BOREALIS_CLUSTER_EDGE_VIP", "value": "192.0.2.2"}]}
        MODULE.set_container_environment(container, "BOREALIS_CLUSTER_EDGE_VIP", "192.168.3.248")
        self.assertEqual(container["env"], [{"name": "BOREALIS_CLUSTER_EDGE_VIP", "value": "192.168.3.248"}])

    def test_api_candidate_joins_only_aegis_peer_routing(self) -> None:
        metadata = MODULE.clean_metadata({}, "api-backend-candidate-engine-2", "engine-2", "a" * 40, "api-backend", True)
        self.assertEqual(metadata["labels"]["borealis.io/aegis-peer"], "true")
        self.assertEqual(metadata["labels"]["borealis.io/traffic-state"], "candidate")
        self.assertEqual(metadata["labels"]["app.kubernetes.io/name"], "api-backend-candidate")

    def test_first_candidate_promotion_derives_active_selector(self) -> None:
        candidate = {
            "spec": {
                "selector": {
                    "matchLabels": {
                        "app.kubernetes.io/name": "api-backend-candidate",
                        "borealis.io/engine-node": "engine-02",
                        "borealis.io/node-workload": "true",
                        "borealis.io/update-candidate": "true",
                        "borealis.io/traffic-state": "candidate",
                    }
                }
            }
        }
        selector = MODULE.promotion_selector("api-backend", "engine-02", candidate, None)
        self.assertEqual(selector["app.kubernetes.io/name"], "api-backend")
        self.assertEqual(selector["borealis.io/update-candidate"], "false")
        self.assertEqual(selector["borealis.io/traffic-state"], "active")
        self.assertEqual(selector["borealis.io/engine-node"], "engine-02")

    def test_existing_active_selector_remains_promotion_source(self) -> None:
        candidate = {"spec": {"selector": {"matchLabels": {"candidate-only": "true"}}}}
        active = {"spec": {"selector": {"matchLabels": {"stable-selector": "kept"}}}}
        selector = MODULE.promotion_selector("job-scheduler", "engine-03", candidate, active)
        self.assertEqual(selector["stable-selector"], "kept")
        self.assertNotIn("candidate-only", selector)


if __name__ == "__main__":
    unittest.main()
