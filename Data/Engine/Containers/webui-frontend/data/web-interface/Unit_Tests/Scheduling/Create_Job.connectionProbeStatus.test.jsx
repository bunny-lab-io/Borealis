import { describe, expect, it } from "vitest";
import { connectionProbeStatusLabel } from "@/Scheduling/Create_Job.jsx";

describe("scheduled job connection probe status", () => {
  it("derives persistent one-second countdown from absolute probe deadline", () => {
    const deadlineTs = 1_700_000_060;

    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_000_000))
      .toBe("Establishing Connection (60s)");
    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_029_100))
      .toBe("Establishing Connection (31s)");
    expect(connectionProbeStatusLabel("Establishing Connection", deadlineTs, 1_700_000_060_000))
      .toBe("Establishing Connection (0s)");
  });

  it("does not replace unrelated job statuses", () => {
    expect(connectionProbeStatusLabel("Running", 1_700_000_060, 1_700_000_000_000)).toBe("");
    expect(connectionProbeStatusLabel("Skipped", 1_700_000_060, 1_700_000_000_000)).toBe("");
  });
});
