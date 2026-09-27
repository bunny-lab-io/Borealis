#!/usr/bin/env python3
"""Create reviewable registry pins for external Linux/AMD64 image inputs.

Run explicitly when preparing or changing release inputs. Packaging must use
reviewed pins, not resolve mutable tags again. No image layer is downloaded,
imported or executed here; expanded footprint still needs archive verification.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import signal
import subprocess
import tempfile
import time

import node_external_image_inputs as inputs

DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
INDEX_TYPES = {"application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"}
MANIFEST_TYPES = {"application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"}
CONFIG_TYPES = {"application/vnd.oci.image.config.v1+json", "application/vnd.docker.container.image.v1+json"}
LAYER_TYPES = {"application/vnd.oci.image.layer.v1.tar+gzip", "application/vnd.docker.image.rootfs.diff.tar.gzip",
               "application/vnd.oci.image.layer.v1.tar"}
MAX_DOCUMENT = 1 << 20
LOCK = "Data/Engine/K3s/cluster/external-images.lock.json"


def unique_object(pairs):
    value, seen = {}, set()
    for key, item in pairs:
        if key.lower() in seen:
            raise ValueError("ambiguous registry metadata")
        seen.add(key.lower())
        value[key] = item
    return value


def document(raw: bytes, types: set) -> dict:
    if not isinstance(raw, bytes) or not 0 < len(raw) <= MAX_DOCUMENT:
        raise ValueError("invalid registry document size")
    value = json.loads(raw, object_pairs_hook=unique_object)
    if not isinstance(value, dict) or type(value.get("schemaVersion")) is not int or value["schemaVersion"] != 2 or value.get("mediaType") not in types:
        raise ValueError("unsupported registry document")
    return value


def descriptor(value, types: set, limit: int) -> bool:
    return (isinstance(value, dict) and value.get("mediaType") in types and
            isinstance(value.get("digest"), str) and DIGEST.fullmatch(value["digest"]) is not None and
            value["digest"] != "sha256:" + "0" * 64 and type(value.get("size")) is int and
            0 < value["size"] <= limit and not value.get("urls") and not value.get("data"))


def resolve_reference(reference: str, read) -> dict:
    if not (inputs.REFERENCE.fullmatch(reference) or inputs.DIGEST_REFERENCE.fullmatch(reference)):
        raise ValueError("unsupported external image reference")
    repository = reference.split("@", 1)[0].split(":", 1)[0]
    raw = read(reference)
    index = document(raw, INDEX_TYPES)
    index_digest = "sha256:" + hashlib.sha256(raw).hexdigest()
    if "@" in reference and reference.split("@", 1)[1] != index_digest:
        raise ValueError("source-pinned image index changed")
    descriptors = index.get("manifests")
    if not isinstance(descriptors, list) or not 1 <= len(descriptors) <= 64:
        raise ValueError("invalid registry platform inventory")
    matches = []
    for item in descriptors:
        if not isinstance(item, dict) or not isinstance(item.get("platform"), dict):
            raise ValueError("missing registry platform identity")
        platform = item["platform"]
        if platform.get("os") == "linux" and platform.get("architecture") == "amd64":
            if set(platform) != {"os", "architecture"} or not descriptor(item, MANIFEST_TYPES, MAX_DOCUMENT):
                raise ValueError("unsupported Linux/AMD64 image descriptor")
            matches.append(item)
    if len(matches) != 1:
        raise ValueError("ambiguous or missing Linux/AMD64 image")
    selected = matches[0]
    raw_manifest = read(repository + "@" + selected["digest"])
    if len(raw_manifest) != selected["size"] or "sha256:" + hashlib.sha256(raw_manifest).hexdigest() != selected["digest"]:
        raise ValueError("registry manifest differs from index descriptor")
    manifest = document(raw_manifest, MANIFEST_TYPES)
    config, layers = manifest.get("config"), manifest.get("layers")
    if manifest["mediaType"] != selected["mediaType"] or not descriptor(config, CONFIG_TYPES, MAX_DOCUMENT) or not isinstance(layers, list) or not 1 <= len(layers) <= 128:
        raise ValueError("unsupported registry image manifest")
    if any(not descriptor(layer, LAYER_TYPES, (2 << 30) - 1) for layer in layers):
        raise ValueError("unsupported registry image layer")
    if sum(layer["size"] for layer in layers) > (2 << 30) - 1:
        raise ValueError("external image exceeds archive budget")
    return dict(reference=reference, index_digest=index_digest, index_bytes=len(raw),
                manifest_digest=selected["digest"], manifest_bytes=len(raw_manifest),
                config_digest=config["digest"], config_bytes=config["size"],
                layer_count=len(layers), layer_blob_bytes=sum(layer["size"] for layer in layers))


def validate_pin_sources(source: Path, value: dict) -> None:
    """Offline gate: reviewed pins must follow the current manifest sources.

    Does not re-resolve tags. Exact manifest hashes bind the inputs originally
    inspected; a dependency change requires explicit review/resolution again.
    """
    if (set(value) != {"version", "kind", "platform", "components", "dynamic_images", "excluded_operations"} or
            type(value["version"]) is not int or value["version"] != 1 or value["kind"] != "external-image-pins" or
            value["platform"] != "linux-amd64" or value["dynamic_images"] != ["source-postgresql-runtime"] or
            value["excluded_operations"] != ["k3s-version-upgrade"]):
        raise ValueError("invalid external image pin contract")
    dependencies = inputs.dependency_pins(source)
    local = {"kube-vip": "kube-vip.yaml.in", "snapshot-controller": "snapshot-controller.yaml",
             "probe-conformance": "run-probe-conformance.sh"}
    if not isinstance(value["components"], list) or [v["component"] for v in value["components"]] != sorted(set(dependencies) | set(local)):
        raise ValueError("external component set differs from source")
    seen = set()
    for component in value["components"]:
        name = component["component"]
        keys = {"component", "manifest_sha256", "images"} | ({"version"} if name in dependencies else set())
        if set(component) != keys:
            raise ValueError("invalid external component fields")
        if name in dependencies:
            expected_hash = dependencies[name]["sha256"]
            if component["version"] != dependencies[name]["version"]:
                raise ValueError("external component version changed")
        else:
            expected_hash = hashlib.sha256(inputs.bounded_read(source / "Data/Engine/K3s/cluster" / local[name])).hexdigest()
        if component["manifest_sha256"] != expected_hash or len(component["images"]) != inputs.COMPONENTS.get(name, 1):
            raise ValueError("external component manifest changed")
        previous = ""
        for image in component["images"]:
            if set(image) != {"reference", "index_digest", "index_bytes", "manifest_digest", "manifest_bytes",
                              "config_digest", "config_bytes", "layer_count", "layer_blob_bytes"}:
                raise ValueError("invalid external image pin fields")
            reference = image["reference"]
            if not isinstance(reference, str) or not (inputs.REFERENCE.fullmatch(reference) or inputs.DIGEST_REFERENCE.fullmatch(reference)) or reference <= previous or reference in seen:
                raise ValueError("invalid external image pin identity")
            previous = reference
            seen.add(reference)
            for key in ("index_digest", "manifest_digest", "config_digest"):
                if not isinstance(image[key], str) or not DIGEST.fullmatch(image[key]) or image[key] == "sha256:" + "0" * 64:
                    raise ValueError("invalid external image digest pin")
            if "@" in reference and reference.split("@", 1)[1] != image["index_digest"]:
                raise ValueError("external source digest changed")
            for key, bound in (("index_bytes", MAX_DOCUMENT), ("manifest_bytes", MAX_DOCUMENT), ("config_bytes", MAX_DOCUMENT),
                               ("layer_count", 128), ("layer_blob_bytes", (2 << 30) - 1)):
                if type(image[key]) is not int or not 0 < image[key] <= bound:
                    raise ValueError("invalid external metadata bound")


def public_registry_read(reference: str, environment: dict) -> bytes:
    # stdout is bounded while running, stderr is never retained. Empty private
    # DOCKER_CONFIG prevents use of the operator's registry credentials/helpers.
    process = subprocess.Popen(["docker", "buildx", "imagetools", "inspect", "--raw", reference],
                               env=environment, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    output = bytearray()
    deadline = time.monotonic() + 90
    try:
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not selector.select(remaining):
                    raise ValueError("public registry metadata timed out")
                chunk = os.read(process.stdout.fileno(), min(65536, MAX_DOCUMENT + 1 - len(output)))
                if not chunk:
                    break
                output.extend(chunk)
                if len(output) > MAX_DOCUMENT:
                    raise ValueError("public registry metadata too large")
        if process.wait(timeout=max(0.001, deadline - time.monotonic())) != 0:
            raise ValueError("public registry metadata unavailable")
        return bytes(output)
    finally:
        # Docker launches the Buildx plugin as a child. Bound the whole private
        # process group on timeout/interruption, including a lingering plugin.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
        process.stdout.close()


def resolve(source: Path, manifests: Path) -> dict:
    catalog = inputs.collect(source, manifests)
    catalog["kind"] = "external-image-pins"
    with tempfile.TemporaryDirectory(prefix="borealis-public-registry-") as temporary:
        environment = {key: value for key, value in os.environ.items() if not key.startswith(("DOCKER_", "BUILDX_"))}
        environment["DOCKER_CONFIG"] = temporary
        for component in catalog["components"]:
            component["images"] = [resolve_reference(ref, lambda ref: public_registry_read(ref, environment))
                                   for ref in component["images"]]
    validate_pin_sources(source, catalog)
    return catalog


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--manifest-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.output.exists():
            raise ValueError("output already exists")
        value = resolve(args.source.resolve(), args.manifest_dir.resolve())
        with args.output.open("x") as output:
            json.dump(value, output, sort_keys=True, indent=2)
            output.write("\n")
        return 0
    except Exception:
        parser.exit(1, "External image pin resolution failed; diagnostics withheld.\n")


if __name__ == "__main__":
    raise SystemExit(main())
