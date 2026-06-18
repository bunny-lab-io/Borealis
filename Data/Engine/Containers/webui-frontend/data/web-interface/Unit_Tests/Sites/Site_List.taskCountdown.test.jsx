import { describe, expect, it } from "vitest";
import { buildTaskGroupsByWorker } from "@/Sites/Site_List.jsx";

describe("site assigned task countdown", () => {
  it("keeps terminal task groups for 60 seconds after last activity", () => {
    const payload = {
      recent_work: [
        {
          kind: "scheduled_run",
          site_id: 7,
          job_id: 144,
          status: "succeeded",
          started_at: 980,
          finished_at: 1000,
          task_link: {
            path: "/jobs/144?tab=job_history",
          },
        },
      ],
    };

    const firstGroups = buildTaskGroupsByWorker(payload, 1000).get("site:7");
    expect(firstGroups).toHaveLength(1);
    expect(firstGroups[0].removal_remaining_seconds).toBe(60);

    const laterGroups = buildTaskGroupsByWorker(payload, 1045).get("site:7");
    expect(laterGroups).toHaveLength(1);
    expect(laterGroups[0].removal_remaining_seconds).toBe(15);

    const expiredGroups = buildTaskGroupsByWorker(payload, 1060).get("site:7");
    expect(expiredGroups).toEqual([]);
  });

  it("does not expire active assigned task groups", () => {
    const payload = {
      active_work: [
        {
          kind: "scheduled_run",
          site_id: 7,
          job_id: 144,
          status: "running",
          started_at: 1000,
          updated_at: 1200,
        },
      ],
    };

    const groups = buildTaskGroupsByWorker(payload, 5000).get("site:7");
    expect(groups).toHaveLength(1);
    expect(groups[0].expires_at).toBeNull();
    expect(groups[0].removal_remaining_seconds).toBeNull();
    expect(groups[0].counts.running).toBe(1);
  });
});
