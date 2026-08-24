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
