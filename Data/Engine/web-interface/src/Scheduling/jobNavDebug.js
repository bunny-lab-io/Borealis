const LOG_KEY = "__BOREALIS_SCHEDULED_JOB_NAV_LOGS";
const HOOK_KEY = "__BOREALIS_SCHEDULED_JOB_NAV_HOOKS_INSTALLED";
const MAX_LOG_ENTRIES = 250;

const currentLocation = () => {
  if (typeof window === "undefined" || !window.location) return "";
  return `${window.location.pathname || ""}${window.location.search || ""}`;
};

const serializeError = (value) => {
  if (!value) return "";
  if (value instanceof Error) {
    return {
      name: value.name || "Error",
      message: value.message || "",
      stack: value.stack || "",
    };
  }
  if (typeof value === "object") {
    try {
      return JSON.parse(JSON.stringify(value));
    } catch {
      return String(value);
    }
  }
  return String(value);
};

export function logScheduledJobNav(source, event, payload = {}) {
  const entry = {
    ts: new Date().toISOString(),
    source,
    event,
    path: currentLocation(),
    ...payload,
  };

  if (typeof window !== "undefined") {
    const next = Array.isArray(window[LOG_KEY]) ? window[LOG_KEY] : [];
    next.push(entry);
    if (next.length > MAX_LOG_ENTRIES) {
      next.splice(0, next.length - MAX_LOG_ENTRIES);
    }
    window[LOG_KEY] = next;
    window.__BOREALIS_SCHEDULED_JOB_NAV_LAST = entry;
  }

  try {
    console.debug(`[ScheduledJobNav][${source}] ${event}`, entry);
  } catch {
    /* best-effort console logging */
  }

  return entry;
}

export function installScheduledJobNavGlobalErrorHooks() {
  if (typeof window === "undefined" || window[HOOK_KEY]) return;

  window.addEventListener("error", (event) => {
    logScheduledJobNav("window", "error", {
      message: event?.message || "",
      filename: event?.filename || "",
      lineno: Number(event?.lineno || 0),
      colno: Number(event?.colno || 0),
      error: serializeError(event?.error),
    });
  });

  window.addEventListener("unhandledrejection", (event) => {
    logScheduledJobNav("window", "unhandledrejection", {
      reason: serializeError(event?.reason),
    });
  });

  window[HOOK_KEY] = true;
  logScheduledJobNav("window", "debug_hooks_installed", { logKey: LOG_KEY });
}
