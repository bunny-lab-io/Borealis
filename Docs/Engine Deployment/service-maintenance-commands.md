# Service Maintenance Commands
Use service-scoped commands when troubleshooting one Engine component without redeploying the full Borealis stack.

!!! info "When to use these"
    These commands are useful after config edits, WebUI rebuilds, Traefik routing changes, or WireGuard tunnel issues. Use the normal Engine update path when you want to pull and redeploy the entire platform.

```sh
# Restart the API backend container.
./Engine.sh --service api-backend restart

# Rebuild the WebUI frontend container in production mode.
./Engine.sh --service webui-frontend rebuild prod

# Rebuild the WebUI frontend container in development mode.
./Engine.sh --service webui-frontend rebuild dev

# Reload Traefik edge configuration.
./Engine.sh --service traefik-edge reload

# Reconcile WireGuard tunnel state to fix agent tunnel connections.
./Engine.sh --service wireguard-tunnel reconcile
```

!!! warning "Service commands are targeted"
    A service command only touches the named component. If multiple services changed, run the normal deployment command from [Updating the Engine](updating-the-engine.md).

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Updating the Engine](updating-the-engine.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - Service-scoped commands go through `Engine.sh` so Compose project naming, env loading, and service role detection remain consistent.
    - `api-backend restart` is enough for most backend-only config and code reload checks after a container image already exists.
    - `webui-frontend rebuild prod` recreates the static WebUI service for production.
    - `webui-frontend rebuild dev` keeps Vite/HMR behavior available for development deployments.
    - `wireguard-tunnel reconcile` repairs Engine-side tunnel state without requiring a full stack redeploy.
