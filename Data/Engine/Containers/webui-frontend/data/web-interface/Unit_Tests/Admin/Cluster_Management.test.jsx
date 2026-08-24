import React from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import ClusterManagement from "@/Admin/Cluster_Management.jsx";

const state = vi.hoisted(() => ({
  hmrState: "active",
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    useLoaderData: () => ({
      cluster: {
        enabled: true,
        status: "Healthy",
        active_size: 3,
        desired_size: 3,
        baseline_release: "2026.08.1",
        k3s_version: "v1.36.3+k3s1",
        hmr: { state: state.hmrState, node_id: state.hmrState === "active" ? "11111111-1111-4111-8111-111111111111" : "" },
        leaders: {
          etcd_leader: "engine-1",
          control_vip_owner: "engine-1",
          edge_vip_owner: "engine-1",
          postgres_primary: "engine-1",
          scheduler_leader: "engine-1",
          wireguard_owner: "engine-1",
        },
        nodes: [
          { id: "11111111-1111-4111-8111-111111111111", node_name: "engine-1", membership_state: "Active", application_state: "active", roles: {}, probe_health: {} },
          { id: "22222222-2222-4222-8222-222222222222", node_name: "engine-2", membership_state: "Active", application_state: "active", roles: {}, probe_health: {} },
          { id: "33333333-3333-4333-8333-333333333333", node_name: "engine-3", membership_state: "Active", application_state: "active", roles: {}, probe_health: {} },
        ],
        operations: [],
        admissions: [],
      },
      releases: { releases: [] },
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
