# WebUI HMR Development
Use this workflow when testing Engine WebUI changes on a K3s-based Borealis Engine without rebuilding or rolling every Engine workload for each JSX/CSS edit.

## Requirements
- Run commands from the Engine host checkout, usually `/opt/Borealis`.
- Use the same `--network-mode` value that was used for the Engine install.
- Keep durable WebUI source under `Data/Engine/Containers/webui-frontend/data/web-interface/`.
- Treat `Engine/Services/webui-frontend/data/web-interface/` as disposable runtime source for live HMR sessions.

## Start Dev WebUI
Use a scoped WebUI rebuild when the Engine stack already exists and only the frontend needs dev mode.

=== "Local"

    ```sh
    cd /opt/Borealis
    ./Engine.sh --network-mode local --service webui-frontend rebuild dev
    ```

=== "Public"

    ```sh
    cd /opt/Borealis
    ./Engine.sh --network-mode public --service webui-frontend rebuild dev
    ```

Use full dev deploy when shared Engine configuration changed or when switching a stale stack into dev mode:

```sh
cd /opt/Borealis
./Engine.sh --network-mode local deploy dev
```

## Edit Loop
After dev WebUI starts, the K3s WebUI pod reads source from:

```text
Engine/Services/webui-frontend/data/web-interface/
```

Edit files there for fastest HMR feedback. Vite serves changes through the normal Borealis HTTPS URL, and browser HMR connects through:

```text
wss://<engine-fqdn>/__vite_hmr
```

When edits are ready to keep, make the same source changes under:

```text
Data/Engine/Containers/webui-frontend/data/web-interface/
```

Then run the scoped dev rebuild again to refresh the runtime copy from committed source:

```sh
./Engine.sh --network-mode local --service webui-frontend rebuild dev
```

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

## Return To Production
Switch WebUI back to production static serving after HMR work:

```sh
cd /opt/Borealis
./Engine.sh --network-mode local --service webui-frontend rebuild prod
```

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Updating the Engine](updating-the-engine.md)
    - [Engine Maintenance Commands](engine-maintenance-commands.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - `webui-frontend rebuild dev` syncs `Data/Engine/Containers/webui-frontend/data/web-interface/` into `Engine/Services/webui-frontend/data/web-interface/`, builds the WebUI dev image target when its declared inputs changed, imports that image into K3s containerd when missing, and reconciles only the WebUI workload when the WebUI config hash changed.
    - Dev mode sets `BOREALIS_WEBUI_MODE=dev`, starts Vite from the WebUI container entrypoint, and binds inside the pod on `0.0.0.0:8000` so the K3s `webui-frontend` ClusterIP Service can route traffic. Health checks still target `127.0.0.1:8000` inside the pod.
    - `vite.config.mts` enables the public-edge HMR proxy path when `BOREALIS_DEV_UI_PROXY_ENABLED=1`. The browser uses `wss://<engine-fqdn>/__vite_hmr`; Traefik's WebUI catch-all route forwards that websocket to the K3s WebUI Service.
    - Dev source mounts are read-only hostPath mounts from `Engine/Services/webui-frontend/data/web-interface/` into `/opt/Borealis/Data/Engine/web-interface/`. Vite optimizer and temporary config output use memory-backed `emptyDir` mounts at `node_modules/.vite` and `node_modules/.vite-temp`.
    - Do not run raw `npm`, `vite`, or `vitest` from staged source under `Data/Engine/Containers/webui-frontend/data/web-interface/`. Use `Engine.sh` for deploy/rebuild validation and `./Engine_Unit_Tests.sh --domain webui` for WebUI unit tests when the runtime test cache exists.
