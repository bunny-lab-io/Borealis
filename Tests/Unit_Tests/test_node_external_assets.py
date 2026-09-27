import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "Tests/tools"))
import build_node_external_assets as assets
import node_registry_download as registry
sys.path.pop(0)


class Response(io.BytesIO):
    def __init__(self, data, **headers):
        super().__init__(data)
        self.status, self.headers = 200, headers


class RegistryDownloadTests(unittest.TestCase):
    def test_public_token_scope_and_redirect_strip_authorization(self):
        body = b"authenticated public bytes"
        digest = "sha256:" + hashlib.sha256(body).hexdigest()
        reader = registry.RegistryReader("ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0")
        requests = []

        def open_request(request, **kwargs):
            requests.append(request)
            if len(requests) == 1:
                raise urllib.error.HTTPError(request.full_url, 401, "private", {}, None)
            if len(requests) == 2:
                self.assertTrue(request.full_url.startswith("https://ghcr.io/token?"))
                self.assertIn("repository%3Acloudnative-pg%2Fcloudnative-pg%3Apull", request.full_url)
                self.assertIsNone(request.get_header("Authorization"))
                return Response(b'{"token":"fixture-sensitive-token"}')
            if len(requests) == 3:
                self.assertEqual(request.get_header("Authorization"), "Bearer fixture-sensitive-token")
                raise urllib.error.HTTPError(request.full_url, 307, "", {"Location": "https://pkg-containers.githubusercontent.com/blob?signed=private"}, None)
            self.assertIsNone(request.get_header("Authorization"))
            self.assertIsNone(request.get_header("Cookie"))
            return Response(body, **{"Content-Length": str(len(body))})

        reader.opener = mock.Mock(open=open_request)
        with tempfile.TemporaryDirectory() as temporary:
            destination = Path(temporary) / "blob"
            reader.fetch(digest, len(body), destination)
            self.assertEqual(destination.read_bytes(), body)
            with self.assertRaisesRegex(ValueError, "^pinned public registry download failed$"):
                reader.fetch(digest, len(body), destination)
            self.assertEqual(destination.read_bytes(), body)
        reader.close()
        self.assertIsNone(reader.token)

    def test_download_bounds_digest_errors_and_redirect_allowlist(self):
        body = b"source pinned bytes"
        digest = "sha256:" + hashlib.sha256(body).hexdigest()
        for mode in ("short", "long", "digest", "length", "encoding", "private error", "foreign redirect"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                reader = registry.RegistryReader("quay.io/jetstack/cert-manager-webhook:v1.21.1")
                response = Response(body[:-1] if mode == "short" else body + b"x" if mode == "long" else body)
                if mode == "length": response.headers["Content-Length"] = "999"
                if mode == "encoding": response.headers["Content-Encoding"] = "gzip"
                reader.opener = mock.Mock()
                reader.opener.open.return_value = response
                if mode == "private error": reader.opener.open.side_effect = RuntimeError("private-token signed-url")
                if mode == "foreign redirect": reader.opener.open.side_effect = urllib.error.HTTPError("https://quay.io/", 302, "", {"Location": "https://127.0.0.1/private"}, None)
                with self.assertRaisesRegex(ValueError, "^pinned public registry download failed$"):
                    reader.fetch("sha256:" + "a" * 64 if mode == "digest" else digest, len(body), Path(temporary) / "blob")
                reader.close()
        for url in ("http://cdn01.quay.io/blob", "https://cdn01.quay.io.evil/blob", "https://user@cdn01.quay.io/blob", "https://cdn01.quay.io:444/blob", "https://cdn01.quay.io/blob#fragment"):
            self.assertFalse(registry.allowed_redirect("quay.io", url))
        self.assertTrue(registry.allowed_redirect("quay.io", "https://cdn01.quay.io/blob?signature=fixture"))
        self.assertTrue(registry.allowed_redirect("docker.io", "https://production.cloudfront.docker.com/blob?signature=fixture"))
        self.assertFalse(registry.allowed_redirect("docker.io", "https://production.cloudfront.docker.com.evil/blob"))
        with self.assertRaises(ValueError):
            registry.read_bounded(Response(b"x"), 1, 0, lambda _: self.fail("read after deadline"))


class ExternalAssetsTests(unittest.TestCase):
    def test_exact_source_complete_set_cleanup_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "source"
            source.mkdir()
            git = assets.bootstrap.git
            git(source, "init", "--quiet", "--template=")
            git(source, "config", "user.name", "External image fixture")
            git(source, "config", "user.email", "fixture@example.invalid")
            paths = [assets.pins.LOCK, "Data/Engine/K3s/cluster/dependencies.lock",
                     "Data/Engine/K3s/cluster/kube-vip.yaml.in", "Data/Engine/K3s/cluster/snapshot-controller.yaml",
                     "Data/Engine/K3s/cluster/run-probe-conformance.sh"]
            for name in paths:
                target = source / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes((ROOT / name).read_bytes())
            git(source, "add", ".")
            git(source, "commit", "--quiet", "-m", "fixture")
            sha = git(source, "rev-parse", "HEAD")
            release = "2026.09.999-rc.1"
            git(source, "tag", release)
            (source / "operator-private").write_text("not in tagged source")
            observed = []

            def compiler(snapshot, destination, go_bin):
                self.assertNotEqual(snapshot, source)
                self.assertFalse((snapshot / "operator-private").exists())
                # Producer orchestration fixture only. Native Go tests and real
                # exact-source packaging separately verify archive semantics.
                destination.write_text("#!/usr/bin/env python3\nimport json,sys\nassert sys.argv[1]=='external-image-proof'\nprint(json.dumps({'reference':sys.argv[-1]}))\n")
                destination.chmod(0o700)

            def assemble(pin, cache, destination):
                observed.append(pin["reference"])
                destination.write_bytes(b"producer fixture")

            kwargs = dict(source=source, release=release, source_sha=sha, repository="bunny-lab-io/Borealis", output_dir=root / "assets", go_bin="fixture")
            with mock.patch.object(assets.bootstrap, "build_manager", side_effect=compiler), mock.patch.object(assets, "assemble_archive", side_effect=assemble):
                result = assets.build_assets(**kwargs)
                self.assertEqual(len(observed), 23)
                self.assertEqual(observed, sorted(set(observed)))
                self.assertEqual(result["source_sha"], sha)
                self.assertEqual(len(list(kwargs["output_dir"].glob("*.oci.tar"))), 23)
                with self.assertRaises(FileExistsError): assets.build_assets(**kwargs)
                self.assertTrue((kwargs["output_dir"] / assets.INVENTORY).exists())
            kwargs["output_dir"] = root / "failed"
            with mock.patch.object(assets.bootstrap, "build_manager", side_effect=compiler), mock.patch.object(assets, "assemble_archive", side_effect=ValueError("failure")):
                with self.assertRaises(ValueError): assets.build_assets(**kwargs)
            self.assertFalse(kwargs["output_dir"].exists())


if __name__ == "__main__":
    unittest.main()
