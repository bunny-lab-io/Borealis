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
