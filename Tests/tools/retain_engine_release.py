#!/usr/bin/env python3
"""Acquire and retain immutable Engine release assets on one coordinating host."""
import argparse
import contextlib
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import tempfile
import urllib.error
import urllib.parse
import urllib.request

import build_node_bootstrap_assets as bootstrap
from node_artifact_cache import configured_store, regular_reader


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(url, accept, token):
    headers = {"Accept": accept, "User-Agent": "Borealis-release-cache", "Accept-Encoding": "identity"}
    if token:
        headers["Authorization"] = "Bearer " + token
    opener = urllib.request.build_opener(NoRedirect)
    return opener.open(urllib.request.Request(url, headers=headers), timeout=60)


def metadata(url, token):
    with request(url, "application/vnd.github+json", token) as response:
        raw = response.read((2 << 20) + 1)
    if len(raw) > 2 << 20:
        raise ValueError("oversized GitHub release metadata")
    return json.loads(raw)


def download(asset, output, token):
    try:
        response = request(asset["url"], "application/octet-stream", token)
    except urllib.error.HTTPError as exc:
        with contextlib.closing(exc):
            location = exc.headers.get("Location", "")
            target = urllib.parse.urlsplit(location)
            if (exc.code != 302 or len(location) > 8192 or target.scheme != "https"
                    or target.netloc != "release-assets.githubusercontent.com" or target.fragment
                    or not target.path.startswith("/github-production-release-asset/")):
                raise ValueError("release asset response unavailable") from None
        response = request(location, "application/octet-stream", "")
    with response, output.open("xb") as writer:
        if response.status != 200 or response.headers.get("Content-Encoding") not in (None, "identity"):
            raise ValueError("invalid release asset encoding")
        remaining = asset["size"]
        while remaining:
            data = response.read(min(remaining, 1 << 20))
            if not data:
                raise ValueError("truncated release asset")
            writer.write(data)
            remaining -= len(data)
        if response.read(1):
            raise ValueError("excess release asset bytes")


def release_assets(repo, tag, sha, token):
    if not bootstrap.REPOSITORY.fullmatch(repo) or not bootstrap.RELEASE.fullmatch(tag) or not bootstrap.SHA.fullmatch(sha):
        raise ValueError("invalid release selector")
    api = "https://api.github.com/repos/" + repo
    release = metadata(api + "/releases/tags/" + tag, token)
    if (release["tag_name"] != tag or release["draft"] is not False or release["immutable"] is not True
            or release["prerelease"] is not ("-rc." in tag) or not release["published_at"]
            or not 1 <= len(release["assets"]) <= 100):
        raise ValueError("published release identity or channel mismatch")
    datetime.datetime.fromisoformat(release["published_at"].replace("Z", "+00:00"))
    ref = metadata(api + "/git/ref/tags/" + tag, token)
    if ref["ref"] != "refs/tags/" + tag:
        raise ValueError("release reference mismatch")
    obj = ref["object"]
    for _ in range(4):
        if obj["type"] == "commit":
            break
        if obj["type"] != "tag" or not bootstrap.SHA.fullmatch(obj["sha"]):
            raise ValueError("unsupported release tag object")
        obj = metadata(api + "/git/tags/" + obj["sha"], token)["object"]
    if obj["type"] != "commit" or obj["sha"] != sha:
        raise ValueError("tag does not resolve to approved commit")
    assets, ids = {}, set()
    for a in release["assets"]:
        name = a["name"]
        if not name.startswith("borealis-node-"):
            continue
        if (not re.fullmatch(r"borealis-node-[a-z0-9][a-z0-9.-]{0,160}", name) or name in assets
                or type(a["id"]) is not int or a["id"] <= 0 or a["id"] in ids or a["state"] != "uploaded"
                or a["url"] != api + "/releases/assets/" + str(a["id"])
                or a["browser_download_url"] != f"https://github.com/{repo}/releases/download/{tag}/{name}"
                or type(a["size"]) is not int or not 0 < a["size"] < 2 << 30
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", a["digest"])):
            raise ValueError("invalid or ambiguous immutable release asset")
        assets[name] = a
        ids.add(a["id"])
    if bootstrap.MANIFEST_NAME not in assets or bootstrap.BUNDLE_NAME not in assets:
        raise ValueError("missing immutable bootstrap publication")
    return assets


def retain(repo, tag, sha, references, seed=None, token=""):
    assets = release_assets(repo, tag, sha, token)
    cache = configured_store()
    catalog = {"repository": repo, "release": tag, "source_sha": sha, "assets": {
        name: {"sha256": a["digest"][7:], "bytes": a["size"]} for name, a in assets.items()}}
    raw = (json.dumps(catalog, sort_keys=True, separators=(",", ":")) + "\n").encode()
    identity = "release-" + hashlib.sha256(raw).hexdigest()
    references = references.resolve(strict=True)
    destination = references / identity
    local = {}
    if seed:
        for path in seed.resolve(strict=True).rglob("borealis-node-*"):
            if path.name in assets:
                if path.is_symlink() or not path.is_file() or path.name in local:
                    raise ValueError("ambiguous local seed asset")
                local[path.name] = path
    with tempfile.TemporaryDirectory(dir=references, prefix=".acquire-") as tmp:
        stage = Path(tmp) / "release"
        stage.mkdir(mode=0o700)
        for name, asset in sorted(assets.items()):
            def acquire(output, name=name, asset=asset):
                if name in local:
                    with regular_reader(local[name], asset["size"]) as reader, output.open("xb") as writer:
                        shutil.copyfileobj(reader, writer)
                else:
                    download(asset, output, token)
            cache.materialize(asset["digest"][7:], asset["size"], stage / name, acquire, retain=True)
        with (stage / "release.json").open("xb") as index:
            index.write(raw)
            index.flush()
            os.fsync(index.fileno())
        try:
            stage.rename(destination)
        except OSError:
            if destination.is_symlink() or (destination / "release.json").is_symlink() or (destination / "release.json").read_bytes() != raw:
                raise
            if {p.name for p in destination.iterdir()} != set(assets) | {"release.json"}:
                raise ValueError("existing release is incomplete")
            for name in assets:
                if (destination / name).is_symlink() or not (destination / name).samefile(stage / name):
                    raise ValueError("retained release reference changed")
        fd = os.open(references, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", default="bunny-lab-io/Borealis")
    parser.add_argument("--release", required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--references", type=Path, required=True)
    parser.add_argument("--local-assets", type=Path)
    args = parser.parse_args()
    try:
        # This trusted acquisition process never executes code from the selected source.
        print(retain(args.repository, args.release, args.source_sha, args.references,
                     args.local_assets, os.environ.get("GITHUB_TOKEN", "")))
    except (OSError, ValueError, KeyError) as exc:
        # Network exceptions can contain signed redirect URLs.
        raise SystemExit("Release acquisition failed (" + type(exc).__name__ + ")") from None


if __name__ == "__main__":
    main()
