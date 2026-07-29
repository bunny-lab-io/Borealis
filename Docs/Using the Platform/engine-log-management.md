# Engine Log Access

Borealis keeps Engine log reading and cleanup outside the WebUI. Use host CLI access for diagnostics, and let automatic retention prune Borealis-owned rotated file logs.

## K3s Pod Logs

List Borealis pods first so container names and restart counts are visible.

```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pods -o wide
```

Tail core workload logs.

```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/api-backend -c api-backend --tail=200 --timestamps
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/job-scheduler -c job-scheduler --tail=200 --timestamps
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs statefulset/postgres-db -c postgres-db --tail=200 --timestamps
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/remote-desktop-guacd --tail=200 --timestamps
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/wireguard-tunnel -c wireguard-tunnel --tail=200 --timestamps
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs deployment/traefik-edge -c traefik-edge --tail=200 --timestamps
```

Tail all current site-worker pods.

```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs -l app.kubernetes.io/name=site-worker -c site-worker --tail=200 --timestamps --prefix=true
```

Read previous container logs after crash/restart by selecting pod name first.

```sh
POD="$(sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pod -l app.kubernetes.io/name=api-backend -o jsonpath='{.items[0].metadata.name}')"
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis logs "pod/${POD}" -c api-backend --previous --tail=200 --timestamps
```

## Borealis File Logs

Some Engine components still write Borealis-owned file logs under `Engine/Services/<role>/logs/`.

```sh
sudo find \
  /opt/Borealis/Engine/Services/api-backend/logs \
  /opt/Borealis/Engine/Services/traefik-edge/logs \
  /opt/Borealis/Engine/Services/wireguard-tunnel/logs \
  -type f -name '*.log*' -printf '%TY-%Tm-%Td %TH:%TM %s %p\n' 2>/dev/null | sort
```

```sh
sudo tail -n 200 /opt/Borealis/Engine/Services/api-backend/logs/engine.log
sudo tail -n 200 /opt/Borealis/Engine/Services/api-backend/logs/error.log
sudo tail -n 200 /opt/Borealis/Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log
```

!!! warning

    Do not manually delete `/var/log/pods`, `/var/log/containers`, or container runtime log files. Kubelet owns those files and rotates them by configured size/count policy.

## Retention

Borealis-owned rotated file logs default to 30-day retention. The API backend sweeps configured log roots at startup and then every six hours while API background loops are enabled.

K3s pod logs use Kubernetes kubelet rotation, which is size/count based instead of date based. To set optional Borealis-managed kubelet log rotation values, export the values before deploy.

```sh
BOREALIS_K3S_CONTAINER_LOG_MAX_SIZE=10Mi \
BOREALIS_K3S_CONTAINER_LOG_MAX_FILES=30 \
bash /opt/Borealis/Engine.sh --network-mode local deploy prod
```

!!! info

    Changing K3s kubelet log rotation changes the Borealis-managed K3s config file. `Engine.sh` restarts K3s only when that config changes.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `/api/server/logs*` routes are retired and return `410 Gone` for authenticated administrators.
    - WebUI log browsing, retention overrides, deletion, and purge actions are not exposed.

    ### Related documentation

    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)
    - [Kubernetes Logging Architecture](https://kubernetes.io/docs/concepts/cluster-administration/logging/)
    - [Kubelet Configuration API](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/)
    - [K3s Configuration](https://docs.k3s.io/installation/configuration)

    ### Source map

    - Retired log routes and file retention: `Data/Engine/Containers/api-backend/cmd/api-backend/server_logs.go`
    - API startup/background loop wiring: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go`
    - K3s log rotation config rendering: `Engine.sh`

    ### Runtime behavior

    - `borealis-operator` does not receive Kubernetes `pods/log` RBAC.
    - Automatic retention only removes rotated files matching `*.log.YYYY-MM-DD` under Borealis-owned file log roots.
    - Active `.log` files, non-rotation files, and Kubernetes pod/container log files are left alone.
    - `BOREALIS_ENGINE_FILE_LOG_RETENTION_DAYS` controls Borealis file-log retention and defaults to `30`.
    - `BOREALIS_K3S_CONTAINER_LOG_MAX_SIZE` and `BOREALIS_K3S_CONTAINER_LOG_MAX_FILES` render optional K3s `kubelet-arg` values when set.
