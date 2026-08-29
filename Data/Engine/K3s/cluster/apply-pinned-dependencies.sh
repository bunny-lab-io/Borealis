#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
lock_file="${script_dir}/dependencies.lock"
kubectl_bin="${BOREALIS_KUBECTL_BIN:-k3s kubectl}"
download_dir="$(mktemp -d)"

cleanup() {
  find "${download_dir}" -mindepth 1 -maxdepth 1 -type f -delete
  rmdir "${download_dir}"
}
trap cleanup EXIT

apply_manifest() {
  name="$1"
  version="$2"
  url="$3"
  expected_sha="$4"
  target="${download_dir}/${name}-${version}"
  curl -fsSL --proto '=https' --tlsv1.2 "${url}" -o "${target}"
  printf '%s  %s\n' "${expected_sha}" "${target}" | sha256sum -c - >/dev/null
  case "${name}" in
    kube-vip-sbom)
      # Attestation checksum verifies release identity. Borealis-owned kube-vip
      # manifest pins same release image and is applied with cluster foundation.
      ;;
    longhorn)
      # Standalone Engine already owns Longhorn through client-side apply.
      # Preserve that ownership while converging to cluster-pinned manifest.
      ${kubectl_bin} apply -f "${target}"
      ;;
    *)
      ${kubectl_bin} apply --server-side --field-manager=borealis-cluster-bootstrap -f "${target}"
      ;;
  esac
}

wait_for_workload() {
  namespace="$1"
  resource="$2"
  for attempt in {1..150}; do
    if ${kubectl_bin} -n "${namespace}" get "${resource}" >/dev/null 2>&1; then
      return 0
    fi
    [[ "${attempt}" -lt 150 ]] || {
      printf 'Pinned dependency workload %s/%s was not created.\n' "${namespace}" "${resource}" >&2
      exit 1
    }
    sleep 2
  done
}

apply_dependency_probe_guards() {
  # Kubernetes issue 141155 can run liveness before startup after a container
  # restart. Delay stays longer than each dependency startup-probe budget.
  wait_for_workload cnpg-system deployment/cnpg-controller-manager
  ${kubectl_bin} -n cnpg-system patch deployment/cnpg-controller-manager --type=strategic -p \
    '{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"40"}},"spec":{"containers":[{"name":"manager","livenessProbe":{"initialDelaySeconds":40}}]}}}}'

  wait_for_workload longhorn-system daemonset/longhorn-csi-plugin
  ${kubectl_bin} -n longhorn-system patch daemonset/longhorn-csi-plugin --type=strategic -p \
    '{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"200"}},"spec":{"containers":[{"name":"longhorn-csi-plugin","livenessProbe":{"initialDelaySeconds":200}}]}}}}'
}

while IFS='|' read -r name version url expected_sha; do
  [[ -n "${name}" && "${name:0:1}" != "#" ]] || continue
  [[ "${expected_sha}" =~ ^[0-9a-f]{64}$ ]] || {
    printf 'Invalid checksum for %s %s.\n' "${name}" "${version}" >&2
    exit 64
  }
  apply_manifest "${name}" "${version}" "${url}" "${expected_sha}"
done < "${lock_file}"

apply_dependency_probe_guards
