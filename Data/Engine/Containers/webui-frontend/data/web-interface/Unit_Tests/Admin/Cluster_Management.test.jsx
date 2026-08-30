import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import ClusterManagement, {
  buildClusterOperationDetails,
  clusterOperationNodeLabel,
  clusterNodeDatabaseStatus,
  clusterNodeRolesPresentation,
  clusterNodeStatusLabel,
  formatClusterTimestamp,
  friendlyClusterOperationName,
} from "@/Admin/Cluster_Management.jsx";

const state = vi.hoisted(() => ({
  hmrState: "active",
  activeSize: 3,
  desiredSize: 3,
  clusterStatus: "Healthy",
  releases: [],
  releaseError: "",
  operations: [],
  events: [],
  database: { configured_instances: 3, ready_instances: 3, fully_ready: true },
  drainedNodeID: "",
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    useLoaderData: () => ({
      cluster: {
        enabled: true,
        status: state.clusterStatus,
        active_size: state.activeSize,
        desired_size: state.desiredSize,
        baseline_release: "2026.08.1",
        k3s_version: "v1.36.3+k3s1",
        hmr: { state: state.hmrState, node_id: state.hmrState === "active" ? "11111111-1111-4111-8111-111111111111" : "" },
        leaders: {
          etcd_leader: "11111111-1111-4111-8111-111111111111",
          control_vip_owner: "11111111-1111-4111-8111-111111111111",
          edge_vip_owner: "11111111-1111-4111-8111-111111111111",
          postgres_primary: "11111111-1111-4111-8111-111111111111",
          scheduler_leader: "11111111-1111-4111-8111-111111111111",
          wireguard_owner: "11111111-1111-4111-8111-111111111111",
        },
        database: state.database,
        nodes: [
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
        admissions: [{ id: "44444444-4444-4444-8444-444444444444", node_name: "engine-4", state: "Pending Quorum" }],
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
    state.hmrState = "active";
    state.activeSize = 3;
    state.desiredSize = 3;
    state.clusterStatus = "Healthy";
    state.releases = [];
    state.releaseError = "";
    state.operations = [];
    state.events = [];
    state.database = { configured_instances: 3, ready_instances: 3, fully_ready: true };
    state.drainedNodeID = "";
    vi.unstubAllGlobals();
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
      ownershipLabels: ["Edge VIP Owner"],
      label: "Edge VIP Owner",
    });
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

  it("labels development isolation and explains stable GitHub releases", () => {
    state.hmrState = "inactive";
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText("Cluster-Wide Node Isolation")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Isolated Node" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Isolated Node" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Enable Isolation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disable Isolation" })).toBeInTheDocument();
    expect(screen.getByText(/published, non-prerelease GitHub releases/)).toBeInTheDocument();
    expect(screen.getByText(/YYYY\.MM\.DD\.N/)).toBeInTheDocument();
  });

  it("renders node status, identity, database, role, probe, and action columns", async () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    for (const column of ["Status", "Node", "IP Address", "Database", "Roles", "Probes", "Actions"]) {
      expect(await screen.findByRole("columnheader", { name: column })).toBeInTheDocument();
    }
    expect(await screen.findAllByText("Active / Active")).toHaveLength(3);
    expect(screen.getByText("Control VIP Owner")).toBeInTheDocument();
    const activeDatabaseLink = screen.getByRole("button", { name: "Active" });
    expect(activeDatabaseLink).toHaveStyle({ color: "#58a6ff", fontWeight: "500", textDecoration: "none" });
    expect(screen.getAllByText("Replica Healthy")).toHaveLength(2);
    expect(screen.queryByText(/PostgreSQL \(/)).not.toBeInTheDocument();
    expect(screen.getByText("9/9 Passed")).toBeInTheDocument();
    expect(screen.queryByText(/k3s version/i)).not.toBeInTheDocument();

    fireEvent.click(activeDatabaseLink);
    expect(screen.getByRole("tab", { name: "Database" })).toHaveAttribute("aria-selected", "true");
  });

  it("locks isolated-node selection to current target while isolation is active", () => {
    renderClusterManagement("/cluster-management?tab=maintenance");

    const isolatedNode = screen.getByRole("combobox", { name: "Isolated Node" });
    expect(isolatedNode).toHaveAttribute("aria-disabled", "true");
    expect(isolatedNode).toHaveTextContent("engine-1");
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
    expect(screen.getByText("Install selected Engine release")).toBeInTheDocument();
    expect(screen.getByText("Safely remove two cluster nodes")).toBeInTheDocument();
    expect(screen.getByText("Remove externally fenced node")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: /Enter Maintenance Mode/ }));

    expect(screen.getByText("Administrator access required. Destructive actions also require exact typed confirmation.")).toBeInTheDocument();
    expect(screen.queryByText(/Sign in again/)).not.toBeInTheDocument();
  });

  it("explains empty release catalog", () => {
    renderClusterManagement();
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText(/No published stable cluster-compatible release exists at or above baseline 2026.08.1/)).toBeInTheDocument();
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
