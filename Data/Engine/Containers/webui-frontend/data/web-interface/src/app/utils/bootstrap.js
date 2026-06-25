export const EMPTY_BOOTSTRAP_STATE = {
  phase: "loading",
  configured: false,
  locked: false,
  apiAvailable: false,
};

export function normalizeBootstrapState(payload) {
  return {
    phase: String(payload?.phase || EMPTY_BOOTSTRAP_STATE.phase),
    configured: Boolean(payload?.configured),
    locked: Boolean(payload?.locked),
    apiAvailable: true,
  };
}

export function engineLoadingBootstrapState() {
  return {
    ...EMPTY_BOOTSTRAP_STATE,
    phase: "loading",
    apiAvailable: false,
  };
}

export function bootstrapBlocksLogin(bootstrapState) {
  return String(bootstrapState?.phase || "") !== "login_required";
}
