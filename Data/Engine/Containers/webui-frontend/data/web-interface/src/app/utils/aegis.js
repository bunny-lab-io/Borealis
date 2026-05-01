export const EMPTY_AEGIS_STATUS = {
  configured: false,
  locked: false,
  unlock_scope: "engine_global",
  secret_scope: ["credentials", "github_token", "operator_auth"],
  updated_at: 0,
};

export function normalizeAegisStatus(payload) {
  return {
    configured: Boolean(payload?.configured),
    locked: Boolean(payload?.locked),
    unlock_scope: payload?.unlock_scope || EMPTY_AEGIS_STATUS.unlock_scope,
    secret_scope: Array.isArray(payload?.secret_scope)
      ? payload.secret_scope
      : EMPTY_AEGIS_STATUS.secret_scope,
    updated_at: Number(payload?.updated_at) || 0,
  };
}
