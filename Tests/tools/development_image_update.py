"""Rebuild a binary-only OCI layer on an explicitly retained runtime image."""

import hashlib
import json
from pathlib import Path
import re
import shutil
import tarfile

import build_node_bootstrap_assets as bootstrap
from build_node_image_assets import normalize_index, unique_object

DIST = "Data/Engine/Containers/api-backend/dist/"
MODULE = "Data/Engine/Containers/api-backend/"
ROLES = {"api-backend", "borealis-operator", "job-scheduler", "wireguard-tunnel"}


def binary_layer(reuse, role, snapshot, item, paths):
    if role not in ROLES or not paths or item["context"] != ".":
        return None
    declared = {n for n in reuse.original.keys() | reuse.current.keys()
                if reuse.original.get(n) != reuse.current.get(n)
                and any(n == p or n.startswith(p + "/") for p in paths)}
    # The API declares rollout compatibility metadata as an input to release
    # selection. The Go compiler reads the module. A byte copied into the
    # actual runtime image must still take the normal full build path.
    compatibility = "Data/Engine/release-manifest.json"
    binary_changes = declared - {compatibility}
    if not binary_changes or any(not n.startswith(MODULE) or not n.endswith((".go", "/go.mod", "/go.sum")) for n in binary_changes):
        return None
    # Apply this shortcut only to known Dockerfiles whose post-COPY effects
    # consist of setting executable modes. Other instructions require a full build.
    file = snapshot / item["dockerfile"]
    instructions = file.read_text().replace("\\\n", " ").splitlines()
    copies, copy_seen, destinations = [], False, set()
    for line in instructions:
        parts = line.split()
        if not parts or parts[0].startswith("#"):
            continue
        verb = parts[0].upper()
        if verb == "COPY":
            copy_seen = True
            if len(parts) != 3 or not parts[2].startswith("/"):
                return None
            if compatibility in declared:
                source = Path(parts[1])
                if (not re.fullmatch(r"[A-Za-z0-9_./-]+", parts[1]) or source.is_absolute()
                        or ".." in source.parts or Path(compatibility).is_relative_to(source)):
                    return None
            if parts[1].startswith(DIST):
                name = parts[1][len(DIST):]
                if not re.fullmatch(r"[a-z][a-z0-9-]+", name) or not re.fullmatch(r"/usr/local/bin/[a-z][a-z0-9-]+", parts[2]):
                    return None
                copies.append((name, parts[2]))
            destinations.add(parts[2])
        elif copy_seen and verb == "RUN":
            if parts[1:3] != ["chmod", "0755"] or any(name not in destinations for name in parts[3:]):
                return None
        elif copy_seen and verb not in {"EXPOSE", "USER", "ENTRYPOINT"}:
            return None
    return copies or None


def prepare_layout(reuse, role, proof, directory):
    name = f"borealis-node-image-{role}-linux-amd64.oci.tar"
    src = reuse.directory / name
    identity = reuse.record["assets"][name]
    if (identity != {"bytes": proof["archive_bytes"], "sha256": proof["archive_sha256"]}
            or src.is_symlink() or src.stat().st_size != identity["bytes"] or bootstrap.digest(src) != identity["sha256"]):
        raise ValueError("binary-layer base differs from retained release")
    unpack_layout(src, directory, identity["bytes"])


def unpack_layout(src, directory, maximum):
    directory.mkdir()
    (directory / "blobs/sha256").mkdir(parents=True)
    # Only OCI layout files, never image filesystem paths. BuildKit resolves
    # by the original verified manifest digest; every copied blob is measured.
    count = 0
    with tarfile.open(src, "r:") as archive:
        for member in archive:
            count += 1
            name = member.name
            if (count > 260 or not member.isfile() or not 0 < member.size <= maximum
                    or name not in {"index.json", "oci-layout"} and not re.fullmatch(r"blobs/sha256/[0-9a-f]{64}", name)):
                raise ValueError("invalid retained OCI layout")
            path = directory / name
            with archive.extractfile(member) as reader, path.open("xb") as out:
                shutil.copyfileobj(reader, out, 1 << 20)
            if name.startswith("blobs/") and bootstrap.digest(path) != path.name:
                raise ValueError("retained OCI blob changed")


def compose_layers(base, addition, manifest_digest, labels, output):
    # BuildKit emits only the changed files into a scratch image. The original
    # config (including non-root User, entrypoint and environment) and layer
    # order come from the separately verified prior generation.
    def read(path):
        if path.stat().st_size > 1 << 20:
            raise ValueError("oversized OCI metadata")
        return json.loads(path.read_bytes(), object_pairs_hook=unique_object)

    def blob(directory, descriptor):
        digest = descriptor["digest"]
        if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest):
            raise ValueError("invalid OCI descriptor")
        path = directory / "blobs/sha256" / digest[7:]
        if path.stat().st_size != descriptor["size"]:
            raise ValueError("incorrect OCI descriptor size")
        return path

    def image(directory):
        index = read(directory / "index.json")
        (desc,) = index["manifests"]
        manifest = read(blob(directory, desc))
        config = read(blob(directory, manifest["config"]))
        if (config["os"], config["architecture"], config["rootfs"]["type"]) != ("linux", "amd64", "layers"):
            raise ValueError("incompatible OCI layer")
        if len(manifest["layers"]) != len(config["rootfs"]["diff_ids"]):
            raise ValueError("incomplete OCI root filesystem")
        return index, desc, manifest, config

    def store(descriptor, data):
        raw = json.dumps(data, sort_keys=True, separators=(",", ":")).encode()
        digest = hashlib.sha256(raw).hexdigest()
        path = base / "blobs/sha256" / digest
        path.write_bytes(raw)
        descriptor.update(digest="sha256:" + digest, size=len(raw))
        return path

    index, desc, manifest, config = image(base)
    _, _, delta, delta_config = image(addition)
    if desc["digest"] != manifest_digest or len(delta["layers"]) != 1:
        raise ValueError("unexpected base or incremental layer")
    paths = [blob(base, layer) for layer in manifest["layers"]]
    paths.append(blob(addition, delta["layers"][0]))
    manifest["layers"].extend(delta["layers"])
    config["rootfs"]["diff_ids"].extend(delta_config["rootfs"]["diff_ids"])
    config.setdefault("history", []).extend(delta_config.get("history", []))
    config["config"].setdefault("Labels", {}).update(dict(label.split("=", 1) for label in labels))
    paths.append(store(manifest["config"], config))
    paths.append(store(desc, manifest))
    (base / "index.json").write_text(json.dumps(index, separators=(",", ":")))
    with tarfile.open(output, "x:", format=tarfile.USTAR_FORMAT) as archive:
        objects = {p.name: p for p in paths}
        for name, path in [("oci-layout", base / "oci-layout"), ("index.json", base / "index.json"),
                           *(("blobs/sha256/" + digest, path) for digest, path in sorted(objects.items()))]:
            header = tarfile.TarInfo(name)
            header.size = path.stat().st_size
            with path.open("rb") as f:
                archive.addfile(header, f)


def build_layer(run, builder, reuse, role, proof, snapshot, copies, root, reference, exported, labels):
    layout = root / (role + "-base")
    inputs = root / (role + "-bin")
    inputs.mkdir()
    try:
        prepare_layout(reuse, role, proof, layout)
        binary_dir = inputs / "bin"
        binary_dir.mkdir()
        for name, dest in copies:
            src = snapshot / DIST / name
            if src.is_symlink() or not src.is_file():
                raise ValueError("compiled development binary missing")
            target = binary_dir / Path(dest).name
            if target.exists():
                raise ValueError("duplicate compiled image destination")
            shutil.copyfile(src, target)
        (inputs / "Dockerfile").write_text("FROM scratch\nCOPY --chmod=0755 bin/ /usr/local/bin/\n")
        incremental = inputs / "delta.tar"
        args = [*builder, "build", "--frontend", "dockerfile.v0", "--local", f"context={inputs}",
                "--local", f"dockerfile={inputs}", "--opt", "platform=linux/amd64", "--output",
                f"type=oci,name={reference},oci-mediatypes=true,compression=gzip,dest={incremental}"]
        run(args)
        normalized = inputs / "normalized.tar"
        normalize_index(incremental, normalized, reference)
        addition = inputs / "layout"
        unpack_layout(normalized, addition, normalized.stat().st_size)
        compose_layers(layout, addition, proof["manifest_digest"],
                       labels + ["io.borealis.development.base=" + proof["manifest_digest"]], exported)
    finally:
        shutil.rmtree(inputs)
        if layout.exists():
            shutil.rmtree(layout)
