import { describe, expect, it } from "vitest";
import {
  buildScheduledJobCategoryFlags,
  buildScheduledJobFilterCounts,
  filterScheduledJobRows,
  isMaintenanceJobKind,
  isPatchManagementJobKind,
} from "@/Scheduling/scheduledJobFilters.js";

function row(name, categoryFlags) {
  return { name, categoryFlags };
}

describe("Scheduled Jobs list filters", () => {
  it("classifies internal maintenance and patch jobs outside normal queues", () => {
    const maintenance = buildScheduledJobCategoryFlags({
      jobKind: "agent_maintenance",
      scheduleRaw: "immediately",
      allTargetsEvaluated: false,
      jobExpiredFlag: false,
    });
    const patchManagement = buildScheduledJobCategoryFlags({
      jobKind: "patch_install",
      scheduleRaw: "once",
      allTargetsEvaluated: false,
      jobExpiredFlag: false,
    });
    const normalImmediate = buildScheduledJobCategoryFlags({
      jobKind: "automation",
      scheduleRaw: "immediately",
      allTargetsEvaluated: false,
      jobExpiredFlag: false,
    });

    expect(maintenance).toMatchObject({
      normal: false,
      immediate: false,
      maintenance: true,
      patch_management: false,
    });
    expect(patchManagement).toMatchObject({
      normal: false,
      scheduled: false,
      maintenance: false,
      patch_management: true,
    });
    expect(normalImmediate).toMatchObject({
      normal: true,
      immediate: true,
      maintenance: false,
      patch_management: false,
    });
  });

  it("keeps maintenance and patch counts separate from normal list counts", () => {
    const rows = [
      row("operator immediate", buildScheduledJobCategoryFlags({ jobKind: "automation", scheduleRaw: "immediately" })),
      row("operator scheduled", buildScheduledJobCategoryFlags({ jobKind: "onboarding", scheduleRaw: "once" })),
      row("operator completed", buildScheduledJobCategoryFlags({
        jobKind: "automation",
        scheduleRaw: "once",
        allTargetsEvaluated: true,
      })),
      row("agent update", buildScheduledJobCategoryFlags({ jobKind: "agent_update", scheduleRaw: "immediately" })),
      row("kb install", buildScheduledJobCategoryFlags({ jobKind: "patch_install", scheduleRaw: "once" })),
    ];

    expect(buildScheduledJobFilterCounts(rows)).toEqual({
      normal: 3,
      immediate: 1,
      scheduled: 1,
      recurring: 0,
      completed: 1,
      maintenance: 1,
      patch_management: 1,
    });
    expect(filterScheduledJobRows(rows, "normal").map((item) => item.name)).toEqual([
      "operator immediate",
      "operator scheduled",
      "operator completed",
    ]);
    expect(filterScheduledJobRows(rows, "maintenance").map((item) => item.name)).toEqual(["agent update"]);
    expect(filterScheduledJobRows(rows, "patch_management").map((item) => item.name)).toEqual(["kb install"]);
    expect(filterScheduledJobRows(rows, "unknown").map((item) => item.name)).toEqual([
      "operator immediate",
      "operator scheduled",
      "operator completed",
    ]);
  });

  it("accepts future maintenance names without broad patch false positives", () => {
    expect(isMaintenanceJobKind("engine_maintenance")).toBe(true);
    expect(isMaintenanceJobKind("database-maintenance")).toBe(true);
    expect(isPatchManagementJobKind("windows_patch_policy")).toBe(true);
    expect(isPatchManagementJobKind("dispatch")).toBe(false);
  });
});
