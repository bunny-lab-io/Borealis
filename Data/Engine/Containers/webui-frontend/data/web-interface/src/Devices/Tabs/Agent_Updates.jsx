import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Chip, Typography } from "@mui/material";
import {
  AutorenewRounded as StageActiveIcon,
  CheckCircleRounded as StageCompleteIcon,
  ErrorOutlineRounded as StageErrorIcon,
  ExpandMore as ExpandMoreIcon,
  OpenInNewRounded as OpenInNewRoundedIcon,
  RadioButtonUncheckedRounded as StagePendingIcon,
  RemoveCircleOutlineRounded as StageSkippedIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { themeQuartz } from "ag-grid-community";
import { APP_PATHS } from "../../app/routes/paths.js";
import { MAGIC_UI, gridFontFamily } from "./Shared.jsx";

const ACTIVE_UPDATE_STATES = new Set(["pending", "requested", "running", "recovering", "awaiting_reconnect", "awaiting_health", "verifying"]);
const POLL_ACTIVE_MS = 5000;
const POLL_IDLE_MS = 30000;

const AGENT_UPDATE_GRID_THEME = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  chromeBackgroundColor: "#0b1224",
  foregroundColor: "#f4f7ff",
  fontFamily: { googleFont: "IBM Plex Sans" },
  headerFontSize: 14,
  rowHeight: 44,
});

const AGENT_UPDATE_PHASES = [
  { id: "requesting_agent_update", label: "Agent Recieved Update Command" },
  { id: "resolving_engine_artifact", label: "Resolving Engine Artifact" },
  { id: "downloading_agent_artifact", label: "Downloading Agent Artifact" },
  { id: "verifying_agent_artifact", label: "Verifying Agent Artifact" },
  { id: "protecting_agent_identity_trust", label: "Protecting Identity/Trust" },
  { id: "quiescing_managed_components", label: "Quiescing Managed Components" },
  { id: "stopping_borealis_agent_service", label: "Borealis Agent Service", depth: 1 },
  { id: "stopping_ultravnc_service", label: "UltraVNC Service", depth: 1 },
  { id: "stopping_wireguard_service", label: "WireGuard Services", depth: 1 },
  { id: "evaluating_rdp_service", label: "Native RDP Service", depth: 1 },
  { id: "staging_agent_binary", label: "Staging Agent Binary" },
  { id: "reconciling_agent_host", label: "Repairing Agent Configuration" },
  { id: "starting_agent_runtime", label: "Starting Borealis Agent Service" },
  { id: "waiting_agent_reconnection", label: "Waiting for Reconnection" },
  { id: "verifying_post_update_health", label: "Checking Post-Update Agent Role Health" },
  { id: "update_completed", label: "Agent Update Completed" },
];

const AGENT_UPDATE_SUMMARY_LABELS = new Map([
  ["Agent Received Request", "Agent Recieved Update Command"],
  ["Current Binary Retained", "Current Agent Already Up-To-Date"],
  ["Skipped When Current", "Skipped When Already Up-To-Date"],
  ["Managed Components Stopped", "Stopped Services"],
  ["Agent Host Reconciled", "Agent Configuration Repaired"],
  ["Engine Socket", "Engine Socket Connection"],
  ["Post-Update Health Verified", "Post-Update Agent Role Health"],
  ["SYSTEM Context", "Role: SYSTEM Context"],
  ["Current User Context", "Role: Current User Context"],
  ["Device Auditor", "Role: Device Inventory"],
  ["File Mangement", "Role: File Management"],
  ["File Management", "Role: File Management"],
  ["Process Management", "Role: Process Management"],
  ["Registry Management", "Role: Registry Editor"],
  ["Remote Shell", "Role: Remote Shell"],
  ["Borealis Agent - RDP", "Service: Borealis Agent - RDP"],
  ["Service Management", "Role: Service Management"],
  ["Patch Management", "Role: Patch Management"],
  ["Software Management", "Role: Software Management"],
  ["UltraVNC Service", "Service: UltraVNC"],
  ["WireGuard VPN", "Service: WireGuard"],
  ["Borealis Agent Service Started", "Started Borealis Agent Service"],
  ["Update Completed", "Agent Update Completed"],
]);

const AGENT_UPDATE_HEALTH_SERVICE_PHASES = new Set([
  "role:system:rdp",
  "role:system:vnc",
  "role:system:wireguard_tunnel",
]);

const AGENT_UPDATE_PROMOTED_HEALTH_PHASES = new Set(["role:system:engine_socket"]);

const AGENT_UPDATE_AUTO_SIZE_COLUMNS = ["status", "source_label", "requested_by", "started_label", "duration_label"];

const AGENT_UPDATE_ISLAND_SX = {
  minWidth: 0,
  minHeight: 0,
  height: "100%",
  display: "flex",
  flexDirection: "column",
  overflow: "hidden",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  borderRadius: 3,
  background: MAGIC_UI.panelBg,
  boxShadow: "0 18px 45px rgba(2,6,23,0.5)",
};

function normalizedText(value) {
  return String(value ?? "").trim();
}

export function agentUpdateIsActive(operation = null) {
  return Boolean(operation && ACTIVE_UPDATE_STATES.has(normalizedText(operation.status).toLowerCase()));
}

export function formatAgentUpdateTimestamp(epochSeconds = 0) {
  const timestamp = Number(epochSeconds || 0);
  if (!Number.isFinite(timestamp) || timestamp <= 0) return "—";
  return new Date(timestamp * 1000).toLocaleString();
}

export function formatAgentUpdateDuration(startEpoch = 0, endEpoch = 0) {
  const start = Number(startEpoch || 0);
  const end = Number(endEpoch || 0);
  if (!start || !end) return "—";
  let remaining = Math.max(0, Math.floor(end - start));
  const hours = Math.floor(remaining / 3600);
  remaining %= 3600;
  const minutes = Math.floor(remaining / 60);
  const seconds = remaining % 60;
  return [hours ? `${hours}h` : "", minutes ? `${minutes}m` : "", `${seconds}s`].filter(Boolean).join(" ");
}

function latestEventsByPhase(events = []) {
  const latest = new Map();
  for (const event of Array.isArray(events) ? events : []) {
    const phaseID = normalizedText(event?.phase_id).toLowerCase();
    if (phaseID) latest.set(phaseID, event);
  }
  return latest;
}

function phaseTone(state = "pending") {
  switch (normalizedText(state).toLowerCase()) {
    case "success":
      return { visualState: "complete", color: MAGIC_UI.accentC };
    case "failed":
    case "timed_out":
      return { visualState: "failed", color: "#fb7185" };
    case "running":
    case "recovering":
    case "requested":
    case "awaiting_reconnect":
    case "awaiting_health":
    case "verifying":
      return { visualState: "active", color: MAGIC_UI.accentA };
    case "skipped":
      return { visualState: "skipped", color: MAGIC_UI.textMuted };
    default:
      return { visualState: "pending", color: "rgba(148, 163, 184, 0.58)" };
  }
}

function timelineStep(phase, event = {}) {
  const state = normalizedText(event.state || "pending").toLowerCase();
  const tone = phaseTone(state);
  const sourceLabel = normalizedText(event.summary) || phase.label;
  return {
    id: phase.id,
    label: AGENT_UPDATE_SUMMARY_LABELS.get(sourceLabel) || sourceLabel,
    detail: normalizedText(event.detail),
    state,
    visualState: tone.visualState,
    color: tone.color,
    depth: Number(phase.depth || 0),
    isGroup: Boolean(phase.isGroup),
    retryCount: Number(event.retry_count || 0),
  };
}

function timelineGroup(id, label, children = []) {
  const visualStates = children.map(({ visualState }) => visualState);
  let state = "pending";
  if (visualStates.includes("failed")) state = "failed";
  else if (visualStates.includes("active")) state = "running";
  else if (visualStates.length > 0 && visualStates.every((value) => ["complete", "skipped"].includes(value))) state = "success";
  else if (visualStates.some((value) => ["complete", "skipped"].includes(value))) state = "running";
  return timelineStep({ id, label, depth: 1, isGroup: true }, { state });
}

export function buildAgentUpdateTimeline(operation = null) {
  const events = Array.isArray(operation?.events) ? operation.events : [];
  const latest = latestEventsByPhase(events);
  const known = new Set(AGENT_UPDATE_PHASES.map(({ id }) => id));
  const healthSteps = Array.from(latest.entries())
    .filter(([id]) => id.startsWith("role:") && !known.has(id))
    .map(([id, event]) => timelineStep({ id, label: id.slice(5), depth: 2 }, event));
  const promotedHealthSteps = healthSteps
    .filter(({ id }) => AGENT_UPDATE_PROMOTED_HEALTH_PHASES.has(id))
    .map((step) => ({ ...step, depth: 0 }));
  const roleSteps = healthSteps.filter(({ id }) => !AGENT_UPDATE_HEALTH_SERVICE_PHASES.has(id) && !AGENT_UPDATE_PROMOTED_HEALTH_PHASES.has(id));
  const serviceSteps = healthSteps.filter(({ id }) => AGENT_UPDATE_HEALTH_SERVICE_PHASES.has(id));
  const steps = [];
  for (const phase of AGENT_UPDATE_PHASES) {
    if (phase.id === "waiting_agent_reconnection") steps.push(...promotedHealthSteps);
    steps.push(timelineStep(phase, latest.get(phase.id)));
    if (phase.id === "verifying_post_update_health") {
      steps.push(timelineGroup("post_update_roles", "Roles", roleSteps), ...roleSteps);
      steps.push(timelineGroup("post_update_services", "Services", serviceSteps), ...serviceSteps);
    }
  }
  return steps;
}

export function useAgentUpdateHistory(deviceGuid = "") {
  const [data, setData] = useState({ active_operation: null, operations: [] });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const normalizedGUID = normalizedText(deviceGuid);
  const refresh = useCallback(async ({ silent = false } = {}) => {
    if (!normalizedGUID) return;
    if (!silent) setLoading(true);
    try {
      const response = await fetch(`/api/devices/${encodeURIComponent(normalizedGUID)}/agent-updates?limit=100`, { credentials: "include" });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      setData({ active_operation: payload?.active_operation || null, operations: Array.isArray(payload?.operations) ? payload.operations : [] });
      setError("");
    } catch (requestError) {
      setError(String(requestError?.message || requestError));
    } finally {
      if (!silent) setLoading(false);
    }
  }, [normalizedGUID]);

  const active = agentUpdateIsActive(data.active_operation);
  useEffect(() => {
    if (!normalizedGUID) {
      setData({ active_operation: null, operations: [] });
      return undefined;
    }
    void refresh();
    const timer = window.setInterval(() => void refresh({ silent: true }), active ? POLL_ACTIVE_MS : POLL_IDLE_MS);
    return () => window.clearInterval(timer);
  }, [active, normalizedGUID, refresh]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedGUID) return undefined;
    const handleProgress = (payload = {}) => {
      if (normalizedText(payload.device_guid).toUpperCase() !== normalizedGUID.toUpperCase()) return;
      void refresh({ silent: true });
    };
    socket.on("agent_update_progress_changed", handleProgress);
    return () => socket.off("agent_update_progress_changed", handleProgress);
  }, [normalizedGUID, refresh]);

  return { ...data, loading, error, refresh };
}

function operationStatusTone(status = "") {
  const normalized = normalizedText(status).toLowerCase();
  if (normalized === "success") return "success";
  if (normalized === "failed" || normalized === "timed_out") return "error";
  if (normalized === "running" || normalized === "awaiting_health" || normalized === "awaiting_reconnect") return "info";
  return "default";
}

export default function AgentUpdates({ history, selectedOperationId = "", onSelectOperation, onOpenJob }) {
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  const gridApiRef = useRef(null);
  const operations = Array.isArray(history?.operations) ? history.operations : [];
  const selectedOperation = useMemo(
    () => operations.find((operation) => normalizedText(operation.operation_id) === normalizedText(selectedOperationId)) || history?.active_operation || operations[0] || null,
    [history?.active_operation, operations, selectedOperationId]
  );
  const timeline = useMemo(() => buildAgentUpdateTimeline(selectedOperation), [selectedOperation]);

  useEffect(() => {
    if (!agentUpdateIsActive(selectedOperation)) return undefined;
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [selectedOperation]);

  const rows = useMemo(() => operations.map((operation) => {
    const end = Number(operation.ended_at || 0) || (agentUpdateIsActive(operation) ? now : Number(operation.updated_at || 0));
    return {
      ...operation,
      source_label: operation.source === "hourly_update_checker" ? "Hourly Update Checker" : "Operator Initiated",
      started_label: formatAgentUpdateTimestamp(operation.started_at),
      duration_label: formatAgentUpdateDuration(operation.started_at, end),
    };
  }), [now, operations]);

  const columns = useMemo(() => [
    {
      field: "status", headerName: "Status", minWidth: 125, cellClass: "auto-col-tight",
      cellRenderer: ({ value }) => <Chip size="small" color={operationStatusTone(value)} variant="outlined" label={normalizedText(value).replaceAll("_", " ") || "Unknown"} />,
    },
    { field: "source_label", headerName: "Source", minWidth: 170, cellClass: "auto-col-tight" },
    { field: "requested_by", headerName: "Requested By", minWidth: 150, cellClass: "auto-col-tight" },
    { field: "started_label", headerName: "Started", minWidth: 180, cellClass: "auto-col-tight" },
    { field: "duration_label", headerName: "Duration", minWidth: 110, cellClass: "auto-col-tight" },
    {
      field: "scheduled_job_id", headerName: "Job", minWidth: 130, flex: 1, resizable: false, cellClass: "auto-col-tight",
      cellRenderer: ({ value }) => value ? (
        <Button size="small" endIcon={<OpenInNewRoundedIcon fontSize="small" />} onClick={(event) => { event.stopPropagation(); onOpenJob?.(value); }}>
          Job {value}
        </Button>
      ) : "—",
    },
  ], [onOpenJob]);

  const autoSizeHistoryColumns = useCallback((api = gridApiRef.current) => {
    if (!api || history?.loading || rows.length === 0) return;
    const schedule = typeof window.requestAnimationFrame === "function"
      ? window.requestAnimationFrame.bind(window)
      : (callback) => window.setTimeout(callback, 0);
    schedule(() => {
      try {
        api.autoSizeColumns(AGENT_UPDATE_AUTO_SIZE_COLUMNS, true);
      } catch {
        // Grid can be disposed before deferred sizing runs.
      }
    });
  }, [history?.loading, rows.length]);

  useEffect(() => {
    autoSizeHistoryColumns();
  }, [autoSizeHistoryColumns, operations]);

  const failedEvents = (Array.isArray(selectedOperation?.events) ? selectedOperation.events : []).filter((event) => ["failed", "timed_out"].includes(normalizedText(event?.state).toLowerCase()));

  return (
    <Box
      data-testid="agent-updates-layout"
      sx={{
        flex: "1 1 0",
        width: "100%",
        height: { xs: "auto", lg: "100%" },
        minWidth: 0,
        minHeight: { xs: 900, lg: 0 },
        display: "grid",
        gridTemplateColumns: { xs: "minmax(0, 1fr)", lg: "minmax(280px, 1fr) minmax(0, 2fr)" },
        gridTemplateRows: { xs: "minmax(420px, auto) minmax(460px, 1fr)", lg: "minmax(0, 1fr)" },
        gap: 2,
        overflow: "hidden",
        "@keyframes agentUpdateTimelineSpin": {
          from: { transform: "rotate(0deg)" },
          to: { transform: "rotate(360deg)" },
        },
        "@keyframes agentUpdateTimelinePulse": {
          "0%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0.28)" },
          "70%": { boxShadow: "0 0 0 10px rgba(125, 211, 252, 0)" },
          "100%": { boxShadow: "0 0 0 0 rgba(125, 211, 252, 0)" },
        },
      }}
    >
      <Box data-testid="agent-update-timeline-island" sx={AGENT_UPDATE_ISLAND_SX}>
        <Box sx={{ px: 2, py: 1.75, borderBottom: "1px solid rgba(148,163,184,0.18)", flexShrink: 0 }}>
          <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>Update Timeline</Typography>
        </Box>
        <Box role="list" aria-label="Agent update timeline" sx={{ flexGrow: 1, minHeight: 0, overflowY: "auto", px: 2, py: 1.75 }}>
          {timeline.map((step, index) => {
            const isLast = index === timeline.length - 1;
            const depth = Math.max(0, step.depth);
            const previousDepth = index > 0 ? Math.max(0, timeline[index - 1].depth) : -1;
            const nextDepth = !isLast ? Math.max(0, timeline[index + 1].depth) : -1;
            const treeIndent = 22;
            const markerCenter = 10;
            const markerLeft = depth * treeIndent;
            const showDetail = ["active", "failed", "skipped"].includes(step.visualState) && Boolean(step.detail);
            const connectorColor =
              step.visualState === "complete"
                ? "rgba(52,211,153,0.62)"
                : step.visualState === "active"
                  ? "rgba(125,211,252,0.68)"
                  : step.visualState === "failed"
                    ? "rgba(251,113,133,0.68)"
                    : "rgba(148,163,184,0.32)";
            return (
              <Box
                key={step.id}
                role="listitem"
                sx={{
                  display: "flex",
                  alignItems: "stretch",
                  gap: 1.25,
                  width: "100%",
                }}
              >
                <Box sx={{ width: markerLeft + 22, minHeight: 20, position: "relative", flexShrink: 0 }}>
                  {Array.from({ length: depth }, (_, level) => {
                    const continuesBelow = level === 0 ? nextDepth >= 0 : nextDepth > level;
                    return (
                      <Box
                        key={`${step.id}-ancestor-${level}`}
                        sx={{
                          position: "absolute",
                          zIndex: 0,
                          top: 0,
                          bottom: continuesBelow ? 0 : `calc(100% - ${markerCenter}px)`,
                          left: level * treeIndent + markerCenter - 1,
                          width: 2,
                          borderRadius: 999,
                          background: connectorColor,
                        }}
                      />
                    );
                  })}
                  {depth > 0 ? (
                    <Box
                      sx={{
                        position: "absolute",
                        zIndex: 0,
                        top: markerCenter - 1,
                        left: markerLeft - treeIndent + markerCenter,
                        width: treeIndent,
                        height: 2,
                        borderRadius: 999,
                        background: connectorColor,
                      }}
                    />
                  ) : null}
                  {index > 0 && (depth === 0 || previousDepth === depth) ? (
                    <Box
                      sx={{
                        position: "absolute",
                        zIndex: 0,
                        top: 0,
                        height: markerCenter,
                        left: markerLeft + markerCenter - 1,
                        width: 2,
                        borderRadius: 999,
                        background: connectorColor,
                      }}
                    />
                  ) : null}
                  {!isLast && nextDepth >= depth ? (
                    <Box
                      sx={{
                        position: "absolute",
                        zIndex: 0,
                        top: markerCenter,
                        bottom: 0,
                        left: markerLeft + markerCenter - 1,
                        width: 2,
                        borderRadius: 999,
                        background: connectorColor,
                      }}
                    />
                  ) : null}
                  <Box
                    sx={{
                      position: "absolute",
                      zIndex: 1,
                      top: 0,
                      left: markerLeft,
                      width: 20,
                      height: 20,
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                      color: step.color,
                      borderRadius: "50%",
                      ...(step.visualState === "active"
                        ? { background: "rgba(125,211,252,0.12)", animation: "agentUpdateTimelinePulse 1.8s ease-out infinite" }
                        : null),
                    }}
                  >
                    {step.visualState === "complete" ? (
                      <StageCompleteIcon sx={{ fontSize: 18 }} />
                    ) : step.visualState === "active" ? (
                      <StageActiveIcon sx={{ fontSize: 18, animation: "agentUpdateTimelineSpin 1.15s linear infinite" }} />
                    ) : step.visualState === "failed" ? (
                      <StageErrorIcon sx={{ fontSize: 18 }} />
                    ) : step.visualState === "skipped" ? (
                      <StageSkippedIcon sx={{ fontSize: 18 }} />
                    ) : (
                      <StagePendingIcon sx={{ fontSize: 18 }} />
                    )}
                  </Box>
                </Box>
                <Box sx={{ pt: 0.05, pb: isLast ? 0 : 0.65, minWidth: 0, flexGrow: 1 }}>
                  <Box sx={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", gap: 1 }}>
                    <Typography
                      sx={{
                        color: step.visualState === "pending" ? MAGIC_UI.textMuted : MAGIC_UI.textBright,
                        fontSize: step.isGroup ? "0.82rem" : step.depth > 1 ? "0.76rem" : step.depth ? "0.78rem" : "0.85rem",
                        fontWeight: step.isGroup ? 700 : ["active", "complete"].includes(step.visualState) ? 600 : 500,
                        letterSpacing: 0.2,
                        minWidth: 0,
                      }}
                    >
                      {step.label}
                    </Typography>
                    <Typography sx={{ color: step.color, fontSize: "0.62rem", fontWeight: 700, letterSpacing: 0.7, textTransform: "uppercase", flexShrink: 0 }}>
                      {step.state.replaceAll("_", " ")}
                    </Typography>
                  </Box>
                  {showDetail ? (
                    <Typography sx={{ mt: 0.2, color: step.visualState === "failed" ? "#fb7185" : step.color, fontSize: "0.72rem", lineHeight: 1.35 }}>
                      {step.detail}{step.retryCount ? ` Retry count: ${step.retryCount}.` : ""}
                    </Typography>
                  ) : null}
                </Box>
              </Box>
            );
          })}
          {selectedOperation?.failure_summary || failedEvents.length ? (
            <Accordion disableGutters sx={{ mt: 1.5, bgcolor: "rgba(127,29,29,0.18)", border: "1px solid rgba(248,113,113,0.35)", color: "#fecaca" }}>
              <AccordionSummary expandIcon={<ExpandMoreIcon sx={{ color: "#fecaca" }} />}>
                <Typography sx={{ fontWeight: 700 }}>Failure details</Typography>
              </AccordionSummary>
              <AccordionDetails>
                <Typography sx={{ whiteSpace: "pre-wrap", fontSize: 13 }}>{selectedOperation.failure_summary || "Agent reported failed update phase."}</Typography>
                {failedEvents.map((event) => (
                  <Box key={event.event_id || `${event.phase_id}-${event.agent_timestamp}`} sx={{ mt: 1 }}>
                    <Typography sx={{ fontWeight: 700, fontSize: 13 }}>{event.summary || event.phase_id}</Typography>
                    <Typography sx={{ color: "#fca5a5", fontSize: 12 }}>{event.detail || "No additional Agent diagnostic detail."} Retry count: {Number(event.retry_count || 0)}.</Typography>
                  </Box>
                ))}
              </AccordionDetails>
            </Accordion>
          ) : null}
        </Box>
      </Box>

      <Box
        data-testid="agent-update-history-island"
        sx={{
          ...AGENT_UPDATE_ISLAND_SX,
          fontFamily: gridFontFamily,
          "& .ag-root-wrapper": { height: "100%", border: "none", borderRadius: 0 },
          "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            textAlign: "left",
            padding: "8px 12px 8px 18px",
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
            paddingLeft: "12px",
            paddingRight: "9px",
          },
          "& .ag-row-hover": { backgroundColor: "rgba(73,156,196,0.2) !important" },
        }}
      >
        {history?.error ? <Alert severity="error" sx={{ m: 2, flexShrink: 0 }}>{history.error}</Alert> : null}
        <Box sx={{ flexGrow: 1, minHeight: 0 }}>
          <AgGridReact
            theme={AGENT_UPDATE_GRID_THEME}
            rowData={rows}
            columnDefs={columns}
            defaultColDef={{ sortable: true, resizable: true, filter: "agTextColumnFilter", flex: 0 }}
            getRowId={({ data }) => normalizedText(data.operation_id)}
            rowHeight={44}
            headerHeight={44}
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            suppressCellFocus
            loading={Boolean(history?.loading)}
            onGridReady={({ api }) => {
              gridApiRef.current = api;
              autoSizeHistoryColumns(api);
            }}
            onRowClicked={({ data }) => onSelectOperation?.(data?.operation_id)}
          />
        </Box>
      </Box>
    </Box>
  );
}

export function openAgentUpdateJob(navigate, jobID) {
  if (!jobID) return;
  navigate(`${APP_PATHS.job(jobID)}?tab=job_history`);
}
