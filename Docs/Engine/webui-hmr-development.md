# WebUI HMR Development
Use a standalone Engine for WebUI development. Dev mode serves JSX/CSS changes through Vite without rebuilding every Engine workload. Enabled one-node and three-node clusters support recovery of existing isolation; new clustered HMR entry is disabled.

## Requirements
- Run commands from the Engine host checkout, usually `/opt/Borealis`.
- Use the same `--network-mode` value that was used for the Engine install.
- Run `Engine.sh` with `sudo` unless the shell user can access `/var/run/docker.sock`.
- Keep durable WebUI source under `Data/Engine/Containers/webui-frontend/data/web-interface/`.
- Treat `Engine/Services/webui-frontend/data/web-interface/` as disposable runtime source for live HMR sessions.

!!! info "Clustered development is disabled"
    Clustered dev deploys and service commands are blocked, including legacy acknowledgement flags. Use a standalone development Engine, then publish and qualify an immutable release for cluster updates. To restore an already isolated cluster, follow [Existing clustered isolation](#clustered-node-isolation-hmr-preview) below.

## Start Dev WebUI
Use a scoped WebUI rebuild when the Engine stack already exists and only the frontend needs dev mode.

!!! warning "Clustered Engine"
    Commands in this section require a standalone Engine. On an enabled cluster, restore existing isolation below and use Cluster Management for release updates.

=== "Local"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local --service webui-frontend rebuild dev
    ```

=== "Public"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public --service webui-frontend rebuild dev
    ```

Use full dev deploy when shared Engine configuration changed or when switching a stale stack into dev mode:

=== "Local"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local deploy dev
    ```

=== "Public"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public deploy dev
    ```

## Switch WebUI Modes
Use these commands to move only the WebUI between dev/HMR and production static serving.

!!! info "Standalone mode changes"
    These shortcuts change standalone WebUI mode. Clustered production restoration uses the authenticated recovery command below.

=== "Local Dev"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local --service webui-frontend rebuild dev
    ```

=== "Public Dev"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public --service webui-frontend rebuild dev
    ```

=== "Local Prod"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode local --service webui-frontend rebuild prod
    ```

=== "Public Prod"

    ```sh
    cd /opt/Borealis
    sudo bash Engine.sh --network-mode public --service webui-frontend rebuild prod
    ```

## Edit Loop
After dev WebUI starts, the K3s WebUI pod reads source from:

```text
/opt/Borealis/Engine/Services/webui-frontend/data/web-interface/src/
```

Edit files there for fastest HMR feedback. Vite serves changes through the normal Borealis HTTPS URL, and browser HMR connects through:

```text
wss://<engine-fqdn>/__vite_hmr
```

When edits are ready to keep, make the same source changes under:

```text
/opt/Borealis/Data/Engine/Containers/webui-frontend/data/web-interface/src/
```

Then run the scoped dev rebuild again to refresh the runtime copy from committed source:

```sh
sudo bash Engine.sh --network-mode <local-or-public> --service webui-frontend rebuild dev
```

Do not blindly copy whole runtime tree when it contains temporary probes or experiments. Reproduce only accepted edits in durable source, remove temporary markers, then compare trees:

```bash
diff -ru \
  /opt/Borealis/Data/Engine/Containers/webui-frontend/data/web-interface/src \
  /opt/Borealis/Engine/Services/webui-frontend/data/web-interface/src
```

Expected differences should match accepted work only.

## Backend Or Combined Changes

Backend source is not live-mounted. Edit durable source under:

```text
/opt/Borealis/Data/Engine/Containers/api-backend/
```

After accepted WebUI edits exist in durable source, use full development deploy for backend or combined change:

```bash
cd /opt/Borealis

sudo bash Engine.sh --network-mode <local-or-public> deploy dev
```

Do not use `api-backend restart` for changed Go source; restart does not build new image. Do not run direct production deploy while cluster mode is active.

## Verify HMR
Confirm the WebUI pod is in dev mode:

```sh
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/webui-frontend -- sh -lc 'test "$BOREALIS_WEBUI_MODE" = dev && echo webui-dev'
```

Confirm the public edge can reach the Vite server:

```sh
curl -kI https://<engine-fqdn>/
```

Open browser developer tools on the Borealis page and check the Network tab for `__vite_hmr`. It should use `wss` and stay connected.

## Validate Durable Changes

Run repository validation from `/opt/Borealis`. Keep dependencies and build output outside staged source:

=== "WebUI"

    ```bash
    bash Tests/run-repository-policy.sh
    bash Tests/run-webui.sh
    BOREALIS_DOCKER_USE_SUDO=1 \
      bash Tests/run-containers.sh --service webui-frontend
    git diff --check
    ```

=== "Backend or Combined"

    ```bash
    bash Tests/run-repository-policy.sh
    bash Tests/run-engine-go.sh
    bash Tests/run-k3s-policy.sh
    BOREALIS_DOCKER_USE_SUDO=1 \
      bash Tests/run-containers.sh --service api-backend
    git diff --check
    ```

Run `bash Tests/run-docs.sh` when documentation changes. Do not run raw `npm`, `vite`, or `vitest` from staged source under `Data/Engine/Containers/*/data`.

## Return To Production

On a standalone Engine, use the production mode command above.

### Existing clustered isolation { #clustered-node-isolation-hmr-preview }

Restore existing isolation through Cluster Management. New entry and clustered dev rebuilds are disabled, including on the current isolated node. Before deploying this gate, qualify restoration using an immutable release containing the production WebUI fix from [PR #492](https://github.com/bunny-lab-io/Borealis/pull/492); source validation alone does not establish live restoration.

Disable isolation from WebUI:


1. Open **Admin > Cluster Management > Maintenance**.
2. Under **Cluster-Wide Node Isolation**, select **Disable Isolation**.
3. Type legacy confirmation phrase `EXIT HMR` and submit once.
4. Open **Cluster Events**, record operation ID, and wait for **Cluster-Wide Node Isolation Disabled** to succeed.
5. Return to **Overview** and **Nodes**, refresh, and verify isolation inactive plus every current member `Active / Active`.

Selecting **Exit Maintenance Mode** on drained standby during isolation is alternate exit path, not single-node activation. WebUI warns that Cluster-Wide Node Isolation will be disabled, requires `EXIT HMR`, and starts same full production-restore operation. Use **Disable Isolation** for clearest normal workflow.

??? note "CLI production restoration"
    Load a recent Admin Bearer token without placing it in history or command arguments. Use the Engine's deployed network mode and HTTPS URL:

    ```bash
    export BOREALIS_CLUSTER_API_URL='https://<engine-fqdn>'
    read -rsp 'Paste recent Borealis Admin access token: ' BOREALIS_CLUSTER_ADMIN_TOKEN
    printf '\n'
    export BOREALIS_CLUSTER_ADMIN_TOKEN
    # Set only for a custom CA.
    export BOREALIS_CLUSTER_CA_FILE='/path/to/ca.pem'
    ```

    CLI requests the same controller-owned restoration:


    ```sh
    cd /opt/Borealis

    sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
      bash Engine.sh \
      --network-mode <local-or-public> \
      --service webui-frontend \
      rebuild prod
    ```

    CLI submits the fixed `EXIT HMR` request and waits for the existing controller flow. Active or failed restoration remains recoverable; unavailable cluster/API state stops the command.

Controller restores saved pinned production release, keeps local edits untouched, verifies target, and restores standby workloads one node at time. Failed restore leaves HMR state and warning visible for explicit recovery.

After isolation is disabled, confirm Cluster Management shows:

- Isolation inactive and cluster `Healthy`.
- Active membership equal to desired membership.
- No active operation.
- Every node `Active / Active`.
- CloudNativePG configured count equal to Ready count.

Cancelling queued HMR work stops further steps and retains the isolated target for recovery; it does not mark the cluster restored.

If exit fails, stop. Record operation ID and exact failed step. Do not begin **Update All**, submit second operation, clear drain, patch Deployment, or pull source on standby nodes.

## Promote Accepted Work

Commit durable repository source and documentation after standalone testing and validation complete. Push the issue branch and wait for required GitHub checks. Follow the issue workflow before merging and publishing.

Publishing release remains separate GitHub action. For cluster testing, publish immutable `YYYY.MM.REVISION[.HOTFIX]-rc.N` tag as GitHub prerelease, then use **Qualification Engine Version > Deploy Qualification One Node at a Time** and type `DEPLOY QUALIFICATION`. Qualification stays unsupported and visible until forward promotion. Never use branch head as cluster target.

After candidate passes, publish same commit or descendant as normal `YYYY.MM.REVISION[.HOTFIX]` GitHub release. Use **Stable Engine Version > Update All One at a Time** and type `UPDATE CLUSTER`. Stable promotion runs deferred contract-phase schema work. Downgrade rollback is not supported. Each target verifies exact published tag, pinned commit, ancestry, release channel, and compatibility manifest before staging. Do not copy files, run host-by-host `git pull`, or patch workloads manually. See [Cluster-Aware Updates](managing-engine-clusters.md#cluster-aware-updates) for full contract.

K3s Spegel can share imported container image layers between cluster peers. It does not copy repository source, publish release, or replace tag/SHA verification. Borealis currently has no supported local-only, non-GitHub source rollout. Internal node-manager fetch and staging commands remain controller-only.

## Clean Up Credentials

Remove temporary CLI credentials after HMR operations:

```bash
unset BOREALIS_CLUSTER_ADMIN_TOKEN
unset BOREALIS_CLUSTER_API_URL
unset BOREALIS_CLUSTER_CA_FILE
```

Close temporary port-forwards. Do not persist these variables in shell profile or environment file.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Updating the Engine](updating-the-engine.md)
    - [Managing Engine Clusters](managing-engine-clusters.md)
    - [Engine Maintenance Commands](engine-maintenance-commands.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - `webui-frontend rebuild dev` syncs `Data/Engine/Containers/webui-frontend/data/web-interface/` into `Engine/Services/webui-frontend/data/web-interface/`, builds the WebUI dev image target when its declared inputs changed, imports that image into K3s containerd when missing, and reconciles only the WebUI workload when the WebUI config hash changed.
    - Dev mode sets `BOREALIS_WEBUI_MODE=dev`, starts Vite from the WebUI container entrypoint, and binds inside the pod on `0.0.0.0:8000` so the K3s `webui-frontend` ClusterIP Service can route traffic. Health checks still target `127.0.0.1:8000` inside the pod.
    - `vite.config.mts` enables the public-edge HMR proxy path when `BOREALIS_DEV_UI_PROXY_ENABLED=1`. The browser uses `wss://<engine-fqdn>/__vite_hmr`; Traefik's WebUI catch-all route forwards that websocket to the K3s WebUI Service.
    - Dev source mounts are read-only hostPath mounts from `Engine/Services/webui-frontend/data/web-interface/` into `/opt/Borealis/Data/Engine/web-interface/`. Vite optimizer and temporary config output use memory-backed `emptyDir` mounts at `node_modules/.vite` and `node_modules/.vite-temp`.
    - Pinned-release restoration and rolling release staging create isolated candidate Deployments from generic zero-replica templates. WebUI candidate reconciliation always forces `BOREALIS_WEBUI_MODE=prod` and removes every HMR-only source, configuration, test, and Vite cache mount before applying candidate. This prevents generic template left in development mode from starting production image through unavailable Vite entrypoint.
    - Do not run raw `npm`, `vite`, or `vitest` from staged source under `Data/Engine/Containers/webui-frontend/data/web-interface/`. Use `Engine.sh` for deploy/rebuild validation and `./Engine_Unit_Tests.sh --domain webui` for WebUI unit tests when the runtime test cache exists.

    ### Clustered entry gate and recovery

    - `Engine.sh` checks HMR membership before dev deploy or any dev service action, including restart/reload paths that can rewrite shared WebUI runtime mode. Existing K3s needs a readable kubeconfig and successful bounded discovery. Missing cluster CRD or instance is confirmed standalone; API errors and unreadable configuration fail closed. Any BorealisCluster instance blocks dev, including transitional membership. General `cluster_mode_enabled` consumers are unchanged.
    - Public `/api/server/cluster/hmr/start` retains authentication, bounded input and confirmation validation, then returns `409 cluster_hmr_entry_disabled` before store mutation. Store creation and retry reject `hmr_start` independently. There is no acknowledgement or configuration override.
    - Controller claims old queued/interrupted `hmr_start` and fails it before Kubernetes intent or runtime steps. Normal failure handling retains target/baseline and records `restore_failed`; the runner also rejects every entry step. Cancelling queued entry or exit retains `restore_failed` and the isolated target because a retry may have changed runtime already. Only verified production restoration clears isolation. Exit, lost-target recovery, production candidate cleanup and failed-exit retry remain intact.
    - Failed HMR recovery can queue `hmr_exit` despite generic failed-operation quorum status. Controller still verifies supported active membership, fencing and runtime health before restoration. No automatic membership reset, data deletion or drain clearing is introduced.
    - Cluster Management removes entry selection/submission and presents the recorded isolated node with **Disable Isolation** for `active` or `restore_failed`. Standby **Exit Maintenance Mode** still submits the same `EXIT HMR` recovery request.
    - Do not deploy U01 disablement until exact-release restoration containing #492 succeeds with existing isolation, production mode, no HMR mounts and fresh UI/API access. Q01 repeats restoration followed by denial on the final immutable release. Portable tests are not live qualification evidence.
    - For UI verification, open `/cluster-management?tab=maintenance` on the qualified preview: no **Enable Isolation** or editable isolated-node selector; inactive state disables recovery button; active/failed isolation exposes recorded node and unchanged confirmation. On Nodes, drained standby retains full isolation exit.

    ### Development and qualification

    Run mutable development only on a standalone Engine. Keep durable edits in `Data/Engine/`, run affected portable lanes, and follow [Publishing Engine Releases](publishing-engine-releases.md) for immutable candidate and stable publication. Cluster Management owns distribution and exact source verification; do not copy source between members or use host-by-host `git pull`.

    #461 retains post-MVP preview architecture. #466/H03 transport and task-ownership recovery remain required even with HMR entry disabled. Keep the operator's existing approval scope and never manufacture release or live qualification evidence.

    ### Recovery evidence and secrets

    After production restoration, require inactive isolation, Healthy status, expected active/desired membership, no active operation, all nodes `Active / Active` and full CloudNativePG readiness. On failure, retain operation ID, attempt, failed step, target, related Job/Pod logs, serving state and drain state. Recover through the recorded exit flow; never retry disabled entry, clear drain labels, delete audit records or patch images to bypass recovery.

    Keep credentials out of arguments, source, issue comments and logs. Use interactive SSH/sudo when approved remote access is needed; never infer credentials or use `sshpass`. Unset temporary Admin token/URL/CA variables and close port-forwards after recovery. Do not operate the hypervisor as part of this workflow.
