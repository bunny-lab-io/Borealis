# K3s Engine Migration

This release moves the Borealis Engine from a Docker Compose runtime into a single-node K3s Kubernetes control plane. Docker Compose is retired as the active Engine runtime, while Borealis keeps the same `Engine.sh` deployment entrypoint operators already use.

The practical goal is a more enterprise-shaped Engine foundation: stronger workload boundaries, safer runtime control, faster reconciliation, clearer rollout state, and a platform path that can grow into future cluster-friendly scaling features.

## What Improved

- **Security posture:** Engine workloads now run as Kubernetes workloads with tighter boundaries, fixed service templates, namespace-scoped operator control, dropped capabilities where practical, read-only roots where supported, and no runtime Docker socket dependency for normal Borealis service control.
- **Operational safety:** `Engine.sh deploy` reconciles desired state instead of depending on long-lived Compose service shape. K3s rollout checks make service readiness clearer during install, update, restore, and recovery.
- **Cleaner service ownership:** PostgreSQL, API backend, scheduler, WireGuard, Traefik, WebUI, guacd, and site-workers are now K3s-owned workloads. Site-workers are managed through the Borealis operator, not ad hoc Docker launches.
- **Faster recovery behavior:** Site-worker churn and restored runtime state converge faster. Agents now handle site-worker socket reconnection more reliably and the WebUI shows `Reconnecting` when heartbeat is online but the management socket is still reattaching.
- **Future scale path:** Kubernetes gives Borealis a better foundation for later multi-node and horizontal scaling work. More Borealis services can move toward cluster-native scheduling, workload isolation, metrics, and eventually smarter capacity placement.

## Upgrade Approach

Use a Backup/Restore migration for this release. Export a configuration backup first, deploy the updated K3s Engine, then restore that backup into the new runtime.

This gives you a clean migration path where Borealis imports configuration, trust, users, sites, devices, saved automation content, and protected Engine secrets into the current K3s PostgreSQL schema instead of carrying forward old Docker Compose runtime state.

!!! warning
    Keep the same Engine FQDN and network mode when you expect existing agents to reconnect without reinstalling. Backup/Restore keeps Engine trust material, but agents still trust the Engine by hostname and TLS identity.

## Before Updating

1. Sign in as an administrator.
2. Open **Admin Settings > Backup/Restore**.
3. Select **Export**.
4. Store the encrypted JSON backup somewhere safe.
5. Confirm you have the Aegis Cipher for that Engine.

Do not continue until the backup file and Aegis Cipher are both available.

## Pull the Updated Engine

Use these commands on an existing Engine checkout after the backup is complete.

```sh
cd /opt/Borealis

# Pull the released Engine source by fast-forward only.
git fetch origin
git checkout main
git pull --ff-only origin main
```

If you are building a fresh replacement Engine instead of updating the existing checkout, clone the released source first.

```sh
sudo git clone https://github.com/bunny-lab-io/Borealis.git /opt/Borealis
cd /opt/Borealis

# Pin to the released branch. Use a release tag instead when one is provided.
sudo git checkout main
sudo git pull --ff-only origin main
```

## Deploy K3s Engine

Use the same network mode the Engine should serve after migration.

=== "Public"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public deploy prod
    ```

=== "Local"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local deploy prod
    ```

Wait for deployment to finish. On a fresh host, open the Engine URL and choose **Restore Engine Config Backup** from the Aegis setup screen. On an existing host, sign in as an administrator and use **Admin Settings > Backup/Restore**.

## Restore the Backup

1. Select the encrypted JSON backup.
2. Enter the Aegis Cipher from the source Engine.
3. Select **Analyze** and review the import counts.
4. Type `RESTORE ENGINE CONFIG BACKUP`.
5. Select **Import**.
6. Keep the restore page open while Borealis refreshes K3s services.
7. Unlock Aegis only after the page says the Engine is ready.

After restore, Borealis refreshes runtime services so restored keys, sessions, WireGuard state, and site-worker routing load cleanly.

## Post-Upgrade Checks

Run these checks from the Engine host after restore completes.

=== "Public"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public deploy prod
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pods -o wide
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/api-backend
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/job-scheduler
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
    ```

=== "Local"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local deploy prod
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get pods -o wide
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/api-backend
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/job-scheduler
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
    ```

Then confirm the WebUI opens, Aegis unlock works, Sites loads site-workers, Device Inventory shows agents reconnecting or connected, and at least one remote operation works against a Windows or Linux agent.

## What This Opens Next

K3s does not make Borealis multi-node or horizontally scaled by itself in this release. It gives Borealis the control-plane shape needed to get there.

Future Borealis work can build on this foundation with more Kubernetes-native service placement, site-worker scaling, richer resource controls, safer rolling changes, stronger runtime isolation, and eventually cluster-aware deployment patterns that were awkward or unsafe to build on Docker Compose.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Backup and Restore](../Using%20the%20Platform/backup-restore.md)
    - [Updating the Engine](../Engine/updating-the-engine.md)
    - [Engine Deployment](../Engine/deploying-the-engine.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Source map

    - Engine deploy entrypoint: `Engine.sh`
    - K3s runtime docs: `Docs/Reference/Core Runtimes/engine-runtime.md`
    - Stack inventory: `Docs/Reference/Core Runtimes/Stack_Breakdown.md`
    - Backup/Restore docs: `Docs/Using the Platform/backup-restore.md`
