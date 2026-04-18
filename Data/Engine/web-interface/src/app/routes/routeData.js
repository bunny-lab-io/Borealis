import { redirect } from "react-router-dom";
import { buildLoginPath, buildSitesPath } from "./paths.js";

const EMPTY_ROUTE_REQUEST_PROGRESS = Object.freeze({
  active: false,
  target: "",
  total: 0,
  completed: 0,
});

let routeRequestProgressState = EMPTY_ROUTE_REQUEST_PROGRESS;
let routeRequestProgressPlans = new Map();
let routeRequestProgressNextPlanId = 1;
const routeRequestProgressListeners = new Set();

export class RouteDataError extends Error {
  constructor(message, { status = 500, payload = null } = {}) {
    super(message);
    this.name = "RouteDataError";
    this.status = status;
    this.payload = payload;
  }
}

function resolveRouteSignal({ request, signal } = {}) {
  if (signal) return signal;
  if (request?.signal) return request.signal;
  return undefined;
}

function emitRouteRequestProgress() {
  routeRequestProgressListeners.forEach((listener) => {
    try {
      listener();
    } catch {
      /* ignore subscriber failures */
    }
  });
}

function recomputeRouteRequestProgress(target, active = true) {
  let total = 0;
  let completed = 0;
  routeRequestProgressPlans.forEach((plan) => {
    total += Number(plan?.planned || 0);
    completed += Math.min(Number(plan?.completed || 0), Number(plan?.planned || 0));
  });
  routeRequestProgressState = {
    active,
    target: String(target || ""),
    total,
    completed,
  };
  emitRouteRequestProgress();
}

function ensureRouteRequestTarget(target) {
  const normalizedTarget = String(target || "").trim();
  if (!normalizedTarget) {
    return normalizedTarget;
  }
  if (routeRequestProgressState.target !== normalizedTarget) {
    routeRequestProgressPlans = new Map();
  }
  recomputeRouteRequestProgress(normalizedTarget, true);
  return normalizedTarget;
}

function normalizeProgressCount(value) {
  const parsed = Number(value || 0);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }
  return Math.floor(parsed);
}

async function readResponsePayload(response) {
  let text = "";
  try {
    text = await response.text();
  } catch {
    text = "";
  }
  if (!text) return {};
  try {
    return JSON.parse(text);
  } catch {
    return { message: text };
  }
}

function buildFailureMessage(response, payload, fallback) {
  return (
    payload?.message ||
    payload?.error ||
    (Array.isArray(payload?.errors) ? payload.errors.find(Boolean) : "") ||
    fallback ||
    `Request failed (${response.status})`
  );
}

export async function fetchRoutePayload(input, { request, signal, ...init } = {}) {
  const response = await fetch(input, {
    credentials: "include",
    cache: "no-store",
    ...init,
    signal: resolveRouteSignal({ request, signal }),
  });
  const payload = await readResponsePayload(response);
  return { response, payload };
}

export async function fetchRouteJson(input, options = {}) {
  const { response, payload } = await fetchRoutePayload(input, options);
  if (!response.ok) {
    throw new RouteDataError(buildFailureMessage(response, payload), {
      status: response.status,
      payload,
    });
  }
  return payload;
}

export function rethrowIfRouteRedirect(error) {
  if (error instanceof Response) {
    throw error;
  }
  if (error?.name === "AbortError") {
    throw error;
  }
}

function resolveRequestTarget(request) {
  try {
    const currentUrl = new URL(request?.url || "", "http://borealis.local");
    return `${currentUrl.pathname}${currentUrl.search}`;
  } catch {
    return "";
  }
}

export function getRouteRequestProgressSnapshot() {
  return routeRequestProgressState;
}

export function subscribeRouteRequestProgress(listener) {
  routeRequestProgressListeners.add(listener);
  return () => {
    routeRequestProgressListeners.delete(listener);
  };
}

export function startRouteRequestProgress(target) {
  ensureRouteRequestTarget(target);
}

export function clearRouteRequestProgress() {
  routeRequestProgressPlans = new Map();
  routeRequestProgressState = EMPTY_ROUTE_REQUEST_PROGRESS;
  emitRouteRequestProgress();
}

export function createRouteRequestPlan(request, plannedRequests = 0) {
  const target = ensureRouteRequestTarget(resolveRequestTarget(request));
  const planId = routeRequestProgressNextPlanId++;
  const plan = {
    planned: normalizeProgressCount(plannedRequests),
    completed: 0,
  };
  routeRequestProgressPlans.set(planId, plan);
  recomputeRouteRequestProgress(target, true);

  let closed = false;

  const updatePlan = (nextCompleted, nextPlanned = plan.planned) => {
    const currentPlan = routeRequestProgressPlans.get(planId);
    if (!currentPlan) {
      return;
    }
    currentPlan.completed = Math.max(0, nextCompleted);
    currentPlan.planned = Math.max(currentPlan.completed, Math.max(0, nextPlanned));
    routeRequestProgressPlans.set(planId, currentPlan);
    recomputeRouteRequestProgress(target, true);
  };

  const controller = {
    fetchPayload(input, options = {}) {
      return controller.track(
        fetchRoutePayload(input, {
          ...options,
          request: options.request || request,
        })
      );
    },
    fetchJson(input, options = {}) {
      return controller.track(
        fetchRouteJson(input, {
          ...options,
          request: options.request || request,
        })
      );
    },
    track(task) {
      return Promise.resolve(task).finally(() => {
        controller.complete(1);
      });
    },
    complete(count = 1) {
      if (closed) return;
      const normalizedCount = normalizeProgressCount(count) || 1;
      const currentPlan = routeRequestProgressPlans.get(planId);
      if (!currentPlan) return;
      updatePlan(currentPlan.completed + normalizedCount, currentPlan.planned);
    },
    skip(count = 1) {
      controller.complete(count);
    },
    finalize() {
      if (closed) return;
      closed = true;
      const currentPlan = routeRequestProgressPlans.get(planId);
      if (!currentPlan) return;
      updatePlan(currentPlan.completed, currentPlan.completed);
    },
  };

  return controller;
}

export async function requireAuthenticatedRequest(request, progress = null) {
  const target = resolveRequestTarget(request);
  const loadPayload =
    progress?.fetchPayload ||
    ((input, options = {}) =>
      fetchRoutePayload(input, {
        ...options,
        request: options.request || request,
      }));
  const { response: bootstrapResponse, payload: bootstrapPayload } = await loadPayload(
    "/api/bootstrap/state"
  );

  if (!bootstrapResponse.ok || String(bootstrapPayload?.phase || "") !== "login_required") {
    throw redirect(buildLoginPath(target));
  }

  const { response: authResponse, payload: authPayload } = await loadPayload("/api/auth/me");
  if (!authResponse.ok || !String(authPayload?.username || "").trim()) {
    throw redirect(buildLoginPath(target));
  }

  return authPayload;
}

export async function requireAdminRequest(request, progress = null) {
  const authPayload = await requireAuthenticatedRequest(request, progress);
  if (String(authPayload?.role || "").trim().toLowerCase() !== "admin") {
    throw redirect(buildSitesPath({ notAuthorized: true }));
  }
  return authPayload;
}

export function getRouteErrorMessage(error, fallbackMessage) {
  if (error instanceof RouteDataError && error.message) {
    return error.message;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallbackMessage;
}
