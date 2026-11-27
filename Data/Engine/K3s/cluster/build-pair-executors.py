#!/usr/bin/env python3
"""Build the temporary stopped-pair executors against an existing development catalog."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def output(*args, **opts):
    return subprocess.check_output(args, text=True, **opts).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("commit")
    parser.add_argument("catalog", type=Path)
    parser.add_argument("pin", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    repo = Path(output("git", "rev-parse", "--show-toplevel"))
    commit = output("git", "rev-parse", "--verify", args.commit + "^{commit}", cwd=repo)
    if args.commit != commit or not re.fullmatch("[0-9a-f]{40}", commit):
        parser.error("specify the full reviewed commit")
    pin = json.loads(args.pin.read_bytes())
    catalog = args.catalog.resolve()
    identity_file = catalog / "development-identity.json"
    if pin["release"] != "dev-" + digest(identity_file) or pin["release"] != catalog.name:
        parser.error("frozen development catalog mismatch")
    identity = json.loads(identity_file.read_bytes())
    inventories = {}
    for role, name in (("bootstrap", "borealis-node-bootstrap-linux-amd64.json"),
                       ("image", "borealis-node-images-linux-amd64.json")):
        path = catalog / role / name
        if digest(path) != identity["inventories"][role]:
            parser.error(f"{role} inventory mismatch")
        inventories[role] = json.loads(path.read_bytes())
    if pin["catalog_manager"] != inventories["bootstrap"]["node_manager"]["sha256"]:
        parser.error("catalog helper mismatch")
    image = next(i for i in inventories["image"]["images"] if "/api-backend:" in i["image"])
    archive = catalog / "image/borealis-node-image-api-backend-linux-amd64.oci.tar"
    if digest(archive) != image["archive_sha256"]:
        parser.error("original controller image archive mismatch")
    args.output.mkdir(mode=0o700, parents=True, exist_ok=False)
    dest = args.output.resolve()
    go = os.getenv("BOREALIS_GO_BIN", "go")
    if output(go, "env", "GOVERSION") != "go1.25.12":
        parser.error("the pinned Go 1.25.12 toolchain is required")
    encoded = base64.urlsafe_b64encode(json.dumps(pin, separators=(",", ":")).encode()).decode().rstrip("=")
    prefix = "borealis/api-backend/internal/"
    flags = "-X " + prefix + "clusterbootstrap.pairExecutorPin=" + encoded
    env = dict(os.environ, GOMAXPROCS="4", CGO_ENABLED="0", GOOS="linux", GOARCH="amd64", GOTOOLCHAIN="local")
    with tempfile.TemporaryDirectory() as tmp:
        src = Path(tmp)
        with tempfile.TemporaryFile() as tar:
            subprocess.run(["git", "archive", commit, "Data/Engine/Containers/api-backend"], cwd=repo, stdout=tar, check=True)
            tar.seek(0)
            subprocess.run(["tar", "-xf", "-", "-C", tmp], stdin=tar, check=True)
        module = src / "Data/Engine/Containers/api-backend"
        for package, filename in (("borealis-node-manager", "pair-node-manager"), ("api-backend", "pair-worker")):
            subprocess.run([go, "build", "-p", "2", "-trimpath", "-mod=readonly", "-ldflags", flags,
                            "-o", str(dest / filename), "./cmd/" + package], cwd=module, env=env, check=True)
            if package == "borealis-node-manager":
                flags += " -X " + prefix + "clusterremote.pairExecutorSHA256=" + digest(dest / filename)
        subprocess.run(["podman", "load", "-i", str(archive)], check=True)
        if output("podman", "image", "inspect", "--format", "{{.Digest}}", image["image"]) != image["manifest_digest"]:
            parser.error("loaded original image digest mismatch")
        tag = "localhost/borealis-pair-controller:" + digest(dest / "pair-worker")[:24]
        recipe = src / "Containerfile"
        recipe.write_text("FROM " + image["image"].split(":")[0] + "@" + image["manifest_digest"] +
                          "\nCOPY --chmod=0755 pair-worker /usr/local/bin/borealis-cluster-controller\n")
        subprocess.run(["podman", "build", "--network=none", "--pull=never", "--memory=2g", "--cpu-quota=300000",
                        "-f", str(recipe), "-t", tag, str(dest)], check=True)
        subprocess.run(["podman", "save", "--format=oci-archive", "-o", str(dest / "controller.oci.tar"), tag], check=True)
        image_digest = output("podman", "image", "inspect", "--format", "{{.Digest}}", tag)
    record = {"commit": commit, "pin": pin, "base_image": image["image"].split(":")[0] + "@" + image["manifest_digest"],
              "controller_image": tag.split(":")[0] + "@" + image_digest,
              "sha256": {name: digest(dest / name) for name in ("pair-worker", "pair-node-manager", "controller.oci.tar")}}
    (dest / "recovery.json").write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
