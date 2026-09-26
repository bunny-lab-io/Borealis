#!/usr/bin/env python3
"""Package source-pinned K3s inputs without running K3s or importing images."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request

import build_node_bootstrap_assets as bootstrap

INVENTORY = "borealis-node-k3s-linux-amd64.json"
BINARY = "borealis-node-k3s-linux-amd64"
ARCHIVE = "borealis-node-k3s-images-linux-amd64.tar"
LOCK = "Data/Engine/Containers/api-backend/internal/clusterbootstrap/k3s_inputs.lock.json"


class ReleaseRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        url = urllib.parse.urlsplit(newurl)
        if (getattr(req, "release_redirected", False) or code != 302 or
                url.scheme != "https" or url.hostname != "release-assets.githubusercontent.com" or
                url.port not in (None, 443) or url.username or url.password or url.fragment or
                not url.path.startswith("/github-production-release-asset/")):
            raise ValueError("unexpected K3s asset redirect")
        # Public artifacts only; never forward credentials, cookies or headers.
        request = urllib.request.Request(newurl)
        request.release_redirected = True
        return request


def download(version: str, pin: dict, destination: Path) -> None:
    if (not bootstrap.re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+\+k3s[1-9][0-9]*", version) or
            pin["name"] not in ("k3s", "k3s-airgap-images-amd64.tar") or
            not bootstrap.re.fullmatch(r"[0-9a-f]{64}", pin["sha256"]) or
            type(pin["size"]) is not int or not 0 < pin["size"] < 2 << 30):
        raise ValueError("invalid pinned K3s input")
    url = "https://github.com/k3s-io/k3s/releases/download/" + urllib.parse.quote(version, safe="") + "/" + pin["name"]
    opener = urllib.request.build_opener(ReleaseRedirect())
    digest, size = hashlib.sha256(), 0
    try:
        with opener.open(url, timeout=60) as response, destination.open("xb") as output:
            if response.status != 200 or response.headers.get("Content-Encoding") not in (None, "identity"):
                raise ValueError("invalid K3s asset response")
            length = response.headers.get("Content-Length")
            if length is not None and length != str(pin["size"]):
                raise ValueError("changed K3s asset length")
            while chunk := response.read(min(1 << 20, pin["size"] - size + 1)):
                size += len(chunk)
                if size > pin["size"]:
                    raise ValueError("oversized K3s asset")
                digest.update(chunk)
                output.write(chunk)
        if size != pin["size"] or digest.hexdigest() != pin["sha256"]:
            raise ValueError("changed K3s asset content")
    except Exception:
        # Do not expose upstream responses or signed CDN query strings.
        raise ValueError("pinned K3s asset download failed") from None


def build_assets(*, source: Path, release: str, source_sha: str, repository: str,
                 output_dir: Path, go_bin: str) -> dict:
    if not bootstrap.RELEASE.fullmatch(release) or not bootstrap.SHA.fullmatch(source_sha) or not bootstrap.REPOSITORY.fullmatch(repository):
        raise ValueError("invalid immutable K3s release identity")
    output_dir = output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=False)
    try:
        with tempfile.TemporaryDirectory(prefix="borealis-release-k3s-") as temporary:
            root = Path(temporary)
            snapshot = root / "source"
            bootstrap.source_snapshot(source, snapshot, release, source_sha, repository)
            pins = json.loads((snapshot / LOCK).read_text())
            baseline = json.loads((snapshot / "Data/Engine/release-manifest.json").read_text())
            if baseline["required_k3s_baseline"] != pins["version"]:
                raise ValueError("K3s packaging lock differs from release baseline")
            verifier = root / "node-manager"
            bootstrap.build_manager(snapshot, verifier, go_bin)
            download(pins["version"], pins["binary"], output_dir / BINARY)
            download(pins["version"], pins["archive"], output_dir / ARCHIVE)
            result = subprocess.run([str(verifier), "k3s-archive-proof", "--archive", str(output_dir / ARCHIVE)],
                                    check=True, stdout=subprocess.PIPE, timeout=600)
            proof = json.loads(result.stdout)
            inventory = dict(version=1, repository=repository, release=release, source_sha=source_sha,
                             platform="linux-amd64", k3s_version=pins["version"], binary=pins["binary"], archive=proof)
            (output_dir / INVENTORY).write_text(json.dumps(inventory, sort_keys=True, separators=(",", ":")) + "\n")
            return inventory
    except BaseException:
        shutil.rmtree(output_dir)
        raise


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("release", "source-sha", "repository"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--go-bin", default="go")
    build_assets(**vars(parser.parse_args()))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
