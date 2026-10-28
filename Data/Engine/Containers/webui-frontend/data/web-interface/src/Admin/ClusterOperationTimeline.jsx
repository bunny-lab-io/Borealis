import React, { useState } from "react";
import { Box, Button, Typography } from "@mui/material";
import { CheckCircleOutlineRounded, ErrorOutlineRounded, PendingOutlined, SyncRounded } from "@mui/icons-material";

const humanize = (value) => String(value || "").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
const SOAK_FRESH_MS = 15_000; // Existing cluster snapshot/ownership freshness window.
const SOAK_EVENTS = new Set(["operation_soak_started", "operation_soak_observed", "operation_soak_elapsed", "operation_soak_interrupted"]);

// Existing authenticated /api/server/time supplies UTC. Request duration bounds
// uncertainty; monotonic elapsed time avoids dependence on the browser wall clock.
export function clusterSoakClock(payload, sentAt, receivedAt, previous = null) {
  const serverMS = typeof payload?.utc === "string" ? Date.parse(payload.utc) : NaN;
  const uncertaintyMS = receivedAt - sentAt;
  if (!Number.isFinite(serverMS) || !Number.isSafeInteger(payload?.epoch) || Math.floor(serverMS / 1000) !== payload.epoch
    || !Number.isFinite(sentAt) || !Number.isFinite(receivedAt) || uncertaintyMS < 0 || uncertaintyMS > SOAK_FRESH_MS) return null;
  if (previous && (receivedAt < previous.receivedAt || serverMS + uncertaintyMS < previous.serverMS + sentAt - previous.receivedAt)) return null;
  return { serverMS, receivedAt, uncertaintyMS };
}

function clusterSoakTiming(operation, ordered, timing) {
  if (!timing?.fresh || !timing.clock || operation.state !== "running" || operation.superseded_by) return null;
  const { clock, now } = timing;
  const elapsed = now - clock.receivedAt;
  if (!Number.isFinite(elapsed) || elapsed < 0 || elapsed > SOAK_FRESH_MS) return null;
  const latest = ordered.filter((event) => String(event.event_type).startsWith("operation_soak_")).at(-1);
  if (!latest || !["operation_soak_started", "operation_soak_observed"].includes(latest.event_type)) return null;
  const detail = latest.details || {};
  if (detail.operation_id !== operation.id || detail.attempt !== operation.attempt || detail.step !== operation.current_step
    || !Number.isSafeInteger(detail.attempt) || detail.attempt < 1 || typeof detail.holder !== "string" || !detail.holder
    || typeof detail.phase !== "string" || !detail.phase
    || !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(detail.run_id || "")) return null;
  const start = ordered.filter((event) => event.event_type === "operation_soak_started").at(-1);
  const identity = ["operation_id", "attempt", "step", "run_id", "holder", "phase", "started_at_ms", "deadline_at_ms"];
  if (!start || Number(start.id) > Number(latest.id) || identity.some((key) => start.details[key] !== detail[key])) return null;
  if (ordered.some((event) => Number(event.id) > Number(start.id) && Number(event.id) <= Number(latest.id)
    && ["operation_soak_interrupted", "operation_soak_elapsed"].includes(event.event_type))) return null;
  const { started_at_ms: started, deadline_at_ms: deadline, observed_at_ms: observed, valid_until_ms: validUntil } = detail;
  if (![started, deadline, observed, validUntil].every(Number.isSafeInteger) || started <= 0 || deadline < started || observed < started || validUntil <= observed) return null;
  const serverNow = clock.serverMS + clock.uncertaintyMS + elapsed;
  if (serverNow < observed || serverNow >= validUntil) return null;
  return { remaining: Math.max(0, Math.ceil((deadline - serverNow) / 1000)), runID: detail.run_id };
}
const STEP_LABELS = {
  preflight: "Preflight checks",
  enter_drain: "Enter maintenance",
  exit_drain: "Exit maintenance",
  transfer_roles: "Transfer cluster roles",
  restore_roles: "Restore cluster roles",
  reconcile_vip_placement: "Place cluster virtual IP",
  wait_endpoint_withdrawal: "Wait for traffic withdrawal",
  prepare_restore: "Prepare workload restoration",
  inspect_health: "Verify workload health",
  inspect_role_health: "Verify cluster role health",
  minimum_ready_soak: "Workload soak wait",
  minimum_role_soak: "Cluster role soak wait",
  minimum_candidate_soak: "Candidate soak wait",
  hmr_candidate_soak: "Candidate soak wait",
  verify_cluster: "Verify cluster health",
  inspect_ssh_targets: "Inspect SSH targets",
  qualify_ssh_targets: "Qualify SSH targets",
  prepare_ssh_targets: "Prepare SSH targets",
  recover_ssh_identity: "Recover target identity",
  identity_recovery_reserved: "Confirm member identity recovery",
  identity_recovery_confirmed: "Verify member before identity recovery",
  inspect_existing_identity: "Inspect member identity",
  recover_existing_identity: "Recover member identity",
};

// These are persisted parent transitions or explicitly recorded target outcomes.
// Target preparation/recovery can finish while the parent stays running: pause
// its indicator without turning that evidence into operation success/admission.
const phase = (label, state, step, options = {}) => ({ label, next: { state, step, ...options } });
const SSH_EVENTS = {
  ssh_inspection_queued: phase("SSH inspection queued", "queued", "preflight"),
  ssh_inspection_started: phase("SSH target inspection started", "running", "inspect_ssh_targets"),
  ssh_target_inspected: { label: "SSH target inspected · Preparation not authorized", tone: "complete" },
  ssh_inspection_complete: { ...phase("SSH inspections complete · Qualification required", "waiting", "qualify_ssh_targets"), tone: "complete" },
  ssh_inspection_failed: { ...phase("SSH inspection failed", "failed", "inspect_ssh_targets"), tone: "failed" },
  ssh_qualification_observed: phase("Qualification results recorded", "waiting", "qualify_ssh_targets"),
  ssh_preparation_requested: phase("Preparation confirmation recorded · Approval required", "waiting", "qualify_ssh_targets"),
  ssh_preparation_approved: phase("Preparation approved", "running", "prepare_ssh_targets"),
  ssh_preparation_complete: {
    ...phase("Preparation complete · Admission not authorized", "running", "prepare_ssh_targets",
      { label: "Prepared · Admission not authorized", active: false }), tone: "complete",
  },
  ssh_identity_recovery_reserved: phase("Target identity recovery reserved", "running", "recover_ssh_identity"),
  ssh_identity_recovery_required: {
    ...phase("Target identity recovery outcome uncertain", "running", "recover_ssh_identity",
      { label: "Recovery required · Target outcome uncertain", active: false, tone: "failed" }), tone: "failed",
  },
  ssh_identity_recovered: {
    ...phase("Target identity recovered · Restart not authorized", "running", "recover_ssh_identity",
      { label: "Target identity recovered · Restart not authorized", active: false }), tone: "complete",
  },
};
const IDENTITY_EVENTS = {
  existing_identity_reserved: phase("Member identity recovery reserved", "waiting", "identity_recovery_reserved"),
  existing_identity_confirmed: phase("Member identity recovery confirmed · Inspection required", "waiting", "identity_recovery_confirmed"),
  existing_identity_observed: { ...phase("Member inspection complete · Recovery remains gated", "waiting", "identity_recovery_confirmed"), tone: "complete" },
  existing_identity_recovery_reserved: phase("Member identity recovery reserved", "running", "recover_existing_identity"),
  existing_identity_recovery_required: {
    ...phase("Member identity recovery outcome uncertain", "running", "recover_existing_identity",
      { label: "Recovery required · Member outcome uncertain", active: false, tone: "failed" }), tone: "failed",
  },
  existing_identity_recovered: {
    ...phase("Member identity recovered · Controller completion pending", "running", "recover_existing_identity",
      { label: "Member identity recovered · Maintenance remains in effect", active: false }), tone: "complete",
  },
  completed: {
    ...phase("Identity recovery completed · Maintenance remains in effect", "succeeded", "complete"), tone: "complete",
  },
};

function stepLabel(value, identities) {
  const raw = String(value || "");
  const scoped = /^(?:node|admit):([^:]+):(.+)$/.exec(raw);
  const name = scoped ? scoped[2] : raw;
  const label = Object.hasOwn(STEP_LABELS, name) ? STEP_LABELS[name] : humanize(name);
  return scoped ? `${identities.get(scoped[1]) || scoped[1]} · ${label}` : label;
}

// Only known durable events change current progress. Unknown events remain history.
// Do not infer unrecorded future steps, admission, or soak deadlines.
export function buildClusterOperationTimeline(operation, events = [], nodes = [], admissions = [], timing = null) {
  const identities = new Map([...nodes, ...admissions].map((node) => [String(node.id), node.node_name]));
  const ordered = [...new Map(events.filter((event) => String(event.operation_id) === String(operation.id))
    .map((event) => [String(event.id), event])).values()].sort((a, b) => Number(a.id) - Number(b.id));
  let transition = null;
  let attempt = 1;
  const entries = ordered.map((event) => {
    const detail = event.details || {};
    const suppliedAttempt = detail.attempt;
    const hasAttempt = detail.attempt !== undefined;
    let eventAttempt = hasAttempt ? (Number.isSafeInteger(suppliedAttempt) && suppliedAttempt > 0 ? suppliedAttempt : 0) : attempt;
    const step = stepLabel(detail.step, identities);
    let label = humanize(event.event_type || "Cluster event");
    let tone = "recorded";
    let next = null;
    switch (event.event_type) {
      case "automatic_recovery_observation_started":
        label = "Automatic recovery observation started · Maintenance dispatch remains gated";
        break;
      case "automatic_recovery_observation_changed":
        label = detail.phase === "health_observed" ? "Automatic recovery health observation complete · Return remains gated"
          : detail.phase === "blocked" ? "Automatic recovery observation blocked · Maintenance unchanged"
            : "Automatic recovery observation updated · Maintenance unchanged";
        break;
      case "operation_soak_started":
        label = "Configured soak wait started";
        break;
      case "operation_soak_observed":
        label = "Configured soak wait observed";
        break;
      case "operation_soak_elapsed":
        label = "Configured soak wait elapsed · Verification pending";
        break;
      case "operation_soak_interrupted":
        label = "Configured soak wait interrupted · Elapsed time discarded";
        break;
      case "operation_started":
        label = "Operation started";
        next = { state: "running", step: transition?.step || operation.current_step };
        break;
      case "operation_step_passed":
        label = step ? `${step} — passed` : "Operation step passed";
        tone = "complete";
        next = { state: "running", step: detail.next_step };
        break;
      case "operation_failed":
        label = step ? `${step} — failed` : "Operation failed";
        tone = "failed";
        next = { state: "failed", step: detail.step, error: detail.error };
        break;
      case "operation_retry":
        if (!hasAttempt) eventAttempt = attempt + 1;
        label = eventAttempt ? `Retry requested · Attempt ${eventAttempt}` : "Retry requested";
        next = { state: "queued", step: detail.resume_step || "preflight" };
        break;
      case "operation_cancel":
      case "operation_cancelled":
        label = "Operation cancelled";
        next = { state: "cancelled" };
        break;
      case "operation_succeeded":
        label = "Operation completed";
        tone = "complete";
        next = { state: "succeeded", step: "complete" };
        break;
      default:
        {
          const contract = operation.kind === "ssh_onboarding" ? SSH_EVENTS
            : operation.kind === "ssh_identity_recovery" ? IDENTITY_EVENTS : {};
          const known = Object.hasOwn(contract, event.event_type) ? contract[event.event_type] : null;
          // "completed" is shared vocabulary; only this exact producer state
          // proves completion of an existing-member identity recovery operation.
          if (known && (event.event_type !== "completed" || event.state === "succeeded")) {
            label = known.label;
            tone = known.tone || "recorded";
            if (known.next) next = { ...known.next, message: event.message };
            if (next && event.event_type === "ssh_inspection_failed") next.step = detail.step || next.step;
          }
        }
        break;
    }
    // An old attempt can arrive after newer snapshot/event requests. Keep its
    // diagnostic history, but never move the current attempt backwards.
    if (next && eventAttempt >= attempt) {
      attempt = eventAttempt;
      transition = { ...next, timestamp: Number(event.created_at), attempt };
    }
    return {
      id: `event-${event.id}`, label, tone, timestamp: event.created_at,
      message: [event.message, detail.error].filter(Boolean).join("\n"),
    };
  });
  const snapshotTime = Number(operation.updated_at || operation.finished_at || operation.started_at || operation.created_at || 0);
  // Snapshot and event requests finish independently. A newer event can precede its snapshot.
  const snapshotTerminal = ["failed", "succeeded", "cancelled"].includes(operation.state);
  const eventNewer = transition && (transition.timestamp > snapshotTime
    || (transition.timestamp === snapshotTime && (!snapshotTerminal || transition.attempt > Number(operation.attempt || 1))));
  const current = eventNewer && transition.attempt >= Number(operation.attempt || 1)
    ? transition
    : { state: operation.state, step: operation.current_step, timestamp: snapshotTime, error: operation.error };
  const state = operation.superseded_by ? "superseded" : String(current.state || "unknown").toLowerCase();
  const active = state === "running" && current.step !== "complete" && current.active !== false;
  const status = { running: "In progress", queued: "Queued", waiting: "Waiting", succeeded: "Completed", failed: "Failed", cancelled: "Cancelled", superseded: "Superseded" }[state] || humanize(state);
  const step = current.step && current.step !== "complete" ? stepLabel(current.step, identities) : "";
  // Current snapshot and event transition must agree before timing can animate.
  const soak = active && current.step === operation.current_step && (!current.attempt || current.attempt === operation.attempt)
    ? clusterSoakTiming(operation, ordered, timing) : null;
  const hasSoak = ordered.some((event) => SOAK_EVENTS.has(event.event_type) && event.details?.step === operation.current_step
    && event.details?.attempt === operation.attempt);
  return {
    // Ownership refreshes drive timing but are not separate operator milestones.
    // Full durable observations remain available in copied diagnostics.
    entries: entries.filter((_, index) => ordered[index].event_type !== "operation_soak_observed"),
    current: {
      id: "current", label: state !== "superseded" && current.label ? current.label
        : `${status}${step && ["running", "queued", "waiting", "failed"].includes(state) ? ` · ${step}` : ""}`,
      tone: state !== "superseded" && current.tone ? current.tone
        : active ? "active" : state === "failed" ? "failed" : state === "succeeded" ? "complete" : "recorded",
      timestamp: current.timestamp, message: current.error || current.message || "", active,
      soakLabel: soak ? (soak.remaining > 0 ? `Configured soak wait: ${soak.remaining}s remaining` : "Wait deadline reached · Awaiting controller verification")
        : active && hasSoak ? "Soak timing unavailable · Awaiting current controller evidence" : "",
    },
  };
}

const COLORS = { recorded: "#94a3b8", active: "#7dd3fc", complete: "#00d18c", failed: "#ff8a8a" };

export default function ClusterOperationTimeline({ timeline, stale, formatTimestamp }) {
  const [expanded, setExpanded] = useState(false);
  const history = timeline.entries;
  const hiddenCount = Math.max(0, history.length - 3);
  const entries = [...(expanded ? history : history.slice(-3)), timeline.current];
  return (
    <Box sx={{ flex: 1, minWidth: 0, py: 1, whiteSpace: "normal", lineHeight: 1.4 }}>
      {hiddenCount > 0 && (
        <Button size="small" aria-expanded={expanded} onClick={(event) => { event.stopPropagation(); setExpanded(!expanded); }}
          sx={{ color: "#8fbfff", p: 0, mb: 0.75, textTransform: "none", fontSize: "0.75rem" }}>
          {expanded ? "Show recent steps" : `Show ${hiddenCount} earlier events`}
        </Button>
      )}
      <Box component="ol" aria-label="Operation timeline" sx={{ m: 0, p: 0, listStyle: "none", maxHeight: expanded ? 360 : "none", overflowY: expanded ? "auto" : "visible",
        "@keyframes clusterTimelineSpin": { to: { transform: "rotate(360deg)" } } }}>
        {entries.map((entry, index) => {
          const animate = entry.active && !stale;
          const color = COLORS[entry.active && stale ? "recorded" : entry.tone];
          const Icon = entry.tone === "failed" ? ErrorOutlineRounded : entry.tone === "complete" ? CheckCircleOutlineRounded : animate ? SyncRounded : PendingOutlined;
          return (
            <Box component="li" key={entry.id} aria-current={entry.id === "current" ? "step" : undefined}
              sx={{ display: "flex", gap: 1, position: "relative", pb: index === entries.length - 1 ? 0 : 1,
                "&:not(:last-child)::before": { content: '""', position: "absolute", left: 8, top: 20, bottom: 2, width: 1, bgcolor: "rgba(148,163,184,0.3)" } }}>
              <Icon aria-hidden="true" sx={{ fontSize: 18, mt: 0.2, flexShrink: 0, color,
                animation: animate ? "clusterTimelineSpin 1.15s linear infinite" : "none",
                "@media (prefers-reduced-motion: reduce)": { animation: "none" } }} />
              <Box sx={{ minWidth: 0, overflowWrap: "anywhere" }}>
                <Typography sx={{ color, fontSize: "0.8rem", fontWeight: entry.id === "current" ? 600 : 500 }}>{entry.id === "current" ? "Current: " : ""}{entry.label}</Typography>
                {entry.soakLabel && <Typography sx={{ color: "#cbd5e1", fontSize: "0.75rem" }}>{stale ? "Soak timing unavailable · Updates delayed" : entry.soakLabel}</Typography>}
                {entry.message && <Typography sx={{ color: "#cbd5e1", fontSize: "0.75rem", whiteSpace: "pre-wrap" }}>{entry.message}</Typography>}
                <Typography sx={{ color: "#94a3b8", fontSize: "0.7rem" }}>
                  {entry.id === "current" ? "Last recorded: " : ""}{formatTimestamp(entry.timestamp)}
                </Typography>
              </Box>
            </Box>
          );
        })}
      </Box>
      {!history.length && <Typography sx={{ color: "#94a3b8", fontSize: "0.75rem", mt: 0.5 }}>No lifecycle events available.</Typography>}
      {stale && <Typography role="status" sx={{ color: "#fbbf24", fontSize: "0.75rem", mt: 0.5 }}>Updates delayed · showing last recorded progress</Typography>}
    </Box>
  );
}
