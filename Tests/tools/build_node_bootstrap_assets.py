#!/usr/bin/env python3
"""Package exact tagged source and its node manager for central SSH delivery."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile


RELEASE = re.compile(r"^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?(?:-rc\.[1-9][0-9]*)?$")
SHA = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$")
PLATFORM = "linux-amd64"
GO_VERSION = "go1.25.12"
BUNDLE_NAME = f"borealis-node-bootstrap-{PLATFORM}.tar.gz"
MANIFEST_NAME = f"borealis-node-bootstrap-{PLATFORM}.json"
MAX_BUNDLE_BYTES = 256 * 1024 * 1024


def digest(path: Path) -> str:
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def git_environment() -> dict[str, str]:
    # Never inherit credential helpers, hooks, replacement objects, alternates,
    # global filters or operator-specific repository configuration into a bundle.
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_NO_REPLACE_OBJECTS="1", GIT_TERMINAL_PROMPT="0")
    return env


def git(root: Path, *args: str) -> str:
    result = subprocess.run(["git", "-C", str(root), *args], env=git_environment(),
                            capture_output=True, text=True, timeout=120, check=False)
    if result.returncode:
        raise ValueError("local Git packaging command failed; diagnostics withheld")
    return result.stdout.strip()


def source_snapshot(source: Path, target: Path, release: str, source_sha: str,
                    repository: str) -> None:
    if git(source, "rev-parse", f"refs/tags/{release}^{{commit}}") != source_sha:
        raise ValueError("release tag does not identify requested source SHA")
    # Use Git's shallow clone protocol: exact commit/tree plus original tag,
    # without historical source, working-tree files, caches or host credentials.
    result = subprocess.run(
        ["git", "clone", "--quiet", "--no-local", "--depth=1", "--single-branch",
         "--no-tags", "--no-checkout", "--template=", "--branch", release,
         str(source.resolve()), str(target)], env=git_environment(),
        capture_output=True, timeout=120, check=False)
    if result.returncode:
        raise ValueError("tagged source snapshot failed; diagnostics withheld")
    if git(target, "rev-parse", "HEAD") != source_sha:
        raise ValueError("release tag moved during source snapshot")
    # Current Engine source contains regular files only. Reject links before
    # checkout/build so an archive cannot depend on external or ambiguous paths.
    entries = git(target, "ls-tree", "-rz", "HEAD").split("\0")
    for entry in filter(None, entries):
        metadata, name = entry.split("\t", 1)
        mode, _, _ = metadata.split()
        path = Path(name)
        if path.is_absolute() or any(part.lower() == ".git" or part == ".." for part in path.parts):
            raise ValueError("unsafe tracked source path")
        if mode not in ("100644", "100755"):
            raise ValueError("source bundle cannot contain symlinks, submodules or unsupported objects")
    config = target / ".git" / "config"
    config.write_text(
        "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n"
        "\tlogAllRefUpdates = false\n[remote \"origin\"]\n"
        f"\turl = https://github.com/{repository}.git\n"
        "\tfetch = +refs/heads/*:refs/remotes/origin/*\n", encoding="utf-8")
    git(target, "reset", "--hard", source_sha)
    if git(target, "status", "--porcelain"):
        raise ValueError("tagged source checkout is not clean")
    git(target, "-c", "pack.threads=1", "repack", "-ad")
    for name in ("logs", "hooks"):
        shutil.rmtree(target / ".git" / name, ignore_errors=True)
    for name in ("FETCH_HEAD", "ORIG_HEAD", "description", "index"):
        (target / ".git" / name).unlink(missing_ok=True)
    # Zero-stat index keeps archive deterministic across checkout times/paths.
    git(target, "read-tree", source_sha)


def build_manager(source: Path, destination: Path, go_bin: str) -> None:
    version = subprocess.run([go_bin, "version"], capture_output=True, text=True,
                             timeout=15, check=True).stdout.split()
    if len(version) < 3 or version[2] != GO_VERSION:
        raise ValueError(f"node bootstrap requires {GO_VERSION}")
    env = os.environ.copy()
    env.update(GOWORK="off", GOTOOLCHAIN="local", GOOS="linux", GOARCH="amd64",
               CGO_ENABLED="0", GOFLAGS="-mod=readonly")
    destination.parent.mkdir(parents=True)
    subprocess.run(
        [go_bin, "-C", str(source / "Data/Engine/Containers/api-backend"), "build",
         "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", str(destination),
         "./cmd/borealis-node-manager"], env=env, timeout=600, check=True)
    destination.chmod(0o755)


def archive(root: Path, destination: Path) -> None:
    with destination.open("xb") as output:
        with gzip.GzipFile(filename="", fileobj=output, mode="wb", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as tar:
                for path in sorted(root.rglob("*")):
                    info = tar.gettarinfo(str(path), arcname=path.relative_to(root).as_posix())
                    info.uid = info.gid = info.mtime = 0
                    info.uname = info.gname = ""
                    info.pax_headers = {}
                    info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
                    if info.isfile():
                        with path.open("rb") as handle:
                            tar.addfile(info, handle)
                    else:
                        tar.addfile(info)


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, go_bin: str) -> dict:
    if not RELEASE.fullmatch(release) or not SHA.fullmatch(source_sha) or not REPOSITORY.fullmatch(repository):
        raise ValueError("invalid immutable node bootstrap identity")
    output_dir = output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    if any((output_dir / name).exists() for name in (BUNDLE_NAME, MANIFEST_NAME)):
        raise ValueError("refusing to replace previously packaged node assets")
    with tempfile.TemporaryDirectory(prefix="borealis-node-bootstrap-") as temporary:
        root = Path(temporary)
        snapshot = root / "source"
        source_snapshot(source, snapshot, release, source_sha, repository)
        binary = root / "bin/borealis-node-manager"
        build_manager(snapshot, binary, go_bin)
        identity = {"schema_version": 1, "repository": repository, "release": release,
                    "source_sha": source_sha, "source_tree": git(snapshot, "rev-parse", "HEAD^{tree}"),
                    "platform": PLATFORM, "go_version": GO_VERSION,
                    "node_manager": {"path": "bin/borealis-node-manager", "sha256": digest(binary),
                                     "size": binary.stat().st_size}}
        (root / "identity.json").write_text(json.dumps(identity, sort_keys=True, indent=2) + "\n")
        bundle = output_dir / BUNDLE_NAME
        try:
            archive(root, bundle)
            if bundle.stat().st_size > MAX_BUNDLE_BYTES:
                raise ValueError("node bootstrap exceeds bounded download size")
            manifest = dict(identity, asset={"name": BUNDLE_NAME, "size": bundle.stat().st_size,
                            "sha256": digest(bundle),
                            "url": f"https://github.com/{repository}/releases/download/{release}/{BUNDLE_NAME}"})
            with (output_dir / MANIFEST_NAME).open("x", encoding="utf-8") as handle:
                handle.write(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
        except Exception:
            bundle.unlink(missing_ok=True)
            raise
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--release", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--go-bin", default="go")
    build_assets(**vars(parser.parse_args()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
