# Reverse Tunnel Updates Checklist

Keep these tasks aligned with `Docs/Codex/REVERSE_TUNNELS.md` and the current Engine/Agent implementations.

- [ ] **Signed tokens only**: Require Ed25519 signing when issuing tunnel tokens and have both Engine and Agent reject unsigned tokens (no unsigned fallbacks).
- [ ] **Agent-targeted start/stop**: Emit `reverse_tunnel_start/stop` to the intended agent only (Socket.IO room or equivalent), not a broadcast.
- [ ] **Close per-lease listeners**: When a lease ends (stop/idle/grace/agent disconnect), close the WebSocket server bound to that lease port and free it.
- [ ] **Enforce idle/grace fully**: Lease sweeper should call `stop_tunnel` for expired/idle leases; Agent watchdog should treat `expires_at` as an absolute cutoff (no doubled grace).
- [ ] **TLS required**: Refuse to start tunnel listeners without cert/key (or pinned bundle); disable plaintext listeners and surface clear errors.

Out of scope (per current decision): payload size limits and backpressure changes.
