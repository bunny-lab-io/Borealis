# WebUI HMR Development
Use this workflow when testing Engine WebUI changes on a K3s-based Borealis Engine without rebuilding or rolling every Engine workload for each JSX/CSS edit.

## Requirements
- Run commands from the Engine host checkout, usually `/opt/Borealis`.
- Use the same `--network-mode` value that was used for the Engine install.
- Run `Engine.sh` with `sudo` unless the shell user can access `/var/run/docker.sock`.
- Keep durable WebUI source under `Data/Engine/Containers/webui-frontend/data/web-interface/`.
- Treat `Engine/Services/webui-frontend/data/web-interface/` as disposable runtime source for live HMR sessions.

!!! danger "Cluster-Wide Node Isolation"
    On clustered Engine, every dev deploy and WebUI dev rebuild requires exclusive Cluster-Wide Node Isolation operation. Admin must type legacy confirmation phrase `ENABLE HMR`. Borealis drains application workloads on other nodes and moves application traffic onto isolated node, so application HA remains unavailable until isolation is disabled. Infrastructure quorum remains active. See [Managing Engine Clusters](managing-engine-clusters.md).

!!! bug "Clustered HMR remains manual"
    Clustered HMR is controlled preview workflow, not HA development or release distribution. Network loss of active isolated node can still leave public application unavailable until target returns or operator completes recovery. Keep operator available throughout isolation window, disable isolation before any rolling update, and follow [issue #466](https://github.com/bunny-lab-io/Borealis/issues/466) for automated lost-target recovery work.

## Clustered Node Isolation / HMR Preview

Clustered development uses two separate phases:

1. Cluster-Wide Node Isolation runs mutable HMR development workload on one selected Engine node while other application nodes remain drained.
2. Published stable release plus Cluster Management **Update All One at a Time** distributes accepted immutable revision across every Engine node.

Isolation never copies source to standby nodes. Returning to production restores saved pinned release, not local development source.

### Prepare Cluster

Obtain explicit operator approval and keep operator available before enabling isolation. Start only when Cluster Management shows:

- Cluster status `Healthy` with active membership equal to desired membership.
- Cluster-Wide Node Isolation inactive and no active cluster operation.
- Every node `Active / Active`, with no drained or cordoned member.
- CloudNativePG configured count equal to Ready count, with synchronous durability available when cluster has multiple members.
- Intended isolated node healthy on pinned production release.

Start from clean issue branch and clean worktree on intended isolated node. Do not overwrite unrelated operator changes. Confirm deployed network mode instead of guessing:

```bash
awk -F= \
  '$1 == "BOREALIS_ENGINE_NETWORK_MODE" {
    print substr($0, index($0, "=") + 1)
  }' \
  /opt/Borealis/Engine/Deploy/runtime.env
```

Expected value is `local` or `public`.

??? note "Authenticate Cluster CLI"
    Clustered `Engine.sh` HMR commands require current Admin Bearer token. Load it without placing secret in shell history or command arguments:

    ```bash
    cd /opt/Borealis

    export BOREALIS_CLUSTER_API_URL='https://<engine-fqdn>'
    read -rsp 'Paste recent Borealis Admin access token: ' BOREALIS_CLUSTER_ADMIN_TOKEN
    printf '\n'
    export BOREALIS_CLUSTER_ADMIN_TOKEN

    # Set only when Engine uses custom CA.
    export BOREALIS_CLUSTER_CA_FILE='/path/to/ca.pem'
    ```

    Browser `borealis_auth` cookie and Admin Bearer token are not interchangeable. Browser session can submit HMR isolation through Cluster Management, but later `Engine.sh` development rebuild still requires Bearer token to verify active HMR target. Never place password, Admin token, cookie, K3s token, or private key in repository, PR, issue, logs, shell profile, or command arguments.

### Clear Maintenance State

Cluster Management disables HMR entry while any active member remains drained. Kubernetes `Ready` alone is insufficient; every active member must also be out of application maintenance.

1. Sign in as Administrator and open **Admin > Cluster Management > Nodes**.
2. Select **Refresh** and verify every current member reports `Active / Active`.
3. For each `Active / Drained` member, open row **Actions**, choose **Exit Maintenance Mode**, enter concise reason, and submit once.
4. Open **Cluster Events** and wait for **Maintenance Mode Disabled** to succeed.
5. Return to **Nodes**, refresh, and verify member changed to `Active / Active` before restoring another member.
6. Continue until application-drained warning disappears and every active member is `Active / Active`.

If maintenance exit fails, stop and copy operation details. Do not clear drain labels, patch node runtime objects, uncordon manually, or start second recovery operation.

### Enable Cluster-Wide Node Isolation

Choose Engine node holding development checkout and runtime source as isolated node. Selected WebUI node and host used for later rebuild must match.

1. Open **Admin > Cluster Management > Maintenance**.
2. Find **Cluster-Wide Node Isolation**.
3. Select intended target from **Isolated Node**.
4. Select **Enable Isolation**.
5. Read non-HA warning, type legacy confirmation phrase `ENABLE HMR`, and submit once.
6. Open **Cluster Events**, record operation ID, and wait for **Cluster-Wide Node Isolation Enabled** to succeed.
7. Confirm cluster-wide isolation banner is active and accepted Cluster Event targets expected node before running development rebuild.

WebUI operation isolates cluster and moves application traffic. It does not copy branch source, distribute code to standby nodes, or start Vite development workload.

### Start Development Workload On Isolated Node

After WebUI operation succeeds, run scoped rebuild from same selected node:

```bash
cd /opt/Borealis

sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh \
  --network-mode <local-or-public> \
  --service webui-frontend \
  rebuild dev
```

`Engine.sh` confirms HMR is already active on local node, then syncs durable WebUI source into runtime tree and starts development workload. Bearer token remains required for state check even though browser already submitted HMR operation.

??? note "CLI-only HMR entry"
    Operator may combine isolation and development rebuild through CLI when WebUI path is unavailable. Start from same healthy, maintenance-free preflight and run command above while HMR is inactive. Interactive CLI then requires typing:

    ```text
    ENABLE HMR
    ```

    Use `--acknowledge-cluster-non-ha` only for non-interactive operation after explicit operator approval.

After isolation begins, do not start maintenance, membership, release, K3s, or normal PostgreSQL operations.

## Start Dev WebUI
Use a scoped WebUI rebuild when the Engine stack already exists and only the frontend needs dev mode.

!!! warning "Clustered Engine"
    Commands in this section are standalone shortcuts. On cluster, use **Clustered HMR Preview** above so operator restores every node from maintenance, selects HMR target through Cluster Management, and preserves authenticated CLI variables for rebuild.

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

!!! warning "Cluster authentication"
    Basic commands below suit standalone Engine. On cluster, preserve authenticated CLI variables exactly as shown in **Clustered HMR Preview** so `Engine.sh` can submit and monitor HMR operation.

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
sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh --network-mode <local-or-public> --service webui-frontend rebuild dev
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

sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
  bash Engine.sh \
  --network-mode <local-or-public> \
  deploy dev
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
Disable isolation from WebUI after operator finishes HMR testing:

1. Open **Admin > Cluster Management > Maintenance**.
2. Under **Cluster-Wide Node Isolation**, select **Disable Isolation**.
3. Type legacy confirmation phrase `EXIT HMR` and submit once.
4. Open **Cluster Events**, record operation ID, and wait for **Cluster-Wide Node Isolation Disabled** to succeed.
5. Return to **Overview** and **Nodes**, refresh, and verify isolation inactive plus every current member `Active / Active`.

??? note "CLI production restoration"
    CLI can request same controller-owned restoration when Cluster Management is unavailable:

    ```sh
    cd /opt/Borealis

    sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE \
      bash Engine.sh \
      --network-mode <local-or-public> \
      --service webui-frontend \
      rebuild prod
    ```

    Interactive clustered CLI requires typing `EXIT HMR`.

Controller restores saved pinned production release, keeps local edits untouched, verifies target, and restores standby workloads one node at time. Failed restore leaves HMR state and warning visible for explicit recovery.

After isolation is disabled, confirm Cluster Management shows:

- Isolation inactive and cluster `Healthy`.
- Active membership equal to desired membership.
- No active operation.
- Every node `Active / Active`.
- CloudNativePG configured count equal to Ready count.

If exit fails, stop. Record operation ID and exact failed step. Do not begin **Update All**, submit second operation, clear drain, patch Deployment, or pull source on standby nodes.

## Promote Accepted Work

Commit only durable repository source and documentation after isolation is disabled and validation completes. Push issue branch and wait for required GitHub checks. Keep pull request Draft until operator requests review.

Publishing stable release is separate public action requiring explicit operator approval and unused dotted-numeric tag. Current convention is `YYYY.MM.DD.N`, where final number distinguishes multiple releases for same date. Suffix is part of publisher-selected GitHub tag; Cluster Management does not generate it. Older releases may use fewer numeric segments. Cluster updater rejects drafts, prereleases, branch heads, nonnumeric tags, and incompatible release manifests. Never change compatibility fields to bypass selection.

After approved stable release exists, use **Admin > Cluster Management > Maintenance > Stable Engine Release > Update All One at a Time**. Isolation does not distribute source. Each target verifies exact published tag and commit before local staging. Do not copy files, run host-by-host `git pull`, or patch workloads manually. See [Cluster-Aware Updates](managing-engine-clusters.md#cluster-aware-updates) for rollout and recovery contract.

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
    - Do not run raw `npm`, `vite`, or `vitest` from staged source under `Data/Engine/Containers/webui-frontend/data/web-interface/`. Use `Engine.sh` for deploy/rebuild validation and `./Engine_Unit_Tests.sh --domain webui` for WebUI unit tests when the runtime test cache exists.

    ### Codex clustered HMR workflow

    Treat clustered WebUI development and release distribution as separate operations:

    ```text
    Cluster-Wide Node Isolation / HMR preview on one Engine node
    -> operator accepts runtime behavior
    -> reproduce accepted edits in durable source
    -> validate, commit, and push issue branch
    -> disable isolation to restore pinned production release
    -> operator approves stable qualification release
    -> Cluster Management Update All One at a Time
    ```

    Isolation does not synchronize checkout or source between Engine nodes. Do not use HMR as release mechanism. Spegel distributes imported container image content only; it does not distribute or authorize Git source.

    ### Access and secret handling

    - Work locally on chosen HMR target when possible. Use interactive SSH for remote host audit only when needed.
    - Request SSH/sudo password and fresh Admin Bearer token from operator through secure channel when unavailable. Never infer or fabricate credentials.
    - Never place password, Admin token, browser cookie, K3s token, or private key in command arguments, source, shell profile, logs, issue, PR, or commit history.
    - Never use `sshpass`. Let SSH and remote `sudo` prompt interactively.
    - Do not interact with hypervisor as part of HMR or release workflow.

    ### WebUI-first isolation coordination

    Before asking operator to enable isolation, present explicit checkpoint and wait for confirmation:

    ```text
    NODE ISOLATION ENTRY CHECKPOINT:
    - Cluster shows Healthy and Ready.
    - Active membership equals desired membership.
    - Cluster-Wide Node Isolation is inactive and no cluster operation is active.
    - Every current node shows Active / Active and is out of maintenance mode.
    - CloudNativePG configured and Ready counts match; required durability is available.
    - Selected isolated node matches host containing development checkout.
    - Operator accepts temporary loss of application HA.
    ```

    Never treat Kubernetes `Ready` or uncordoned state as proof application maintenance ended. Read `BorealisNodeRuntime` desired and observed application states, or use Nodes view. If any member is drained:

    1. Pause isolation/HMR work.
    2. Direct operator to **Nodes > Actions > Exit Maintenance Mode** for one drained member.
    3. Wait for corresponding Cluster Event to succeed and row to become `Active / Active`.
    4. Repeat one member at time until no application-drained banner remains.
    5. Stop on failed maintenance exit. Do not patch Kubernetes labels or runtime CRs.

    Generic read-only preflight:

    ```bash
    sudo k3s kubectl \
      --kubeconfig /etc/rancher/k3s/k3s.yaml \
      -n borealis get borealiscluster/borealis \
      -o jsonpath='{.status.phase}{"\t"}{.status.hmrState}{"\n"}'

    sudo k3s kubectl \
      --kubeconfig /etc/rancher/k3s/k3s.yaml \
      -n borealis get borealisnoderuntimes \
      -o custom-columns='NODE:.spec.nodeName,DESIRED:.spec.desiredApplicationState,OBSERVED:.status.observedApplicationState,READY:.status.nodeReady'
    ```

    After operator confirms checkpoint, direct them to **Maintenance > Cluster-Wide Node Isolation**, select agreed **Isolated Node**, choose **Enable Isolation**, type legacy confirmation phrase `ENABLE HMR`, and submit once. Wait for operation success before running any development command. Browser action owns isolation only; authenticated CLI rebuild on same target owns source sync, dev image, and Vite startup.

    ### Before HMR entry

    1. Read this page, [Managing Engine Clusters](managing-engine-clusters.md), [Updating the Engine](updating-the-engine.md), and [Unit Testing](../Reference/Unit_Testing.md).
    2. Confirm issue branch, matching issue PR, clean worktree, and exact deployed network mode.
    3. Obtain explicit operator approval for cluster-wide non-HA isolation window. Keep operator available until production restore completes.
    4. Confirm Cluster Management reports `Healthy`, Ready, expected active/desired membership, isolation inactive, no active operation, all members `Active / Active`, and full CloudNativePG readiness plus synchronous durability.
    5. Confirm Kubernetes nodes Ready and production workloads healthy. Stop for maintenance, update, admission, database degradation, cordon, drain, or active operation.
    6. Load Admin token with silent `read` as shown above. Browser cookie may submit WebUI isolation, but CLI source rebuild still requires Bearer token.

    Read-only Kubernetes preflight:

    ```bash
    sudo k3s kubectl \
      --kubeconfig /etc/rancher/k3s/k3s.yaml \
      get nodes -o wide

    sudo k3s kubectl \
      --kubeconfig /etc/rancher/k3s/k3s.yaml \
      -n borealis get pods
    ```

    ### Development loop

    1. Operator enables isolation on agreed node through Cluster Management WebUI and waits for success.
    2. On same node, run authenticated scoped WebUI rebuild. Guard verifies existing internal HMR target instead of submitting second start operation.
    3. Edit runtime WebUI source with `apply_patch` for fast browser feedback.
    4. Confirm dev pod mode, public HTTPS response, and connected `wss://<engine-fqdn>/__vite_hmr` socket.
    5. Ask operator to accept live result before reproducing it in durable source.
    6. Apply only accepted edits under durable `Data/Engine/.../src/`. Remove probes and temporary markers.
    7. Compare runtime and durable trees. Investigate every unexpected difference; never use broad `rsync --delete` from mutable runtime tree.
    8. For backend change, edit durable backend source and run authenticated full `deploy dev`; restart alone cannot compile changed Go source.
    9. Run affected repository, unit, container, and docs lanes. Commit only durable source.

    Runtime and durable paths:

    ```text
    Runtime WebUI: /opt/Borealis/Engine/Services/webui-frontend/data/web-interface/src/
    Durable WebUI: /opt/Borealis/Data/Engine/Containers/webui-frontend/data/web-interface/src/
    Durable API:   /opt/Borealis/Data/Engine/Containers/api-backend/
    ```

    ### Production restoration gate

    Disable isolation before creating or rolling release. Production restore returns cluster to saved pinned release; it does not publish local changes.

    After WebUI or CLI production restoration completes, require isolation inactive, Healthy status, expected active/desired membership, no active operation, all nodes `Active / Active`, and full CloudNativePG readiness. If exit fails:

    - Record operation ID, attempt, target, failed step, exact error, and latest event.
    - Confirm healthy members still serve and whether failed target remains drained.
    - Capture related Kubernetes Job and Pod logs.
    - Do not submit Update All or second operation.
    - Do not delete Jobs or audit records, clear drain, uncordon, patch images, patch generic templates, or pull source on standby nodes.

    ### Qualification release checkpoint

    Stable release publication is external public action. Stop for explicit operator approval and unused dotted-numeric tag after branch validation succeeds. Use current `YYYY.MM.DD.N` convention unless operator directs otherwise; `N` increments for additional release on same date and remains part of immutable GitHub tag. Confirm clean worktree, local/remote branch SHA match, compatible release manifest, and successful PR checks:

    ```bash
    cd /opt/Borealis

    git status --short --branch
    git rev-parse HEAD
    git rev-parse origin/issue/<descriptive-dashed-name>
    jq . Data/Engine/release-manifest.json
    gh pr checks <pr-number> --repo bunny-lab-io/Borealis
    ```

    After approval, publish exact committed SHA:

    ```bash
    QUALIFICATION_TAG='<operator-approved-tag>'
    QUALIFICATION_SHA="$(git rev-parse HEAD)"

    gh release create "${QUALIFICATION_TAG}" \
      --repo bunny-lab-io/Borealis \
      --target "${QUALIFICATION_SHA}" \
      --title "${QUALIFICATION_TAG}" \
      --notes 'Qualification release for issue #<number>. Includes <concise scope>.'
    ```

    Verify release is published, stable, and resolves exact commit:

    ```bash
    gh release view "${QUALIFICATION_TAG}" \
      --repo bunny-lab-io/Borealis \
      --json tagName,targetCommitish,isDraft,isPrerelease,publishedAt,url

    git ls-remote origin "refs/tags/${QUALIFICATION_TAG}"
    ```

    Stop if catalog marks release incompatible. Inspect displayed reason and `release-manifest.json`; never bypass catalog or patch database state.

    ### Rolling distribution

    In **Admin > Cluster Management > Maintenance > Stable Engine Release**:

    1. Reconfirm cluster Healthy, isolation inactive, and no active operation.
    2. Select exact approved qualification tag and verify expected SHA.
    3. Choose **Update All One at a Time**.
    4. Type `UPDATE CLUSTER` and submit once.
    5. Copy operation ID immediately and monitor Cluster Events to terminal state.

    Controller owns backup, immutable target SHA verification, non-leader-first ordering, role transfer, drain and EndpointSlice withdrawal, local image staging, schema expansion, isolated candidate creation, candidate health/soak, promotion, active and role-aware health/soak, drain exit, schema finalize, and cluster baseline advancement. Every target fetches published tag and verifies exact SHA before staging. Spegel may provide peer image layers after import, but source remains Git-backed. Never replace controller flow with node-to-node copies, host-by-host `git pull`, manual image import, or Deployment patch.

    ### Failed rolling operation

    Record operation ID, attempt, failed step, target node, error, latest event, related Job/Pod logs, drain state, and serving state on healthy members. Do not create second operation, delete failure evidence, clear drain, uncordon, patch workloads, or remove recovery artifacts.

    Retry existing operation only after root cause is understood and operator approves. Retry resumes recorded checkpoint; it does not create replacement operation. API contract is:

    ```http
    POST /api/server/cluster/operations/<operation-id>/retry
    ```

    ```json
    {
      "confirmation": "RETRY OPERATION"
    }
    ```

    Never retry terminal successful operation.

    ### Post-rollout audit

    Require Cluster Management to report Healthy and Ready, expected active/desired membership, isolation inactive, exact release tag/SHA baseline, every node desired/observed revision matching target, all nodes active without drain reason, all node probes passing, full CloudNativePG readiness and synchronous durability, and no active operation or pending admission.

    Inspect Kubernetes state:

    ```bash
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml get nodes -o wide
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get deployments
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get daemonsets
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get jobs
    sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis get endpointslices
    ```

    Expected state:

    - Every K3s node Ready and uncordoned.
    - Active node-scoped Deployments ready; candidate Deployments and generic templates at zero replicas.
    - Generic templates record exact target revision and container-image map.
    - WireGuard route DaemonSet ready on every node.
    - API, WebUI, guacd, and PostgreSQL RW EndpointSlices contain no unexpected unready endpoints.
    - No active Jobs or nonterminal node-action Pods.

    Audit remote hosts through interactive prompt, repeating for every nonlocal Engine node:

    ```bash
    ssh -t <ssh-user>@<engine-node>
    sudo -v
    cd /opt/Borealis
    git status --short --branch
    git rev-parse HEAD
    sudo systemctl is-active borealis-node-manager
    sudo test -S /run/borealis/node-manager.sock && echo node-manager-socket-present
    sudo cat /etc/borealis/node-manager-revision
    exit
    ```

    Do not place password in command. Do not delete quarantine, recovery, audit, or temporary recovery artifacts without operator approval.

    ### Final PR evidence

    Before merge, record issue goal, files and behavior, qualification tag and exact SHA, rolling operation ID/attempt/order, backup and schema results, candidate and promotion results, continuity evidence, final cluster/K3s/CloudNativePG/Deployment/EndpointSlice/storage/host state, commands actually run, GitHub check result, remaining risks, and release synthesis instructions. Keep PR Draft until operator directs review. Never merge or publish release automatically.

    ### Validation

    - Browser check: visible UI/UX change updates without full Engine redeploy.
    - HMR socket check: browser Network tab shows `__vite_hmr` connected over `wss`.
    - Pod mode check: `sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/webui-frontend -- sh -lc 'test "$BOREALIS_WEBUI_MODE" = dev && echo webui-dev'`.
    - Repository check: `git diff -- Data/Engine/Containers/webui-frontend/data/web-interface/src`.
    - Run current portable lanes from [Unit Testing](../Reference/Unit_Testing.md); do not depend on stale runtime caches.
