#!/usr/bin/env python3
"""Build deterministic Borealis Engine installer release assets."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import shutil
from pathlib import Path


STABLE_RELEASE = re.compile(r"^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?$")
COMMIT_SHA = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
SUPPORTED_PLATFORMS = ("linux-amd64", "linux-arm64")


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def asset(path: Path, *, repository: str, release: str) -> dict[str, object]:
    return {
        "name": path.name,
        "url": f"https://github.com/{repository}/releases/download/{release}/{path.name}",
        "sha256": sha256(path),
        "size": path.stat().st_size,
    }


def build_assets(
    *,
    release: str,
    repository: str,
    source_sha: str,
    engine_script: Path,
    bootstrap_script: Path,
    output_dir: Path,
) -> dict[str, object]:
    if not STABLE_RELEASE.fullmatch(release):
        raise ValueError("release must use YYYY.MM.REVISION[.HOTFIX]")
    if not REPOSITORY.fullmatch(repository):
        raise ValueError("repository must use owner/name form")
    if not COMMIT_SHA.fullmatch(source_sha):
        raise ValueError("source SHA must be 40 lowercase hexadecimal characters")
    for path in (engine_script, bootstrap_script):
        if not path.is_file():
            raise ValueError(f"required script is missing: {path}")

    output_dir.mkdir(parents=True, exist_ok=True)
    engine_asset = output_dir / "Engine.sh"
    bootstrap_asset = output_dir / "Install-Engine.sh"
    shutil.copyfile(engine_script, engine_asset)
    shutil.copyfile(bootstrap_script, bootstrap_asset)
    engine_asset.chmod(0o755)
    bootstrap_asset.chmod(0o755)

    manifest = {
        "schema_version": 1,
        "repository": repository,
        "release": release,
        "source_sha": source_sha,
        "supported_platforms": list(SUPPORTED_PLATFORMS),
        "assets": {
            "bootstrap": asset(
                bootstrap_asset, repository=repository, release=release
            ),
            "engine": asset(engine_asset, repository=repository, release=release),
        },
    }
    manifest_path = output_dir / "borealis-engine-install-manifest.json"
    manifest_path.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )

    checksum_paths = (bootstrap_asset, engine_asset, manifest_path)
    checksums = "".join(f"{sha256(path)}  {path.name}\n" for path in checksum_paths)
    (output_dir / "SHA256SUMS").write_text(checksums, encoding="utf-8")
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--release", required=True)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--engine-script", required=True, type=Path)
    parser.add_argument("--bootstrap-script", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    build_assets(
        release=args.release,
        repository=args.repository,
        source_sha=args.source_sha,
        engine_script=args.engine_script,
        bootstrap_script=args.bootstrap_script,
        output_dir=args.output_dir,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
