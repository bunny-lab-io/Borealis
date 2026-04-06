import { io } from "socket.io-client";

let bootstrapped = false;

export function bootstrapClientRuntime() {
  if (bootstrapped || typeof window === "undefined") {
    return;
  }

  if (!window.BorealisSocket) {
    window.BorealisSocket = io(window.location.origin, { transports: ["websocket"] });
  }
  if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 200;
  }
  bootstrapped = true;
}

export function getBorealisSocket() {
  return typeof window !== "undefined" ? window.BorealisSocket || null : null;
}

export function getBorealisUpdateRate() {
  return typeof window !== "undefined" ? window.BorealisUpdateRate || 200 : 200;
}
