#!/usr/bin/env python3
"""Package an explicitly selected local Git revision in a private Linux build worker."""

import argparse
import hashlib
import json
from pathlib import Path
import tempfile

import build_node_bootstrap_assets as bootstrap
import build_node_external_assets as external
import build_node_host_package_assets as packages
import build_node_image_assets as images
import build_node_k3s_assets as k3s
import development_upgrade as upgrade
from engine_component_reuse import RetainedRelease

PACKAGERS = dict(bootstrap=bootstrap, external=external, host_package=packages, image=images, k3s=k3s)
INDEX = "development-manifest.json"
BINDING = "development-identity.json"


def canonical(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def binding(record):
    inventories = {}
    for family, pkg in PACKAGERS.items():
        name = bootstrap.MANIFEST_NAME if family == "bootstrap" else pkg.INVENTORY
        inventories[family] = record["artifacts"][family + "/" + name]["sha256"]
    return canonical(dict(schema=2, channel="development", repository=record["repository"],
                          source_sha=record["source_sha"], source_tree=record["source_tree"],
                          platform=record["platform"], inventories=inventories,
                          manifest_sha256=hashlib.sha256(canonical(record)).hexdigest()))


def freeze_revision(source, revision, destination):
    sha = bootstrap.git(source, "rev-parse", "--verify", "--end-of-options", revision + "^{commit}")
    if not bootstrap.SHA.fullmatch(sha):
        raise ValueError("invalid local source revision")
    label = "dev-src-" + sha
    destination.mkdir(mode=0o700)
    bootstrap.git(destination, "init", "--quiet", "--template=")
    bootstrap.git(destination, "-c", "protocol.allow=never", "-c", "protocol.file.allow=always",
                  "fetch", "--quiet", "--no-tags", "--depth=1", "--no-recurse-submodules",
                  source.resolve(strict=True).as_uri(), sha + ":refs/tags/" + label)
    # Packagers re-open this private tag. Never create a ref in the input repo.
    bootstrap.git(destination, "checkout", "--quiet", "--detach", sha)
    tree = bootstrap.git(destination, "rev-parse", "HEAD^{tree}")
    return sha, tree, label


def artifact_names(family, source):
    if family == "bootstrap":
        return {bootstrap.BUNDLE_NAME, bootstrap.MANIFEST_NAME}
    if family == "image":
        return {images.INVENTORY, *(f"borealis-node-image-{r}-linux-amd64.oci.tar" for r in images.ROLES)}
    if family == "host_package":
        return {packages.ARCHIVE, packages.INVENTORY}
    if family == "k3s":
        return {k3s.ARCHIVE, k3s.BINARY, k3s.INSTALLER, k3s.INVENTORY}
    pins = json.loads((source / external.pins.LOCK).read_bytes())
    return {external.INVENTORY, *(external.asset_name(i["reference"]) for c in pins["components"] for i in c["images"])}


def build(source, revision, output, families, repository, go_bin, archives=None, reuse_release=None, base_sha=None):
    if not bootstrap.REPOSITORY.fullmatch(repository) or not families or set(families) - PACKAGERS.keys():
        raise ValueError("invalid packaging selection")
    output = output.resolve(strict=True)
    with tempfile.TemporaryDirectory(dir=output, prefix=".build-") as temp:
        work = Path(temp)
        snapshot = work / "snapshot"
        sha, tree, label = freeze_revision(source, revision, snapshot)
        base = RetainedRelease(reuse_release, repository, source, sha) if reuse_release else None
        result = work / "artifacts"
        result.mkdir()
        objects = {}
        for family in sorted(set(families)):
            directory = result / family
            kwargs = dict(archives=archives) if family == "host_package" else dict(go_bin=go_bin)
            inventory = base.dependency_family(family, snapshot, sha, label, directory) if base else None
            if inventory is None:
                if family == "image" and base:
                    kwargs["reuse"] = base
                inventory = PACKAGERS[family].build_assets(source=snapshot, release=label, source_sha=sha,
                            repository=repository, output_dir=directory, **kwargs)
            if inventory["source_sha"] != sha or inventory["release"] != label or inventory["repository"] != repository:
                raise ValueError("packager returned a different snapshot")
            names = artifact_names(family, snapshot)
            if {p.name for p in directory.iterdir()} != names:
                raise ValueError("packager output inventory changed")
            for name in sorted(names):
                path = directory / name
                if path.is_symlink() or not path.is_file() or path.stat().st_size < 1:
                    raise ValueError("invalid packaged file")
                objects[family + "/" + name] = dict(bytes=path.stat().st_size, sha256=bootstrap.digest(path))
        complete = set(families) == PACKAGERS.keys()
        descriptor = dict(schema=1, channel="development", kind="candidate" if complete else "component-set",
                          repository=repository, source_sha=sha, source_tree=tree, platform=bootstrap.PLATFORM,
                          artifacts=objects)
        if base_sha is not None:
            if not complete:
                raise ValueError("existing-member history requires a complete candidate")
            directory = result / upgrade.FAMILY
            descriptor["upgrade"] = upgrade.package(source, snapshot, base_sha, sha, directory)
            history = directory / upgrade.BUNDLE
            objects[upgrade.FAMILY + "/" + upgrade.BUNDLE] = dict(bytes=history.stat().st_size, sha256=bootstrap.digest(history))
        raw = canonical(descriptor)
        identity_bytes = binding(descriptor) if complete else raw
        if complete:
            (result / BINDING).write_bytes(identity_bytes)
        identity = ("dev-" if complete else "components-") + hashlib.sha256(identity_bytes).hexdigest()
        (result / INDEX).write_bytes(raw)
        destination = output / identity
        # Same filesystem. An interrupted build never appears at the final name.
        try:
            result.rename(destination)
        except OSError:
            if not destination.is_dir() or destination.is_symlink() or (destination / INDEX).read_bytes() != raw:
                raise
            verify(destination)
        return destination


def verify(directory):
    if directory.is_symlink() or (directory / INDEX).is_symlink():
        raise ValueError("invalid development manifest path")
    raw = (directory / INDEX).read_bytes()
    record = json.loads(raw)
    kind = record["kind"]
    prefix = {"candidate": "dev-", "component-set": "components-"}.get(kind)
    bound = raw
    if kind == "candidate":
        pointer = directory / BINDING
        if pointer.is_symlink() or not pointer.is_file() or pointer.stat().st_size > 4096:
            raise ValueError("development candidate binding missing")
        bound = binding(record)
        if pointer.read_bytes() != bound:
            raise ValueError("development candidate composition mismatch")
    if (not prefix or record["channel"] != "development" or record["schema"] != 1 or canonical(record) != raw or
            directory.name != prefix + hashlib.sha256(bound).hexdigest()):
        raise ValueError("development identity mismatch")
    upgrade_input = "upgrade" in record
    if (set(record) - {"upgrade"} != {"schema", "channel", "kind", "repository", "source_sha", "source_tree", "platform", "artifacts"}
            or not bootstrap.REPOSITORY.fullmatch(record["repository"]) or record["platform"] != bootstrap.PLATFORM
            or any(not bootstrap.SHA.fullmatch(record[k]) or record[k] == "0" * 40 for k in ("source_sha", "source_tree"))):
        raise ValueError("invalid development source identity")
    families = {p.name for p in directory.iterdir()} - ({INDEX, BINDING} if kind == "candidate" else {INDEX})
    if upgrade_input:
        meta = record["upgrade"]
        folder = directory / upgrade.FAMILY
        if (kind != "candidate" or set(meta) != {"base_sha", "compatibility"} or not bootstrap.SHA.fullmatch(meta["base_sha"])
                or meta["base_sha"] == record["source_sha"] or not isinstance(meta["compatibility"], dict)
                or folder.is_symlink() or not folder.is_dir() or {p.name for p in folder.iterdir()} != {upgrade.BUNDLE}):
            raise ValueError("invalid development upgrade package")
        families.discard(upgrade.FAMILY)
    if not families or families - PACKAGERS.keys() or (kind == "candidate") != (families == PACKAGERS.keys()):
        raise ValueError("invalid development family selection")
    if any((directory / f).is_symlink() or not (directory / f).is_dir() for f in families):
        raise ValueError("invalid development family directory")
    for family in families:
        pkg = PACKAGERS[family]
        inv_name = bootstrap.MANIFEST_NAME if family == "bootstrap" else pkg.INVENTORY
        inv_file = directory / family / inv_name
        if not inv_file.is_file() or inv_file.is_symlink() or inv_file.stat().st_size > 2 << 20:
            raise ValueError("development family inventory unavailable")
        inv = json.loads(inv_file.read_bytes())
        if (inv["release"] != "dev-src-" + record["source_sha"] or inv["source_sha"] != record["source_sha"]
                or inv["repository"] != record["repository"] or inv["platform"] != record["platform"]
                or family == "bootstrap" and inv["source_tree"] != record["source_tree"]):
            raise ValueError("development family source mismatch")
        if family == "external":
            expected = {inv_name, *(external.asset_name(i["reference"]) for i in inv["images"])}
            if not inv["images"]:
                raise ValueError("empty external inventory")
        else:
            expected = artifact_names(family, None)
        if {p.name for p in (directory / family).iterdir()} != expected:
            raise ValueError("incomplete development family")
    entries = {p.relative_to(directory).as_posix(): p for p in directory.glob("*/*")}
    if entries.keys() != record["artifacts"].keys():
        raise ValueError("development artifact inventory mismatch")
    for key, p in entries.items():
        item = record["artifacts"][key]
        if p.is_symlink() or not p.is_file() or p.stat().st_size != item["bytes"] or bootstrap.digest(p) != item["sha256"]:
            raise ValueError("development artifact content mismatch")
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    cmds = parser.add_subparsers(dest="command", required=True)
    package = cmds.add_parser("build")
    package.add_argument("--source", type=Path, required=True)
    package.add_argument("--revision", required=True)
    package.add_argument("--output", type=Path, required=True)
    package.add_argument("--family", choices=sorted(PACKAGERS), action="append")
    package.add_argument("--repository", default="bunny-lab-io/Borealis")
    package.add_argument("--go-bin", default="go")
    package.add_argument("--archives", type=Path)
    package.add_argument("--reuse-release", type=Path, help="Explicit digest-named retained immutable release")
    package.add_argument("--base-sha", help="Exact current source SHA for an existing-member upgrade")
    check = cmds.add_parser("verify")
    check.add_argument("directory", type=Path)
    args = parser.parse_args()
    if args.command == "verify":
        record = verify(args.directory)
        print(record["channel"], record["kind"], record["source_sha"], len(record["artifacts"]))
    else:
        print(build(args.source, args.revision, args.output, args.family or list(PACKAGERS),
                    args.repository, args.go_bin, args.archives, args.reuse_release, args.base_sha))


if __name__ == "__main__":
    main()
