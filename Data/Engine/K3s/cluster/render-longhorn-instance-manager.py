#!/usr/bin/env python3
"""Render review-only instance-manager admission objects; never contact Kubernetes."""

import importlib.util
import json
import pathlib
import sys

sys.dont_write_bytecode = True
_HELPERS = importlib.util.spec_from_file_location(
    "controller_resources", pathlib.Path(__file__).with_name("render-system-upgrade.py")
)
_HELPERS_MODULE = importlib.util.module_from_spec(_HELPERS)
_HELPERS.loader.exec_module(_HELPERS_MODULE)
NAME = "borealis-longhorn-instance-manager-bounds"
NAMESPACE = "longhorn-system"
ACCOUNT = "longhorn-service-account"


def resources(raw):
    value = json.loads(raw, object_pairs_hook=_HELPERS_MODULE.unique_object)
    if (not isinstance(value, dict) or set(value) != {"requests", "limits"}
            or not isinstance(value["requests"], dict) or set(value["requests"]) != {"memory"}
            or not isinstance(value["limits"], dict) or set(value["limits"]) != {"cpu", "memory"}):
        raise ValueError("native CPU request must be preserved")
    _HELPERS_MODULE.cpu_milli(value["limits"]["cpu"])
    if _HELPERS_MODULE.memory_bytes(value["requests"]["memory"]) > _HELPERS_MODULE.memory_bytes(value["limits"]["memory"]):
        raise ValueError("memory request exceeds cap")
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def read_resources(path):
    with pathlib.Path(path).open("rb") as stream:
        raw = stream.read(4097)
    if len(raw) > 4096:
        raise ValueError("resource configuration exceeds bound")
    return resources(raw)


def render(bounds):
    value = json.loads(resources(bounds))
    memory = json.dumps(value["requests"]["memory"])
    limits = json.dumps(value["limits"], sort_keys=True)
    cpu_cap = json.dumps(value["limits"]["cpu"])
    memory_cap = json.dumps(value["limits"]["memory"])
    # Scope selects the producer's role, not its current resource shape. Unsupported
    # selected Pods must fail, rather than silently escape mutation via a selector.
    supported = (
        'object.spec.serviceAccountName == "longhorn-service-account" && '
        'object.metadata.labels["longhorn.io/data-engine"] == "v1" && '
        'object.metadata.labels["longhorn.io/instance-manager-type"] == "aio" && '
        'object.spec.containers.size() == 1 && '
        'object.spec.containers[0].name == "instance-manager" && '
        '(!has(object.spec.initContainers) || object.spec.initContainers.size() == 0) && '
        '(!has(object.spec.ephemeralContainers) || object.spec.ephemeralContainers.size() == 0) && '
        '!has(object.spec.resources) && !has(object.spec.overhead) && '
        '(!has(object.spec.resourceClaims) || object.spec.resourceClaims.size() == 0) && '
        '!has(object.spec.runtimeClassName) && '
        '(!has(object.spec.containers[0].resizePolicy) || object.spec.containers[0].resizePolicy.size() == 0) && '
        '!has(object.spec.containers[0].restartPolicy) && '
        'has(object.spec.containers[0].resources) && '
        'has(object.spec.containers[0].resources.requests) && '
        '"cpu" in object.spec.containers[0].resources.requests && '
        'object.spec.containers[0].resources.requests["cpu"].matches("^[1-9][0-9]*m?$") && '
        f'quantity(object.spec.containers[0].resources.requests["cpu"]).compareTo(quantity({cpu_cap})) <= 0 && '
        'object.spec.containers[0].resources.requests.all(k, k in ["cpu", "memory"]) && '
        f'(!("memory" in object.spec.containers[0].resources.requests) || quantity(object.spec.containers[0].resources.requests["memory"]).compareTo(quantity({memory})) == 0) && '
        '(!has(object.spec.containers[0].resources.limits) || ('
        'object.spec.containers[0].resources.limits.size() == 2 && '
        '"cpu" in object.spec.containers[0].resources.limits && '
        '"memory" in object.spec.containers[0].resources.limits && '
        f'quantity(object.spec.containers[0].resources.limits["cpu"]).compareTo(quantity({cpu_cap})) == 0 && '
        f'quantity(object.spec.containers[0].resources.limits["memory"]).compareTo(quantity({memory_cap})) == 0))'
    )
    expression = (
        'variables.supported ? [\n'
        f'  JSONPatch{{op: "add", path: "/spec/containers/0/resources/requests/memory", value: {memory}}},\n'
        f'  JSONPatch{{op: "add", path: "/spec/containers/0/resources/limits", value: {limits}}}\n'
        '] : [JSONPatch{op: "test", path: "/spec/containers", value: []}]'
    )
    # The failure branch tests an impossible valid Pod shape. With failurePolicy
    # Fail, malformed selected requests are rejected, not admitted unbounded.
    policy = {
        "apiVersion": "admissionregistration.k8s.io/v1", "kind": "MutatingAdmissionPolicy",
        "metadata": {"name": NAME},
        "spec": {
            "failurePolicy": "Fail", "reinvocationPolicy": "IfNeeded",
            "matchConstraints": {
                "matchPolicy": "Exact",
                "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": NAMESPACE}},
                "objectSelector": {"matchLabels": {"longhorn.io/component": "instance-manager"}},
                "resourceRules": [{"apiGroups": [""], "apiVersions": ["v1"], "operations": ["CREATE"], "resources": ["pods"], "scope": "Namespaced"}],
            },
            "matchConditions": [{"name": "longhorn-producer", "expression": f'request.userInfo.username == "system:serviceaccount:{NAMESPACE}:{ACCOUNT}"'}],
            "variables": [{"name": "supported", "expression": supported}],
            "mutations": [{"patchType": "JSONPatch", "jsonPatch": {"expression": expression}}],
        },
    }
    binding = {
        "apiVersion": "admissionregistration.k8s.io/v1", "kind": "MutatingAdmissionPolicyBinding",
        "metadata": {"name": NAME},
        "spec": {"policyName": NAME, "matchResources": {"namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": NAMESPACE}}}},
    }
    return json.dumps({"apiVersion": "v1", "kind": "List", "items": [policy, binding]}, sort_keys=True, indent=2)


def main():
    try:
        if len(sys.argv) != 3 or sys.argv[1] not in {"config", "policy"}:
            raise ValueError("invalid arguments")
        bounds = read_resources(sys.argv[2])
        result = bounds if sys.argv[1] == "config" else render(bounds)
    except (ValueError, KeyError, TypeError, OSError, RecursionError):
        print("Instance-manager rendering failed: require valid memory request and CPU/memory caps; CPU request is owned by Longhorn.", file=sys.stderr)
        return 1
    sys.stdout.write(result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
