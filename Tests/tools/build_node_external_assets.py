#!/usr/bin/env python3
"""Package reviewed external image pins from an isolated exact-tag checkout."""

from __future__ import annotations

import argparse
import hashlib
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

import build_node_bootstrap_assets as bootstrap
import resolve_node_external_images as pins
from node_registry_download import RegistryReader

INVENTORY = "borealis-node-external-images-linux-amd64.json"


def asset_name(reference: str) -> str:
    repository = reference.split("@", 1)[0].split(":", 1)[0]
    return "borealis-node-external-" + repository.rsplit("/", 1)[1] + "-linux-amd64.oci.tar"


def assemble_archive(pin: dict, cache: Path, destination: Path) -> None:
    """Build OCI layout around unchanged upstream index and selected blobs.

    Native source-built verifier is independent of this producer and measures
    all layer bytes. No Docker/K3s daemon or image execution is involved.
    """
    reader = RegistryReader(pin["reference"])
    included = {}

    def fetch(digest, size, *, manifest=False):
        path = cache / digest.split(":", 1)[1]
        if not path.exists():
            reader.fetch(digest, size, path, manifest=manifest)
        # Cache is private to this packaging run. Verify all reused bytes too.
        if not path.is_file() or path.is_symlink() or path.stat().st_size != size:
            raise ValueError("external image cache identity changed")
        with path.open("rb") as file:
            if "sha256:" + hashlib.file_digest(file, "sha256").hexdigest() != digest:
                raise ValueError("external image cache content changed")
        included["blobs/sha256/" + digest.split(":", 1)[1]] = path
        return path

    try:
        index_path = fetch(pin["index_digest"], pin["index_bytes"], manifest=True)
        index = pins.document(index_path.read_bytes(), pins.INDEX_TYPES)
        selected = [item for item in index.get("manifests", []) if item.get("platform") == {"os": "linux", "architecture": "amd64"}]
        if len(selected) != 1 or selected[0]["digest"] != pin["manifest_digest"] or selected[0]["size"] != pin["manifest_bytes"]:
            raise ValueError("pinned external platform changed")
        manifest_path = fetch(pin["manifest_digest"], pin["manifest_bytes"], manifest=True)
        manifest = pins.document(manifest_path.read_bytes(), pins.MANIFEST_TYPES)
        config, layers = manifest["config"], manifest["layers"]
        if (not pins.descriptor(config, pins.CONFIG_TYPES, pins.MAX_DOCUMENT) or
                config["digest"] != pin["config_digest"] or config["size"] != pin["config_bytes"] or
                len(layers) != pin["layer_count"] or any(not pins.descriptor(layer, pins.LAYER_TYPES, (2 << 30) - 1) for layer in layers) or
                sum(layer["size"] for layer in layers) != pin["layer_blob_bytes"]):
            raise ValueError("pinned external blob inventory changed")
        fetch(config["digest"], config["size"])
        for layer in layers:
            fetch(layer["digest"], layer["size"])
        descriptor = dict(mediaType=index["mediaType"], digest=pin["index_digest"], size=pin["index_bytes"],
                          annotations={"org.opencontainers.image.ref.name": pin["reference"], "io.containerd.image.name": pin["reference"]})
        metadata = {"oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
                    "index.json": json.dumps(dict(schemaVersion=2, mediaType="application/vnd.oci.image.index.v1+json", manifests=[descriptor]),
                                             sort_keys=True, separators=(",", ":")).encode()}
        archive_bytes = sum(((path.stat().st_size + 511) // 512 + 1) * 512 for path in included.values()) + 10240
        if archive_bytes >= 2 << 30:
            raise ValueError("external archive exceeds release bound")
        with destination.open("xb") as output, tarfile.open(fileobj=output, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name in sorted(set(included) | set(metadata)):
                entry = tarfile.TarInfo(name)
                entry.mode = 0o644
                entry.size = included[name].stat().st_size if name in included else len(metadata[name])
                with (included[name].open("rb") if name in included else io.BytesIO(metadata[name])) as file:
                    archive.addfile(entry, file)
        if destination.stat().st_size >= 2 << 30:
            raise ValueError("external archive exceeds release bound")
    finally:
        reader.close()


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, go_bin: str) -> dict:
    if not bootstrap.RELEASE.fullmatch(release) or not bootstrap.SHA.fullmatch(source_sha) or not bootstrap.REPOSITORY.fullmatch(repository):
        raise ValueError("invalid immutable external image release")
    output_dir = output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=False)
    try:
        with tempfile.TemporaryDirectory(prefix="borealis-release-external-") as temporary:
            root = Path(temporary)
            snapshot, cache, verifier = root / "source", root / "cache", root / "node-manager"
            cache.mkdir()
            bootstrap.source_snapshot(source, snapshot, release, source_sha, repository)
            lock = json.loads((snapshot / pins.LOCK).read_bytes(), object_pairs_hook=pins.unique_object)
            pins.validate_pin_sources(snapshot, lock)
            images = sorted((i for component in lock["components"] for i in component["images"]), key=lambda i: i["reference"])
            if len({asset_name(i["reference"]) for i in images}) != len(images):
                raise ValueError("ambiguous external archive names")
            bootstrap.build_manager(snapshot, verifier, go_bin)
            proofs = []
            for pin in images:
                destination = output_dir / asset_name(pin["reference"])
                assemble_archive(pin, cache, destination)
                result = subprocess.run([str(verifier), "external-image-proof", "--archive", str(destination), "--reference", pin["reference"]],
                                        check=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=600)
                proofs.append(json.loads(result.stdout, object_pairs_hook=pins.unique_object))
            inventory = dict(version=1, repository=repository, release=release, source_sha=source_sha,
                             platform="linux-amd64", images=proofs)
            (output_dir / INVENTORY).write_text(json.dumps(inventory, sort_keys=True, separators=(",", ":")) + "\n")
            return inventory
    except BaseException:
        shutil.rmtree(output_dir)
        raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--release", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--go-bin", default="go")
    try:
        build_assets(**vars(parser.parse_args()))
        return 0
    except Exception:
        parser.exit(1, "External image packaging failed; private diagnostics withheld.\n")


if __name__ == "__main__":
    raise SystemExit(main())
