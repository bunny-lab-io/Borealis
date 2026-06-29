import { io } from "socket.io-client";

let bootstrapped = false;
const GO_REALTIME_EVENTS = [
  "agent_status_changed",
  "device_inventory_changed",
  "device_services_changed",
  "borealis_notification",
  "server_operator_presence_changed",
  "watchdog_incidents_changed",
  "device_watchdogs_changed",
];
const GO_REALTIME_EVENT_SET = new Set(GO_REALTIME_EVENTS);

function hasAuthCookie() {
  if (typeof document === "undefined") return false;
  return document.cookie.split(";").some((item) => item.trim().startsWith("borealis_auth="));
}

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
    if (reconnectTimer || !hasAuthCookie()) return;
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null;
      connectOperatorEvents();
    }, 5000);
  }

  function connectOperatorEvents() {
    if (eventSource || !hasAuthCookie()) return;
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
      if (!GO_REALTIME_EVENT_SET.has(eventName)) {
        legacySocket.on(eventName, handler);
      }
      rememberHandler(eventName, handler);
      return bridge;
    },
    off(eventName, handler) {
      if (!GO_REALTIME_EVENT_SET.has(eventName)) {
        legacySocket.off(eventName, handler);
      }
      forgetHandler(eventName, handler);
      return bridge;
    },
    emit(eventName, ...args) {
      if (eventName === "operator_presence_sync" || eventName === "operator_presence_clear") {
        return false;
      }
      if (!legacySocket.connected) {
        legacySocket.connect?.();
      }
      return legacySocket.emit(eventName, ...args);
    },
    connect() {
      legacySocket.connect?.();
      connectOperatorEvents();
      return bridge;
    },
    disconnect() {
      closeOperatorEvents();
      return legacySocket.disconnect?.();
    },
    connectOperatorEvents,
    disconnectOperatorEvents: closeOperatorEvents,
    get connected() {
      return Boolean(legacySocket.connected);
    },
    get id() {
      return legacySocket.id;
    },
    legacySocket,
  };

  legacySocket.on("connect", connectOperatorEvents);
  legacySocket.on("disconnect", () => {
    if (!hasAuthCookie()) {
      closeOperatorEvents();
    }
  });

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
