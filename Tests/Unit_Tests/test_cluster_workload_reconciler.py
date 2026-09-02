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


def patched_generic_deployment(container_name: str, image: str, revision: str) -> str:
    return json.dumps(
        {
            "metadata": {"annotations": {"borealis.io/revision": revision}},
            "spec": {
                "replicas": 0,
                "template": {
                    "metadata": {"annotations": {"borealis.io/revision": revision}},
                    "spec": {"containers": [{"name": container_name, "image": image}]},
                },
            },
        }
    )


class ClusterWorkloadReconcilerTests(unittest.TestCase):
    def test_cluster_workload_probe_guards_exceed_startup_budget(self) -> None:
        for base, delay in MODULE.PROBE_GUARD_DELAYS.items():
            annotations: dict[str, str] = {}
            containers = [
                {
                    "startupProbe": {"periodSeconds": 2, "failureThreshold": 30, "timeoutSeconds": 1},
                    "livenessProbe": {"initialDelaySeconds": 0},
                }
            ]
            MODULE.enforce_startup_liveness_guard(base, annotations, containers)
            self.assertEqual(containers[0]["livenessProbe"]["initialDelaySeconds"], delay)
            self.assertEqual(annotations["borealis.io/liveness-startup-guard"], str(delay))
            self.assertGreater(delay, 61)

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

    def test_cluster_virtual_ip_environment_replaces_stale_value(self) -> None:
        container = {"env": [{"name": "BOREALIS_CLUSTER_EDGE_VIP", "value": "192.0.2.2"}]}
        MODULE.set_container_environment(container, "BOREALIS_CLUSTER_EDGE_VIP", "192.168.3.248")
        self.assertEqual(container["env"], [{"name": "BOREALIS_CLUSTER_EDGE_VIP", "value": "192.168.3.248"}])

    def test_cluster_virtual_ip_reads_canonical_crd_field(self) -> None:
        with mock.patch.object(MODULE, "load_json", return_value={"spec": {"clusterVIP": "192.168.3.248"}}):
            self.assertEqual(MODULE.cluster_virtual_ip(), "192.168.3.248")
        for payload in ({"spec": {"clusterVIP": "8.8.8.8"}}, {"spec": {"clusterVIP": "2001:db8::1"}}, {}):
            with self.subTest(payload=payload), mock.patch.object(MODULE, "load_json", return_value=payload):
                with self.assertRaises(RuntimeError):
                    MODULE.cluster_virtual_ip()

    def test_api_candidate_joins_only_aegis_peer_routing(self) -> None:
        metadata = MODULE.clean_metadata({}, "api-backend-candidate-engine-2", "engine-2", "a" * 40, "api-backend", True)
        self.assertEqual(metadata["labels"]["borealis.io/aegis-peer"], "true")
        self.assertEqual(metadata["labels"]["borealis.io/traffic-state"], "candidate")
        self.assertEqual(metadata["labels"]["app.kubernetes.io/name"], "api-backend-candidate")

    def test_controller_candidate_pins_its_action_job_image(self) -> None:
        source = {
            "metadata": {"labels": {"app.kubernetes.io/name": "borealis-cluster-controller"}},
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app.kubernetes.io/name": "borealis-cluster-controller"}},
                "template": {
                    "metadata": {"labels": {"app.kubernetes.io/name": "borealis-cluster-controller"}},
                    "spec": {
                        "containers": [
                            {
                                "name": "controller",
                                "startupProbe": {"periodSeconds": 2, "failureThreshold": 30},
                                "livenessProbe": {"initialDelaySeconds": 0},
                            }
                        ]
                    },
                },
            },
        }
        calls: list[tuple[tuple[str, ...], str | None]] = []

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            calls.append((args, stdin))
            return ""

        image = "borealis-engine/api-backend:sha-" + "b" * 12
        with mock.patch.object(MODULE, "load_json", return_value=source), mock.patch.object(
            MODULE, "kubectl", side_effect=fake_kubectl
        ):
            MODULE.reconcile_one(
                "borealis-cluster-controller",
                "api-backend",
                "engine-02",
                "a" * 40,
                {"api-backend": image},
                True,
            )

        manifest = json.loads(next(stdin for args, stdin in calls if args[:2] == ("apply", "--server-side")))
        environment = {
            item["name"]: item.get("value")
            for item in manifest["spec"]["template"]["spec"]["containers"][0]["env"]
        }
        self.assertEqual(environment["BOREALIS_CLUSTER_ACTION_IMAGE"], image)
        self.assertEqual(environment["BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE"], "false")

    def test_reconcile_refreshes_node_clone_from_generic_template(self) -> None:
        stale_image = "borealis-engine/site-worker:sha-" + "a" * 12
        current_image = "borealis-engine/site-worker:sha-" + "b" * 12

        def deployment(image: str) -> dict:
            return {
                "metadata": {"labels": {"app.kubernetes.io/name": "borealis-operator"}},
                "spec": {
                    "replicas": 1,
                    "selector": {"matchLabels": {"app.kubernetes.io/name": "borealis-operator"}},
                    "template": {
                        "metadata": {"labels": {"app.kubernetes.io/name": "borealis-operator"}},
                        "spec": {
                            "containers": [
                                {
                                    "name": "borealis-operator",
                                    "env": [
                                        {
                                            "name": "BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST",
                                            "value": image,
                                        }
                                    ],
                                    "startupProbe": {"periodSeconds": 2, "failureThreshold": 30},
                                    "livenessProbe": {"initialDelaySeconds": 0},
                                }
                            ]
                        },
                    },
                },
            }

        calls: list[tuple[tuple[str, ...], str | None]] = []

        def fake_load(resource: str) -> dict | None:
            if resource == "deployment/borealis-operator-engine-01":
                active = deployment(stale_image)
                active["spec"]["selector"]["matchLabels"]["borealis.io/node-workload"] = "true"
                return active
            if resource == "deployment/borealis-operator":
                return deployment(current_image)
            return None

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            calls.append((args, stdin))
            return ""

        with mock.patch.object(MODULE, "load_json", side_effect=fake_load), mock.patch.object(
            MODULE, "kubectl", side_effect=fake_kubectl
        ):
            MODULE.reconcile_one(
                "borealis-operator",
                "borealis-operator",
                "engine-01",
                "c" * 40,
                {"borealis-operator": "borealis-engine/borealis-operator:sha-" + "d" * 12},
                False,
            )

        manifest = json.loads(next(stdin for args, stdin in calls if args[:2] == ("apply", "--server-side")))
        apply_args = next(args for args, _stdin in calls if args[:2] == ("apply", "--server-side"))
        self.assertIn("--force-conflicts", apply_args)
        self.assertEqual(
            manifest["spec"]["selector"]["matchLabels"]["borealis.io/node-workload"],
            "true",
        )
        self.assertEqual(
            manifest["spec"]["template"]["metadata"]["labels"]["borealis.io/node-workload"],
            "true",
        )
        environment = {
            item["name"]: item.get("value")
            for item in manifest["spec"]["template"]["spec"]["containers"][0]["env"]
        }
        self.assertEqual(environment["BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST"], current_image)

    def test_promotion_refreshes_zero_replica_generic_images_after_active_health(self) -> None:
        revision = "a" * 40
        target_image = "borealis-engine/borealis-operator:sha-" + "b" * 12
        selector = {
            "app.kubernetes.io/name": "borealis-operator-candidate",
            "borealis.io/engine-node": "engine-01",
            "borealis.io/node-workload": "true",
            "borealis.io/update-candidate": "true",
            "borealis.io/traffic-state": "candidate",
        }
        candidate = {
            "metadata": {"annotations": {"borealis.io/revision": revision}},
            "spec": {
                "selector": {"matchLabels": selector},
                "template": {
                    "metadata": {"labels": selector},
                    "spec": {"containers": [{"name": "borealis-operator", "image": target_image}]},
                },
            },
        }
        active = {
            "spec": {
                "selector": {
                    "matchLabels": {**selector, "app.kubernetes.io/name": "borealis-operator"}
                }
            }
        }
        generic = {
            "metadata": {"resourceVersion": "17"},
            "spec": {
                "replicas": 0,
                "template": {
                    "spec": {
                        "containers": [
                            {
                                "name": "borealis-operator",
                                "image": "borealis-engine/borealis-operator:sha-" + "c" * 12,
                            }
                        ]
                    }
                },
            }
        }
        calls: list[tuple[tuple[str, ...], str | None]] = []

        def fake_load(resource: str) -> dict | None:
            if resource.endswith("-candidate"):
                return candidate
            if resource == "deployment/borealis-operator-engine-01":
                return active
            if resource == "deployment/borealis-operator":
                return generic
            return None

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            calls.append((args, stdin))
            if args[:3] == ("-n", "borealis", "patch"):
                return patched_generic_deployment("borealis-operator", target_image, revision)
            return ""

        with mock.patch.object(MODULE, "load_json", side_effect=fake_load), mock.patch.object(
            MODULE, "kubectl", side_effect=fake_kubectl
        ):
            MODULE.promote_one("borealis-operator", "engine-01", revision)

        commands = [args for args, _stdin in calls]
        rollout = (
            "-n",
            "borealis",
            "rollout",
            "status",
            "deployment/borealis-operator-engine-01",
            "--timeout=10m",
        )
        patch_args = next(args for args in commands if args[:3] == ("-n", "borealis", "patch"))
        delete = (
            "-n",
            "borealis",
            "delete",
            "deployment/borealis-operator-engine-01-candidate",
            "--wait=true",
            "--timeout=5m",
        )
        self.assertLess(commands.index(rollout), commands.index(patch_args))
        self.assertLess(commands.index(patch_args), commands.index(delete))
        patch = json.loads(patch_args[patch_args.index("-p") + 1])
        self.assertEqual(patch["metadata"]["resourceVersion"], "17")
        self.assertEqual(patch["metadata"]["annotations"]["borealis.io/revision"], revision)
        self.assertEqual(
            patch["spec"]["template"]["metadata"]["annotations"]["borealis.io/revision"],
            revision,
        )
        self.assertEqual(
            patch["spec"]["template"]["spec"]["containers"],
            [{"name": "borealis-operator", "image": target_image}],
        )
        self.assertEqual(patch_args[-2:], ("-o", "json"))

    def test_generic_image_refresh_rejects_serving_template(self) -> None:
        promoted = {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [
                            {
                                "name": "borealis-operator",
                                "image": "borealis-engine/borealis-operator:sha-" + "b" * 12,
                            }
                        ]
                    }
                }
            }
        }
        generic = {
            "spec": {
                "replicas": 1,
                "template": {"spec": {"containers": [{"name": "borealis-operator"}]}},
            }
        }
        with mock.patch.object(MODULE, "load_json", return_value=generic), mock.patch.object(
            MODULE, "kubectl"
        ) as kubectl:
            with self.assertRaisesRegex(RuntimeError, "must remain at zero replicas"):
                MODULE.refresh_generic_template_images("borealis-operator", promoted, "a" * 40)
        kubectl.assert_not_called()

    def test_generic_image_refresh_requires_resource_version(self) -> None:
        generic = {
            "spec": {
                "replicas": 0,
                "template": {"spec": {"containers": [{"name": "borealis-operator"}]}},
            }
        }
        with mock.patch.object(MODULE, "load_json", return_value=generic), mock.patch.object(
            MODULE, "kubectl"
        ) as kubectl:
            with self.assertRaisesRegex(RuntimeError, "lacks resource version"):
                MODULE.refresh_generic_template_images("borealis-operator", {}, "a" * 40)
        kubectl.assert_not_called()

    def test_generic_image_refresh_rejects_stale_patch_response(self) -> None:
        revision = "a" * 40
        target_image = "borealis-engine/borealis-operator:sha-" + "b" * 12
        promoted = {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{"name": "borealis-operator", "image": target_image}]
                    }
                }
            }
        }
        generic = {
            "metadata": {"resourceVersion": "18"},
            "spec": {
                "replicas": 0,
                "template": {"spec": {"containers": [{"name": "borealis-operator"}]}},
            },
        }
        stale = patched_generic_deployment(
            "borealis-operator",
            "borealis-engine/borealis-operator:sha-" + "c" * 12,
            revision,
        )
        with mock.patch.object(MODULE, "load_json", return_value=generic), mock.patch.object(
            MODULE, "kubectl", return_value=stale
        ):
            with self.assertRaisesRegex(RuntimeError, "patch did not converge"):
                MODULE.refresh_generic_template_images("borealis-operator", promoted, revision)

    def test_generic_image_refresh_rejects_unexpected_patch_container(self) -> None:
        revision = "a" * 40
        target_image = "borealis-engine/borealis-operator:sha-" + "b" * 12
        promoted = {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{"name": "borealis-operator", "image": target_image}]
                    }
                }
            }
        }
        generic = {
            "metadata": {"resourceVersion": "21"},
            "spec": {
                "replicas": 0,
                "template": {"spec": {"containers": [{"name": "borealis-operator"}]}},
            },
        }
        updated = json.loads(patched_generic_deployment("borealis-operator", target_image, revision))
        updated["spec"]["template"]["spec"]["containers"].append(
            {
                "name": "unexpected-sidecar",
                "image": "borealis-engine/unexpected:sha-" + "c" * 12,
            }
        )
        with mock.patch.object(MODULE, "load_json", return_value=generic), mock.patch.object(
            MODULE, "kubectl", return_value=json.dumps(updated)
        ):
            with self.assertRaisesRegex(RuntimeError, "patch did not converge"):
                MODULE.refresh_generic_template_images("borealis-operator", promoted, revision)

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
                    "spec": {
                        "containers": [
                            {
                                "name": "traefik-edge",
                                "startupProbe": {
                                    "periodSeconds": 2,
                                    "failureThreshold": 60,
                                    "timeoutSeconds": 5,
                                },
                                "livenessProbe": {"initialDelaySeconds": 0},
                            }
                        ]
                    },
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
        self.assertEqual(
            manifest["spec"]["template"]["spec"]["containers"][0]["livenessProbe"]["initialDelaySeconds"],
            130,
        )
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
                    "spec": {
                        "containers": [
                            {
                                "name": "traefik-edge",
                                "image": "borealis-engine/traefik-edge:sha-" + "b" * 12,
                            }
                        ]
                    },
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
        generic = {
            "metadata": {"resourceVersion": "19"},
            "spec": {
                "replicas": 0,
                "template": {"spec": {"containers": [{"name": "traefik-edge"}]}},
            }
        }
        commands: list[tuple[str, ...]] = []

        def fake_load(resource: str) -> dict | None:
            if resource.endswith("-candidate"):
                return candidate
            if resource == "deployment/traefik-edge":
                return generic
            return active

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            commands.append(args)
            if args[:3] == ("-n", "borealis", "patch"):
                return patched_generic_deployment(
                    "traefik-edge",
                    "borealis-engine/traefik-edge:sha-" + "b" * 12,
                    "a" * 40,
                )
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

    def test_wireguard_promotion_adapts_pinned_standby_readiness(self) -> None:
        selector = {
            "app.kubernetes.io/name": "wireguard-tunnel-candidate",
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
                    "spec": {
                        "containers": [
                            {
                                "name": "wireguard-tunnel",
                                "image": "borealis-engine/wireguard-tunnel:sha-" + "b" * 12,
                                "readinessProbe": {
                                    "exec": {"command": ["borealis-wireguard-healthcheck"]},
                                    "periodSeconds": 5,
                                },
                            }
                        ]
                    },
                },
            },
        }
        active = {
            "metadata": {},
            "spec": {
                "replicas": 1,
                "selector": {
                    "matchLabels": {**selector, "app.kubernetes.io/name": "wireguard-tunnel"}
                },
            },
        }
        generic = {
            "metadata": {"resourceVersion": "20"},
            "spec": {
                "replicas": 0,
                "template": {"spec": {"containers": [{"name": "wireguard-tunnel"}]}},
            }
        }
        calls: list[tuple[tuple[str, ...], str | None]] = []

        def fake_load(resource: str) -> dict | None:
            if resource.endswith("-candidate"):
                return candidate
            if resource == "deployment/wireguard-tunnel":
                return generic
            return active

        def fake_kubectl(*args: str, stdin: str | None = None) -> str:
            calls.append((args, stdin))
            if args[:3] == ("-n", "borealis", "patch"):
                return patched_generic_deployment(
                    "wireguard-tunnel",
                    "borealis-engine/wireguard-tunnel:sha-" + "b" * 12,
                    "a" * 40,
                )
            return ""

        with mock.patch.object(MODULE, "load_json", side_effect=fake_load), mock.patch.object(
            MODULE, "kubectl", side_effect=fake_kubectl
        ):
            MODULE.promote_one("wireguard-tunnel", "engine-01", "a" * 40)

        manifest = json.loads(next(stdin for args, stdin in calls if args[:2] == ("apply", "--server-side")))
        probe = manifest["spec"]["template"]["spec"]["containers"][0]["readinessProbe"]
        self.assertEqual(probe["periodSeconds"], 5)
        command = probe["exec"]["command"]
        self.assertEqual(command[:2], ["sh", "-ec"])
        self.assertIn('"stdout":"standby"', command[2])
        self.assertIn("ip link show", command[2])
        self.assertIn("exec borealis-wireguard-healthcheck", command[2])
        self.assertIn(
            ("-n", "borealis", "rollout", "status", "deployment/wireguard-tunnel-engine-01", "--timeout=10m"),
            [args for args, _ in calls],
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
