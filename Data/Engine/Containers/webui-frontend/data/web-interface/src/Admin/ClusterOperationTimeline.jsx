import React, { useState } from "react";
import { Box, Button, Typography } from "@mui/material";
import { CheckCircleOutlineRounded, ErrorOutlineRounded, PendingOutlined, SyncRounded } from "@mui/icons-material";

const humanize = (value) => String(value || "").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
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
  minimum_ready_soak: "Observe sustained workload health",
  minimum_role_soak: "Observe sustained cluster role health",
  minimum_candidate_soak: "Observe sustained candidate health",
  hmr_candidate_soak: "Observe sustained candidate health",
  verify_cluster: "Verify cluster health",
};

function stepLabel(value, identities) {
  const raw = String(value || "");
  const scoped = /^(?:node|admit):([^:]+):(.+)$/.exec(raw);
  const name = scoped ? scoped[2] : raw;
  const label = STEP_LABELS[name] || humanize(name);
  return scoped ? `${identities.get(scoped[1]) || scoped[1]} · ${label}` : label;
}

// Events prove completed transitions. Do not infer unrecorded future steps or soak deadlines.
export function buildClusterOperationTimeline(operation, events = [], nodes = [], admissions = []) {
  const identities = new Map([...nodes, ...admissions].map((node) => [String(node.id), node.node_name]));
  const ordered = [...new Map(events.filter((event) => String(event.operation_id) === String(operation.id))
    .map((event) => [String(event.id), event])).values()].sort((a, b) => Number(a.id) - Number(b.id));
  let transition = null;
  let attempt = 1;
  const entries = ordered.map((event) => {
    const detail = event.details || {};
    const step = stepLabel(detail.step, identities);
    let label = humanize(event.event_type || "Cluster event");
    let tone = "recorded";
    let next = null;
    switch (event.event_type) {
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
        attempt = Number(detail.attempt) || attempt + 1;
        label = `Retry requested · Attempt ${attempt}`;
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
        break;
    }
    if (next) transition = { ...next, timestamp: Number(event.created_at), attempt };
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
  const active = state === "running" && current.step !== "complete";
  const status = { running: "In progress", queued: "Queued", waiting: "Waiting", succeeded: "Completed", failed: "Failed", cancelled: "Cancelled", superseded: "Superseded" }[state] || humanize(state);
  const step = current.step && current.step !== "complete" ? stepLabel(current.step, identities) : "";
  return {
    entries,
    current: {
      id: "current", label: `${status}${step && ["running", "queued", "waiting", "failed"].includes(state) ? ` · ${step}` : ""}`,
      tone: active ? "active" : state === "failed" ? "failed" : state === "succeeded" ? "complete" : "recorded",
      timestamp: current.timestamp, message: current.error || "", active,
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
