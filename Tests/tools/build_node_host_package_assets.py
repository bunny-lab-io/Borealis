#!/usr/bin/env python3
"""Package reviewed Ubuntu host archives from fixed URLs or an offline cache.

No APT resolution, installation or service execution occurs. The
source lock records content measurements only; generated files, transient
installation allocations and service state remain separate readiness inputs.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request

import build_node_bootstrap_assets as bootstrap

LOCK = "Data/Engine/Containers/api-backend/internal/clusterbootstrap/host_packages.lock.json"
INVENTORY = "borealis-node-host-packages-ubuntu-noble-linux-amd64.json"
ARCHIVE = "borealis-node-host-packages-ubuntu-noble-linux-amd64.tar"
ROOTS = {
    "python3": "base", "ca-certificates": "base", "curl": "base", "gnupg": "base",
    "iptables": "base", "open-iscsi": "base", "nfs-common": "base",
    "docker-ce": "docker", "docker-ce-cli": "docker", "containerd.io": "docker", "docker-buildx-plugin": "docker",
}
FIELDS = {"name", "version", "architecture", "file", "phase", "size", "sha256", "url", "file_bytes", "entries"}
MAX_ARCHIVE, MAX_EXPANDED, MAX_ENTRIES = (2 << 30) - 1, 32 << 30, 1_000_000


def unique_object(pairs):
    value = {}
    seen = set()
    for key, item in pairs:
        if key.lower() in seen:
            raise ValueError("ambiguous host package JSON")
        seen.add(key.lower())
        value[key] = item
    return value


def archive_size(packages: list[dict]) -> int:
    size = 1024 + sum(512 + ((p["size"] + 511) // 512) * 512 for p in packages)
    return ((size + 10239) // 10240) * 10240


def valid_url(pin: dict) -> bool:
    raw = pin["url"]
    url = urllib.parse.urlsplit(raw)
    if (len(raw) > 2048 or url.scheme != "https" or url.netloc not in ("archive.ubuntu.com", "download.docker.com") or
            url.username or url.password or url.query or url.fragment or any(c in raw for c in "%?#") or url.geturl() != raw):
        return False
    parts = url.path.removeprefix("/").split("/")
    if any(not re.fullmatch(r"[A-Za-z0-9+._~\-]+", part) or part in (".", "..") for part in parts):
        return False
    version = pin["version"].split(":", 1)[-1]
    if parts[-1] != f"{pin['name']}_{version}_{pin['architecture']}.deb":
        return False
    if url.netloc == "archive.ubuntu.com":
        return len(parts) == 6 and parts[:2] == ["ubuntu", "pool"] and parts[2] in ("main", "universe", "restricted", "multiverse")
    return len(parts) == 8 and parts[:7] == ["linux", "ubuntu", "dists", "noble", "pool", "stable", "amd64"]


def parse_plan(raw: bytes) -> tuple[dict, str]:
    # Exact committed bytes, including one terminal newline, are the shared
    # digest input. Never rely on matching Python/Go object serialization.
    if not 0 < len(raw) <= 512 << 10 or not raw.endswith(b"\n") or raw.endswith(b"\n\n"):
        raise ValueError("invalid host package lock framing")
    plan = json.loads(raw, object_pairs_hook=unique_object)
    if (not isinstance(plan, dict) or set(plan) != {"version", "os", "os_version", "suite", "platform", "packages"} or
            type(plan["version"]) is not int or plan["version"] != 1 or plan["os"] != "ubuntu" or
            plan["os_version"] != "24.04" or plan["suite"] != "noble" or plan["platform"] != "linux-amd64" or
            not isinstance(plan["packages"], list) or not len(ROOTS) <= len(plan["packages"]) <= 256):
        raise ValueError("unsupported host package plan")
    files, names, roots = set(), [], set()
    compressed = expanded = entries = 0
    for p in plan["packages"]:
        if (not isinstance(p, dict) or set(p) != FIELDS or
                any(not isinstance(p[k], str) for k in FIELDS - {"size", "file_bytes", "entries"}) or
                not re.fullmatch(r"[a-z0-9][a-z0-9+.-]{0,199}", p["name"]) or
                not re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]{0,199}", p["version"]) or
                p["architecture"] not in ("amd64", "all") or p["phase"] not in ("base", "docker") or
                not re.fullmatch(r"[a-z0-9][A-Za-z0-9.+:%_~\-]{0,95}\.deb", p["file"]) or p["file"] in files or
                not re.fullmatch(r"[0-9a-f]{64}", p["sha256"]) or p["sha256"] == "0" * 64 or not valid_url(p) or
                any(type(p[k]) is not int for k in ("size", "file_bytes", "entries")) or
                not 0 < p["size"] <= MAX_ARCHIVE or not 0 <= p["file_bytes"] <= MAX_EXPANDED or
                not 0 < p["entries"] <= MAX_ENTRIES):
            raise ValueError("invalid host package pin")
        if p["name"] in ROOTS:
            if p["phase"] != ROOTS[p["name"]]:
                raise ValueError("host package root phase changed")
            roots.add(p["name"])
        files.add(p["file"])
        names.append(p["name"])
        compressed, expanded, entries = compressed + p["size"], expanded + p["file_bytes"], entries + p["entries"]
    if (names != sorted(set(names)) or roots != set(ROOTS) or compressed > MAX_ARCHIVE or
            expanded > MAX_EXPANDED or entries > MAX_ENTRIES or archive_size(plan["packages"]) > MAX_ARCHIVE):
        raise ValueError("incomplete or oversized host package closure")
    return plan, hashlib.sha256(raw).hexdigest()


class DigestReader:
    def __init__(self, source):
        self.source, self.digest, self.size = source, hashlib.sha256(), 0

    def read(self, size=-1):
        value = self.source.read(size)
        self.digest.update(value)
        self.size += len(value)
        return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("host package origin redirected")


def download(pin: dict, destination: Path) -> None:
    if not valid_url(pin) or type(pin["size"]) is not int or not 0 < pin["size"] <= MAX_ARCHIVE:
        raise ValueError("invalid host package download pin")
    # Fixed public origins; no proxies, credentials, cookies or redirects.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    digest, size, deadline = hashlib.sha256(), 0, time.monotonic() + 300
    try:
        with opener.open(pin["url"], timeout=60) as response, destination.open("xb") as output:
            if response.status != 200 or response.headers.get("Content-Encoding") not in (None, "identity"):
                raise ValueError("invalid package response")
            if response.headers.get("Content-Length") not in (None, str(pin["size"])):
                raise ValueError("changed package length")
            while True:
                if time.monotonic() >= deadline:
                    raise ValueError("package download expired")
                chunk = response.read(min(1 << 20, pin["size"] - size + 1))
                if not chunk:
                    break
                size += len(chunk)
                if size > pin["size"]:
                    raise ValueError("oversized package download")
                digest.update(chunk)
                output.write(chunk)
            if time.monotonic() >= deadline or size != pin["size"] or digest.hexdigest() != pin["sha256"]:
                raise ValueError("changed package bytes")
    except Exception:
        raise ValueError("pinned host package download failed") from None


def assemble_archive(plan: dict, archives: Path, destination: Path) -> None:
    """Copy only pinned inert archives; every copied byte is rehashed.

    Native package metadata/content measurements belong to reviewed lock
    updates. The exact archive hash authenticates those same measured bytes;
    packaging does not introduce another package parser or require dpkg tools.
    """
    with destination.open("xb") as output, tarfile.open(fileobj=output, mode="w", format=tarfile.USTAR_FORMAT) as archive:
        for pin in plan["packages"]:
            fd = os.open(archives / pin["file"], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(fd, "rb") as source:
                before = os.fstat(source.fileno())
                if not stat.S_ISREG(before.st_mode) or before.st_size != pin["size"]:
                    raise ValueError("host package cache file changed")
                if hashlib.file_digest(source, "sha256").hexdigest() != pin["sha256"]:
                    raise ValueError("host package cache content changed")
                source.seek(0)
                entry = tarfile.TarInfo(pin["file"])
                entry.mode, entry.size = 0o644, pin["size"]
                copied = DigestReader(source)
                archive.addfile(entry, copied)
                after = os.fstat(source.fileno())
                if (source.read(1) or copied.size != pin["size"] or copied.digest.hexdigest() != pin["sha256"] or
                        (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) !=
                        (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns)):
                    raise ValueError("host package cache changed while packaging")
    if destination.stat().st_size != archive_size(plan["packages"]):
        raise ValueError("host package archive framing changed")


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, archives: Path | None = None) -> dict:
    if (not bootstrap.RELEASE.fullmatch(release) or not bootstrap.SHA.fullmatch(source_sha) or source_sha == "0" * 40 or
            not bootstrap.REPOSITORY.fullmatch(repository)):
        raise ValueError("invalid immutable host package release identity")
    if bootstrap.git(source, "status", "--porcelain", "--untracked-files=no"):
        raise ValueError("host package source has tracked local changes")
    if archives is not None:
        archives = archives.resolve(strict=True)
    if archives is not None and not archives.is_dir():
        raise ValueError("host package archive cache unavailable")
    output_dir = output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=False)
    try:
        with tempfile.TemporaryDirectory(prefix="borealis-release-host-packages-") as temporary:
            snapshot = Path(temporary) / "source"
            bootstrap.source_snapshot(source, snapshot, release, source_sha, repository)
            plan, digest = parse_plan((snapshot / LOCK).read_bytes())
            if archives is None:
                archives = Path(temporary) / "cache"
                archives.mkdir()
                for pin in plan["packages"]:
                    download(pin, archives / pin["file"])
            destination = output_dir / ARCHIVE
            assemble_archive(plan, archives, destination)
            inventory = dict(plan, repository=repository, release=release, source_sha=source_sha, plan_sha256=digest,
                             archive=dict(name=ARCHIVE, size=destination.stat().st_size, sha256=bootstrap.digest(destination)))
            (output_dir / INVENTORY).write_text(json.dumps(inventory, sort_keys=True, separators=(",", ":")) + "\n")
            return inventory
    except BaseException:
        shutil.rmtree(output_dir)
        raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("release", "source-sha", "repository"):
        parser.add_argument("--" + name, required=True)
    for name in ("source", "output-dir"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--archives", type=Path, help="Existing reviewed .deb cache; omit to fetch exact pinned URLs")
    build_assets(**vars(parser.parse_args()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
