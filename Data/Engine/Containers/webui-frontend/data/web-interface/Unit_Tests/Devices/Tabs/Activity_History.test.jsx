import { describe, expect, it } from "vitest";
import {
  historyActivityLabel,
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
    expect(historyActivityLabel(row)).toBe("Job: Zoom Workplace [WIN]");
  });

  it("falls back to task name then job id for scheduled job labels", () => {
    expect(
      historyActivityLabel({
        script_display_name: "Patch KB5000001",
        metadata: {
          scheduled_job_id: 12,
        },
      })
    ).toBe("Job: Patch KB5000001");
    expect(
      historyActivityLabel({
        metadata: {
          scheduled_job_id: 13,
        },
      })
    ).toBe("Job: #13");
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
    expect(historyActivityLabel({ ...row, script_display_name: "Uninstall - 7-Zip" })).toBe("Quick Job: Uninstall - 7-Zip");
  });

  it("keeps non-script activity labels readable", () => {
    expect(historyActivityLabel({ script_type: "ansible" })).toBe("Ansible Playbook");
    expect(historyActivityLabel({ script_type: "reverse_tunnel" })).toBe("Reverse VPN Tunnel");
  });
});
