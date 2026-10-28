import React from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import ClusterOperationTimeline, { buildClusterOperationTimeline } from "@/Admin/ClusterOperationTimeline.jsx";

const operation = { id: "op-1", state: "running", current_step: "preflight", updated_at: 100, attempt: 1 };
const event = (id, event_type, details = {}) => ({ id, operation_id: operation.id, event_type, details, created_at: 100 + id });
const build = (events, patch = {}) => buildClusterOperationTimeline({ ...operation, ...patch }, events, [{ id: "n1", node_name: "engine-1" }]);

describe("cluster operation timeline", () => {
  afterEach(cleanup);

  it("orders and deduplicates durable events while isolating operations and resolving node steps", () => {
    const passed = event(2, "operation_step_passed", { step: "node:n1:enter_drain", next_step: "node:n1:wait_endpoint_withdrawal" });
    const timeline = build([passed, event(1, "operation_started"), passed, { ...event(3, "operation_failed"), operation_id: "op-2" }]);
    expect(timeline.entries.map(({ id }) => id)).toEqual(["event-1", "event-2"]);
    expect(timeline.entries[1]).toMatchObject({ label: "engine-1 · Enter maintenance — passed", tone: "complete" });
    expect(timeline.current).toMatchObject({ label: "In progress · engine-1 · Wait for traffic withdrawal", active: true, timestamp: 102 });
  });

  it("retains failed attempts, marks retry boundary, and resumes only recorded step", () => {
    const timeline = build([
      event(1, "operation_failed", { step: "inspect_health", error: "Health check failed" }),
      event(2, "operation_retry", { attempt: 2, resume_step: "inspect_health" }),
      event(3, "operation_started"),
      event(4, "operation_step_passed", { step: "inspect_health", next_step: "minimum_ready_soak" }),
    ]);
    expect(timeline.entries[0]).toMatchObject({ tone: "failed", message: "Health check failed" });
    expect(timeline.entries[1].label).toBe("Retry requested · Attempt 2");
    expect(timeline.current).toMatchObject({ label: "In progress · Observe sustained workload health", tone: "active" });
    expect(JSON.stringify(timeline)).not.toMatch(/countdown|60s|deadline/);
  });

  it.each([
    ["operation_failed", "failed"], ["operation_succeeded", "complete"],
    ["operation_cancel", "recorded"], ["operation_cancelled", "recorded"],
    ["operation_retry", "recorded"],
  ])("stops old snapshot animation when newer %s arrives", (type, tone) => {
    expect(build([event(1, type)]).current).toMatchObject({ active: false, tone });
  });

  it("prefers newer snapshots and newer retry attempts over retained terminal history", () => {
    const failure = event(1, "operation_failed", { step: "preflight" });
    expect(build([failure], { updated_at: 102, state: "waiting" }).current).toMatchObject({ label: "Waiting · Preflight checks", active: false });
    expect(build([failure], { updated_at: 101, attempt: 2, state: "queued" }).current.label).toBe("Queued · Preflight checks");
    expect(build([failure], { superseded_by: "op-2" }).current).toMatchObject({ label: "Superseded", active: false });
    expect(build([], { current_step: "complete" }).current.active).toBe(false);
  });

  it("shows snapshot fallback without inventing completed steps", () => {
    const timeline = build([], { state: "failed", current_step: "future_step", error: "Failed safely" });
    render(<ClusterOperationTimeline timeline={timeline} formatTimestamp={String} />);
    expect(screen.getByText("No lifecycle events available.")).toBeInTheDocument();
    expect(screen.getByText("Current: Failed · Future Step")).toBeInTheDocument();
    expect(screen.getByText("Failed safely")).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
  });

  it("expands retained history, reports stale progress and keeps current step accessible", () => {
    const timeline = build(Array.from({ length: 6 }, (_, i) => event(i + 1, "operation_step_passed", { step: `step_${i}`, next_step: `step_${i + 1}` })));
    const view = render(<ClusterOperationTimeline timeline={timeline} stale formatTimestamp={String} />);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
    expect(screen.getByRole("status")).toHaveTextContent("Updates delayed");
    fireEvent.click(screen.getByRole("button", { name: "Show 3 earlier events" }));
    expect(screen.getAllByRole("listitem")).toHaveLength(7);
    expect(screen.getByRole("button", { name: "Show recent steps" })).toHaveAttribute("aria-expanded", "true");
    const current = screen.getAllByRole("listitem").at(-1);
    expect(current).toHaveAttribute("aria-current", "step");
    expect(within(current).queryByTestId("SyncRoundedIcon")).not.toBeInTheDocument();
    view.rerender(<ClusterOperationTimeline timeline={timeline} stale={false} formatTimestamp={String} />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(within(current).getByTestId("SyncRoundedIcon")).toBeInTheDocument();
  });
});
