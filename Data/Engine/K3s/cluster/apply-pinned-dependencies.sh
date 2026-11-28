#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
lock_file="${script_dir}/dependencies.lock"
kubectl_bin="${BOREALIS_KUBECTL_BIN:-k3s kubectl}"
repo_root="$(cd -- "${script_dir}/../../../.." && pwd)"
resource_source="${repo_root}/Engine/Deploy/system-upgrade-resources.json"
cert_resource_source="${repo_root}/Engine/Deploy/cert-manager-controller-resources.json"
webhook_resource_source="${repo_root}/Engine/Deploy/cert-manager-webhook-resources.json"
cainjector_resource_source="${repo_root}/Engine/Deploy/cert-manager-cainjector-resources.json"
manager_resource_source="${repo_root}/Engine/Deploy/longhorn-manager-resources.json"
prepull_resource_source="${repo_root}/Engine/Deploy/longhorn-pre-pull-resources.json"
ui_resource_source="${repo_root}/Engine/Deploy/longhorn-ui-resources.json"
driver_resource_source="${repo_root}/Engine/Deploy/longhorn-driver-deployer-resources.json"
wait_resource_source="${repo_root}/Engine/Deploy/longhorn-driver-wait-resources.json"
instance_resource_source="${repo_root}/Engine/Deploy/longhorn-instance-manager-resources.json"
instance_renderer="${script_dir}/render-longhorn-instance-manager.py"
resource_renderer="${script_dir}/render-system-upgrade.py"
# Validate before downloading or applying any dependency; caller may freeze an
# earlier workflow observation. Never source configuration as shell code.
upgrade_resources="$(python3 "${resource_renderer}" config "${resource_source}")"
cert_resources="$(python3 "${resource_renderer}" config "${cert_resource_source}")"
webhook_resources="$(python3 "${resource_renderer}" config "${webhook_resource_source}")"
cainjector_resources="$(python3 "${resource_renderer}" config "${cainjector_resource_source}")"
manager_resources="$(python3 "${resource_renderer}" config "${manager_resource_source}")"
prepull_resources="$(python3 "${resource_renderer}" config "${prepull_resource_source}")"
ui_resources="$(python3 "${resource_renderer}" config "${ui_resource_source}")"
driver_resources="$(python3 "${resource_renderer}" config "${driver_resource_source}")"
wait_resources="$(python3 "${resource_renderer}" config "${wait_resource_source}")"
instance_resources="$(python3 "${instance_renderer}" config "${instance_resource_source}")"
[[ "$#" -eq 0 || ( "$#" -eq 10 && "$1" == "${upgrade_resources}" && "$2" == "${cert_resources}" && "$3" == "${webhook_resources}" && "$4" == "${cainjector_resources}" && "$5" == "${manager_resources}" && "$6" == "${prepull_resources}" && "$7" == "${ui_resources}" && "$8" == "${driver_resources}" && "$9" == "${wait_resources}" && "${10}" == "${instance_resources}" ) ]] || {
  printf 'Controller resource source changed before dependency apply.\n' >&2
  exit 1
}
download_dir="$(mktemp -d)"

cleanup() {
  find "${download_dir}" -mindepth 1 -maxdepth 1 -type f -delete
  rmdir "${download_dir}"
}
trap cleanup EXIT

# Render only frozen inputs; live files are rechecked before every apply.
printf '%s' "${instance_resources}" > "${download_dir}/instance-resources.json"
python3 "${instance_renderer}" policy "${download_dir}/instance-resources.json" > "${download_dir}/instance-policy.json"
python3 "${instance_renderer}" probe "${download_dir}/instance-resources.json" > "${download_dir}/instance-probe.json"

check_resource_sources() {
  current_upgrade_resources="$(python3 "${resource_renderer}" config "${resource_source}")"
  current_cert_resources="$(python3 "${resource_renderer}" config "${cert_resource_source}")"
  current_webhook_resources="$(python3 "${resource_renderer}" config "${webhook_resource_source}")"
  current_cainjector_resources="$(python3 "${resource_renderer}" config "${cainjector_resource_source}")"
  current_manager_resources="$(python3 "${resource_renderer}" config "${manager_resource_source}")"
  current_prepull_resources="$(python3 "${resource_renderer}" config "${prepull_resource_source}")"
  current_ui_resources="$(python3 "${resource_renderer}" config "${ui_resource_source}")"
  current_driver_resources="$(python3 "${resource_renderer}" config "${driver_resource_source}")"
  current_wait_resources="$(python3 "${resource_renderer}" config "${wait_resource_source}")"
  current_instance_resources="$(python3 "${instance_renderer}" config "${instance_resource_source}")"
  [[ "${current_upgrade_resources}" == "${upgrade_resources}" && "${current_cert_resources}" == "${cert_resources}" && "${current_webhook_resources}" == "${webhook_resources}" && "${current_cainjector_resources}" == "${cainjector_resources}" && "${current_manager_resources}" == "${manager_resources}" && "${current_prepull_resources}" == "${prepull_resources}" && "${current_ui_resources}" == "${ui_resources}" && "${current_driver_resources}" == "${driver_resources}" && "${current_wait_resources}" == "${wait_resources}" && "${current_instance_resources}" == "${instance_resources}" ]] || {
    printf 'Controller resource source changed during dependency preparation.\n' >&2
    exit 1
  }
}

verify_instance_admission() {
  # API persistence does not imply admission caches have loaded the binding.
  # Use the real producer identity and CREATE admission without storing a Pod.
  for attempt in {1..15}; do
    check_resource_sources
    if ${kubectl_bin} --request-timeout=5s --as=system:serviceaccount:longhorn-system:longhorn-service-account \
      create --dry-run=server -f "${download_dir}/instance-probe.json" -o json \
      > "${download_dir}/instance-probe-result.json" 2>/dev/null \
      && python3 "${instance_renderer}" verify-probe "${download_dir}/instance-resources.json" \
        < "${download_dir}/instance-probe-result.json" >/dev/null 2>&1; then
      return 0
    fi
    [[ "${attempt}" -lt 15 ]] || {
      printf 'Instance-manager admission did not return frozen bounds; Longhorn apply blocked.\n' >&2
      return 1
    }
    sleep 2
  done
}

apply_manifest() {
  name="$1"
  version="$2"
  url="$3"
  expected_sha="$4"
  target="${download_dir}/${name}-${version}"
  curl -fsSL --proto '=https' --tlsv1.2 "${url}" -o "${target}"
  printf '%s  %s\n' "${expected_sha}" "${target}" | sha256sum -c - >/dev/null
  if [[ "${name}" == "system-upgrade-controller" ]]; then
    # Bound the first applied Deployment; never start the upstream unbounded Pod.
    python3 "${resource_renderer}" manifest "${target}" "${upgrade_resources}" > "${target}.bounded"
    target="${target}.bounded"
  elif [[ "${name}" == "cert-manager" ]]; then
    python3 "${resource_renderer}" cert-manager-manifest "${target}" "${cert_resources}" "${webhook_resources}" "${cainjector_resources}" > "${target}.bounded"
    target="${target}.bounded"
  elif [[ "${name}" == "longhorn" ]]; then
    python3 "${resource_renderer}" longhorn-manifest "${target}" "${manager_resources}" "${prepull_resources}" "${ui_resources}" "${driver_resources}" "${wait_resources}" > "${target}.bounded"
    target="${target}.bounded"
  fi
  check_resource_sources
  case "${name}" in
    kube-vip-sbom)
      # Attestation checksum verifies release identity. Borealis-owned kube-vip
      # manifest pins same release image and is applied with cluster foundation.
      ;;
    longhorn)
      ${kubectl_bin} apply --server-side --field-manager=borealis-cluster-bootstrap -f "${download_dir}/instance-policy.json"
      verify_instance_admission
      check_resource_sources
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
