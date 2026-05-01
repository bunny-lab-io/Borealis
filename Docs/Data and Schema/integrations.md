# Integrations
[Back to Docs Index](index.md) | [Index (HTML)](index.html)

## Purpose
Document external integrations used by Borealis, primarily the GitHub repository hash service.

## GitHub Integration (Repository Hash)
- The Engine can query GitHub for the latest commit hash of a repository/branch.
- Results are cached locally to reduce API usage.
- Admins can store a GitHub API token via the WebUI.

## API Endpoints
- `GET /api/github/token` (Admin) - GitHub token status.
- `POST /api/github/token` (Admin) - update GitHub token.
- `GET /api/repo/current_hash` (Device or Token Authenticated) - current repo hash.

## Related Documentation
- [Engine Runtime](engine-runtime.md)
- [API Reference](api-reference.md)
- [Logging and Operations](logging-and-operations.md)

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

### `GET /api/repo/current_hash`
- This endpoint uses the cached GitHub integration to return a hash.
- It supports device-auth and operator-auth contexts.
- Useful for agent update checks and diagnostics.

### Debug checklist
- Token missing: call `/api/github/token` as Admin and confirm `has_token`.
- API rate limit errors: inspect the response payload for `rate_limit` fields.
- Cache stale: use the `force_refresh` behavior in `GitHubIntegration` (via config or code).
