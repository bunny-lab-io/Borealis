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
const BASE_ROW_HEIGHT = 44;
const WORKER_REMOVE_SECONDS = 30;
const AUTO_SIZE_COLUMNS = ["site_name", "container_id", "created_label", "status_label", "connected_devices", "assigned_tasks"];
const BOREALIS_LINK_COLOR = "#58a6ff";
const BOREALIS_LINK_HOVER_COLOR = "#7dd3fc";
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
  unknown: { border: "rgba(148,163,184,0.28)", bg: "rgba(30,41,59,0.2)", text: "#cbd5e1" },
};

const TASK_SECTIONS = [
  { key: "success", label: "Success", color: "#00d18c" },
  { key: "running", label: "Running", color: "#58a6ff" },
  { key: "pending", label: "Pending", color: "#999999" },
  { key: "failed", label: "Failed", color: "#ff4f4f" },
  { key: "skipped", label: "Skipped", color: "#f0c36d" },
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
    const jobKey = jobId ? `job:${jobId}` : `work:${work?.kind || "task"}:${work?.id || identity}`;
    keys.forEach((key) => {
      const workerGroups = byWorker.get(key) || new Map();
      const group = workerGroups.get(jobKey) || {
        key: jobKey,
        job_id: jobId,
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
    return { label: `Removing in ${Math.max(0, Math.ceil(removalRemainingSeconds))}s`, tone: "stopped" };
  }
  if (connected > 0 && !["lost", "stopped", "failed"].includes(normalized)) {
    return { label: "Running", tone: "running" };
  }
  if (normalized === "idle") {
    const idleSince = Number(worker?.idle_since || nowSeconds);
    const remaining = Math.max(0, Math.ceil(Number(idleTtlSeconds || 60) - Math.max(0, nowSeconds - idleSince)));
    if (remaining > 0) {
      return { label: `Tearing Down in ${remaining}s`, tone: "idle" };
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
  const idleTtlSeconds = Math.max(30, Number(payload?.worker_idle_ttl_seconds || 60));
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
  return workers
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
      return {
        id: workerGuid || containerName || `site-worker-${siteId}-${index}`,
        worker_guid: workerGuid,
        site_id: siteId,
        site_name: String(worker?.site_name || siteNameFor(payload, siteId)).trim() || `Site ${siteId}`,
        container_name: containerName,
        container_id: containerId || "Unknown",
        container_id_full: String(worker?.container_id_full || "").trim(),
        created_label: epochLabel(worker?.started_at),
        status_label: status.label,
        status_tone: status.tone,
        connected_devices: connectedDeviceCount(worker),
        assigned_task_groups: taskGroups,
        raw: worker,
      };
    })
    .sort((left, right) => String(left.site_name || "").localeCompare(String(right.site_name || "")));
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
        py: 0.35,
        borderRadius: 999,
        border: `1px solid ${style.border}`,
        background: style.bg,
        color: style.text,
        fontSize: "0.78rem",
        fontWeight: 700,
        lineHeight: 1.2,
        whiteSpace: "nowrap",
        overflow: "hidden",
        textOverflow: "ellipsis",
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
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1.1, width: "100%", minWidth: 0 }}>
      <Typography
        component="span"
        title={row.container_id_full || row.container_name || row.container_id}
        sx={{
          color: row.container_id === "Unknown" ? "rgba(148,163,184,0.78)" : "#f4f7ff",
          fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, monospace',
          fontSize: "0.82rem",
          lineHeight: 1.4,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          minWidth: 0,
        }}
      >
        {row.container_id}
      </Typography>
      <Box
        component="button"
        type="button"
        disabled={!canRecreate || busy}
        onMouseDown={stopGridEvent}
        onClick={(event) => {
          stopGridEvent(event);
          if (!canRecreate || busy) return;
          onRecreate?.(row);
        }}
        sx={{
          ml: "auto",
          display: "inline-flex",
          alignItems: "center",
          p: 0,
          border: 0,
          background: "transparent",
          color: BOREALIS_LINK_COLOR,
          cursor: !canRecreate || busy ? "default" : "pointer",
          font: "inherit",
          fontSize: "0.82rem",
          fontWeight: 700,
          lineHeight: 1.45,
          textDecoration: "none",
          whiteSpace: "nowrap",
          transition: "color 160ms ease, opacity 160ms ease",
          "&:hover": !canRecreate || busy
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
        {busy ? "Queued..." : "Re-Create"}
      </Box>
    </Box>
  );
}

function StatusCell(params) {
  return <StatusPill label={params?.data?.status_label || "Unknown"} tone={params?.data?.status_tone || "unknown"} />;
}

function ConnectedDevicesCell(params) {
  return (
    <Typography sx={{ color: "#f4f7ff", fontSize: "0.86rem", fontWeight: 700, lineHeight: 1.3 }}>
      {Number(params?.data?.connected_devices || 0)}
    </Typography>
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
    <Box sx={{ display: "flex", flexDirection: "column", justifyContent: "center", width: "100%", minHeight: "100%", py: 0.35 }}>
      {groups.map((group) => (
        <Box
          key={group.key}
          sx={{
            display: "grid",
            gridTemplateColumns: "112px minmax(0, 1fr)",
            alignItems: "center",
            columnGap: 1.2,
            minHeight: BASE_ROW_HEIGHT - 2,
            width: "100%",
            minWidth: 0,
          }}
        >
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
    try {
      const response = await fetch(`/api/server/workers?history_seconds=${WORKER_HISTORY_SECONDS}`, {
        credentials: "include",
        cache: "no-store",
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${response.status}`);
      }
      setPayload(data && typeof data === "object" ? data : {});
      setLoadError("");
      setLastLoadedAt(Date.now());
    } catch (error) {
      setLoadError(error?.message || "Unable to load site workers.");
    } finally {
      setLoading(false);
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
        headerName: "Site",
        field: "site_name",
        minWidth: 220,
        flex: 1,
        sort: "asc",
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
      },
      {
        headerName: "Status",
        field: "status_label",
        minWidth: 180,
        flex: 0.75,
        cellClass: "auto-col-tight",
        cellRenderer: StatusCell,
      },
      {
        headerName: "Connected Devices",
        field: "connected_devices",
        minWidth: 155,
        flex: 0.55,
        cellClass: "auto-col-tight",
        cellRenderer: ConnectedDevicesCell,
      },
      {
        headerName: "Assigned Tasks",
        field: "assigned_tasks",
        minWidth: 340,
        flex: 1.2,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        suppressHeaderContextMenu: true,
        cellRendererParams: {
          suppressMouseEventHandling: () => true,
        },
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
                {rows.length} active or recent site workers
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
