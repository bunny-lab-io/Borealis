import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, Chip, Stack, Tooltip, Typography } from "@mui/material";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";
import OpenInNewRoundedIcon from "@mui/icons-material/OpenInNewRounded";
import { AgGridReact } from "ag-grid-react";
import { themeQuartz } from "ag-grid-community";
import ReactFlow, { Background, Controls, MarkerType } from "reactflow";
import "reactflow/dist/style.css";
import PageBodyFrame from "../../PageBodyFrame.jsx";
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

const PHASES = [
  ["requesting_agent_update", "Agent Received Request", 20, 20],
  ["resolving_engine_artifact", "Resolving Engine Artifact", 310, 20],
  ["downloading_agent_artifact", "Downloading Agent Artifact", 600, 20],
  ["verifying_agent_artifact", "Verifying Agent Artifact", 890, 20],
  ["protecting_agent_identity_trust", "Protecting Identity/Trust", 20, 130],
  ["quiescing_managed_components", "Quiescing Managed Components", 310, 130],
  ["staging_agent_binary", "Staging Agent Binary", 600, 130],
  ["reconciling_agent_host", "Reconciling Agent Host", 890, 130],
  ["starting_agent_runtime", "Starting Agent Runtime", 20, 450],
  ["waiting_agent_reconnection", "Waiting for Reconnection", 310, 450],
  ["verifying_post_update_health", "Verifying Post-Update Health", 600, 450],
  ["update_completed", "Update Completed", 890, 450],
  ["stopping_borealis_agent_service", "Borealis Agent", 20, 285],
  ["stopping_ultravnc_service", "UltraVNC", 310, 285],
  ["stopping_wireguard_service", "WireGuard", 600, 285],
  ["evaluating_rdp_service", "Native RDP", 890, 285],
];

const MAIN_PHASE_IDS = PHASES.slice(0, 12).map(([id]) => id);

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
    case "success": return { color: "#34d399", background: "rgba(52,211,153,0.12)" };
    case "failed": return { color: "#fb7185", background: "rgba(251,113,133,0.12)" };
    case "timed_out": return { color: "#f87171", background: "rgba(248,113,113,0.12)" };
    case "running": return { color: "#7dd3fc", background: "rgba(125,211,252,0.13)" };
    case "recovering": return { color: "#fbbf24", background: "rgba(251,191,36,0.13)" };
    case "skipped": return { color: "#94a3b8", background: "rgba(148,163,184,0.1)" };
    default: return { color: "#64748b", background: "rgba(100,116,139,0.08)" };
  }
}

export function buildAgentUpdateGraph(operation = null) {
  const events = Array.isArray(operation?.events) ? operation.events : [];
  const latest = latestEventsByPhase(events);
  const nodes = PHASES.map(([id, fallbackLabel, x, y]) => {
    const event = latest.get(id) || {};
    const state = normalizedText(event.state || "pending").toLowerCase();
    const tone = phaseTone(state);
    return {
      id,
      position: { x, y },
      data: {
        label: (
          <Tooltip arrow title={normalizedText(event.detail) || `${fallbackLabel}: ${state.replaceAll("_", " ")}`}>
            <Stack spacing={0.35}>
              <Typography sx={{ color: "#e2e8f0", fontSize: 12, fontWeight: 700, lineHeight: 1.2 }}>
                {normalizedText(event.summary) || fallbackLabel}
              </Typography>
              <Typography sx={{ color: tone.color, fontSize: 10, textTransform: "uppercase", letterSpacing: 0.7 }}>
                {state.replaceAll("_", " ")}
              </Typography>
            </Stack>
          </Tooltip>
        ),
      },
      draggable: false,
      selectable: false,
      style: {
        width: 250,
        minHeight: 58,
        color: "#e2e8f0",
        background: tone.background,
        border: `1px solid ${tone.color}`,
        borderRadius: 10,
        boxShadow: "0 10px 28px rgba(2,6,23,0.5)",
      },
    };
  });

  const known = new Set(PHASES.map(([id]) => id));
  const roleEvents = Array.from(latest.entries()).filter(([id]) => id.startsWith("role:") && !known.has(id));
  roleEvents.forEach(([id, event], index) => {
    const state = normalizedText(event.state || "pending").toLowerCase();
    const tone = phaseTone(state);
    nodes.push({
      id,
      position: { x: 20 + (index % 4) * 290, y: 565 + Math.floor(index / 4) * 70 },
      data: { label: `${normalizedText(event.summary) || id.slice(5)} · ${state.replaceAll("_", " ")}` },
      draggable: false,
      selectable: false,
      style: { width: 250, minHeight: 48, color: tone.color, background: tone.background, border: `1px solid ${tone.color}`, borderRadius: 10 },
    });
  });

  const edgeStyle = { stroke: "#7dd3fc", strokeWidth: 1.6 };
  const edges = MAIN_PHASE_IDS.slice(0, -1).map((source, index) => ({
    id: `${source}-${MAIN_PHASE_IDS[index + 1]}`,
    source,
    target: MAIN_PHASE_IDS[index + 1],
    animated: latest.get(source)?.state === "running",
    style: edgeStyle,
    markerEnd: { type: MarkerType.ArrowClosed, color: "#7dd3fc" },
  }));
  ["stopping_borealis_agent_service", "stopping_ultravnc_service", "stopping_wireguard_service", "evaluating_rdp_service"].forEach((target) => {
    edges.push({ id: `quiesce-${target}`, source: "quiescing_managed_components", target, style: edgeStyle, markerEnd: { type: MarkerType.ArrowClosed, color: "#7dd3fc" } });
  });
  roleEvents.forEach(([target]) => {
    edges.push({ id: `health-${target}`, source: "verifying_post_update_health", target, style: edgeStyle, markerEnd: { type: MarkerType.ArrowClosed, color: "#7dd3fc" } });
  });
  return { nodes, edges };
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
  const operations = Array.isArray(history?.operations) ? history.operations : [];
  const selectedOperation = useMemo(
    () => operations.find((operation) => normalizedText(operation.operation_id) === normalizedText(selectedOperationId)) || history?.active_operation || operations[0] || null,
    [history?.active_operation, operations, selectedOperationId]
  );
  const graph = useMemo(() => buildAgentUpdateGraph(selectedOperation), [selectedOperation]);

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
      ended_label: agentUpdateIsActive(operation) ? "In progress" : formatAgentUpdateTimestamp(operation.ended_at),
      duration_label: formatAgentUpdateDuration(operation.started_at, end),
      build_label: [operation.installed_build_before, operation.installed_build_after || operation.target_build_id].filter(Boolean).join(" → ") || "—",
    };
  }), [now, operations]);

  const columns = useMemo(() => [
    { field: "operation_id", headerName: "Operation ID", minWidth: 220 },
    { field: "source_label", headerName: "Source", minWidth: 180 },
    {
      field: "status", headerName: "Status", minWidth: 135,
      cellRenderer: ({ value }) => <Chip size="small" color={operationStatusTone(value)} variant="outlined" label={normalizedText(value).replaceAll("_", " ") || "Unknown"} />,
    },
    { field: "requested_by", headerName: "Requested By", minWidth: 150 },
    { field: "started_label", headerName: "Started", minWidth: 180 },
    { field: "ended_label", headerName: "Ended", minWidth: 180 },
    { field: "duration_label", headerName: "Duration", minWidth: 120 },
    { field: "build_label", headerName: "Installed Build", minWidth: 220 },
    {
      field: "scheduled_job_id", headerName: "Job", minWidth: 130, flex: 1,
      cellRenderer: ({ value }) => value ? (
        <Button size="small" endIcon={<OpenInNewRoundedIcon fontSize="small" />} onClick={(event) => { event.stopPropagation(); onOpenJob?.(value); }}>
          Job {value}
        </Button>
      ) : "—",
    },
  ], [onOpenJob]);

  const failedEvents = (Array.isArray(selectedOperation?.events) ? selectedOperation.events : []).filter((event) => ["failed", "timed_out"].includes(normalizedText(event?.state).toLowerCase()));
  const graphStack = (
    <Stack spacing={1.5}>
      <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" spacing={1}>
        <Box>
          <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>Update topology and progress</Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: 13 }}>
            {selectedOperation ? `Operation ${selectedOperation.operation_id} · ${formatAgentUpdateDuration(selectedOperation.started_at, Number(selectedOperation.ended_at || 0) || now)}` : "No Agent update operation recorded."}
          </Typography>
        </Box>
        {selectedOperation ? <Chip color={operationStatusTone(selectedOperation.status)} variant="outlined" label={normalizedText(selectedOperation.status).replaceAll("_", " ")} /> : null}
      </Stack>
      <Box sx={{ height: 430, border: "1px solid rgba(125,211,252,0.2)", borderRadius: 2, background: "rgba(2,6,23,0.72)", overflow: "hidden" }}>
        <ReactFlow nodes={graph.nodes} edges={graph.edges} fitView fitViewOptions={{ padding: 0.14 }} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} panOnDrag zoomOnDoubleClick={false} proOptions={{ hideAttribution: true }}>
          <Background color="rgba(125,211,252,0.16)" gap={24} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </Box>
      {selectedOperation?.failure_summary || failedEvents.length ? (
        <Accordion disableGutters sx={{ bgcolor: "rgba(127,29,29,0.18)", border: "1px solid rgba(248,113,113,0.35)", color: "#fecaca" }}>
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
    </Stack>
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      fillHeight={false}
      outerSx={{ p: 0, minHeight: 760 }}
      shellSx={{ minHeight: 760 }}
      stack={graphStack}
      main={(
        <Box sx={{ minHeight: 380, height: 430, fontFamily: gridFontFamily }}>
          {history?.error ? <Alert severity="error" sx={{ m: 2 }}>{history.error}</Alert> : null}
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
            onRowClicked={({ data }) => onSelectOperation?.(data?.operation_id)}
          />
        </Box>
      )}
    />
  );
}

export function openAgentUpdateJob(navigate, jobID) {
  if (!jobID) return;
  navigate(`${APP_PATHS.job(jobID)}?tab=job_history`);
}
