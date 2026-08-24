import React from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import ClusterManagement from "@/Admin/Cluster_Management.jsx";

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
        hmr: { state: "active", node_id: "11111111-1111-4111-8111-111111111111" },
        leaders: {
          etcd_leader: "engine-1",
          control_vip_owner: "engine-1",
          edge_vip_owner: "engine-1",
          postgres_primary: "engine-1",
          scheduler_leader: "engine-1",
          wireguard_owner: "engine-1",
        },
        nodes: [],
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
  afterEach(() => cleanup());

  it("shows role ownership, six management views, and cluster-wide HMR warning", () => {
    render(<ClusterManagement />);

    for (const tab of ["Overview", "Nodes", "Database", "Updates", "Operations", "Maintenance"]) {
      expect(screen.getByRole("tab", { name: tab })).toBeInTheDocument();
    }
    expect(screen.getByText(/Cluster-wide non-HA HMR active/)).toBeInTheDocument();
    expect(screen.getByText("etcd leader")).toBeInTheDocument();
    expect(screen.getByText("WireGuard owner")).toBeInTheDocument();
  });
});
