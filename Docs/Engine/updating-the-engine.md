# Updating the Engine
Use this page when moving an existing standalone Borealis Engine host to exact stable release.

!!! warning "Clustered Engines"
    Do not run host-by-host `git pull` or direct production deploy on clustered Engine. Use **Admin > Cluster Management > Updates** so Borealis pins immutable release SHA, drains one node, transfers roles, validates candidate, and restores service before moving to next node. See [Managing Engine Clusters](managing-engine-clusters.md).

!!! info "Expected path"
    Run these commands from Engine host. Choose release version explicitly and keep same network mode used during install. Standalone bootstrap verifies selected identity but does not choose version for operator; Cluster Management separately limits cluster choices to compatible same-or-newer releases.

=== "Public"

    ```sh
    BOREALIS_RELEASE="YYYY.MM.REVISION"
    curl --fail --location --proto '=https' --tlsv1.2 \
      --output Install-Engine.sh \
      "https://github.com/bunny-lab-io/Borealis/releases/download/${BOREALIS_RELEASE}/Install-Engine.sh"
    less Install-Engine.sh
    sudo bash Install-Engine.sh --release "${BOREALIS_RELEASE}" --network-mode public
    ```

=== "Local"

    ```sh
    BOREALIS_RELEASE="YYYY.MM.REVISION"
    curl --fail --location --proto '=https' --tlsv1.2 \
      --output Install-Engine.sh \
      "https://github.com/bunny-lab-io/Borealis/releases/download/${BOREALIS_RELEASE}/Install-Engine.sh"
    less Install-Engine.sh
    sudo bash Install-Engine.sh --release "${BOREALIS_RELEASE}" --network-mode local
    ```

Installer requires stable `YYYY.MM.REVISION` or `YYYY.MM.REVISION.HOTFIX` tag backed by published, non-prerelease, immutable GitHub release. It verifies release assets and exact tag commit before replacing checked-out source. No stable update command resolves `latest` or falls back to `main`.

!!! warning "Local changes"
    Stable update resets tracked source to selected release commit and removes untracked source outside preserved `Engine`, `Engine.old`, and `Agent` runtime roots. Review and commit development changes before update.

!!! info "GitHub unavailable"
    New release install or update stops before source changes when GitHub release metadata or assets cannot be verified. Existing standalone Engine can still reconcile its already checked-out release with local `sudo bash Engine.sh --network-mode public|local deploy prod` command.

Every redeploy rebuilds Engine-hosted Agent artifact from `Data/Agent` on Engine host. Sites install links and Agent updates use that artifact exclusively; Borealis does not publish or retrieve Agent binaries through GitHub.

When only Agent source changed, use `bash Engine.sh --redeploy-agent-binaries` from [Engine Maintenance Commands](engine-maintenance-commands.md). It publishes current binaries and rotates outdated active site workers without rebuilding or redeploying unrelated Engine workloads.

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
    - [Publishing Engine Releases](publishing-engine-releases.md)
    - [Managing Engine Clusters](managing-engine-clusters.md)
    - [WebUI HMR Development](webui-hmr-development.md)
    - [Service Maintenance Commands](engine-maintenance-commands.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)

    ### Stable release publication

    Use [Publishing Engine Releases](publishing-engine-releases.md) for qualification tags, stable draft packaging, asset inspection, immutable publication, and correction rules. Do not duplicate release-creation commands here; this page covers Engine consumer update path.

    ### Runtime behavior

    - Stable update starts from exact `Install-Engine.sh` release asset. Bootstrap requires immutable, published, non-prerelease GitHub release; verifies GitHub asset digests plus manifest release, repository, Linux platform, artifact URLs, sizes, and hashes; resolves tag commit SHA; then calls `Engine.sh --release VERSION --release-sha COMMIT_SHA`.
    - Stable `Engine.sh` sync fetches exact tag and refuses missing, malformed, or mismatched release/SHA identity. It never resolves latest tag and never falls back to mutable branch.
    - Development repo sync requires explicit `--release-channel unstable`; ref defaults to `main` when developer does not provide `--repo-branch`.
    - `Engine.sh --network-mode public|local deploy prod` stages source, checks dependencies, builds the local Agent installer cache from `Data/Agent/build-agent.sh`, builds changed images, writes deploy manifests, reconciles K3s-owned workloads, and keeps Docker Compose retired through the empty manifest/policy check.
    - `Engine.sh --redeploy-agent-binaries` is scoped Agent maintenance. It uses existing deployed mode/network state, does not rebuild API/WebUI/Traefik/PostgreSQL/WireGuard/guacd, and updates job-scheduler only to change desired site-worker image after validated blue/green cutover.
    - Production mode serves the static WebUI from the K3s `webui-frontend` workload through the existing Traefik edge. The retired Compose WebUI container is removed during deploy.
    - Development mode keeps the same stack shape, syncs staged WebUI source into `Engine/Services/webui-frontend/data/web-interface/`, and runs the WebUI through Vite/HMR behind Traefik.
