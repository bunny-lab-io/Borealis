import base64
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest import mock
import urllib.error


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location(
    "legacy_admission_recovery", ROOT / "Data/Engine/K3s/cluster/repair-legacy-admission.py")
RECOVERY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RECOVERY)
OPERATION = "c13c7396-3e60-45e2-8223-4315a37b85d6"
NODE_UID = "00000000-0000-4000-8000-000000000002"
CLUSTER = "00000000-0000-4000-8000-000000000001"
COHORT = ["00000000-0000-4000-8000-000000000003",
          "00000000-0000-4000-8000-000000000004"]
SHA = "b" * 40
ENDPOINT = "https://cluster.example.test"
LITERAL = "fixture-only-$(touch SHOULD_NOT_EXIST)-" + chr(96) + "false" + chr(96) + "-$literal=tail"


def snapshot():
    return {
        "enabled": True, "status": "Degraded Quorum", "active_size": 1, "desired_size": 1,
        "active_operation_id": None, "hmr": {"state": "inactive"}, "cluster_id": CLUSTER,
        "baseline_sha": RECOVERY.LEGACY_SHA, "baseline_release": "dev-" + RECOVERY.LEGACY_SHA[:12],
        "database": {"fully_ready": True, "durability_quorum": True,
                     "ready_instances": 3, "configured_instances": 3},
        "nodes": [{"node_name": "engine-01", "management_ip": "192.168.50.21",
                   "membership_state": "Active", "application_state": "active"}],
        "admissions": [
            {"id": COHORT[0], "node_name": "engine-02", "state": "Approved", "management_ip": "192.168.50.22"},
            {"id": COHORT[1], "node_name": "engine-03", "state": "Approved", "management_ip": "192.168.50.23"}],
        "operations": [{"id": OPERATION, "kind": "membership_admit", "state": "failed",
                        "current_step": "apply_membership", "attempt": 4,
                        "payload": {"admission_ids": COHORT, "node_names": ["engine-02", "engine-03"],
                                    "action_image": "borealis-engine/api-backend:sha-bc1c328ee3ba",
                                    "baseline_sha": RECOVERY.LEGACY_SHA,
                                    "baseline_release": "dev-" + RECOVERY.LEGACY_SHA[:12],
                                    "k3s_version": RECOVERY.K3S_VERSION}}],
    }


def secret():
    values = {
        "BOREALIS_ENGINE_NETWORK_MODE": "public",
        "BOREALIS_PUBLIC_HOSTNAME": "cluster.example.test",
        "BOREALIS_K3S_PEER_CIDRS": "192.168.50.21/32,192.168.50.22/32,192.168.50.23/32",
        "BOREALIS_DATABASE_URL": "postgresql://borealis@borealis-postgres-rw.borealis.svc:5432/borealis",
        "POSTGRES_PASSWORD": LITERAL,
    }
    return {"metadata": {"name": RECOVERY.SECRET_NAME, "namespace": "borealis",
                         "uid": CLUSTER, "resourceVersion": "42"},
            "data": {k: base64.b64encode(v.encode()).decode() for k, v in values.items()}}


def node():
    labels = {"borealis.io/engine-node": "true", "borealis.io/application-state": "drained"}
    labels.update({"borealis.io/" + role: "false" for role in (
        "control-plane-eligible", "edge-eligible", "postgres-primary-eligible",
        "scheduler-eligible", "hmr-target")})
    return {"metadata": {"name": "engine-02", "uid": NODE_UID, "labels": labels},
            "status": {"nodeInfo": {"kubeletVersion": RECOVERY.K3S_VERSION},
                       "conditions": [{"type": k, "status": "True"} for k in ("Ready", "EtcdIsVoter")],
                       "addresses": [{"type": "InternalIP", "address": "192.168.50.22"}]}}


class LegacyAdmissionIdentityTests(unittest.TestCase):
    def test_observation_requires_whole_cohort_and_no_target_action(self):
        args = SimpleNamespace(operation=OPERATION, node_name="engine-02", node_uid=NODE_UID,
                               repair_sha=SHA, repair_release="2026.09.2-rc.2", endpoint=ENDPOINT)
        repair = RECOVERY.Repair(args)
        peers = [node() for _ in range(3)]
        for index, peer in enumerate(peers, 1):
            peer["metadata"]["name"] = "engine-0" + str(index)
        for change in ("valid", "missing_peer", "wrong_peer", "unready_peer", "active_action", "wrong_secret"):
            with self.subTest(change=change):
                nodes, pods, runtime = copy.deepcopy(peers), [], secret()
                if change == "missing_peer":
                    nodes.pop()
                elif change == "wrong_peer":
                    nodes[0]["metadata"]["name"] = "replacement"
                elif change == "unready_peer":
                    nodes[0]["status"]["conditions"][0]["status"] = "False"
                elif change == "active_action":
                    pods = [{"spec": {"nodeName": "engine-02"}, "status": {"phase": "Running"}}]
                elif change == "wrong_secret":
                    runtime["metadata"]["uid"] = "invalid"
                with mock.patch.object(RECOVERY, "get_json", return_value=snapshot()), \
                        mock.patch.object(repair, "kubectl", side_effect=[{"items": nodes}, {"items": pods}, runtime]):
                    if change == "valid":
                        proof, _ = repair.observe()
                        self.assertEqual(proof["secret_resource_version"], "42")
                    else:
                        with self.assertRaises(RECOVERY.RecoveryError):
                            repair.observe()

    def test_source_checks_real_clean_git_tree_and_exact_origin(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            def git(*args):
                return RECOVERY.run(["git", "-C", str(root), *args]).strip()
            git("init", "--quiet")
            git("config", "user.name", "Fixture")
            git("config", "user.email", "fixture@example.test")
            git("commit", "--quiet", "--allow-empty", "-m", "fixture")
            git("remote", "add", "origin", "https://github.com/" + RECOVERY.REPOSITORY + ".git")
            sha = git("rev-parse", "HEAD")
            RECOVERY.verify_source(root, sha)
            with self.assertRaises(RECOVERY.RecoveryError):
                RECOVERY.verify_source(root, SHA)
            (root / "untracked").write_text("pending")
            with self.assertRaises(RECOVERY.RecoveryError):
                RECOVERY.verify_source(root, sha)
            (root / "untracked").unlink()
            git("remote", "set-url", "origin", "https://example.test/untrusted.git")
            with self.assertRaises(RECOVERY.RecoveryError):
                RECOVERY.verify_source(root, sha)

    def test_timeout_kills_descendant_before_rollback_can_finish(self):
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "late-write"
            with self.assertRaises(subprocess.TimeoutExpired):
                RECOVERY.run(["bash", "-c", '(sleep 0.3; touch "$1") & wait', "fixture", str(marker)],
                             timeout=0.05)
            time.sleep(0.4)
            self.assertFalse(marker.exists())

    def test_valid_snapshot_and_each_state_boundary(self):
        original = snapshot()
        proof = RECOVERY.validate_snapshot(original, OPERATION, "engine-02")
        self.assertEqual(proof["admission_id"], COHORT[0])
        changes = [
            ("active_operation_id", OPERATION), ("enabled", False), ("active_size", 3),
            ("desired_size", 3), ("hmr", {"state": "active"}), ("baseline_sha", SHA),
            ("status", "Healthy"), ("database", {"fully_ready": False}),
            ("operations", []), ("nodes", []), ("admissions", []),
        ]
        for key, value in changes:
            with self.subTest(key=key):
                changed = copy.deepcopy(original)
                changed[key] = value
                with self.assertRaises(RECOVERY.RecoveryError):
                    RECOVERY.validate_snapshot(changed, OPERATION, "engine-02")
        for key, value in (("state", "running"), ("kind", "engine_update"), ("current_step", "complete")):
            with self.subTest(operation_field=key):
                changed = snapshot()
                changed["operations"][0][key] = value
                with self.assertRaises(RECOVERY.RecoveryError):
                    RECOVERY.validate_snapshot(changed, OPERATION, "engine-02")
        changed = snapshot()
        changed["operations"][0]["payload"]["admission_ids"] = [COHORT[0], COHORT[0]]
        with self.assertRaises(RECOVERY.RecoveryError):
            RECOVERY.validate_snapshot(changed, OPERATION, "engine-02")
        with self.assertRaises(RECOVERY.RecoveryError):
            RECOVERY.validate_snapshot(original, OPERATION, "engine-01")

    def test_node_uid_address_health_and_each_role_fence(self):
        proof = RECOVERY.validate_snapshot(snapshot(), OPERATION, "engine-02")
        RECOVERY.validate_node(node(), proof, NODE_UID)
        for key in node()["metadata"]["labels"]:
            with self.subTest(label=key):
                changed = node()
                changed["metadata"]["labels"][key] = "wrong"
                with self.assertRaises(RECOVERY.RecoveryError):
                    RECOVERY.validate_node(changed, proof, NODE_UID)
        for change in ("uid", "ip", "voter", "version"):
            with self.subTest(change=change):
                changed = node()
                if change == "uid":
                    changed["metadata"]["uid"] = CLUSTER
                elif change == "ip":
                    changed["status"]["addresses"] = []
                elif change == "voter":
                    changed["status"]["conditions"][1]["status"] = "False"
                else:
                    changed["status"]["nodeInfo"]["kubeletVersion"] = "v1.36.4+k3s1"
                with self.assertRaises(RECOVERY.RecoveryError):
                    RECOVERY.validate_node(changed, proof, NODE_UID)

    def test_secret_preserves_syntax_and_rejects_wrong_configuration(self):
        proof = RECOVERY.validate_snapshot(snapshot(), OPERATION, "engine-02")
        RECOVERY.validate_secret(secret(), proof, ENDPOINT)
        for host in ("borealis-postgres-rw", "borealis-postgres-rw.borealis", "borealis-postgres-rw.borealis.svc",
                     "borealis-postgres-rw.borealis.svc.cluster.local"):
            runtime = secret()
            runtime["data"]["BOREALIS_DATABASE_URL"] = base64.b64encode(
                ("postgresql://borealis@" + host + ":5432/borealis").encode()).decode()
            RECOVERY.validate_secret(runtime, proof, ENDPOINT)
        for key, value in (
            ("bad-key", "value"), ("POSTGRES_PASSWORD", "line\nsecond"),
            ("BOREALIS_ENGINE_NETWORK_MODE", "unknown"), ("BOREALIS_PUBLIC_HOSTNAME", "wrong.test"),
            ("BOREALIS_K3S_PEER_CIDRS", "192.168.50.0/24"),
            ("BOREALIS_DATABASE_URL", "postgresql://borealis@postgres-db.borealis.svc/borealis"),
        ):
            with self.subTest(key=key):
                changed = secret()
                changed["data"][key] = base64.b64encode(value.encode()).decode()
                with self.assertRaises(RECOVERY.RecoveryError):
                    RECOVERY.validate_secret(changed, proof, ENDPOINT)

    def test_https_origin_and_credential_file_boundary(self):
        self.assertEqual(RECOVERY.https_origin(ENDPOINT + "/"), ENDPOINT)
        for value in ("http://cluster.test", "https://user:pass@cluster.test",
                      ENDPOINT + "/path", ENDPOINT + "?token=value", ENDPOINT + "#fragment"):
            with self.subTest(value=value), self.assertRaises(RECOVERY.RecoveryError):
                RECOVERY.https_origin(value)
        with tempfile.TemporaryDirectory() as root, mock.patch.dict(os.environ, {"SUDO_UID": str(os.getuid())}):
            path = Path(root) / "credential"
            path.write_text("fixture-only-session\n")
            path.chmod(0o600)
            self.assertEqual(RECOVERY.private_file(path), "fixture-only-session")
            path.chmod(0o644)
            with self.assertRaises(RECOVERY.RecoveryError):
                RECOVERY.private_file(path)
            link = Path(root) / "link"
            link.symlink_to(path)
            with self.assertRaises(OSError):
                RECOVERY.private_file(link)

    def test_release_checks_immutable_channel_and_annotated_full_sha(self):
        release = "2026.09.2-rc.2"
        publication = {"tag_name": release, "draft": False, "immutable": True, "prerelease": True}
        manifest = {"cluster_compatible": True, "required_k3s_baseline": RECOVERY.K3S_VERSION,
                    "allowed_release_channels": ["qualification"]}
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            path = root / "Data/Engine/release-manifest.json"
            path.parent.mkdir(parents=True)
            path.write_text(json.dumps(manifest))
            for change in ("valid", "mutable", "draft", "wrong_channel", "moved_tag"):
                with self.subTest(change=change):
                    metadata = dict(publication)
                    if change == "mutable":
                        metadata["immutable"] = False
                    if change == "draft":
                        metadata["draft"] = True
                    if change == "wrong_channel":
                        metadata["prerelease"] = False
                    replies = [metadata, {"object": {"type": "tag", "sha": "c" * 40}},
                               {"object": {"type": "commit", "sha": "d" * 40 if change == "moved_tag" else SHA}}]
                    with mock.patch.object(RECOVERY, "verify_source"), mock.patch.object(RECOVERY, "git") as git_call, \
                            mock.patch.object(RECOVERY, "get_json", side_effect=replies) as http:
                        if change == "valid":
                            RECOVERY.verify_release(root, release, SHA)
                            self.assertTrue(http.call_args.args[0].endswith("/git/tags/" + "c" * 40))
                            git_call.assert_called_once_with(root, "merge-base", "--is-ancestor", RECOVERY.LEGACY_SHA, SHA)
                        else:
                            with self.assertRaises(RECOVERY.RecoveryError):
                                RECOVERY.verify_release(root, release, SHA)

    def test_redirect_does_not_forward_authorization(self):
        self.assertIsNone(RECOVERY.NoRedirect().redirect_request(None, None, None, None, None, None))
        opener = mock.Mock()
        opener.open.side_effect = urllib.error.HTTPError("https://example.test", 302, "redirect", {}, None)
        with mock.patch.object(RECOVERY.urllib.request, "build_opener", return_value=opener):
            with self.assertRaises(RECOVERY.RecoveryError) as result:
                RECOVERY.get_json(ENDPOINT, "fixture-only-session")
        self.assertNotIn("fixture-only-session", str(result.exception))


class LegacyAdmissionTransactionTests(unittest.TestCase):
    def exercise(self, root, case):
        host = root / "host"
        deploy = host / "Engine/Deploy"
        deploy.mkdir(parents=True)
        original = {}
        if case != "fresh":
            for name, mode in (("compose.env", 0o640), ("runtime.env", 0o600)):
                path = deploy / name
                path.write_text("original-" + name + "\n")
                path.chmod(mode)
                os.utime(path, ns=(1_600_000_000_000_000_000, 1_600_000_000_000_000_000))
                info = path.stat()
                original[name] = (path.read_bytes(), info.st_mode, info.st_uid, info.st_gid, info.st_mtime_ns)
        args = SimpleNamespace(operation=OPERATION, node_name="engine-02", node_uid=NODE_UID,
                               repair_sha=SHA, repair_release="2026.09.2-rc.2", endpoint=ENDPOINT)
        repair = RECOVERY.Repair(args, host, root / "journal")
        proof = RECOVERY.validate_snapshot(snapshot(), OPERATION, "engine-02")
        proof.update(node_uid=NODE_UID, repair_sha=SHA, repair_release=args.repair_release)
        services = []
        failed_start = False

        def service(action):
            nonlocal failed_start
            services.append(action)
            if action == "start" and case == "start_failure" and not failed_start:
                failed_start = True
                raise RECOVERY.RecoveryError("fixture service failure")

        real_run = RECOVERY.run

        def runner(command, **kwargs):
            if command[0] == "bash" and case == "write_failure":
                (deploy / "compose.env").write_text("partial\n")
                raise RECOVERY.RecoveryError("fixture preparation failure")
            if command[0] == "bash":
                # Exercise real transaction as test user without invoking sudo.
                command = list(command)
                command[2] = command[2].replace(
                    "\nprepare_cluster_node_runtime",
                    '\nrun_privileged() { "$@"; }\nprepare_cluster_node_runtime')
            return real_run(command, **kwargs)

        observations = [(proof, secret()), (dict(proof, attempt=5) if case == "changed_operation" else proof, secret())]
        with mock.patch.object(repair, "service", side_effect=service), \
                mock.patch.object(repair, "observe", side_effect=observations), \
                mock.patch.object(RECOVERY, "run", side_effect=runner):
            if case in ("write_failure", "changed_operation", "start_failure"):
                with self.assertRaises(RECOVERY.RecoveryError):
                    repair.apply(proof, secret())
            else:
                result = repair.apply(proof, secret())
                self.assertEqual(result["state"], "committed")
                self.assertEqual(repair.apply(proof, secret())["state"], "already_committed")
        directory = root / "journal" / (OPERATION + "-" + NODE_UID)
        journal = json.loads((directory / "journal.json").read_text())
        self.assertFalse((directory / "runtime-secret.json").exists())
        self.assertEqual(services[-1], "start")
        self.assertEqual(list(host.iterdir()), [host / "Engine"])
        self.assertEqual(list((host / "Engine").iterdir()), [deploy])
        self.assertFalse((ROOT / "SHOULD_NOT_EXIST").exists())
        if case in ("write_failure", "changed_operation", "start_failure"):
            self.assertEqual(journal["state"], "rolled_back")
            for name, expected in original.items():
                path = deploy / name
                info = path.stat()
                self.assertEqual((path.read_bytes(), info.st_mode, info.st_uid, info.st_gid, info.st_mtime_ns), expected)
        else:
            self.assertEqual(journal["state"], "committed")
            for name in ("compose.env", "runtime.env"):
                path = deploy / name
                self.assertIn("BOREALIS_PUBLIC_HOSTNAME=cluster.example.test\n", path.read_text())
                self.assertIn(LITERAL, path.read_text())
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        return repair, proof, deploy, directory, services

    def test_real_configuration_only_seed_and_transaction_failures(self):
        for case in ("fresh", "existing", "write_failure", "changed_operation", "start_failure"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                self.exercise(Path(temporary), case)

    def test_interrupted_journal_rolls_back_before_new_attempt(self):
        with tempfile.TemporaryDirectory() as temporary:
            repair, proof, deploy, directory, _ = self.exercise(Path(temporary), "existing")
            path = directory / "journal.json"
            journal = json.loads(path.read_text())
            journal["state"] = "prepared"
            RECOVERY.atomic_json(path, journal)
            calls = []
            with mock.patch.object(repair, "service", side_effect=calls.append):
                result = repair.apply(proof, secret())
            self.assertEqual(result["state"], "rolled_back")
            self.assertEqual(calls, ["stop", "start"])
            self.assertEqual((deploy / "compose.env").read_text(), "original-compose.env\n")

    def test_symlink_destination_rejected_before_service_pause(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            deploy = root / "Engine/Deploy"
            deploy.mkdir(parents=True)
            foreign = root / "foreign"
            foreign.write_text("keep")
            (deploy / "compose.env").symlink_to(foreign)
            args = SimpleNamespace(operation=OPERATION, node_uid=NODE_UID)
            repair = RECOVERY.Repair(args, root, root / "journal")
            with mock.patch.object(repair, "service") as service, self.assertRaises(RECOVERY.RecoveryError):
                repair.apply({}, secret())
            service.assert_not_called()
            self.assertEqual(foreign.read_text(), "keep")


if __name__ == "__main__":
    unittest.main()
