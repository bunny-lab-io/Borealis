#!/usr/bin/env python3
"""Build immutable production OCI images from an isolated exact-tag checkout."""

from __future__ import annotations

import argparse
import io
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tarfile
import tempfile
import uuid

import build_node_bootstrap_assets as bootstrap

INVENTORY = "borealis-node-images-linux-amd64.json"
ROLES = ("api-backend", "borealis-operator", "job-scheduler", "postgres-db",
         "remote-desktop-guacd", "site-worker", "traefik-edge", "webui-frontend",
         "wireguard-tunnel")
MAX_ARCHIVE = (2 << 30) - 1


def unique_object(pairs):
    result, seen = {}, set()
    for key, value in pairs:
        if key.lower() in seen:
            raise ValueError("ambiguous image JSON")
        seen.add(key.lower())
        result[key] = value
    return result


def source_path(snapshot: Path, value: str, *, directory=False) -> Path:
    if not isinstance(value, str) or not value or Path(value).is_absolute():
        raise ValueError("invalid image build input")
    resolved = (snapshot / value).resolve(strict=True)
    if not resolved.is_relative_to(snapshot) or (not resolved.is_dir() if directory else not resolved.is_file()):
        raise ValueError("image build input escapes isolated source")
    return resolved


def run(args: list[str], *, env=None, timeout=1800, capture=False):
    result = subprocess.run(args, env=env, timeout=timeout, check=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else None


def normalize_index(source: Path, destination: Path, reference: str) -> None:
    """Fix the import reference, preserving every hashed image/layer byte.

    No layer is extracted. The shared Go verifier checks the complete output
    before an asset can be retained or its inventory written.
    """
    if source.stat().st_size > MAX_ARCHIVE:
        raise ValueError("image archive exceeds release asset bound")
    seen = set()
    with tarfile.open(source, "r:") as src, destination.open("xb") as output:
        with tarfile.open(fileobj=output, mode="w", format=tarfile.USTAR_FORMAT) as dst:
            for entry in src:
                name = entry.name.rstrip("/")
                if name in seen or len(seen) >= 260 or entry.size < 0 or entry.size > MAX_ARCHIVE:
                    raise ValueError("ambiguous OCI export")
                seen.add(name)
                if entry.isdir():
                    if name not in ("blobs", "blobs/sha256"):
                        raise ValueError("foreign OCI export directory")
                    continue
                if not entry.isfile() or not (name in ("index.json", "oci-layout") or
                        name.startswith("blobs/sha256/") and bootstrap.re.fullmatch(
                            r"blobs/sha256/[0-9a-f]{64}", name)):
                    raise ValueError("foreign OCI export member")
                reader = src.extractfile(entry)
                if name == "index.json":
                    if entry.size > 1024 * 1024:
                        raise ValueError("oversized OCI index")
                    index = json.loads(reader.read().decode("utf-8"), object_pairs_hook=unique_object)
                    if index.get("schemaVersion") != 2 or len(index.get("manifests", [])) != 1:
                        raise ValueError("release image must contain one platform without attestations")
                    descriptor = index["manifests"][0]
                    descriptor["annotations"] = {"org.opencontainers.image.ref.name": reference,
                                                 "io.containerd.image.name": reference}
                    data = json.dumps(index, sort_keys=True, separators=(",", ":")).encode()
                    reader = io.BytesIO(data)
                    entry.size = len(data)
                header = tarfile.TarInfo(name)
                header.size, header.mode = entry.size, 0o644
                dst.addfile(header, reader)
    if destination.stat().st_size > MAX_ARCHIVE:
        raise ValueError("normalized OCI image exceeds release asset bound")


def build_runtime(source: Path, go_bin: str) -> Path:
    dist = source / "Data/Engine/Containers/api-backend/dist"
    dist.mkdir(parents=True, exist_ok=True)
    manager = dist / "borealis-node-manager"
    bootstrap.build_manager(source, manager, go_bin)
    env = os.environ.copy()
    env.update(GOWORK="off", GOTOOLCHAIN="local", GOOS="linux", GOARCH="amd64",
               CGO_ENABLED="0", GOFLAGS="-mod=readonly")
    for name in ("api-backend", "wireguard-control", "wireguard-control-client", "wireguard-route-daemon"):
        run([go_bin, "-C", str(dist.parent), "build", "-trimpath", "-buildvcs=false",
             "-ldflags=-buildid=", "-o", str(dist / name), "./cmd/" + name], env=env, timeout=600)
        (dist / name).chmod(0o755)
    shutil.copy(dist / "api-backend", dist / "borealis-cluster-controller")
    return manager


# Development workers receive only this private BuildKit socket. The release
# builder continues to use its ephemeral buildx instance in GitHub Actions.
DEV_BUILDKIT = Path("/run/borealis-buildkit/buildkitd.sock")


def development_builder():
    try:
        mode = DEV_BUILDKIT.lstat().st_mode
    except FileNotFoundError:
        mode = 0
    if not stat.S_ISSOCK(mode) or DEV_BUILDKIT.resolve() != DEV_BUILDKIT:
        raise ValueError("changed development images require the dedicated BuildKit worker socket")
    return ["buildctl", "--addr", "unix://" + str(DEV_BUILDKIT)]


def development_image_command(builder, context, dockerfile, item, reference, exported, labels):
    args = [*builder, "build", "--frontend", "dockerfile.v0",
            "--local", f"context={context}", "--local", f"dockerfile={dockerfile.parent}",
            "--opt", f"filename={dockerfile.name}", "--opt", "platform=linux/amd64",
            "--output", f"type=oci,name={reference},oci-mediatypes=true,compression=gzip,dest={exported}"]
    for label in labels:
        args.extend(["--opt", "label:" + label])
    if "targets" in item:
        args.extend(["--opt", "target=" + item["targets"]["prod"]])
    return args


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, go_bin: str, docker: str = "docker", reuse=None) -> dict:
    if not bootstrap.packaging_identity(release, source_sha) or not bootstrap.REPOSITORY.fullmatch(repository):
        raise ValueError("invalid immutable image release identity")
    if reuse is not None and not release.startswith("dev-src-"):
        raise ValueError("component reuse requires a development snapshot")
    output_dir = output_dir.resolve()
    # Dedicated output directory: an interrupted or previous packaging run is
    # never overwritten. Nothing is uploaded until the complete inventory exists.
    output_dir.mkdir(parents=True, exist_ok=False)
    try:
        with tempfile.TemporaryDirectory(prefix="borealis-release-images-") as temporary:
            root = Path(temporary)
            snapshot = root / "source"
            bootstrap.source_snapshot(source, snapshot, release, source_sha, repository)
            manifest = json.loads((snapshot / "Data/Engine/Containers/build-manifest.json").read_text(), object_pairs_hook=unique_object)
            if set(manifest["services"]) != set(ROLES):
                raise ValueError("release production role inventory changed")
            reused = {}
            input_digests = {}
            layers = {}
            if reuse:
                import development_image_update as update
                base_inventory = reuse.inventory(INVENTORY)
                if [i["role"] for i in base_inventory["images"]] != list(ROLES):
                    raise ValueError("retained role inventory mismatch")
                for i in base_inventory["images"]:
                    role = i["role"]
                    paths = reuse.role_inputs(snapshot, manifest["services"][role])
                    if paths and reuse.unchanged(paths):
                        if i["image"] != f"docker.io/borealis-engine/{role}:release-{reuse.base}":
                            raise ValueError("retained image build source mismatch")
                        reused[role] = i
                        input_digests[role] = reuse.inputs(paths, reuse.current)
                    else:
                        copies = update.binary_layer(reuse, role, snapshot, manifest["services"][role], paths)
                        if copies:
                            layers[role] = (i, copies)
            dev_builder = development_builder() if release.startswith("dev-src-") and len(reused) < len(ROLES) else None
            verifier = build_runtime(snapshot, go_bin) if len(reused) != len(ROLES) else None
            builder = "borealis-release-" + uuid.uuid4().hex
            created = False
            images = []
            try:
                if verifier and not dev_builder:
                    run([docker, "buildx", "create", "--driver", "docker-container", "--name", builder], timeout=120)
                    created = True
                for role in ROLES:
                    if role in reused:
                        proof = reused[role]
                        reuse.copy(f"borealis-node-image-{role}-linux-amd64.oci.tar", output_dir,
                                   proof["archive_sha256"], proof["archive_bytes"])
                        images.append(proof)
                        continue
                    item = manifest["services"][role]
                    dockerfile = source_path(snapshot, item["dockerfile"])
                    context = source_path(snapshot, item["context"], directory=True)
                    reference = f"docker.io/borealis-engine/{role}:release-{source_sha}"
                    exported = root / f"{role}.oci.tar"
                    archive = output_dir / f"borealis-node-image-{role}-linux-amd64.oci.tar"
                    labels = [f"org.opencontainers.image.revision={source_sha}",
                              f"org.opencontainers.image.source=https://github.com/{repository}",
                              f"io.borealis.service={role}"]
                    if role in layers:
                        proof, copies = layers[role]
                        update.build_layer(run, dev_builder, reuse, role, proof, snapshot, copies,
                                           root, reference, exported, labels)
                    elif dev_builder:
                        args = development_image_command(dev_builder, context, dockerfile, item, reference, exported, labels)
                    else:
                        args = [docker, "buildx", "build", "--builder", builder, "--platform", "linux/amd64",
                                "--provenance=false", "--sbom=false", "--tag", reference,
                                "--output", f"type=oci,oci-mediatypes=true,compression=gzip,dest={exported}",
                                "--file", str(dockerfile)]
                        for label in labels:
                            args.extend(["--label", label])
                        if "targets" in item:
                            args += ["--target", item["targets"]["prod"]]
                        args += [str(context)]
                    if role not in layers:
                        run(args)
                    normalize_index(exported, archive, reference)
                    measured = run([str(verifier), "image-archive-proof", "--archive", str(archive),
                                    "--role", role, "--source-sha", source_sha], timeout=600, capture=True)
                    images.append(json.loads(measured))
                    exported.unlink()
            finally:
                if created:
                    # Remove only this packager's private builder/cache. Never
                    # change the selected builder or prune operator Docker state.
                    run([docker, "buildx", "rm", builder], timeout=120)
            inventory = dict(version=1, repository=repository, release=release, source_sha=source_sha,
                             platform="linux-amd64", images=images)
            if reused:
                inventory.update(version=2, retained_release=reuse.directory.name, reused={
                    role: {"source_sha": reuse.base, "inputs_sha256": input_digests[role]} for role in reused})
            (output_dir / INVENTORY).write_text(json.dumps(inventory, sort_keys=True, separators=(",", ":")) + "\n")
            return inventory
    except BaseException:
        shutil.rmtree(output_dir)
        raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("release", "source-sha", "repository"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--go-bin", default="go")
    parser.add_argument("--docker", default="docker")
    build_assets(**vars(parser.parse_args()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
