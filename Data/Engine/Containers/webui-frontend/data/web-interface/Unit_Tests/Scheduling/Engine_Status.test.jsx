import { describe, expect, it } from "vitest";
import { buildGraph, buildGraphAt, mergeWorkerPayload, statusTone } from "@/Admin/Engine_Status.jsx";

describe("active site workers canvas mapping", () => {
  it("renders engine service topology nodes and edges", () => {
    const graph = buildGraphAt(
      {
        services: [
          { key: "api-backend", label: "API Backend", status: "healthy", docker_state: "running", actions: [{ id: "restart", label: "Restart", action: "restart" }] },
          { key: "postgres-db", label: "Postgres", status: "healthy", docker_state: "running", actions: [{ id: "restart", label: "Restart", action: "restart" }] },
          { key: "webui-frontend", label: "Web UI", status: "healthy", docker_state: "running", actions: [{ id: "rebuild_prod", label: "Rebuild Prod", action: "rebuild", mode: "prod" }] },
          { key: "traefik-edge", label: "Traefik Edge", status: "warning", docker_state: "running", actions: [{ id: "reload", label: "Reload", action: "reload" }] },
          { key: "remote-desktop-guacd", label: "Guacamole", status: "healthy", docker_state: "running", actions: [{ id: "restart", label: "Restart", action: "restart" }] },
          { key: "wireguard-tunnel", label: "WireGuard", status: "healthy", docker_state: "running", actions: [{ id: "reconcile", label: "Reconcile", action: "reconcile" }] },
          { key: "docker-proxy", label: "Docker Proxy", status: "healthy", docker_state: "running", docker_status_text: "Up 3 minutes", actions: [{ id: "restart", label: "Restart", action: "restart" }] },
          { key: "job-scheduler", label: "Job Scheduler", status: "healthy", docker_state: "running", actions: [{ id: "restart", label: "Restart", action: "restart" }] },
        ],
      },
      () => {},
      1000,
      { onServiceAction: () => {} }
    );

    expect(graph.nodes.some((node) => node.id === "service:api-backend")).toBe(true);
    expect(graph.nodes.some((node) => node.id === "service:postgres-db")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "service:api-backend" && edge.target === "service:postgres-db")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "service:api-backend" && edge.target === "service:docker-proxy")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "service:api-backend" && edge.target === "service:job-scheduler")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "service:api-backend" && edge.target === "service:traefik-edge")).toBe(true);
    expect(graph.edges.some((edge) => edge.source === "service:webui-frontend" && edge.target === "service:traefik-edge")).toBe(false);
    expect(graph.nodes.find((node) => node.id === "service:api-backend")?.position.x).toBeLessThan(graph.nodes.find((node) => node.id === "service:postgres-db")?.position.x);
    expect(graph.nodes.find((node) => node.id === "service:job-scheduler")?.position.y).toBe(330);
    expect(graph.nodes.find((node) => node.id === "service:job-scheduler")?.position.y).toBeLessThan(graph.nodes.find((node) => node.id === "service:remote-desktop-guacd")?.position.y);
    expect(graph.nodes.find((node) => node.id === "service:remote-desktop-guacd")?.position.y).toBeLessThan(graph.nodes.find((node) => node.id === "service:wireguard-tunnel")?.position.y);
    expect(graph.nodes.find((node) => node.id === "service:remote-desktop-guacd")?.data.label).toBe("Guacamole");
    expect(graph.nodes.find((node) => node.id === "service:docker-proxy")?.position.y).toBeGreaterThan(graph.nodes.find((node) => node.id === "service:job-scheduler")?.position.y);
    expect(graph.nodes.find((node) => node.id === "service:docker-proxy")?.data.startedLabel).toBe("Up 3 minutes");
    expect(graph.nodes.find((node) => node.id === "service:wireguard-tunnel")?.data.actions[0].label).toBe("Reconcile");
  });

  it("uses service job scheduler as site-worker parent when available", () => {
    const graph = buildGraphAt(
      {
        services: [
          { key: "api-backend", label: "API Backend", status: "healthy", docker_state: "running" },
          { key: "job-scheduler", label: "Job Scheduler", status: "healthy", docker_state: "running" },
        ],
        workers: [
          { worker_guid: "job-scheduler-worker", site_id: 0, status: "running", started_at: 100 },
          { worker_guid: "site-worker-1", site_id: 7, status: "running", started_at: 100 },
        ],
      },
      () => {},
      1000
    );

    expect(graph.nodes.filter((node) => node.data?.label === "Job Scheduler")).toHaveLength(1);
    expect(graph.edges.some((edge) => edge.source === "service:job-scheduler" && edge.target === "worker:site-worker-1")).toBe(true);
  });

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

  it("marks active work from terminal workers as reassigning", () => {
    const now = 1000;
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "old-worker",
            site_id: 7,
            status: "stopped",
            started_at: now - 80,
            stopped_at: now - 5,
          },
          {
            worker_guid: "new-worker",
            site_id: 7,
            status: "running",
            started_at: now - 2,
          },
        ],
        recent_work: [
          {
            id: 71,
            kind: "scheduled_run",
            lane: "scheduled_job",
            site_id: 7,
            worker_guid: "old-worker",
            job_id: 71,
            status: "running",
            started_at: now - 15,
          },
        ],
      },
      () => {},
      now,
      { dismissInactive: true, dismissStartedAt: now - 5 }
    );

    const stoppedWorker = graph.nodes.find((node) => node.id === "worker:old-worker");
    const activeWorker = graph.nodes.find((node) => node.id === "worker:new-worker");
    const taskNode = graph.nodes.find((node) => node.type === "task" && node.data.status === "running");
    const taskEdge = graph.edges.find((edge) => edge.target === taskNode?.id);

    expect(stoppedWorker).toBeTruthy();
    expect(activeWorker).toBeTruthy();
    expect(taskNode).toBeTruthy();
    expect(taskNode?.data.visualStatus).toBe("reassigning");
    expect(taskNode?.data.visualStatusLabel).toBe("Reassigning to New Worker");
    expect(statusTone(taskNode?.data.visualStatus)).toBe("queued");
    expect(taskEdge?.source).toBe(activeWorker?.id);
    expect(taskEdge?.source).not.toBe(stoppedWorker?.id);
  });

  it("does not retain stale active work rows when holding inactive workers", () => {
    const merged = mergeWorkerPayload(
      {
        workers: [],
        recent_work: [
          {
            id: 81,
            kind: "scheduled_run",
            site_id: 7,
            worker_guid: "old-worker",
            job_id: 81,
            status: "running",
          },
          {
            id: 82,
            kind: "scheduled_run",
            site_id: 7,
            worker_guid: "old-worker",
            job_id: 82,
            status: "succeeded",
          },
        ],
      },
      {
        workers: [],
        recent_work: [
          {
            id: 81,
            kind: "scheduled_run",
            site_id: 7,
            worker_guid: "old-worker",
            job_id: 81,
            status: "succeeded",
          },
        ],
      },
      true
    );

    expect(merged.recent_work).toHaveLength(2);
    expect(merged.recent_work.find((work) => work.id === 81)?.status).toBe("succeeded");
    expect(merged.recent_work.some((work) => work.id === 81 && work.status === "running")).toBe(false);
    expect(merged.recent_work.find((work) => work.id === 82)?.status).toBe("succeeded");
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

  it("lays out tasks first and centers workers to their task groups", () => {
    const graph = buildGraphAt(
      {
        workers: [
          {
            worker_guid: "worker-a",
            site_id: 7,
            status: "running",
          },
          {
            worker_guid: "worker-b",
            site_id: 8,
            status: "running",
          },
        ],
        recent_work: [
          {
            id: 51,
            kind: "scheduled_run",
            site_id: 7,
            worker_guid: "worker-a",
            job_id: 51,
            status: "queued",
          },
          {
            id: 52,
            kind: "scheduled_run",
            site_id: 7,
            worker_guid: "worker-a",
            job_id: 52,
            status: "queued",
          },
          {
            id: 53,
            kind: "scheduled_run",
            site_id: 8,
            worker_guid: "worker-b",
            job_id: 53,
            status: "queued",
          },
        ],
      },
      () => {},
      1000
    );

    const workerA = graph.nodes.find((node) => node.id === "worker:worker-a");
    const workerB = graph.nodes.find((node) => node.id === "worker:worker-b");
    const scheduler = graph.nodes.find((node) => node.data?.label === "Job Scheduler");
    const workerATasks = graph.nodes.filter((node) => node.type === "task" && graph.edges.some((edge) => edge.source === workerA?.id && edge.target === node.id));
    const workerBTasks = graph.nodes.filter((node) => node.type === "task" && graph.edges.some((edge) => edge.source === workerB?.id && edge.target === node.id));

    expect(workerATasks).toHaveLength(2);
    expect(Math.abs(workerA.position.y - ((workerATasks[0].position.y + workerATasks[1].position.y) / 2))).toBeLessThan(1);
    expect(workerB.position.y).toBe(workerBTasks[0].position.y);
    expect(workerBTasks[0].position.y - workerATasks[1].position.y).toBeGreaterThanOrEqual(72);
    expect(scheduler.position.y).toBeCloseTo((workerA.position.y + workerB.position.y) / 2, 0);
  });
});
