import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Paper,
  Tooltip,
  Typography,
} from "@mui/material";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import CachedIcon from "@mui/icons-material/Cached";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry, themeQuartz } from "ag-grid-community";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_BUTTON_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const WORKER_HISTORY_SECONDS = 300;
const POLL_INTERVAL_MS = 3000;
const BASE_ROW_HEIGHT = 56;
const WORKER_REMOVE_SECONDS = 30;
const AUTO_SIZE_COLUMNS = ["site_name", "container_id", "created_label", "connected_devices", "assigned_task_groups"];
const BOREALIS_LINK_COLOR = "#58a6ff";
const BOREALIS_LINK_HOVER_COLOR = "#7dd3fc";
const PLACEHOLDER_TEXT_COLOR = "rgba(148,163,184,0.52)";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const SITE_WORKER_GRID_THEME = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor",
  },
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#FFF",
  headerFontSize: 14,
});
const SITE_WORKER_GRID_THEME_CLASS = SITE_WORKER_GRID_THEME.themeName || "ag-theme-quartz";

const GRID_STYLE = {
  "--ag-background-color": "#070b1a",
  "--ag-foreground-color": "#f4f7ff",
  "--ag-header-background-color": "#0f172a",
  "--ag-header-foreground-color": "#cfe0ff",
  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "--ag-border-color": "rgba(125,183,255,0.18)",
  "--ag-row-border-color": "rgba(125,183,255,0.14)",
  "--ag-border-radius": "8px",
};

const GRID_SHELL_SX = {
  width: "100%",
  flexGrow: 1,
  minHeight: 0,
  height: "100%",
  position: "relative",
  overflow: "hidden",
  color: "#f4f7ff",
  fontFamily: gridFontFamily,
  "--ag-font-family": gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "& .ag-root-wrapper": {
    minHeight: "100%",
    border: "none",
    borderRadius: 0,
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-paging-panel, & .ag-center-cols-container": {
    background: "transparent",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
  "& .ag-header": {
    backgroundColor: "rgba(11,18,36,0.92)",
    borderBottom: "1px solid rgba(148,163,184,0.18)",
  },
  "& .ag-header-cell, & .ag-header-group-cell": {
    borderColor: "rgba(148,163,184,0.12)",
  },
  "& .ag-header-cell-label": {
    color: "#f4f7ff",
    fontWeight: 600,
    letterSpacing: 0,
  },
  "& .ag-header-cell-menu-button, & .ag-header-cell-filter-button": {
    opacity: 1,
    color: "#9fb4d4",
    transition: "color 160ms ease, background 160ms ease",
    borderRadius: 8,
  },
  "& .ag-header-cell-menu-button:hover, & .ag-header-cell-filter-button:hover": {
    color: "#7dd3fc",
    background: "rgba(125,211,252,0.08)",
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
    color: "#f4f7ff",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    padding: 0,
    color: "#f4f7ff",
  },
  "& .ag-cell-value": {
    color: "#f4f7ff !important",
    fontWeight: 500,
    width: "100%",
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
  },
  "& .ag-center-cols-container .ag-cell.center-col, & .ag-pinned-left-cols-container .ag-cell.center-col, & .ag-pinned-right-cols-container .ag-cell.center-col": {
    justifyContent: "center",
    textAlign: "center",
  },
  "& .ag-center-cols-container .ag-cell.center-col .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.center-col .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.center-col .ag-cell-wrapper": {
    justifyContent: "center",
  },
  "& .ag-center-cols-container .ag-cell.center-col .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.center-col .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.center-col .ag-cell-value": {
    display: "flex",
    justifyContent: "center",
  },
  "& .ag-row": {
    borderColor: "rgba(255,255,255,0.04)",
    transition: "background 160ms ease, box-shadow 160ms ease, transform 160ms ease",
  },
  "& .ag-row:nth-of-type(odd)": {
    backgroundColor: "rgba(15,23,42,0.16)",
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.34)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
    boxShadow: "inset 3px 0 0 rgba(125,211,252,0.45)",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-paging-panel": {
    borderTop: "1px solid rgba(148,163,184,0.2)",
    backgroundColor: "rgba(3,7,18,0.8)",
  },
};

const TONE_STYLES = {
  running: { border: "rgba(125,211,252,0.5)", bg: "rgba(14,116,144,0.2)", text: "#bae6fd" },
  idle: { border: "rgba(52,211,153,0.34)", bg: "rgba(6,78,59,0.16)", text: "#bbf7d0" },
  pending: { border: "rgba(251,191,36,0.5)", bg: "rgba(120,53,15,0.2)", text: "#fde68a" },
  failed: { border: "rgba(251,113,133,0.52)", bg: "rgba(127,29,29,0.22)", text: "#fecdd3" },
  stopped: { border: "rgba(148,163,184,0.3)", bg: "rgba(30,41,59,0.22)", text: "#cbd5e1" },
  no_online: { border: "rgba(148,163,184,0.28)", bg: "rgba(30,41,59,0.18)", text: "#94a3b8" },
  unknown: { border: "rgba(148,163,184,0.28)", bg: "rgba(30,41,59,0.2)", text: "#cbd5e1" },
};

const TASK_SECTIONS = [
  { key: "success", label: "Success", color: "#00d18c" },
  { key: "running", label: "Running", color: "#58a6ff" },
  { key: "pending", label: "Pending", color: "#999999" },
  { key: "failed", label: "Failed", color: "#ff4f4f" },
  { key: "skipped", label: "Skipped", color: "#f0c36d" },
];

const CONNECTION_SECTIONS = [
  { key: "connected", label: "Connected", color: "#00d18c" },
  { key: "disconnected", label: "Disconnected", color: "#f0c36d" },
  { key: "offline", label: "Offline", color: "#999999" },
];

function stopGridEvent(event) {
  event?.preventDefault?.();
  event?.stopPropagation?.();
}

function titleCase(value) {
  return String(value || "")
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

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

function durationLabel(seconds) {
  const value = Math.max(0, Math.ceil(Number(seconds || 0)));
  if (value >= 60) {
    const minutes = Math.floor(value / 60);
    const remainder = value % 60;
    return `${minutes}m${String(remainder).padStart(2, "0")}s`;
  }
  return `${value}s`;
}

function siteNameFor(payload, siteId) {
  const normalizedSiteId = Number(siteId || 0);
  if (normalizedSiteId <= 0) return "";
  const siteNames = payload?.site_names || {};
  return String(siteNames[String(normalizedSiteId)] || siteNames[normalizedSiteId] || "").trim() || `Site ${normalizedSiteId}`;
}

function connectedDeviceCount(worker) {
  const direct = [
    worker?.connected_device_count,
    worker?.connectedDeviceCount,
    worker?.registered_device_count,
    worker?.registeredDeviceCount,
  ]
    .map((value) => Number(value || 0))
    .find((value) => Number.isFinite(value) && value > 0);
  if (direct) return direct;
  const links = Array.isArray(worker?.task_links) ? worker.task_links : [];
  const link = links.find((item) => String(item?.kind || "").toLowerCase() === "agent_sockets");
  const count = Number(link?.count || link?.device_count || link?.target_count || 0);
  return Number.isFinite(count) && count > 0 ? count : 0;
}

function siteDeviceCount(payload, worker, siteId) {
  const direct = [
    worker?.site_device_count,
    worker?.siteDeviceCount,
    worker?.site_total_device_count,
    worker?.siteTotalDeviceCount,
  ]
    .map((value) => Number(value || 0))
    .find((value) => Number.isFinite(value) && value > 0);
  if (direct) return direct;
  const counts = payload?.site_device_counts || payload?.siteDeviceCounts || {};
  const mapped = Number(counts[String(siteId)] || counts[siteId] || 0);
  return Number.isFinite(mapped) && mapped > 0 ? mapped : 0;
}

function siteOnlineDeviceCount(payload, worker, siteId, connectedCount = 0) {
  const direct = [
    worker?.site_online_device_count,
    worker?.siteOnlineDeviceCount,
    worker?.online_device_count,
    worker?.onlineDeviceCount,
  ]
    .map((value) => Number(value || 0))
    .find((value) => Number.isFinite(value) && value > 0);
  if (direct) return direct;
  const counts = payload?.site_online_device_counts || payload?.siteOnlineDeviceCounts || {};
  const mapped = Number(counts[String(siteId)] || counts[siteId] || 0);
  if (Number.isFinite(mapped) && mapped > 0) return mapped;
  return Math.max(0, Number(connectedCount || 0));
}

function deviceConnectionBreakdown(payload, worker, siteId, connectedDevices, siteDevices) {
  const connected = Math.max(0, Number(connectedDevices || 0));
  const total = Math.max(0, Number(siteDevices || 0), connected);
  const online = Math.max(connected, Math.min(total, siteOnlineDeviceCount(payload, worker, siteId, connected)));
  const disconnected = Math.max(0, online - connected);
  const offline = Math.max(0, total - connected - disconnected);
  return {
    connected_devices: connected,
    site_device_count: total,
    site_online_device_count: online,
    disconnected_devices: disconnected,
    offline_devices: offline,
  };
}

function siteRecords(payload) {
  const sites = Array.isArray(payload?.sites) ? payload.sites : [];
  if (sites.length) {
    return sites
      .map((site) => {
        const siteId = Number(site?.id || site?.site_id || 0);
        if (!Number.isFinite(siteId) || siteId <= 0) return null;
        const name = String(site?.name || site?.site_name || "").trim() || `Site ${siteId}`;
        const deviceCount = Number(site?.device_count || site?.site_device_count || 0);
        const onlineDeviceCount = Number(site?.online_device_count || site?.site_online_device_count || 0);
        return {
          site_id: siteId,
          site_name: name,
          site_device_count: Number.isFinite(deviceCount) && deviceCount > 0 ? deviceCount : 0,
          site_online_device_count: Number.isFinite(onlineDeviceCount) && onlineDeviceCount > 0 ? onlineDeviceCount : 0,
        };
      })
      .filter(Boolean);
  }
  const names = payload?.site_names || {};
  const counts = payload?.site_device_counts || {};
  const onlineCounts = payload?.site_online_device_counts || {};
  return Object.entries(names)
    .map(([siteIdRaw, nameRaw]) => {
      const siteId = Number(siteIdRaw || 0);
      if (!Number.isFinite(siteId) || siteId <= 0) return null;
      const deviceCount = Number(counts[String(siteId)] || counts[siteId] || 0);
      const onlineDeviceCount = Number(onlineCounts[String(siteId)] || onlineCounts[siteId] || 0);
      return {
        site_id: siteId,
        site_name: String(nameRaw || "").trim() || `Site ${siteId}`,
        site_device_count: Number.isFinite(deviceCount) && deviceCount > 0 ? deviceCount : 0,
        site_online_device_count: Number.isFinite(onlineDeviceCount) && onlineDeviceCount > 0 ? onlineDeviceCount : 0,
      };
    })
    .filter(Boolean);
}

function statusSortRank(label, tone) {
  const normalizedLabel = String(label || "").trim().toLowerCase();
  const normalizedTone = String(tone || "").trim().toLowerCase();
  if (normalizedLabel === "running" || normalizedTone === "running") return 0;
  if (normalizedLabel === "starting" || normalizedTone === "pending") return 1;
  if (normalizedLabel === "no online devices" || normalizedTone === "no_online") return 2;
  if (normalizedLabel.includes("tearing down") || normalizedLabel === "idle" || normalizedTone === "idle") return 3;
  if (normalizedTone === "failed" || normalizedTone === "stopped") return 4;
  return 5;
}

function workStatusBucket(status) {
  const normalized = String(status || "").trim().toLowerCase();
  if (["succeeded", "success", "completed"].includes(normalized)) return "success";
  if (["running"].includes(normalized)) return "running";
  if (["queued", "pending", "reassigning"].includes(normalized)) return "pending";
  if (["cancelled", "canceled", "skipped", "stopped"].includes(normalized)) return "skipped";
  if (["failed", "lost", "error", "timed_out", "timeout"].includes(normalized)) return "failed";
  return "pending";
}

function statusRank(status) {
  const bucket = workStatusBucket(status);
  const ranks = { running: 0, pending: 1, failed: 2, success: 3, skipped: 4 };
  return ranks[bucket] ?? 5;
}

function emptyTaskCounts() {
  return {
    success: 0,
    running: 0,
    pending: 0,
    failed: 0,
    skipped: 0,
    total_tasks: 0,
  };
}

function terminalWorkerAgeSeconds(worker, nowSeconds) {
  const stoppedAt = Number(worker?.stopped_at || worker?.updated_at || worker?.last_seen_at || worker?.started_at || 0);
  if (stoppedAt > 0) return Math.max(0, Number(nowSeconds || 0) - stoppedAt);
  return 0;
}

function workerStartedSeconds(worker) {
  const startedAt = Number(worker?.started_at || 0);
  return Number.isFinite(startedAt) && startedAt > 0 ? startedAt : 0;
}

function workerVisibleKey(worker) {
  const workerGuid = String(worker?.worker_guid || "").trim();
  if (workerGuid) return `guid:${workerGuid}`;
  const containerName = String(worker?.container_name || "").trim();
  if (containerName) return `container:${containerName}`;
  const containerId = String(worker?.container_id || "").trim();
  if (containerId) return `docker:${containerId}`;
  return `site:${Number(worker?.site_id || 0)}:${workerStartedSeconds(worker)}:${String(worker?.status || "").trim()}`;
}

function isTerminalWorker(worker) {
  const normalized = String(worker?.status || "").trim().toLowerCase();
  return ["stopped", "lost"].includes(normalized);
}

function isActiveWorker(worker) {
  const normalized = String(worker?.status || "").trim().toLowerCase();
  return ["starting", "running", "idle"].includes(normalized);
}

function workIdentity(work) {
  const id = String(work?.id ?? "").trim();
  if (id) return `id:${id}`;
  return [
    "fallback",
    work?.kind || "work",
    work?.site_id || "",
    work?.job_id || "",
    work?.run_id || "",
    work?.target_id || "",
    work?.worker_guid || work?.lease_owner || "",
  ].join(":");
}

function jobIdForWork(work) {
  const taskLink = work?.task_link || {};
  const value = work?.job_id || taskLink?.job_id || "";
  const numberValue = Number(value || 0);
  return Number.isFinite(numberValue) && numberValue > 0 ? numberValue : null;
}

function jobPathForWork(work, jobId) {
  const taskLink = work?.task_link || {};
  const path = String(taskLink?.path || "").trim();
  if (path) return path;
  if (jobId) return `${APP_PATHS.job(jobId)}?tab=job_history`;
  return "";
}

function jobNameForWork(work) {
  const taskLink = work?.task_link || {};
  return String(work?.job_name || taskLink?.job_name || taskLink?.name || taskLink?.label || "").trim();
}

function buildTaskGroupsByWorker(payload) {
  const byWorker = new Map();
  const seen = new Set();
  const activeWork = Array.isArray(payload?.active_work) ? payload.active_work : [];
  const recentWork = Array.isArray(payload?.recent_work) ? payload.recent_work : [];
  [...activeWork, ...recentWork].forEach((work) => {
    const identity = workIdentity(work);
    if (seen.has(identity)) return;
    seen.add(identity);
    const keys = new Set(
      [work?.worker_guid, work?.lease_owner, work?.container_name]
        .map((value) => String(value || "").trim())
        .filter(Boolean)
    );
    if (!keys.size) return;
    const bucket = workStatusBucket(work?.status);
    const jobId = jobIdForWork(work);
    const jobName = jobNameForWork(work);
    const jobKey = jobId ? `job:${jobId}` : `work:${work?.kind || "task"}:${work?.id || identity}`;
    keys.forEach((key) => {
      const workerGroups = byWorker.get(key) || new Map();
      const group = workerGroups.get(jobKey) || {
        key: jobKey,
        job_id: jobId,
        job_name: jobName,
        label: jobId ? `Job ID: ${jobId}` : "Task",
        path: jobPathForWork(work, jobId),
        counts: emptyTaskCounts(),
        first_started_at: Number(work?.started_at || work?.created_at || 0),
        last_activity_at: Number(work?.finished_at || work?.heartbeat_at || work?.updated_at || work?.started_at || 0),
        rank: statusRank(work?.status),
      };
      const counts = group.counts;
      counts[bucket] = Number(counts[bucket] || 0) + 1;
      counts.total_tasks = Number(counts.total_tasks || 0) + 1;
      group.rank = Math.min(Number(group.rank ?? 5), statusRank(work?.status));
      group.first_started_at = Math.min(
        Number(group.first_started_at || work?.started_at || work?.created_at || 0),
        Number(work?.started_at || work?.created_at || group.first_started_at || 0)
      );
      group.last_activity_at = Math.max(
        Number(group.last_activity_at || 0),
        Number(work?.finished_at || work?.heartbeat_at || work?.updated_at || work?.started_at || 0)
      );
      if (!group.job_name && jobName) group.job_name = jobName;
      if (!group.path) group.path = jobPathForWork(work, jobId);
      workerGroups.set(jobKey, group);
      byWorker.set(key, workerGroups);
    });
  });
  const normalized = new Map();
  byWorker.forEach((groups, key) => {
    normalized.set(
      key,
      Array.from(groups.values()).sort((left, right) => {
        if (left.rank !== right.rank) return left.rank - right.rank;
        if (Number(left.job_id || 0) && Number(right.job_id || 0) && Number(left.job_id) !== Number(right.job_id)) {
          return Number(left.job_id) - Number(right.job_id);
        }
        return Number(right.last_activity_at || 0) - Number(left.last_activity_at || 0);
      })
    );
  });
  return normalized;
}

function workerStatusMeta(worker, nowSeconds, idleTtlSeconds, removalRemainingSeconds = null) {
  const normalized = String(worker?.status || "").trim().toLowerCase();
  const connected = connectedDeviceCount(worker);
  if (removalRemainingSeconds != null) {
    return { label: `Removing in ${durationLabel(removalRemainingSeconds)}`, tone: "stopped" };
  }
  if (connected > 0 && !["lost", "stopped", "failed"].includes(normalized)) {
    return { label: "Running", tone: "running" };
  }
  if (normalized === "idle") {
    const idleSince = Number(worker?.idle_since || nowSeconds);
    const remaining = Math.max(0, Math.ceil(Number(idleTtlSeconds || 300) - Math.max(0, nowSeconds - idleSince)));
    if (remaining > 0) {
      return { label: `Tearing Down in ${durationLabel(remaining)}`, tone: "idle" };
    }
    return { label: "Idle", tone: "idle" };
  }
  if (normalized === "running") return { label: "Running", tone: "running" };
  if (normalized === "starting") return { label: "Starting", tone: "pending" };
  if (normalized === "lost") return { label: "Lost", tone: "failed" };
  if (normalized === "stopped") return { label: "Stopped", tone: "stopped" };
  return { label: titleCase(normalized || "unknown"), tone: "unknown" };
}

function normalizeRows(payload, nowSeconds) {
  const workers = Array.isArray(payload?.workers) ? payload.workers : [];
  const taskGroupsByWorker = buildTaskGroupsByWorker(payload);
  const idleTtlSeconds = Math.max(300, Number(payload?.worker_idle_ttl_seconds || 300));
  const newestActiveBySite = new Map();
  workers.forEach((worker) => {
    const siteId = Number(worker?.site_id || 0);
    if (siteId <= 0 || !isActiveWorker(worker)) return;
    const startedAt = workerStartedSeconds(worker);
    const key = workerVisibleKey(worker);
    const existing = newestActiveBySite.get(siteId);
    if (!existing || startedAt > existing.startedAt || (startedAt === existing.startedAt && key > existing.key)) {
      newestActiveBySite.set(siteId, { key, startedAt });
    }
  });
  const newestTerminalBySite = new Map();
  workers.forEach((worker) => {
    const siteId = Number(worker?.site_id || 0);
    if (siteId <= 0 || newestActiveBySite.has(siteId) || !isTerminalWorker(worker)) return;
    if (terminalWorkerAgeSeconds(worker, nowSeconds) >= WORKER_REMOVE_SECONDS) return;
    const startedAt = workerStartedSeconds(worker);
    const key = workerVisibleKey(worker);
    const existing = newestTerminalBySite.get(siteId);
    if (!existing || startedAt > existing.startedAt || (startedAt === existing.startedAt && key > existing.key)) {
      newestTerminalBySite.set(siteId, { key, startedAt });
    }
  });
  const rowsBySite = new Map();
  workers
    .filter((worker) => Number(worker?.site_id || 0) > 0)
    .filter((worker) => {
      const siteId = Number(worker?.site_id || 0);
      const key = workerVisibleKey(worker);
      const active = newestActiveBySite.get(siteId);
      if (active) return key === active.key;
      if (!isTerminalWorker(worker)) return true;
      const terminal = newestTerminalBySite.get(siteId);
      return Boolean(terminal && key === terminal.key);
    })
    .map((worker, index) => {
      const siteId = Number(worker?.site_id || 0);
      const workerGuid = String(worker?.worker_guid || "").trim();
      const containerName = String(worker?.container_name || "").trim();
      const removalRemainingSeconds = isTerminalWorker(worker)
        ? WORKER_REMOVE_SECONDS - terminalWorkerAgeSeconds(worker, nowSeconds)
        : null;
      const status = workerStatusMeta(worker, nowSeconds, idleTtlSeconds, removalRemainingSeconds);
      const taskGroups =
        (workerGuid && taskGroupsByWorker.get(workerGuid)) ||
        (containerName && taskGroupsByWorker.get(containerName)) ||
        [];
      const containerId = String(worker?.container_id || "").trim();
      const connectedDevices = connectedDeviceCount(worker);
      const siteDevices = siteDeviceCount(payload, worker, siteId);
      const deviceCounts = deviceConnectionBreakdown(payload, worker, siteId, connectedDevices, siteDevices);
      const row = {
        id: `site-worker-site-${siteId}`,
        worker_instance_id: workerGuid || containerName || `site-worker-${siteId}-${index}`,
        worker_guid: workerGuid,
        site_id: siteId,
        site_name: String(worker?.site_name || siteNameFor(payload, siteId)).trim() || `Site ${siteId}`,
        container_name: containerName,
        container_id: containerId || "Unknown",
        container_id_full: String(worker?.container_id_full || "").trim(),
        created_label: epochLabel(worker?.started_at),
        status_label: status.label,
        status_tone: status.tone,
        status_sort: statusSortRank(status.label, status.tone),
        connected_devices: deviceCounts.connected_devices,
        site_device_count: deviceCounts.site_device_count,
        site_online_device_count: deviceCounts.site_online_device_count,
        disconnected_devices: deviceCounts.disconnected_devices,
        offline_devices: deviceCounts.offline_devices,
        assigned_task_groups: taskGroups,
        raw: worker,
      };
      rowsBySite.set(siteId, row);
    });
  siteRecords(payload).forEach((site) => {
    const siteId = Number(site?.site_id || 0);
    if (siteId <= 0 || rowsBySite.has(siteId)) return;
    const siteDevices = Math.max(0, Number(site?.site_device_count || 0));
    const onlineDevices = Math.max(0, Math.min(siteDevices, Number(site?.site_online_device_count || 0)));
    rowsBySite.set(siteId, {
      id: `site-worker-site-${siteId}`,
      worker_instance_id: `site-placeholder-${siteId}`,
      worker_guid: "",
      site_id: siteId,
      site_name: String(site?.site_name || "").trim() || `Site ${siteId}`,
      container_name: "",
      container_id: "N/A",
      container_id_full: "",
      created_label: "N/A",
      status_label: "No Online Devices",
      status_tone: "no_online",
      status_sort: statusSortRank("No Online Devices", "no_online"),
      connected_devices: 0,
      site_device_count: siteDevices,
      site_online_device_count: onlineDevices,
      disconnected_devices: onlineDevices,
      offline_devices: Math.max(0, siteDevices - onlineDevices),
      assigned_task_groups: [],
      raw: null,
    });
  });
  return Array.from(rowsBySite.values()).sort((left, right) => {
    const leftRank = Number(left.status_sort || 0);
    const rightRank = Number(right.status_sort || 0);
    if (leftRank !== rightRank) return leftRank - rightRank;
    return String(left.site_name || "").localeCompare(String(right.site_name || ""));
  });
}

function StatusPill({ label, tone }) {
  const style = TONE_STYLES[tone] || TONE_STYLES.unknown;
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        minWidth: 86,
        maxWidth: "100%",
        px: 1.15,
        py: 0,
        height: 24,
        borderRadius: 999,
        border: `1px solid ${style.border}`,
        background: style.bg,
        color: style.text,
        fontSize: "0.78rem",
        fontWeight: 700,
        lineHeight: 1,
        whiteSpace: "nowrap",
        overflow: "hidden",
        textOverflow: "ellipsis",
        verticalAlign: "middle",
      }}
    >
      {label}
    </Box>
  );
}

function TaskResultsBar({ counts }) {
  const total = Math.max(1, Number(counts?.total_tasks || 0));
  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 0.35, lineHeight: 1.55, fontFamily: gridFontFamily, minWidth: 0 }}>
      <Box sx={{ display: "flex", borderRadius: 1, overflow: "hidden", width: 220, maxWidth: "100%", height: 6 }}>
        {TASK_SECTIONS.map((section) => {
          const value = Number(counts?.[section.key] || 0);
          if (!value) return null;
          return (
            <Box
              key={section.key}
              component="span"
              sx={{
                display: "block",
                height: "100%",
                width: `${Math.max(4, Math.round((value / total) * 100))}%`,
                backgroundColor: section.color,
              }}
            />
          );
        })}
      </Box>
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          columnGap: 0.75,
          rowGap: 0.2,
          color: "#aaa",
          fontSize: 11,
          fontFamily: gridFontFamily,
        }}
      >
        {TASK_SECTIONS.filter((section) => Number(counts?.[section.key] || 0) > 0).map((section) => (
          <Box key={section.key} component="span" sx={{ display: "inline-flex", alignItems: "center", gap: 0.5 }}>
            <Box component="span" sx={{ width: 6, height: 6, borderRadius: 1, backgroundColor: section.color }} />
            {counts?.[section.key]} {section.label}
          </Box>
        ))}
      </Box>
    </Box>
  );
}

function SiteLinkCell(params) {
  const row = params?.data || {};
  const navigate = params?.context?.navigate;
  const href = `${APP_PATHS.devices}?site=${encodeURIComponent(String(row.site_id || ""))}`;
  return (
    <Box
      component="a"
      href={href}
      onMouseDown={stopGridEvent}
      onClick={(event) => {
        stopGridEvent(event);
        if (navigate) navigate(href);
      }}
      sx={{
        color: BOREALIS_LINK_COLOR,
        cursor: "pointer",
        fontWeight: 700,
        fontSize: "0.88rem",
        lineHeight: 1.4,
        textDecoration: "none",
        transition: "color 160ms ease",
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
        "&:hover": {
          color: BOREALIS_LINK_HOVER_COLOR,
          textDecoration: "none",
        },
      }}
    >
      {row.site_name || "Unknown Site"}
    </Box>
  );
}

function ContainerCell(params) {
  const row = params?.data || {};
  const onRecreate = params?.context?.onRecreate;
  const busy = params?.context?.recreateBusyId === row.worker_guid;
  const canRecreate = Boolean(row.worker_guid);
  const muted = row.container_id === "Unknown" || row.container_id === "N/A";
  return (
    <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 0.8, width: "100%", minWidth: 0 }}>
      <Typography
        component="span"
        title={row.container_id_full || row.container_name || row.container_id}
        sx={{
          color: muted ? PLACEHOLDER_TEXT_COLOR : "#f4f7ff",
          fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, monospace',
          fontSize: "0.82rem",
          lineHeight: 1.4,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          minWidth: 0,
          flexShrink: 1,
        }}
      >
        {row.container_id}
      </Typography>
      {canRecreate ? (
        <Box
          component="button"
          type="button"
          disabled={busy}
          onMouseDown={stopGridEvent}
          onClick={(event) => {
            stopGridEvent(event);
            if (busy) return;
            onRecreate?.(row);
          }}
          sx={{
            display: "inline-flex",
            alignItems: "center",
            p: 0,
            m: 0,
            border: 0,
            background: "transparent",
            color: BOREALIS_LINK_COLOR,
            cursor: busy ? "default" : "pointer",
            font: "inherit",
            fontSize: "0.82rem",
            fontWeight: 700,
            lineHeight: 1.45,
            textDecoration: "none",
            whiteSpace: "nowrap",
            transition: "color 160ms ease, opacity 160ms ease",
            "&:hover": busy
              ? undefined
              : {
                  color: BOREALIS_LINK_HOVER_COLOR,
                  textDecoration: "underline",
                },
            "&:disabled": {
              opacity: 0.6,
            },
          }}
        >
          {busy ? "[Queued...]" : "[Re-Create]"}
        </Box>
      ) : null}
    </Box>
  );
}

function CreatedCell(params) {
  const value = String(params?.data?.created_label || "").trim() || "N/A";
  return (
    <Typography
      component="span"
      sx={{
        color: value === "N/A" ? PLACEHOLDER_TEXT_COLOR : "#f4f7ff",
        fontSize: "0.84rem",
        fontWeight: 700,
        lineHeight: 1.35,
        textAlign: "center",
      }}
    >
      {value}
    </Typography>
  );
}

function StatusCell(params) {
  return <StatusPill label={params?.data?.status_label || "Unknown"} tone={params?.data?.status_tone || "unknown"} />;
}

function ConnectedDevicesCell(params) {
  const connected = Number(params?.data?.connected_devices || 0);
  const disconnected = Number(params?.data?.disconnected_devices || 0);
  const offline = Number(params?.data?.offline_devices || 0);
  const total = Math.max(0, Number(params?.data?.site_device_count || 0), connected + disconnected + offline);
  const counts = { connected, disconnected, offline };
  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 0.35,
        width: "100%",
        minWidth: 0,
        lineHeight: 1.25,
      }}
    >
      <Box
        sx={{
          display: "flex",
          borderRadius: 1,
          overflow: "hidden",
          width: 220,
          maxWidth: "100%",
          height: 6,
          backgroundColor: "rgba(148,163,184,0.18)",
        }}
      >
        {CONNECTION_SECTIONS.map((section) => {
          const value = Number(counts[section.key] || 0);
          if (!value || total <= 0) return null;
          return (
            <Box
              key={section.key}
              component="span"
              sx={{
                display: "block",
                height: "100%",
                width: `${Math.max(4, Math.round((value / total) * 100))}%`,
                backgroundColor: section.color,
              }}
            />
          );
        })}
      </Box>
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          justifyContent: "center",
          columnGap: 0.7,
          rowGap: 0.1,
          color: "#aaa",
          fontSize: 10,
          fontFamily: gridFontFamily,
        }}
      >
        {CONNECTION_SECTIONS.map((section) => (
          <Box key={section.key} component="span" sx={{ display: "inline-flex", alignItems: "center", gap: 0.35 }}>
            <Box component="span" sx={{ width: 5, height: 5, borderRadius: 1, backgroundColor: section.color }} />
            {counts[section.key]} {section.label}
          </Box>
        ))}
      </Box>
    </Box>
  );
}

function AssignedTasksCell(params) {
  const groups = Array.isArray(params?.data?.assigned_task_groups) ? params.data.assigned_task_groups : [];
  const navigate = params?.context?.navigate;
  if (!groups.length) {
    return (
      <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
        No assigned tasks
      </Typography>
    );
  }
  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        width: "100%",
        minHeight: "100%",
        py: 0.35,
      }}
    >
      {groups.map((group) => (
        <Box
          key={group.key}
          sx={{
            display: "grid",
            gridTemplateColumns: "112px minmax(0, 1fr)",
            alignItems: "center",
            columnGap: 1.2,
            minHeight: BASE_ROW_HEIGHT - 2,
            width: "min(100%, 470px)",
            minWidth: 0,
          }}
        >
          <Tooltip title={group.job_name || group.label || ""} placement="top-start" arrow>
            <Box
              component="button"
              type="button"
              disabled={!group.path}
              onMouseDown={stopGridEvent}
              onClick={(event) => {
                stopGridEvent(event);
                if (group.path && navigate) navigate(String(group.path));
              }}
              sx={{
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "flex-start",
                p: 0,
                border: 0,
                background: "transparent",
                color: BOREALIS_LINK_COLOR,
                cursor: group.path ? "pointer" : "default",
                font: "inherit",
                fontSize: "0.8rem",
                fontWeight: 700,
                lineHeight: 1.4,
                textDecoration: "none",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
                transition: "color 160ms ease",
                "&:hover": group.path
                  ? {
                      color: BOREALIS_LINK_HOVER_COLOR,
                      textDecoration: "underline",
                    }
                  : undefined,
                "&:disabled": {
                  opacity: 0.72,
                },
              }}
            >
              {group.label}
            </Box>
          </Tooltip>
          <TaskResultsBar counts={group.counts || emptyTaskCounts()} />
        </Box>
      ))}
    </Box>
  );
}

export default function SiteWorkers() {
  const navigate = useNavigate();
  const notify = useAppNotifications({ title: "Site Workers", icon: "accounttree", variant: "info" });
  const gridApiRef = useRef(null);
  const requestSeqRef = useRef(0);
  const [payload, setPayload] = useState({ workers: [], active_work: [], recent_work: [] });
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [lastLoadedAt, setLastLoadedAt] = useState(0);
  const [nowSeconds, setNowSeconds] = useState(() => Math.floor(Date.now() / 1000));
  const [confirmRow, setConfirmRow] = useState(null);
  const [recreateBusyId, setRecreateBusyId] = useState("");
  const [actionError, setActionError] = useState("");

  const rows = useMemo(() => normalizeRows(payload, nowSeconds), [nowSeconds, payload]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api || loading || !rows.length) return;
    const doSize = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        /* grid may still be settling */
      }
    };
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(doSize);
    } else {
      setTimeout(doSize, 0);
    }
  }, [loading, rows.length]);

  const loadWorkers = useCallback(async ({ showLoading = false } = {}) => {
    if (showLoading) setLoading(true);
    const requestSeq = requestSeqRef.current + 1;
    requestSeqRef.current = requestSeq;
    try {
      const response = await fetch(`/api/server/workers?history_seconds=${WORKER_HISTORY_SECONDS}&_=${Date.now()}`, {
        credentials: "include",
        cache: "no-store",
        headers: {
          "Cache-Control": "no-store",
        },
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${response.status}`);
      }
      if (requestSeq !== requestSeqRef.current) return;
      setPayload(data && typeof data === "object" ? data : {});
      setLoadError("");
      setLastLoadedAt(Date.now());
    } catch (error) {
      if (requestSeq !== requestSeqRef.current) return;
      setLoadError(error?.message || "Unable to load site workers.");
    } finally {
      if (requestSeq === requestSeqRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadWorkers({ showLoading: true });
    const poll = window.setInterval(() => {
      void loadWorkers();
    }, POLL_INTERVAL_MS);
    return () => window.clearInterval(poll);
  }, [loadWorkers]);

  useEffect(() => {
    const timer = window.setInterval(() => setNowSeconds(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    autoSizeColumns();
  }, [autoSizeColumns]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    const refresh = () => {
      try {
        api.refreshCells({ force: true });
        api.resetRowHeights();
      } catch {
        /* grid may still be settling */
      }
    };
    if (typeof requestAnimationFrame === "function") {
      requestAnimationFrame(refresh);
    } else {
      setTimeout(refresh, 0);
    }
  }, [rows]);

  const openRecreateDialog = useCallback((row) => {
    setActionError("");
    setConfirmRow(row || null);
  }, []);

  const closeRecreateDialog = useCallback(() => {
    if (recreateBusyId) return;
    setConfirmRow(null);
    setActionError("");
  }, [recreateBusyId]);

  const confirmRecreate = useCallback(async () => {
    const workerGuid = String(confirmRow?.worker_guid || "").trim();
    if (!workerGuid) return;
    setRecreateBusyId(workerGuid);
    setActionError("");
    try {
      const response = await fetch(`/api/server/workers/${encodeURIComponent(workerGuid)}/recreate`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${response.status}`);
      }
      await notify(`Re-create queued for ${confirmRow?.site_name || "site worker"}.`);
      setConfirmRow(null);
      await loadWorkers({ showLoading: false });
    } catch (error) {
      const message = error?.message || "Unable to queue site-worker re-create.";
      setActionError(message);
      await notify({ message, variant: "error" });
    } finally {
      setRecreateBusyId("");
    }
  }, [confirmRow, loadWorkers, notify]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "site-workers-refresh",
        label: "Refresh",
        icon: <CachedIcon />,
        tone: "secondary",
        loading,
        onClick: () => loadWorkers({ showLoading: true }),
      },
    ],
    [loadWorkers, loading]
  );

  useRoutePageChrome({
    title: "Site Workers",
    subtitle: "Site-worker containers, connected Agent sessions, and worker-assigned task state.",
    Icon: AccountTreeRoundedIcon,
    actions: pageHeaderActions,
  });

  const gridContext = useMemo(
    () => ({
      navigate,
      onRecreate: openRecreateDialog,
      recreateBusyId,
    }),
    [navigate, openRecreateDialog, recreateBusyId]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: true,
      resizable: true,
      minWidth: 120,
      suppressHeaderMenuButton: false,
      suppressHeaderContextMenu: true,
    }),
    []
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Status Sort",
        field: "status_sort",
        hide: true,
        sort: "asc",
        sortIndex: 0,
      },
      {
        headerName: "Site",
        field: "site_name",
        minWidth: 220,
        flex: 1,
        sort: "asc",
        sortIndex: 1,
        cellRendererParams: {
          suppressMouseEventHandling: () => true,
        },
        cellRenderer: SiteLinkCell,
      },
      {
        headerName: "Container ID",
        field: "container_id",
        minWidth: 260,
        flex: 1.05,
        cellClass: "center-col",
        cellRendererParams: {
          suppressMouseEventHandling: () => true,
        },
        cellRenderer: ContainerCell,
      },
      {
        headerName: "Created",
        field: "created_label",
        minWidth: 185,
        flex: 0.75,
        cellClass: "center-col",
        cellRenderer: CreatedCell,
      },
      {
        headerName: "Status",
        field: "status_label",
        minWidth: 160,
        flex: 0.45,
        cellClass: "auto-col-tight center-col",
        cellRenderer: StatusCell,
      },
      {
        headerName: "Connected Devices",
        field: "connected_devices",
        minWidth: 250,
        flex: 0.85,
        cellClass: "auto-col-tight center-col",
        cellRenderer: ConnectedDevicesCell,
      },
      {
        headerName: "Assigned Tasks",
        field: "assigned_task_groups",
        minWidth: 340,
        flex: 1.2,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        cellRendererParams: {
          suppressMouseEventHandling: () => true,
        },
        cellClass: "center-col",
        cellRenderer: AssignedTasksCell,
      },
    ],
    []
  );

  const lastUpdatedLabel = useMemo(() => {
    if (!lastLoadedAt) return "";
    const date = new Date(lastLoadedAt);
    return `Updated ${date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}`;
  }, [lastLoadedAt]);

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
        borderRadius: 0,
        fontFamily: gridFontFamily,
        color: "#f5f7fa",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
      }}
      elevation={0}
    >
      <PageBodyFrame
        variant="grid_with_stack"
        stack={
          <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1.5, minHeight: 24 }}>
            {loadError ? (
              <Alert severity="error" variant="outlined" sx={{ py: 0, color: "#fecdd3", borderColor: "rgba(251,113,133,0.42)" }}>
                {loadError}
              </Alert>
            ) : (
              <Typography sx={{ color: "rgba(148,163,184,0.88)", fontSize: "0.86rem" }}>
                {rows.length} sites
              </Typography>
            )}
            {lastUpdatedLabel ? (
              <Typography sx={{ color: "rgba(148,163,184,0.76)", fontSize: "0.78rem", ml: "auto" }}>
                {lastUpdatedLabel}
              </Typography>
            ) : null}
          </Box>
        }
      >
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          <Box className={SITE_WORKER_GRID_THEME_CLASS} sx={GRID_SHELL_SX} style={GRID_STYLE}>
            <AgGridReact
              rowData={rows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              context={gridContext}
              theme={SITE_WORKER_GRID_THEME}
              rowHeight={BASE_ROW_HEIGHT}
              getRowHeight={(params) =>
                BASE_ROW_HEIGHT * Math.max(1, Array.isArray(params?.data?.assigned_task_groups) ? params.data.assigned_task_groups.length : 0)
              }
              headerHeight={44}
              animateRows
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              suppressCellFocus
              suppressContextMenu
              preventDefaultOnContextMenu
              loading={loading}
              overlayLoadingTemplate={"<span class='ag-overlay-loading-center'>Loading site workers...</span>"}
              overlayNoRowsTemplate={"<span class='ag-overlay-no-rows-center'>No active or recent site workers.</span>"}
              getRowId={(params) => String(params?.data?.id || params?.rowIndex)}
              onGridReady={(params) => {
                gridApiRef.current = params.api;
                autoSizeColumns();
              }}
              onFirstDataRendered={autoSizeColumns}
              onRowDataUpdated={autoSizeColumns}
            />
            {loading ? (
              <Box
                sx={{
                  position: "absolute",
                  inset: 0,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  bgcolor: "rgba(30,30,30,0.38)",
                  zIndex: 2,
                  pointerEvents: "none",
                }}
              >
                <CircularProgress size={32} sx={{ color: BOREALIS_LINK_COLOR }} />
              </Box>
            ) : null}
          </Box>
        </Box>
      </PageBodyFrame>

      <Dialog open={Boolean(confirmRow)} onClose={closeRecreateDialog} PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Re-Create Site Worker"
            subtitle={confirmRow?.site_name ? `Site: ${confirmRow.site_name}` : ""}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Borealis will stop this site-worker container. Job Scheduler will deploy a replacement when same-site Agent or task demand remains.
          </Typography>
          <Typography
            sx={{
              mt: 1.4,
              color: "#f4f7ff",
              fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, monospace',
              fontSize: "0.84rem",
              overflowWrap: "anywhere",
            }}
          >
            {confirmRow?.container_id || confirmRow?.container_name || "Unknown container"}
          </Typography>
          {actionError ? (
            <Alert severity="error" variant="outlined" sx={{ mt: 2, color: "#fecdd3", borderColor: "rgba(251,113,133,0.42)" }}>
              {actionError}
            </Alert>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={closeRecreateDialog} sx={DIALOG_BUTTON_SX} disabled={Boolean(recreateBusyId)}>
            Cancel
          </Button>
          <Button onClick={confirmRecreate} sx={DIALOG_DANGER_BUTTON_SX} disabled={Boolean(recreateBusyId)}>
            {recreateBusyId ? "Queuing..." : "Re-Create"}
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
