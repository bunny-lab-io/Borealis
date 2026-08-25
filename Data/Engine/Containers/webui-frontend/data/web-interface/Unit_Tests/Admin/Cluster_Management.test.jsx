import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import ClusterManagement from "@/Admin/Cluster_Management.jsx";

const state = vi.hoisted(() => ({
  hmrState: "active",
  activeSize: 3,
  desiredSize: 3,
  clusterStatus: "Healthy",
  releases: [],
  releaseError: "",
  operations: [],
  database: {},
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
          { id: "11111111-1111-4111-8111-111111111111", node_name: "engine-1", membership_state: "Active", application_state: state.drainedNodeID === "11111111-1111-4111-8111-111111111111" ? "drained" : "active", roles: { control_vip_owner: true, k3s_version: "v1.36.3+k3s1" }, probe_health: {} },
          { id: "22222222-2222-4222-8222-222222222222", node_name: "engine-2", membership_state: "Active", application_state: state.drainedNodeID === "22222222-2222-4222-8222-222222222222" ? "drained" : "active", roles: {}, probe_health: {} },
          { id: "33333333-3333-4333-8333-333333333333", node_name: "engine-3", membership_state: "Active", application_state: state.drainedNodeID === "33333333-3333-4333-8333-333333333333" ? "drained" : "active", roles: {}, probe_health: {} },
        ],
        operations: state.operations,
        admissions: [{ id: "44444444-4444-4444-8444-444444444444", node_name: "engine-4", state: "Pending Quorum" }],
      },
      releases: { releases: state.releases },
      releaseError: state.releaseError,
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
    state.database = {};
    state.drainedNodeID = "";
    vi.unstubAllGlobals();
  });

  it("shows role ownership, six management views, and cluster-wide HMR warning", () => {
    render(<ClusterManagement />);

    for (const tab of ["Overview", "Nodes", "Database", "Updates", "Operations", "Maintenance"]) {
      expect(screen.getByRole("tab", { name: tab })).toBeInTheDocument();
    }
    expect(screen.getByText(/Cluster-wide non-HA HMR active/)).toBeInTheDocument();
    expect(screen.getByText("etcd leader")).toBeInTheDocument();
    expect(screen.getByText("WireGuard owner")).toBeInTheDocument();
    expect(screen.getAllByText("engine-1").length).toBeGreaterThan(1);
  });

  it("labels node states and keeps K3s metadata out of role list", () => {
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    expect(screen.getAllByText("Membership: Active")).toHaveLength(3);
    expect(screen.getAllByText("Application: active")).toHaveLength(3);
    expect(screen.getByText(/K3s v1.36.3\+k3s1/)).toBeInTheDocument();
    expect(screen.getByText("Roles: control vip owner")).toBeInTheDocument();
    expect(screen.queryByText(/Roles:.*k3s version/)).not.toBeInTheDocument();
  });

  it("explains empty release catalog", () => {
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Updates" }));

    expect(screen.getByText(/No published stable cluster-compatible release exists at or above baseline 2026.08.1/)).toBeInTheDocument();
  });

  it("shows reduced PostgreSQL readiness separately from configured instances", () => {
    state.clusterStatus = "Degraded Database";
    state.database = {
      configured_instances: 3,
      ready_instances: 2,
      fully_ready: false,
      durability_quorum: true,
      synchronous_acknowledgements: 1,
      phase: "Waiting for the instances to become active",
    };
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Database" }));

    expect(screen.getAllByText(/2 of 3 PostgreSQL instances are Ready/).length).toBeGreaterThan(0);
    expect(screen.getByText("Configured / ready")).toBeInTheDocument();
    expect(screen.getByText("3 / 2")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    expect(screen.getAllByRole("button", { name: "Enter Maintenance" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Update Node" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Remove Pair" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Emergency Remove" })[0]).toBeEnabled();
  });

  it("keeps normal controls fenced when mixed-version status masks database recovery", () => {
    state.hmrState = "inactive";
    state.clusterStatus = "Mixed Version";
    state.database = { configured_instances: 3, ready_instances: 2, fully_ready: false, durability_quorum: true };
    render(<ClusterManagement />);

    expect(screen.getByText(/PostgreSQL recovery remains required even while cluster lifecycle status is Mixed Version/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Updates" }));
    expect(screen.getByRole("button", { name: "Update All One at a Time" })).toBeDisabled();
  });

  it("allows drained-node recovery while fencing additional application drains", () => {
    state.hmrState = "inactive";
    state.drainedNodeID = "11111111-1111-4111-8111-111111111111";
    render(<ClusterManagement />);

    expect(screen.getByText(/active cluster members remain application-drained/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    expect(screen.getByRole("button", { name: "Exit Maintenance" })).toBeEnabled();
    expect(screen.getAllByRole("button", { name: "Enter Maintenance" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Enter Maintenance" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Update Node" })[0]).toBeDisabled();
  });

  it("marks superseded failures as audit history and removes retry action", () => {
    state.operations = [{
      id: "11111111-1111-4111-8111-111111111199",
      kind: "membership_admit",
      current_step: "apply_membership",
      state: "failed",
      attempt: 2,
      error: "apply_membership: Kubernetes API returned HTTP 503: noisy detail",
      superseded_by: "22222222-2222-4222-8222-222222222299",
    }];
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Operations" }));

    expect(screen.getByText("superseded")).toBeInTheDocument();
    expect(screen.getByText(/Historical failure retained for audit/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
    expect(screen.getByText("Technical details")).toBeInTheDocument();
  });

  it("requires paired safe removal and explicit external fencing for emergency removal", async () => {
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    fireEvent.click(screen.getAllByRole("button", { name: "Remove Pair" })[0]);
    expect(screen.getByText(/Safe downscale removes two nodes sequentially/)).toBeInTheDocument();
    expect(screen.getByLabelText("Paired removal node")).toBeInTheDocument();
    expect(screen.getByText("Type REMOVE NODE PAIR")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: "Emergency Remove" })[0]);
    expect(screen.getByText(/Target must be powered off and unable to rejoin/)).toBeInTheDocument();
    expect(screen.getByLabelText("External fencing confirmation")).toBeInTheDocument();
    expect(screen.getByText("Type EMERGENCY REMOVE NODE")).toBeInTheDocument();
  });

  it("fences expansion after supported three-node membership", () => {
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByText(/Three-node release limit reached/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Node Invitation" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Request Pair Expansion" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve Pair" })).toBeDisabled();

    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));
    expect(screen.getAllByRole("button", { name: "Remove Pair" })[0]).toBeEnabled();
  });

  it("fences removal from unsupported five-plus membership", () => {
    state.activeSize = 5;
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Nodes" }));

    expect(screen.getAllByRole("button", { name: "Remove Pair" })[0]).toBeDisabled();
    expect(screen.getAllByRole("button", { name: "Emergency Remove" })[0]).toBeDisabled();
  });

  it("keeps one-to-three membership controls available", () => {
    state.activeSize = 1;
    state.desiredSize = 1;
    render(<ClusterManagement />);
    fireEvent.click(screen.getByRole("tab", { name: "Maintenance" }));

    expect(screen.getByRole("button", { name: "Create Node Invitation" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Request Pair Expansion" })).toBeEnabled();
  });

  it("offers exactly one replacement path for degraded two-of-three membership", () => {
    state.hmrState = "inactive";
    state.activeSize = 2;
    state.desiredSize = 3;
    state.clusterStatus = "Degraded Quorum";
    render(<ClusterManagement />);
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
    render(<ClusterManagement />);
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
