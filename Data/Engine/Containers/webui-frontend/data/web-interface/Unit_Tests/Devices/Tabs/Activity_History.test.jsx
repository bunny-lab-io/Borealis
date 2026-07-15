import { describe, expect, it } from "vitest";
import {
  decorateHistoryActivityRows,
  historyActivityLabel,
  historyActivityColumnWidth,
  historyActivityGroupKey,
  scheduledJobActivityId,
  scheduledJobActivityPath,
} from "@/Devices/Tabs/Activity_History.jsx";

describe("Activity History helpers", () => {
  it("builds scheduled job links from activity metadata", () => {
    const row = {
      script_type: "powershell",
      activity_kind: "scheduled_job",
      scheduled_job_name: "Zoom Workplace [WIN]",
      metadata: {
        scheduled_job_id: 42,
        scheduled_job_run_id: 9001,
      },
    };

    expect(scheduledJobActivityId(row)).toBe(42);
    expect(scheduledJobActivityPath(row)).toBe("/jobs/42?tab=job_history");
    expect(historyActivityLabel(row)).toBe("Zoom Workplace [WIN]");
  });

  it("falls back to task name then job id for scheduled job labels", () => {
    expect(
      historyActivityLabel({
        script_display_name: "Patch KB5000001",
        metadata: {
          scheduled_job_id: 12,
        },
      })
    ).toBe("Patch KB5000001");
    expect(
      historyActivityLabel({
        metadata: {
          scheduled_job_id: 13,
        },
      })
    ).toBe("#13");
  });

  it("uses trusted task-link paths when backend supplies one", () => {
    expect(
      scheduledJobActivityPath({
        metadata: {
          task_link: {
            job_id: "7",
            path: "/jobs/7?tab=job_history",
          },
        },
      })
    ).toBe("/jobs/7?tab=job_history");
  });

  it("ignores non-job task-link paths", () => {
    expect(
      scheduledJobActivityPath({
        metadata: {
          task_link: {
            job_id: "7",
            path: "/devices/LAB-OPERATOR-01",
          },
        },
      })
    ).toBe("/jobs/7?tab=job_history");
  });

  it("does not build scheduled job paths for direct quick jobs", () => {
    const row = { id: 123, script_type: "powershell", metadata: {} };

    expect(scheduledJobActivityId(row)).toBe(0);
    expect(scheduledJobActivityPath(row)).toBe("");
    expect(historyActivityLabel({ ...row, script_display_name: "Uninstall - 7-Zip" })).toBe("Uninstall - 7-Zip");
  });

  it("keeps non-script activity labels readable", () => {
    expect(historyActivityLabel({ script_type: "ansible" })).toBe("Ansible Playbook");
    expect(historyActivityLabel({ script_type: "reverse_tunnel" })).toBe("Reverse VPN Tunnel");
  });

  it("groups scheduled rows by job occurrence", () => {
    const first = {
      id: 1,
      scheduled_job_name: "Zoom Workplace [WIN]",
      metadata: { scheduled_job_id: 42, scheduled_job_run_id: 9001 },
    };
    const second = {
      id: 2,
      scheduled_job_name: "Zoom Workplace [WIN]",
      metadata: { scheduled_job_id: 42, scheduled_job_run_id: 9001 },
    };
    const third = {
      id: 3,
      scheduled_job_name: "Zoom Workplace [WIN]",
      metadata: { scheduled_job_id: 42, scheduled_job_run_id: 9002 },
    };

    expect(historyActivityGroupKey(first)).toBe(historyActivityGroupKey(second));
    expect(historyActivityGroupKey(first)).not.toBe(historyActivityGroupKey(third));
  });

  it("decorates rows with labels and group keys", () => {
    const rows = decorateHistoryActivityRows([
      {
        id: 1,
        script_display_name: "Uninstall - 7-Zip",
        script_type: "powershell",
      },
    ]);

    expect(rows[0].activity_label).toBe("Uninstall - 7-Zip");
    expect(rows[0].activity_group_key).toBe("activity:1");
  });

  it("sizes activity column from widest label plus padding", () => {
    const rows = decorateHistoryActivityRows([
      { id: 1, script_display_name: "Short", script_type: "powershell" },
      { id: 2, scheduled_job_name: "Long Scheduled Job Name", metadata: { scheduled_job_id: 7 } },
    ]);

    expect(historyActivityColumnWidth(rows, (value) => String(value).length * 10)).toBe(302);
  });
});
