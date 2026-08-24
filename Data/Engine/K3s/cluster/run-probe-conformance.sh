#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

namespace="borealis-probe-conformance-$$"
result_file="${BOREALIS_K3S_PROBE_CONFORMANCE_FILE:-/var/lib/rancher/k3s/server/borealis-probe-conformance.json}"
kubectl=(k3s kubectl)

# Reproduce Kubernetes issue 141155: replacement must run startup probes before liveness resumes.
version="$(k3s --version | awk 'NR == 1 {print $3}')"
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$ ]] || {
  printf 'Cluster mode requires stable K3s release; saw %s.\n' "${version}" >&2
  exit 1
}
node_name="$(hostname -s | tr '[:upper:]' '[:lower:]')"
"${kubectl[@]}" get "node/${node_name}" >/dev/null

install -d -m 0700 "$(dirname -- "${result_file}")"
find "$(dirname -- "${result_file}")" -maxdepth 1 -type f -name "$(basename -- "${result_file}")" -delete

cleanup() {
  "${kubectl[@]}" delete namespace "${namespace}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${kubectl[@]}" create namespace "${namespace}" >/dev/null
sed "s|__BOREALIS_CONFORMANCE_NODE__|${node_name}|g" <<'EOF' | "${kubectl[@]}" -n "${namespace}" apply -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: startup-restart-conformance
spec:
  nodeName: __BOREALIS_CONFORMANCE_NODE__
  restartPolicy: Always
  terminationGracePeriodSeconds: 2
  volumes:
    - name: state
      emptyDir: {}
  containers:
    - name: probe
      image: registry.k8s.io/e2e-test-images/busybox@sha256:a9155b13325b2abef48e71de77bb8ac015412a566829f621d06bfae5c699b1b9
      command: [sh, -c]
      args:
        - |
          instance=initial
          if test -f /state/container-seen; then
            instance=replacement
          else
            touch /state/container-seen
          fi
          echo "$instance" > /state/instance
          printf '%s %s container-started\n' "$(date -u +%FT%TZ)" "$instance" >> /state/events
          on_term() {
            printf '%s %s received-SIGTERM\n' "$(date -u +%FT%TZ)" "$instance" >> /state/events
            exit 0
          }
          trap on_term TERM
          sleep 3600 &
          wait $!
      volumeMounts:
        - name: state
          mountPath: /state
      startupProbe:
        exec:
          command:
            - sh
            - -c
            - |
              instance="$(cat /state/instance)"
              result=failure
              test "$instance" = initial && result=success
              printf '%s %s startup-probe:%s\n' "$(date -u +%FT%TZ)" "$instance" "$result" >> /state/events
              test "$result" = success
        periodSeconds: 1
        failureThreshold: 100
      livenessProbe:
        exec:
          command:
            - sh
            - -c
            - |
              instance="$(cat /state/instance)"
              result=success
              if test "$instance" = initial && test -f /state/fail-liveness; then
                result=failure
              fi
              printf '%s %s liveness-probe:%s\n' "$(date -u +%FT%TZ)" "$instance" "$result" >> /state/events
              test "$result" = success
        periodSeconds: 1
        failureThreshold: 1
EOF

pod="startup-restart-conformance"
"${kubectl[@]}" -n "${namespace}" wait "pod/${pod}" \
  --for=jsonpath='{.status.containerStatuses[0].started}'=true \
  --timeout=120s >/dev/null
sleep 2

first_id="$("${kubectl[@]}" -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].containerID}')"
"${kubectl[@]}" -n "${namespace}" exec "${pod}" -- touch /state/fail-liveness >/dev/null

second_id="${first_id}"
attempt=0
while [[ "${second_id}" == "${first_id}" && "${attempt}" -lt 600 ]]; do
  second_id="$("${kubectl[@]}" -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].containerID}')"
  attempt=$((attempt + 1))
  [[ -n "${second_id}" && "${second_id}" != "${first_id}" ]] || sleep 0.1
done
[[ -n "${second_id}" && "${second_id}" != "${first_id}" ]] || {
  printf 'K3s probe conformance failed: liveness failure did not restart container. Cluster mode remains disabled.\n' >&2
  exit 1
}

sleep 3
replacement_started="$("${kubectl[@]}" -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].started}')"
restart_count="$("${kubectl[@]}" -n "${namespace}" get pod "${pod}" -o jsonpath='{.status.containerStatuses[0].restartCount}')"
events="$("${kubectl[@]}" -n "${namespace}" exec "${pod}" -- cat /state/events)"

[[ "${restart_count}" == "1" ]] || {
  printf 'K3s probe conformance failed: expected one restart; saw %s. Cluster mode remains disabled.\n' "${restart_count}" >&2
  exit 1
}
[[ "${replacement_started}" == "false" ]] || {
  printf 'K3s probe conformance failed: replacement bypassed startup probe with started=%s. Cluster mode remains disabled.\n' "${replacement_started}" >&2
  exit 1
}
grep -Fq 'initial liveness-probe:failure' <<<"${events}" || {
  printf 'K3s probe conformance failed: initial liveness failure was not observed. Cluster mode remains disabled.\n' >&2
  exit 1
}
grep -Fq 'replacement startup-probe:failure' <<<"${events}" || {
  printf 'K3s probe conformance failed: replacement startup probe did not run. Cluster mode remains disabled.\n' >&2
  exit 1
}
if grep -Fq 'replacement liveness-probe:' <<<"${events}"; then
  printf 'K3s probe conformance failed: replacement liveness ran before startup completed. Cluster mode remains disabled.\n' >&2
  exit 1
fi

temp_result="$(mktemp)"
printf '{"id":"pod-restart-policy-startup-probe-v1","status":"passed","k3s_version":"%s","tested_at":"%s"}\n' "${version}" "$(date -u +%FT%TZ)" > "${temp_result}"
install -m 0600 "${temp_result}" "${result_file}"
find "$(dirname -- "${temp_result}")" -maxdepth 1 -type f -name "$(basename -- "${temp_result}")" -delete
printf 'K3s startup/liveness restart conformance passed for %s.\n' "${version}"
