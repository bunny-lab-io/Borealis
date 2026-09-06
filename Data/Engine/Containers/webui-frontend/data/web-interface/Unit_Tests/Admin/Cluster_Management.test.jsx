import React from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import ClusterManagement, {
  RELEASE_MENU_PROPS,
  RELEASE_SELECT_PROPS,
  buildClusterOperationDetails,
  clusterReleaseOptionsAtOrAboveBaseline,
  clusterOperationNodeLabel,
  clusterNodeDatabaseStatus,
  clusterNodeRolesPresentation,
  clusterNodeStatusLabel,
  clusterNodeVersionPresentation,
  compareBorealisVersions,
  formatClusterTimestamp,
  friendlyClusterOperationName,
} from "@/Admin/Cluster_Management.jsx";

const state = vi.hoisted(() => ({
  enabled: true,
  hmrState: "active",
  activeSize: 3,
  desiredSize: 3,
  clusterStatus: "Healthy",
  releases: [],
  releaseError: "",
  releaseChannel: "stable",
  lastStableRelease: "2026.08.1",
  operations: [],
  events: [],
  database: { configured_instances: 3, ready_instances: 3, fully_ready: true },
  drainedNodeID: "",
  nodes: null,
  admissions: null,
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    useLoaderData: () => ({
      cluster: {
        enabled: state.enabled,
        status: state.clusterStatus,
        active_size: state.activeSize,
        desired_size: state.desiredSize,
        baseline_release: "2026.08.1",
        release_channel: state.releaseChannel,
        last_stable_release: state.lastStableRelease,
        k3s_version: "v1.36.3+k3s1",
        hmr: { state: state.hmrState, node_id: ["active", "restore_failed"].includes(state.hmrState) ? "11111111-1111-4111-8111-111111111111" : "" },
        leaders: {
          etcd_leader: "11111111-1111-4111-8111-111111111111",
          control_vip_owner: "11111111-1111-4111-8111-111111111111",
          edge_vip_owner: "11111111-1111-4111-8111-111111111111",
          postgres_primary: "11111111-1111-4111-8111-111111111111",
          scheduler_leader: "11111111-1111-4111-8111-111111111111",
          wireguard_owner: "11111111-1111-4111-8111-111111111111",
        },
        database: state.database,
        nodes: state.nodes ?? [
          {
            id: "11111111-1111-4111-8111-111111111111",
            node_name: "engine-1",
            membership_state: "Active",
            application_state: state.drainedNodeID === "11111111-1111-4111-8111-111111111111" ? "drained" : "active",
            roles: { control_vip_owner: true, k3s_version: "v1.36.3+k3s1" },
            probe_health: {
              startup: "passed",
              readiness: "passed",
              liveness: "passed",
              api: "passed",
              database: "passed",
              scheduler: "passed",
              webui: "passed",
              wireguard: "passed",
              storage: "passed",
            },
          },
          { id: "22222222-2222-4222-8222-222222222222", node_name: "engine-2", membership_state: "Active", application_state: state.drainedNodeID === "22222222-2222-4222-8222-222222222222" ? "drained" : "active", roles: {}, probe_health: {} },
          { id: "33333333-3333-4333-8333-333333333333", node_name: "engine-3", membership_state: "Active", application_state: state.drainedNodeID === "33333333-3333-4333-8333-333333333333" ? "drained" : "active", roles: {}, probe_health: {} },
        ],
        operations: state.operations,
        admissions: state.admissions ?? [{ id: "44444444-4444-4444-8444-444444444444", node_name: "engine-4", state: "Pending Quorum" }],
      },
      releases: { releases: state.releases },
      releaseError: state.releaseError,
      events: state.events,
      eventError: "",
      initialError: "",
    }),
  };
});

vi.mock("@/app/hooks/useAppNotifications.js", () => ({
  useAppNotifications: () => vi.fn(),
}));

vi.mock("@/app/hooks/useRoutePageChrome.js", () => ({
  useRoutePageChrome: vi.fn(),
}));

function renderClusterManagement(initialEntry = "/cluster-management") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <ClusterManagement />
    </MemoryRouter>
  );
}

async function openNodeActions(nodeName = "engine-1") {
  fireEvent.click(await screen.findByRole("button", { name: `${nodeName} Actions` }));
}

describe("Cluster Management", () => {
  afterEach(() => {
    cleanup();
    state.enabled = true;
    state.hmrState = "active";
    state.activeSize = 3;
    state.desiredSize = 3;
    state.clusterStatus = "Healthy";
    state.releases = [];
    state.releaseError = "";
    state.releaseChannel = "stable";
    state.lastStableRelease = "2026.08.1";
    state.operations = [];
    state.events = [];
    state.database = { configured_instances: 3, ready_instances: 3, fully_ready: true };
    state.drainedNodeID = "";
    state.nodes = null;
    state.admissions = null;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("keeps recorded release identity separate from report freshness and runtime verification", () => {
    const now = 1_787_770_000_000;
    const node = { release_tag: "2026.09.1.1", release_sha: "a".repeat(40), last_seen_at: now / 1000 - 10 };
    const version = clusterNodeVersionPresentation(node, [], now);
    expect(version.release).toBe("2026.09.1.1");
    expect(version.summary).toBe("Recorded · Recent report");
    expect(version.detail).toContain(`Recorded commit SHA: ${"a".repeat(40)}`);
    expect(version.detail).toContain("Runtime release identity: not freshly measured");
    expect(clusterNodeVersionPresentation({ ...node, last_seen_at: now / 1000 - 301 }, [], now).summary).toContain("Stale report");
    for (const last_seen_at of [null, undefined, 0, "bad", now / 1000 + 1]) {
      expect(clusterNodeVersionPresentation({ ...node, last_seen_at }, [], now).summary).toContain("Report time unknown");
    }
    expect(clusterNodeVersionPresentation({ ...node, release_sha: null }, [], now).summary).toContain("SHA unknown");
    expect(clusterNodeVersionPresentation({ last_seen_at: now / 1000 }, [], now).release).toBe("Unknown");
    expect(clusterNodeVersionPresentation(node, [], now, true).summary).toContain("Snapshot stale");
    expect(clusterNodeVersionPresentation({ ...node, release_tag: "2026.09.2-rc.1" }, [], now).release).toBe("2026.09.2-rc.1");
    expect(clusterNodeVersionPresentation({ ...node, release_tag: "dev-aaaaaaaaaaaa" }, [], now).release).toBe("dev-aaaaaaaaaaaa");
  });

  it("shows only applicable in-flight Engine targets without replacing recorded version", () => {
    const node = { id: "engine-1", membership_state: "Active", release_tag: "2026.09.1", release_sha: "a".repeat(40) };
    const operation = { kind: "engine_update", state: "running", target_release: "2026.09.2", target_sha: "b".repeat(40), payload: { scope: "all" } };
    for (const state of ["queued", "running", "waiting"]) {
      const version = clusterNodeVersionPresentation(node, [{ ...operation, state }]);
      expect(version.release).toBe("2026.09.1");
      expect(version.summary).toContain("Pending 2026.09.2");
      expect(version.detail).toContain(`Target commit SHA: ${"b".repeat(40)}`);
    }
    for (const state of ["failed", "cancelled", "succeeded"]) {
      expect(clusterNodeVersionPresentation(node, [{ ...operation, state }]).summary).not.toContain("Pending");
    }
    expect(clusterNodeVersionPresentation(node, [{ ...operation, kind: "k3s_update" }]).summary).not.toContain("Pending");
    expect(clusterNodeVersionPresentation({ ...node, membership_state: "Removed" }, [operation]).summary).not.toContain("Pending");
    expect(clusterNodeVersionPresentation(node, [{ ...operation, payload: { scope: "node", node_ids: ["engine-2"] } }]).summary).not.toContain("Pending");
    expect(clusterNodeVersionPresentation(node, [{ ...operation, payload: { scope: "node", node_ids: ["engine-1"] } }]).summary).toContain("Pending");
    expect(clusterNodeVersionPresentation(node, [{ ...operation, payload: { scope: "all", update_node_ids: ["engine-2"] } }]).summary).not.toContain("Pending");
    expect(clusterNodeVersionPresentation({ ...node, release_tag: operation.target_release, release_sha: operation.target_sha }, [operation]).summary).toContain("Target recorded 2026.09.2");
  });

  it("renders mixed versions and unknown identity across three node rows with full SHA tooltip", async () => {
    state.nodes = [
      { id: "node-1", node_name: "version-one", membership_state: "Active", release_tag: "2026.09.1", release_sha: "a".repeat(40) },
      { id: "node-2", node_name: "version-two", membership_state: "Active", release_tag: "2026.09.2", release_sha: "b".repeat(40) },
      { id: "node-3", node_name: "version-unknown", membership_state: "Active" },
    ];
    state.operations = [{ kind: "engine_update", state: "running", target_release: "2026.09.2", target_sha: "b".repeat(40), payload: { scope: "all" } }];
    renderClusterManagement("/cluster-management?tab=nodes");
    expect(await screen.findByText("Engine Version")).toBeInTheDocument();
    for (const [name, release] of [["version-one", "2026.09.1"], ["version-two", "2026.09.2"], ["version-unknown", "Unknown"]]) {
      const row = (await screen.findByText(name)).closest('[role="row"]');
      expect(within(row.querySelector('[col-id="engine-version"]')).getByText(release)).toBeInTheDocument();
    }
    const firstRow = screen.getByText("version-one").closest('[role="row"]');
    expect(within(firstRow).getByText(/Pending 2026.09.2/)).toBeInTheDocument();
    fireEvent.mouseOver(within(firstRow).getByText("2026.09.1"));
    expect(await screen.findByRole("tooltip")).toHaveTextContent(`Recorded commit SHA: ${"a".repeat(40)}`);
    expect(screen.getByRole("tooltip")).toHaveTextContent("not freshly measured");
  });

  it.each(["failed", "hanging"])("ages retained version records during %s polling", async (failure) => {
    let now = 1_787_770_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    let poll;
    const originalInterval = window.setInterval.bind(window);
    vi.spyOn(window, "setInterval").mockImplementation((callback, delay, ...args) => {
      if (delay === 5000) { poll = callback; return 0; }
      return originalInterval(callback, delay, ...args);
    });
    state.nodes = [{ id: "node-1", node_name: "version-one", membership_state: "Active", release_tag: "2026.09.1", release_sha: "a".repeat(40), last_seen_at: now / 1000 }];
    vi.stubGlobal("fetch", vi.fn(() => failure === "failed" ? Promise.reject(new Error("offline")) : new Promise(() => {})));
    renderClusterManagement("/cluster-management?tab=nodes");
    expect(await screen.findByText(/Recorded · Recent report/)).toBeInTheDocument();
    await act(async () => { now += failure === "failed" ? 5000 : 20_000; poll(); });
    expect(await screen.findByText(/Snapshot stale/)).toBeInTheDocument();
    expect(screen.getByText("2026.09.1")).toBeInTheDocument();
    const nextRelease = failure === "hanging" ? "2026.09.1" : "2026.09.2";
    vi.stubGlobal("fetch", vi.fn(async (path) => ({ ok: true, json: async () => String(path) === "/api/server/cluster" ? {
      enabled: true, nodes: [{ ...state.nodes[0], release_tag: nextRelease, release_sha: "b".repeat(40) }], operations: [],
    } : { releases: [], events: [] } })));
    await act(async () => { now += 5000; poll(); });
    expect(await screen.findByText(nextRelease)).toBeInTheDocument();
    expect(screen.getByText(/Recorded · Recent report/)).toBeInTheDocument();
    expect(screen.queryByText(/Snapshot stale/)).not.toBeInTheDocument();
    fireEvent.mouseOver(screen.getByText(nextRelease));
    expect(await screen.findByRole("tooltip")).toHaveTextContent(`Recorded commit SHA: ${"b".repeat(40)}`);
    // Same tag, report time and visible status: only the tooltip SHA changes.
    vi.stubGlobal("fetch", vi.fn(async (path) => ({ ok: true, json: async () => String(path) === "/api/server/cluster" ? {
      enabled: true, nodes: [{ ...state.nodes[0], release_tag: nextRelease, release_sha: "c".repeat(40) }], operations: [],
    } : { releases: [], events: [] } })));
    await act(async () => { now += 5000; poll(); });
    fireEvent.mouseOver(screen.getByText(nextRelease));
    await waitFor(() => expect(screen.getByRole("tooltip")).toHaveTextContent(`Recorded commit SHA: ${"c".repeat(40)}`));
  });

  it("receives node snapshots before slow catalog/history and does not redate them later", async () => {
    let now = 1_787_770_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    let poll;
    const originalInterval = window.setInterval.bind(window);
    vi.spyOn(window, "setInterval").mockImplementation((callback, delay, ...args) => {
      if (delay === 5000) { poll = callback; return 0; }
      return originalInterval(callback, delay, ...args);
    });
    state.nodes = [{ id: "node-1", node_name: "version-one", membership_state: "Active", release_tag: "2026.09.1", release_sha: "a".repeat(40), last_seen_at: now / 1000 }];
    let requests = 0;
    const auxiliary = [];
    vi.stubGlobal("fetch", vi.fn((path) => {
      if (path === "/api/server/cluster") {
        requests += 1;
        if (requests > 1) return new Promise(() => {});
        return Promise.resolve({ ok: true, json: async () => ({ enabled: true, nodes: [{ ...state.nodes[0], release_tag: "2026.09.2" }], operations: [] }) });
      }
      return new Promise((resolve) => auxiliary.push(resolve));
    }));
    renderClusterManagement("/cluster-management?tab=nodes");
    expect(await screen.findByText("2026.09.1")).toBeInTheDocument();
    await act(async () => { now += 5000; poll(); });
    expect(await screen.findByText("2026.09.2")).toBeInTheDocument();
    expect(screen.getByText(/Recorded · Recent report/)).toBeInTheDocument();
    await act(async () => { now += 20_000; poll(); });
    expect(await screen.findByText(/Snapshot stale/)).toBeInTheDocument();
    await act(async () => { auxiliary.forEach((resolve) => resolve({ ok: true, json: async () => ({ releases: [], events: [] }) })); });
    expect(screen.getByText(/Snapshot stale/)).toBeInTheDocument();
  });

  it.each(["failure", "success"])("ignores older polling %s after newer snapshot arrives", async (completion) => {
    let now = 1_787_770_000_000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    let poll;
    const originalInterval = window.setInterval.bind(window);
    vi.spyOn(window, "setInterval").mockImplementation((callback, delay, ...args) => {
      if (delay === 5000) { poll = callback; return 0; }
      return originalInterval(callback, delay, ...args);
    });
    state.nodes = [{ id: "node-1", node_name: "version-one", membership_state: "Active", release_tag: "2026.09.1", release_sha: "a".repeat(40), last_seen_at: now / 1000 }];
    const pending = [];
    const response = (release) => ({ ok: true, json: async () => ({ enabled: true, nodes: [{ ...state.nodes[0], release_tag: release }], operations: [] }) });
    vi.stubGlobal("fetch", vi.fn((path) => path === "/api/server/cluster"
      ? new Promise((resolve, reject) => pending.push({ resolve, reject }))
      : Promise.resolve({ ok: true, json: async () => ({ releases: [], events: [] }) })));
    renderClusterManagement("/cluster-management?tab=nodes");
    expect(await screen.findByText("2026.09.1")).toBeInTheDocument();
    await act(async () => { now += 5000; poll(); });
    await act(async () => { now += 5000; poll(); });
    await act(async () => { pending[1].resolve(response("2026.09.2")); });
    expect(await screen.findByText("2026.09.2")).toBeInTheDocument();
    await act(async () => { completion === "failure" ? pending[0].reject(new Error("old failure")) : pending[0].resolve(response("2026.09.1")); });
    expect(screen.getByText("2026.09.2")).toBeInTheDocument();
    expect(screen.getByText(/Recorded · Recent report/)).toBeInTheDocument();
    expect(screen.queryByText(/Snapshot stale/)).not.toBeInTheDocument();
  });

  it("cancels only pending admissions and exposes retained-target renewal", async () => {
    state.hmrState = "inactive";
    state.activeSize = 1;
    state.admissions = [
      { id: "44444444-4444-4444-8444-444444444444", node_name: "engine-4", state: "Pending Quorum" },
      { id: "55555555-5555-4555-8555-555555555555", node_name: "engine-5", state: "Recovery Required" },
    ];
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") return { ok: true, json: async () => ({ state: "Cancelled" }) };
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: [] }) };
      if (String(path).includes("/events")) return { ok: true, json: async () => ({ events: [] }) };
      return { ok: true, json: async () => ({ enabled: true, admissions: state.admissions, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement("/cluster-management?tab=maintenance");
    expect(screen.getAllByRole("button", { name: "Cancel Admission" })).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Renew Invitation" })).toBeEnabled();
    expect(screen.getByText(/Node identity retained/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel Admission" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/admissions/44444444-4444-4444-8444-444444444444/cancel",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ confirmation: "CANCEL ADMISSION" }) })
    ));
  });

  it("offers retry for retained cancelled admission operations without unsafe cancellation", async () => {
    state.operations = [
      { id: "44444444-4444-4444-8444-444444444444", kind: "membership_admit", state: "queued", current_step: "preflight", created_at: 1787770000 },
      { id: "55555555-5555-4555-8555-555555555555", kind: "membership_admit", state: "cancelled", current_step: "cancelled", created_at: 1787770001 },
    ];
    renderClusterManagement("/cluster-management?tab=operations");
    expect(await screen.findByRole("button", { name: "Retry" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
  });

  it("formats operator-facing node and operation labels", () => {
    expect(clusterNodeStatusLabel({ membership_state: "Active", application_state: "drained" })).toBe("Active / Drained");
    expect(clusterNodeStatusLabel({ membership_state: "Active", application_state: "active", cordoned: true })).toBe("Active / Cordoned");
    expect(friendlyClusterOperationName({ kind: "node_maintenance", payload: { action: "enter" } })).toBe("Maintenance Mode Enabled");
    expect(friendlyClusterOperationName({ kind: "node_maintenance", payload: { action: "exit" } })).toBe("Maintenance Mode Disabled");
    expect(friendlyClusterOperationName({ kind: "hmr_start" })).toBe("Cluster-Wide Node Isolation Enabled");
    expect(friendlyClusterOperationName({ kind: "hmr_exit" })).toBe("Cluster-Wide Node Isolation Disabled");
    expect(formatClusterTimestamp(1_787_770_000)).toMatch(/^\d{2}\/\d{2}\/\d{4} @ \d{2}:\d{2}:\d{2}$/);
  });

  it("separates database state from role ownership", () => {
    const primaryID = "11111111-1111-4111-8111-111111111111";
    const healthyDatabase = { configured_instances: 3, ready_instances: 3, fully_ready: true };
    expect(clusterNodeDatabaseStatus({ id: primaryID, membership_state: "Active", roles: {} }, primaryID, healthyDatabase, 3)).toBe("Active");
    expect(clusterNodeDatabaseStatus({ id: "22222222-2222-4222-8222-222222222222", membership_state: "Active", roles: {} }, primaryID, healthyDatabase, 3)).toBe("Replica Healthy");
    expect(clusterNodeDatabaseStatus({ id: "22222222-2222-4222-8222-222222222222", membership_state: "Active", roles: {} }, primaryID, { ...healthyDatabase, ready_instances: 2, fully_ready: false }, 3)).toBe("Not Ready");
    expect(clusterNodeDatabaseStatus({ id: "33333333-3333-4333-8333-333333333333", membership_state: "Removed", roles: {} }, primaryID, healthyDatabase, 3)).toBe("Not Active");
    expect(clusterNodeRolesPresentation({ roles: { edge_vip_owner: true, postgres_primary: true } })).toMatchObject({
      ownershipLabels: ["Cluster Virtual IP Owner"],
      label: "Cluster Virtual IP Owner",
    });
  });

  it("orders monthly revisions and hotfixes while hiding older release options", () => {
    expect(compareBorealisVersions("2026.08.7", "2026.08.7")).toBe(0);
    expect(compareBorealisVersions("2026.08.7.1", "2026.08.7")).toBe(1);
    expect(compareBorealisVersions("2026.09.1", "2026.08.99.9")).toBe(1);
    expect(compareBorealisVersions("2026.08.6", "2026.08.7")).toBe(-1);
    expect(compareBorealisVersions("2026.08.7-rc.1", "2026.08.7-rc.2")).toBe(-1);
    expect(compareBorealisVersions("2026.08.7-rc.2", "2026.08.7")).toBe(-1);
    expect(compareBorealisVersions("v2026.08.8", "2026.08.7")).toBeNull();

    const releases = [
      { tag: "2026.08.6", selectable: true },
      { tag: "2026.08.7", selectable: true },
      { tag: "2026.08.7.1", selectable: true },
      { tag: "2026.08.8", selectable: false, reason: "K3s baseline mismatch" },
      { tag: "2026.08.8-rc.1", channel: "qualification", selectable: true },
    ];
    expect(clusterReleaseOptionsAtOrAboveBaseline(releases, "2026.08.7").map((release) => release.tag)).toEqual([
      "2026.08.7",
      "2026.08.7.1",
      "2026.08.8",
    ]);
    expect(clusterReleaseOptionsAtOrAboveBaseline(releases, "dev-fedcba987654").map((release) => release.tag)).toEqual([
      "2026.08.6",
      "2026.08.7",
      "2026.08.7.1",
    ]);
    expect(clusterReleaseOptionsAtOrAboveBaseline(releases, "2026.08.7", "qualification").map((release) => release.tag)).toEqual([
      "2026.08.8-rc.1",
    ]);
  });

  it("resolves operation hostnames and redacts copied lifecycle details", () => {
    const operation = {
      id: "11111111-1111-4111-8111-111111111199",
      kind: "membership_admit",
      state: "running",
      current_step: "apply_membership",
      attempt: 1,
      payload: { admission_ids: ["44444444-4444-4444-8444-444444444444"], invite_bundle: "temporary-secret" },
    };
    const events = [{
      id: 42,
      event_type: "operation_step_passed",
      state: "running",
      message: "Kubernetes membership applied; node conformance started.",
      details: { next_step: "wait_node_conformance", token: "temporary-secret" },
    }];
    const nodeLabel = clusterOperationNodeLabel(
      operation,
      [],
      [{ id: "44444444-4444-4444-8444-444444444444", node_name: "engine-4" }],
      events
    );
    const details = buildClusterOperationDetails(operation, events, nodeLabel);

    expect(nodeLabel).toBe("engine-4");
    expect(details.summary).toBe("Kubernetes membership applied; node conformance started.");
    expect(details.copyText).toContain("operation_step_passed");
    expect(details.copyText).toContain("wait_node_conformance");
    expect(details.copyText).toContain("[redacted]");
    expect(details.copyText).not.toContain("temporary-secret");
  });

  it("shows role ownership and five management views without duplicate isolation warning", () => {
    renderClusterManagement();

    for (const tab of ["Overview", "Nodes", "Database", "Cluster Events", "Maintenance"]) {
      expect(screen.getByRole("tab", { name: tab })).toBeInTheDocument();
    }
    expect(screen.queryByRole("tab", { name: "Updates" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Cluster-Wide Node Isolation active/)).not.toBeInTheDocument();
    expect(screen.getByText("etcd leader")).toBeInTheDocument();
    expect(screen.getByText("WireGuard owner")).toBeInTheDocument();
    expect(screen.getAllByText("engine-1").length).toBeGreaterThan(1);
  });

  it("labels isolation recovery and explains monthly revision and hotfix versions", async () => {
    state.hmrState = "inactive";
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText("Cluster-Wide Node Isolation")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Isolated Node" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Enable Isolation" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disable Isolation" })).toBeDisabled();
    expect(screen.getByText(/New clustered HMR entry is disabled/)).toBeInTheDocument();
    expect(screen.getByText(/published, non-prerelease GitHub releases/)).toBeInTheDocument();
    expect(screen.getByText(/YYYY\.MM\.REVISION for normal monthly releases/)).toBeInTheDocument();
    expect(screen.getByText(/YYYY\.MM\.REVISION\.HOTFIX for focused corrections/)).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Target Engine Version" })).toBeInTheDocument();
    expect(screen.getByText(/Older versions are not shown/)).toBeInTheDocument();

    fireEvent.mouseOver(screen.getByRole("button", { name: "Borealis version format" }));
    expect(await screen.findByRole("tooltip")).toHaveTextContent(/REVISION counts published updates within that month/);
  });

  it("renders node status, identity, database, role, probe, and action columns", async () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    for (const column of ["Status", "Node", "IP Address", "Database", "Roles", "Probes", "Actions"]) {
      expect(await screen.findByRole("columnheader", { name: column })).toBeInTheDocument();
    }
    expect(await screen.findAllByText("Active / Active")).toHaveLength(3);
    expect(screen.getByText("Cluster Virtual IP Owner")).toBeInTheDocument();
    const activeDatabaseLink = screen.getByRole("button", { name: "Active" });
    expect(activeDatabaseLink).toHaveStyle({ color: "#58a6ff", fontWeight: "500", textDecoration: "none" });
    expect(screen.getAllByText("Replica Healthy")).toHaveLength(2);
    expect(screen.queryByText(/PostgreSQL \(/)).not.toBeInTheDocument();
    expect(screen.getByText("9/9 Passed")).toBeInTheDocument();
    expect(screen.queryByText(/k3s version/i)).not.toBeInTheDocument();

    fireEvent.click(activeDatabaseLink);
    expect(screen.getByRole("tab", { name: "Database" })).toHaveAttribute("aria-selected", "true");
  });

  it("explains empty node inventory when Cluster Management is not enabled", async () => {
    state.nodes = [];
    renderClusterManagement("/cluster-management?tab=nodes");

    expect(await screen.findByText("Borealis Cluster Management Not Enabled on this Node")).toBeInTheDocument();
  });

  it.each([1, 3])("offers no new HMR entry on %i-node clusters", (size) => {
    state.hmrState = "inactive";
    state.activeSize = size;
    state.desiredSize = size;
    renderClusterManagement("/cluster-management?tab=maintenance");
    expect(screen.queryByRole("button", { name: "Enable Isolation" })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Isolated Node" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disable Isolation" })).toBeDisabled();
  });

  it.each(["active", "restore_failed"])("restores existing %s isolation through unchanged exit contract", async (hmrState) => {
    state.hmrState = hmrState;
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") return { ok: true, status: 202, json: async () => ({ operation_id: "44444444-4444-4444-8444-444444444444" }) };
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: [] }) };
      if (String(path).includes("/events")) return { ok: true, json: async () => ({ events: [] }) };
      return { ok: true, json: async () => ({ enabled: true, hmr: { state: "restoring" }, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement("/cluster-management?tab=maintenance");
    expect(screen.getByText("Isolated node: engine-1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Disable Isolation" }));
    fireEvent.change(screen.getByLabelText("Typed confirmation"), { target: { value: "EXIT HMR" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/hmr/exit",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ confirmation: "EXIT HMR" }) }),
    ));
    expect(fetchMock.mock.calls.some(([path]) => String(path).endsWith("/hmr/start"))).toBe(false);
  });

  it("enables clustering with one private Cluster Virtual IP", async () => {
    state.enabled = false;
    state.hmrState = "inactive";
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") {
        return { ok: true, status: 202, json: async () => ({ operation_id: "44444444-4444-4444-8444-444444444444" }) };
      }
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: [] }) };
      if (String(path).includes("/events")) return { ok: true, json: async () => ({ events: [] }) };
      return { ok: true, json: async () => ({ enabled: false, active_size: 1, hmr: { state: "inactive" }, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement("/cluster-management?tab=maintenance");

    fireEvent.click(screen.getByRole("button", { name: "Enable Cluster" }));
    const enableDialog = screen.getByRole("dialog");
    expect(within(enableDialog).getByText("Enable cluster")).toBeInTheDocument();
    expect(within(enableDialog).getByText("Set address shared by K3s API, ingress, and WireGuard.")).toBeInTheDocument();
    expect(screen.getByLabelText("Cluster Virtual IP")).toBeInTheDocument();
    for (const removedField of ["Control-plane VIP", "Borealis edge VIP", "Current node management IPv4", "Current node name", "Architecture"]) {
      expect(screen.queryByLabelText(removedField)).not.toBeInTheDocument();
    }
    expect(screen.queryByLabelText("Typed confirmation")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Cluster Virtual IP"), { target: { value: "8.8.8.8" } });
    fireEvent.click(within(enableDialog).getByRole("button", { name: "Enable Cluster" }));
    expect(screen.getByText("Valid private Cluster Virtual IP required.")).toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([, options]) => options?.method === "POST")).toBe(false);

    fireEvent.change(screen.getByLabelText("Cluster Virtual IP"), { target: { value: "192.168.3.249" } });
    fireEvent.click(within(enableDialog).getByRole("button", { name: "Enable Cluster" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/enable",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ cluster_vip: "192.168.3.249" }) }),
    ));
  });

  it("disables isolation when operator exits maintenance on drained standby", async () => {
    state.drainedNodeID = "22222222-2222-4222-8222-222222222222";
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") {
        return { ok: true, status: 202, json: async () => ({ operation_id: "44444444-4444-4444-8444-444444444444" }) };
      }
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: [] }) };
      if (String(path).includes("/events")) return { ok: true, json: async () => ({ events: [] }) };
      return { ok: true, json: async () => ({ enabled: true, active_size: 3, hmr: { state: "restoring" }, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement("/cluster-management?tab=nodes");

    await openNodeActions("engine-2");
    fireEvent.click(screen.getByRole("menuitem", { name: /Exit Maintenance Mode/ }));
    expect(screen.getByText("Cluster-Wide Node Isolation will be disabled if the node exits maintenance mode.")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Typed confirmation"), { target: { value: "EXIT HMR" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/hmr/exit",
      expect.objectContaining({ method: "POST", body: JSON.stringify({ confirmation: "EXIT HMR" }) }),
    ));
  });

  it("uses active Admin session without a cluster step-up prompt", async () => {
    state.hmrState = "inactive";
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    await openNodeActions();
    expect(screen.getByText("Drain active roles to other nodes")).toBeInTheDocument();
    expect(screen.getByText("Install selected same-version or newer Engine version")).toBeInTheDocument();
    expect(screen.getByText("Safely remove two cluster nodes")).toBeInTheDocument();
    expect(screen.getByText("Remove externally fenced node")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: /Enter Maintenance Mode/ }));

    expect(screen.getByText("Administrator access required. Destructive actions also require exact typed confirmation.")).toBeInTheDocument();
    expect(screen.queryByText(/Sign in again/)).not.toBeInTheDocument();
  });

  it("explains empty release catalog", () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText(/No approved Engine release exists at or above baseline 2026.08.1/)).toBeInTheDocument();
  });

  it("shows unsupported qualification state and submits whole-cluster acknowledgement", async () => {
    state.hmrState = "inactive";
    state.releaseChannel = "qualification";
    state.releases = [{ tag: "2026.08.2-rc.1", title: "Cluster candidate", channel: "qualification", selectable: true }];
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") {
        return { ok: true, status: 202, json: async () => ({ operation_id: "44444444-4444-4444-8444-444444444444" }) };
      }
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: state.releases }) };
      if (String(path).includes("/events")) return { ok: true, json: async () => ({ events: [] }) };
      return { ok: true, json: async () => ({ enabled: true, status: "Healthy", active_size: 3, desired_size: 3, baseline_release: "2026.08.2-rc.1", release_channel: "qualification", last_stable_release: "2026.08.1", hmr: { state: "inactive" }, database: state.database, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement("/cluster-management?tab=maintenance");

    expect(screen.getByText(/Qualification channel active/)).toHaveTextContent(/unsupported for production/);
    fireEvent.mouseDown(screen.getByRole("combobox", { name: "Qualification Version" }));
    fireEvent.click(await screen.findByRole("option", { name: /2026\.08\.2-rc\.1 is compatible/ }));
    const deployButton = screen.getByRole("button", { name: "Deploy Qualification One Node at a Time" });
    await waitFor(() => expect(deployButton).toBeEnabled());
    fireEvent.click(deployButton);
    expect(await screen.findByText(/cannot downgrade back to last stable source/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Typed confirmation"), { target: { value: "DEPLOY QUALIFICATION" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/updates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          scope: "all",
          node_ids: [],
          release_tag: "2026.08.2-rc.1",
          confirmation: "DEPLOY QUALIFICATION",
          maintenance_outage_acknowledgement: "",
        }),
      }),
    ));
  });

  it("bounds release catalog menu dimensions", () => {
    expect(RELEASE_SELECT_PROPS).toMatchObject({ autoWidth: true, MenuProps: RELEASE_MENU_PROPS });
    expect(RELEASE_MENU_PROPS.PaperProps.sx).toMatchObject({
      width: 720,
      maxWidth: "calc(100vw - 32px)",
      maxHeight: 360,
    });
    expect(RELEASE_MENU_PROPS.PaperProps.sx["& .MuiMenuItem-root"].whiteSpace).toBe("normal");
  });

  it("shows reduced PostgreSQL readiness separately from configured instances", async () => {
    state.clusterStatus = "Degraded Database";
    state.database = {
      configured_instances: 3,
      ready_instances: 2,
      fully_ready: false,
      durability_quorum: true,
      synchronous_acknowledgements: 1,
      phase: "Waiting for the instances to become active",
    };
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Database" }));

    expect(screen.getAllByText(/2 of 3 PostgreSQL instances are Ready/).length).toBeGreaterThan(0);
    expect(screen.getByText("Configured / ready")).toBeInTheDocument();
    expect(screen.getByText("3 / 2")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    await openNodeActions();
    expect(screen.getByRole("menuitem", { name: /Enter Maintenance/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /Update Node/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /Remove Pair/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /Emergency Remove/ })).not.toHaveAttribute("aria-disabled", "true");
  });

  it("keeps normal controls fenced when mixed-version status masks database recovery", () => {
    state.hmrState = "inactive";
    state.clusterStatus = "Mixed Version";
    state.database = { configured_instances: 3, ready_instances: 2, fully_ready: false, durability_quorum: true };
    renderClusterManagement();

    expect(screen.getByText(/PostgreSQL recovery remains required even while cluster lifecycle status is Mixed Version/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));
    expect(screen.getByRole("button", { name: "Update All One at a Time" })).toBeDisabled();
  });

  it("allows drained-node recovery while fencing additional application drains", async () => {
    state.hmrState = "inactive";
    state.drainedNodeID = "11111111-1111-4111-8111-111111111111";
    renderClusterManagement();

    expect(screen.getByText(/active cluster members remain application-drained/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    expect(await screen.findByText("Active / Drained")).toBeInTheDocument();
    await openNodeActions("engine-1");
    expect(screen.getByRole("menuitem", { name: /Exit Maintenance/ })).not.toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /Update Node/ })).toHaveAttribute("aria-disabled", "true");
  });

  it("uses friendly operation labels and removes retry from superseded failures", async () => {
    state.operations = [{
      id: "11111111-1111-4111-8111-111111111199",
      kind: "membership_admit",
      current_step: "apply_membership",
      state: "failed",
      attempt: 2,
      error: "apply_membership: Kubernetes API returned HTTP 503: noisy detail",
      superseded_by: "22222222-2222-4222-8222-222222222299",
    }];
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Cluster Events" }));

    expect(await screen.findByText("Cluster Node Added")).toBeInTheDocument();
    expect(screen.getByText("Superseded")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Retry/ })).not.toBeInTheDocument();
  });

  it("renders failed operations with friendly timestamp and no inline retry affordance", async () => {
    state.operations = [{
      id: "11111111-1111-4111-8111-111111111199",
      kind: "node_remove",
      current_step: "prepare_member_removal",
      state: "failed",
      attempt: 3,
      created_at: 1_787_770_000,
      error: "prepare_member_removal: node action failed",
      target_node_id: "11111111-1111-4111-8111-111111111111",
      payload: { emergency: false },
    }];
    renderClusterManagement("/cluster-management?tab=operations");

    for (const column of ["Node", "Status", "Operation", "Details", "Timestamp"]) {
      expect(await screen.findByRole("columnheader", { name: column })).toBeInTheDocument();
    }
    const headers = await screen.findAllByRole("columnheader");
    expect(headers.map((header) => header.querySelector(".ag-header-cell-text")?.textContent)).toEqual([
      "Node",
      "Status",
      "Operation",
      "Details",
      "Timestamp",
    ]);
    expect(screen.getByText("engine-1")).toBeInTheDocument();
    expect(screen.getByText("Node Pair Removed")).toBeInTheDocument();
    expect(screen.getByText(/^\d{2}\/\d{2}\/\d{4} @ \d{2}:\d{2}:\d{2}$/)).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Retry/ })).not.toBeInTheDocument();
  });

  it("shows linked lifecycle details and copies redacted diagnostics", async () => {
    const operationID = "11111111-1111-4111-8111-111111111199";
    state.operations = [{
      id: operationID,
      kind: "membership_admit",
      current_step: "apply_membership",
      state: "running",
      attempt: 1,
      created_at: 1_787_770_000,
      requested_by: "admin",
      payload: { node_names: ["engine-2", "engine-3"], invite_bundle: "temporary-secret" },
    }];
    state.events = [{
      id: 42,
      operation_id: operationID,
      event_type: "operation_step_passed",
      state: "running",
      message: "Kubernetes membership applied; node conformance started.",
      details: { step: "apply_membership", next_step: "wait_node_conformance", token: "temporary-secret" },
      created_at: 1_787_770_010,
    }];
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window.navigator, "clipboard", { configurable: true, value: { writeText } });

    renderClusterManagement("/cluster-management?tab=operations");

    expect(await screen.findByText("engine-2, engine-3")).toBeInTheDocument();
    expect(screen.getByText("Kubernetes membership applied; node conformance started.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Copy details for Cluster Node Added" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    const copied = writeText.mock.calls[0][0];
    expect(copied).toContain("Node: engine-2, engine-3");
    expect(copied).toContain("operation_step_passed");
    expect(copied).toContain("wait_node_conformance");
    expect(copied).toContain("[redacted]");
    expect(copied).not.toContain("temporary-secret");
  });

  it("requires paired safe removal and explicit external fencing for emergency removal", async () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    await openNodeActions();
    fireEvent.click(screen.getByRole("menuitem", { name: /Remove Pair/ }));
    expect(screen.getByText(/Safe downscale removes two nodes sequentially/)).toBeInTheDocument();
    expect(screen.getByLabelText("Paired removal node")).toBeInTheDocument();
    expect(screen.getByText("Type REMOVE NODE PAIR")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await openNodeActions();
    fireEvent.click(screen.getByRole("menuitem", { name: /Emergency Remove/ }));
    expect(screen.getByText(/Target must be powered off and unable to rejoin/)).toBeInTheDocument();
    expect(screen.getByLabelText("External fencing confirmation")).toBeInTheDocument();
    expect(screen.getByText("Type EMERGENCY REMOVE NODE")).toBeInTheDocument();
  });

  it("fences expansion after supported three-node membership", async () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText(/Three-node release limit reached/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Node Invitation" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Request Pair Expansion" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve Pair" })).toBeDisabled();

    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    await openNodeActions();
    expect(screen.getByRole("menuitem", { name: /Remove Pair/ })).not.toHaveAttribute("aria-disabled", "true");
  });

  it("fences removal from unsupported five-plus membership", async () => {
    state.activeSize = 5;
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    await openNodeActions();
    expect(screen.getByRole("menuitem", { name: /Remove Pair/ })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /Emergency Remove/ })).toHaveAttribute("aria-disabled", "true");
  });

  it("keeps one-to-three membership controls available", () => {
    state.activeSize = 1;
    state.desiredSize = 1;
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByRole("button", { name: "Create Node Invitation" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Request Pair Expansion" })).toBeEnabled();
  });

  it("offers exactly one replacement path for degraded two-of-three membership", () => {
    state.hmrState = "inactive";
    state.activeSize = 2;
    state.desiredSize = 3;
    state.clusterStatus = "Degraded Quorum";
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText(/running on two surviving members/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Node Invitation" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Approve Replacement" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Request Pair Expansion" })).not.toBeInTheDocument();
  });

  it("submits K3s upgrade through distinct ordered all-server contract", async () => {
    state.hmrState = "inactive";
    const fetchMock = vi.fn(async (path, options = {}) => {
      if (options.method === "POST") {
        return { ok: true, status: 202, json: async () => ({ operation_id: "44444444-4444-4444-8444-444444444444" }) };
      }
      if (String(path).endsWith("/releases")) return { ok: true, json: async () => ({ releases: [] }) };
      return { ok: true, json: async () => ({ enabled: true, active_size: 3, hmr: { state: "inactive" }, nodes: [], operations: [] }) };
    });
    vi.stubGlobal("fetch", fetchMock);
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));
    fireEvent.click(screen.getByRole("button", { name: "Upgrade K3s One Server at a Time" }));
    fireEvent.change(screen.getByLabelText("Stable K3s target"), { target: { value: "v1.36.4+k3s1" } });
    fireEvent.change(screen.getByLabelText("Typed confirmation"), { target: { value: "UPDATE K3S" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "/api/server/cluster/updates",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          update_type: "k3s",
          scope: "all",
          node_ids: [],
          k3s_version: "v1.36.4+k3s1",
          confirmation: "UPDATE K3S",
          maintenance_outage_acknowledgement: "",
        }),
      }),
    ));
  });
});
