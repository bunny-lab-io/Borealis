# Integrations

Document external integrations used by Borealis, primarily the GitHub repository hash service.

## GitHub Integration (Repository Hash)
- The Engine can query GitHub for the latest commit hash of a repository/branch.
- Results are cached locally to reduce API usage.
- Admins can store a GitHub API token via the WebUI.
- Agent installation and update do not use GitHub integration. Engine builds and serves Agent binaries from local `Data/Agent` source.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/github/token` (Admin) - GitHub token status.
    - `POST /api/github/token` (Admin) - update GitHub token.
    - `GET /api/repo/current_hash` (Device or Token Authenticated) - current repo hash.

    ### Related documentation

    - [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
    - [API Reference](api-reference.md)
    - [Engine Log Access](../../Using%20the%20Platform/engine-log-management.md)

    ### Integration implementation
    - `Data/Engine/Containers/api-backend/cmd/api-backend/repo_hash.go` owns repository-head lookup and cache behavior.
    - `Data/Engine/Containers/api-backend/cmd/api-backend/github_token.go` owns stored token management and verification.
    - The integration uses:
      - Cached results stored in `repo_hash_cache.json` (under the Engine cache directory).
      - Token storage in the `github_token` PostgreSQL table.

    ### Defaults and overrides
    - Default repo: `bunny-lab-io/Borealis`.
    - Default branch: `main`.
    - Environment overrides:
      - `BOREALIS_REPO`
      - `BOREALIS_REPO_BRANCH`
    - Cache TTL can be overridden via Engine config (`repo_hash_refresh`).

    ### Token management
    - Admins manage tokens via `/api/github/token`.
    - The token is stored in the Engine database (`github_token` table).
    - `GitHubIntegration.verify_token()` reports validity and rate-limit status.

    ### `GET /api/repo/current_hash`
    - This endpoint uses the cached GitHub integration to return a hash for `repo`, `branch`, and `ttl` query parameters.
    - Branch refs with slashes are resolved through GitHub's commit endpoint with URL-encoded refs for Engine repository operations.
    - Agent install/update paths do not call this endpoint or resolve repository branches.
    - It supports device-auth and operator-auth contexts.
    - Device/operator bearer tokens authenticate the Borealis request only. GitHub calls use stored Engine token, `X-GitHub-Token`, or environment token.
    - Useful for Engine repository diagnostics and Engine branch-aware operations.

    ### Debug checklist
    - Token missing: call `/api/github/token` as Admin and confirm `has_token`.
    - API rate limit errors: inspect the response payload for `rate_limit` fields.
    - Cache stale: send `refresh=force` to repository-hash endpoint or inspect Go cache path.
