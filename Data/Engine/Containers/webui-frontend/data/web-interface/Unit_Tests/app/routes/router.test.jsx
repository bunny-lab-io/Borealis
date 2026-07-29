import { describe, expect, it } from "vitest";
import { buildAppRoutes } from "@/app/routes/router.jsx";

function collectRoutes(routes, prefix = "", results = []) {
  routes.forEach((route) => {
    const routePath = route.path
      ? route.path.startsWith("/")
        ? route.path
        : `${prefix}/${route.path}`.replace(/\/+/g, "/")
      : prefix || "/";
    results.push({
      path: route.path ?? "(index)",
      fullPath: route.index ? `${prefix || "/"}` : routePath,
      hasLazy: typeof route.lazy === "function",
      handle: route.handle || null,
    });
    if (Array.isArray(route.children)) {
      collectRoutes(route.children, route.index ? prefix : routePath, results);
    }
  });
  return results;
}

describe("app router", () => {
  it("registers the canonical resource-first routes", () => {
    const flattened = collectRoutes(buildAppRoutes());
    const paths = flattened.map((route) => route.fullPath);

    expect(paths).toContain("/devices/:deviceId");
    expect(paths).toContain("/devices/:deviceId/remote-desktop");
    expect(paths).toContain("/filters/:filterId");
    expect(paths).toContain("/jobs/:jobId");
    expect(paths).toContain("/assemblies/new/script");
    expect(paths).toContain("/assemblies/new/ansible_playbook");
    expect(paths).toContain("/assemblies/new/workflow");
    expect(paths).toContain("/assemblies/scripts/:assemblyGuid");
    expect(paths).toContain("/assemblies/ansible_playbooks/:assemblyGuid");
    expect(paths).toContain("/assemblies/workflows/:workflowGuid");
    expect(paths).toContain("/assemblies/workflows/runs/:runId");
  });

  it("keeps lazy-loaded route modules on the domain pages", () => {
    const flattened = collectRoutes(buildAppRoutes());
    const lazyPaths = flattened.filter((route) => route.hasLazy).map((route) => route.fullPath);

    expect(lazyPaths).toContain("/devices");
    expect(lazyPaths).toContain("/devices/:deviceId/remote-desktop");
    expect(lazyPaths).toContain("/filters");
    expect(lazyPaths).toContain("/jobs");
    expect(lazyPaths).toContain("/assemblies");
    expect(lazyPaths).toContain("/patch-management");
    expect(lazyPaths).toContain("/patch-management/windows");
    expect(lazyPaths).toContain("/patch-management/linux");
    expect(lazyPaths).toContain("/patch-management/macos");
    expect(lazyPaths).toContain("/credentials");
  });

  it("publishes canonical route page keys for nested resource routes", () => {
    const flattened = collectRoutes(buildAppRoutes());
    const byPath = new Map(flattened.map((route) => [route.fullPath, route.handle]));

    expect(byPath.get("/devices/:deviceId")?.pageKey).toBe("device");
    expect(byPath.get("/devices/:deviceId/remote-desktop")?.pageKey).toBe("device-remote-desktop");
    expect(byPath.get("/filters/:filterId")?.pageKey).toBe("filter");
    expect(byPath.get("/jobs/:jobId")?.pageKey).toBe("job");
    expect(byPath.get("/assemblies/scripts/:assemblyGuid")?.pageKey).toBe("script-assembly");
    expect(byPath.get("/assemblies/ansible_playbooks/:assemblyGuid")?.pageKey).toBe("ansible-playbook");
    expect(byPath.get("/assemblies/workflows/:workflowGuid")?.pageKey).toBe("workflow");
    expect(byPath.get("/patch-management")?.navKey).toBe("patch-management");
    expect(byPath.get("/patch-management/windows")?.navKey).toBe("patch-management");
    expect(byPath.get("/patch-management/linux")?.navKey).toBe("patch-management");
    expect(byPath.get("/patch-management/macos")?.navKey).toBe("patch-management");
  });
});
