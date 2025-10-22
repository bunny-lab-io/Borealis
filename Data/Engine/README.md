# Borealis Engine Overview

The Engine is an additive server stack that will ultimately replace the legacy Flask app under `Data/Server`.  It is safe to run the Engine entrypoint (`Data/Engine/bootstrapper.py`) side-by-side with the legacy server while we migrate functionality feature-by-feature.

## Environment configuration

The Engine mirrors the legacy defaults so it can boot without additional configuration.  These environment variables are read by `Data/Engine/config/environment.py`:

| Variable | Purpose | Default |
| --- | --- | --- |
| `BOREALIS_ROOT` | Overrides automatic project root detection.  Useful when running from a packaged location. | Directory two levels above `Data/Engine/` |
| `BOREALIS_DATABASE_PATH` | Path to the SQLite database. | `<project_root>/database.db` |
| `BOREALIS_STATIC_ROOT` | Directory that serves static assets for the SPA. | First existing path among `Data/Server/web-interface/build`, `Data/Server/WebUI/build`, `Data/WebUI/build` |
| `BOREALIS_CORS_ALLOWED_ORIGINS` | Comma-delimited list of origins granted CORS access. Use `*` for all origins. | `*` |
| `BOREALIS_FLASK_SECRET_KEY` | Secret key for Flask session signing. | `change-me` |
| `BOREALIS_DEBUG` | Enables debug logging, disables secure-cookie requirements, and allows Werkzeug debug mode. | `false` |
| `BOREALIS_HOST` | Bind address for the HTTP/Socket.IO server. | `127.0.0.1` |
| `BOREALIS_PORT` | Bind port for the HTTP/Socket.IO server. | `5000` |

## Logging expectations

`Data/Engine/config/logging.py` configures a timed rotating file handler that writes to `Logs/Server/engine.log`.  Each entry follows the `<timestamp>-engine-<message>` format required by the project logging policy.  The handler is attached to both the Engine logger (`borealis.engine`) and the root logger so that third-party frameworks share the same log destination.

## Bootstrapping flow

1. `Data/Engine/bootstrapper.py` loads the environment, configures logging, and builds the Flask application via `Data/Engine/server.py`.
2. Placeholder HTTP and Socket.IO registration hooks run so the Engine can start without any migrated routes yet.
3. The resulting runtime object exposes the Flask app, resolved settings, and optional Socket.IO server.  `bootstrapper.main()` runs the appropriate server based on whether Socket.IO is present.

As migration continues, services, repositories, interfaces, and integrations will live under their respective subpackages while maintaining isolation from the legacy server.

## Interface scaffolding

The Engine currently exposes placeholder HTTP blueprints under `Data/Engine/interfaces/http/` (agents, enrollment, tokens, admin, and health) so that future commits can drop in real routes without reshaping the bootstrap wiring. WebSocket namespaces follow the same pattern in `Data/Engine/interfaces/ws/`, with feature-oriented modules (e.g., `agents`, `job_management`) registered by `bootstrapper.bootstrap()` when Socket.IO is available. These stubs intentionally contain no business logic yet—they merely ensure the application factory exercises the full wiring path.
