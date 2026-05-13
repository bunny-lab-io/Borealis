import React, { memo, useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import ReactFlow, { Background, Handle, MarkerType, Position, useEdgesState, useNodesState } from "reactflow";
import "reactflow/dist/style.css";

import { Alert, Box, Checkbox, CircularProgress, FormControlLabel, Tooltip, Typography } from "@mui/material";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import ErrorRoundedIcon from "@mui/icons-material/ErrorRounded";
import HourglassEmptyRoundedIcon from "@mui/icons-material/HourglassEmptyRounded";
import RadioButtonCheckedRoundedIcon from "@mui/icons-material/RadioButtonCheckedRounded";
import ScheduleRoundedIcon from "@mui/icons-material/ScheduleRounded";
import StopCircleRoundedIcon from "@mui/icons-material/StopCircleRounded";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";

const WORKER_HISTORY_SECONDS = 60;
const WORKER_CLOSE_SECONDS = 60;
const TASK_CLOSE_SECONDS = 30;
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
const NODE_HEADER_HEIGHT = 34;
const NODE_BODY_HEIGHT = 54;
const NODE_ROW_GAP = 128;
const TASK_ROW_GAP = 108;
const MANAGER_COLUMN_X = 64;
const WORKER_COLUMN_X = 440;
const TASK_COLUMN_X = 820;
const PYRAMID_CENTER_Y = 330;

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
    const date = new Date(numberValue * 1000);
    const month = String(date.getMonth() + 1).padStart(2, "0");
    const day = String(date.getDate()).padStart(2, "0");
    const year = date.getFullYear();
    const hours24 = date.getHours();
    const hours = hours24 % 12 || 12;
    const minutes = String(date.getMinutes()).padStart(2, "0");
    const suffix = hours24 >= 12 ? "PM" : "AM";
    return `${month}-${day}-${year} @ ${hours}:${minutes}${suffix}`;
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

function displayStatusLabel(data) {
  const tone = statusTone(data?.status);
  if (tone === "idle" && !data?.isManager && data?.idleRemainingSeconds != null) {
    const remaining = Number(data?.idleRemainingSeconds ?? 0);
    return `Idle - Teardown in ${Math.max(0, Math.ceil(remaining))}s`;
  }
  if (!data?.isTask && !data?.isManager && ["failed", "stopped"].includes(tone) && data?.closeRemainingSeconds != null) {
    const remaining = Number(data.closeRemainingSeconds || 0);
    return `${statusLabel(data?.status)} - Closing in ${Math.max(0, Math.ceil(remaining))}s`;
  }
  if (data?.isTask && ["success", "failed", "stopped"].includes(tone) && data?.closeRemainingSeconds != null) {
    const remaining = Number(data.closeRemainingSeconds || 0);
    return `${statusLabel(data?.status)} - Closing in ${Math.max(0, Math.ceil(remaining))}s`;
  }
  return statusLabel(data?.status);
}

function taskLabel() {
  return "Task";
}

function targetCountLabel(targetCount) {
  const count = Number(targetCount || 0);
  if (count <= 0) return "";
  return `${count} ${count === 1 ? "Device" : "Devices"} Targeted`;
}

function taskTypeLabel(work) {
  const fromPayload = String(work?.task_type || "").trim();
  if (fromPayload) return fromPayload;
  const kind = String(work?.kind || "").toLowerCase();
  if (kind === "onboarding_run") return "Onboarding";
  if (kind === "scheduled_workflow_run") return "Workflow";
  if (kind === "scheduled_run") return "Assembly";
  return titleCase(kind || "Task");
}

function portHandleStyle(direction, tone) {
  const color = edgeColor(tone);
  const isInput = direction === "input";
  return {
    top: (NODE_HEADER_HEIGHT + NODE_BODY_HEIGHT) / 2,
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

function WorkflowLikeCard({ data, children, hasOutput = false, hasInput = false, onClick }) {
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
      {hasInput ? (
        <Handle id="input" type="target" position={Position.Left} className="borealis-handle" style={portHandleStyle("input", tone)} />
      ) : null}
      {hasOutput ? (
        <Handle id="output" type="source" position={Position.Right} className="borealis-handle" style={portHandleStyle("output", tone)} />
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
            <Typography sx={{ color: "#f8fafc", fontSize: "0.82rem", fontWeight: 700, lineHeight: 1.15, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
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
          {displayStatusLabel(data)}
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
    <WorkflowLikeCard data={data} hasInput={!data.isManager} hasOutput>
      <Box sx={{ minWidth: 0, pr: 4 }}>
        <Typography sx={{ color: COLORS.muted, fontSize: "0.7rem", lineHeight: 1.15 }}>
          Started: {data.startedLabel}
        </Typography>
        {data.isManager ? null : (
          <Box sx={{ mt: 0.55, display: "flex", flexWrap: "wrap", gap: 0.65 }}>
            <Box sx={{ borderRadius: 999, border: "1px solid rgba(148,163,184,0.24)", background: "rgba(15,23,42,0.42)", px: 0.85, py: 0.32, color: COLORS.muted, fontSize: "0.67rem", lineHeight: 1 }}>
              {data.siteLabel}
            </Box>
          </Box>
        )}
      </Box>
    </WorkflowLikeCard>
  );
});

const TaskNode = memo(({ data }) => {
  const canOpen = Boolean(data.path);
  return (
    <Tooltip title={data.error || ""} placement="top">
      <Box>
        <WorkflowLikeCard data={data} hasInput>
          <Box sx={{ minWidth: 0, pr: 4 }}>
            <Typography sx={{ color: COLORS.muted, fontSize: "0.7rem", lineHeight: 1.15 }}>
              {data.taskType || "Task"} · Duration: {data.duration || "pending"}
            </Typography>
            {data.targetCountLabel ? (
              <Typography sx={{ mt: 0.35, color: COLORS.muted, fontSize: "0.68rem", lineHeight: 1.1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {data.targetCountLabel}
              </Typography>
            ) : null}
            {canOpen ? (
              <Typography
                component="button"
                type="button"
                onClick={data.onOpen}
                sx={{
                  mt: 0.35,
                  p: 0,
                  border: 0,
                  background: "transparent",
                  color: COLORS.blue,
                  cursor: "pointer",
                  fontSize: "0.69rem",
                  fontWeight: 700,
                  lineHeight: 1.1,
                  textDecoration: "none",
                  maxWidth: "100%",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  display: "block",
                  textAlign: "left",
                  "&:hover": {
                    color: "#bae6fd",
                    textDecoration: "none",
                  },
                }}
              >
                {data.taskName}
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

function distributeY(count, centerY, gap) {
  if (count <= 1) return [centerY];
  const start = centerY - ((count - 1) * gap) / 2;
  return Array.from({ length: count }, (_value, index) => start + index * gap);
}

function siteNameFor(payload, siteId) {
  const normalizedSiteId = Number(siteId || 0);
  if (normalizedSiteId <= 0) return "";
  const siteNames = payload?.site_names || {};
  return String(siteNames[String(normalizedSiteId)] || siteNames[normalizedSiteId] || "").trim() || `Site ${normalizedSiteId}`;
}

function workerNodeId(worker, index) {
  return `worker:${worker?.worker_guid || worker?.container_name || index}`;
}

function workCacheKey(work) {
  return [
    work?.id || "",
    work?.worker_guid || work?.lease_owner || "",
    work?.kind || "work",
    work?.job_id || "",
    work?.run_id || "",
    work?.status || "",
  ].join(":");
}

function mergeWorkerPayload(currentPayload, nextPayload, holdInactive) {
  if (!holdInactive) return nextPayload;
  const nextWorkers = Array.isArray(nextPayload?.workers) ? nextPayload.workers : [];
  const currentWorkers = Array.isArray(currentPayload?.workers) ? currentPayload.workers : [];
  const workerMap = new Map();
  currentWorkers.forEach((worker, index) => workerMap.set(workerNodeId(worker, index), worker));
  nextWorkers.forEach((worker, index) => workerMap.set(workerNodeId(worker, index), worker));

  const nextWork = Array.isArray(nextPayload?.recent_work) ? nextPayload.recent_work : [];
  const currentWork = Array.isArray(currentPayload?.recent_work) ? currentPayload.recent_work : [];
  const workMap = new Map();
  currentWork.forEach((work) => workMap.set(workCacheKey(work), work));
  nextWork.forEach((work) => workMap.set(workCacheKey(work), work));

  return {
    ...currentPayload,
    ...nextPayload,
    workers: Array.from(workerMap.values()),
    recent_work: Array.from(workMap.values()),
  };
}

export function buildGraph(payload, navigate) {
  const nowSeconds = Math.floor(Date.now() / 1000);
  return buildGraphAt(payload, navigate, nowSeconds);
}

function terminalAgeSeconds(item, nowSeconds, fallbackSince) {
  if (Number(fallbackSince || 0) > 0) return Math.max(0, nowSeconds - Number(fallbackSince));
  const terminalAt = Number(item?.stopped_at || item?.finished_at || item?.updated_at || item?.last_seen_at || item?.started_at || 0);
  if (terminalAt > 0) return Math.max(0, nowSeconds - terminalAt);
  return 0;
}

export function buildGraphAt(payload, navigate, nowSeconds, options = {}) {
  const dismissInactive = options?.dismissInactive !== false;
  const dismissStartedAt = Number(options?.dismissStartedAt || 0);
  const workers = Array.isArray(payload?.workers) ? payload.workers : [];
  const recentWorkRaw = Array.isArray(payload?.recent_work) ? payload.recent_work : Array.isArray(payload?.active_work) ? payload.active_work : [];
  const idleTtlSeconds = Math.max(30, Number(payload?.worker_idle_ttl_seconds || 60));
  const visibleWorkers = workers.filter((worker) => {
    const status = String(worker?.status || "").toLowerCase();
    if (["starting", "running", "idle"].includes(status)) return true;
    if (!["stopped", "lost"].includes(status)) return false;
    if (!dismissInactive) return true;
    if (dismissStartedAt <= 0) return false;
    return terminalAgeSeconds(worker, nowSeconds, dismissStartedAt) < WORKER_CLOSE_SECONDS;
  });

  const managerWorker = visibleWorkers.find((worker) => Number(worker?.site_id || 0) <= 0) || {
    worker_guid: "job-scheduler",
    container_name: "borealis-engine-job-scheduler",
    site_id: 0,
    status: "running",
    started_at: 0,
    current_lanes: ["scheduled_tick", "worker_reconcile"],
    claimed_count: 0,
  };
  const managerNodeId = workerNodeId(managerWorker, 0);
  const siteWorkers = visibleWorkers.filter((worker) => Number(worker?.site_id || 0) > 0);
  const nodeIdByGuid = new Map();
  const nodeIdBySite = new Map();
  siteWorkers.forEach((worker, index) => {
    const nodeId = workerNodeId(worker, index + 1);
    const guid = String(worker?.worker_guid || "").trim();
    const leaseOwner = String(worker?.lease_owner || "").trim();
    if (guid) nodeIdByGuid.set(guid, nodeId);
    if (leaseOwner) nodeIdByGuid.set(leaseOwner, nodeId);
    const siteId = Number(worker?.site_id || 0);
    if (siteId > 0 && !nodeIdBySite.has(siteId)) nodeIdBySite.set(siteId, nodeId);
  });

  const workerRows = [...siteWorkers];
  const placeholderBySite = new Map();
  const anchorForWork = (work) => {
    const workerGuid = String(work?.worker_guid || work?.lease_owner || "").trim();
    if (workerGuid && nodeIdByGuid.has(workerGuid)) return nodeIdByGuid.get(workerGuid);
    const siteId = Number(work?.site_id || 0);
    if (siteId > 0 && nodeIdBySite.has(siteId)) return nodeIdBySite.get(siteId);
    if (siteId <= 0) return managerNodeId;
    const tone = statusTone(work?.status);
    if (dismissInactive && ["success", "failed", "stopped"].includes(tone)) return null;
    if (!placeholderBySite.has(siteId)) {
      const placeholder = {
        worker_guid: `queued-site-${siteId}`,
        container_name: "Site Worker",
        site_id: siteId,
        site_name: siteNameFor(payload, siteId),
        status: "queued",
        started_at: 0,
        current_lanes: ["queued"],
        claimed_count: 0,
      };
      const nodeId = workerNodeId(placeholder, workerRows.length + 1);
      placeholderBySite.set(siteId, nodeId);
      nodeIdBySite.set(siteId, nodeId);
      workerRows.push(placeholder);
    }
    return placeholderBySite.get(siteId);
  };

  const groupedWork = new Map();
  recentWorkRaw.forEach((work) => {
    const tone = statusTone(work?.status);
    if (
      dismissInactive &&
      ["success", "failed", "stopped"].includes(tone) &&
      terminalAgeSeconds(work, nowSeconds, dismissStartedAt) >= TASK_CLOSE_SECONDS
    ) {
      return;
    }
    const anchorId = anchorForWork(work);
    if (!anchorId) return;
    const taskLink = work?.task_link || {};
    const jobPart = work?.job_id || taskLink?.job_id || work?.run_id || work?.id;
    const stableKey = [
      anchorId,
      work?.kind || "work",
      jobPart || work?.id,
      taskLink?.path || taskLink?.label || "",
      work?.status || "",
    ].join(":");
    const existing = groupedWork.get(stableKey);
    if (!existing) {
      groupedWork.set(stableKey, {
        anchorId,
        work: { ...work },
        count: 1,
        targetCount: Number(work?.target_count || 0),
        started_at: work?.started_at,
        finished_at: work?.finished_at,
      });
      return;
    }
    existing.count += 1;
    existing.targetCount = Math.max(Number(existing.targetCount || 0), Number(work?.target_count || 0));
    const existingFinished = Number(existing.work?.finished_at || existing.work?.updated_at || 0);
    const nextFinished = Number(work?.finished_at || work?.updated_at || 0);
    if (nextFinished >= existingFinished) {
      existing.work = { ...work };
    }
    existing.started_at = existing.started_at || work?.started_at;
    existing.finished_at = existing.work?.finished_at || existing.finished_at;
  });

  const tasksByWorker = new Map();
  groupedWork.forEach((entry) => {
    if (!tasksByWorker.has(entry.anchorId)) tasksByWorker.set(entry.anchorId, []);
    tasksByWorker.get(entry.anchorId).push(entry);
  });

  const workerY = distributeY(workerRows.length || 1, PYRAMID_CENTER_Y, NODE_ROW_GAP);
  const workerNodes = [];
  workerNodes.push({
    id: managerNodeId,
    type: "worker",
    position: { x: MANAGER_COLUMN_X, y: PYRAMID_CENTER_Y },
    data: {
      label: "Job Scheduler",
      startedLabel: epochLabel(managerWorker?.started_at),
      status: managerWorker?.status || "running",
      isManager: true,
      siteLabel: "manager",
    },
    draggable: false,
  });

  workerRows.forEach((worker, index) => {
    const siteId = Number(worker?.site_id || 0);
    const siteName = String(worker?.site_name || siteNameFor(payload, siteId)).trim();
    const id = workerNodeId(worker, index + 1);
    workerNodes.push({
      id,
      type: "worker",
      position: { x: WORKER_COLUMN_X, y: workerY[index] },
      data: {
        label: "Site Worker",
        startedLabel: epochLabel(worker?.started_at),
        status: worker?.status || "unknown",
        isManager: false,
        siteLabel: siteName || `Site ${siteId}`,
        idleRemainingSeconds:
          dismissInactive && statusTone(worker?.status) === "idle"
            ? idleTtlSeconds - Math.max(0, nowSeconds - Number(worker?.idle_since || nowSeconds))
            : null,
        closeRemainingSeconds:
          dismissInactive && ["failed", "stopped"].includes(statusTone(worker?.status))
            ? WORKER_CLOSE_SECONDS - terminalAgeSeconds(worker, nowSeconds, dismissStartedAt)
            : null,
      },
      draggable: false,
    });
  });

  const taskNodes = [];
  const edges = [];
  workerNodes
    .filter((node) => node.id !== managerNodeId)
    .forEach((workerNode) => {
      const tone = statusTone(workerNode.data.status);
      const color = edgeColor(tone === "stopped" ? "queued" : tone);
      edges.push({
        id: `edge:${managerNodeId}:${workerNode.id}`,
        source: managerNodeId,
        target: workerNode.id,
        sourceHandle: "output",
        targetHandle: "input",
        animated: tone === "running" || tone === "queued",
        type: "bezier",
        markerEnd: { type: MarkerType.ArrowClosed, color },
        style: {
          stroke: color,
          strokeWidth: 2,
          strokeDasharray: "6 3",
        },
      });
    });

  workerNodes.forEach((workerNode) => {
    const tasks = tasksByWorker.get(workerNode.id) || [];
    const taskY = distributeY(tasks.length || 1, workerNode.position.y, TASK_ROW_GAP);
    tasks.forEach((entry, taskIndex) => {
      const work = entry.work || {};
      const taskLink = work?.task_link || {};
      const label = taskLabel();
      const targets = targetCountLabel(entry.targetCount || work?.target_count);
      const taskNameBase = taskLink?.label || titleCase(work?.kind || "task");
      const taskName = taskNameBase;
      const duration = elapsedLabel(entry.started_at || work?.started_at, entry.finished_at || work?.finished_at);
      const tone = statusTone(work?.status);
      const terminalFinishedAt = Number(entry.finished_at || work?.finished_at || work?.updated_at || 0);
      const closeRemainingSeconds =
        dismissInactive && ["success", "failed", "stopped"].includes(tone) && terminalFinishedAt > 0
          ? TASK_CLOSE_SECONDS - terminalAgeSeconds(work, nowSeconds, dismissStartedAt)
          : null;
      const nodeId = `task:${workerNode.id}:${taskIndex}:${work?.id || work?.job_id || work?.run_id || "work"}`;
      taskNodes.push({
        id: nodeId,
        type: "task",
        position: { x: TASK_COLUMN_X, y: taskY[taskIndex] },
        data: {
          label,
          taskName,
          taskType: taskTypeLabel(work),
          targetCountLabel: targets,
          duration,
          status: work?.status || "unknown",
          isTask: true,
          closeRemainingSeconds,
          error: work?.error || "",
          path: taskLink?.path || "",
          onOpen: taskLink?.path ? () => navigate(String(taskLink.path)) : undefined,
        },
        draggable: false,
      });
      const color = edgeColor(tone);
      edges.push({
        id: `edge:${workerNode.id}:${nodeId}`,
        source: workerNode.id,
        target: nodeId,
        sourceHandle: "output",
        targetHandle: "input",
        animated: tone === "running" || tone === "queued",
        type: "bezier",
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

export function ActiveSiteWorkersCanvas({ active = true }) {
  const navigate = useNavigate();
  const [payload, setPayload] = useState({ workers: [], recent_work: [] });
  const [clockSeconds, setClockSeconds] = useState(() => Math.floor(Date.now() / 1000));
  const [dismissInactive, setDismissInactive] = useState(true);
  const [dismissStartedAt, setDismissStartedAt] = useState(0);
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
      setPayload((currentPayload) => mergeWorkerPayload(currentPayload, nextPayload, !dismissInactive));
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load site workers.");
    } finally {
      setLoaded(true);
      setLoading(false);
    }
  }, [dismissInactive, loaded]);

  useEffect(() => {
    if (!active) return undefined;
    void fetchWorkers();
    const timer = setInterval(() => void fetchWorkers(), POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [active, fetchWorkers]);

  useEffect(() => {
    if (!active) return undefined;
    const timer = setInterval(() => setClockSeconds(Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(timer);
  }, [active]);

  const handleDismissInactiveChange = useCallback((event) => {
    const checked = Boolean(event.target.checked);
    setDismissInactive(checked);
    if (checked) {
      setDismissStartedAt(Math.floor(Date.now() / 1000));
    }
  }, []);

  const graph = useMemo(
    () => buildGraphAt(payload, navigate, clockSeconds, { dismissInactive, dismissStartedAt }),
    [clockSeconds, dismissInactive, dismissStartedAt, navigate, payload]
  );

  useEffect(() => {
    setNodes(graph.nodes);
    setEdges(graph.edges);
  }, [graph, setEdges, setNodes]);

  const empty = !loading && !error && graph.nodes.length === 0;
  const taskNodeCount = graph.nodes.filter((node) => node.type === "task").length;

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
        <FormControlLabel
          control={
            <Checkbox
              checked={dismissInactive}
              onChange={handleDismissInactiveChange}
              size="small"
              sx={{
                color: "rgba(148,163,184,0.72)",
                py: 0,
                "&.Mui-checked": { color: COLORS.blue },
              }}
            />
          }
          label="Dismiss Inactive Workers"
          sx={{
            m: 0,
            mr: 1,
            color: COLORS.text,
            "& .MuiFormControlLabel-label": { fontSize: "0.76rem", fontWeight: 700 },
          }}
        />
        {loading ? <CircularProgress size={16} sx={{ color: COLORS.blue }} /> : <RadioButtonCheckedRoundedIcon sx={{ fontSize: 16, color: COLORS.green }} />}
        <Typography sx={{ color: COLORS.text, fontSize: "0.84rem", fontWeight: 700 }}>
          Active Site Workers
        </Typography>
        <Typography sx={{ color: COLORS.muted, fontSize: "0.76rem" }}>
          {Number(payload?.active_count || 0)} active · {taskNodeCount} recent task groups
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
          background:
            "linear-gradient(to bottom, rgba(9,44,68,0.9) 0%, rgba(30,30,30,0) 45%, rgba(30,30,30,0) 75%, rgba(9,44,68,0.7) 100%), #050816",
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
          className="flow-editor-container"
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
        >
          <Background variant="lines" color="rgba(255,255,255,0.2)" gap={65} size={1} />
        </ReactFlow>
      </Box>
    </Box>
  );
}

export default function SiteWorkers() {
  useRoutePageChrome({
    title: "Site Workers",
    subtitle: "Live site-worker containers, assigned workloads, terminal status, and teardown timing.",
    Icon: AccountTreeRoundedIcon,
    breadcrumbLabel: "Site Workers",
  });

  return (
    <PageBodyFrame variant="grid">
      <ActiveSiteWorkersCanvas active />
    </PageBodyFrame>
  );
}
