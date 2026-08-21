import { io } from "socket.io-client";
import { installBorealisInputValidationGuard } from "../utils/inputValidation.js";

let bootstrapped = false;
const GO_REALTIME_EVENTS = [
  "agent_status_changed",
  "agent_update_progress_changed",
  "device_inventory_changed",
  "device_services_changed",
  "borealis_notification",
  "scheduled_job_patch_progress",
  "server_operator_presence_changed",
  "watchdog_incidents_changed",
  "device_watchdogs_changed",
];
const LEGACY_SOCKET_EVENTS = new Set([
  "agent_window_list",
  "agent_screenshot_task",
  "connect_error",
  "device_activity_changed",
  "device_processes_changed",
  "macro_status",
]);
const LEGACY_SOCKET_EMIT_EVENTS = new Set(["list_agent_windows"]);

function createBorealisSocketBridge() {
  const legacySocket = io(window.location.origin, {
    transports: ["websocket"],
    autoConnect: false,
    reconnection: false,
  });
  const handlers = new Map();
  let eventSource = null;
  let reconnectTimer = null;

  function rememberHandler(eventName, handler) {
    if (typeof handler !== "function") return;
    const existing = handlers.get(eventName) || new Set();
    existing.add(handler);
    handlers.set(eventName, existing);
  }

  function forgetHandler(eventName, handler) {
    const existing = handlers.get(eventName);
    if (!existing) return;
    if (typeof handler === "function") {
      existing.delete(handler);
    } else {
      existing.clear();
    }
    if (existing.size === 0) {
      handlers.delete(eventName);
    }
  }

  function dispatchLocal(eventName, payload) {
    const existing = handlers.get(eventName);
    if (!existing) return;
    for (const handler of Array.from(existing)) {
      try {
        handler(payload);
      } catch {
        /* listener errors stay local */
      }
    }
  }

  function closeOperatorEvents() {
    if (reconnectTimer) {
      window.clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  }

  function scheduleReconnect() {
    if (reconnectTimer) return;
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      connectOperatorEvents();
    }, 5000);
  }

  function connectOperatorEvents() {
    if (eventSource) return;
    eventSource = new EventSource("/api/realtime/events", { withCredentials: true });
    for (const eventName of GO_REALTIME_EVENTS) {
      eventSource.addEventListener(eventName, (event) => {
        let payload = {};
        try {
          payload = event?.data ? JSON.parse(event.data) : {};
        } catch {
          payload = {};
        }
        dispatchLocal(eventName, payload);
      });
    }
    eventSource.onerror = () => {
      closeOperatorEvents();
      scheduleReconnect();
    };
  }

  const bridge = {
    on(eventName, handler) {
      if (LEGACY_SOCKET_EVENTS.has(eventName)) {
        legacySocket.on(eventName, handler);
      }
      rememberHandler(eventName, handler);
      return bridge;
    },
    off(eventName, handler) {
      if (LEGACY_SOCKET_EVENTS.has(eventName)) {
        legacySocket.off(eventName, handler);
      }
      forgetHandler(eventName, handler);
      return bridge;
    },
    emit(eventName, ...args) {
      if (eventName === "operator_presence_sync" || eventName === "operator_presence_clear") {
        return false;
      }
      if (!LEGACY_SOCKET_EMIT_EVENTS.has(eventName)) {
        return false;
      }
      if (!legacySocket.connected) {
        legacySocket.connect?.();
      }
      return legacySocket.emit(eventName, ...args);
    },
    connect() {
      connectOperatorEvents();
      dispatchLocal("connect", { transport: "sse" });
      return bridge;
    },
    disconnect() {
      closeOperatorEvents();
      dispatchLocal("disconnect", { transport: "sse" });
      if (legacySocket.connected) {
        return legacySocket.disconnect?.();
      }
      return undefined;
    },
    connectOperatorEvents,
    disconnectOperatorEvents: closeOperatorEvents,
    get connected() {
      return Boolean(eventSource) || Boolean(legacySocket.connected);
    },
    get id() {
      return legacySocket.id;
    },
    legacySocket,
  };

  legacySocket.on("connect", connectOperatorEvents);
  legacySocket.on("disconnect", closeOperatorEvents);

  return bridge;
}

export function bootstrapClientRuntime() {
  if (bootstrapped || typeof window === "undefined") {
    return;
  }

  if (!window.BorealisSocket) {
    window.BorealisSocket = createBorealisSocketBridge();
  }
  if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 200;
  }
  installBorealisInputValidationGuard(window);
  bootstrapped = true;
}

export function getBorealisSocket() {
  return typeof window !== "undefined" ? window.BorealisSocket || null : null;
}

export function connectBorealisRealtime() {
  getBorealisSocket()?.connectOperatorEvents?.();
}

export function disconnectBorealisRealtime() {
  getBorealisSocket()?.disconnectOperatorEvents?.();
}

export function getBorealisUpdateRate() {
  return typeof window !== "undefined" ? window.BorealisUpdateRate || 200 : 200;
}
