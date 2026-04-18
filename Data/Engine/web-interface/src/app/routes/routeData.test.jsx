import { afterEach, describe, expect, it, vi } from "vitest";
import {
  clearRouteRequestProgress,
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteRequestProgressSnapshot,
  requireAdminRequest,
  requireAuthenticatedRequest,
  RouteDataError,
} from "./routeData.js";

function jsonResponse(body, init = {}) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
}

describe("routeData helpers", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    clearRouteRequestProgress();
  });

  it("redirects unauthenticated loader requests to login with the target path", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      jsonResponse({ phase: "login_required" })
    ).mockResolvedValueOnce(
      jsonResponse({ error: "Unauthorized" }, { status: 401 })
    );

    try {
      await requireAuthenticatedRequest(
        new Request("http://borealis.local/devices/alpha?tab=device_summary")
      );
      throw new Error("Expected loader redirect");
    } catch (response) {
      expect(response.status).toBe(302);
      expect(response.headers.get("Location")).toBe(
        "/login?next=%2Fdevices%2Falpha%3Ftab%3Ddevice_summary"
      );
    }
  });

  it("redirects non-admin loader requests to sites with a not-authorized flag", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      jsonResponse({ phase: "login_required" })
    ).mockResolvedValueOnce(
      jsonResponse({ username: "operator", role: "User" })
    );

    try {
      await requireAdminRequest(new Request("http://borealis.local/server"));
      throw new Error("Expected admin redirect");
    } catch (response) {
      expect(response.headers.get("Location")).toBe("/sites?not_authorized=1");
    }
  });

  it("surfaces API error payloads through fetchRouteJson", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      jsonResponse({ message: "Boom" }, { status: 500 })
    );

    await expect(fetchRouteJson("/api/test")).rejects.toEqual(
      expect.objectContaining({
        message: "Boom",
        status: 500,
      })
    );
  });

  it("throws a RouteDataError for non-ok route fetches", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response("Bad gateway", { status: 502 })
    );

    await expect(fetchRouteJson("/api/test")).rejects.toBeInstanceOf(RouteDataError);
  });

  it("tracks planned route request progress and finalizes skipped requests", () => {
    const progress = createRouteRequestPlan(
      new Request("http://borealis.local/devices/alpha"),
      5
    );

    expect(getRouteRequestProgressSnapshot()).toMatchObject({
      active: true,
      target: "/devices/alpha",
      total: 5,
      completed: 0,
    });

    progress.complete(2);
    progress.skip(2);

    expect(getRouteRequestProgressSnapshot()).toMatchObject({
      total: 5,
      completed: 4,
    });

    progress.finalize();

    expect(getRouteRequestProgressSnapshot()).toMatchObject({
      total: 4,
      completed: 4,
    });
  });
});
