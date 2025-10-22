# Borealis Engine Overview

The Engine is an additive server stack that will ultimately replace the legacy Flask app under `Data/Server`.  It is safe to run the Engine entrypoint (`Data/Engine/bootstrapper.py`) side-by-side with the legacy server while we migrate functionality feature-by-feature.

## Architectural roles

The Engine is organized around explicit dependency layers so each concern stays
testable and replaceable:

- **Configuration (`Data/Engine/config/`)** parses environment variables into
  immutable settings objects that the bootstrapper hands to factories and
  integrations.
- **Builders (`Data/Engine/builders/`)** transform external inputs (HTTP
  headers, JSON payloads, scheduled job definitions) into validated immutable
  records that services can trust.
- **Domain models (`Data/Engine/domain/`)** house pure value objects, enums, and
  error types with no I/O so services can express intent without depending on
  Flask or SQLite.
- **Repositories (`Data/Engine/repositories/`)** encapsulate all SQLite access
  and expose protocol methods that return domain models.  They are injected into
  services through the container so persistence can be swapped or mocked.
- **Services (`Data/Engine/services/`)** host business logic such as device
  authentication, enrollment, job scheduling, GitHub artifact lookups, and
  real-time agent coordination.  Services depend only on repositories,
  integrations, and builders.
- **Integrations (`Data/Engine/integrations/`)** wrap external systems (GitHub
  today) and keep HTTP/token handling outside the services that consume them.
- **Interfaces (`Data/Engine/interfaces/`)** provide thin HTTP/Socket.IO
  adapters that translate requests to builder/service calls and serialize
  responses.  They contain no business rules of their own.

The runtime factory (`Data/Engine/runtime.py`) wires these layers together and
attaches the resulting container to the Flask app created in
`Data/Engine/server.py`.

## Environment configuration

The Engine mirrors the legacy defaults so it can boot without additional configuration.  These environment variables are read by `Data/Engine/config/environment.py`:

| Variable | Purpose | Default |
| --- | --- | --- |
| `BOREALIS_ROOT` | Overrides automatic project root detection.  Useful when running from a packaged location. | Directory two levels above `Data/Engine/` |
| `BOREALIS_DATABASE_PATH` | Path to the SQLite database. | `<project_root>/database.db` |
| `BOREALIS_ENGINE_AUTO_MIGRATE` | Run Engine-managed schema migrations during bootstrap (`true`/`false`). | `true` |
| `BOREALIS_STATIC_ROOT` | Directory that serves static assets for the SPA. | First existing path among `Data/Server/web-interface/build`, `Data/Server/WebUI/build`, `Data/WebUI/build` |
| `BOREALIS_CORS_ALLOWED_ORIGINS` | Comma-delimited list of origins granted CORS access. Use `*` for all origins. | `*` |
| `BOREALIS_FLASK_SECRET_KEY` | Secret key for Flask session signing. | `change-me` |
| `BOREALIS_DEBUG` | Enables debug logging, disables secure-cookie requirements, and allows Werkzeug debug mode. | `false` |
| `BOREALIS_HOST` | Bind address for the HTTP/Socket.IO server. | `127.0.0.1` |
| `BOREALIS_PORT` | Bind port for the HTTP/Socket.IO server. | `5000` |
| `BOREALIS_REPO` | Default GitHub repository (`owner/name`) for artifact lookups. | `bunny-lab-io/Borealis` |
| `BOREALIS_REPO_BRANCH` | Default branch tracked by the Engine GitHub integration. | `main` |
| `BOREALIS_REPO_HASH_REFRESH` | Seconds between default repository head refresh attempts (clamped 30-3600). | `60` |
| `BOREALIS_CACHE_DIR` | Directory used to persist Engine cache files (GitHub repo head cache). | `<project_root>/Data/Engine/cache` |

## Logging expectations

`Data/Engine/config/logging.py` configures a timed rotating file handler that writes to `Logs/Server/engine.log`.  Each entry follows the `<timestamp>-engine-<message>` format required by the project logging policy.  The handler is attached to both the Engine logger (`borealis.engine`) and the root logger so that third-party frameworks share the same log destination.

## Bootstrapping flow

1. `Data/Engine/bootstrapper.py` loads the environment, configures logging, prepares the SQLite connection factory, optionally applies schema migrations, and builds the Flask application via `Data/Engine/server.py`.
2. A service container is assembled (`Data/Engine/services/container.py`) that wires repositories, JWT/DPoP helpers, and Engine services (device auth, token refresh, enrollment). The container is stored on the Flask app for interface modules to consume.
3. HTTP and Socket.IO interfaces register against the new service container.  The resulting runtime object exposes the Flask app, resolved settings, optional Socket.IO server, and the configured database connection factory.  `bootstrapper.main()` runs the appropriate server based on whether Socket.IO is present.

As migration continues, services, repositories, interfaces, and integrations will live under their respective subpackages while maintaining isolation from the legacy server.

## Python dependencies

`Data/Engine/requirements.txt` mirrors the minimal runtime stack (Flask, Flask-SocketIO, CORS, requests, PyJWT, and cryptography) needed by the Engine entrypoint.  The PowerShell launcher consumes this file when preparing the `Engine/` virtual environment so parity tests always run against an environment with the expected web and security packages preinstalled.

## HTTP interfaces

The Engine now exposes working HTTP routes alongside the remaining scaffolding:

- `Data/Engine/interfaces/http/health.py` implements `GET /health` for liveness probes.
- `Data/Engine/interfaces/http/tokens.py` ports the refresh-token endpoint (`POST /api/agent/token/refresh`) using the Engine `TokenService` and request builders.
- `Data/Engine/interfaces/http/enrollment.py` handles the enrollment handshake (`/api/agent/enroll/request` and `/api/agent/enroll/poll`) with rate limiting, nonce protection, and repository-backed approvals.
- The admin and agent blueprints remain placeholders until their services migrate.

## WebSocket interfaces

Step 9 introduces real-time handlers backed by the new service container:

- `Data/Engine/services/realtime/agent_registry.py` manages connected-agent state, last-seen persistence, collector updates, and screenshot caches without sharing globals with the legacy server.
- `Data/Engine/interfaces/ws/agents/events.py` ports the agent namespace, handling connect/disconnect logging, heartbeat reconciliation, screenshot relays, macro status broadcasts, and provisioning lookups through the realtime service.
- `Data/Engine/interfaces/ws/job_management/events.py` now forwards scheduler updates and responds to job status requests, keeping WebSocket clients informed as new runs are simulated.

The WebSocket factory (`Data/Engine/interfaces/ws/__init__.py`) now accepts the Engine service container so namespaces can resolve dependencies just like their HTTP counterparts.

## Authentication services

Step 6 introduces the first real Engine services:

- `Data/Engine/builders/device_auth.py` normalizes headers for access-token authentication and token refresh payloads.
- `Data/Engine/builders/device_enrollment.py` prepares enrollment payloads and nonce proof challenges for future migration steps.
- `Data/Engine/services/auth/device_auth_service.py` ports the legacy `DeviceAuthManager` into a repository-driven service that emits `DeviceAuthContext` instances from the new domain layer.
- `Data/Engine/services/auth/token_service.py` issues refreshed access tokens while enforcing DPoP bindings and repository lookups.

Interfaces now consume these services via the shared container, keeping business logic inside the Engine service layer while HTTP modules remain thin request/response translators.

## SQLite repositories

Step 7 ports the first persistence adapters into the Engine:

- `Data/Engine/repositories/sqlite/device_repository.py` exposes `SQLiteDeviceRepository`, mirroring the legacy device lookups and automatic record recovery used during authentication.
- `Data/Engine/repositories/sqlite/token_repository.py` provides `SQLiteRefreshTokenRepository` for refresh-token validation, DPoP binding management, and usage timestamps.
- `Data/Engine/repositories/sqlite/enrollment_repository.py` surfaces enrollment install-code counters and device approval records so future services can operate without touching raw SQL.

Each repository accepts the shared `SQLiteConnectionFactory`, keeping all SQL execution confined to the Engine layer while services depend only on protocol interfaces.

## Job scheduling services

Step 10 migrates the foundational job scheduler into the Engine:

- `Data/Engine/builders/job_fabricator.py` transforms stored job definitions into immutable manifests, decoding scripts, resolving environment variables, and preparing execution metadata.
- `Data/Engine/repositories/sqlite/job_repository.py` encapsulates scheduled job persistence, run history, and status tracking in SQLite.
- `Data/Engine/services/jobs/scheduler_service.py` runs the background evaluation loop, emits Socket.IO lifecycle events, and exposes CRUD helpers for the HTTP and WebSocket interfaces.
- `Data/Engine/interfaces/http/job_management.py` mirrors the legacy REST surface for creating, updating, toggling, and inspecting scheduled jobs and their run history.

The scheduler service starts automatically from `Data/Engine/bootstrapper.py` once the Engine runtime builds the service container, ensuring a no-op scheduling loop executes independently of the legacy server.

## GitHub integration

Step 11 migrates the GitHub artifact provider into the Engine:

- `Data/Engine/integrations/github/artifact_provider.py` caches branch head lookups, verifies API tokens, and optionally refreshes the default repository in the background.
- `Data/Engine/repositories/sqlite/github_repository.py` persists the GitHub API token so HTTP handlers do not speak to SQLite directly.
- `Data/Engine/services/github/github_service.py` coordinates token caching, verification, and repo head lookups for both HTTP and background refresh flows.
- `Data/Engine/interfaces/http/github.py` exposes `/api/repo/current_hash` and `/api/github/token` through the Engine stack while keeping business logic in the service layer.

The service container now wires `github_service`, giving other interfaces and background jobs a clean entry point for GitHub functionality.

## Final parity checklist

Step 12 tracks the final integration work required before switching over to the
Engine entrypoint. Use the detailed playbook in
[`Data/Engine/STAGING_GUIDE.md`](./STAGING_GUIDE.md) to coordinate each
staging run:

1. Stand up the Engine in a staging environment and exercise enrollment, token
   refresh, scheduler operations, and the agent real-time channel side-by-side
   with the legacy server.
2. Capture any behavioural differences uncovered during staging using the
   divergence table in the staging guide and file them for follow-up fixes
   before the cut-over.
3. When satisfied with parity, coordinate the entrypoint swap (point production
   tooling at `Data/Engine/bootstrapper.py`) and plan the deprecation of
   `Data/Server`.

## Performing unit tests

Targeted unit tests cover the most important domain, builder, repository, and
migration behaviours without requiring Flask or external services.  Run them
with the standard library test runner:

```bash
python -m unittest discover Data/Engine/tests
```

The suite currently validates:

- Domain normalization helpers for GUIDs, fingerprints, and authentication
  failures.
- Device authentication and refresh-token builders, including error handling for
  malformed requests.
- SQLite schema migrations to ensure the Engine can provision required tables in
  a fresh database.

Successful execution prints a summary similar to:

```
.............
----------------------------------------------------------------------
Ran 13 tests in <N>.<M>s

OK
```

Additional tests should follow the same pattern and live under
`Data/Engine/tests/` so this command remains the single entry point for Engine
unit verification.
