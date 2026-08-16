import { describe, expect, it } from "vitest";
import {
  connectionProbeStatusLabel,
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
