import { describe, expect, it } from "vitest";
import {
  buildJobHistoryStatusCounts,
  connectionProbeStatusLabel,
  filterJobHistoryRowsByStatus,
  JOB_HISTORY_STATUS_FILTER_GROUPS,
  JOB_STATUS_COLUMN_MIN_WIDTH,
  STATUS_PILL_LAYOUT_SX,
  statusPillTextTransform,
} from "@/Scheduling/Create_Job.jsx";

describe("scheduled job connection probe status", () => {
  it("derives persistent one-second countdown from absolute probe deadline", () => {
    const deadlineTs = 1_700_000_060;

    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_000_000))
      .toBe("Establishing Connection - 60s Until Timeout");
    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_029_100))
      .toBe("Establishing Connection - 31s Until Timeout");
    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_060_000))
      .toBe("Establishing Connection - 0s Until Timeout");
  });

  it("does not replace unrelated job statuses", () => {
    expect(connectionProbeStatusLabel("Running", 1_700_000_060, 1_700_000_000_000)).toBe("");
    expect(connectionProbeStatusLabel("Skipped", 1_700_000_060, 1_700_000_000_000)).toBe("");
  });

  it("keeps countdown suffix visible inside job status pills", () => {
    expect(STATUS_PILL_LAYOUT_SX).toMatchObject({
      whiteSpace: "nowrap",
      flexShrink: 0,
    });
    expect(JOB_STATUS_COLUMN_MIN_WIDTH).toBeGreaterThanOrEqual(340);
    expect(statusPillTextTransform(true)).toBe("none");
    expect(statusPillTextTransform(false)).toBe("uppercase");
  });
});

describe("scheduled job history status filters", () => {
  it("publishes requested Not Started and Started slider order", () => {
    expect(
      JOB_HISTORY_STATUS_FILTER_GROUPS.map((group) => ({
        label: group.label,
        options: group.options.map((option) => option.label),
      }))
    ).toEqual([
      { label: "Not Started", options: ["Pending", "Expired", "Skipped"] },
      { label: "Started", options: ["Running", "Failed", "Warning", "Success"] },
    ]);
  });

  it("counts display buckets from live device status rows", () => {
    const rows = [
      { job_status: "Pending" },
      { job_status: "Expired" },
      { job_status: "Skipped" },
      { job_status: "No Devices Targeted" },
      { job_status: "No Eligible Targets" },
      { job_status: "Running" },
      { job_status: "Establishing Connection" },
      { job_status: "Failed" },
      { job_status: "Timed Out" },
      { job_status: "Warning" },
      { job_status: "Success" },
    ];

    expect(buildJobHistoryStatusCounts(rows)).toEqual({
      pending: 1,
      expired: 1,
      skipped: 3,
      running: 2,
      failed: 2,
      warning: 1,
      success: 1,
    });
  });

  it("shows all rows by default and one selected status bucket at a time", () => {
    const rows = [
      { hostname: "pending", job_status: "Pending" },
      { hostname: "probing", job_status: "Establishing Connection" },
      { hostname: "timeout", job_status: "Timed Out" },
      { hostname: "skipped", job_status: "Skipped" },
    ];

    expect(filterJobHistoryRowsByStatus(rows, "")).toEqual(rows);
    expect(filterJobHistoryRowsByStatus(rows, "running").map((row) => row.hostname)).toEqual(["probing"]);
    expect(filterJobHistoryRowsByStatus(rows, "failed").map((row) => row.hostname)).toEqual(["timeout"]);
    expect(filterJobHistoryRowsByStatus(rows, "skipped").map((row) => row.hostname)).toEqual(["skipped"]);
  });
});
