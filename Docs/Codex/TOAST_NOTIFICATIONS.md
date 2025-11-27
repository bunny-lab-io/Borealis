# Codex Guide: Toast Notifications (Borealis WebUI)

Use this guide to add, configure, and test transient toast notifications across Borealis. It documents the backend endpoint, frontend listener, payload contract, and quick Firefox console commands you can hand to operators for validation.

## Components & Paths
- Backend endpoint: `Data/Engine/services/API/notifications/management.py` (registered as `/api/notifications/notify`).
- Frontend listener + renderer: `Data/Engine/web-interface/src/Notifications.jsx` (mounted in `App.jsx`).
- Transport: Socket.IO event `borealis_notification` broadcast to connected WebUI clients.

## Backend Behavior
- Auth: Uses `RequestAuthContext.require_user()`; session/Bearer must be present. Returns `401/403` otherwise.
- Route: `POST /api/notifications/notify`
  - Emits `borealis_notification` over Socket.IO (no persistence).
  - Logs via `service_log("notifications", ...)`.
- Validation: Requires `message` in payload. `title` defaults to `"Notification"` if omitted.
- Registration: API group `notifications` is enabled by default via `DEFAULT_API_GROUPS` and `_GROUP_REGISTRARS` in `Data/Engine/services/API/__init__.py`.

## Payload Schema
Send JSON body (session-authenticated):
- `title` (string, optional): Heading line. Default `"Notification"`.
- `message` (string, required): Body copy.
- `icon` (string, optional): Material icon name hint (e.g., `info`, `filter`, `schedule`, `warning`, `error`). Falls back to `NotificationsActive`.
- `variant` (string, optional): Visual theme. Accepted: `info` | `warning` | `error` (case-insensitive). Aliases: `type` or `severity`. Defaults to `info`.
- `ttl_ms` (number, optional): Client-side lifetime in milliseconds; defaults to ~5200ms before fade-out.

Notes:
- Payload is fanned out verbatim to the WebUI (plus server-added fields: `id`, `username`, `role`, `created_at`).
- The client caps the visible stack to the 5 most recent items (newest on top).
- Non-empty `message` is mandatory; otherwise HTTP 400.

## Frontend Rendering Rules
- Component: `Notifications.jsx` listens to `borealis_notification` on `window.BorealisSocket`.
- Stack position: fixed top-right, high z-index, pointer events enabled on toasts only.
- Auto-dismiss: ~5s default; each item fades out and is removed.
- Theme by `variant`:
  - `info` (default): Borealis blue aurora gradient.
  - `warning`: Muted amber gradient.
  - `error`: Deep red gradient.
- Icon: No container; uses the provided Material icon hint. Small drop shadow for legibility.

## Implementation Steps (Recap)
1) Backend: Ensure `/api/notifications/notify` is registered (already in repo). New services should import `register_notifications` if API groups are customized.
2) Emit: From any authenticated server flow, POST to `/api/notifications/notify` with the payload above.
3) Frontend: `App.jsx` mounts `Notifications` globally; no per-page wiring needed.
4) Test: Use the Firefox console examples below while logged in to confirm toast rendering.

## Firefox Console Examples (run while signed in)
Info (default blue):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Test Notification",
    message: "Hello from the console!",
    icon: "info",
    variant: "info"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

Warning (amber):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Heads up",
    message: "This is a warning example.",
    icon: "warning",
    variant: "warning"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

Error (red):
```js
fetch("/api/notifications/notify", {
  method: "POST",
  credentials: "include",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    title: "Error encountered",
    message: "Something failed during processing.",
    icon: "error",
    variant: "error"
  })
}).then(r => r.json()).then(console.log).catch(console.error);
```

## Usage Notes & Tips
- Keep `message` concise; multiline is supported via `\n`.
- Use `icon` to match the source feature (e.g., `filter`, `schedule`, `device`, `error`).
- The server adds `username`/`role` to payloads; the client currently shows all variants regardless of role (filtering is per-username match when present).
- If sockets are unavailable, the endpoint still returns 200; toasts simply will not render until Socket.IO is connected.
