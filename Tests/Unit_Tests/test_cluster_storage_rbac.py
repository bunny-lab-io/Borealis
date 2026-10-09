import contextlib
import copy
import importlib.util
import io
from pathlib import Path
import unittest
from unittest import mock

import yaml


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("k3s_policy", ROOT / "Tests/policy/check_k3s_manifests.py")
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class ClusterStorageRBACTests(unittest.TestCase):
    def setUp(self):
        self.objects = list(yaml.safe_load_all(
            (ROOT / "Data/Engine/K3s/cluster/controller.yaml").read_text(encoding="utf-8")
        ))

    def validate(self, objects):
        with mock.patch.object(policy.yaml, "safe_load_all", return_value=objects):
            policy.validate_cluster_controller_contract()

    def test_committed_controller_passes(self):
        self.validate(self.objects)

    def test_storage_role_rejects_permission_drift(self):
        mutations = {
            "extra setting": lambda role: role["rules"][0]["resourceNames"].append("unreviewed-setting"),
            "extra daemonset": lambda role: role["rules"][1]["resourceNames"].append("other-manager"),
            "wrong namespace": lambda role: role["metadata"].update(namespace="borealis"),
            "extra rule": lambda role: role["rules"].append({"apiGroups": [""], "resources": ["secrets"], "verbs": ["get"]}),
            "missing plugin read": lambda role: role["rules"][1]["resourceNames"].remove("longhorn-csi-plugin"),
            "missing manager rule": lambda role: role["rules"].pop(1),
        }
        for index in (0, 1):
            for verb in ("list", "watch", "create", "update", "patch", "delete", "*"):
                mutations[f"rule {index} verb {verb}"] = lambda role, i=index, v=verb: role["rules"][i]["verbs"].append(v)
            for field in ("apiGroups", "resources", "resourceNames"):
                mutations[f"rule {index} wildcard {field}"] = lambda role, i=index, f=field: role["rules"][i].update({f: ["*"]})
            mutations[f"rule {index} unrestricted names"] = lambda role, i=index: role["rules"][i].pop("resourceNames")
        for setting in ("default-engine-image", "support-bundle-manager-image"):
            mutations[f"missing {setting}"] = lambda role, s=setting: role["rules"][0]["resourceNames"].remove(s)
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                role = next(item for item in objects if item.get("kind") == "Role" and item["metadata"]["name"] == "borealis-cluster-storage-policy")
                mutate(role)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_ui_replicaset_role_rejects_permission_drift(self):
        mutations = {
            "missing read": lambda role: role["rules"].pop(2),
            "no verbs": lambda role: role["rules"][2].update(verbs=[]),
            "wrong group": lambda role: role["rules"][2].update(apiGroups=[""]),
            "extra resource": lambda role: role["rules"][2]["resources"].append("deployments"),
            "subresource": lambda role: role["rules"][2].update(resources=["replicasets/status"]),
            "fixed name cannot cover dynamic owners": lambda role: role["rules"][2].update(resourceNames=["longhorn-ui"]),
            "wildcard names do not match revisions": lambda role: role["rules"][2].update(resourceNames=["longhorn-ui-*"]),
            "duplicate rule": lambda role: role["rules"].append(copy.deepcopy(role["rules"][2])),
        }
        for verb in ("list", "watch", "create", "update", "patch", "delete", "deletecollection", "*"):
            mutations[f"verb {verb}"] = lambda role, v=verb: role["rules"][2]["verbs"].append(v)
        for field in ("apiGroups", "resources"):
            mutations[f"wildcard {field}"] = lambda role, f=field: role["rules"][2].update({f: ["*"]})
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                role = next(item for item in objects if item.get("kind") == "Role" and item["metadata"]["name"] == "borealis-cluster-storage-policy")
                mutate(role)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_ui_replicaset_read_cannot_move_to_cluster_role(self):
        objects = copy.deepcopy(self.objects)
        role = next(item for item in objects if item.get("kind") == "ClusterRole")
        role["rules"].append({"apiGroups": ["apps"], "resources": ["replicasets"], "verbs": ["get"]})
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.validate(objects)

    def test_storage_binding_rejects_scope_drift(self):
        mutations = {
            "wrong namespace": lambda binding: binding["metadata"].update(namespace="borealis"),
            "cluster binding": lambda binding: binding.update(kind="ClusterRoleBinding"),
            "cluster role": lambda binding: binding["roleRef"].update(kind="ClusterRole"),
            "wrong role": lambda binding: binding["roleRef"].update(name="other-role"),
            "wrong subject": lambda binding: binding["subjects"][0].update(name="other-controller"),
            "wrong subject namespace": lambda binding: binding["subjects"][0].update(namespace="default"),
            "extra subject": lambda binding: binding["subjects"].append({"kind": "Group", "name": "system:authenticated"}),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                binding = next(item for item in objects if item.get("kind") == "RoleBinding" and item["metadata"]["name"] == "borealis-cluster-storage-policy")
                mutate(binding)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_upgrade_role_rejects_permission_drift(self):
        mutations = {
            "wrong namespace": lambda role: role["metadata"].update(namespace="borealis"),
            "cluster role": lambda role: role.update(kind="ClusterRole"),
            "extra name": lambda role: role["rules"][0]["resourceNames"].append("other"),
            "all names": lambda role: role["rules"][0].pop("resourceNames"),
            "missing read": lambda role: role.update(rules=[]),
            "Secret": lambda role: role["rules"][0].update(resources=["secrets"]),
            "extra rule": lambda role: role["rules"].append({"apiGroups": [""], "resources": ["secrets"], "verbs": ["get"]}),
        }
        for verb in ("list", "watch", "create", "update", "patch", "delete", "*"):
            mutations[f"verb {verb}"] = lambda role, v=verb: role["rules"][0]["verbs"].append(v)
        for field in ("apiGroups", "resources", "resourceNames"):
            mutations[f"wildcard {field}"] = lambda role, f=field: role["rules"][0].update({f: ["*"]})
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                role = next(item for item in objects if item.get("kind") == "Role" and item["metadata"]["name"] == "borealis-cluster-upgrade-settings")
                mutate(role)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_upgrade_binding_rejects_scope_drift(self):
        mutations = {
            "wrong namespace": lambda binding: binding["metadata"].update(namespace="borealis"),
            "cluster binding": lambda binding: binding.update(kind="ClusterRoleBinding"),
            "cluster role": lambda binding: binding["roleRef"].update(kind="ClusterRole"),
            "wrong role": lambda binding: binding["roleRef"].update(name="other-role"),
            "wrong subject": lambda binding: binding["subjects"][0].update(name="other-controller"),
            "wrong subject namespace": lambda binding: binding["subjects"][0].update(namespace="default"),
            "extra subject": lambda binding: binding["subjects"].append({"kind": "Group", "name": "system:authenticated"}),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                binding = next(item for item in objects if item.get("kind") == "RoleBinding" and item["metadata"]["name"] == "borealis-cluster-upgrade-settings")
                mutate(binding)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_cnpg_role_rejects_permission_drift(self):
        mutations = {
            "wrong namespace": lambda role: role["metadata"].update(namespace="borealis"),
            "cluster role": lambda role: role.update(kind="ClusterRole"),
            "extra name": lambda role: role["rules"][0]["resourceNames"].append("other"),
            "all names": lambda role: role["rules"][0].pop("resourceNames"),
            "missing read": lambda role: role.update(rules=[]),
            "missing ConfigMap read": lambda role: role["rules"][0].update(resources=["secrets"]),
            "missing Secret read": lambda role: role["rules"][0].update(resources=["configmaps"]),
            "extra resource": lambda role: role["rules"][0]["resources"].append("services"),
            "extra API group": lambda role: role["rules"][0]["apiGroups"].append("apps"),
            "extra rule": lambda role: role["rules"].append({"apiGroups": [""], "resources": ["secrets"], "verbs": ["get"]}),
        }
        for verb in ("list", "watch", "create", "update", "patch", "delete", "*"):
            mutations[f"verb {verb}"] = lambda role, v=verb: role["rules"][0]["verbs"].append(v)
        for field in ("apiGroups", "resources", "resourceNames"):
            mutations[f"wildcard {field}"] = lambda role, f=field: role["rules"][0].update({f: ["*"]})
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                role = next(item for item in objects if item.get("kind") == "Role" and item["metadata"]["name"] == "borealis-cluster-cnpg-settings")
                mutate(role)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_cnpg_binding_rejects_scope_drift(self):
        mutations = {
            "wrong namespace": lambda binding: binding["metadata"].update(namespace="borealis"),
            "cluster binding": lambda binding: binding.update(kind="ClusterRoleBinding"),
            "cluster role": lambda binding: binding["roleRef"].update(kind="ClusterRole"),
            "wrong role": lambda binding: binding["roleRef"].update(name="other-role"),
            "wrong subject": lambda binding: binding["subjects"][0].update(name="other-controller"),
            "wrong subject namespace": lambda binding: binding["subjects"][0].update(namespace="default"),
            "extra subject": lambda binding: binding["subjects"].append({"kind": "Group", "name": "system:authenticated"}),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                binding = next(item for item in objects if item.get("kind") == "RoleBinding" and item["metadata"]["name"] == "borealis-cluster-cnpg-settings")
                mutate(binding)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_kube_vip_role_rejects_permission_drift(self):
        mutations = {
            "wrong namespace": lambda role: role["metadata"].update(namespace="borealis"),
            "cluster role": lambda role: role.update(kind="ClusterRole"),
            "extra name": lambda role: role["rules"][0]["resourceNames"].append("other"),
            "all names": lambda role: role["rules"][0].pop("resourceNames"),
            "missing read": lambda role: role.update(rules=[]),
            "wrong resource": lambda role: role["rules"][0].update(resources=["deployments"]),
            "extra resource": lambda role: role["rules"][0]["resources"].append("secrets"),
            "extra API group": lambda role: role["rules"][0]["apiGroups"].append(""),
            "extra rule": lambda role: role["rules"].append({"apiGroups": [""], "resources": ["secrets"], "verbs": ["get"]}),
        }
        for verb in ("list", "watch", "create", "update", "patch", "delete", "*"):
            mutations[f"verb {verb}"] = lambda role, v=verb: role["rules"][0]["verbs"].append(v)
        for field in ("apiGroups", "resources", "resourceNames"):
            mutations[f"wildcard {field}"] = lambda role, f=field: role["rules"][0].update({f: ["*"]})
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                role = next(item for item in objects if item.get("kind") == "Role" and item["metadata"]["name"] == "borealis-cluster-kube-vip-source")
                mutate(role)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_kube_vip_binding_rejects_scope_drift(self):
        mutations = {
            "wrong namespace": lambda binding: binding["metadata"].update(namespace="borealis"),
            "cluster binding": lambda binding: binding.update(kind="ClusterRoleBinding"),
            "cluster role": lambda binding: binding["roleRef"].update(kind="ClusterRole"),
            "wrong role": lambda binding: binding["roleRef"].update(name="other-role"),
            "wrong subject": lambda binding: binding["subjects"][0].update(name="other-controller"),
            "wrong subject namespace": lambda binding: binding["subjects"][0].update(namespace="default"),
            "wrong role API group": lambda binding: binding["roleRef"].update(apiGroup="other"),
            "extra subject": lambda binding: binding["subjects"].append({"kind": "Group", "name": "system:authenticated"}),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                objects = copy.deepcopy(self.objects)
                binding = next(item for item in objects if item.get("kind") == "RoleBinding" and item["metadata"]["name"] == "borealis-cluster-kube-vip-source")
                mutate(binding)
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    self.validate(objects)

    def test_kube_vip_objects_must_exist_once(self):
        for kind in ("Role", "RoleBinding"):
            for mode in ("missing", "duplicate"):
                with self.subTest(kind=kind, mode=mode):
                    objects = copy.deepcopy(self.objects)
                    item = next(item for item in objects if item.get("kind") == kind and item["metadata"]["name"] == "borealis-cluster-kube-vip-source")
                    if mode == "missing":
                        objects.remove(item)
                    else:
                        objects.append(copy.deepcopy(item))
                    with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                        self.validate(objects)

    def test_kube_vip_read_cannot_move_to_cluster_role(self):
        objects = copy.deepcopy(self.objects)
        role = next(item for item in objects if item.get("kind") == "ClusterRole")
        role["rules"].append({"apiGroups": ["apps"], "resources": ["daemonsets"], "resourceNames": ["kube-vip-borealis-cluster"], "verbs": ["get"]})
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.validate(objects)

    def test_configmap_read_cannot_move_to_cluster_role(self):
        objects = copy.deepcopy(self.objects)
        role = next(item for item in objects if item.get("kind") == "ClusterRole")
        role["rules"].append({"apiGroups": [""], "resources": ["configmaps"], "resourceNames": ["default-controller-env"], "verbs": ["get"]})
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.validate(objects)

    def test_cnpg_secret_read_cannot_move_to_cluster_role(self):
        objects = copy.deepcopy(self.objects)
        role = next(item for item in objects if item.get("kind") == "ClusterRole")
        role["rules"].append({"apiGroups": [""], "resources": ["secrets"], "resourceNames": ["cnpg-controller-manager-config"], "verbs": ["get"]})
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.validate(objects)


if __name__ == "__main__":
    unittest.main()
