#!/usr/bin/env python3
"""Activate the one approved recovery controller while the original pair is still stopped."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time


def sha(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def kubectl(*args, **options):
    return subprocess.check_output(["k3s", "kubectl", "--request-timeout=30s", *args], **options)


def patch_image(pod, expected, replacement):
    name = pod["metadata"]["name"]
    if len(pod["spec"]["containers"]) != 1:
        raise ValueError("controller container count changed")
    patch = [{"op": "test", "path": path, "value": value} for path, value in (
        ("/metadata/uid", pod["metadata"]["uid"]), ("/metadata/resourceVersion", pod["metadata"]["resourceVersion"]),
        ("/spec/containers/0/name", "controller"), ("/spec/containers/0/image", expected))]
    patch.append({"op": "replace", "path": "/spec/containers/0/image", "value": replacement})
    kubectl("-n", "borealis", "patch", "pod", name, "--type=json", "-p", json.dumps(patch))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("package", type=Path)
    parser.add_argument("--approved-manifest-sha256", required=True)
    args = parser.parse_args()
    folder = args.package.resolve()
    if sha(folder / "recovery.json") != args.approved_manifest_sha256:
        parser.error("recovery manifest differs from authorization")
    rec = json.loads((folder / "recovery.json").read_bytes())
    for name in ("pair-worker", "pair-node-manager", "controller.oci.tar"):
        if sha(folder / name) != rec["sha256"][name]:
            parser.error(f"{name} differs from authorization")
    nodes = json.loads(kubectl("get", "nodes", "-o", "json"))["items"]
    if len(nodes) != 1:
        parser.error("original single-node topology required")
    node = nodes[0]
    host = os.uname().nodename.split(".")[0]
    if (host != node["metadata"]["name"] or node["metadata"]["uid"] != rec["pin"]["source_boot"]["source_uid"]
            or node["status"]["nodeInfo"]["bootID"] != rec["pin"]["source_boot"]["after"]
            or not any(c["type"] == "Ready" and c["status"] == "True" for c in node["status"]["conditions"])):
        parser.error("run only on the approved Ready original Engine and boot")
    pods = json.loads(kubectl("-n", "borealis", "get", "pods", "-o", "json"))["items"]
    api, = [p for p in pods if p["metadata"]["name"].startswith("api-backend-" + host + "-")
            and p["metadata"].get("deletionTimestamp") is None and p["spec"]["nodeName"] == host]
    if len(api["spec"]["containers"]) != 1 or api["spec"]["containers"][0]["image"] != rec["base_image"]:
        parser.error("original serving image mismatch")
    api_name = api["metadata"]["name"]

    def api_exec(*cmd, **opts):
        return kubectl("-n", "borealis", "exec", "-i", api_name, "-c", "api-backend", "--", *cmd, **opts)

    scratch = api_exec("mktemp", "-d", "/tmp/borealis-pair.XXXXXXXXXX").decode().strip()
    if not re.fullmatch(r"/tmp/borealis-pair\.[a-zA-Z0-9]{10}", scratch):
        parser.error("private executor staging directory unavailable")
    print("Original API Pod", api_name, "private executor directory", scratch, flush=True)
    for name in ("pair-worker", "pair-node-manager"):
        path = scratch + "/" + name
        with (folder / name).open("rb") as f:
            api_exec("sh", "-ec", 'umask 077; cat > "$1"; chmod 0500 "$1"', "stage", path, stdin=f)
        if api_exec("sha256sum", path).decode().split()[0] != rec["sha256"][name]:
            parser.error(f"staged {name} mismatch")
    worker = scratch + "/pair-worker"
    proof = json.loads(api_exec(worker, "pair-recovery-check"))
    ns = json.loads(kubectl("get", "namespace", "kube-system", "-o", "json"))
    if (proof["namespace_uid"] != ns["metadata"]["uid"] or proof["operation"] != rec["pin"]["operation"]
            or proof["node"] != host or proof["plan"] != rec["pin"]["plan"]):
        parser.error("original stopped operation or Kubernetes identity differs")
    name = proof["controller"]

    def controller():
        return json.loads(kubectl("-n", "borealis", "get", "pod", name, "-o", "json"))

    pod = controller()
    uid = pod["metadata"]["uid"]
    container, = pod["spec"]["containers"]
    if (pod["metadata"].get("deletionTimestamp") is not None or pod["spec"]["nodeName"] != host
            or container["name"] != "controller" or container["command"] != ["/usr/local/bin/borealis-cluster-controller"]
            or container["image"] not in (rec["base_image"], rec["controller_image"])):
        parser.error("original controller Pod or executable differs")
    if container["image"] == rec["base_image"]:
        subprocess.run(["k3s", "ctr", "images", "import", "--digests", str(folder / "controller.oci.tar")], check=True)
        # Import may take time. Recheck stopped custody and the original Pod.
        if json.loads(api_exec(worker, "pair-recovery-check")) != proof:
            parser.error("stopped custody changed during import")
        latest = controller()
        if latest["metadata"]["uid"] != uid:
            parser.error("original controller was replaced during import")
        patch_image(latest, rec["base_image"], rec["controller_image"])
    for _ in range(60):
        latest = controller()
        if latest["metadata"]["uid"] != uid or latest["spec"]["containers"][0]["image"] != rec["controller_image"]:
            parser.error("controller custody changed during activation")
        statuses = latest["status"].get("containerStatuses", [])
        if len(statuses) == 1 and statuses[0].get("ready"):
            binary = kubectl("-n", "borealis", "exec", name, "-c", "controller", "--", "sha256sum",
                             "/usr/local/bin/borealis-cluster-controller").decode().split()[0]
            if binary == rec["sha256"]["pair-worker"] and json.loads(api_exec(worker, "pair-recovery-check")) == proof:
                print("Recovery controller ready", uid, "operation remains stopped", proof["operation"])
                return
        time.sleep(2)
    parser.error("controller activation unresolved; inspect this Pod UID and stopped custody before retry")


if __name__ == "__main__":
    main()
