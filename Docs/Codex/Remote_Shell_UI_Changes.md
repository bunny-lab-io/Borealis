# Remote Shell UI Changes Handoff

You are a new ChatGPT Codex agent working in `d:\Github\Borealis`. Start by reading
`AGENTS.md`, then follow the doc chain it specifies. Also read
`Docs/Codex/TOAST_NOTIFICATIONS.md` to implement toast notifications correctly.

## Current Situation
- The WireGuard tunnel and Remote Shell work once the agent SYSTEM socket is online.
- If the operator clicks **Connect** too early, the UI shows `agent_socket_missing`
  and no toast appears.
- Goal: prevent the Remote Shell connect attempt until the agent is actually ready,
  and show a toast notification if the operator clicks too early.

## Required Behavior
- When the agent SYSTEM socket is not registered, the UI must block the connection
  attempt, show a toast via `/api/notifications/notify`, and keep the UI idle
  (no tunnel/session attempt).
- Toast title: `Agent Onboarding Underway`
- Toast message:
  `Please wait for the agent to finish onboarding into Borealis. It takes about 1 minute to finish the process.`

## Important References
- `Docs/Codex/TOAST_NOTIFICATIONS.md` (toast API path, payload schema, auth, Socket.IO event)
- `AGENTS.md` (instructions and precedence)
- UI file: `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx`
- API status endpoint: `/api/tunnel/status` returns `agent_socket` when available
- Socket error path: `agent_socket_missing`

## Troubleshooting Context
- Engine logs show `vpn_shell_open_failed ... reason=agent_socket_missing` when the
  SYSTEM socket is not connected.
- Toasts do not appear; likely causes: WebUI build is reused (`Existing WebUI build found`)
  or the UI error path doesn't trigger the toast.
- Ensure the toast is sent via `/api/notifications/notify` with `credentials: "include"`
  and the payload schema from `TOAST_NOTIFICATIONS.md`.

## Deliverables
- Update UI logic to call the notification API and block the connection attempt until
  readiness is confirmed.
- Cover both preflight status checks and the `agent_socket_missing` shell open response.
- Provide explicit rebuild/restart steps if the WebUI build must be refreshed.
