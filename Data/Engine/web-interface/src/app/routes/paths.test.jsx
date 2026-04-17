import { describe, expect, it } from "vitest";
import { APP_PATHS, buildSiteAssignmentPath } from "./paths.js";

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
});
