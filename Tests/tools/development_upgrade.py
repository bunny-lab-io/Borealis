"""Package the exact fast-forward Git range for an existing cluster member."""

import json
from pathlib import Path
import tempfile

import build_node_bootstrap_assets as bootstrap

FAMILY = "upgrade"
BUNDLE = "borealis-node-development-history.bundle"
REF = "refs/heads/borealis-candidate"


def package(source, snapshot, base_sha, source_sha, directory):
    if not bootstrap.SHA.fullmatch(base_sha) or base_sha == source_sha:
        raise ValueError("development upgrade requires a distinct exact baseline commit")
    bootstrap.git(source, "merge-base", "--is-ancestor", base_sha, source_sha)
    directory.mkdir()
    with tempfile.TemporaryDirectory(dir=directory.parent) as tmp:
        mirror = Path(tmp)
        # A private transient object database: no changes to the operator tree
        # or its refs. The single emitted ref contains only the selected range.
        bootstrap.git(mirror, "init", "--bare", "--quiet", "--template=")
        objects = Path(bootstrap.git(source, "rev-parse", "--path-format=absolute", "--git-path", "objects"))
        if not objects.is_dir() or any(c in str(objects) for c in "\r\n"):
            raise ValueError("invalid local Git object directory")
        (mirror / "objects/info/alternates").write_text(str(objects) + "\n")
        bootstrap.git(mirror, "update-ref", REF, source_sha)
        bootstrap.git(mirror, "-c", "pack.threads=1", "bundle", "create", str(directory / BUNDLE), REF, "^" + base_sha)
        heads = bootstrap.git(mirror, "bundle", "list-heads", str(directory / BUNDLE))
        if heads != source_sha + " " + REF:
            raise ValueError("development history has unexpected refs")
    bundle = directory / BUNDLE
    if not 0 < bundle.stat().st_size <= bootstrap.MAX_BUNDLE_BYTES:
        raise ValueError("development history exceeds the source transport limit")
    compat = (snapshot / "Data/Engine/release-manifest.json").read_bytes()
    if len(compat) > 16384:
        raise ValueError("invalid release compatibility manifest")
    return dict(base_sha=base_sha, compatibility=json.loads(compat))
