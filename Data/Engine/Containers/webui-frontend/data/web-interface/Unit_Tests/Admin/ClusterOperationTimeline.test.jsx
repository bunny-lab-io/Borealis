import React from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import ClusterOperationTimeline, { buildClusterOperationTimeline, clusterSoakClock } from "@/Admin/ClusterOperationTimeline.jsx";

const operation = { id: "op-1", state: "running", current_step: "preflight", updated_at: 100, attempt: 1 };
const event = (id, event_type, details = {}) => ({ id, operation_id: operation.id, event_type, details, created_at: 100 + id });
const build = (events, patch = {}) => buildClusterOperationTimeline({ ...operation, ...patch }, events, [{ id: "n1", node_name: "engine-1" }]);

describe("server-owned soak timing", () => {
  const startMS = Date.parse("2026-10-28T12:00:00Z");
  const runID = "11111111-1111-4111-8111-111111111111";
  const op = { ...operation, current_step: "minimum_ready_soak" };
  const details = { operation_id: op.id, attempt: 1, step: op.current_step, run_id: runID, holder: "controller-1", phase: "ready",
    started_at_ms: startMS, deadline_at_ms: startMS + 30_000, observed_at_ms: startMS, valid_until_ms: startMS + 15_000 };
  const started = event(1, "operation_soak_started", details);
  const clock = { serverMS: startMS, receivedAt: 100, uncertaintyMS: 0 };
  const timed = (events = [started], patch = {}, timing = {}) => buildClusterOperationTimeline({ ...op, ...patch }, events, [], [], { fresh: true, clock, now: 100, ...timing });

  it("counts only persisted deadline against current server clock and never marks health complete", () => {
    expect(timed().current.soakLabel).toBe("Configured soak wait: 30s remaining");
    expect(timed([started], {}, { now: 1100 }).current.soakLabel).toBe("Configured soak wait: 29s remaining");
    const observed = event(2, "operation_soak_observed", { ...details, observed_at_ms: startMS + 29_000, valid_until_ms: startMS + 44_000 });
    const timeline = timed([started, observed], {}, { clock: { ...clock, serverMS: startMS + 31_000 } });
    expect(timeline.current).toMatchObject({ active: true, tone: "active", soakLabel: "Wait deadline reached · Awaiting controller verification" });
    expect(timeline.entries.every((entry) => entry.tone === "recorded")).toBe(true);
    expect(timeline.entries).toHaveLength(1); // Freshness records do not crowd milestones.
  });

  it.each([
    { fresh: false }, { clock: null }, { now: 15101 }, { now: 99 },
    { clock: { ...clock, serverMS: startMS - 1 } }, { clock: { ...clock, serverMS: startMS + 15_000 } },
  ])("stops countdown with stale/missing/regressed clock or expired ownership: %j", (timing) => {
    expect(timed([started], {}, timing).current.soakLabel).not.toMatch(/\ds remaining/);
  });

  it.each([
    { attempt: 2 }, { attempt: "1" }, { current_step: "exit_drain" }, { state: "failed" }, { state: "cancelled" }, { superseded_by: "other" },
  ])("rejects mismatched current snapshot: %j", (patch) => {
    expect(timed([started], patch).current.soakLabel).not.toMatch(/\ds remaining/);
  });

  it("rejects missing start, mismatched run, malformed timing and durable interruption", () => {
    const observed = event(2, "operation_soak_observed", details);
    for (const events of [[observed], [started, { ...observed, details: { ...details, run_id: "22222222-2222-4222-8222-222222222222" } }],
      [started, { ...observed, details: { ...details, holder: "other" } }], [started, { ...observed, details: { ...details, deadline_at_ms: startMS + 99_000 } }],
      [started, event(2, "operation_soak_interrupted", details)], [started, event(2, "operation_soak_elapsed", details)],
      [started, event(2, "operation_retry", { attempt: 2 })], [started, event(2, "operation_failed", { step: op.current_step })]]) {
      expect(timed(events).current.soakLabel).not.toMatch(/\ds remaining/);
    }
  });

  it("uses exact replacement run and retained history after refresh", () => {
    const replacement = { ...details, run_id: "22222222-2222-4222-8222-222222222222", started_at_ms: startMS + 5000,
      deadline_at_ms: startMS + 35_000, observed_at_ms: startMS + 5000, valid_until_ms: startMS + 20_000 };
    const events = [started, event(2, "operation_soak_interrupted", details), event(3, "operation_soak_started", replacement)];
    expect(timed(events, {}, { clock: { ...clock, serverMS: startMS + 5000 } }).current.soakLabel).toBe("Configured soak wait: 30s remaining");
    expect(timed([...events, event(4, "operation_soak_observed", details)], {}, { clock: { ...clock, serverMS: startMS + 5000 } }).current.soakLabel).not.toMatch(/\ds remaining/);
  });

  it("bounds network uncertainty and rejects clock rollback, slow/malformed server responses", () => {
    const payload = { utc: "2026-10-28T12:00:00.125000+00:00", epoch: startMS / 1000 };
    const received = clusterSoakClock(payload, 100, 200);
    expect(received).toEqual({ serverMS: startMS + 125, receivedAt: 200, uncertaintyMS: 100 });
    expect(clusterSoakClock(payload, 1200, 1300, received)).toBeNull();
    expect(clusterSoakClock(payload, 100, 99)).toBeNull();
    expect(clusterSoakClock(payload, 100, 15101)).toBeNull();
    expect(clusterSoakClock({ ...payload, epoch: 1 }, 100, 200)).toBeNull();
    expect(clusterSoakClock({ epoch: payload.epoch }, 100, 200)).toBeNull();
  });

  it("accepts newest fresh response when clock requests overlap, while rejecting receive-clock rollback", () => {
    const payload = (offset) => ({ utc: new Date(startMS + offset).toISOString(), epoch: (startMS + offset) / 1000 });
    const first = clusterSoakClock(payload(5000), 0, 6000);
    expect(clusterSoakClock(payload(10000), 5000, 11000, first)).toEqual({ serverMS: startMS + 10000, receivedAt: 11000, uncertaintyMS: 6000 });
    expect(clusterSoakClock(payload(10000), 5000, 5999, first)).toBeNull();
  });
});

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
    expect(timeline.current).toMatchObject({ label: "In progress · Workload soak wait", tone: "active" });
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

  it("keeps automatic recovery observations separate from maintenance and return authority", () => {
    const events = [event(1, "automatic_recovery_observation_started"), event(2, "automatic_recovery_observation_changed", { phase: "health_observed", runtime_dispatched: false })];
    const timeline = build(events, { kind: "node_maintenance", state: "waiting", current_step: "observe_automatic_recovery" });
    expect(timeline.entries[1]).toMatchObject({ label: "Automatic recovery health observation complete · Return remains gated", tone: "recorded" });
    expect(timeline.current).toMatchObject({ active: false, tone: "recorded", soakLabel: "" });
  });

  it("shows SSH waiting gates when parent events arrive before their snapshot", () => {
    const events = [
      event(1, "ssh_inspection_queued"),
      event(2, "ssh_inspection_started", { attempt: 1, step: "inspect_ssh_targets" }),
      event(3, "ssh_target_inspected", { target_id: "target-1", generation: 8 }),
      event(4, "ssh_inspection_complete", { attempt: 1, step: "qualify_ssh_targets" }),
      event(5, "ssh_qualification_observed", { attempt: 1 }),
      event(6, "ssh_preparation_requested", { attempt: 1 }),
    ];
    const timeline = build([...events].reverse(), { kind: "ssh_onboarding", current_step: "inspect_ssh_targets" });
    expect(timeline.entries.map(({ label }) => label)).toEqual([
      "SSH inspection queued", "SSH target inspection started", "SSH target inspected · Preparation not authorized",
      "SSH inspections complete · Qualification required", "Qualification results recorded", "Preparation confirmation recorded · Approval required",
    ]);
    expect(timeline.current).toMatchObject({ label: "Waiting · Qualify SSH targets", active: false, tone: "recorded", timestamp: 106 });
    expect(build(events.slice(0, 3), { kind: "ssh_onboarding" }).current).toMatchObject({ label: "In progress · Inspect SSH targets", timestamp: 102 });
  });

  it("keeps preparation completion separate from admission even with misleading receipt fields", () => {
    const prepared = event(2, "ssh_preparation_complete", { attempt: 1, target_count: 2, step: "complete", ready: true });
    const timeline = build([prepared, event(1, "ssh_preparation_approved", { attempt: 1 })], { kind: "ssh_onboarding" });
    expect(timeline.entries[0].label).toBe("Preparation approved");
    expect(timeline.entries[1]).toMatchObject({ label: "Preparation complete · Admission not authorized", tone: "complete" });
    expect(timeline.current).toMatchObject({ label: "Prepared · Admission not authorized", active: false, tone: "recorded" });
    expect(build([prepared], { kind: "ssh_onboarding", state: "failed", updated_at: 102, error: "Stopped safely" }).current)
      .toMatchObject({ label: "Failed · Preflight checks", tone: "failed", message: "Stopped safely" });
    expect(build([prepared], { kind: "ssh_onboarding", updated_at: 103, current_step: "recover_ssh_identity" }).current)
      .toMatchObject({ label: "In progress · Recover target identity", active: true });
  });

  it("shows SSH inspection failures and rejects older attempt transitions", () => {
    const failure = event(3, "ssh_inspection_failed", { attempt: 1, step: "inspect_ssh_targets" });
    expect(build([failure], { kind: "ssh_onboarding" }).current)
      .toMatchObject({ label: "Failed · Inspect SSH targets", active: false, tone: "failed" });
    const retry = event(2, "operation_retry", { attempt: 2, resume_step: "inspect_ssh_targets" });
    const events = [failure, retry, event(1, "ssh_inspection_started", { attempt: 1 })];
    const timeline = build(events, { kind: "ssh_onboarding" });
    expect(timeline.entries.at(-1).tone).toBe("failed");
    expect(timeline.current).toMatchObject({ label: "Queued · Inspect SSH targets", active: false, timestamp: 102 });
    expect(build([failure], { kind: "ssh_onboarding", attempt: 2, state: "waiting", current_step: "qualify_ssh_targets" }).current)
      .toMatchObject({ label: "Waiting · Qualify SSH targets", timestamp: 100 });
    expect(build([event(4, "ssh_preparation_approved", { attempt: 2 })], { kind: "ssh_onboarding", attempt: 2 }).current)
      .toMatchObject({ label: "In progress · Prepare SSH targets", timestamp: 104 });
  });

  it.each([0, -1, 1.5, null, true, "2", "invalid"])("retains malformed attempt %s as history only", (attempt) => {
    const timeline = build([event(1, "ssh_preparation_complete", { attempt })], { kind: "ssh_onboarding" });
    expect(timeline.entries).toHaveLength(1);
    expect(timeline.current).toMatchObject({ label: "In progress · Preflight checks", timestamp: 100 });
  });

  it.each([
    ["ssh_onboarding", "ssh_identity_recovery_reserved", "ssh_identity_recovery_required", "ssh_identity_recovered", "Target"],
    ["ssh_identity_recovery", "existing_identity_recovery_reserved", "existing_identity_recovery_required", "existing_identity_recovered", "Member"],
  ])("keeps %s uncertain outcomes visible without claiming parent completion", (kind, reserved, uncertain, recovered, subject) => {
    const timeline = build([event(1, reserved), event(2, uncertain)], { kind });
    expect(timeline.entries[1]).toMatchObject({ label: `${subject} identity recovery outcome uncertain`, tone: "failed" });
    expect(timeline.current).toMatchObject({ label: `Recovery required · ${subject} outcome uncertain`, active: false, tone: "failed" });
    const settled = build([event(1, reserved), event(2, uncertain), event(3, recovered)], { kind });
    expect(settled.entries.at(-1).tone).toBe("complete");
    expect(settled.current).toMatchObject({ active: false, tone: "recorded" });
    expect(settled.current.label).toBe(kind === "ssh_onboarding"
      ? "Target identity recovered · Restart not authorized" : "Member identity recovered · Maintenance remains in effect");
    render(<ClusterOperationTimeline timeline={timeline} formatTimestamp={String} />);
    const current = screen.getAllByRole("listitem").at(-1);
    expect(within(current).queryByTestId("SyncRoundedIcon")).not.toBeInTheDocument();
    expect(within(current).getByTestId("ErrorOutlineRoundedIcon")).toBeInTheDocument();
  });

  it("accepts only recorded existing-member completion and preserves maintenance", () => {
    const events = ["existing_identity_reserved", "existing_identity_confirmed", "existing_identity_observed"]
      .map((type, i) => event(i + 1, type));
    expect(build(events, { kind: "ssh_identity_recovery" }).current)
      .toMatchObject({ label: "Waiting · Verify member before identity recovery", active: false });
    const completed = { ...event(4, "completed"), state: "succeeded" };
    expect(build([...events, completed], { kind: "ssh_identity_recovery" }).current)
      .toMatchObject({ label: "Completed", active: false, tone: "complete" });
    expect(build([...events, completed], { kind: "ssh_identity_recovery" }).entries.at(-1).label)
      .toBe("Identity recovery completed · Maintenance remains in effect");
    expect(build([completed], { kind: "ssh_onboarding" }).current.timestamp).toBe(100);
    expect(build([{ ...completed, state: "running" }], { kind: "ssh_identity_recovery" }).current.timestamp).toBe(100);
  });

  it("keeps unknown events and native progress cursors out of current-state decisions", () => {
    const timeline = build([
      event(1, "ssh_preparation_approved", { attempt: 1 }),
      event(2, "future_ssh_ready", { attempt: 9, next_step: "complete", ready: true }),
      event(3, "constructor", { step: "complete" }),
    ], { kind: "ssh_onboarding", payload: { ssh_preparation: { cursor: "complete", ready: true } } });
    expect(timeline.entries.at(-2)).toMatchObject({ label: "Future Ssh Ready", tone: "recorded" });
    expect(timeline.entries.at(-1)).toMatchObject({ label: "Constructor", tone: "recorded" });
    expect(timeline.current).toMatchObject({ label: "In progress · Prepare SSH targets", timestamp: 101 });
    expect(build([], { kind: "ssh_onboarding", current_step: "constructor" }).current.label).toBe("In progress · Constructor");
    expect(JSON.stringify(timeline)).not.toMatch(/countdown|deadline|admitted/i);
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
