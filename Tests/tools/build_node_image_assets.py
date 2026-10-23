#!/usr/bin/env python3
"""Build immutable production OCI images from an isolated exact-tag checkout."""

from __future__ import annotations

import argparse
import io
import json
import os
from pathlib import Path
import shutil
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
    shutil.copyfile(dist / "api-backend", dist / "borealis-cluster-controller")
    return manager


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, go_bin: str, docker: str = "docker") -> dict:
    if not bootstrap.RELEASE.fullmatch(release) or not bootstrap.SHA.fullmatch(source_sha) or not bootstrap.REPOSITORY.fullmatch(repository):
        raise ValueError("invalid immutable image release identity")
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
            verifier = build_runtime(snapshot, go_bin)
            builder = "borealis-release-" + uuid.uuid4().hex
            created = False
            images = []
            try:
                run([docker, "buildx", "create", "--driver", "docker-container", "--name", builder], timeout=120)
                created = True
                for role in ROLES:
                    item = manifest["services"][role]
                    dockerfile = source_path(snapshot, item["dockerfile"])
                    context = source_path(snapshot, item["context"], directory=True)
                    reference = f"docker.io/borealis-engine/{role}:release-{source_sha}"
                    exported = root / f"{role}.oci.tar"
                    archive = output_dir / f"borealis-node-image-{role}-linux-amd64.oci.tar"
                    args = [docker, "buildx", "build", "--builder", builder, "--platform", "linux/amd64",
                            "--provenance=false", "--sbom=false", "--tag", reference,
                            "--label", f"org.opencontainers.image.revision={source_sha}",
                            "--label", f"org.opencontainers.image.source=https://github.com/{repository}",
                            "--label", f"io.borealis.service={role}",
                            "--output", f"type=oci,oci-mediatypes=true,compression=gzip,dest={exported}",
                            "--file", str(dockerfile)]
                    if "targets" in item:
                        args += ["--target", item["targets"]["prod"]]
                    args += [str(context)]
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
