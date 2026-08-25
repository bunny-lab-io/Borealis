from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import unittest
from unittest import mock


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

    def test_reconcile_drops_controller_owned_deployment_annotations(self) -> None:
        metadata = MODULE.clean_metadata(
            {
                "annotations": {
                    "deployment.kubernetes.io/revision": "7",
                    "kubectl.kubernetes.io/last-applied-configuration": "{}",
                    "borealis.io/retained": "true",
                }
            },
            "api-backend-candidate-engine-2",
            "engine-2",
            "a" * 40,
            "api-backend",
            True,
        )
        self.assertNotIn("deployment.kubernetes.io/revision", metadata["annotations"])
        self.assertNotIn("kubectl.kubernetes.io/last-applied-configuration", metadata["annotations"])
        self.assertEqual(metadata["annotations"]["borealis.io/retained"], "true")

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
        self.assertEqual(selector, {"stable-selector": "kept"})

    def test_host_port_candidate_stages_at_zero_replicas(self) -> None:
        source = {
            "metadata": {"labels": {"app.kubernetes.io/name": "traefik-edge"}},
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app.kubernetes.io/name": "traefik-edge"}},
                "template": {
                    "metadata": {"labels": {"app.kubernetes.io/name": "traefik-edge"}},
                    "spec": {"containers": [{"name": "traefik-edge"}]},
                },
            },
        }
        calls: list[tuple[tuple[str, ...], str | None]] = []

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            calls.append((args, stdin))
            return ""

        with mock.patch.object(
            MODULE,
            "load_json",
            side_effect=lambda resource: source if resource.endswith("traefik-edge") else None,
        ), mock.patch.object(MODULE, "kubectl", side_effect=fake_kubectl):
            MODULE.reconcile_one(
                "traefik-edge",
                "traefik-edge",
                "engine-01",
                "a" * 40,
                {"traefik-edge": "borealis-engine/traefik-edge:sha-" + "b" * 12},
                True,
            )

        manifest = json.loads(next(stdin for args, stdin in calls if args[:2] == ("apply", "--server-side")))
        self.assertEqual(manifest["spec"]["replicas"], 0)
        self.assertGreaterEqual(manifest["spec"]["minReadySeconds"], 15)
        self.assertFalse(any(args[:3] == ("-n", "borealis", "rollout") for args, _ in calls))

    def test_traefik_promotion_uses_bounded_host_port_handoff(self) -> None:
        selector = {
            "app.kubernetes.io/name": "traefik-edge-candidate",
            "borealis.io/engine-node": "engine-01",
            "borealis.io/node-workload": "true",
            "borealis.io/update-candidate": "true",
            "borealis.io/traffic-state": "candidate",
        }
        candidate = {
            "metadata": {"annotations": {"borealis.io/revision": "a" * 40}},
            "spec": {
                "replicas": 0,
                "selector": {"matchLabels": selector},
                "template": {
                    "metadata": {"labels": selector},
                    "spec": {"containers": [{"name": "traefik-edge"}]},
                },
            },
        }
        active = {
            "metadata": {},
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {**selector, "app.kubernetes.io/name": "traefik-edge"}},
            },
        }
        commands: list[tuple[str, ...]] = []

        def fake_load(resource: str) -> dict | None:
            return candidate if resource.endswith("-candidate") else active

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            commands.append(args)
            return ""

        with mock.patch.object(MODULE, "load_json", side_effect=fake_load), mock.patch.object(
            MODULE, "kubectl", side_effect=fake_kubectl
        ):
            MODULE.promote_one("traefik-edge", "engine-01", "a" * 40)

        active_name = "deployment/traefik-edge-engine-01"
        candidate_name = "deployment/traefik-edge-engine-01-candidate"
        self.assertLess(
            commands.index(("-n", "borealis", "scale", active_name, "--replicas=0")),
            commands.index(("-n", "borealis", "scale", candidate_name, "--replicas=1")),
        )
        self.assertLess(
            commands.index(("-n", "borealis", "rollout", "status", candidate_name, "--timeout=10m")),
            commands.index(("-n", "borealis", "scale", candidate_name, "--replicas=0")),
        )

    def test_promotion_rejects_candidate_from_different_revision(self) -> None:
        candidate = {
            "metadata": {"annotations": {"borealis.io/revision": "a" * 40}},
            "spec": {"selector": {"matchLabels": {"candidate": "true"}}},
        }
        with mock.patch.object(MODULE, "load_json", side_effect=[candidate, None]):
            with self.assertRaisesRegex(RuntimeError, "does not match requested revision"):
                MODULE.promote_one("api-backend", "engine-01", "b" * 40)


if __name__ == "__main__":
    unittest.main()
