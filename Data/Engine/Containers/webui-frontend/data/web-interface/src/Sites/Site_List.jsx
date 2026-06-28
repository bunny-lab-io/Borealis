import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import { useLoaderData, useNavigate } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  Menu,
  MenuItem,
  Paper,
  Typography,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import AssessmentRoundedIcon from "@mui/icons-material/AssessmentRounded";
import DeleteRoundedIcon from "@mui/icons-material/DeleteRounded";
import DevicesRoundedIcon from "@mui/icons-material/DevicesRounded";
import DriveFileRenameOutlineRoundedIcon from "@mui/icons-material/DriveFileRenameOutlineRounded";
import LocationCityIcon from "@mui/icons-material/LocationCity";
import DownloadRoundedIcon from "@mui/icons-material/DownloadRounded";
import AltRouteRoundedIcon from "@mui/icons-material/AltRouteRounded";
import DevicesIcon from "@mui/icons-material/Devices";
import CheckCircleOutlineRoundedIcon from "@mui/icons-material/CheckCircleOutlineRounded";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreateSiteDialog, RenameSiteDialog } from "../Dialogs.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import {
  appendDockerStatsHistory,
  dockerStatsHistoryKey,
  formatDockerStatBytes,
  formatDockerStatPercent,
  formatDockerStatRate,
  hasDockerStats,
  pruneDockerStatsHistory,
} from "../app/utils/dockerStats.js";
import {
  createRouteRequestPlan,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../app/routes/routeData.js";
import { APP_PATHS } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const WORKER_HISTORY_SECONDS = 300;
const WORKER_REMOVE_SECONDS = 30;
const TASK_REMOVE_SECONDS = 60;
const SITE_WORKER_REFRESH_MS = 5000;
const BASE_ROW_HEIGHT = 56;
const SITE_WORKER_CONTAINER_COLUMN_ID = "site_worker_container_id";
const AUTO_SIZE_COLUMNS = [SITE_WORKER_CONTAINER_COLUMN_ID, "connected_devices"];
const DEFAULT_INSTALL_BRANCH = "main";
const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;
const RAW_BOREALIS_BASE_URL = "https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads";
const BOREALIS_LINK_COLOR = "#58a6ff";
const BOREALIS_LINK_HOVER_COLOR = "#7dd3fc";
const INSTALL_OS_OPTIONS = [
  { id: "windows", label: "Windows" },
  { id: "linux", label: "Linux" },
];

const TASK_SECTIONS = [
  { key: "success", label: "Success", color: "#00d18c" },
  { key: "running", label: "Running", color: "#58a6ff" },
  { key: "pending", label: "Pending", color: "#999999" },
  { key: "failed", label: "Failed", color: "#ff4f4f" },
  { key: "skipped", label: "Skipped", color: "#f0c36d" },
];

const CONNECTION_SECTIONS = [
  { key: "connected", label: "Connected", color: "#00d18c" },
  { key: "disconnected", label: "Disconnected", color: "#ff4f4f" },
  { key: "offline", label: "Offline", color: "#999999" },
];

const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.94) 60%, rgba(15, 6, 26, 0.96) 100%)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  success: "#34d399",
};

const SITE_DIALOG_PAPER_SX = {
  borderRadius: 3,
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: MAGIC_UI.textBright,
  overflow: "hidden",
};

const SITE_DIALOG_TITLE_SX = {
  px: 3,
  pt: 3,
  pb: 0.75,
  background: "transparent",
};

const SITE_DIALOG_CONTENT_SX = {
  px: 3,
  pt: 1,
  pb: 2.5,
  background: "transparent",
};

const SITE_DIALOG_ACTIONS_SX = {
  px: 3,
  pt: 0.5,
  pb: 2.5,
  gap: 1,
  background: "transparent",
};

const SITE_DIALOG_BUTTON_SX = {
  borderRadius: 999,
  px: 2,
  minHeight: 38,
  textTransform: "none",
  fontWeight: 600,
  fontSize: "0.92rem",
  color: MAGIC_UI.textBright,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.84)",
  transition: "background 160ms ease, border-color 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background: "rgba(8,14,30,0.92)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&:active": {
    transform: "translateY(0.5px)",
  },
};

const SITE_DIALOG_DANGER_BUTTON_SX = {
  ...SITE_DIALOG_BUTTON_SX,
  color: "#ff9aa5",
  borderColor: "rgba(244,63,94,0.38)",
  background: "rgba(44,8,22,0.6)",
  "&:hover": {
    background: "rgba(58,10,28,0.76)",
    borderColor: "rgba(251,113,133,0.58)",
  },
  "&.Mui-disabled": {
    color: "rgba(255,154,165,0.48)",
    borderColor: "rgba(244,63,94,0.16)",
    background: "rgba(44,8,22,0.24)",
  },
};

const INSTALL_MENU_PAPER_SX = {
  mt: 0.8,
  minWidth: 180,
  borderRadius: 2.5,
  overflow: "hidden",
  background:
    "radial-gradient(140% 140% at 0% 0%, rgba(76,186,255,0.18), transparent 52%), " +
    "radial-gradient(140% 140% at 100% 0%, rgba(214,130,255,0.22), transparent 62%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: MAGIC_UI.textBright,
  "& .MuiList-root": {
    py: 0.8,
  },
};

const INSTALL_MENU_ITEM_SX = {
  mx: 0.8,
  my: 0.3,
  borderRadius: 2,
  minHeight: 40,
  fontSize: "0.92rem",
  fontWeight: 600,
  color: MAGIC_UI.textBright,
  transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background:
      "linear-gradient(90deg, rgba(125,211,252,0.16) 0%, rgba(192,132,252,0.14) 100%)",
  },
  "&.Mui-focusVisible": {
    background:
      "linear-gradient(90deg, rgba(125,211,252,0.16) 0%, rgba(192,132,252,0.14) 100%)",
  },
};

const SITE_CONTEXT_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const SITE_CONTEXT_MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: MAGIC_UI.textBright,
  alignItems: "center",
  px: 1,
  py: 0.85,
  position: "relative",
  overflow: "hidden",
  "&:hover": {
    backgroundColor: "rgba(88,166,255,0.12)",
  },
  "&::before": {
    content: '""',
    position: "absolute",
    left: 0,
    top: 8,
    bottom: 8,
    width: 3,
    borderRadius: 999,
    background: "transparent",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const SITE_CONTEXT_MENU_DANGER_ITEM_SX = {
  ...SITE_CONTEXT_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const SITE_CONTEXT_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const SITE_CONTEXT_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const SITE_CONTEXT_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const SITE_CONTEXT_MENU_HEADER_ICON_SX = {
  width: 32,
  height: 32,
  borderRadius: 1.35,
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid rgba(148,163,184,0.14)",
  background: "rgba(255,255,255,0.04)",
  color: "#8fd3ff",
};

const SITE_CONTEXT_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const SITE_CONTEXT_MENU_LABEL_SX = {
  color: MAGIC_UI.textBright,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const SITE_CONTEXT_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const SITE_CONTEXT_MENU_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const SITE_CONTEXT_MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
  view: "View",
};

const SITE_CONTEXT_MENU_GROUP_ORDER = ["primary", "organize", "danger", "view"];

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
  return {
    connected_devices: connected,
    site_device_count: total,
    site_online_device_count: online,
    disconnected_devices: Math.max(0, online - connected),
    offline_devices: Math.max(0, total - online),
  };
}

function siteRecordsById(payload) {
  const records = new Map();
  const sites = Array.isArray(payload?.sites) ? payload.sites : [];
  if (sites.length) {
    sites.forEach((site) => {
      const siteId = Number(site?.id || site?.site_id || 0);
      if (!Number.isFinite(siteId) || siteId <= 0) return;
      const deviceCount = Number(site?.device_count || site?.site_device_count || 0);
      const onlineDeviceCount = Number(site?.online_device_count || site?.site_online_device_count || 0);
      records.set(siteId, {
        site_device_count: Number.isFinite(deviceCount) && deviceCount > 0 ? deviceCount : 0,
        site_online_device_count: Number.isFinite(onlineDeviceCount) && onlineDeviceCount > 0 ? onlineDeviceCount : 0,
      });
    });
    return records;
  }

  const names = payload?.site_names || {};
  const counts = payload?.site_device_counts || {};
  const onlineCounts = payload?.site_online_device_counts || {};
  Object.keys(names).forEach((siteIdRaw) => {
    const siteId = Number(siteIdRaw || 0);
    if (!Number.isFinite(siteId) || siteId <= 0) return;
    const deviceCount = Number(counts[String(siteId)] || counts[siteId] || 0);
    const onlineDeviceCount = Number(onlineCounts[String(siteId)] || onlineCounts[siteId] || 0);
    records.set(siteId, {
      site_device_count: Number.isFinite(deviceCount) && deviceCount > 0 ? deviceCount : 0,
      site_online_device_count: Number.isFinite(onlineDeviceCount) && onlineDeviceCount > 0 ? onlineDeviceCount : 0,
    });
  });
  return records;
}

function workStatusBucket(status) {
  const normalized = String(status || "").trim().toLowerCase();
  if (["succeeded", "success", "completed"].includes(normalized)) return "success";
  if (normalized === "running") return "running";
  if (["queued", "pending", "reassigning"].includes(normalized)) return "pending";
  if (["cancelled", "canceled", "skipped", "stopped"].includes(normalized)) return "skipped";
  if (["failed", "lost", "error", "timed_out", "timed out", "timeout"].includes(normalized)) return "failed";
  return "pending";
}

function isTerminalTaskBucket(bucket) {
  return ["success", "failed", "skipped"].includes(String(bucket || "").trim().toLowerCase());
}

function statusRank(status) {
  const ranks = { running: 0, pending: 1, failed: 2, success: 3, skipped: 4 };
  return ranks[workStatusBucket(status)] ?? 5;
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

function positiveWorkNumber(...values) {
  for (const value of values) {
    const parsed = Number(value || 0);
    if (Number.isFinite(parsed) && parsed > 0) return parsed;
  }
  return 0;
}

function workRunId(work) {
  const taskLink = work?.task_link || {};
  return positiveWorkNumber(work?.run_id, taskLink?.run_id);
}

function workJobId(work) {
  const taskLink = work?.task_link || {};
  return positiveWorkNumber(work?.job_id, taskLink?.job_id);
}

function workTargetCount(work) {
  return positiveWorkNumber(work?.target_count, work?.task_link?.target_count);
}

function workTargetId(work) {
  return positiveWorkNumber(work?.target_id, work?.task_link?.target_id);
}

function workDisplayIdentity(work, activeSingletonRunIds) {
  const runId = workRunId(work);
  if (runId > 0) {
    if (activeSingletonRunIds?.has(runId)) return `run:${runId}:single-target`;
    const targetId = workTargetId(work);
    if (targetId > 0) return `run:${runId}:target:${targetId}`;
    if (workTargetCount(work) <= 1) return `run:${runId}:single-target`;
    return `run:${runId}:all-targets`;
  }

  const jobId = workJobId(work);
  if (jobId > 0) {
    const targetId = workTargetId(work);
    if (targetId > 0) return `job:${jobId}:target:${targetId}`;
    if (workTargetCount(work) <= 1) return `job:${jobId}:single-target`;
    return `job:${jobId}:all-targets`;
  }

  return workIdentity(work);
}

function shouldReplaceDisplayedWork(existing, next) {
  const existingBucket = workStatusBucket(existing?.status);
  const nextBucket = workStatusBucket(next?.status);
  const existingActive = !isTerminalTaskBucket(existingBucket);
  const nextActive = !isTerminalTaskBucket(nextBucket);
  if (existingActive !== nextActive) return nextActive;
  const rankDelta = statusRank(next?.status) - statusRank(existing?.status);
  if (rankDelta !== 0) return rankDelta < 0;
  return taskActivitySeconds(next) >= taskActivitySeconds(existing);
}

function visibleWorkerTaskRecords(activeWork, recentWork) {
  const activeSingletonRunIds = new Set();
  activeWork.forEach((work) => {
    const runId = workRunId(work);
    if (runId <= 0 || isTerminalTaskBucket(workStatusBucket(work?.status))) return;
    const targetCount = workTargetCount(work);
    if (workTargetId(work) <= 0 && targetCount > 0 && targetCount <= 1) {
      activeSingletonRunIds.add(runId);
    }
  });

  const seen = new Set();
  const byDisplayIdentity = new Map();
  [...activeWork, ...recentWork].forEach((work) => {
    const identity = workIdentity(work);
    if (seen.has(identity)) return;
    seen.add(identity);
    const displayIdentity = workDisplayIdentity(work, activeSingletonRunIds);
    const existing = byDisplayIdentity.get(displayIdentity);
    if (!existing || shouldReplaceDisplayedWork(existing, work)) {
      byDisplayIdentity.set(displayIdentity, work);
    }
  });
  return Array.from(byDisplayIdentity.values());
}

function jobIdForWork(work) {
  const numberValue = workJobId(work);
  return numberValue > 0 ? numberValue : null;
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

function taskActivitySeconds(work) {
  return Number(work?.finished_at || work?.heartbeat_at || work?.updated_at || work?.started_at || work?.created_at || 0);
}

function taskCountdownSecondsLabel(seconds) {
  return `${Math.max(0, Math.ceil(Number(seconds || 0)))}s`;
}

export function buildTaskGroupsByWorker(payload, nowSeconds = Math.floor(Date.now() / 1000)) {
  const byWorker = new Map();
  const activeWork = Array.isArray(payload?.active_work) ? payload.active_work : [];
  const recentWork = Array.isArray(payload?.recent_work) ? payload.recent_work : [];
  visibleWorkerTaskRecords(activeWork, recentWork).forEach((work) => {
    const keys = new Set(
      [work?.worker_guid, work?.lease_owner, work?.container_name]
        .map((value) => String(value || "").trim())
        .filter(Boolean)
    );
    const siteId = Number(work?.site_id || 0);
    if (Number.isFinite(siteId) && siteId > 0) {
      keys.add(`site:${siteId}`);
    }
    if (!keys.size) return;
    const bucket = workStatusBucket(work?.status);
    const jobId = jobIdForWork(work);
    const jobName = jobNameForWork(work);
    const jobKey = jobId ? `job:${jobId}` : `work:${work?.kind || "task"}:${work?.id || identity}`;
    const activitySeconds = taskActivitySeconds(work);
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
        last_activity_at: activitySeconds,
        terminal_last_activity_at: 0,
        has_active_work: false,
        expires_at: null,
        removal_remaining_seconds: null,
        rank: statusRank(work?.status),
      };
      const counts = group.counts;
      counts[bucket] = Number(counts[bucket] || 0) + 1;
      counts.total_tasks = Number(counts.total_tasks || 0) + 1;
      if (isTerminalTaskBucket(bucket)) {
        group.terminal_last_activity_at = Math.max(Number(group.terminal_last_activity_at || 0), activitySeconds);
      } else {
        group.has_active_work = true;
      }
      group.rank = Math.min(Number(group.rank ?? 5), statusRank(work?.status));
      group.first_started_at = Math.min(
        Number(group.first_started_at || work?.started_at || work?.created_at || 0),
        Number(work?.started_at || work?.created_at || group.first_started_at || 0)
      );
      group.last_activity_at = Math.max(
        Number(group.last_activity_at || 0),
        activitySeconds
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
      Array.from(groups.values())
        .map((group) => {
          const terminalAt = Number(group.terminal_last_activity_at || 0);
          if (!group.has_active_work && terminalAt > 0) {
            const expiresAt = terminalAt + TASK_REMOVE_SECONDS;
            const remaining = expiresAt - Number(nowSeconds || 0);
            return {
              ...group,
              expires_at: expiresAt,
              removal_remaining_seconds: Math.max(0, remaining),
            };
          }
          return group;
        })
        .filter((group) => Number(group.expires_at || 0) <= 0 || Number(group.removal_remaining_seconds || 0) > 0)
        .sort((left, right) => {
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

function buildWorkerRowsBySite(payload, nowSeconds) {
  const workers = Array.isArray(payload?.workers) ? payload.workers : [];
  const taskGroupsByWorker = buildTaskGroupsByWorker(payload, nowSeconds);
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
    .forEach((worker) => {
      const siteId = Number(worker?.site_id || 0);
      const workerGuid = String(worker?.worker_guid || "").trim();
      const containerName = String(worker?.container_name || "").trim();
      const taskGroups =
        (workerGuid && taskGroupsByWorker.get(workerGuid)) ||
        (containerName && taskGroupsByWorker.get(containerName)) ||
        taskGroupsByWorker.get(`site:${siteId}`) ||
        [];
      const connectedDevices = connectedDeviceCount(worker);
      const deviceCounts = deviceConnectionBreakdown(
        payload,
        worker,
        siteId,
        connectedDevices,
        siteDeviceCount(payload, worker, siteId)
      );
      rowsBySite.set(siteId, {
        site_worker_guid: workerGuid,
        site_worker_container_name: containerName,
        site_worker_container_id: String(worker?.container_id || "").trim() || "Unknown",
        site_worker_container_id_full: String(worker?.container_id_full || "").trim(),
        site_worker_container_size_rootfs_bytes: Number(worker?.container_size_rootfs_bytes || 0),
        site_worker_container_size_rw_bytes: Number(worker?.container_size_rw_bytes || 0),
        site_worker_container_storage_limit_bytes: Number(worker?.container_storage_limit_bytes || 0),
        site_worker_container_storage_limit_source: String(worker?.container_storage_limit_source || "").trim(),
        site_worker_docker_stats: worker?.docker_stats || null,
        connected_devices: deviceCounts.connected_devices,
        site_device_count: deviceCounts.site_device_count,
        site_online_device_count: deviceCounts.site_online_device_count,
        disconnected_devices: deviceCounts.disconnected_devices,
        offline_devices: deviceCounts.offline_devices,
        assigned_task_groups: taskGroups,
        raw_site_worker: worker,
      });
    });
  return rowsBySite;
}

function mergeSiteWorkerRows(sites, payload, nowSeconds) {
  const workerRowsBySite = buildWorkerRowsBySite(payload, nowSeconds);
  const recordsBySite = siteRecordsById(payload);
  return (Array.isArray(sites) ? sites : [])
    .map((site) => {
      const siteId = Number(site?.id || site?.site_id || 0);
      const siteName = String(site?.name || site?.site_name || "").trim();
      const siteRecord = recordsBySite.get(siteId) || {};
      const workerRow = workerRowsBySite.get(siteId);
      const rawSiteDeviceCount = Number(site?.device_count || 0);
      if (workerRow) {
        const connected = Number(workerRow.connected_devices || 0);
        const total = Math.max(0, rawSiteDeviceCount, Number(workerRow.site_device_count || 0));
        const online = Math.max(
          connected,
          Math.min(total, Math.max(Number(workerRow.site_online_device_count || 0), connected + Number(workerRow.disconnected_devices || 0)))
        );
        return {
          ...site,
          ...workerRow,
          site_id: siteId,
          site_name: siteName,
          device_count: total,
          site_device_count: total,
          site_online_device_count: online,
          disconnected_devices: Math.max(0, online - connected),
          offline_devices: Math.max(0, total - online),
        };
      }

      const total = Math.max(0, rawSiteDeviceCount, Number(siteRecord.site_device_count || 0));
      const online = Math.max(0, Math.min(total, Number(siteRecord.site_online_device_count || 0)));
      return {
        ...site,
        site_id: siteId,
        site_name: siteName,
        site_worker_guid: "",
        site_worker_container_name: "",
        site_worker_container_id: "N/A",
        site_worker_container_id_full: "",
        site_worker_container_size_rootfs_bytes: 0,
        site_worker_container_size_rw_bytes: 0,
        site_worker_container_storage_limit_bytes: 0,
        site_worker_container_storage_limit_source: "",
        site_worker_docker_stats: null,
        connected_devices: 0,
        site_device_count: total,
        site_online_device_count: online,
        disconnected_devices: online,
        offline_devices: Math.max(0, total - online),
        assigned_task_groups: [],
        raw_site_worker: null,
      };
    })
    .sort((left, right) =>
      String(left?.name || left?.site_name || "").localeCompare(
        String(right?.name || right?.site_name || ""),
        undefined,
        { sensitivity: "base", numeric: true }
      )
    );
}

function recordSiteWorkerResourceHistory(historyMap, payload, sampledAtMs = Date.now()) {
  if (!(historyMap instanceof Map)) return 0;
  const workerRowsBySite = buildWorkerRowsBySite(payload, Math.floor(sampledAtMs / 1000));
  const activeKeys = new Set();
  workerRowsBySite.forEach((row) => {
    const key = dockerStatsHistoryKey(row);
    if (!key) return;
    activeKeys.add(key);
    appendDockerStatsHistory(historyMap, row, sampledAtMs);
  });
  pruneDockerStatsHistory(historyMap, activeKeys);
  return activeKeys.size;
}

function attachResourceHistoryToRows(rows, historyMap) {
  if (!Array.isArray(rows) || !(historyMap instanceof Map)) return rows;
  return rows.map((row) => {
    const key = dockerStatsHistoryKey(row);
    const history = key ? historyMap.get(key) : null;
    return {
      ...row,
      site_worker_resource_history_key: key,
      site_worker_resource_history: Array.isArray(history) ? history : [],
    };
  });
}

export function siteWorkerContainerRefreshValue(row) {
  const stats = row?.site_worker_docker_stats || {};
  const history = Array.isArray(row?.site_worker_resource_history) ? row.site_worker_resource_history : [];
  const latest = history.length ? history[history.length - 1] : null;
  return [
    row?.site_worker_container_id,
    row?.site_worker_resource_history_key,
    latest?.sampledAtMs,
    stats?.read_at,
    stats?.cpu_percent,
    stats?.memory_usage_bytes,
    stats?.memory_limit_bytes,
    stats?.net_input_bytes,
    stats?.net_output_bytes,
    row?.site_worker_container_size_rootfs_bytes,
    row?.site_worker_container_size_rw_bytes,
    row?.site_worker_container_storage_limit_bytes,
  ]
    .map((value) => String(value ?? ""))
    .join("|");
}

async function fetchSiteWorkerPayloadRoute(progress) {
  try {
    const payload = await progress.fetchJson(`/api/server/workers?history_seconds=${WORKER_HISTORY_SECONDS}`);
    return payload && typeof payload === "object" ? payload : {};
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {};
  }
}

async function fetchSiteWorkerPayloadBrowser() {
  try {
    const response = await fetch(`/api/server/workers?history_seconds=${WORKER_HISTORY_SECONDS}&_=${Date.now()}`, {
      credentials: "include",
      cache: "no-store",
      headers: {
        Accept: "application/json",
        "Cache-Control": "no-cache",
        Pragma: "no-cache",
      },
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) return null;
    return payload && typeof payload === "object" ? payload : {};
  } catch {
    return null;
  }
}

function normalizeInstallServerUrl(value) {
  return String(value || "").trim().replace(/\/+$/, "");
}

function deriveInstallServerUrl(payload) {
  const explicitBaseUrl = normalizeInstallServerUrl(payload?.public_base_url);
  if (explicitBaseUrl) {
    return explicitBaseUrl;
  }

  const explicitHostname = String(payload?.public_hostname || "").trim();
  if (explicitHostname) {
    if (/^https?:\/\//i.test(explicitHostname)) {
      return normalizeInstallServerUrl(explicitHostname);
    }
    return `https://${explicitHostname}`;
  }

  if (typeof window === "undefined") {
    return "";
  }

  try {
    const currentOrigin = String(window.location?.origin || "").trim();
    if (!currentOrigin) return "";
    const currentUrl = new URL(currentOrigin);
    const host = currentUrl.hostname.toLowerCase();
    if (host === "localhost" || host === "127.0.0.1" || host === "0.0.0.0") {
      return "";
    }
    return normalizeInstallServerUrl(currentUrl.origin);
  } catch {
    return "";
  }
}

export async function loadSiteListPageData(request) {
  const progress = createRouteRequestPlan(request, 5);
  try {
    await requireAuthenticatedRequest(request, progress);
    const data = await progress.fetchJson("/api/sites");
    let installServerUrl = deriveInstallServerUrl(data);
    if (!installServerUrl) {
      try {
        const overviewPayload = await progress.fetchJson("/api/server/overview");
        installServerUrl = deriveInstallServerUrl({
          public_base_url:
            overviewPayload?.host?.public_base_url || overviewPayload?.public_edge?.public_base_url || "",
          public_hostname:
            overviewPayload?.host?.public_hostname || overviewPayload?.public_edge?.fqdn || "",
        });
      } catch (error) {
        rethrowIfRouteRedirect(error);
      }
    } else {
      progress.skip(1);
    }
    const siteWorkerPayload = await fetchSiteWorkerPayloadRoute(progress);

    return {
      rows: Array.isArray(data?.sites) ? data.sites : [],
      siteWorkerPayload,
      installServerUrl,
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      rows: [],
      siteWorkerPayload: {},
      installServerUrl: "",
      initialError: getRouteErrorMessage(error, "Unable to load sites."),
    };
  } finally {
    progress.finalize();
  }
}

function stopGridRowSelectionEvent(event) {
  if (!event) return;
  if (typeof event.stopPropagation === "function") {
    event.stopPropagation();
  }
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

function TaskExpiryCountdown({ group }) {
  const expiresAt = Number(group?.expires_at || 0);
  const remaining = Math.max(0, Math.ceil(Number(group?.removal_remaining_seconds || 0)));
  if (expiresAt <= 0 || remaining <= 0) return null;
  return (
    <Box sx={{ minWidth: 0, width: "100%", display: "flex", justifyContent: "flex-start" }}>
      <Typography
        component="span"
        sx={{
          display: "block",
          width: "100%",
          color: "rgba(148,163,184,0.72)",
          fontSize: "0.68rem",
          fontWeight: 500,
          lineHeight: 1.2,
          textAlign: "left",
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {taskCountdownSecondsLabel(remaining)}
      </Typography>
    </Box>
  );
}

function SiteWorkerContainerCell(params) {
  const row = params?.data || {};
  if (!hasDockerStats(row.site_worker_docker_stats)) {
    return (
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%", height: "100%" }}>
        <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
          Site Worker Not Running
        </Typography>
      </Box>
    );
  }
  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        width: "100%",
        height: "100%",
        minWidth: 0,
      }}
    >
      <SiteWorkerStatsCell data={row} />
    </Box>
  );
}

function latestResourceSample(samples, fallback) {
  return Array.isArray(samples) && samples.length ? samples[samples.length - 1] : fallback;
}

function resourceHistoryMax(samples, key) {
  return (Array.isArray(samples) ? samples : []).reduce((max, sample) => {
    const value = Number(sample?.[key] || 0);
    return Number.isFinite(value) && value > max ? value : max;
  }, 0);
}

function resourceSparklinePoints(samples, key, maxValue) {
  const values = Array.isArray(samples) && samples.length ? samples : [];
  if (!values.length) return "";
  const width = 72;
  const height = 18;
  const topPad = 2;
  const bottomPad = 2;
  const safeMax = Math.max(1, Number(maxValue || 0));
  const pointFor = (sample, index, count) => {
    const rawValue = Math.max(0, Number(sample?.[key] || 0));
    const x = count <= 1 ? width : (index / (count - 1)) * width;
    const y = height - bottomPad - Math.min(1, rawValue / safeMax) * (height - topPad - bottomPad);
    return `${x.toFixed(2)},${y.toFixed(2)}`;
  };
  if (values.length === 1) {
    const point = pointFor(values[0], 0, 1).split(",");
    return `0,${point[1]} ${width},${point[1]}`;
  }
  return values.map((sample, index) => pointFor(sample, index, values.length)).join(" ");
}

function ResourceTrendPanel({ label, value, scaleLabel, tooltip, color, samples, valueKey, maxValue }) {
  const points = resourceSparklinePoints(samples, valueKey, maxValue);
  return (
    <Tooltip title={tooltip || ""} placement="top">
      <Box
        sx={{
          height: 46,
          minWidth: 0,
          borderRadius: 1,
          border: "1px solid rgba(148,163,184,0.22)",
          background: "rgba(15,23,42,0.36)",
          px: 0.45,
          py: 0.3,
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 0.35,
            minWidth: 0,
          }}
        >
          <Typography
            component="span"
            sx={{
              color: "rgba(148,163,184,0.88)",
              fontSize: "0.49rem",
              fontWeight: 900,
              lineHeight: 1,
              textTransform: "uppercase",
              whiteSpace: "nowrap",
            }}
          >
            {label}
          </Typography>
          <Typography
            component="span"
            sx={{
              color: MAGIC_UI.textBright,
              fontSize: "0.56rem",
              fontWeight: 900,
              lineHeight: 1,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              minWidth: 0,
            }}
          >
            {value}
          </Typography>
        </Box>
        <Box
          component="svg"
          viewBox="0 0 72 18"
          preserveAspectRatio="none"
          sx={{
            width: "100%",
            height: 18,
            display: "block",
            overflow: "visible",
          }}
        >
          <line x1="0" y1="17" x2="72" y2="17" stroke="rgba(148,163,184,0.18)" strokeWidth="1" />
          {points ? (
            <polyline
              points={points}
              fill="none"
              stroke={color}
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              vectorEffect="non-scaling-stroke"
            />
          ) : null}
        </Box>
        <Typography
          component="span"
          sx={{
            color: "rgba(148,163,184,0.72)",
            fontSize: "0.47rem",
            fontWeight: 800,
            lineHeight: 1,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            minWidth: 0,
            textAlign: "right",
          }}
        >
          {scaleLabel}
        </Typography>
      </Box>
    </Tooltip>
  );
}

function SiteWorkerStatsCell(params) {
  const stats = params?.data?.site_worker_docker_stats;
  if (!hasDockerStats(stats)) {
    return (
      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", fontWeight: 600 }}>
        Stats unavailable
      </Typography>
    );
  }
  const history = Array.isArray(params?.data?.site_worker_resource_history) ? params.data.site_worker_resource_history : [];
  const fallbackSample = {
    cpuPercent: Math.max(0, Number(stats.cpu_percent || 0)),
    memoryUsageBytes: Math.max(0, Number(stats.memory_usage_bytes || 0)),
    memoryLimitBytes: Math.max(0, Number(stats.memory_limit_bytes || 0)),
    netInputBps: 0,
    netOutputBps: 0,
    netTotalBps: 0,
    diskUsageBytes: Math.max(0, Number(params?.data?.site_worker_container_size_rootfs_bytes || 0)),
    diskWritableBytes: Math.max(0, Number(params?.data?.site_worker_container_size_rw_bytes || 0)),
    diskLimitBytes: Math.max(0, Number(params?.data?.site_worker_container_storage_limit_bytes || 0)),
    diskLimitSource: String(params?.data?.site_worker_container_storage_limit_source || "").trim(),
  };
  const samples = history.length ? history : [fallbackSample];
  const latest = latestResourceSample(samples, fallbackSample);
  const cpuMax = Math.max(resourceHistoryMax(samples, "cpuPercent"), latest.cpuPercent);
  const ramMax = latest.memoryLimitBytes > 0
    ? latest.memoryLimitBytes
    : Math.max(resourceHistoryMax(samples, "memoryUsageBytes"), latest.memoryUsageBytes);
  const netMax = Math.max(resourceHistoryMax(samples, "netTotalBps"), latest.netTotalBps);
  const diskMax = latest.diskLimitBytes > 0
    ? latest.diskLimitBytes
    : Math.max(resourceHistoryMax(samples, "diskUsageBytes"), latest.diskUsageBytes);
  const metrics = [
    {
      label: "CPU",
      value: formatDockerStatPercent(latest.cpuPercent),
      scaleLabel: formatDockerStatPercent(cpuMax),
      tooltip: `Current ${formatDockerStatPercent(latest.cpuPercent)} | 60s max ${formatDockerStatPercent(cpuMax)}`,
      color: "#7dd3fc",
      valueKey: "cpuPercent",
      maxValue: cpuMax,
    },
    {
      label: "RAM",
      value: formatDockerStatBytes(latest.memoryUsageBytes),
      scaleLabel: latest.memoryLimitBytes > 0 ? formatDockerStatBytes(latest.memoryLimitBytes) : formatDockerStatBytes(ramMax),
      tooltip: latest.memoryLimitBytes > 0
        ? `${formatDockerStatBytes(latest.memoryUsageBytes)} / ${formatDockerStatBytes(latest.memoryLimitBytes)}`
        : `${formatDockerStatBytes(latest.memoryUsageBytes)} | 60s max ${formatDockerStatBytes(ramMax)}`,
      color: "#c084fc",
      valueKey: "memoryUsageBytes",
      maxValue: ramMax,
    },
    {
      label: "NET",
      value: formatDockerStatRate(latest.netTotalBps),
      scaleLabel: formatDockerStatRate(netMax),
      tooltip: `${formatDockerStatRate(latest.netTotalBps)} total | In ${formatDockerStatRate(latest.netInputBps)} | Out ${formatDockerStatRate(latest.netOutputBps)}`,
      color: "#34d399",
      valueKey: "netTotalBps",
      maxValue: netMax,
    },
    {
      label: "DISK",
      value: formatDockerStatBytes(latest.diskUsageBytes),
      scaleLabel: latest.diskLimitBytes > 0 ? formatDockerStatBytes(latest.diskLimitBytes) : formatDockerStatBytes(diskMax),
      tooltip: latest.diskLimitBytes > 0
        ? `${formatDockerStatBytes(latest.diskUsageBytes)} / ${formatDockerStatBytes(latest.diskLimitBytes)}${latest.diskLimitSource ? ` | ${latest.diskLimitSource}` : ""}`
        : `${formatDockerStatBytes(latest.diskUsageBytes)} | 60s max ${formatDockerStatBytes(diskMax)} | Writable ${formatDockerStatBytes(latest.diskWritableBytes)}`,
      color: "#fbbf24",
      valueKey: "diskUsageBytes",
      maxValue: diskMax,
    },
  ];
  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        display: "grid",
        gridTemplateColumns: "repeat(4, minmax(0, 1fr))",
        gap: 0.4,
      }}
    >
      {metrics.map((metric) => (
        <ResourceTrendPanel key={metric.label} samples={samples} {...metric} />
      ))}
    </Box>
  );
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
        height: "100%",
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
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%", height: "100%" }}>
        <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
          No Running Tasks
        </Typography>
      </Box>
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
        height: "100%",
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
          <Box
            sx={{
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-start",
              justifyContent: "center",
              alignSelf: "stretch",
              minWidth: 0,
              minHeight: BASE_ROW_HEIGHT - 2,
              gap: 0.05,
            }}
          >
            <Tooltip title={group.job_name || group.label || ""} placement="top-start" arrow>
              <Box
                component="button"
                type="button"
                disabled={!group.path}
                onMouseDown={stopGridRowSelectionEvent}
                onClick={(event) => {
                  stopGridRowSelectionEvent(event);
                  if (group.path && navigate) navigate(String(group.path));
                }}
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "flex-start",
                  width: "100%",
                  maxWidth: "100%",
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
            <TaskExpiryCountdown group={group} />
          </Box>
          <TaskResultsBar counts={group.counts || emptyTaskCounts()} />
        </Box>
      ))}
    </Box>
  );
}

function escapePowerShellDoubleQuoted(value) {
  return String(value || "").replace(/`/g, "``").replace(/"/g, '`"');
}

function escapeShellDoubleQuoted(value) {
  return String(value || "").replace(/(["\\$`])/g, "\\$1");
}

function normalizeInstallBranch(value) {
  return String(value || DEFAULT_INSTALL_BRANCH).trim() || DEFAULT_INSTALL_BRANCH;
}

function rawBorealisFileUrl(branch, fileName) {
  const encodedBranch = normalizeInstallBranch(branch)
    .split("/")
    .map((part) => encodeURIComponent(part))
    .join("/");
  return `${RAW_BOREALIS_BASE_URL}/${encodedBranch}/${fileName}`;
}

function quoteShellValue(value) {
  return `"${escapeShellDoubleQuoted(value)}"`;
}

function quotePowerShellValue(value) {
  return `"${escapePowerShellDoubleQuoted(value)}"`;
}

export function buildInstallCommand(osId, serverUrl, enrollmentCode, branch = DEFAULT_INSTALL_BRANCH) {
  const normalizedServerUrl = normalizeInstallServerUrl(serverUrl);
  const normalizedEnrollmentCode = String(enrollmentCode || "").trim();
  const normalizedBranch = normalizeInstallBranch(branch);
  const usesDefaultBranch = normalizedBranch === DEFAULT_INSTALL_BRANCH;
  if (!normalizedServerUrl || !normalizedEnrollmentCode) {
    return "";
  }

  if (osId === "windows") {
    const agentUrl = rawBorealisFileUrl(normalizedBranch, "Data/Agent/dist/windows-amd64/Agent.exe");
    return `$borealisAgent = Join-Path $env:TEMP "Borealis-Agent.exe"; ` +
      `Invoke-WebRequest -UseBasicParsing -Uri ${quotePowerShellValue(agentUrl)} -OutFile $borealisAgent; ` +
      `& $borealisAgent --server-url ${quotePowerShellValue(normalizedServerUrl)} ` +
      `--repo-ref ${quotePowerShellValue(normalizedBranch)} ` +
      `--site-enrollment-code ${quotePowerShellValue(normalizedEnrollmentCode)}`;
  }

  if (osId === "linux") {
    const agentUrl = rawBorealisFileUrl(normalizedBranch, "Data/Agent/dist/linux-amd64/Agent");
    const urlArg = usesDefaultBranch ? agentUrl : quoteShellValue(agentUrl);
    const launchArgs = `--server-url "${escapeShellDoubleQuoted(normalizedServerUrl)}" ` +
      `--repo-ref "${escapeShellDoubleQuoted(normalizedBranch)}" ` +
      `--site-enrollment-code "${escapeShellDoubleQuoted(normalizedEnrollmentCode)}" --install-service`;
    return `curl -fsSL ${urlArg} -o /tmp/Borealis-Agent; ` +
      `chmod 700 /tmp/Borealis-Agent; ` +
      `sudo /tmp/Borealis-Agent ${launchArgs}`;
  }
  return "";
}

function SiteDeleteDialog({ open, onCancel, onConfirm, sites }) {
  const siteNames = Array.isArray(sites) ? sites.map((site) => site?.name).filter(Boolean) : [];
  const previewNames = siteNames.slice(0, 4);
  const remainingCount = Math.max(siteNames.length - previewNames.length, 0);
  const deleteLabel = "Delete Site(s)";

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            {deleteLabel}
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Permanently remove the selected site records from Borealis.
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        {previewNames.length ? (
          <Box
            sx={{
              mt: 0.5,
              borderRadius: 2.5,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(7,12,24,0.82)",
              px: 1.5,
              py: 1.35,
            }}
          >
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", fontWeight: 700, letterSpacing: 0.75, textTransform: "uppercase" }}>
              Selected Sites
            </Typography>
            <Box sx={{ mt: 1.2, display: "flex", flexWrap: "wrap", gap: 0.9 }}>
              {previewNames.map((name) => (
                <Box
                  key={name}
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(148,163,184,0.26)",
                    background: "rgba(15,23,42,0.76)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.84rem", fontWeight: 500 }}>
                    {name}
                  </Typography>
                </Box>
              ))}
              {remainingCount > 0 ? (
                <Box
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(244,63,94,0.26)",
                    background: "rgba(44,8,22,0.38)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: "#ffb1b9", fontSize: "0.84rem", fontWeight: 600 }}>
                    +{remainingCount} more
                  </Typography>
                </Box>
              ) : null}
            </Box>
          </Box>
        ) : null}
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onConfirm} disabled={!siteNames.length} sx={SITE_DIALOG_DANGER_BUTTON_SX}>
          {deleteLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function InstallBranchDialog({
  open,
  rows,
  loading,
  error,
  draftBranch,
  onDraftBranchChange,
  onRefresh,
  onCancel,
  onApply,
}) {
  const branchGridRef = useRef(null);
  const branchGridApiRef = useRef(null);
  const normalizedDraftBranch = normalizeInstallBranch(draftBranch);

  const selectDraftBranch = useCallback(() => {
    const api = branchGridApiRef.current || branchGridRef.current?.api;
    if (!api || typeof api.forEachNode !== "function") return;
    api.forEachNode((node) => {
      const name = String(node?.data?.name || "");
      node.setSelected?.(name === normalizedDraftBranch);
    });
  }, [normalizedDraftBranch]);

  useEffect(() => {
    if (!open) {
      branchGridApiRef.current = null;
      return undefined;
    }
    const handle = setTimeout(selectDraftBranch, 0);
    return () => clearTimeout(handle);
  }, [open, rows, selectDraftBranch]);

  const branchColumnDefs = useMemo(() => [
    {
      headerName: "Branch",
      field: "name",
      minWidth: 260,
      flex: 1,
      cellStyle: {
        display: "flex",
        alignItems: "center",
      },
      cellRenderer: (params) => (
        <Typography sx={{ color: "#58a6ff", fontSize: "0.88rem", fontWeight: 600, lineHeight: 1.2 }}>
          {params.value}
        </Typography>
      ),
    },
    {
      headerName: "Commit",
      field: "sha",
      minWidth: 130,
      maxWidth: 150,
      valueFormatter: (params) => String(params.value || "").slice(0, 12),
    },
  ], []);

  const branchDefaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
  }), []);

  const branchRowSelection = useMemo(
    () => ({
      mode: "singleRow",
      checkboxes: true,
      headerCheckbox: false,
      enableClickSelection: true,
    }),
    []
  );

  const branchSelectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
      sortable: false,
      resizable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="md" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            Switch Branch
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Selected branch: {normalizedDraftBranch}
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        {error ? (
          <Alert severity="error" sx={{ mb: 1.5 }}>
            {error}
          </Alert>
        ) : null}
        {loading ? (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, mb: 1.5, color: MAGIC_UI.textMuted }}>
            <CircularProgress size={16} color="inherit" />
            <Typography sx={{ fontSize: "0.84rem", color: "inherit" }}>Loading branches</Typography>
          </Box>
        ) : null}
        <Box
          className={themeClassName}
          sx={{
            height: 440,
            minHeight: 320,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              background: "rgba(7,12,24,0.82)",
            },
            "& .ag-header": {
              backgroundColor: "rgba(15,23,42,0.9)",
              borderBottom: "1px solid rgba(148,163,184,0.25)",
            },
            "& .ag-row-selected": {
              backgroundColor: "rgba(125,211,252,0.2) !important",
              boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
            },
          }}
        >
          <AgGridReact
            ref={branchGridRef}
            rowData={rows}
            columnDefs={branchColumnDefs}
            defaultColDef={branchDefaultColDef}
            rowSelection={branchRowSelection}
            selectionColumnDef={branchSelectionColumnDef}
            suppressCellFocus
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            animateRows
            getRowId={(params) => String(params.data?.name || "")}
            onGridReady={(params) => {
              branchGridApiRef.current = params.api;
              selectDraftBranch();
            }}
            onFirstDataRendered={selectDraftBranch}
            onRowDataUpdated={selectDraftBranch}
            onSelectionChanged={() => {
              const api = branchGridApiRef.current || branchGridRef.current?.api;
              const selected = api?.getSelectedRows?.()?.[0]?.name;
              if (selected) {
                onDraftBranchChange(selected);
              }
            }}
            theme={myTheme}
          />
        </Box>
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onRefresh} disabled={loading} sx={SITE_DIALOG_BUTTON_SX}>Refresh</Button>
        <Button onClick={onCancel} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onApply} disabled={!normalizedDraftBranch || loading} sx={SITE_DIALOG_BUTTON_SX}>
          Apply
        </Button>
      </DialogActions>
    </Dialog>
  );
}

const PAGE_TITLE = "Sites";
const PAGE_SUBTITLE = "Manage sites and open device inventories by site.";
const PAGE_ICON = LocationCityIcon;

export default function SiteList() {
  const loaderData = useLoaderData();
  const navigate = useNavigate();
  const initialRows = useMemo(() => (Array.isArray(loaderData?.rows) ? loaderData.rows : []), [loaderData]);
  const initialSiteWorkerPayload = useMemo(
    () => (loaderData?.siteWorkerPayload && typeof loaderData.siteWorkerPayload === "object" ? loaderData.siteWorkerPayload : {}),
    [loaderData]
  );
  const initialInstallServerUrl = String(loaderData?.installServerUrl || "");
  const [rows, setRows] = useState(() => initialRows);
  const [siteWorkerPayload, setSiteWorkerPayload] = useState(() => initialSiteWorkerPayload);
  const [installServerUrl, setInstallServerUrl] = useState(() => initialInstallServerUrl);
  const [nowSeconds, setNowSeconds] = useState(() => Math.floor(Date.now() / 1000));
  const [loadError, setLoadError] = useState(() => String(loaderData?.initialError || ""));
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [renameSiteId, setRenameSiteId] = useState(null);
  const [autoApprovalOpen, setAutoApprovalOpen] = useState(false);
  const [autoApprovalSite, setAutoApprovalSite] = useState(null);
  const [autoApprovalUntil, setAutoApprovalUntil] = useState(() => dayjs().add(6, "hour"));
  const [autoApprovalSaving, setAutoApprovalSaving] = useState(false);
  const [autoApprovalError, setAutoApprovalError] = useState("");
  const [siteContextMenu, setSiteContextMenu] = useState({ open: false, top: 0, left: 0, row: null });
  const [installMenuAnchorEl, setInstallMenuAnchorEl] = useState(null);
  const [installMenuSite, setInstallMenuSite] = useState(null);
  const [selectedInstallBranch, setSelectedInstallBranch] = useState(DEFAULT_INSTALL_BRANCH);
  const [draftInstallBranch, setDraftInstallBranch] = useState(DEFAULT_INSTALL_BRANCH);
  const [branchDialogOpen, setBranchDialogOpen] = useState(false);
  const [branchRows, setBranchRows] = useState([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchLoadError, setBranchLoadError] = useState("");
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const autoSizeHandleRef = useRef(null);
  const siteWorkerRefreshInFlightRef = useRef(false);
  const resourceHistoryRef = useRef(new Map());
  const [resourceHistoryVersion, setResourceHistoryVersion] = useState(0);
  const notify = useAppNotifications({
    title: PAGE_TITLE,
    icon: "locationcity",
    variant: "info",
  });
  const sendNotification = useCallback(
    async (message) => {
      await notify(message);
    },
    [notify]
  );
  const displayRows = useMemo(
    () => attachResourceHistoryToRows(mergeSiteWorkerRows(rows, siteWorkerPayload, nowSeconds), resourceHistoryRef.current),
    [nowSeconds, resourceHistoryVersion, rows, siteWorkerPayload]
  );

  const handleOpenDevicesForSite = useCallback(
    (site) => {
      const siteId = site?.id;
      if (siteId == null || siteId === "") return;
      navigate(`${APP_PATHS.devices}?site=${encodeURIComponent(String(siteId))}`);
    },
    [navigate]
  );

  const handleOpenSoftwareAuditForSite = useCallback(
    (site) => {
      const siteId = site?.id;
      if (siteId == null || siteId === "") return;
      navigate(`${APP_PATHS.software}?site=${encodeURIComponent(String(siteId))}`);
    },
    [navigate]
  );

  const handleOpenOnboardingForSite = useCallback(
    (site) => {
      const siteId = site?.id;
      if (siteId == null || siteId === "") return;
      navigate(`${APP_PATHS.jobOnboardingNew}?site_id=${encodeURIComponent(String(siteId))}&site_locked=1&tab=scope`);
    },
    [navigate]
  );

  const fetchInstallServerUrlFromOverview = useCallback(async () => {
    try {
      const response = await fetch("/api/server/overview", {
        credentials: "include",
        cache: "no-store",
      });
      if (!response.ok) {
        return "";
      }
      const payload = await response.json().catch(() => ({}));
      return deriveInstallServerUrl({
        public_base_url: payload?.host?.public_base_url || payload?.public_edge?.public_base_url || "",
        public_hostname: payload?.host?.public_hostname || payload?.public_edge?.fqdn || "",
      });
    } catch {
      return "";
    }
  }, []);

  const fetchSites = useCallback(async () => {
    try {
      const [res, workerPayload] = await Promise.all([
        fetch("/api/sites", { credentials: "include", cache: "no-store" }),
        fetchSiteWorkerPayloadBrowser(),
      ]);
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${res.status}`);
      }
      const nextInstallServerUrl = deriveInstallServerUrl(data) || await fetchInstallServerUrlFromOverview();
      setRows(Array.isArray(data?.sites) ? data.sites : []);
      setSiteWorkerPayload(workerPayload && typeof workerPayload === "object" ? workerPayload : {});
      setInstallServerUrl(nextInstallServerUrl);
      setLoadError("");
    } catch {
      setRows([]);
      setSiteWorkerPayload({});
      setInstallServerUrl("");
      setLoadError("Unable to load sites.");
    }
  }, [fetchInstallServerUrlFromOverview]);

  const fetchInstallBranches = useCallback(async () => {
    setBranchesLoading(true);
    setBranchLoadError("");
    try {
      const tokenRes = await fetch("/api/github/token", {
        credentials: "include",
        cache: "no-store",
      });
      const tokenData = await tokenRes.json().catch(() => ({}));
      if (!tokenRes.ok) {
        const message = tokenData?.message || tokenData?.error || `GitHub token lookup failed (HTTP ${tokenRes.status}).`;
        throw new Error(message);
      }
      const githubToken = String(tokenData?.token || "").trim();
      if (!githubToken) {
        throw new Error(tokenData?.message || "GitHub API token is unavailable.");
      }

      const nextRows = [];
      for (let page = 1; page <= 10; page += 1) {
        const branchRes = await fetch(`${GITHUB_BRANCHES_API_URL}?per_page=100&page=${page}`, {
          cache: "no-store",
          headers: {
            Accept: "application/vnd.github+json",
            Authorization: `Bearer ${githubToken}`,
          },
        });
        if (!branchRes.ok) {
          const body = await branchRes.text().catch(() => "");
          throw new Error(`GitHub branch lookup failed (HTTP ${branchRes.status})${body ? `: ${body.slice(0, 180)}` : ""}`);
        }
        const pageRows = await branchRes.json().catch(() => []);
        if (!Array.isArray(pageRows)) {
          throw new Error("GitHub branch lookup returned an unexpected payload.");
        }
        pageRows.forEach((branch) => {
          const name = String(branch?.name || "").trim();
          if (!name) return;
          nextRows.push({
            name,
            sha: String(branch?.commit?.sha || "").trim(),
            protected: Boolean(branch?.protected),
            default: name === DEFAULT_INSTALL_BRANCH,
          });
        });
        if (pageRows.length < 100) {
          break;
        }
      }
      nextRows.sort((a, b) => {
        if (a.name === DEFAULT_INSTALL_BRANCH) return -1;
        if (b.name === DEFAULT_INSTALL_BRANCH) return 1;
        return a.name.localeCompare(b.name);
      });
      setBranchRows(nextRows);
      if (!nextRows.length) {
        setBranchLoadError("No GitHub branches returned.");
      }
    } catch (error) {
      setBranchRows([]);
      setBranchLoadError(error instanceof Error ? error.message : "GitHub branch lookup failed.");
    } finally {
      setBranchesLoading(false);
    }
  }, []);

  useEffect(() => {
    setRows(initialRows);
    setSiteWorkerPayload(initialSiteWorkerPayload);
    setInstallServerUrl(initialInstallServerUrl);
    setLoadError(String(loaderData?.initialError || ""));
  }, [initialInstallServerUrl, initialRows, initialSiteWorkerPayload, loaderData]);

  useEffect(() => {
    recordSiteWorkerResourceHistory(resourceHistoryRef.current, siteWorkerPayload, Date.now());
    setResourceHistoryVersion((version) => version + 1);
  }, [siteWorkerPayload]);

  useEffect(() => {
    const intervalId = setInterval(() => {
      setNowSeconds(Math.floor(Date.now() / 1000));
    }, 1000);
    return () => clearInterval(intervalId);
  }, []);

  useEffect(() => {
    let active = true;
    const refreshSiteWorkerPayload = async () => {
      if (siteWorkerRefreshInFlightRef.current) return;
      siteWorkerRefreshInFlightRef.current = true;
      try {
        const workerPayload = await fetchSiteWorkerPayloadBrowser();
        if (active && workerPayload && typeof workerPayload === "object") {
          setSiteWorkerPayload(workerPayload);
        }
      } finally {
        siteWorkerRefreshInFlightRef.current = false;
      }
    };
    const intervalId = setInterval(refreshSiteWorkerPayload, SITE_WORKER_REFRESH_MS);
    refreshSiteWorkerPayload();
    return () => {
      active = false;
      clearInterval(intervalId);
      siteWorkerRefreshInFlightRef.current = false;
    };
  }, []);

  useEffect(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api) return;
    if (typeof api.isDestroyed === "function" && api.isDestroyed()) return;
    try {
      api.refreshCells({ columns: [SITE_WORKER_CONTAINER_COLUMN_ID], force: true });
    } catch {}
  }, [resourceHistoryVersion, siteWorkerPayload]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || !displayRows.length) return;
    const doSize = () => {
      autoSizeHandleRef.current = null;
      const liveApi = gridApiRef.current || gridRef.current?.api || api;
      if (!liveApi) return;
      if (typeof liveApi.isDestroyed === "function" && liveApi.isDestroyed()) return;
      try {
        liveApi.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {}
    };
    if (autoSizeHandleRef.current != null) {
      if (typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(autoSizeHandleRef.current);
      } else {
        clearTimeout(autoSizeHandleRef.current);
      }
      autoSizeHandleRef.current = null;
    }
    if (typeof requestAnimationFrame === "function") {
      autoSizeHandleRef.current = requestAnimationFrame(doSize);
    } else {
      autoSizeHandleRef.current = setTimeout(doSize, 0);
    }
  }, [displayRows.length]);

  useEffect(() => {
    autoSizeColumns();
  }, [displayRows, autoSizeColumns]);

  useEffect(() => {
    return () => {
      if (autoSizeHandleRef.current != null) {
        if (typeof cancelAnimationFrame === "function") {
          cancelAnimationFrame(autoSizeHandleRef.current);
        } else {
          clearTimeout(autoSizeHandleRef.current);
        }
        autoSizeHandleRef.current = null;
      }
      gridApiRef.current = null;
      resourceHistoryRef.current.clear();
    };
  }, []);

  const copyTextToClipboard = useCallback(async (value, promptTitle = "Copy text") => {
    const normalizedValue = String(value || "").trim();
    if (!normalizedValue) {
      return false;
    }
    try {
      await navigator.clipboard.writeText(normalizedValue);
      return true;
    } catch {
      window.prompt(promptTitle, normalizedValue);
      return false;
    }
  }, []);

  const handleCloseInstallMenu = useCallback(() => {
    setInstallMenuAnchorEl(null);
    setInstallMenuSite(null);
  }, []);

  const handleOpenBranchDialog = useCallback(() => {
    setDraftInstallBranch(selectedInstallBranch);
    setBranchDialogOpen(true);
    handleCloseInstallMenu();
    void fetchInstallBranches();
  }, [fetchInstallBranches, handleCloseInstallMenu, selectedInstallBranch]);

  const handleCloseBranchDialog = useCallback(() => {
    setBranchDialogOpen(false);
    setDraftInstallBranch(selectedInstallBranch);
  }, [selectedInstallBranch]);

  const handleApplyInstallBranch = useCallback(async () => {
    const nextBranch = normalizeInstallBranch(draftInstallBranch);
    setSelectedInstallBranch(nextBranch);
    setDraftInstallBranch(nextBranch);
    setBranchDialogOpen(false);
    await sendNotification({
      title: "Install Branch Updated",
      message: `Agent install commands now target <b>${nextBranch}</b>.`,
      icon: "done",
      variant: "info",
    });
  }, [draftInstallBranch, sendNotification]);

  const handleCopyInstallCommand = useCallback(async (osId, site) => {
    const siteName = String(site?.name || "Unknown Site").trim() || "Unknown Site";
    const enrollmentCode = String(site?.enrollment_code || "").trim();
    const osLabel = INSTALL_OS_OPTIONS.find((option) => option.id === osId)?.label || "Agent";
    const command = buildInstallCommand(osId, installServerUrl, enrollmentCode, selectedInstallBranch);

    if (!command) {
      await sendNotification({
        title: "Install Command Unavailable",
        message: `Borealis could not build the <b>${osLabel}</b> install command for <b>${siteName}</b> because the public engine URL or enrollment code is unavailable.`,
        icon: "warning",
        variant: "error",
      });
      return;
    }

    const copied = await copyTextToClipboard(command, `Copy ${osLabel} install command`);
    if (copied) {
      await sendNotification({
        title: "Install Command Copied",
        message: `Agent installation command for <b>${osLabel}</b> at <b>${siteName}</b> copied to clipboard.`,
        icon: "done",
        variant: "info",
      });
      return;
    }

    await sendNotification({
      title: "Manual Copy Required",
      message: `Clipboard access was blocked, so Borealis opened a manual copy prompt for the <b>${osLabel}</b> install command at <b>${siteName}</b>.`,
      icon: "warning",
      variant: "warning",
    });
  }, [copyTextToClipboard, installServerUrl, selectedInstallBranch, sendNotification]);

  const handleSelectInstallOs = useCallback(async (osId) => {
    const activeSite = installMenuSite;
    handleCloseInstallMenu();
    if (!activeSite) {
      return;
    }
    await handleCopyInstallCommand(osId, activeSite);
  }, [handleCloseInstallMenu, handleCopyInstallCommand, installMenuSite]);

  const openRenameDialog = useCallback((siteOverride = null) => {
    const selId = siteOverride?.id ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
    if (selId == null) return;
    const site = siteOverride || rows.find((row) => row.id === selId);
    setRenameSiteId(selId);
    setRenameValue(site?.name || "");
    setRenameOpen(true);
  }, [rows, selectedIds]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableClickSelection: true,
      enableSelectionWithoutKeys: true,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
      sortable: false,
      resizable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const columnDefs = useMemo(() => [
    {
      headerName: "Name",
      field: "name",
      minWidth: 260,
      flex: 1.15,
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellRenderer: (params) => {
        const description = String(params?.data?.description || "").trim();
        return (
          <Box
            sx={{
              display: "flex",
              flexDirection: "column",
              justifyContent: "center",
              alignItems: "flex-start",
              width: "100%",
              height: "100%",
              minWidth: 0,
              lineHeight: 1.25,
            }}
          >
            <Box
              component="span"
              sx={{
                color: BOREALIS_LINK_COLOR,
                cursor: "pointer",
                fontWeight: 500,
                fontSize: "0.88rem",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
                maxWidth: "100%",
              }}
              onMouseDown={stopGridRowSelectionEvent}
              onClick={(event) => {
                stopGridRowSelectionEvent(event);
                handleOpenDevicesForSite(params.data);
              }}
            >
              {params.value}
            </Box>
            {description ? (
              <Typography
                component="span"
                title={description}
                sx={{
                  mt: 0.25,
                  color: "rgba(148,163,184,0.72)",
                  fontSize: "0.72rem",
                  fontWeight: 500,
                  lineHeight: 1.2,
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  maxWidth: "100%",
                }}
              >
                {description}
              </Typography>
            ) : null}
          </Box>
        );
      },
    },
    {
      headerName: "Site Worker Container",
      colId: SITE_WORKER_CONTAINER_COLUMN_ID,
      valueGetter: (params) => siteWorkerContainerRefreshValue(params.data),
      minWidth: 540,
      flex: 1.45,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellClass: "center-col",
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellRenderer: SiteWorkerContainerCell,
    },
    {
      headerName: "Connected Devices",
      field: "connected_devices",
      resizable: false,
      minWidth: 250,
      flex: 0.9,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellClass: "center-col",
      cellRenderer: ConnectedDevicesCell,
    },
    {
      headerName: "Assigned Tasks",
      field: "assigned_task_groups",
      minWidth: 340,
      flex: 1.2,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellClass: "center-col",
      cellRenderer: AssignedTasksCell,
    },
    {
      headerName: "Auto-Approval",
      field: "auto_approve_until",
      minWidth: 240,
      valueGetter: (params) => {
        const until = Number(params.data?.auto_approve_until || 0);
        const active = Boolean(params.data?.auto_approval_active) && until > Math.floor(Date.now() / 1000);
        if (!active) return "Off";
        return `Until ${new Date(until * 1000).toLocaleString()}`;
      },
      cellStyle: {
        display: "flex",
        alignItems: "center",
      },
      cellRenderer: (params) => {
        const until = Number(params.data?.auto_approve_until || 0);
        const active = Boolean(params.data?.auto_approval_active) && until > Math.floor(Date.now() / 1000);
        return (
          <Typography
            variant="body2"
            sx={{
              color: active ? MAGIC_UI.success : MAGIC_UI.textMuted,
              fontWeight: active ? 700 : 500,
              lineHeight: 1.2,
            }}
          >
            {params.value}
          </Typography>
        );
      },
    },
  ], [handleOpenDevicesForSite]);

  const defaultColDef = useMemo(() => ({
    sortable: false,
    filter: "agTextColumnFilter",
    resizable: true,
    minWidth: 160,
  }), []);

  const selectedSiteRows = useMemo(
    () => rows.filter((row) => row?.id != null && selectedIds.has(row.id)),
    [rows, selectedIds]
  );

  const singleSelectedSite = useMemo(
    () => (selectedSiteRows.length === 1 ? selectedSiteRows[0] : null),
    [selectedSiteRows]
  );

  const hasSelectedSites = selectedSiteRows.length > 0;
  const canOpenInstallMenu = Boolean(singleSelectedSite && installServerUrl && singleSelectedSite?.enrollment_code);
  const installActionTooltip = selectedIds.size === 0
    ? "Select one site to copy an install command"
    : selectedIds.size > 1
      ? "Select exactly one site to copy an install command"
      : !installServerUrl
        ? "Borealis public engine URL is unavailable"
        : !singleSelectedSite?.enrollment_code
          ? "The selected site is missing an enrollment code"
          : undefined;

  const handleOpenInstallMenu = useCallback((event) => {
    if (!singleSelectedSite) return;
    event?.preventDefault?.();
    event?.stopPropagation?.();
    setInstallMenuAnchorEl(event.currentTarget);
    setInstallMenuSite(singleSelectedSite);
  }, [singleSelectedSite]);

  const handleOpenSiteContextMenu = useCallback((event, row, rowNode = null) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    if (rowNode && !rowNode.isSelected?.()) {
      rowNode.setSelected?.(true, true);
    }
    setSiteContextMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row: row || null,
    });
  }, []);

  const handleCloseSiteContextMenu = useCallback(() => {
    setSiteContextMenu({ open: false, top: 0, left: 0, row: null });
  }, []);

  const openAutoApprovalDialog = useCallback((siteOverride = null) => {
    const selId = siteOverride?.id ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
    const site = siteOverride || rows.find((row) => row.id === selId);
    if (!site?.id) return;
    handleCloseSiteContextMenu();
    setAutoApprovalSite(site);
    const existingUntil = Number(site.auto_approve_until || 0);
    setAutoApprovalUntil(existingUntil > Math.floor(Date.now() / 1000) ? dayjs.unix(existingUntil) : dayjs().add(6, "hour"));
    setAutoApprovalError("");
    setAutoApprovalOpen(true);
  }, [handleCloseSiteContextMenu, rows, selectedIds]);

  const handleOpenDeleteDialog = useCallback((siteOverride = null) => {
    handleCloseSiteContextMenu();
    if (siteOverride?.id != null && !selectedIds.has(siteOverride.id)) {
      setSelectedIds(new Set([siteOverride.id]));
      setDeleteOpen(true);
      return;
    }
    if (!hasSelectedSites) return;
    setDeleteOpen(true);
  }, [handleCloseSiteContextMenu, hasSelectedSites, selectedIds]);

  useEffect(() => {
    if (!hasSelectedSites) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
      return;
    }

    if (!singleSelectedSite) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
      return;
    }

    if (installMenuSite && String(installMenuSite.id) !== String(singleSelectedSite.id)) {
      setInstallMenuAnchorEl(null);
      setInstallMenuSite(null);
    }
  }, [hasSelectedSites, installMenuSite, singleSelectedSite]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "install-site-agent",
        label: "Install Agent(s)",
        icon: <DownloadRoundedIcon />,
        tone: "primary",
        disabled: !canOpenInstallMenu,
        tooltip: installActionTooltip,
        onClick: handleOpenInstallMenu,
      },
      ...(singleSelectedSite
        ? [{
            id: "onboard-site-devices",
            label: "Onboard Devices",
            icon: <DevicesIcon />,
            tone: "primary",
            onClick: () => handleOpenOnboardingForSite(singleSelectedSite),
          },
          {
            id: "site-auto-approval",
            label: "Auto-Approval",
            icon: <CheckCircleOutlineRoundedIcon />,
            tone: "secondary",
            onClick: () => openAutoApprovalDialog(singleSelectedSite),
          }]
        : []),
      {
        id: "create-site",
        label: "Create Site",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => setCreateOpen(true),
      },
    ],
    [
      canOpenInstallMenu,
      handleOpenInstallMenu,
      handleOpenOnboardingForSite,
      openAutoApprovalDialog,
      installActionTooltip,
      singleSelectedSite,
    ]
  );

  const siteContextRow = siteContextMenu.row || null;
  const siteContextSubtitle = siteContextRow
    ? `${Number(siteContextRow.device_count || 0).toLocaleString()} Device${Number(siteContextRow.device_count || 0) === 1 ? "" : "s"}`
    : "Site";
  const siteContextActions = useMemo(() => {
    const row = siteContextMenu.row || null;
    const unavailableReason = row ? "" : "Select a site first.";
    const deleteTargetCount = row?.id != null && selectedIds.has(row.id) ? selectedSiteRows.length : row ? 1 : 0;
    return [
      {
        id: "display-site-devices",
        group: "primary",
        label: "Display Site Devices",
        icon: DevicesRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Open Devices with this site filter applied.",
        onClick: () => {
          handleCloseSiteContextMenu();
          handleOpenDevicesForSite(row);
        },
      },
      {
        id: "display-software-audit",
        group: "primary",
        label: "Display Software Audit",
        icon: AssessmentRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Open Software Audit filtered to this site.",
        onClick: () => {
          handleCloseSiteContextMenu();
          handleOpenSoftwareAuditForSite(row);
        },
      },
      {
        id: "onboard-site-devices",
        group: "primary",
        label: "Onboard Devices",
        icon: DevicesIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Open Automatic Device Onboarding locked to this site.",
        onClick: () => {
          handleCloseSiteContextMenu();
          handleOpenOnboardingForSite(row);
        },
      },
      {
        id: "configure-auto-approval",
        group: "primary",
        label: "Configure Auto-Approval",
        icon: CheckCircleOutlineRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Approve new enrollments for this site until a chosen time.",
        onClick: () => openAutoApprovalDialog(row),
      },
      {
        id: "rename-site",
        group: "organize",
        label: "Rename",
        icon: DriveFileRenameOutlineRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Rename this site record.",
        onClick: () => {
          handleCloseSiteContextMenu();
          openRenameDialog(row);
        },
      },
      {
        id: "delete-site",
        group: "danger",
        label: deleteTargetCount > 1 ? "Delete Selected Sites" : "Delete",
        icon: DeleteRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description:
          deleteTargetCount > 1
            ? `Delete ${deleteTargetCount} selected site records.`
            : "Delete this site record.",
        onClick: () => handleOpenDeleteDialog(row),
      },
    ];
  }, [
    handleCloseSiteContextMenu,
    handleOpenDeleteDialog,
    handleOpenDevicesForSite,
    handleOpenOnboardingForSite,
    handleOpenSoftwareAuditForSite,
    openRenameDialog,
    openAutoApprovalDialog,
    selectedSiteRows.length,
    selectedIds,
    siteContextMenu.row,
  ]);
  const groupedSiteContextActions = useMemo(
    () =>
      SITE_CONTEXT_MENU_GROUP_ORDER.map((groupId) => ({
        id: groupId,
        label: SITE_CONTEXT_MENU_GROUP_LABELS[groupId],
        actions: siteContextActions.filter((action) => action.group === groupId),
      })).filter((group) => group.actions.length),
    [siteContextActions]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  const gridContext = useMemo(
    () => ({
      navigate,
    }),
    [navigate]
  );

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
        borderRadius: 0,
        border: "none",
        background: "transparent",
        boxShadow: "none",
        overflow: "hidden",
      }}
      elevation={0}
    >
      <Menu
        anchorEl={installMenuAnchorEl}
        open={Boolean(installMenuAnchorEl)}
        onClose={handleCloseInstallMenu}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{ sx: INSTALL_MENU_PAPER_SX }}
      >
        {INSTALL_OS_OPTIONS.map((option) => (
          <MenuItem
            key={option.id}
            onClick={() => void handleSelectInstallOs(option.id)}
            sx={INSTALL_MENU_ITEM_SX}
          >
            {option.label}
          </MenuItem>
        ))}
        <Divider sx={{ my: 0.55, borderColor: "rgba(148,163,184,0.16)" }} />
        <MenuItem onClick={handleOpenBranchDialog} sx={{ ...INSTALL_MENU_ITEM_SX, alignItems: "flex-start", gap: 1 }}>
          <AltRouteRoundedIcon sx={{ mt: 0.15, fontSize: 18, color: "rgba(226,232,240,0.92)" }} />
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.92rem", fontWeight: 700, lineHeight: 1.2 }}>
              Switch Branch
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem", lineHeight: 1.25, mt: 0.25 }}>
              Current: {selectedInstallBranch}
            </Typography>
          </Box>
        </MenuItem>
      </Menu>
      <Menu
        open={Boolean(siteContextMenu.open)}
        onClose={handleCloseSiteContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={siteContextMenu.open ? { top: siteContextMenu.top, left: siteContextMenu.left } : undefined}
        PaperProps={{ sx: SITE_CONTEXT_MENU_PAPER_SX }}
      >
        <Box component="li" role="presentation" sx={SITE_CONTEXT_MENU_HEADER_SX}>
          <Box sx={SITE_CONTEXT_MENU_HEADER_ICON_SX}>
            <LocationCityIcon sx={{ fontSize: 19, color: "currentColor" }} />
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Tooltip title={siteContextRow?.name || "Site"} placement="top-start">
              <Typography
                sx={{
                  ...SITE_CONTEXT_MENU_TRUNCATE_SX,
                  color: MAGIC_UI.textBright,
                  fontSize: "0.88rem",
                  fontWeight: 600,
                  lineHeight: 1.2,
                  maxWidth: 240,
                }}
              >
                {siteContextRow?.name || "Site"}
              </Typography>
            </Tooltip>
            <Tooltip title={siteContextSubtitle} placement="top-start">
              <Typography
                sx={{
                  ...SITE_CONTEXT_MENU_TRUNCATE_SX,
                  color: "rgba(148,163,184,0.82)",
                  fontSize: "0.73rem",
                  lineHeight: 1.25,
                  mt: 0.22,
                  maxWidth: 240,
                }}
              >
                {siteContextSubtitle}
              </Typography>
            </Tooltip>
          </Box>
        </Box>
        {groupedSiteContextActions.map((group) => (
          <React.Fragment key={group.id}>
            <Divider component="li" sx={SITE_CONTEXT_MENU_DIVIDER_SX} />
            <Box component="li" role="presentation" sx={SITE_CONTEXT_MENU_SECTION_LABEL_SX}>{group.label}</Box>
            {group.actions.map((action) => {
              const Icon = action.icon;
              const helperText = action.disabledReason || action.description || "";
              return (
                <MenuItem
                  key={action.id}
                  disabled={Boolean(action.disabled)}
                  onClick={() => action.onClick?.()}
                  sx={action.intent === "danger" ? SITE_CONTEXT_MENU_DANGER_ITEM_SX : SITE_CONTEXT_MENU_ITEM_SX}
                >
                  <Icon
                    sx={{
                      ...SITE_CONTEXT_MENU_ROW_ICON_SX,
                      color: action.intent === "danger" ? "rgba(248,113,113,0.92)" : "rgba(226,232,240,0.92)",
                    }}
                  />
                  <Box
                    sx={{
                      flex: 1,
                      minWidth: 0,
                      display: "flex",
                      flexDirection: "column",
                      justifyContent: helperText ? "flex-start" : "center",
                    }}
                  >
                    <Typography sx={SITE_CONTEXT_MENU_LABEL_SX}>{action.label}</Typography>
                    {helperText ? <Typography sx={SITE_CONTEXT_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
                  </Box>
                </MenuItem>
              );
            })}
          </React.Fragment>
        ))}
      </Menu>
      <PageBodyFrame variant="grid">
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          {loadError ? (
            <Alert severity="error" sx={{ mx: 2, mt: 2, mb: 0 }}>
              {loadError}
            </Alert>
          ) : null}
          <Box
            className={themeClassName}
            sx={{
              flexGrow: 1,
              minHeight: 0,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "& .ag-root-wrapper": {
                minHeight: "100%",
                border: "none",
                borderRadius: 0,
                background: "transparent",
              },
              "& .ag-header": {
                backgroundColor: "rgba(15,23,42,0.9)",
                borderBottom: "1px solid rgba(148,163,184,0.25)",
              },
              "& .ag-header-cell-label": {
                color: "#e2e8f0",
                fontWeight: 600,
                letterSpacing: 0.3,
              },
              "& .ag-row": {
                borderColor: "rgba(255,255,255,0.04)",
                transition: "background 0.2s ease",
              },
              "& .ag-row:nth-of-type(even)": {
                backgroundColor: "rgba(15,23,42,0.45)",
              },
              "& .ag-row-hover": {
                backgroundColor: "rgba(73,156,196,0.2) !important",
              },
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.2) !important",
                boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
              },
              "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
                display: "flex",
                alignItems: "center",
              },
              "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
                width: "100%",
                height: "100%",
                display: "flex",
                alignItems: "center",
                paddingTop: 0,
                paddingBottom: 0,
              },
              "& .ag-center-cols-container .ag-cell.center-col, & .ag-pinned-left-cols-container .ag-cell.center-col, & .ag-pinned-right-cols-container .ag-cell.center-col": {
                justifyContent: "center",
                textAlign: "center",
              },
              "& .ag-center-cols-container .ag-cell.center-col .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.center-col .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.center-col .ag-cell-wrapper": {
                justifyContent: "center",
              },
              "& .ag-cell-value": {
                width: "100%",
                height: "100%",
                display: "flex",
                alignItems: "center",
              },
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={displayRows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              context={gridContext}
              rowSelection={rowSelection}
              selectionColumnDef={selectionColumnDef}
              suppressCellFocus
              suppressContextMenu
              preventDefaultOnContextMenu
              rowHeight={BASE_ROW_HEIGHT}
              getRowHeight={(params) =>
                BASE_ROW_HEIGHT * Math.max(1, Array.isArray(params?.data?.assigned_task_groups) ? params.data.assigned_task_groups.length : 0)
              }
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              getRowId={getRowId}
              onGridReady={(params) => {
                gridApiRef.current = params.api;
                autoSizeColumns();
              }}
              onGridPreDestroyed={() => {
                gridApiRef.current = null;
                if (autoSizeHandleRef.current != null) {
                  if (typeof cancelAnimationFrame === "function") {
                    cancelAnimationFrame(autoSizeHandleRef.current);
                  } else {
                    clearTimeout(autoSizeHandleRef.current);
                  }
                  autoSizeHandleRef.current = null;
                }
              }}
              onSelectionChanged={() => {
                const api = gridApiRef.current || gridRef.current?.api;
                if (!api) return;
                const selected = api.getSelectedNodes().map((n) => n.data?.id).filter((id) => id != null);
                setSelectedIds(new Set(selected));
              }}
              onCellContextMenu={(params) => handleOpenSiteContextMenu(params.event, params.data, params.node)}
              onFirstDataRendered={autoSizeColumns}
              onRowDataUpdated={autoSizeColumns}
              theme={myTheme}
            />
          </Box>
        </Box>
      </PageBodyFrame>

      <CreateSiteDialog
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreate={async (name, description) => {
          try {
            const res = await fetch("/api/sites", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name, description }),
            });
            if (res.ok) {
              setCreateOpen(false);
              if (name) {
                sendNotification(`Site ${name} Created Successfully`);
              }
              fetchSites();
            }
          } catch {}
        }}
      />

      <SiteDeleteDialog
        open={deleteOpen}
        onCancel={() => setDeleteOpen(false)}
        sites={selectedSiteRows}
        onConfirm={async () => {
          try {
            const ids = selectedSiteRows.map((row) => row.id);
            const selectedNames = selectedSiteRows.map((row) => row?.name).filter(Boolean);
            const resp = await fetch("/api/sites/delete", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ ids }),
            });
            if (resp.ok) {
              selectedNames.forEach((name) => sendNotification(`Site ${name} Deleted Successfully`));
            }
          } catch {}
          setDeleteOpen(false);
          setSelectedIds(new Set());
          fetchSites();
        }}
      />

      <RenameSiteDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => {
          setRenameOpen(false);
          setRenameSiteId(null);
        }}
        onSave={async () => {
          const newName = (renameValue || "").trim();
          if (!newName) return;
          const selId = renameSiteId ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
          if (!selId) return;
          const oldName = rows.find((r) => r.id === selId)?.name || "Site";
          try {
            const res = await fetch("/api/sites/rename", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: selId, new_name: newName }),
            });
            if (res.ok) {
              setRenameOpen(false);
              setRenameSiteId(null);
              sendNotification(`Site ${oldName} Renamed as ${newName} Successfully`);
              fetchSites();
            }
          } catch {}
        }}
      />

      <Dialog
        open={autoApprovalOpen}
        onClose={() => {
          if (!autoApprovalSaving) setAutoApprovalOpen(false);
        }}
        maxWidth="xs"
        fullWidth
        PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
          Site Auto-Approval
        </DialogTitle>
        <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mb: 2 }}>
            {autoApprovalSite?.name || "Selected site"}
          </Typography>
          {autoApprovalError ? <Alert severity="error" sx={{ mb: 2 }}>{autoApprovalError}</Alert> : null}
          <LocalizationProvider dateAdapter={AdapterDayjs}>
            <DateTimePicker
              label="Approve Until"
              value={autoApprovalUntil}
              onChange={(nextValue) => setAutoApprovalUntil(nextValue)}
              minDateTime={dayjs()}
              slotProps={{
                textField: {
                  fullWidth: true,
                  size: "small",
                },
              }}
            />
          </LocalizationProvider>
        </DialogContent>
        <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
          <Button
            sx={SITE_DIALOG_BUTTON_SX}
            disabled={autoApprovalSaving || !autoApprovalSite?.auto_approval_active}
            onClick={async () => {
              if (!autoApprovalSite?.id) return;
              setAutoApprovalSaving(true);
              setAutoApprovalError("");
              try {
                const resp = await fetch(`/api/sites/${encodeURIComponent(autoApprovalSite.id)}/auto-approval`, {
                  method: "POST",
                  credentials: "include",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({ auto_approve_until: null }),
                });
                const body = await resp.json().catch(() => ({}));
                if (!resp.ok) throw new Error(body?.message || body?.error || `HTTP ${resp.status}`);
                setAutoApprovalOpen(false);
                await sendNotification(`Site ${autoApprovalSite.name} Auto-Approval Disabled`);
                fetchSites();
              } catch (err) {
                setAutoApprovalError(String(err?.message || err || "Unable to disable auto-approval"));
              } finally {
                setAutoApprovalSaving(false);
              }
            }}
          >
            Disable
          </Button>
          <Button sx={SITE_DIALOG_BUTTON_SX} onClick={() => setAutoApprovalOpen(false)} disabled={autoApprovalSaving}>
            Cancel
          </Button>
          <Button
            sx={SITE_DIALOG_BUTTON_SX}
            disabled={autoApprovalSaving || !autoApprovalUntil?.isValid?.()}
            onClick={async () => {
              if (!autoApprovalSite?.id || !autoApprovalUntil?.isValid?.()) return;
              setAutoApprovalSaving(true);
              setAutoApprovalError("");
              try {
                const resp = await fetch(`/api/sites/${encodeURIComponent(autoApprovalSite.id)}/auto-approval`, {
                  method: "POST",
                  credentials: "include",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({ auto_approve_until: autoApprovalUntil.unix() }),
                });
                const body = await resp.json().catch(() => ({}));
                if (!resp.ok) throw new Error(body?.message || body?.error || `HTTP ${resp.status}`);
                setAutoApprovalOpen(false);
                await sendNotification(`Site ${autoApprovalSite.name} Auto-Approval Updated`);
                fetchSites();
              } catch (err) {
                setAutoApprovalError(String(err?.message || err || "Unable to update auto-approval"));
              } finally {
                setAutoApprovalSaving(false);
              }
            }}
          >
            {autoApprovalSaving ? "Saving..." : "Save"}
          </Button>
        </DialogActions>
      </Dialog>

      <InstallBranchDialog
        open={branchDialogOpen}
        rows={branchRows}
        loading={branchesLoading}
        error={branchLoadError}
        draftBranch={draftInstallBranch}
        onDraftBranchChange={setDraftInstallBranch}
        onRefresh={() => void fetchInstallBranches()}
        onCancel={handleCloseBranchDialog}
        onApply={() => void handleApplyInstallBranch()}
      />
    </Paper>
  );
}
