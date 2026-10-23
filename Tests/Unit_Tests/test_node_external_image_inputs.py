import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("external_inputs", ROOT / "Tests/tools/node_external_image_inputs.py")
inputs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inputs)


def workload(image, **container):
    return {"kind": "Deployment", "spec": {"template": {"spec": {
        "containers": [dict(name="controller", image=image, **container)]}}}}


def fixture_manifests():
    names = ["manager", "engine", "instance", "share", "backing", "support", "ui"]
    refs = {name: "docker.io/longhornio/" + name + ":v1.12.0" for name in names}
    flags = sorted(inputs.IMAGE_FLAGS["longhorn"])
    args = [value for flag, name in zip(flags, names[1:]) for value in [flag, refs[name]]]
    env = [dict(name=name, value="docker.io/longhornio/csi-" + str(i) + ":v1")
           for i, name in enumerate(sorted(inputs.IMAGE_ENV))]
    longhorn = workload(refs["manager"], command=args, env=env)
    cert = workload("quay.io/jetstack/cert-manager-controller:v1", args=[
        "--acme-http01-solver-image=quay.io/jetstack/cert-manager-acmesolver:v1"])
    cert["spec"]["template"]["spec"]["initContainers"] = [
        {"name": "other", "image": "quay.io/jetstack/cert-manager-cainjector:v1"},
        {"name": "webhook", "image": "quay.io/jetstack/cert-manager-webhook:v1"}]
    return {
        "longhorn": [longhorn],
        "cert-manager": [cert],
        "cloudnative-pg": [workload("ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0", env=[
            dict(name="OPERATOR_IMAGE_NAME", value="ghcr.io/cloudnative-pg/cloudnative-pg:1.30.0")])],
        "system-upgrade-controller": [workload("rancher/system-upgrade-controller:v0.20.1"),
            {"kind": "ConfigMap", "data": {"SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE": "rancher/kubectl:v1.30.3",
                                         "SYSTEM_UPGRADE_JOB_KUBECTL_IMAGE_WINDOWS": ""}}],
    }


class ExternalImageInputTests(unittest.TestCase):
    def test_indirect_images_init_images_and_schema_exclusion(self):
        for component, objects in fixture_manifests().items():
            # Neither CRD image schemas nor arbitrary command URLs are images.
            objects.append({"kind": "CustomResourceDefinition", "spec": {"image": "not-a-runtime-image"}})
            result = inputs.manifest_images(yaml.safe_dump_all(objects + [None]).encode(), component)
            self.assertEqual(len(result), inputs.COMPONENTS[component])
            self.assertEqual(result, sorted(set(result)))
            self.assertTrue(all(not ref.startswith("rancher/") for ref in result))

    def test_incomplete_indirect_inputs_rejected(self):
        for mode in ("longhorn flag", "longhorn env", "solver", "kubectl", "foreign", "extra", "unknown flag", "unknown env", "bootstrap drift"):
            with self.subTest(mode=mode):
                objects = fixture_manifests()
                component = "longhorn"
                container = objects[component][0]["spec"]["template"]["spec"]["containers"][0]
                if mode == "longhorn flag":
                    container["command"] = container["command"][2:]
                elif mode == "longhorn env":
                    container["env"].pop()
                elif mode == "solver":
                    component = "cert-manager"
                    objects[component][0]["spec"]["template"]["spec"]["containers"][0]["args"] = []
                elif mode == "kubectl":
                    component = "system-upgrade-controller"
                    objects[component].pop()
                elif mode == "foreign":
                    container["image"] = "private.example/unknown:v1"
                elif mode == "unknown flag":
                    container["command"].append("--new-image=docker.io/longhornio/new:v1")
                elif mode == "unknown env":
                    container["env"].append(dict(name="NEW_IMAGE", value="docker.io/longhornio/new:v1"))
                elif mode == "bootstrap drift":
                    component = "cloudnative-pg"
                    objects[component][0]["spec"]["template"]["spec"]["containers"][0]["env"][0]["value"] = "other"
                else:
                    objects[component].append(workload("docker.io/longhornio/extra:v1"))
                with self.assertRaises(ValueError):
                    inputs.manifest_images(yaml.safe_dump_all(objects[component]).encode(), component)

    def test_catalog_hash_binding_static_pins_and_dynamic_postgres_boundary(self):
        with tempfile.TemporaryDirectory() as temporary:
            source = Path(temporary) / "source"
            cluster = source / "Data/Engine/K3s/cluster"
            cluster.mkdir(parents=True)
            manifests = Path(temporary) / "manifests"
            manifests.mkdir()
            lines = []
            for component, objects in fixture_manifests().items():
                raw = yaml.safe_dump_all(objects).encode()
                (manifests / (component + ".yaml")).write_bytes(raw)
                lines.append(component + "|v1|https://example.invalid/input|" + hashlib.sha256(raw).hexdigest())
            (source / inputs.LOCK).write_text("\n".join(lines))
            for component, name in (("kube-vip", "kube-vip.yaml.in"), ("snapshot-controller", "snapshot-controller.yaml"),
                                    ("probe", "run-probe-conformance.sh")):
                (cluster / name).write_bytes((ROOT / "Data/Engine/K3s/cluster" / name).read_bytes())
            result = inputs.collect(source, manifests)
            self.assertEqual(sum(len(c["images"]) for c in result["components"]), 23)
            self.assertEqual(result["dynamic_images"], ["source-postgresql-runtime"])
            self.assertEqual(result["excluded_operations"], ["k3s-version-upgrade"])
            self.assertFalse(any("/postgresql:" in image for c in result["components"] for image in c["images"]))
            changed = manifests / "cloudnative-pg.yaml"
            changed.write_bytes(changed.read_bytes() + b"\n# drift\n")
            with self.assertRaisesRegex(ValueError, "source pin"):
                inputs.collect(source, manifests)


if __name__ == "__main__":
    unittest.main()
