import React, { memo, useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import ReactFlow, { Background, Controls, Handle, MarkerType, Position, useEdgesState, useNodesState } from "reactflow";
import "reactflow/dist/style.css";

import { Alert, Box, CircularProgress, Tooltip, Typography } from "@mui/material";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import ErrorRoundedIcon from "@mui/icons-material/ErrorRounded";
import HourglassEmptyRoundedIcon from "@mui/icons-material/HourglassEmptyRounded";
import RadioButtonCheckedRoundedIcon from "@mui/icons-material/RadioButtonCheckedRounded";
import ScheduleRoundedIcon from "@mui/icons-material/ScheduleRounded";
import StopCircleRoundedIcon from "@mui/icons-material/StopCircleRounded";

const WORKER_HISTORY_SECONDS = 600;
const POLL_INTERVAL_MS = 3000;

const COLORS = {
  bg: "#070b1a",
  panel: "rgba(8,12,24,0.92)",
  border: "rgba(148,163,184,0.28)",
  text: "#e2e8f0",
  muted: "#94a3b8",
  blue: "#7dd3fc",
  green: "#34d399",
  red: "#fb7185",
  amber: "#fbbf24",
  gray: "#94a3b8",
};

const NODE_WIDTH = 292;
const NODE_HEADER_HEIGHT = 42;
const NODE_BODY_HEIGHT = 96;
const NODE_ROW_GAP = 188;
const TASK_COLUMN_GAP = 348;
const WORKER_COLUMN_X = 48;
const WORKER_START_Y = 68;

const TONE_COLORS = {
  success: { accent: COLORS.green, border: "rgba(52,211,153,0.46)", bg: "rgba(6,78,59,0.2)", text: "#bbf7d0" },
  failed: { accent: COLORS.red, border: "rgba(251,113,133,0.52)", bg: "rgba(127,29,29,0.22)", text: "#fecdd3" },
  running: { accent: COLORS.blue, border: "rgba(125,211,252,0.5)", bg: "rgba(14,116,144,0.2)", text: "#bae6fd" },
  queued: { accent: COLORS.amber, border: "rgba(251,191,36,0.5)", bg: "rgba(120,53,15,0.2)", text: "#fde68a" },
  stopped: { accent: COLORS.gray, border: "rgba(148,163,184,0.3)", bg: "rgba(30,41,59,0.22)", text: "#cbd5e1" },
  idle: { accent: COLORS.green, border: "rgba(52,211,153,0.34)", bg: "rgba(6,78,59,0.16)", text: "#bbf7d0" },
  unknown: { accent: COLORS.gray, border: "rgba(148,163,184,0.28)", bg: "rgba(30,41,59,0.2)", text: "#cbd5e1" },
};

function epochLabel(value) {
  const numberValue = Number(value || 0);
  if (!Number.isFinite(numberValue) || numberValue <= 0) return "not started";
  try {
    return new Date(numberValue * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  } catch {
    return "not started";
  }
}

function elapsedLabel(started, ended) {
  const start = Number(started || 0);
  if (!Number.isFinite(start) || start <= 0) return "";
  const end = Number(ended || Math.floor(Date.now() / 1000));
  const seconds = Math.max(0, Math.floor(end - start));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return `${minutes}m ${remainder}s`;
}

function titleCase(value) {
  return String(value || "")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

export function statusTone(status) {
  const normalized = String(status || "").toLowerCase();
  if (["succeeded", "success", "completed"].includes(normalized)) return "success";
  if (["failed", "lost", "error"].includes(normalized)) return "failed";
  if (["running", "starting"].includes(normalized)) return "running";
  if (["queued", "pending"].includes(normalized)) return "queued";
  if (["cancelled", "stopped"].includes(normalized)) return "stopped";
  if (normalized === "idle") return "idle";
  return "unknown";
}

function StatusIcon({ tone }) {
  if (tone === "success") return <CheckCircleRoundedIcon sx={{ fontSize: 18, color: COLORS.green }} />;
  if (tone === "failed") return <ErrorRoundedIcon sx={{ fontSize: 18, color: COLORS.red }} />;
  if (tone === "running") return <AutorenewRoundedIcon sx={{ fontSize: 18, color: COLORS.blue, animation: "siteWorkerSpin 1.1s linear infinite" }} />;
  if (tone === "queued") return <ScheduleRoundedIcon sx={{ fontSize: 18, color: COLORS.amber }} />;
  if (tone === "stopped") return <StopCircleRoundedIcon sx={{ fontSize: 18, color: COLORS.gray }} />;
  if (tone === "idle") return <RadioButtonCheckedRoundedIcon sx={{ fontSize: 18, color: COLORS.green }} />;
  return <HourglassEmptyRoundedIcon sx={{ fontSize: 18, color: COLORS.gray }} />;
}

function toneColor(tone) {
  return TONE_COLORS[tone] || TONE_COLORS.unknown;
}

function edgeColor(tone) {
  return toneColor(tone).accent;
}

function statusLabel(status) {
  return titleCase(status || "unknown");
}

function portHandleStyle(direction, tone) {
  const color = edgeColor(tone);
  const isInput = direction === "input";
  return {
    top: NODE_HEADER_HEIGHT + NODE_BODY_HEIGHT - 24,
    left: isInput ? 0 : "auto",
    right: isInput ? "auto" : 0,
    width: 14,
    height: 14,
    borderRadius: "50%",
    border: `2px solid ${color}`,
    background: "rgba(8,17,31,0.98)",
    boxShadow: `0 0 0 2px rgba(8,17,31,0.98), 0 0 0 1px ${color}66`,
    transform: isInput ? "translate(-50%, -50%)" : "translate(50%, -50%)",
    zIndex: 8,
  };
}

function PortLabel({ side, children }) {
  return (
    <Typography
      sx={{
        position: "absolute",
        bottom: 18,
        [side]: 18,
        color: "#dbeafe",
        fontSize: "0.72rem",
        fontWeight: 500,
        lineHeight: 1,
      }}
    >
      {children}
    </Typography>
  );
}

function WorkflowLikeCard({ data, children, outputLabel, inputLabel, onClick }) {
  const tone = statusTone(data.status);
  const colors = toneColor(tone);
  const terminal = tone === "stopped" || tone === "failed";
  return (
    <Box
      onClick={onClick}
      sx={{
        width: NODE_WIDTH,
        minHeight: NODE_HEADER_HEIGHT + NODE_BODY_HEIGHT,
        borderRadius: "10px",
        border: `1px solid ${colors.border}`,
        background:
          `radial-gradient(110% 120% at 0% 0%, ${colors.accent}22, transparent 52%), ` +
          "radial-gradient(110% 120% at 100% 0%, rgba(192,132,252,0.14), transparent 56%), " +
          "rgba(9,14,27,0.96)",
        color: COLORS.text,
        boxShadow: `0 0 0 1px ${colors.border} inset, 0 18px 36px rgba(2,8,23,0.34)`,
        opacity: terminal ? 0.68 : 1,
        position: "relative",
        overflow: "visible",
        cursor: onClick ? "pointer" : "default",
      }}
    >
      {inputLabel ? (
        <>
          <Handle id="input" type="target" position={Position.Left} className="borealis-handle" style={portHandleStyle("input", tone)} />
          <PortLabel side="left">{inputLabel}</PortLabel>
        </>
      ) : null}
      {outputLabel ? (
        <>
          <Handle id="output" type="source" position={Position.Right} className="borealis-handle" style={portHandleStyle("output", tone)} />
          <PortLabel side="right">{outputLabel}</PortLabel>
        </>
      ) : null}
      <Box
        className="borealis-node-header"
        sx={{
          height: NODE_HEADER_HEIGHT,
          px: 1.55,
          borderBottom: "1px solid rgba(148,163,184,0.12)",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 1,
          background: `linear-gradient(135deg, ${colors.accent}22 0%, rgba(15,23,42,0.35) 65%, rgba(15,23,42,0.55) 100%)`,
          borderTopLeftRadius: "10px",
          borderTopRightRadius: "10px",
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.9, minWidth: 0 }}>
          <StatusIcon tone={tone} />
          <Tooltip title={data.label || ""} placement="top-start">
            <Typography sx={{ color: "#f8fafc", fontSize: "0.88rem", fontWeight: 700, lineHeight: 1.15, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {data.label}
            </Typography>
          </Tooltip>
        </Box>
        <Box
          sx={{
            borderRadius: 999,
            border: `1px solid ${colors.border}`,
            bgcolor: colors.bg,
            color: colors.text,
            px: 0.85,
            py: 0.25,
            fontSize: "0.62rem",
            fontWeight: 700,
            whiteSpace: "nowrap",
          }}
        >
          {statusLabel(data.status)}
        </Box>
      </Box>
      <Box sx={{ height: NODE_BODY_HEIGHT, px: 1.4, py: 1.2, position: "relative" }}>
        {children}
      </Box>
    </Box>
  );
}

const WorkerNode = memo(({ data }) => {
  return (
    <WorkflowLikeCard data={data} outputLabel="Work">
      <Box sx={{ minWidth: 0 }}>
        <Typography sx={{ color: COLORS.muted, fontSize: "0.74rem", lineHeight: 1.25 }}>
          {data.detail}
        </Typography>
      </Box>
      <Box sx={{ mt: 1.05, display: "flex", flexWrap: "wrap", gap: 0.65, pr: 4 }}>
        {data.badges.map((badge) => (
          <Box key={badge} sx={{ borderRadius: 999, border: "1px solid rgba(148,163,184,0.24)", background: "rgba(15,23,42,0.42)", px: 0.85, py: 0.32, color: COLORS.muted, fontSize: "0.67rem", lineHeight: 1 }}>
            {badge}
          </Box>
        ))}
      </Box>
    </WorkflowLikeCard>
  );
});

const TaskNode = memo(({ data }) => {
  const canOpen = Boolean(data.path);
  return (
    <Tooltip title={canOpen ? "Open task" : data.error || ""} placement="top">
      <Box>
        <WorkflowLikeCard data={data} inputLabel="Claim" outputLabel="Result" onClick={canOpen ? data.onOpen : undefined}>
          <Box sx={{ minWidth: 0, pr: 4 }}>
            <Typography sx={{ color: COLORS.muted, fontSize: "0.72rem", lineHeight: 1.25 }}>
              {data.detail}
            </Typography>
            {data.error ? (
              <Typography sx={{ color: COLORS.red, fontSize: "0.68rem", mt: 0.8, lineHeight: 1.25, maxHeight: 34, overflow: "hidden" }}>
                {data.error}
              </Typography>
            ) : null}
          </Box>
        </WorkflowLikeCard>
      </Box>
    </Tooltip>
  );
});

const nodeTypes = {
  worker: WorkerNode,
  task: TaskNode,
};

export function buildGraph(payload, navigate) {
  const workers = Array.isArray(payload?.workers) ? payload.workers : [];
  const recentWork = Array.isArray(payload?.recent_work) ? payload.recent_work : Array.isArray(payload?.active_work) ? payload.active_work : [];
  const activeWorkers = workers.filter((worker) => {
    const status = String(worker?.status || "").toLowerCase();
    return ["starting", "running", "idle", "stopped", "lost"].includes(status);
  });
  const workerNodes = activeWorkers.map((worker, index) => {
    const siteId = Number(worker?.site_id || 0);
    const isManager = siteId <= 0;
    const lanes = Array.isArray(worker?.current_lanes) ? worker.current_lanes.filter(Boolean) : [];
    const badges = [
      isManager ? "manager" : `site ${siteId}`,
      lanes.length ? lanes.join(", ") : "idle",
      `claimed ${Number(worker?.claimed_count || 0)}`,
    ];
    return {
      id: `worker:${worker?.worker_guid || worker?.container_name || index}`,
      type: "worker",
      position: { x: WORKER_COLUMN_X, y: WORKER_START_Y + index * NODE_ROW_GAP },
      data: {
        label: worker?.container_name || worker?.worker_guid || (isManager ? "Job Scheduler" : `Site Worker ${siteId}`),
        detail: `${titleCase(worker?.status || "unknown")} · started ${epochLabel(worker?.started_at)}`,
        status: worker?.status || "unknown",
        badges,
      },
      draggable: false,
    };
  });

  const knownWorkerIds = new Set(workerNodes.map((node) => node.id));
  const placeholderBySite = new Map();
  const workerIdForWork = (work) => {
    const workerGuid = String(work?.worker_guid || work?.lease_owner || "").trim();
    const directId = workerGuid ? `worker:${workerGuid}` : "";
    if (directId && knownWorkerIds.has(directId)) return directId;
    const siteId = Number(work?.site_id || 0);
    const key = siteId > 0 ? `site:${siteId}` : "manager";
    if (!placeholderBySite.has(key)) {
      const placeholderId = `placeholder:${key}`;
      placeholderBySite.set(key, placeholderId);
      workerNodes.push({
        id: placeholderId,
        type: "worker",
        position: { x: WORKER_COLUMN_X, y: WORKER_START_Y + workerNodes.length * NODE_ROW_GAP },
        data: {
          label: siteId > 0 ? `Queued Site ${siteId}` : "Queued Manager Work",
          detail: "waiting for worker claim",
          status: "queued",
          badges: [siteId > 0 ? `site ${siteId}` : "manager", "queued"],
        },
        draggable: false,
      });
    }
    return placeholderBySite.get(key);
  };

  const tasksByWorker = new Map();
  recentWork.forEach((work) => {
    const workerId = workerIdForWork(work);
    if (!tasksByWorker.has(workerId)) tasksByWorker.set(workerId, []);
    tasksByWorker.get(workerId).push(work);
  });

  const taskNodes = [];
  const edges = [];
  workerNodes.forEach((workerNode, index) => {
    workerNode.position = { x: WORKER_COLUMN_X, y: WORKER_START_Y + index * NODE_ROW_GAP };
  });
  workerNodes.forEach((workerNode) => {
    const tasks = tasksByWorker.get(workerNode.id) || [];
    tasks.forEach((work, taskIndex) => {
      const taskLink = work?.task_link || {};
      const label = taskLink?.label || titleCase(work?.kind || "work item");
      const detailParts = [
        titleCase(work?.status || "unknown"),
        work?.lane ? titleCase(work.lane) : "",
        elapsedLabel(work?.started_at, work?.finished_at),
      ].filter(Boolean);
      const nodeId = `task:${work?.id}`;
      taskNodes.push({
        id: nodeId,
        type: "task",
        position: { x: workerNode.position.x + TASK_COLUMN_GAP * (taskIndex + 1), y: workerNode.position.y },
        data: {
          label,
          detail: detailParts.join(" · "),
          status: work?.status || "unknown",
          error: work?.error || "",
          path: taskLink?.path || "",
          onOpen: taskLink?.path ? () => navigate(String(taskLink.path)) : undefined,
        },
        draggable: false,
      });
      const tone = statusTone(work?.status);
      const color = edgeColor(tone);
      edges.push({
        id: `edge:${workerNode.id}:${nodeId}`,
        source: workerNode.id,
        target: nodeId,
        sourceHandle: "output",
        targetHandle: "input",
        animated: tone === "running" || tone === "queued",
        type: "bezier",
        label: statusLabel(work?.status),
        labelStyle: {
          fill: color,
          fontSize: 12,
          fontWeight: 700,
        },
        labelBgStyle: {
          fill: "rgba(8,17,31,0.94)",
          stroke: color,
          strokeWidth: 1,
        },
        labelBgPadding: [10, 5],
        labelBgBorderRadius: 999,
        markerEnd: { type: MarkerType.ArrowClosed, color },
        style: {
          stroke: color,
          strokeWidth: 2,
          strokeDasharray: "6 3",
        },
      });
    });
  });

  return { nodes: [...workerNodes, ...taskNodes], edges };
}

export default function ActiveSiteWorkersCanvas({ active }) {
  const navigate = useNavigate();
  const [payload, setPayload] = useState({ workers: [], recent_work: [] });
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  const fetchWorkers = useCallback(async () => {
    try {
      setLoading((current) => current || !loaded);
      const response = await fetch(`/api/server/workers?history_seconds=${WORKER_HISTORY_SECONDS}`, {
        credentials: "include",
        cache: "no-store",
      });
      const nextPayload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(nextPayload?.error || nextPayload?.message || `Worker lookup failed (HTTP ${response.status}).`);
      }
      setPayload(nextPayload);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load site workers.");
    } finally {
      setLoaded(true);
      setLoading(false);
    }
  }, [loaded]);

  useEffect(() => {
    if (!active) return undefined;
    void fetchWorkers();
    const timer = setInterval(() => void fetchWorkers(), POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [active, fetchWorkers]);

  const graph = useMemo(() => buildGraph(payload, navigate), [payload, navigate]);

  useEffect(() => {
    setNodes(graph.nodes);
    setEdges(graph.edges);
  }, [graph, setEdges, setNodes]);

  const empty = !loading && !error && graph.nodes.length === 0;

  return (
    <Box sx={{ position: "relative", display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
      <Box
        sx={{
          px: 2,
          py: 1.2,
          borderBottom: "1px solid rgba(148,163,184,0.18)",
          display: "flex",
          alignItems: "center",
          gap: 1,
          color: COLORS.muted,
          minHeight: 48,
        }}
      >
        {loading ? <CircularProgress size={16} sx={{ color: COLORS.blue }} /> : <RadioButtonCheckedRoundedIcon sx={{ fontSize: 16, color: COLORS.green }} />}
        <Typography sx={{ color: COLORS.text, fontSize: "0.84rem", fontWeight: 700 }}>
          Active Site Workers
        </Typography>
        <Typography sx={{ color: COLORS.muted, fontSize: "0.76rem" }}>
          {Number(payload?.active_count || 0)} active · {Array.isArray(payload?.recent_work) ? payload.recent_work.length : 0} recent tasks
        </Typography>
      </Box>
      {error ? (
        <Alert severity="error" sx={{ mx: 2, mt: 2 }}>
          {error}
        </Alert>
      ) : null}
      <Box
        sx={{
          flexGrow: 1,
          minHeight: 420,
          position: "relative",
          background: "radial-gradient(100% 100% at 0% 0%, rgba(76,186,255,0.12), transparent 55%), #050816",
          "@keyframes siteWorkerSpin": {
            from: { transform: "rotate(0deg)" },
            to: { transform: "rotate(360deg)" },
          },
        }}
      >
        {empty ? (
          <Box sx={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", zIndex: 2 }}>
            <Typography sx={{ color: COLORS.muted, fontSize: "0.92rem" }}>No active or recent site worker activity.</Typography>
          </Box>
        ) : null}
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          fitView
          fitViewOptions={{ padding: 0.18 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
          proOptions={{ hideAttribution: true }}
        >
          <Background variant="lines" color="rgba(255,255,255,0.2)" gap={65} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </Box>
    </Box>
  );
}
