import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("build_node_bootstrap_assets", ROOT / "Tests/tools/build_node_bootstrap_assets.py")
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)
spec = importlib.util.spec_from_file_location("node_images", ROOT / "Tests/tools/build_node_image_assets.py")
images = importlib.util.module_from_spec(spec)
with mock.patch.dict(sys.modules, {"build_node_bootstrap_assets": bootstrap}):
    spec.loader.exec_module(images)


def oci_fixture(destination, role, sha, corrupt=False):
    layer = io.BytesIO()
    with tarfile.open(fileobj=layer, mode="w") as archive:
        header = tarfile.TarInfo("example")
        header.size = 4
        archive.addfile(header, io.BytesIO(b"data"))
    digest = lambda raw: "sha256:" + hashlib.sha256(raw).hexdigest()
    blobs = {}
    def descriptor(raw, kind):
        key = digest(raw)
        blobs["blobs/sha256/" + key[7:]] = raw
        return dict(mediaType="application/vnd.oci.image." + kind, digest=key, size=len(raw))
    config = json.dumps(dict(architecture="amd64", os="linux", config={"Labels": {
        "org.opencontainers.image.revision": sha, "io.borealis.service": role}},
        rootfs=dict(type="layers", diff_ids=[digest(layer.getvalue())]))).encode()
    manifest = json.dumps(dict(schemaVersion=2, mediaType="application/vnd.oci.image.manifest.v1+json",
        config=descriptor(config, "config.v1+json"),
        layers=[descriptor(gzip.compress(layer.getvalue(), mtime=0), "layer.v1.tar+gzip")])).encode()
    blobs["index.json"] = json.dumps(dict(schemaVersion=2, manifests=[descriptor(manifest, "manifest.v1+json")])).encode()
    blobs["oci-layout"] = b'{"imageLayoutVersion":"1.0.0"}'
    if corrupt:
        blobs[next(name for name in blobs if name.startswith("blobs/"))] = b"changed"
    with tarfile.open(destination, "w") as archive:
        for name, raw in blobs.items():
            header = tarfile.TarInfo(name)
            header.size = len(raw)
            archive.addfile(header, io.BytesIO(raw))


class NodeImageAssetsTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.compiled = tempfile.TemporaryDirectory()
        cls.verifier = Path(cls.compiled.name) / "node-manager"
        env = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local")
        subprocess.run([os.environ.get("BOREALIS_GO_BIN", "go"), "-C",
                        str(ROOT / "Data/Engine/Containers/api-backend"), "build",
                        "-o", str(cls.verifier), "./cmd/borealis-node-manager"],
                       env=env, check=True, timeout=120, capture_output=True)

    @classmethod
    def tearDownClass(cls):
        cls.compiled.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / "source"
        self.source.mkdir()
        bootstrap.git(self.source, "init", "--quiet", "--template=")
        bootstrap.git(self.source, "config", "user.name", "Image fixture")
        bootstrap.git(self.source, "config", "user.email", "fixture@example.invalid")
        manifest = self.source / "Data/Engine/Containers/build-manifest.json"
        manifest.parent.mkdir(parents=True)
        manifest.write_text(json.dumps({"services": {role: dict(dockerfile="Dockerfile", context=".") for role in images.ROLES}}))
        (self.source / "Dockerfile").write_text("FROM scratch\n")
        bootstrap.git(self.source, "add", ".")
        bootstrap.git(self.source, "commit", "--quiet", "-m", "fixture")
        self.sha = bootstrap.git(self.source, "rev-parse", "HEAD")
        self.release = "2026.09.999-rc.1"
        bootstrap.git(self.source, "tag", self.release)
        (self.source / "operator-private").write_text("untracked fixture")
        self.builds, self.removed = [], []
        self.real_run = images.run

    def build(self, corrupt=False, fail_role=None):
        def command(args, **kwargs):
            if args[0] != "fixture-docker":
                return self.real_run(args, **kwargs)
            if args[1:3] == ["buildx", "create"]:
                self.assertNotIn("--use", args)
            elif args[1:3] == ["buildx", "rm"]:
                self.removed.append(args[-1])
            else:
                self.assertEqual(args[1:3], ["buildx", "build"])
                self.assertIn("--provenance=false", args)
                self.assertEqual(args[args.index("--platform") + 1], "linux/amd64")
                context = Path(args[-1])
                self.assertNotEqual(context, self.source)
                self.assertFalse((context / "operator-private").exists())
                reference = args[args.index("--tag") + 1]
                role = reference.split("/")[-1].split(":")[0]
                self.builds.append(role)
                if role == fail_role:
                    raise RuntimeError("fixture build failure")
                target = Path(args[args.index("--output") + 1].split("dest=", 1)[1])
                oci_fixture(target, role, self.sha, corrupt)
        with mock.patch.object(images, "run", side_effect=command), mock.patch.object(images, "build_runtime", return_value=self.verifier):
            return images.build_assets(source=self.source, release=self.release, source_sha=self.sha,
                repository="bunny-lab-io/Borealis", output_dir=self.root / "assets",
                go_bin="unused-fixture", docker="fixture-docker")

    def test_all_roles_verified_by_real_go_consumer_from_exact_checkout(self):
        result = self.build()
        self.assertEqual(self.builds, list(images.ROLES))
        self.assertEqual(len(self.removed), 1)
        self.assertEqual([p["role"] for p in result["images"]], list(images.ROLES))
        for proof in result["images"]:
            archive = self.root / "assets" / f'borealis-node-image-{proof["role"]}-linux-amd64.oci.tar'
            self.assertEqual(hashlib.sha256(archive.read_bytes()).hexdigest(), proof["archive_sha256"])
            self.assertEqual(proof["layers"][0]["file_bytes"], 4)
        self.assertEqual(result, json.loads((self.root / "assets" / images.INVENTORY).read_text()))
        with self.assertRaises(FileExistsError):
            self.build()
        self.assertTrue((self.root / "assets" / images.INVENTORY).exists())

    def test_corrupt_export_cannot_publish_inventory(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.build(corrupt=True)
        self.assertFalse((self.root / "assets").exists())
        self.assertEqual(len(self.removed), 1)

    def test_partial_build_cleans_only_its_artifacts_and_builder(self):
        with self.assertRaises(RuntimeError):
            self.build(fail_role="job-scheduler")
        self.assertFalse((self.root / "assets").exists())
        self.assertTrue((self.source / "operator-private").exists())
        self.assertEqual(len(self.removed), 1)


if __name__ == "__main__":
    unittest.main()
