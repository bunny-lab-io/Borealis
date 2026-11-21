import hashlib
import copy
import importlib.util
import json
from pathlib import Path
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "Tests/tools"))
spec = importlib.util.spec_from_file_location("resolve_external", ROOT / "Tests/tools/resolve_node_external_images.py")
resolver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resolver)
sys.path.pop(0)


def fixture(mode="valid"):
    repository = "ghcr.io/kube-vip/kube-vip"
    manifest_type = "application/vnd.oci.image.manifest.v1+json"
    manifest = {"schemaVersion": 2, "mediaType": manifest_type,
                "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "sha256:" + "b" * 64, "size": 128},
                "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "sha256:" + "c" * 64, "size": 256}]}
    if mode == "foreign layer":
        manifest["layers"][0]["urls"] = ["https://untrusted.invalid/blob"]
    elif mode == "unsupported compression":
        manifest["layers"][0]["mediaType"] = "application/vnd.oci.image.layer.v1.tar+zstd"
    elif mode == "oversize":
        manifest["layers"][0]["size"] = 2 << 30
    elif mode == "float size":
        manifest["config"]["size"] = 128.0
    raw_manifest = json.dumps(manifest).encode()
    selected = {"mediaType": manifest_type, "digest": "sha256:" + hashlib.sha256(raw_manifest).hexdigest(),
                "size": len(raw_manifest), "platform": {"os": "linux", "architecture": "amd64"}}
    if mode == "wrong size":
        selected["size"] += 1
    elif mode == "missing platform":
        del selected["platform"]
    elif mode == "wrong architecture":
        selected["platform"]["architecture"] = "arm64"
    elif mode == "variant":
        selected["platform"]["variant"] = "v3"
    index = {"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": [selected]}
    if mode == "duplicate platform":
        index["manifests"].append(dict(selected))
    raw_index = json.dumps(index).encode()
    reference = repository + "@sha256:" + hashlib.sha256(raw_index).hexdigest()
    if mode == "changed pinned index":
        raw_index += b" "
    if mode == "changed manifest":
        raw_manifest += b" "
    if mode == "case alias":
        raw_index = raw_index.replace(b'"schemaVersion": 2,', b'"schemaVersion": 2, "SchemaVersion": 2,')
        reference = repository + "@sha256:" + hashlib.sha256(raw_index).hexdigest()
    reads = {reference: raw_index, repository + "@" + selected["digest"]: raw_manifest}
    return reference, reads


class ResolveExternalImagesTests(unittest.TestCase):
    def test_pin_exact_index_and_linux_manifest_bytes(self):
        ref, reads = fixture()
        calls = []

        def read(reference):
            calls.append(reference)
            return reads[reference]

        result = resolver.resolve_reference(ref, read)
        self.assertEqual(calls[0], ref)
        self.assertEqual(len(calls), 2)
        self.assertEqual(result["index_digest"], ref.split("@", 1)[1])
        self.assertEqual(result["manifest_digest"], calls[1].split("@", 1)[1])
        self.assertEqual(result["layer_blob_bytes"], 256)
        self.assertEqual(result["layer_count"], 1)
        self.assertNotIn("expanded_bytes", result)

    def test_reject_ambiguous_platform_changed_content_and_foreign_layers(self):
        for mode in ("foreign layer", "unsupported compression", "oversize", "float size", "wrong size", "missing platform",
                     "wrong architecture", "variant", "duplicate platform", "changed pinned index", "changed manifest", "case alias"):
            with self.subTest(mode=mode):
                ref, reads = fixture(mode)
                with self.assertRaises(ValueError):
                    resolver.resolve_reference(ref, reads.__getitem__)

    def test_no_network_for_foreign_reference(self):
        for ref in ("private.invalid/image:v1", "ghcr.io/cloudnative-pg/cloudnative-pg:latest?credential=private",
                    "docker.io/rancher/kubectl:v1\n", "http://registry.k8s.io/e2e-test-images/busybox"):
            with self.subTest(ref=ref), self.assertRaises(ValueError):
                resolver.resolve_reference(ref, lambda _: self.fail("network preceded validation"))

    def test_committed_pins_match_source_and_reject_stale_or_ambiguous_inputs(self):
        value = json.loads((ROOT / resolver.LOCK).read_text(), object_pairs_hook=resolver.unique_object)
        resolver.validate_pin_sources(ROOT, value)
        for mode in ("source hash", "component count", "digest", "type", "dynamic database", "unknown"):
            with self.subTest(mode=mode):
                changed = copy.deepcopy(value)
                if mode == "source hash":
                    changed["components"][0]["manifest_sha256"] = "a" * 64
                elif mode == "component count":
                    changed["components"].pop()
                elif mode == "digest":
                    changed["components"][0]["images"][0]["manifest_digest"] = "latest"
                elif mode == "type":
                    changed["components"][0]["images"][0]["layer_count"] = True
                elif mode == "dynamic database":
                    changed["dynamic_images"] = []
                else:
                    changed["components"][0]["images"][0]["expanded_bytes"] = 1
                with self.assertRaises(ValueError):
                    resolver.validate_pin_sources(ROOT, changed)


if __name__ == "__main__":
    unittest.main()
