import { describe, expect, it } from "vitest";
import {
  APP_PATHS,
  buildLoginPath,
  buildSiteAssignmentPath,
  buildSitesPath,
  normalizeAppRedirectTarget,
} from "./paths.js";

describe("route path helpers", () => {
  it("builds canonical resource routes", () => {
    expect(APP_PATHS.device("agent-1")).toBe("/devices/agent-1");
    expect(APP_PATHS.deviceRemoteDesktop("agent-1")).toBe("/devices/agent-1/remote-desktop");
    expect(APP_PATHS.filter(42)).toBe("/filters/42");
    expect(APP_PATHS.job(19)).toBe("/jobs/19");
    expect(APP_PATHS.assemblyScript("SCRIPT-123")).toBe("/assemblies/scripts/SCRIPT-123");
    expect(APP_PATHS.assemblyAnsible("PLAY-123")).toBe("/assemblies/ansible_playbooks/PLAY-123");
    expect(APP_PATHS.assemblyWorkflow("FLOW-123")).toBe("/assemblies/workflows/FLOW-123");
    expect(APP_PATHS.assemblyWorkflowRun(88)).toBe("/assemblies/workflows/runs/88");
  });

  it("builds repeated user query params for site assignment", () => {
    expect(buildSiteAssignmentPath(["alice", "bob"])).toBe(
      `${APP_PATHS.siteAssignment}?user=alice&user=bob`
    );
  });

  it("builds loader-friendly redirect paths", () => {
    expect(buildLoginPath("/devices/alpha?tab=device_summary")).toBe(
      "/login?next=%2Fdevices%2Falpha%3Ftab%3Ddevice_summary"
    );
    expect(buildSitesPath({ notAuthorized: true })).toBe("/sites?not_authorized=1");
  });

  it("sanitizes redirect targets to internal app paths only", () => {
    expect(normalizeAppRedirectTarget("/devices/alpha")).toBe("/devices/alpha");
    expect(normalizeAppRedirectTarget("https://example.com")).toBe("");
    expect(normalizeAppRedirectTarget("//example.com")).toBe("");
    expect(normalizeAppRedirectTarget("/login")).toBe("");
  });
});
