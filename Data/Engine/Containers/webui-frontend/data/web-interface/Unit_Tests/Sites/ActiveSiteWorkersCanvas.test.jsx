import { describe, expect, it } from "vitest";
import { buildGraph, statusTone } from "@/Sites/ActiveSiteWorkersCanvas.jsx";

describe("active site workers canvas mapping", () => {
  it("creates worker and task nodes with terminal task status", () => {
    const payload = {
      workers: [
        {
          worker_guid: "worker-1",
          container_name: "site-worker-1",
          site_id: 7,
          status: "running",
          started_at: 100,
          current_lanes: ["scheduled_job"],
          claimed_count: 2,
        },
      ],
      recent_work: [
        {
          id: 11,
          kind: "scheduled_run",
          lane: "scheduled_job",
          site_id: 7,
          worker_guid: "worker-1",
          status: "succeeded",
          started_at: 110,
          finished_at: 130,
          task_link: {
            label: "Scheduled Job 42",
            path: "/jobs/42?tab=job_history",
          },
        },
      ],
    };

    const graph = buildGraph(payload, () => {});
    const workerNode = graph.nodes.find((node) => node.id === "worker:worker-1");
    const taskNode = graph.nodes.find((node) => node.id === "task:11");
    const edge = graph.edges.find((item) => item.target === "task:11");

    expect(workerNode?.type).toBe("worker");
    expect(taskNode?.type).toBe("task");
    expect(taskNode?.data.status).toBe("succeeded");
    expect(statusTone(taskNode?.data.status)).toBe("success");
    expect(edge?.animated).toBe(false);
  });

  it("attaches queued work to site placeholder until claimed", () => {
    const graph = buildGraph(
      {
        workers: [],
        recent_work: [
          {
            id: 12,
            kind: "scheduled_workflow_run",
            lane: "scheduled_job",
            site_id: 9,
            status: "queued",
            task_link: { label: "Workflow Job 8", path: "/jobs/8?tab=job_history" },
          },
        ],
      },
      () => {}
    );

    expect(graph.nodes.some((node) => node.id === "placeholder:site:9")).toBe(true);
    expect(graph.nodes.some((node) => node.id === "task:12")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "placeholder:site:9" && edge.animated)).toBe(true);
    expect(statusTone("failed")).toBe("failed");
  });
});
