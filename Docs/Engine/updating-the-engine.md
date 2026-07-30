# Updating the Engine
Use this page when updating an existing Borealis Engine host from the Git repository.

!!! info "Expected path"
    Run these commands from the Engine host. They pull current staging files and redeploy the production container stack. Keep the same network mode used during install. Use `sudo` for `Engine.sh` unless the shell user can access `/var/run/docker.sock`.

=== "Public"

    ```sh
    cd /opt/Borealis

    # Pull down changed Engine staging files.
    git pull --ff-only

    # Redeploy updated Engine containers.
    sudo bash Engine.sh --network-mode public deploy prod
    ```

=== "Local"

    ```sh
    cd /opt/Borealis

    # Pull down changed Engine staging files.
    git pull --ff-only

    # Redeploy updated Engine containers.
    sudo bash Engine.sh --network-mode local deploy prod
    ```

!!! warning "Local changes"
    `git pull --ff-only` stops if local files changed. Review those changes before updating so Engine deployment does not mix local edits with upstream changes.

??? note "Optional: Development Redeploy"
    Use development mode only when testing WebUI or Engine changes interactively. Development deploys refresh the HMR runtime source from staged WebUI source under `Data/Engine/Containers/webui-frontend/data/web-interface/`.

    For frontend-only work, prefer the scoped HMR workflow in [WebUI HMR Development](webui-hmr-development.md). It keeps API, scheduler, PostgreSQL, Traefik, WireGuard, and guacd workloads untouched when their inputs have not changed.

    === "Public"

        ```sh
        cd /opt/Borealis
        git pull --ff-only
        sudo bash Engine.sh --network-mode public deploy dev
        ```

    === "Local"

        ```sh
        cd /opt/Borealis
        git pull --ff-only
        sudo bash Engine.sh --network-mode local deploy dev
        ```

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Engine Deployment](deploying-the-engine.md)
    - [WebUI HMR Development](webui-hmr-development.md)
    - [Service Maintenance Commands](engine-maintenance-commands.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - `Engine.sh --network-mode public|local deploy prod` stages source, checks dependencies, builds changed images, writes deploy manifests, reconciles K3s-owned workloads, and keeps Docker Compose retired through the empty manifest/policy check.
    - Production mode serves the static WebUI from the K3s `webui-frontend` workload through the existing Traefik edge. The retired Compose WebUI container is removed during deploy.
    - Development mode keeps the same stack shape, syncs staged WebUI source into `Engine/Services/webui-frontend/data/web-interface/`, and runs the WebUI through Vite/HMR behind Traefik.
