# Engine Maintenance Commands
Use service-scoped commands when troubleshooting one Engine component without redeploying the full Borealis stack.

!!! info "When to use these"
    These commands are useful after config edits, WebUI rebuilds, Traefik routing changes, or WireGuard tunnel issues. Use the same `--network-mode` value used during install. Run with `sudo` unless the shell user can access `/var/run/docker.sock`. Use the normal Engine update path when you want to pull and redeploy the entire platform.

```sh
# Restart the API backend workload.
sudo bash Engine.sh --network-mode local --service api-backend restart

# Restart the WebUI frontend workload without rebuilding its image.
sudo bash Engine.sh --network-mode local --service webui-frontend restart

# Rebuild the WebUI frontend workload in production mode.
sudo bash Engine.sh --network-mode local --service webui-frontend rebuild prod

# Rebuild the WebUI frontend workload in development mode.
sudo bash Engine.sh --network-mode local --service webui-frontend rebuild dev

# Reload Traefik edge configuration.
sudo bash Engine.sh --network-mode local --service traefik-edge reload

# Reconcile WireGuard tunnel state to fix agent tunnel connections.
sudo bash Engine.sh --network-mode local --service wireguard-tunnel reconcile
```

!!! warning "Service commands are targeted"
    A service command only touches the named component. If multiple services changed, run the normal deployment command from [Updating the Engine](updating-the-engine.md).

!!! tip "WebUI HMR"
    Use [WebUI HMR Development](webui-hmr-development.md) for frontend-only edit loops. `webui-frontend rebuild dev` syncs staged WebUI source into the runtime HMR copy and reconciles only the WebUI workload when WebUI inputs changed.

## Rebuild and Publish Agent Binaries

Use Agent binary redeployment after changing checked-out `Data/Agent` source. Command compiles Windows and Linux Agents, atomically publishes Engine-hosted stable artifact, verifies running API can read it, then replaces active site workers still using older site-worker image.

```sh
cd /opt/Borealis
bash Engine.sh --redeploy-agent-binaries
```

Command elevates through `sudo` when current shell is not root. It reads existing Engine mode and network profile, so no `--network-mode` value is needed.

CLI reports build, artifact ID, scheduler drain, every candidate worker, Service cutover, health result, and old-worker retirement. Worker replacement uses this order:

1. Wait for running scheduler work to finish, then pause scheduler reconciliation.
2. Start every outdated candidate while old workers stay live.
3. Require Kubernetes readiness plus direct site-worker `/health` response.
4. Switch stable per-site ClusterIP Service to candidate.
5. Require EndpointSlice convergence plus HTTP health through Service.
6. Update paused scheduler to new desired worker image.
7. Remove old workers, verify replacement scheduler registrations remain active, restore scheduler, and finalize image manifest.

Traefik dynamic routes keep same ClusterIP target. No Traefik reload or route-file rewrite occurs during cutover.

!!! warning "Safe stopping points"
    Failure before scheduler image commit restores old Service selectors, removes candidates, and resumes old scheduler. Failure after commit keeps validated candidates live and resumes scheduler only when Deployment already names new desired image. Read `Engine/Deploy/build.log` before manual recovery.

!!! info "Install delivery"
    Agent ZIP lives in API cache, not inside site worker. Running API hot-loads release-channel file from mounted Engine state. Worker rotation synchronizes site-worker runtime image and proves routing; it does not require API or full Engine redeploy.

!!! warning "Repository binary history cleanup"
    `Data/Agent/dist/` is generated output and should not stay in future commits. Removing older binary blobs from Git history requires coordinated repository maintenance, not a normal Engine deploy. Do this only after open PRs are merged or rebased, protected branches are ready for a force-push window, and operators know they must refresh local clones.

    ```sh
    # Install git-filter-repo if it is not already available.
    python3 -m pip install --user git-filter-repo

    # Run from a fresh clone or maintenance clone.
    git filter-repo \
      --path Data/Agent/dist/windows-amd64/Agent.exe \
      --path Data/Agent/dist/linux-amd64/Agent \
      --invert-paths

    # Push rewritten refs only during an approved maintenance window.
    git push --force-with-lease origin main
    git push --force-with-lease --tags origin
    ```

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Updating the Engine](updating-the-engine.md)
    - [WebUI HMR Development](webui-hmr-development.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - Service-scoped commands go through `Engine.sh` so K3s reconciliation, retired Compose manifest state, env loading, and service role detection remain consistent.
    - `api-backend restart` is enough for most backend-only config and code reload checks after a container image already exists.
    - `webui-frontend restart` restarts the K3s WebUI Deployment without rebuilding image layers.
    - `webui-frontend rebuild prod` rebuilds the production WebUI image and reconciles the K3s WebUI workload.
    - `webui-frontend rebuild dev` syncs staged WebUI source, rebuilds the development image, and keeps Vite/HMR behavior available in the K3s WebUI workload.
    - `wireguard-tunnel reconcile` runs through the K3s tunnel pod and repairs Engine-side tunnel state without requiring a full stack redeploy.
    - `--redeploy-agent-binaries` builds `Data/Agent` through `Data/Agent/build-agent.sh`, packages cache atomically, verifies API hostPath visibility, builds/imports current site-worker image, drains active scheduler work, pauses scheduler, and performs readiness-first ClusterIP selector cutovers for outdated workers.
    - Agent binary redeploy allows old and new immutable site-worker images during preparation. After successful cutover, operator allowlist returns to new image only.
    - Candidate workers get HTTP readiness/liveness probes against `/health`. Old worker pods remain intact until every candidate passes direct and post-Service checks. Candidate heartbeat owns worker and route rows by container incarnation, preventing delayed old-worker exit from retiring replacement state.
    - Pre-commit exit trap restores old selectors and scheduler. Once paused scheduler desired image changes, recovery keeps new traffic path instead of attempting unsafe mixed-image rollback.
    - History cleanup is intentionally manual. Codex must not run `git filter-repo`, force-push rewritten refs, or remove branch history unless the operator explicitly approves that maintenance window.
