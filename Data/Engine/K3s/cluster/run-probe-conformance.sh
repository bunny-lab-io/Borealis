#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

namespace="borealis-probe-conformance"
result_file="${BOREALIS_K3S_PROBE_CONFORMANCE_FILE:-/var/lib/rancher/k3s/server/borealis-probe-conformance.json}"
kubectl=(k3s kubectl)

version="$(k3s --version | awk 'NR == 1 {print $3}')"
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$ ]] || {
  printf 'Cluster mode requires stable K3s release; saw %s.\n' "${version}" >&2
  exit 1
}

cleanup() {
  "${kubectl[@]}" delete namespace "${namespace}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${kubectl[@]}" create namespace "${namespace}" >/dev/null
"${kubectl[@]}" apply -f - >/dev/null <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: startup-restart-conformance
  namespace: borealis-probe-conformance
spec:
  restartPolicy: Always
  containers:
    - name: probe
      image: registry.k8s.io/e2e-test-images/busybox@sha256:a9155b13325b2abef48e71de77bb8ac015412a566829f621d06bfae5c699b1b9
      command: ["/bin/sh", "-c", "rm -f /state/started; sleep 8; touch /state/started; exec sleep 3600"]
      volumeMounts:
        - {name: state, mountPath: /state}
      startupProbe:
        exec: {command: ["/bin/test", "-f", "/state/started"]}
        periodSeconds: 1
        failureThreshold: 20
      readinessProbe:
        exec: {command: ["/bin/test", "-f", "/state/started"]}
        periodSeconds: 1
        failureThreshold: 1
      livenessProbe:
        exec: {command: ["/bin/test", "-f", "/state/started"]}
        periodSeconds: 1
        failureThreshold: 1
  volumes:
    - name: state
      emptyDir: {}
EOF

"${kubectl[@]}" -n "${namespace}" wait --for=condition=Ready pod/startup-restart-conformance --timeout=60s >/dev/null
"${kubectl[@]}" -n "${namespace}" exec startup-restart-conformance -- kill -KILL 1 >/dev/null 2>&1 || true
sleep 12
restart_count="$("${kubectl[@]}" -n "${namespace}" get pod startup-restart-conformance -o jsonpath='{.status.containerStatuses[0].restartCount}')"
ready="$("${kubectl[@]}" -n "${namespace}" get pod startup-restart-conformance -o jsonpath='{.status.containerStatuses[0].ready}')"
[[ "${restart_count}" == "1" && "${ready}" == "true" ]] || {
  printf 'K3s startup/liveness restart conformance failed: restartCount=%s ready=%s. Cluster mode remains disabled.\n' "${restart_count}" "${ready}" >&2
  exit 1
}

install -d -m 0700 "$(dirname -- "${result_file}")"
temp_result="$(mktemp)"
printf '{"id":"pod-restart-policy-startup-probe-v1","status":"passed","k3s_version":"%s","tested_at":"%s"}\n' "${version}" "$(date -u +%FT%TZ)" > "${temp_result}"
install -m 0600 "${temp_result}" "${result_file}"
find "$(dirname -- "${temp_result}")" -maxdepth 1 -type f -name "$(basename -- "${temp_result}")" -delete
printf 'K3s startup/liveness restart conformance passed for %s.\n' "${version}"
