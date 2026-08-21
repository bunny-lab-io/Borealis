import React from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import AgentUpdates, {
  agentUpdateIsActive,
  buildAgentUpdateTimeline,
  formatAgentUpdateDuration,
} from "@/Devices/Tabs/Agent_Updates.jsx";

vi.mock("ag-grid-react", () => ({
  AgGridReact: ({ columnDefs = [] }) => (
    <div
      data-testid="agent-update-grid-columns"
      data-job-resizable={String(columnDefs.find(({ field }) => field === "scheduled_job_id")?.resizable)}
      data-requested-by-min-width={columnDefs.find(({ field }) => field === "requested_by")?.minWidth}
    >
      {columnDefs.map((column) => column.headerName).join("|")}
    </div>
  ),
}));

describe("Agent Updates dashboard helpers", () => {
  it("maps durable progress events onto Remote Desktop-style timeline", () => {
    const timeline = buildAgentUpdateTimeline({
      events: [
        { phase_id: "requesting_agent_update", state: "success", summary: "Agent Received Request" },
        { phase_id: "quiescing_managed_components", state: "running", summary: "Quiescing Managed Components" },
        { phase_id: "role:system:engine_socket", state: "success", summary: "Engine Socket" },
        { phase_id: "role:system:remote_shell", state: "recovering", summary: "Remote Shell" },
        { phase_id: "role:system:vnc", state: "success", summary: "UltraVNC Service" },
      ],
    });

    expect(timeline.find((step) => step.id === "requesting_agent_update")?.visualState).toBe("complete");
    expect(timeline.find((step) => step.id === "requesting_agent_update")?.label).toBe("Agent Recieved Update Command");
    expect(timeline.find((step) => step.id === "quiescing_managed_components")?.visualState).toBe("active");
    expect(timeline.find((step) => step.id === "role:system:engine_socket")?.depth).toBe(0);
    expect(timeline.find((step) => step.id === "role:system:remote_shell")?.depth).toBe(2);
    expect(timeline.find((step) => step.id === "role:system:remote_shell")?.label).toBe("Role: Remote Shell");
    expect(timeline.find((step) => step.id === "role:system:vnc")?.label).toBe("Service: UltraVNC");
    expect(timeline.findIndex((step) => step.id === "role:system:engine_socket"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "waiting_agent_reconnection"));
    expect(timeline.findIndex((step) => step.id === "verifying_post_update_health"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "post_update_roles"));
    expect(timeline.findIndex((step) => step.id === "post_update_roles"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "role:system:remote_shell"));
    expect(timeline.findIndex((step) => step.id === "role:system:remote_shell"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "post_update_services"));
    expect(timeline.findIndex((step) => step.id === "post_update_services"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "role:system:vnc"));
    expect(timeline.findIndex((step) => step.id === "role:system:vnc"))
      .toBeLessThan(timeline.findIndex((step) => step.id === "update_completed"));
  });

  it("classifies active operations and live duration", () => {
    expect(agentUpdateIsActive({ status: "awaiting_health" })).toBe(true);
    expect(agentUpdateIsActive({ status: "success" })).toBe(false);
    expect(formatAgentUpdateDuration(100, 5505)).toBe("1h 30m 5s");
  });

  it("renders full-height timeline and simplified history columns", () => {
    const operation = {
      operation_id: "op-42",
      status: "success",
      source: "operator",
      requested_by: "admin",
      started_at: 100,
      ended_at: 140,
      scheduled_job_id: 396,
      events: [
        { phase_id: "requesting_agent_update", state: "success", summary: "Agent Received Request" },
        { phase_id: "update_completed", state: "success", summary: "Update Completed" },
      ],
    };

    render(
      <AgentUpdates
        history={{ active_operation: null, operations: [operation], loading: false, error: "" }}
        selectedOperationId="op-42"
      />
    );

    expect(screen.getByTestId("agent-update-timeline-island")).toBeTruthy();
    expect(screen.getByTestId("agent-update-history-island")).toBeTruthy();
    const historyGrid = screen.getByTestId("agent-update-grid-columns");
    expect(historyGrid.textContent).toBe(
      "Status|Source|Requested By|Started|Duration|Job"
    );
    expect(historyGrid.getAttribute("data-job-resizable")).toBe("false");
    expect(historyGrid.getAttribute("data-requested-by-min-width")).toBe("150");
    expect(screen.getByText("Update Timeline")).toBeTruthy();
    expect(screen.queryByText(/Operation op-42/)).toBeNull();
    expect(screen.queryByText("Update topology and progress")).toBeNull();
  });
});
