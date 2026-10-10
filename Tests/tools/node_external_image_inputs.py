#!/usr/bin/env python3
"""Select external image inputs from checksum-verified cluster manifests.

This is an input catalog for release packaging, not an installed-size estimate
or a registry resolver. PostgreSQL's runtime digest comes from guarded source
observation; its image must never be inferred from an operator default.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re

import yaml

LOCK = "Data/Engine/K3s/cluster/dependencies.lock"
COMPONENTS = {"longhorn": 13, "cert-manager": 4, "cloudnative-pg": 1,
              "system-upgrade-controller": 2}
IMAGE_FLAGS = {
    "longhorn": {"--engine-image", "--instance-manager-image", "--share-manager-image",
                 "--backing-image-manager-image", "--support-bundle-manager-image", "--manager-image"},
    "cert-manager": {"--acme-http01-solver-image"},
}
IMAGE_ENV = {"CSI_ATTACHER_IMAGE", "CSI_PROVISIONER_IMAGE", "CSI_NODE_DRIVER_REGISTRAR_IMAGE",
             "CSI_RESIZER_IMAGE", "CSI_SNAPSHOTTER_IMAGE", "CSI_LIVENESS_PROBE_IMAGE"}
REFERENCE = re.compile(r"(?:docker\.io/(?:longhornio|rancher)|quay\.io/jetstack|ghcr\.io/cloudnative-pg)/[a-z0-9-]+:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}")
DIGEST_REFERENCE = re.compile(r"(?:ghcr\.io/kube-vip/kube-vip|registry\.k8s\.io/(?:sig-storage/snapshot-controller|e2e-test-images/busybox))@sha256:[0-9a-f]{64}")
MAX_MANIFEST = 4 << 20


def bounded_read(path: Path) -> bytes:
    with path.open("rb") as handle:
        raw = handle.read(MAX_MANIFEST + 1)
    if not raw or len(raw) > MAX_MANIFEST:
        raise ValueError("external manifest size outside supported bounds")
    return raw


def dependency_pins(source: Path) -> dict:
    pins = {}
    for line in bounded_read(source / LOCK).decode("utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        fields = line.split("|")
        if len(fields) != 4:
            raise ValueError("invalid dependency lock row")
        name, version, url, digest = fields
        if name not in COMPONENTS:
            continue
        if name in pins or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError("ambiguous external dependency pin")
        pins[name] = dict(version=version, sha256=digest)
    if set(pins) != set(COMPONENTS):
        raise ValueError("missing external dependency pin")
    return pins


def manifest_images(raw: bytes, component: str) -> list[str]:
    """Inspect executable workload fields, never CRD schemas or arbitrary text."""
    images, indirect_flags, indirect_env = set(), set(), set()

    def add(value):
        if isinstance(value, str) and value.startswith("rancher/"):
            value = "docker.io/" + value
        if not isinstance(value, str) or not (REFERENCE.fullmatch(value) or DIGEST_REFERENCE.fullmatch(value)):
            raise ValueError("unsupported external image reference")
        images.add(value)

    for obj in yaml.safe_load_all(raw):
        if obj is None:  # Upstream bundles contain empty YAML documents.
            continue
        if not isinstance(obj, dict):
            raise ValueError("invalid dependency manifest object")
        kind = obj.get("kind")
        if kind == "ConfigMap" and component == "system-upgrade-controller":
            data = obj.get("data", {})
            if any("IMAGE" in key and key not in {"SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE", "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS", "SYSTEM_UPGRADE_JOB_IMAGE_PULL_POLICY"} for key in data):
                raise ValueError("unreviewed upgrade image configuration")
            if "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE" in data:
                add(data["SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"])
                indirect_env.add("SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE")
                if data.get("SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS") != "":
                    raise ValueError("unexpected Windows upgrade image")
        if kind in ("Deployment", "DaemonSet", "StatefulSet", "Job"):
            spec = obj["spec"]["template"]["spec"]
        elif kind == "Pod":
            spec = obj["spec"]
        elif kind == "CronJob":
            spec = obj["spec"]["jobTemplate"]["spec"]["template"]["spec"]
        else:
            continue
        if spec.get("ephemeralContainers"):
            raise ValueError("ephemeral dependency image unsupported")
        for container in spec.get("containers", []) + spec.get("initContainers", []):
            add(container["image"])
            args = container.get("command", []) + container.get("args", [])
            flags = IMAGE_FLAGS.get(component, set())
            for i, arg in enumerate(args):
                key, separator, value = arg.partition("=")
                if key.startswith("--") and "image" in key and key not in flags:
                    raise ValueError("unreviewed indirect image argument")
                if key in flags:
                    if not separator:
                        if i + 1 == len(args):
                            raise ValueError("missing indirect image argument")
                        value = args[i + 1]
                    add(value)
                    indirect_flags.add(key)
            for env in container.get("env", []):
                name = env.get("name", "")
                if "IMAGE" in name:
                    if component == "longhorn" and name in IMAGE_ENV:
                        add(env.get("value"))
                        indirect_env.add(name)
                    elif component == "cloudnative-pg" and name == "OPERATOR_IMAGE_NAME":
                        if env.get("value") != container["image"] or "valueFrom" in env:
                            raise ValueError("operator bootstrap image differs from container")
                        indirect_env.add(name)
                    else:
                        raise ValueError("unreviewed indirect image environment")
    if indirect_flags != IMAGE_FLAGS.get(component, set()):
        raise ValueError("missing indirect dependency image argument")
    wanted_env = {"longhorn": IMAGE_ENV, "system-upgrade-controller": {"SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE"},
                  "cloudnative-pg": {"OPERATOR_IMAGE_NAME"}}.get(component, set())
    if indirect_env != wanted_env:
        raise ValueError("missing indirect dependency image environment")
    count = COMPONENTS.get(component, 1)
    if len(images) != count:
        raise ValueError("external image set changed; review complete dependency inventory")
    return sorted(images)


def collect(source: Path, manifests: Path) -> dict:
    """Caller must supply isolated exact-source checkout for release use.

    Downloading, registry resolution and archive measurement are separate steps;
    this function neither invokes a shell nor executes/imports any image.
    """
    inputs = []
    for component, pin in sorted(dependency_pins(source).items()):
        raw = bounded_read(manifests / (component + ".yaml"))
        if hashlib.sha256(raw).hexdigest() != pin["sha256"]:
            raise ValueError("external manifest differs from source pin")
        inputs.append(dict(component=component, manifest_sha256=pin["sha256"],
                           version=pin["version"], images=manifest_images(raw, component)))
    for component, relative in (("kube-vip", "kube-vip.yaml.in"),
                                ("snapshot-controller", "snapshot-controller.yaml")):
        raw = bounded_read(source / "Data/Engine/K3s/cluster" / relative)
        images = manifest_images(raw, component)
        if not DIGEST_REFERENCE.fullmatch(images[0]):
            raise ValueError("local dependency must pin image digest")
        inputs.append(dict(component=component, manifest_sha256=hashlib.sha256(raw).hexdigest(), images=images))
    # Fixed literal inside conformance script's YAML heredoc. Do not execute or
    # shell-expand script text to discover its image.
    raw = bounded_read(source / "Data/Engine/K3s/cluster/run-probe-conformance.sh")
    images = re.findall(r"^\s+image: (\S+)\s*$", raw.decode("utf-8"), re.MULTILINE)
    if len(images) != 1 or not DIGEST_REFERENCE.fullmatch(images[0]) or not images[0].startswith("registry.k8s.io/e2e-test-images/busybox@"):
        raise ValueError("probe helper image must have one literal digest pin")
    inputs.append(dict(component="probe-conformance", manifest_sha256=hashlib.sha256(raw).hexdigest(), images=images))
    return dict(version=1, kind="external-image-inputs", platform="linux-amd64", components=sorted(inputs, key=lambda v: v["component"]),
                dynamic_images=["source-postgresql-runtime"],
                excluded_operations=["k3s-version-upgrade"])


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--manifest-dir", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        value = collect(args.source.resolve(), args.manifest_dir.resolve())
        with args.output.open("x") as output:
            json.dump(value, output, sort_keys=True, indent=2)
            output.write("\n")
        return 0
    except Exception:
        parser.exit(1, "External image input collection failed; diagnostics withheld.\n")


if __name__ == "__main__":
    raise SystemExit(main())
