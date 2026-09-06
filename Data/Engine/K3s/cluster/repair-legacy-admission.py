#!/usr/bin/env python3
"""Immutable, operator-invoked configuration repair for pre-F03 admissions."""

import argparse
import base64
import datetime
import fcntl
import ipaddress
import json
import os
from pathlib import Path
import re
import signal
import socket
import stat
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request


REPOSITORY = "bunny-lab-io/Borealis"
LEGACY_SHA = "d94708b7220e79c223fa74fe17db2e3470b42d69"
K3S_VERSION = "v1.36.3+k3s1"
SOURCE_ROOT = Path(__file__).resolve().parents[4]
SERVICE = "borealis-node-manager.service"
SECRET_NAME = "borealis-api-backend-runtime-env"
MAX_JSON = 4 * 1024 * 1024
CLEAN_ENV = {"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
             "LANG": "C.UTF-8", "GIT_OPTIONAL_LOCKS": "0"}


class RecoveryError(Exception):
    """An operator-safe error; never includes command output or secret values."""


def require(condition, message):
    if not condition:
        raise RecoveryError(message)


def fixed(value, pattern, name, maximum):
    require(isinstance(value, str) and len(value) <= maximum
            and re.fullmatch(pattern, value) is not None, "Invalid " + name)
    return value


def uuid(value):
    return fixed(value, r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}", "UUID", 36)


def https_origin(value):
    require(isinstance(value, str) and len(value) <= 2048, "HTTPS endpoint exceeds size limit")
    try:
        url = urllib.parse.urlsplit(value)
        port = url.port
        require(url.scheme == "https" and url.hostname and not url.username
                and not url.password and not url.query and not url.fragment
                and url.path in ("", "/") and (port is None or 1 <= port <= 65535),
                "Endpoint must be an HTTPS origin without credentials")
        fixed(url.hostname, r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?", "endpoint hostname", 253)
    except ValueError:
        raise RecoveryError("Invalid HTTPS endpoint") from None
    return value.rstrip("/")


def run(args, *, env=None, timeout=30):
    # Own process group so cancellation cannot leave a preparation child writing.
    with subprocess.Popen(args, env=env or CLEAN_ENV, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, start_new_session=True) as child:
        try:
            stdout, _ = child.communicate(timeout=timeout)
        except BaseException:
            os.killpg(child.pid, signal.SIGKILL)
            child.wait()
            raise
    require(child.returncode == 0, "Required command failed: " + Path(args[0]).name)
    require(len(stdout) <= MAX_JSON, "Command response exceeds size limit")
    return stdout.decode("utf-8")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def get_json(url, token=None):
    headers = {"Accept": "application/json", "User-Agent": "Borealis-admission-recovery"}
    if token is not None:
        headers["Authorization"] = "Bearer " + token
    try:
        request = urllib.request.Request(url, headers=headers, method="GET")
        with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
            require(response.status == 200 and
                    "application/json" in response.headers.get("Content-Type", ""),
                    "Expected successful JSON response")
            raw = response.read(MAX_JSON + 1)
        require(len(raw) <= MAX_JSON, "JSON response exceeds size limit")
        result = json.loads(raw)
        require(isinstance(result, dict), "Expected JSON object")
        return result
    except (urllib.error.URLError, ValueError):
        raise RecoveryError("HTTPS JSON request failed; redirects are prohibited") from None


def private_file(path, maximum=16384):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as handle:
        info = os.fstat(handle.fileno())
        require(stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600
                and info.st_uid in {0, int(os.environ.get("SUDO_UID", "0"))},
                "Credential file must be owned by root or invoking operator and mode0600")
        raw = handle.read(maximum + 1)
    require(0 < len(raw) <= maximum, "Credential file is empty or oversized")
    token = raw.decode("utf-8").strip()
    require(token and all(32 < ord(c) < 127 for c in token), "Invalid credential encoding")
    return token


def git(root, *args):
    return run(["git", "-c", "safe.directory=" + str(root), "-C", str(root), *args]).strip()


def verify_source(root, sha):
    require(root.is_dir() and root.resolve() == root, "Source root cannot use symlinks")
    require(git(root, "rev-parse", "--show-toplevel") == str(root), "Source must be repository root")
    require(git(root, "rev-parse", "HEAD") == sha, "Source revision mismatch")
    require(not git(root, "status", "--porcelain"), "Source checkout must be clean")
    origin = git(root, "remote", "get-url", "origin").removesuffix(".git")
    require(origin == "https://github.com/" + REPOSITORY, "Unexpected repository origin")


def verify_release(root, release, sha):
    fixed(sha, r"[0-9a-f]{40}", "repair SHA", 40)
    fixed(release, r"\d{4}\.\d{2}\.\d+(?:\.\d+)?(?:-rc\.[1-9]\d*)?", "repair release", 32)
    verify_source(root, sha)
    api = "https://api.github.com/repos/" + REPOSITORY
    published = get_json(api + "/releases/tags/" + release)
    require(published.get("tag_name") == release and published.get("draft") is False
            and published.get("immutable") is True
            and published.get("prerelease") is ("-rc." in release),
            "Repair requires matching published immutable release channel")
    ref = get_json(api + "/git/ref/tags/" + release).get("object", {})
    for _ in range(5):
        fixed(ref.get("sha"), r"[0-9a-f]{40}", "tag object SHA", 40)
        if ref.get("type") == "commit":
            break
        require(ref.get("type") == "tag", "Unsupported release reference")
        ref = get_json(api + "/git/tags/" + ref["sha"]).get("object", {})
    require(ref.get("type") == "commit" and ref.get("sha") == sha, "Release tag SHA mismatch")
    git(root, "merge-base", "--is-ancestor", LEGACY_SHA, sha)
    manifest = json.loads((root / "Data/Engine/release-manifest.json").read_text())
    channel = "qualification" if "-rc." in release else "stable"
    require(manifest.get("cluster_compatible") is True
            and manifest.get("required_k3s_baseline") == K3S_VERSION
            and channel in manifest.get("allowed_release_channels", []),
            "Repair source has incompatible release manifest")


def validate_snapshot(snapshot, operation_id, node_name):
    require(snapshot.get("enabled") is True and snapshot.get("status") == "Degraded Quorum"
            and snapshot.get("active_size") == snapshot.get("desired_size") == 1
            and not snapshot.get("active_operation_id")
            and snapshot.get("hmr", {}).get("state") == "inactive",
            "Repair requires idle failed one-to-three admission without isolation")
    require(snapshot.get("baseline_sha") == LEGACY_SHA
            and snapshot.get("baseline_release") == "dev-" + LEGACY_SHA[:12],
            "Cluster baseline is not supported legacy revision")
    database = snapshot.get("database", {})
    require(database.get("fully_ready") is True and database.get("durability_quorum") is True
            and database.get("ready_instances") == database.get("configured_instances") == 3,
            "CloudNativePG must be healthy3/3")
    matches = [o for o in snapshot.get("operations", []) if o.get("id") == operation_id]
    require(len(matches) == 1, "Original operation absent or ambiguous")
    operation = matches[0]
    require(operation.get("kind") == "membership_admit" and operation.get("state") == "failed"
            and operation.get("current_step") == "apply_membership",
            "Original admission must be failed at apply_membership")
    payload = operation.get("payload", {})
    fixed(payload.get("action_image"), r"borealis-engine/api-backend:sha-[0-9a-f]{12}",
          "legacy action image", 128)
    require(type(operation.get("attempt")) is int and operation["attempt"] > 0,
            "Original operation attempt is invalid")
    require(payload.get("baseline_sha") == LEGACY_SHA
            and payload.get("baseline_release") == "dev-" + LEGACY_SHA[:12]
            and payload.get("k3s_version") == K3S_VERSION,
            "Original operation baseline mismatch")
    cohort = payload.get("admission_ids", [])
    names = payload.get("node_names", [])
    require(isinstance(cohort, list) and len(cohort) == len(set(cohort)) == 2
            and isinstance(names, list) and len(names) == len(set(names)) == 2
            and node_name in names, "Original admission cohort mismatch")
    for identity in cohort:
        uuid(identity)
    admissions = [a for a in snapshot.get("admissions", []) if a.get("id") in cohort]
    require(len(admissions) == 2 and {a.get("node_name") for a in admissions} == set(names)
            and all(a.get("state") == "Approved" for a in admissions),
            "Original cohort must remain approved")
    active = snapshot.get("nodes", [])
    require(len(active) == 1 and active[0].get("node_name") not in names
            and active[0].get("membership_state") == "Active"
            and active[0].get("application_state") == "active",
            "Repair target cannot be active application member")
    addresses = [a.get("management_ip") for a in admissions] + [active[0].get("management_ip")]
    require(all(ipaddress.IPv4Address(address).is_private for address in addresses)
            and len(set(addresses)) == 3, "Cluster requires three distinct private peers")
    target = next(a for a in admissions if a["node_name"] == node_name)
    return {"cluster_id": uuid(snapshot.get("cluster_id")), "operation_id": operation_id,
            "attempt": operation.get("attempt"), "admission_id": target["id"],
            "action_image": payload["action_image"],
            "node_name": node_name, "management_ip": target["management_ip"],
            "baseline_sha": LEGACY_SHA, "peer_addresses": sorted(addresses),
            "peer_nodes": sorted(names + [active[0]["node_name"]])}


def validate_node(node, proof, node_uid):
    require(node.get("metadata", {}).get("uid") == node_uid
            and node.get("metadata", {}).get("name") == proof["node_name"],
            "Kubernetes target identity mismatch")
    status = node.get("status", {})
    conditions = {c.get("type"): c.get("status") for c in status.get("conditions", [])}
    require(conditions.get("Ready") == conditions.get("EtcdIsVoter") == "True"
            and status.get("nodeInfo", {}).get("kubeletVersion") == K3S_VERSION,
            "Target must be Ready etcd voter on exact K3s baseline")
    require(any(a.get("type") == "InternalIP" and a.get("address") == proof["management_ip"]
                for a in status.get("addresses", [])), "Target management address mismatch")
    labels = node["metadata"].get("labels", {})
    require(labels.get("borealis.io/engine-node") == "true"
            and labels.get("borealis.io/application-state") == "drained"
            and all(labels.get("borealis.io/" + role) == "false" for role in
                    ("control-plane-eligible", "edge-eligible", "postgres-primary-eligible",
                     "scheduler-eligible", "hmr-target")),
            "Target must remain application-drained and role-ineligible")


def validate_secret(secret, proof, endpoint):
    require(secret.get("metadata", {}).get("name") == SECRET_NAME
            and secret.get("metadata", {}).get("namespace") == "borealis",
            "Unexpected runtime Secret identity")
    uuid(secret["metadata"].get("uid"))
    fixed(secret["metadata"].get("resourceVersion"), r"[1-9][0-9]*",
          "Secret resource version", 32)
    values = {}
    for key, encoded in secret.get("data", {}).items():
        fixed(key, r"[A-Z][A-Z0-9_]{0,127}", "runtime key", 128)
        value = base64.b64decode(encoded, validate=True).decode("utf-8")
        require(not any(c in value for c in "\r\n\x00"), "Runtime value is not single-line")
        values[key] = value
    require(values.get("BOREALIS_ENGINE_NETWORK_MODE") in ("public", "local")
            and values.get("BOREALIS_PUBLIC_HOSTNAME") == urllib.parse.urlsplit(endpoint).hostname,
            "Runtime network mode or hostname mismatch")
    peers = values.get("BOREALIS_K3S_PEER_CIDRS", "").split(",")
    require(set(peers) == {address + "/32" for address in proof["peer_addresses"]}
            and len(peers) == 3, "Runtime peer allowlist differs from original cohort")
    database = urllib.parse.urlsplit(values.get("BOREALIS_DATABASE_URL", ""))
    require(database.scheme in ("postgres", "postgresql")
            and database.hostname in ("borealis-postgres-rw", "borealis-postgres-rw.borealis",
                                      "borealis-postgres-rw.borealis.svc",
                                      "borealis-postgres-rw.borealis.svc.cluster.local")
            and database.port in (None, 5432), "Runtime must retain CloudNativePG endpoint")


def real_path(path, *, directory=False):
    require(path.resolve() == path, "Recovery path cannot contain symlinks")
    if path.exists():
        mode = path.stat().st_mode
        require(stat.S_ISDIR(mode) if directory else stat.S_ISREG(mode),
                "Unexpected recovery path type")


def atomic_json(path, data):
    raw = json.dumps(data, sort_keys=True, indent=2).encode()
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix=".journal-")
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(raw)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def sync_file(path):
    with path.open("rb") as handle:
        os.fsync(handle.fileno())


def restore_files(directory, deploy, previous):
    require(set(previous) == {"compose.env", "runtime.env"}
            and all(type(value) is bool for value in previous.values()), "Invalid rollback inventory")
    for name, exists in previous.items():
        target = deploy / name
        real_path(target)
        if exists:
            backup = directory / (name + ".previous")
            real_path(backup)
            fd, temporary = tempfile.mkstemp(dir=deploy, prefix=".admission-restore-")
            os.close(fd)
            try:
                run(["cp", "--preserve=all", "--", str(backup), temporary])
                sync_file(Path(temporary))
                os.replace(temporary, target)
            finally:
                Path(temporary).unlink(missing_ok=True)
        elif target.exists():
            target.unlink()
        sync_directory(deploy)


def same_recovery_identity(retained, current):
    # Observation versions may advance after an interrupted controller retry.
    keys = ("cluster_id", "operation_id", "admission_id", "node_name", "management_ip",
            "baseline_sha", "peer_addresses", "peer_nodes", "action_image",
            "node_uid", "repair_sha", "repair_release")
    return all(key in retained and retained[key] == current.get(key) for key in keys)


class Repair:
    def __init__(self, args, host_root=Path("/opt/Borealis"),
                 journal_root=Path("/var/lib/borealis/admission-recovery")):
        self.args = args
        self.host_root = host_root
        self.journal_root = journal_root
        self.token = None

    def kubectl(self, *args):
        return json.loads(run(["k3s", "kubectl", "--request-timeout=10s",
                               *args, "-o", "json"], timeout=15))

    def observe(self):
        snapshot = get_json(self.args.endpoint + "/api/server/cluster", self.token)
        proof = validate_snapshot(snapshot, self.args.operation, self.args.node_name)
        nodes = self.kubectl("get", "nodes").get("items", [])
        require(sorted(n.get("metadata", {}).get("name", "") for n in nodes) == proof["peer_nodes"],
                "Kubernetes membership differs from original cohort")
        for peer in nodes:
            status = peer.get("status", {})
            conditions = {c.get("type"): c.get("status") for c in status.get("conditions", [])}
            require(conditions.get("Ready") == conditions.get("EtcdIsVoter") == "True"
                    and status.get("nodeInfo", {}).get("kubeletVersion") == K3S_VERSION,
                    "Every K3s peer must be Ready voter on exact baseline")
        node = next(n for n in nodes if n["metadata"]["name"] == self.args.node_name)
        validate_node(node, proof, self.args.node_uid)
        pods = self.kubectl("-n", "borealis", "get", "pods", "-l",
                            "app.kubernetes.io/name=borealis-node-action")
        require(not any(p.get("spec", {}).get("nodeName") == self.args.node_name
                        and p.get("status", {}).get("phase") in ("Pending", "Running")
                        for p in pods.get("items", [])), "Target has active node-action Pod")
        secret = self.kubectl("-n", "borealis", "get", "secret", SECRET_NAME)
        validate_secret(secret, proof, self.args.endpoint)
        proof.update({"node_uid": self.args.node_uid, "repair_sha": self.args.repair_sha,
                      "repair_release": self.args.repair_release,
                      "secret_uid": secret["metadata"].get("uid"),
                      "secret_resource_version": secret["metadata"].get("resourceVersion")})
        return proof, secret

    def service(self, action):
        run(["systemctl", action, SERVICE], timeout=45)
        expected = "active" if action == "start" else "inactive"
        state = run(["systemctl", "show", SERVICE, "--property=ActiveState", "--value"]).strip()
        require(state == expected, "Target node-manager did not reach " + expected)

    def apply(self, proof, secret):
        deploy = self.host_root / "Engine/Deploy"
        real_path(deploy, directory=True)
        require(deploy.is_dir(), "Existing legacy deployment directory required")
        real_path(self.journal_root, directory=True)
        self.journal_root.mkdir(mode=0o700, parents=True, exist_ok=True)
        require(self.journal_root.stat().st_uid == os.geteuid()
                and stat.S_IMODE(self.journal_root.stat().st_mode) == 0o700,
                "Journal root must be root-owned mode0700")
        sync_directory(self.journal_root.parent)
        directory = self.journal_root / (self.args.operation + "-" + self.args.node_uid)
        real_path(directory, directory=True)
        directory.mkdir(mode=0o700, exist_ok=True)
        require(directory.stat().st_uid == os.geteuid()
                and stat.S_IMODE(directory.stat().st_mode) == 0o700,
                "Journal directory must be root-owned mode0700")
        sync_directory(self.journal_root)
        journal_path = directory / "journal.json"
        real_path(journal_path)
        if journal_path.exists():
            journal = json.loads(journal_path.read_text())
            require(same_recovery_identity(journal["proof"], proof),
                    "Retained recovery identity differs; inspect journal")
            if journal["state"] == "committed":
                return {"state": "already_committed", "journal": str(journal_path)}
            if journal["state"] != "rolled_back":
                self.service("stop")
                restore_files(directory, deploy, journal["previous"])
                self.service("start")
                (directory / "runtime-secret.json").unlink(missing_ok=True)
                journal["state"] = "rolled_back"
                atomic_json(journal_path, journal)
                return {"state": "rolled_back", "journal": str(journal_path),
                        "next": "Previous interrupted repair restored. Rerun check before applying."}
        previous = {}
        for name in ("compose.env", "runtime.env"):
            target = deploy / name
            real_path(target)
            previous[name] = target.exists()
            if target.exists():
                backup = directory / (name + ".previous")
                real_path(backup)
                run(["cp", "--preserve=all", "--", str(target), str(backup)])
                sync_file(backup)
        journal = {"state": "prepared", "proof": proof, "previous": previous,
                   "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat()}
        atomic_json(journal_path, journal)
        secret_path = directory / "runtime-secret.json"
        success = False
        try:
            self.service("stop")
            require(self.observe()[0] == proof, "Cluster changed before repair")
            atomic_json(secret_path, secret)
            env = dict(CLEAN_ENV, BOREALIS_ENGINE_LIBRARY_MODE="1",
                       BOREALIS_ENGINE_HOST_ROOT=str(self.host_root),
                       BOREALIS_ENGINE_RUNTIME_ROOT=str(self.host_root / "Engine"),
                       TMPDIR=str(directory))
            run(["bash", "-c",
                 'source "$1"\nprepare_cluster_node_runtime configuration-only "$2"',
                 "admission-recovery", str(SOURCE_ROOT / "Engine.sh"), str(secret_path)],
                env=env, timeout=30)
            require(self.observe()[0] == proof, "Cluster changed during repair")
            for name in ("compose.env", "runtime.env"):
                sync_file(deploy / name)
            sync_directory(deploy)
            self.service("start")
            journal["state"] = "committed"
            atomic_json(journal_path, journal)
            success = True
        finally:
            secret_path.unlink(missing_ok=True)
            if not success:
                # Keep backups/journal if either rollback or service recovery fails.
                journal["state"] = "rollback_required"
                atomic_json(journal_path, journal)
                self.service("stop")
                restore_files(directory, deploy, previous)
                self.service("start")
                journal["state"] = "rolled_back"
                atomic_json(journal_path, journal)
        return {"state": "committed", "journal": str(journal_path),
                "next": "Retry original admission through Cluster Management after both targets pass."}

    def execute(self):
        require(os.geteuid() == 0, "Run recovery as root through approved sudo")
        require(self.args.baseline_sha == LEGACY_SHA, "Unsupported legacy baseline")
        require(socket.gethostname().split(".")[0] == self.args.node_name, "Run only on named target")
        verify_release(SOURCE_ROOT, self.args.repair_release, self.args.repair_sha)
        verify_source(self.host_root, LEGACY_SHA)
        require(Path("/etc/borealis/node-manager-revision").read_text().strip() == LEGACY_SHA,
                "Installed node-manager revision mismatch")
        local_secret = self.kubectl("-n", "borealis", "get", "secret", SECRET_NAME)
        hostname = base64.b64decode(local_secret.get("data", {}).get("BOREALIS_PUBLIC_HOSTNAME", ""),
                                   validate=True).decode("utf-8")
        require(hostname == urllib.parse.urlsplit(self.args.endpoint).hostname,
                "Endpoint hostname differs from local cluster configuration")
        self.token = private_file(self.args.admin_token_file)
        identity = get_json(self.args.endpoint + "/api/auth/me", self.token)
        require(identity.get("role") == "Admin", "Admin session required")
        proof, secret = self.observe()
        state = run(["systemctl", "show", SERVICE, "--property=ActiveState", "--value"]).strip()
        if state != "active":
            journal_path = self.journal_root / (self.args.operation + "-" + self.args.node_uid) / "journal.json"
            real_path(journal_path)
            require(state == "inactive" and journal_path.is_file(),
                    "Target node-manager must be active unless resuming recorded repair")
            journal = json.loads(journal_path.read_text())
            require(journal.get("state") in ("prepared", "rollback_required")
                    and same_recovery_identity(journal.get("proof", {}), proof),
                    "Inactive node-manager has no matching interrupted repair")
        if not self.args.apply:
            return {"state": "verified", "proof": proof, "next": "Use --apply --confirmation 'REPAIR ADMISSION'."}
        require(self.args.confirmation == "REPAIR ADMISSION", "Exact REPAIR ADMISSION confirmation required")
        return self.apply(proof, secret)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--endpoint", required=True, type=https_origin)
    parser.add_argument("--admin-token-file", required=True, type=Path)
    parser.add_argument("--operation", required=True, type=uuid)
    parser.add_argument("--node-name", required=True,
                        type=lambda v: fixed(v, r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", "node name", 63))
    parser.add_argument("--node-uid", required=True, type=uuid)
    parser.add_argument("--baseline-sha", required=True)
    parser.add_argument("--repair-release", required=True)
    parser.add_argument("--repair-sha", required=True)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--confirmation")
    os.umask(0o077)
    # Local serialization does not replace cluster observations or manager pause.
    def interrupted(*_):
        raise RecoveryError("Recovery interrupted")
    signal.signal(signal.SIGTERM, interrupted)
    try:
        args = parser.parse_args()
        require(os.geteuid() == 0, "Run recovery as root through approved sudo")
        # Outside manager RuntimeDirectory, which systemd removes during stop.
        lock = Path("/run/borealis-admission-recovery.lock")
        real_path(lock)
        fd = os.open(lock, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "rb") as handle:
            fd = handle.fileno()
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = Repair(args).execute()
        print(json.dumps(result, sort_keys=True))
    except (RecoveryError, OSError, ValueError, KeyError, TypeError,
            subprocess.TimeoutExpired, KeyboardInterrupt) as exc:
        message = str(exc) if isinstance(exc, RecoveryError) else "Recovery failed; inspect private journal and prerequisites"
        print(json.dumps({"state": "failed", "message": message}))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
