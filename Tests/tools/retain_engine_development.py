#!/usr/bin/env python3
"""Import a completed build into the coordinator's verified, read-only catalog."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile

from build_engine_development import BINDING, INDEX, PACKAGERS
import development_upgrade as upgrade
from node_artifact_cache import DIGEST, configured_store

DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW


def open_file(parent, name, maximum):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
    source = os.fdopen(fd, "rb")
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= maximum:
        source.close()
        raise ValueError("invalid build artifact")
    return source


def read_metadata(parent, name, maximum):
    with open_file(parent, name, maximum) as source:
        raw = source.read(maximum + 1)
    if len(raw) > maximum:
        raise ValueError("build metadata exceeds limit")
    return raw


RUNTIME_CATALOG = Path("/var/lib/borealis-artifacts/candidates")
RUNTIME_GROUP = 64646


def publish_reader_access(directory, objects):
    # CAS objects remain owner-only through their cache directory. Only verified
    # inert files are reachable through these administrative generation links.
    for name in [*objects, INDEX, BINDING]:
        with (directory / name).open("rb") as f:
            os.fchown(f.fileno(), 0, RUNTIME_GROUP)
            os.fchmod(f.fileno(), 0o440)
            os.fsync(f.fileno())
    for folder in [*(directory / family for family in sorted({n.split("/")[0] for n in objects})), directory]:
        os.chown(folder, 0, RUNTIME_GROUP)
        folder.chmod(0o750)
        sync_directory(folder)


def retain(source, references, identity, source_sha, repository, verifier, runtime_catalog=False):
    if runtime_catalog:
        if references != RUNTIME_CATALOG or references.resolve(strict=True) != RUNTIME_CATALOG or os.geteuid() != 0:
            raise ValueError("runtime import requires the root-owned fixed catalog")
        for ancestor in [references, *references.parents]:
            st = ancestor.lstat()
            if st.st_uid != 0 or st.st_mode & 0o022:
                raise ValueError("invalid runtime catalog ownership")
        st = references.stat()
        if st.st_gid != RUNTIME_GROUP or stat.S_IMODE(st.st_mode) != 0o750:
            raise ValueError("runtime catalog must have Engine read-only group access")
    if (not re.fullmatch(r"dev-[0-9a-f]{64}", identity) or not re.fullmatch(r"[0-9a-f]{40}", source_sha)
            or not verifier.is_absolute()):
        raise ValueError("an approved candidate, source and installed verifier are required")
    cache = configured_store()
    target = references / identity
    base_fd = os.open(source, DIRECTORY)
    try:
        input_fd = os.open(identity, DIRECTORY, dir_fd=base_fd)
    finally:
        os.close(base_fd)
    try:
        binding = read_metadata(input_fd, BINDING, 4096)
        manifest = read_metadata(input_fd, INDEX, 64 << 10)
        pointer = json.loads(binding)
        if ("dev-" + hashlib.sha256(binding).hexdigest() != identity or pointer["source_sha"] != source_sha
                or pointer["repository"] != repository or pointer["channel"] != "development"
                or hashlib.sha256(manifest).hexdigest() != pointer["manifest_sha256"]):
            raise ValueError("approved candidate or source differs from build output")
        objects = json.loads(manifest)["artifacts"]
        if not 0 < len(objects) <= 100:
            raise ValueError("invalid candidate artifact count")
        with tempfile.TemporaryDirectory(dir=references, prefix=".import-") as private:
            stage = Path(private) / identity
            stage.mkdir(mode=0o700)
            for path, obj in sorted(objects.items()):
                family, name = path.split("/", 1)
                if ((family not in PACKAGERS and (family, name) != (upgrade.FAMILY, upgrade.BUNDLE)) or not re.fullmatch(r"borealis-node-[a-z0-9.-]{1,160}", name)
                        or not DIGEST.fullmatch(obj["sha256"]) or type(obj["bytes"]) is not int
                        or not 0 < obj["bytes"] < 2 << 30):
                    raise ValueError("invalid candidate artifact descriptor")
                directory = stage / family
                directory.mkdir(exist_ok=True, mode=0o700)

                def acquire(output):
                    family_fd = os.open(family, DIRECTORY, dir_fd=input_fd)
                    try:
                        with open_file(family_fd, name, obj["bytes"]) as reader, output.open("xb") as writer:
                            # Bound IO even if the producer changes the input during import.
                            remaining = obj["bytes"]
                            while remaining:
                                chunk = reader.read(min(remaining, 1 << 20))
                                if not chunk:
                                    raise ValueError("incomplete source artifact")
                                writer.write(chunk)
                                remaining -= len(chunk)
                            if reader.read(1):
                                raise ValueError("source artifact grew during import")
                    finally:
                        os.close(family_fd)

                cache.materialize(obj["sha256"], obj["bytes"], directory / name, acquire, retain=True)
            for name, data in ((INDEX, manifest), (BINDING, binding)):
                with (stage / name).open("xb") as dst:
                    dst.write(data)
                    dst.flush()
                    os.fsync(dst.fileno())
            check = [str(verifier), "development-candidate-proof", "--repository", repository,
                     "--source-sha", source_sha, "--candidate", identity, "--root"]
            subprocess.run(check + [private], check=True)
            if runtime_catalog:
                publish_reader_access(stage, objects)
            for directory in [*stage.iterdir(), stage]:
                if directory.is_dir():
                    sync_directory(directory)
            try:
                stage.rename(target)
            except OSError:
                if not target.is_dir() or target.is_symlink():
                    raise
                # A retry must find precisely the already-pinned, verified generation.
                if any(not (target / path).samefile(stage / path) for path in objects):
                    raise ValueError("existing candidate retention conflict")
                subprocess.run(check + [str(references)], check=True)
                if runtime_catalog:
                    publish_reader_access(target, objects)
            sync_directory(references)
    finally:
        os.close(input_fd)
    return target


def sync_directory(path):
    fd = os.open(path, DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("source", "references", "verifier"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--runtime-catalog", action="store_true", help="Grant the non-root Engine group read-only access to verified catalog entries.")
    parser.add_argument("--candidate", dest="identity", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", default="bunny-lab-io/Borealis")
    print(retain(**vars(parser.parse_args())))


if __name__ == "__main__":
    main()
