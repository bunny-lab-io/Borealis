# Service Maintenance Commands
Use service-scoped commands when troubleshooting one Engine component.

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