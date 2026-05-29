# Updating the Engine
Use this page when updating an existing Borealis Engine host from the Git repository.

!!! info "Expected path"
    Run these commands from the Engine host. They pull current staging files and redeploy the production container stack.

```sh
cd /opt/Borealis

# Pull down changed Engine staging files.
git pull --ff-only

# Redeploy updated Engine containers.
./Engine.sh deploy prod
```

!!! warning "Local changes"
    `git pull --ff-only` stops if local files changed. Review those changes before updating so Engine deployment does not mix local edits with upstream changes.

??? note "Optional: Development Redeploy"
    Use development mode only when testing WebUI or Engine changes interactively.

    ```sh
    cd /opt/Borealis
    git pull --ff-only
    ./Engine.sh deploy dev
    ```

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Engine Deployment](deploying-the-engine.md)
    - [Service Maintenance Commands](engine-maintenance-commands.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Runtime behavior

    - `Engine.sh deploy prod` stages source, checks dependencies, builds changed images, writes deploy manifests, and runs Docker Compose under the Borealis project name.
    - Production mode serves the static WebUI from the WebUI frontend container and routes public traffic through Traefik.
    - Development mode keeps the same stack shape but runs the WebUI through Vite/HMR behind Traefik.
