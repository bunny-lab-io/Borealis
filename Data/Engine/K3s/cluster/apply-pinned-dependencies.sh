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
    *)
      ${kubectl_bin} apply --server-side --field-manager=borealis-cluster-bootstrap -f "${target}"
      ;;
  esac
}

while IFS='|' read -r name version url expected_sha; do
  [[ -n "${name}" && "${name:0:1}" != "#" ]] || continue
  [[ "${expected_sha}" =~ ^[0-9a-f]{64}$ ]] || {
    printf 'Invalid checksum for %s %s.\n' "${name}" "${version}" >&2
    exit 64
  }
  apply_manifest "${name}" "${version}" "${url}" "${expected_sha}"
done < "${lock_file}"
