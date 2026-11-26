"""Select unchanged, explicitly retained build inputs from an immutable release."""
import hashlib
import json
from pathlib import Path
import re
import shutil

import build_node_bootstrap_assets as bootstrap
import build_node_external_assets as external
import build_node_host_package_assets as packages
import build_node_image_assets as images
import build_node_k3s_assets as k3s
from node_artifact_cache import regular_reader


class RetainedRelease:
    def __init__(self, directory, repo, git_source, revision):
        self.directory = directory.resolve(strict=True)
        index = self.directory / "release.json"
        if directory.is_symlink() or index.is_symlink() or index.stat().st_size > 128 << 10:
            raise ValueError("invalid retained release catalog")
        raw = index.read_bytes()
        if self.directory.name != "release-" + hashlib.sha256(raw).hexdigest():
            raise ValueError("retained release identity mismatch")
        self.record = json.loads(raw, object_pairs_hook=images.unique_object)
        self.base = self.record["source_sha"]
        if (not bootstrap.SHA.fullmatch(self.base) or not bootstrap.RELEASE.fullmatch(self.record["release"])
                or self.record["repository"] != repo):
            raise ValueError("retained release source mismatch")
        self.original = self.tree(git_source, self.base)
        self.current = self.tree(git_source, revision)

    @staticmethod
    def tree(source, sha):
        entries = bootstrap.git(source, "ls-tree", "-rz", "--full-tree", sha)
        result = {}
        for entry in filter(None, entries.split("\0")):
            obj, name = entry.split("\t", 1)
            result[name] = obj
        return result

    def inputs(self, paths, tree):
        # Git ls-tree includes the file mode and blob ID. Include additions
        # and removals and the selected paths themselves in the fingerprint.
        entries = sorted((name, obj) for name, obj in tree.items()
                         if any(name == path or name.startswith(path + "/") for path in paths))
        return hashlib.sha256(json.dumps([sorted(paths), entries], separators=(",", ":")).encode()).hexdigest()

    def unchanged(self, paths):
        return self.inputs(paths, self.original) == self.inputs(paths, self.current)

    def inventory(self, name):
        obj = self.record["assets"][name]
        if not 0 < obj["bytes"] <= 2 << 20:
            raise ValueError("retained inventory exceeds limit")
        with regular_reader(self.directory / name, obj["bytes"]) as source:
            raw = source.read()
        if hashlib.sha256(raw).hexdigest() != obj["sha256"]:
            raise ValueError("retained inventory digest mismatch")
        inv = json.loads(raw, object_pairs_hook=images.unique_object)
        if (inv["repository"] != self.record["repository"] or inv["release"] != self.record["release"]
                or inv["source_sha"] != self.base or inv["platform"] != bootstrap.PLATFORM):
            raise ValueError("retained inventory binding mismatch")
        return inv

    def copy(self, name, dst, digest, size):
        if self.record["assets"][name] != {"bytes": size, "sha256": digest}:
            raise ValueError("retained archive differs from published inventory")
        with regular_reader(self.directory / name, size) as source, (dst / name).open("xb") as out:
            shutil.copyfileobj(source, out, 1 << 20)
        if bootstrap.digest(dst / name) != digest:
            raise ValueError("retained archive content mismatch")

    def role_inputs(self, snapshot, item):
        context = Path(item["context"])
        paths = {item["dockerfile"], "Data/Engine/Containers/build-manifest.json",
                 str(context / ".dockerignore"), item["dockerfile"] + ".dockerignore"}
        for pattern in item["inputs"]:
            path = pattern.removesuffix("/**")
            if not re.fullmatch(r"[A-Za-z0-9_./-]+", path) or ".." in Path(path).parts:
                return None
            paths.add(path)
        # Unknown context reads force a build. Generated Go binaries depend
        # on the entire module, including embedded files and the toolchain pin.
        dockerfile = images.source_path(snapshot, item["dockerfile"]).read_text()
        if "\\\n" in dockerfile:
            dockerfile = dockerfile.replace("\\\n", " ")
        for line in dockerfile.splitlines():
            parts = line.strip().split()
            if re.match(r"^\s*#\s*(syntax|escape|check)\s*=", line, re.I):
                return None
            if not parts or parts[0].startswith("#"):
                continue
            verb = parts[0].upper()
            if verb == "ADD" or "--mount" in line or "<<" in line or verb == "ONBUILD":
                return None
            if verb != "COPY":
                continue
            args = parts[1:]
            if args and args[0] == "--from=build" and re.search(r"(?im)^FROM\s+\S+\s+AS\s+build\s*$", dockerfile):
                continue
            if args and re.fullmatch(r"--chmod=[0-7]+", args[0]):
                args = args[1:]
            if len(args) < 2:
                return None
            for src in args[:-1]:
                if not re.fullmatch(r"[A-Za-z0-9_./-]+", src) or src.startswith(("/", "-")) or ".." in Path(src).parts:
                    return None
                path = (context / src).as_posix()
                if path.startswith("Data/Engine/Containers/api-backend/dist/"):
                    paths.add("Data/Engine/Containers/api-backend")
                elif path == ".":
                    return None
                else:
                    paths.add(path)
        return paths

    def dependency_family(self, family, snapshot, sha, label, dst):
        if family == "external":
            lock = json.loads((snapshot / external.pins.LOCK).read_bytes())
            external.pins.validate_pin_sources(snapshot, lock)
            if not self.unchanged([external.pins.LOCK]):
                return None
            name = external.INVENTORY
            inv = self.inventory(name)
            pins = {i["reference"] for c in lock["components"] for i in c["images"]}
            if pins != {i["reference"] for i in inv["images"]} or len(pins) != len(inv["images"]):
                raise ValueError("retained external reference set mismatch")
            blobs = [(external.asset_name(i["reference"]), i["archive_sha256"], i["archive_bytes"]) for i in inv["images"]]
        elif family == "k3s":
            if not self.unchanged([k3s.LOCK, "Data/Engine/release-manifest.json"]):
                return None
            name = k3s.INVENTORY
            inv = self.inventory(name)
            blobs = [(k3s.BINARY, inv["binary"]["sha256"], inv["binary"]["size"]),
                     (k3s.INSTALLER, inv["installer"]["sha256"], inv["installer"]["size"]),
                     (k3s.ARCHIVE, inv["archive"]["archive_sha256"], inv["archive"]["archive_bytes"])]
        elif family == "host_package":
            if not self.unchanged([packages.LOCK]):
                return None
            name = packages.INVENTORY
            inv = self.inventory(name)
            blobs = [(packages.ARCHIVE, inv["archive"]["sha256"], inv["archive"]["size"])]
        else:
            return None
        dst.mkdir()
        for filename, digest, size in blobs:
            self.copy(filename, dst, digest, size)
        inv.update(source_sha=sha, release=label)
        (dst / name).write_text(json.dumps(inv, sort_keys=True, separators=(",", ":")) + "\n")
        return inv
