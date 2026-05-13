import { describe, expect, it } from "vitest";
import { buildGraph, buildGraphAt, statusTone } from "@/Scheduling/Site_Workers.jsx";

describe("active site workers canvas mapping", () => {
  it("creates worker and task nodes with terminal task status", () => {
    const now = Math.floor(Date.now() / 1000);
    const idleSince = now - 10;
    const payload = {
      worker_idle_ttl_seconds: 60,
      workers: [
        {
          worker_guid: "worker-1",
          container_name: "site-worker-1",
          site_id: 7,
          status: "idle",
          started_at: 100,
          idle_since: idleSince,
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
          task_type: "Assembly",
          started_at: now - 20,
          finished_at: now - 10,
          task_link: {
            label: "Scheduled Job 42",
            path: "/jobs/42?tab=job_history",
          },
        },
      ],
    };

    const graph = buildGraph(payload, () => {});
    const workerNode = graph.nodes.find((node) => node.id === "worker:worker-1");
    const taskNode = graph.nodes.find((node) => node.type === "task" && node.data.label === "Task");
    const edge = graph.edges.find((item) => item.target === taskNode?.id);

    expect(workerNode?.type).toBe("worker");
    expect(workerNode?.data.idleRemainingSeconds).toBeGreaterThanOrEqual(45);
    expect(workerNode?.data.idleRemainingSeconds).toBeLessThanOrEqual(60);
    expect(taskNode?.type).toBe("task");
    expect(taskNode?.data.status).toBe("succeeded");
    expect(taskNode?.data.taskType).toBe("Assembly");
    expect(taskNode?.data.closeRemainingSeconds).toBeGreaterThanOrEqual(15);
    expect(taskNode?.data.closeRemainingSeconds).toBeLessThanOrEqual(30);
    expect(taskNode?.data.taskName).toBe("Scheduled Job 42");
    expect(statusTone(taskNode?.data.status)).toBe("success");
    expect(edge?.label).toBeUndefined();
    expect(edge?.sourceHandle).toBe("output");
    expect(edge?.targetHandle).toBe("input");
    expect(edge?.animated).toBe(false);
  });

  it("hides terminal workers and their terminal work once worker is gone", () => {
    const now = 1000;
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "worker-1",
            site_id: 7,
            status: "stopped",
            started_at: now - 70,
            stopped_at: now,
          },
        ],
        recent_work: [
          {
            id: 41,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            status: "succeeded",
            started_at: now - 20,
            finished_at: now - 5,
          },
        ],
      },
      () => {},
      now
    );

    expect(graph.nodes.some((node) => node.id === "worker:worker-1")).toBe(false);
    expect(graph.nodes.filter((node) => node.type === "task")).toHaveLength(0);
  });

  it("keeps inactive workers and tasks when visual dismissal is disabled", () => {
    const now = 1000;
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "worker-1",
            site_id: 7,
            status: "stopped",
            started_at: now - 120,
            stopped_at: now - 80,
          },
        ],
        recent_work: [
          {
            id: 42,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            status: "succeeded",
            started_at: now - 100,
            finished_at: now - 70,
          },
        ],
      },
      () => {},
      now,
      { dismissInactive: false }
    );

    const workerNode = graph.nodes.find((node) => node.id === "worker:worker-1");
    const taskNode = graph.nodes.find((node) => node.type === "task");

    expect(workerNode).toBeTruthy();
    expect(workerNode?.data.closeRemainingSeconds).toBeNull();
    expect(taskNode).toBeTruthy();
    expect(taskNode?.data.closeRemainingSeconds).toBeNull();
  });

  it("restarts inactive countdowns when visual dismissal is re-enabled", () => {
    const now = 1000;
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "worker-1",
            site_id: 7,
            status: "stopped",
            started_at: now - 120,
            stopped_at: now - 80,
          },
        ],
        recent_work: [
          {
            id: 42,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            status: "succeeded",
            started_at: now - 100,
            finished_at: now - 70,
          },
        ],
      },
      () => {},
      now,
      { dismissInactive: true, dismissStartedAt: now - 5 }
    );

    const workerNode = graph.nodes.find((node) => node.id === "worker:worker-1");
    const taskNode = graph.nodes.find((node) => node.type === "task");

    expect(workerNode?.data.closeRemainingSeconds).toBe(55);
    expect(taskNode?.data.closeRemainingSeconds).toBe(25);
  });


  it("removes terminal tasks after close window", () => {
    const now = 1000;
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "worker-1",
            site_id: 7,
            status: "running",
            current_lanes: ["scheduled_job"],
          },
        ],
        recent_work: [
          {
            id: 31,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            status: "succeeded",
            started_at: now - 80,
            finished_at: now - 31,
            task_link: { label: "Scheduled Job 42", path: "/jobs/42?tab=job_history" },
          },
        ],
      },
      () => {},
      now
    );

    expect(graph.nodes.filter((node) => node.type === "task")).toHaveLength(0);
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

    const placeholder = graph.nodes.find((node) => node.id === "worker:queued-site-9");
    const taskNode = graph.nodes.find((node) => node.type === "task" && node.data.label === "Task");
    expect(placeholder).toBeTruthy();
    expect(taskNode).toBeTruthy();
    expect(graph.edges.some((edge) => edge.source === placeholder?.id && edge.target === taskNode?.id && edge.animated)).toBe(true);
    expect(statusTone("failed")).toBe("failed");
  });

  it("collapses repeated task rows by worker job and status", () => {
    const graph = buildGraph(
      {
        site_names: { 7: "Bunny Lab" },
        workers: [
          {
            worker_guid: "worker-1",
            site_id: 7,
            status: "running",
            current_lanes: ["scheduled_job"],
          },
        ],
        recent_work: [
          {
            id: 21,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            target_count: 2,
            status: "succeeded",
            task_link: { label: "Scheduled Job 42", path: "/jobs/42?tab=job_history" },
          },
          {
            id: 22,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "worker-1",
            job_id: 42,
            target_count: 2,
            status: "succeeded",
            task_link: { label: "Scheduled Job 42", path: "/jobs/42?tab=job_history" },
          },
        ],
      },
      () => {}
    );

    const workerNode = graph.nodes.find((node) => node.id === "worker:worker-1");
    const taskNodes = graph.nodes.filter((node) => node.type === "task");

    expect(workerNode?.data.label).toBe("Site Worker");
    expect(workerNode?.data.siteLabel).toBe("Bunny Lab");
    expect(taskNodes).toHaveLength(1);
    expect(taskNodes[0].data.label).toBe("Task");
    expect(taskNodes[0].data.targetCountLabel).toBe("2 Devices Targeted");
    expect(taskNodes[0].data.taskName).toBe("Scheduled Job 42");
  });
});
