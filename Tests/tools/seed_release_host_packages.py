#!/usr/bin/env python3
"""Read a published package archive into the exact target release's private input cache."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile


def unpack(archive, output, plan, expected_digest, expected_size):
    with archive.open("rb") as source:
        if archive.stat().st_size != expected_size or hashlib.file_digest(source, "sha256").hexdigest() != expected_digest:
            raise ValueError("published host archive digest or size differs")
    pins = {pin["file"]: pin for pin in plan["packages"]}
    with tempfile.TemporaryDirectory(dir=output.parent) as private:
        stage = Path(private) / "packages"
        stage.mkdir(mode=0o700)
        with tarfile.open(archive, "r:") as tar:
            for member in tar:
                pin = pins.pop(member.name, None)
                if not pin or not member.isfile() or member.size != pin["size"] or Path(member.name).name != member.name:
                    raise ValueError("package member differs from target lock")
                with tar.extractfile(member) as source, (stage / member.name).open("xb+") as dest:
                    shutil.copyfileobj(source, dest, 1 << 20)
                    dest.seek(0)
                    if hashlib.file_digest(dest, "sha256").hexdigest() != pin["sha256"]:
                        raise ValueError("package digest differs from target lock")
        if pins:
            raise ValueError("published archive lacks required packages")
        if output.exists():
            raise ValueError("seed destination already exists")
        stage.rename(output)


def immutable_release(repository, release):
    meta = json.loads(subprocess.check_output(["gh", "api", f"repos/{repository}/releases/tags/{release}"]))
    if (meta.get("tag_name") != release or meta.get("draft") is not False or meta.get("immutable") is not True
            or not meta.get("published_at") or meta.get("prerelease") is not ("-rc." in release)):
        raise ValueError("asset reuse requires an immutable published release")
    return meta


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--release", required=True)
    args = parser.parse_args()
    sys.path.insert(0, str(args.source.resolve() / "Tests/tools"))
    import build_node_host_package_assets as packages

    if not packages.bootstrap.REPOSITORY.fullmatch(args.repository) or not packages.bootstrap.RELEASE.fullmatch(args.release):
        raise ValueError("invalid published release selector")
    plan, _ = packages.parse_plan((args.source / packages.LOCK).read_bytes())
    meta = immutable_release(args.repository, args.release)
    (asset,) = [a for a in meta["assets"] if a["name"] == packages.ARCHIVE]
    if (asset["state"] != "uploaded" or not re.fullmatch("sha256:[0-9a-f]{64}", asset["digest"])
            or asset["size"] != packages.archive_size(plan["packages"])):
        raise ValueError("published archive incompatible with target package lock")
    with tempfile.TemporaryDirectory(dir=args.output.parent) as private:
        archive = Path(private) / packages.ARCHIVE
        subprocess.run(["gh", "release", "download", args.release, "--repo", args.repository,
                        "--pattern", packages.ARCHIVE, "--output", str(archive)], check=True)
        unpack(archive, args.output, plan, asset["digest"][7:], asset["size"])
    print(f"Verified {len(plan['packages'])} packages against the target lock")


if __name__ == "__main__":
    main()
