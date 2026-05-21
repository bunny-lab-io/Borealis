# Integrations
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

## Purpose
Document external integrations used by Borealis, primarily the GitHub repository hash service.

## GitHub Integration (Repository Hash)
- The Engine can query GitHub for the latest commit hash of a repository/branch.
- Results are cached locally to reduce API usage.
- Admins can store a GitHub API token via the WebUI.
- The Sites install menu uses that stored token to list Borealis GitHub branches for branch-specific Agent bootstrap commands.

## API Endpoints
- `GET /api/github/token` (Admin) - GitHub token status.
- `POST /api/github/token` (Admin) - update GitHub token.
- `GET /api/repo/current_hash` (Device or Token Authenticated) - current repo hash.

## Related Documentation
- [Engine Runtime](../Core%20Runtimes/engine-runtime.md)
- [API Reference](api-reference.md)
- [Logging and Operations](../Operations%20and%20Remote%20Access/logging-and-operations.md)

## Codex Agent (Detailed)
### Integration implementation
- `Data/Engine/Containers/api-backend/data/integrations/github.py` implements `GitHubIntegration`.
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
- The Sites page branch picker reads the token through `/api/github/token`, calls GitHub's branch list API from the operator browser, and never stores the token in component state.
- `main` keeps install commands on the default raw URL without `--repo-branch`; any selected non-main branch changes the raw URL and adds `--repo-branch <branch>` to Linux/macOS/Windows agent commands.

### `GET /api/repo/current_hash`
- This endpoint uses the cached GitHub integration to return a hash for `repo`, `branch`, and `ttl` query parameters.
- Branch refs with slashes are resolved through GitHub's commit endpoint with URL-encoded refs, so feature branches such as `feature/agent-metadata-fields` work.
- It supports device-auth and operator-auth contexts.
- Device/operator bearer tokens authenticate the Borealis request only. GitHub calls use stored Engine token, `X-GitHub-Token`, or environment token.
- Useful for agent update checks and diagnostics.

### Debug checklist
- Token missing: call `/api/github/token` as Admin and confirm `has_token`.
- API rate limit errors: inspect the response payload for `rate_limit` fields.
- Cache stale: use the `force_refresh` behavior in `GitHubIntegration` (via config or code).
