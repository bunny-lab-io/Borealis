import React, { memo, useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import ReactFlow, { Background, Controls, MarkerType, useEdgesState, useNodesState } from "reactflow";
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

const WorkerNode = memo(({ data }) => {
  const tone = statusTone(data.status);
  const opacity = tone === "stopped" || tone === "failed" ? 0.72 : 1;
  return (
    <Box
      sx={{
        width: 260,
        minHeight: 112,
        borderRadius: 2,
        border: `1px solid ${tone === "failed" ? "rgba(251,113,133,0.5)" : COLORS.border}`,
        background: COLORS.panel,
        boxShadow: "0 18px 44px rgba(2,8,23,0.34)",
        p: 1.4,
        opacity,
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: 0.9 }}>
        <StatusIcon tone={tone} />
        <Box sx={{ minWidth: 0 }}>
          <Tooltip title={data.label || ""} placement="top-start">
            <Typography sx={{ color: COLORS.text, fontSize: "0.86rem", fontWeight: 700, lineHeight: 1.2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 210 }}>
              {data.label}
            </Typography>
          </Tooltip>
          <Typography sx={{ color: COLORS.muted, fontSize: "0.72rem", lineHeight: 1.25, mt: 0.25 }}>
            {data.detail}
          </Typography>
        </Box>
      </Box>
      <Box sx={{ mt: 1.1, display: "flex", flexWrap: "wrap", gap: 0.7 }}>
        {data.badges.map((badge) => (
          <Box key={badge} sx={{ borderRadius: 999, border: "1px solid rgba(148,163,184,0.2)", px: 0.85, py: 0.35, color: COLORS.muted, fontSize: "0.68rem", lineHeight: 1 }}>
            {badge}
          </Box>
        ))}
      </Box>
    </Box>
  );
});

const TaskNode = memo(({ data }) => {
  const tone = statusTone(data.status);
  const canOpen = Boolean(data.path);
  return (
    <Tooltip title={canOpen ? "Open task" : data.error || ""} placement="top">
      <Box
        onClick={canOpen ? data.onOpen : undefined}
        sx={{
          width: 248,
          minHeight: 94,
          borderRadius: 2,
          border: `1px solid ${
            tone === "success" ? "rgba(52,211,153,0.46)" : tone === "failed" ? "rgba(251,113,133,0.52)" : COLORS.border
          }`,
          background: "rgba(10,16,31,0.94)",
          p: 1.25,
          cursor: canOpen ? "pointer" : "default",
          boxShadow: "0 14px 34px rgba(2,8,23,0.28)",
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.85 }}>
          <StatusIcon tone={tone} />
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={{ color: COLORS.text, fontSize: "0.82rem", fontWeight: 700, lineHeight: 1.2, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 195 }}>
              {data.label}
            </Typography>
            <Typography sx={{ color: COLORS.muted, fontSize: "0.7rem", mt: 0.25 }}>
              {data.detail}
            </Typography>
          </Box>
        </Box>
        {data.error ? (
          <Typography sx={{ color: COLORS.red, fontSize: "0.68rem", mt: 0.8, lineHeight: 1.25, maxHeight: 34, overflow: "hidden" }}>
            {data.error}
          </Typography>
        ) : null}
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
      position: { x: index * 330, y: 40 },
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
        position: { x: workerNodes.length * 330, y: 40 },
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
        position: { x: workerNode.position.x + 36, y: 210 + taskIndex * 128 },
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
      edges.push({
        id: `edge:${workerNode.id}:${nodeId}`,
        source: workerNode.id,
        target: nodeId,
        animated: tone === "running" || tone === "queued",
        type: "smoothstep",
        markerEnd: { type: MarkerType.ArrowClosed, color: tone === "success" ? COLORS.green : tone === "failed" ? COLORS.red : COLORS.blue },
        style: {
          stroke: tone === "success" ? COLORS.green : tone === "failed" ? COLORS.red : COLORS.blue,
          strokeWidth: 2,
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
          <Background color="rgba(148,163,184,0.18)" gap={24} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </Box>
    </Box>
  );
}
