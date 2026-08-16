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
  IconButton,
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
import DevicesIcon from "@mui/icons-material/Devices";
import CheckCircleOutlineRoundedIcon from "@mui/icons-material/CheckCircleOutlineRounded";
import KeyboardArrowDownRoundedIcon from "@mui/icons-material/KeyboardArrowDownRounded";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreateSiteDialog, RenameSiteDialog } from "../Dialogs.jsx";
import { buildRowContextMenuColumnDef, GridRowContextMenuButtonCell } from "../Grid_Row_Context_Menu_Button.jsx";
import RowContextMenu from "../Row_Context_Menu.jsx";
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
const SITE_CONNECTION_STABILITY_GRACE_SECONDS = 20;
const BASE_ROW_HEIGHT = 56;
const INSTALL_LINK_DETAIL_ROW_HEIGHT = 116;
const NAV_SUBSECTION_ROW_BG = "#0f141c";
const SITE_DESCRIPTION_SUBTEXT_COLOR = "rgba(148,163,184,0.72)";
const INSTALL_LINK_HEADER_BG = "#123a5a";
const INSTALL_LINK_STATUS_TONES = {
  upToDate: { label: "Up-to-Date", color: "#34d399" },
  compiling: { label: "Compiling...", color: "#fbbf24" },
  outOfDate: { label: "Out-of-Date", color: "#f87171" },
  sending: { label: "Sending to Site Worker...", color: "#7dd3fc" },
};
const SITE_WORKER_CONTAINER_COLUMN_ID = "site_worker_container_id";
const CONNECTED_DEVICES_COLUMN_ID = "connected_devices";
const RUNNING_TASKS_COLUMN_ID = "assigned_task_groups";
const AUTO_SIZE_COLUMNS = [SITE_WORKER_CONTAINER_COLUMN_ID, CONNECTED_DEVICES_COLUMN_ID];
export const SITE_WORKER_NAME_PREFIX = "site-worker-";
export const SITE_WORKER_KUBERNETES_NAME_MAX = 63;
export const SITE_WORKER_SITE_SLUG_MAX = SITE_WORKER_KUBERNETES_NAME_MAX - SITE_WORKER_NAME_PREFIX.length;

export function siteWorkerSiteSlug(siteName) {
  const normalized = String(siteName || "").trim().toLowerCase();
  let slug = "";
  let lastWasSeparator = false;
  for (const item of normalized) {
    const code = item.charCodeAt(0);
    const isAlpha = code >= 97 && code <= 122;
    const isNumber = code >= 48 && code <= 57;
    if (isAlpha || isNumber) {
      slug += item;
      lastWasSeparator = false;
      continue;
    }
    if (item === " " || item === "\t" || item === "\n" || item === "\r" || item === "-" || item === "_") {
      if (slug.length > 0 && !lastWasSeparator) {
        slug += "-";
        lastWasSeparator = true;
      }
    }
  }
  return slug.replace(/^-+|-+$/g, "");
}

export function validateSiteWorkerSiteName(siteName, sites = [], excludeSiteId = null) {
  const siteSlug = siteWorkerSiteSlug(siteName);
  if (!siteSlug) {
    return {
      title: "Site Name Invalid",
      message: "Site name must contain at least one ASCII letter or number.",
    };
  }
  if (siteSlug.length > SITE_WORKER_SITE_SLUG_MAX) {
    return {
      title: "Site Name Too Long",
      message: `Site name creates <b>${siteSlug.length}</b> site-worker slug characters. Maximum is <b>${SITE_WORKER_SITE_SLUG_MAX}</b>.`,
    };
  }
  const conflict = (sites || []).find((site) => {
    const siteID = site?.id ?? site?.site_id;
    if (excludeSiteId != null && String(siteID) === String(excludeSiteId)) {
      return false;
    }
    return siteWorkerSiteSlug(site?.name) === siteSlug;
  });
  if (conflict) {
    return {
      title: "Site Name Already Used",
      message: `Site name maps to existing worker name <b>${SITE_WORKER_NAME_PREFIX}${siteSlug}</b>. Rename <b>${conflict?.name || "the existing site"}</b> first.`,
    };
  }
  return null;
}

function siteMutationErrorMessage(payload, fallback) {
  return String(payload?.message || payload?.error || fallback || "Site update failed.");
}
const BOREALIS_LINK_COLOR = "#58a6ff";
const BOREALIS_LINK_HOVER_COLOR = "#7dd3fc";
const INSTALL_LINK_PLATFORM_OPTIONS = [
  { id: "windows", platform: "windows-amd64", label: "Windows", shortLabel: "Win" },
  { id: "linux", platform: "linux-amd64", label: "Linux", shortLabel: "Linux" },
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
  { key: "disconnected", label: "Reconnecting", color: "#ffb347" },
  { key: "offline", label: "Offline", color: "#6b7280" },
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

function siteIdForRow(row) {
  const siteId = Number(row?.site_id || row?.id || 0);
  return Number.isFinite(siteId) && siteId > 0 ? siteId : 0;
}

function installLinkDetailRowId(siteId) {
  return `install-links:${String(siteId || "").trim()}`;
}

function isInstallLinkDetailRow(row) {
  return Boolean(row?.__installLinkDetail);
}

export function insertExpandedInstallLinkRows(siteRows, expandedSiteId = null) {
  const rows = Array.isArray(siteRows) ? siteRows : [];
  const expandedId = String(expandedSiteId ?? "").trim();
  if (!expandedId) {
    return rows;
  }
  const nextRows = [];
  rows.forEach((row) => {
    nextRows.push(row);
    const siteId = siteIdForRow(row);
    if (siteId > 0 && String(siteId) === expandedId) {
      nextRows.push({
        id: installLinkDetailRowId(siteId),
        site_id: siteId,
        parent_site_id: siteId,
        __installLinkDetail: true,
        site: row,
      });
    }
  });
  return nextRows;
}

export function buildDeviceListSiteStatusPath(site, statusKey = "") {
  const siteId = siteIdForRow(site);
  if (!siteId) return "";
  const params = new URLSearchParams();
  params.set("site", String(siteId));
  const normalizedStatus = String(statusKey || "").trim().toLowerCase();
  if (CONNECTION_SECTIONS.some((section) => section.key === normalizedStatus)) {
    params.set("status", normalizedStatus);
  }
  return `${APP_PATHS.devices}?${params.toString()}`;
}

function normalizedConnectionCounts(row) {
  const connected = Math.max(0, Number(row?.connected_devices || 0));
  const disconnected = Math.max(0, Number(row?.disconnected_devices || 0));
  const offline = Math.max(0, Number(row?.offline_devices || 0));
  const total = Math.max(
    0,
    Number(row?.site_device_count || 0),
    Number(row?.device_count || 0),
    connected + disconnected + offline
  );
  const online = Math.max(
    connected,
    Math.min(total, Math.max(Number(row?.site_online_device_count || 0), connected + disconnected))
  );
  return {
    connected,
    disconnected: Math.max(0, online - connected),
    offline: Math.max(0, total - online),
    total,
    online,
  };
}

export function recordSiteConnectionSnapshots(snapshotMap, rows, nowSeconds = Math.floor(Date.now() / 1000)) {
  if (!(snapshotMap instanceof Map) || !Array.isArray(rows)) return 0;
  const now = Number.isFinite(Number(nowSeconds)) ? Number(nowSeconds) : Math.floor(Date.now() / 1000);
  const visibleSiteIds = new Set();
  let changed = 0;

  rows.forEach((row) => {
    const siteId = siteIdForRow(row);
    if (!siteId) return;
    visibleSiteIds.add(siteId);
    const counts = normalizedConnectionCounts(row);
    if (counts.connected <= 0 || counts.total <= 0) return;
    const nextSnapshot = {
      connected_devices: counts.connected,
      disconnected_devices: counts.disconnected,
      offline_devices: counts.offline,
      site_device_count: counts.total,
      site_online_device_count: counts.online,
      sampled_at: now,
    };
    const previous = snapshotMap.get(siteId);
    if (
      !previous ||
      previous.connected_devices !== nextSnapshot.connected_devices ||
      previous.disconnected_devices !== nextSnapshot.disconnected_devices ||
      previous.offline_devices !== nextSnapshot.offline_devices ||
      previous.site_device_count !== nextSnapshot.site_device_count ||
      previous.site_online_device_count !== nextSnapshot.site_online_device_count ||
      previous.sampled_at !== nextSnapshot.sampled_at
    ) {
      snapshotMap.set(siteId, nextSnapshot);
      changed += 1;
    }
  });

  snapshotMap.forEach((snapshot, siteId) => {
    const age = now - Number(snapshot?.sampled_at || 0);
    if (!visibleSiteIds.has(siteId) || age > SITE_CONNECTION_STABILITY_GRACE_SECONDS) {
      snapshotMap.delete(siteId);
      changed += 1;
    }
  });
  return changed;
}

export function applySiteConnectionStability(rows, snapshotMap, nowSeconds = Math.floor(Date.now() / 1000)) {
  if (!Array.isArray(rows) || !(snapshotMap instanceof Map)) return rows;
  const now = Number.isFinite(Number(nowSeconds)) ? Number(nowSeconds) : Math.floor(Date.now() / 1000);
  return rows.map((row) => {
    const siteId = siteIdForRow(row);
    const snapshot = siteId ? snapshotMap.get(siteId) : null;
    if (!snapshot) return row;
    const current = normalizedConnectionCounts(row);
    if (current.connected > 0) return row;
    const age = Math.max(0, now - Number(snapshot.sampled_at || 0));
    if (age > SITE_CONNECTION_STABILITY_GRACE_SECONDS) return row;

    const snapshotConnected = Math.max(0, Number(snapshot.connected_devices || 0));
    if (snapshotConnected <= 0) return row;
    const total = Math.max(
      current.total,
      Number(snapshot.site_device_count || 0),
      snapshotConnected + Number(snapshot.disconnected_devices || 0) + Number(snapshot.offline_devices || 0)
    );
    if (total <= 0) return row;
    const connected = Math.min(total, snapshotConnected);
    const online = Math.max(
      connected,
      Math.min(
        total,
        Math.max(Number(snapshot.site_online_device_count || 0), connected + Number(snapshot.disconnected_devices || 0))
      )
    );
    return {
      ...row,
      connected_devices: connected,
      site_device_count: total,
      device_count: Math.max(Number(row?.device_count || 0), total),
      site_online_device_count: online,
      disconnected_devices: Math.max(0, online - connected),
      offline_devices: Math.max(0, total - online),
      site_connection_stabilized: true,
      site_connection_stabilized_remaining_seconds: Math.max(
        0,
        Math.ceil(SITE_CONNECTION_STABILITY_GRACE_SECONDS - age)
      ),
    };
  });
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

function isActiveSiteWorkerRow(row) {
  const status = String(row?.site_worker_status || row?.raw_site_worker?.status || "").trim().toLowerCase();
  return Boolean(row?.site_worker_guid || row?.site_worker_container_name) && ["starting", "running", "idle"].includes(status);
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

export function siteListAssignedTaskGroupCount(row) {
  if (isInstallLinkDetailRow(row)) {
    return 0;
  }
  return Math.max(0, Array.isArray(row?.assigned_task_groups) ? row.assigned_task_groups.length : 0);
}

export function siteListRowHeightForData(row) {
  if (isInstallLinkDetailRow(row)) {
    return INSTALL_LINK_DETAIL_ROW_HEIGHT;
  }
  return BASE_ROW_HEIGHT * Math.max(1, siteListAssignedTaskGroupCount(row));
}

export function siteListRowHeightSignature(rows) {
  if (!Array.isArray(rows)) return "";
  return rows
    .map((row) => `${String(row?.id ?? row?.site_id ?? "")}:${isInstallLinkDetailRow(row) ? "install-links" : siteListAssignedTaskGroupCount(row)}`)
    .join("|");
}

export function shouldShowConnectedDevicesPlaceholder(row) {
  return !Boolean(row?.site_worker_payload_ready);
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
        site_worker_status: String(worker?.status || "").trim(),
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
        site_worker_status: "",
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
    row?.site_worker_payload_ready ? "ready" : "pending",
    row?.site_worker_container_id,
    row?.site_worker_status,
    row?.site_worker_resource_history_key,
    row?.raw_site_worker?.container_metrics_source,
    stats?.source,
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

function normalizeInstallEngineCA(payload) {
  const caPayload = payload?.engine_ca || payload?.public_edge?.local_ca || payload?.local_ca || null;
  const pemB64 = String(caPayload?.pem_b64 || "").trim();
  if (!pemB64) {
    return null;
  }
  return {
    pem_b64: pemB64,
    required: Boolean(payload?.engine_ca_required || caPayload?.enabled),
  };
}

function normalizeInstallServerIPFallback(payload) {
  const text = String(payload?.server_ip_fallback || payload?.public_edge?.server_ip_fallback || "").trim();
  if (!text || text.includes("://") || text.includes("/")) {
    return "";
  }
  const parts = text.split(".");
  if (parts.length !== 4) {
    return "";
  }
  const octets = parts.map((part) => Number(part));
  if (octets.some((octet, index) => !Number.isInteger(octet) || octet < 0 || octet > 255 || String(octet) !== parts[index])) {
    return "";
  }
  if (
    text === "0.0.0.0" ||
    octets[0] === 127 ||
    octets[0] >= 224
  ) {
    return "";
  }
  return text;
}

export async function loadSiteListPageData(request) {
  const progress = createRouteRequestPlan(request, 4);
  try {
    await requireAuthenticatedRequest(request, progress);
    const data = await progress.fetchJson("/api/sites");
    let installServerUrl = deriveInstallServerUrl(data);
    let installEngineCA = normalizeInstallEngineCA(data);
    let installServerIPFallback = normalizeInstallServerIPFallback(data);
    const requiresEngineCA = Boolean(data?.engine_ca_required || data?.deployment_profile === "internal-only");
    if (!installServerUrl || (requiresEngineCA && (!installEngineCA || !installServerIPFallback))) {
      try {
        const overviewPayload = await progress.fetchJson("/api/server/overview");
        installServerUrl = installServerUrl || deriveInstallServerUrl({
          public_base_url:
            overviewPayload?.host?.public_base_url || overviewPayload?.public_edge?.public_base_url || "",
          public_hostname:
            overviewPayload?.host?.public_hostname || overviewPayload?.public_edge?.fqdn || "",
        });
        installEngineCA = installEngineCA || normalizeInstallEngineCA(overviewPayload);
        installServerIPFallback = installServerIPFallback || normalizeInstallServerIPFallback(overviewPayload);
      } catch (error) {
        rethrowIfRouteRedirect(error);
      }
    } else {
      progress.skip(1);
    }

    return {
      rows: Array.isArray(data?.sites) ? data.sites : [],
      siteWorkerPayload: {},
      installServerUrl,
      installEngineCA,
      installServerIPFallback,
      agentInstallArtifact: data?.agent_install_artifact || null,
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      rows: [],
      siteWorkerPayload: {},
      installServerUrl: "",
      installEngineCA: null,
      installServerIPFallback: "",
      agentInstallArtifact: null,
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
  if (!row.site_worker_payload_ready) {
    return (
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%", height: "100%" }}>
        <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
          Polling Site Worker Metrics
        </Typography>
      </Box>
    );
  }
  if (!hasDockerStats(row.site_worker_docker_stats)) {
    const activeWorker = isActiveSiteWorkerRow(row);
    return (
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%", height: "100%" }}>
        <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
          {activeWorker ? "Worker Metrics Unavailable" : "Site Worker Not Running"}
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
  const source = String(stats?.source || params?.data?.raw_site_worker?.container_metrics_source || "").trim();
  const kubernetesStats = source === "metrics.k8s.io";
  const sourceLabel = kubernetesStats ? "K3s Metrics Server" : "Docker";
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
      tooltip: `${sourceLabel} | Current ${formatDockerStatPercent(latest.cpuPercent)} | 60s max ${formatDockerStatPercent(cpuMax)}`,
      color: "#7dd3fc",
      valueKey: "cpuPercent",
      maxValue: cpuMax,
    },
    {
      label: "RAM",
      value: formatDockerStatBytes(latest.memoryUsageBytes),
      scaleLabel: latest.memoryLimitBytes > 0 ? formatDockerStatBytes(latest.memoryLimitBytes) : formatDockerStatBytes(ramMax),
      tooltip: latest.memoryLimitBytes > 0
        ? `${sourceLabel} | ${formatDockerStatBytes(latest.memoryUsageBytes)} / ${formatDockerStatBytes(latest.memoryLimitBytes)}`
        : `${sourceLabel} | ${formatDockerStatBytes(latest.memoryUsageBytes)} | 60s max ${formatDockerStatBytes(ramMax)}`,
      color: "#c084fc",
      valueKey: "memoryUsageBytes",
      maxValue: ramMax,
    },
  ];
  if (!kubernetesStats) {
    metrics.push(
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
    );
  }
  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        display: "grid",
        gridTemplateColumns: kubernetesStats ? "repeat(2, minmax(0, 1fr))" : "repeat(4, minmax(0, 1fr))",
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
  if (shouldShowConnectedDevicesPlaceholder(params?.data)) {
    return (
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%", height: "100%" }}>
        <Typography sx={{ color: "rgba(148,163,184,0.82)", fontSize: 12, fontFamily: gridFontFamily }}>
          Analyzing Agent Connections
        </Typography>
      </Box>
    );
  }
  const connected = Number(params?.data?.connected_devices || 0);
  const disconnected = Number(params?.data?.disconnected_devices || 0);
  const offline = Number(params?.data?.offline_devices || 0);
  const total = Math.max(0, Number(params?.data?.site_device_count || 0), connected + disconnected + offline);
  const counts = { connected, disconnected, offline };
  const openDevicesForSiteStatus = params?.context?.openDevicesForSiteStatus;
  const handleOpenStatus = (event, section) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    openDevicesForSiteStatus?.(params?.data, section.key);
  };
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
        "@keyframes siteConnectivityPulse": {
          "0%": { transform: "scaleY(1)" },
          "100%": { transform: "scaleY(1.55)" },
        },
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
              component="button"
              type="button"
              aria-label={`Show ${section.label} devices for this site`}
              onMouseDown={stopGridRowSelectionEvent}
              onClick={(event) => handleOpenStatus(event, section)}
              sx={{
                display: "block",
                height: "100%",
                width: `${Math.max(4, Math.round((value / total) * 100))}%`,
                backgroundColor: section.color,
                border: 0,
                p: 0,
                m: 0,
                minWidth: 4,
                cursor: "pointer",
                transformOrigin: "center",
                transition: "filter 160ms ease, transform 160ms ease, box-shadow 160ms ease",
                "&:hover, &:focus-visible": {
                  animation: "siteConnectivityPulse 720ms ease-in-out infinite alternate",
                  filter: "brightness(1.18)",
                  outline: "none",
                  boxShadow: "0 0 10px rgba(125,211,252,0.45)",
                  zIndex: 1,
                },
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
          <Box
            key={section.key}
            component="button"
            type="button"
            aria-label={`Show ${section.label} devices for this site`}
            onMouseDown={stopGridRowSelectionEvent}
            onClick={(event) => handleOpenStatus(event, section)}
            sx={{
              display: "inline-flex",
              alignItems: "center",
              gap: 0.35,
              border: 0,
              p: 0,
              m: 0,
              color: "inherit",
              background: "transparent",
              font: "inherit",
              cursor: "pointer",
              borderRadius: 0.75,
              transition: "color 160ms ease, transform 160ms ease",
              "&:hover, &:focus-visible": {
                color: MAGIC_UI.textBright,
                outline: "none",
                transform: "translateY(-0.5px)",
              },
            }}
          >
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

function quotePowerShellValue(value) {
  return `"${escapePowerShellDoubleQuoted(value)}"`;
}

function installEngineCAB64(engineCA) {
  if (!engineCA) {
    return "";
  }
  if (typeof engineCA === "string") {
    return String(engineCA || "").trim();
  }
  return String(engineCA?.pem_b64 || "").trim();
}

function engineInstallDownloadUrl(osId, serverUrl, downloads = {}) {
  const item = downloads?.[osId] || null;
  const rawUrl = String(item?.url || "").trim();
  if (/^https?:\/\//i.test(rawUrl)) {
    return rawUrl;
  }
  const rawPath = String(item?.path || "").trim();
  if (rawPath.startsWith("/")) {
    const normalizedServerUrl = normalizeInstallServerUrl(serverUrl);
    return normalizedServerUrl ? `${normalizedServerUrl}${rawPath}` : "";
  }
  return "";
}

function installDownloadUrlForLink(link, serverUrl) {
  const rawUrl = String(link?.url || "").trim();
  if (/^https?:\/\//i.test(rawUrl)) {
    return rawUrl;
  }
  const rawPath = String(link?.path || "").trim();
  if (rawPath.startsWith("/")) {
    const normalizedServerUrl = normalizeInstallServerUrl(serverUrl);
    return normalizedServerUrl ? `${normalizedServerUrl}${rawPath}` : rawPath;
  }
  return "";
}

function unixTimestampValue(value) {
  const seconds = Number(value || 0);
  return Number.isFinite(seconds) && seconds > 0 ? seconds : 0;
}

function formatInstallLinkTimestamp(value, fallback = "Never") {
  const seconds = unixTimestampValue(value);
  if (!seconds) {
    return fallback;
  }
  try {
    const timestamp = new Date(seconds * 1000);
    const date = timestamp.toLocaleDateString("en-US", {
      month: "2-digit",
      day: "2-digit",
      year: "numeric",
    });
    const time = timestamp.toLocaleTimeString("en-US", {
      hour: "numeric",
      minute: "2-digit",
    });
    return `${date} @ ${time}`;
  } catch {
    return fallback;
  }
}

function formatInstallLinkAge(value, nowSeconds = Math.floor(Date.now() / 1000)) {
  const seconds = unixTimestampValue(value);
  if (!seconds) {
    return "";
  }
  const elapsed = Math.max(0, Number(nowSeconds || 0) - seconds);
  const units = [
    { size: 31536000, label: "Year" },
    { size: 604800, label: "Week" },
    { size: 86400, label: "Day" },
    { size: 3600, label: "Hour" },
    { size: 60, label: "Minute" },
  ];
  const unit = units.find((item) => elapsed >= item.size) || units[units.length - 1];
  const count = Math.max(1, Math.floor(elapsed / unit.size));
  return `${count} ${unit.label}${count === 1 ? "" : "s"} Ago`;
}

function formatInstallCompileDate(value, nowSeconds = Math.floor(Date.now() / 1000), fallback = "Unavailable") {
  const timestamp = formatInstallLinkTimestamp(value, fallback);
  if (timestamp === fallback) {
    return fallback;
  }
  const age = formatInstallLinkAge(value, nowSeconds);
  return age ? `${timestamp} (${age})` : timestamp;
}

export function siteInstallLinkForOS(site, osId) {
  const downloads = site?.agent_install_downloads || {};
  const platform = INSTALL_LINK_PLATFORM_OPTIONS.find((option) => option.id === osId)?.platform || "";
  return downloads?.[osId] || downloads?.[platform] || null;
}

function isActiveSiteInstallLink(link, nowSeconds = Math.floor(Date.now() / 1000)) {
  if (!link || typeof link !== "object") {
    return false;
  }
  const expiresAt = unixTimestampValue(link.expires_at);
  const revokedAt = unixTimestampValue(link.revoked_at);
  return Boolean((link.url || link.path) && link.active !== false && revokedAt === 0 && expiresAt > nowSeconds);
}

export function siteInstallLinkRows(site, nowSeconds = Math.floor(Date.now() / 1000)) {
  return INSTALL_LINK_PLATFORM_OPTIONS.map((option) => {
    const link = siteInstallLinkForOS(site, option.id);
    return {
      ...option,
      link,
      active: isActiveSiteInstallLink(link, nowSeconds),
      downloadCount: Number(link?.download_count || 0),
      expiresAt: unixTimestampValue(link?.expires_at),
      issuedAt: unixTimestampValue(link?.issued_at),
      lastDownloadedAt: unixTimestampValue(link?.last_downloaded_at),
    };
  });
}

function installLinkStatusTone(id, description = "") {
  const base = INSTALL_LINK_STATUS_TONES[id] || INSTALL_LINK_STATUS_TONES.outOfDate;
  return {
    ...base,
    id,
    description,
  };
}

function installArtifactID(value) {
  return String(value?.artifact_id || value?.artifact || "").trim();
}

export function siteInstallLinkStatus(row, site = {}, artifact = null) {
  const buildStatus = String(artifact?.build_status || "").trim().toLowerCase();
  const artifactAvailable = artifact ? Boolean(artifact.available) : true;
  const engineCacheAvailable = artifact?.engine_cache_available !== false;
  const linkStateAvailable = artifact?.link_state_available !== false;
  const buildError = String(artifact?.build_error || artifact?.last_error || "").trim();
  if (buildStatus === "compiling" || (artifact && !engineCacheAvailable && !buildError)) {
    return installLinkStatusTone("compiling", "Engine is still compiling or validating current Agent artifact.");
  }
  if (buildStatus === "link_state_unavailable" || !linkStateAvailable) {
    return installLinkStatusTone("outOfDate", "Engine link-state table is unavailable.");
  }
  if (buildStatus === "error" || (artifact && !artifactAvailable && buildError)) {
    return installLinkStatusTone("outOfDate", "Engine Agent artifact build or cache validation failed.");
  }
  if (!row?.active) {
    return installLinkStatusTone("outOfDate", "This platform link is expired, revoked, or unavailable.");
  }

  const currentArtifact = installArtifactID(artifact);
  const rowArtifact = installArtifactID(row?.link || row);
  if (currentArtifact && rowArtifact && currentArtifact !== rowArtifact) {
    return installLinkStatusTone("outOfDate", "This platform link points at an older Engine Agent artifact.");
  }

  const workerStatus = String(site?.site_worker_status || site?.raw_site_worker?.status || "").trim().toLowerCase();
  if (site?.site_worker_payload_ready === false || workerStatus === "starting") {
    return installLinkStatusTone("sending", "Engine is waiting for the site worker to report readiness.");
  }
  if (site?.site_worker_payload_ready === true && !isActiveSiteWorkerRow(site)) {
    return installLinkStatusTone("sending", "No active site worker has reported ready state for this site.");
  }

  return installLinkStatusTone("upToDate", "Current Engine Agent artifact, active link, and site-worker state agree.");
}

export function siteInstallLinkGridRows(site, installServerUrl = "", nowSeconds = Math.floor(Date.now() / 1000), artifact = null) {
  return siteInstallLinkRows(site, nowSeconds).map((row) => {
    const link = row.link || {};
    const urlText = installDownloadUrlForLink(link, installServerUrl);
    const compiledAt = unixTimestampValue(
      link?.compiled_at ||
      artifact?.compiled_at
    );
    const gridRow = {
      id: `${siteIdForRow(site) || "site"}:${row.platform}`,
      site,
      osId: row.id,
      agentType: `Install on ${row.label}`,
      platform: row.platform,
      link,
      active: row.active,
      url: urlText,
      artifact: String(link?.artifact_id || link?.artifact || "").trim() || "Unavailable",
      agentCompileDate: compiledAt,
      nowSeconds,
      expiresAt: row.expiresAt,
      issuedAt: row.issuedAt,
      downloadCount: row.downloadCount,
      lastDownloadedAt: row.lastDownloadedAt,
    };
    gridRow.installStatus = siteInstallLinkStatus(gridRow, site, artifact);
    return gridRow;
  });
}

export function siteInstallLinkSummary(site, artifact = null, nowSeconds = Math.floor(Date.now() / 1000)) {
  const rows = siteInstallLinkRows(site, nowSeconds);
  const activeRows = rows.filter((row) => row.active);
  const nearestExpiry = activeRows
    .map((row) => row.expiresAt)
    .filter(Boolean)
    .sort((a, b) => a - b)[0] || 0;
  const linkStateAvailable = artifact?.link_state_available !== false;
  const cacheAvailable = artifact ? Boolean(artifact.available) : true;
  return {
    rows,
    activeCount: activeRows.length,
    totalDownloads: rows.reduce((sum, row) => sum + row.downloadCount, 0),
    nearestExpiry,
    available: activeRows.length === INSTALL_LINK_PLATFORM_OPTIONS.length,
    warning:
      !cacheAvailable ||
      !linkStateAvailable ||
      activeRows.length < INSTALL_LINK_PLATFORM_OPTIONS.length,
    cacheAvailable,
    linkStateAvailable,
  };
}

export function buildInstallCommand(osId, serverUrl, enrollmentCode, engineCA = null, serverIPFallback = "", options = {}) {
  const normalizedServerUrl = normalizeInstallServerUrl(serverUrl);
  const normalizedEnrollmentCode = String(enrollmentCode || "").trim();
  const engineDownloadUrl = engineInstallDownloadUrl(osId, normalizedServerUrl, options?.downloads);
  const engineCAB64 = installEngineCAB64(engineCA);
  const normalizedServerIPFallback = normalizeInstallServerIPFallback({ server_ip_fallback: serverIPFallback });
  if (!normalizedServerUrl || !normalizedEnrollmentCode) {
    return "";
  }
  if (engineCA?.required && !engineCAB64) {
    return "";
  }

  if (osId === "windows") {
    if (engineCA?.required && !normalizedServerIPFallback) {
      return "";
    }
    if (!engineDownloadUrl) return "";
    const caArg = engineCAB64 ? ` --trusted-engine-ca-b64 ${quotePowerShellValue(engineCAB64)}` : "";
    const serverIPFallbackArg = engineCAB64 && normalizedServerIPFallback ? ` --server-ip-fallback ${quotePowerShellValue(normalizedServerIPFallback)}` : "";
    return `$borealisAgent = Join-Path $env:TEMP "Borealis-Agent.exe"; ` +
      `Invoke-WebRequest -UseBasicParsing -Uri ${quotePowerShellValue(engineDownloadUrl)} -OutFile $borealisAgent; ` +
      `& $borealisAgent --server-url ${quotePowerShellValue(normalizedServerUrl)} ` +
      `--site-enrollment-code ${quotePowerShellValue(normalizedEnrollmentCode)}${caArg}${serverIPFallbackArg}`;
  }

  if (osId === "linux") {
    if (engineCA?.required && !normalizedServerIPFallback) {
      return "";
    }
    if (!engineDownloadUrl) return "";
    const caArg = engineCAB64 ? ` --trusted-engine-ca-b64 "${escapeShellDoubleQuoted(engineCAB64)}"` : "";
    const serverIPFallbackArg = engineCAB64 && normalizedServerIPFallback ? ` --server-ip-fallback "${escapeShellDoubleQuoted(normalizedServerIPFallback)}"` : "";
    const launchArgs = `--server-url "${escapeShellDoubleQuoted(normalizedServerUrl)}" ` +
      `--site-enrollment-code "${escapeShellDoubleQuoted(normalizedEnrollmentCode)}"${caArg}${serverIPFallbackArg} --install-service`;
    return `curl -fsSL "${escapeShellDoubleQuoted(engineDownloadUrl)}" -o /tmp/Borealis-Agent; ` +
      `chmod 700 /tmp/Borealis-Agent; ` +
      `sudo /tmp/Borealis-Agent ${launchArgs}`;
  }
  return "";
}

function InstallLinkAgentTypeCell(params) {
  const row = params?.data || {};
  const copyInstallCommand = params?.context?.copyInstallCommand;
  const openInstallLinkContextMenu = params?.context?.openInstallLinkContextMenu;
  const hasDownloadLink = Boolean(row.url);
  const installStatus = row.installStatus || (row.active
    ? installLinkStatusTone("upToDate")
    : installLinkStatusTone("outOfDate"));
  const handleCopyLink = useCallback((event) => {
    stopGridRowSelectionEvent(event);
    if (copyInstallCommand) {
      void copyInstallCommand(row.site, row.osId);
    }
  }, [copyInstallCommand, row.osId, row.site]);
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 0.6, minWidth: 0, width: "100%", height: "100%" }}>
      <Box sx={{ width: 32, flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "center" }}>
        <GridRowContextMenuButtonCell
          params={params}
          onOpenContextMenu={openInstallLinkContextMenu}
          tooltip="Install Link Actions"
        />
      </Box>
      <Box sx={{ display: "flex", flexDirection: "column", justifyContent: "center", minWidth: 0, flex: 1, height: "100%" }}>
        <Tooltip title={hasDownloadLink ? "Copy install command" : "Install command unavailable"} placement="top-start">
          <Box
            component="button"
            type="button"
            disabled={!copyInstallCommand}
            onMouseDown={stopGridRowSelectionEvent}
            onClick={handleCopyLink}
            sx={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "flex-start",
              width: "100%",
              maxWidth: "100%",
              p: 0,
              border: 0,
              background: "transparent",
              color: SITE_DESCRIPTION_SUBTEXT_COLOR,
              cursor: copyInstallCommand ? "pointer" : "default",
              font: "inherit",
              fontSize: "0.82rem",
              fontWeight: 800,
              lineHeight: 1.15,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
              textAlign: "left",
              "&:hover": copyInstallCommand
                ? {
                    color: BOREALIS_LINK_COLOR,
                    textDecoration: "underline",
                  }
                : undefined,
              "&:disabled": {
                opacity: 0.8,
              },
            }}
          >
            {row.agentType || "Install Agent"}
          </Box>
        </Tooltip>
        <Tooltip title={installStatus.description || installStatus.label} placement="top-start">
          <Typography
            sx={{
              mt: 0.15,
              color: installStatus.color,
              fontSize: "0.68rem",
              fontWeight: 800,
              lineHeight: 1.1,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {installStatus.label}
          </Typography>
        </Tooltip>
      </Box>
    </Box>
  );
}

function InstallLinkCompileDateCell(params) {
  const value = formatInstallCompileDate(params?.data?.agentCompileDate, params?.context?.nowSeconds || params?.data?.nowSeconds, "Unavailable");
  return (
    <Tooltip title={value} placement="top-start">
      <Typography
        sx={{
          color: SITE_DESCRIPTION_SUBTEXT_COLOR,
          fontSize: "0.72rem",
          fontWeight: 700,
          lineHeight: 1.2,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          maxWidth: "100%",
        }}
      >
        {value}
      </Typography>
    </Tooltip>
  );
}

function InstallLinkTimestampCell(params) {
  return (
    <Typography sx={{ color: SITE_DESCRIPTION_SUBTEXT_COLOR, fontSize: "0.72rem", fontWeight: 600, lineHeight: 1.2 }}>
      {formatInstallLinkTimestamp(params?.value, "Unavailable")}
    </Typography>
  );
}

function InstallLinkDownloadsCell(params) {
  const row = params?.data || {};
  return (
    <Box sx={{ display: "flex", flexDirection: "column", justifyContent: "center", minWidth: 0, width: "100%", height: "100%" }}>
      <Typography sx={{ color: SITE_DESCRIPTION_SUBTEXT_COLOR, fontSize: "0.78rem", fontWeight: 800, lineHeight: 1.15 }}>
        {Number(row.downloadCount || 0).toLocaleString()}
      </Typography>
      <Typography sx={{ mt: 0.15, color: SITE_DESCRIPTION_SUBTEXT_COLOR, fontSize: "0.66rem", fontWeight: 600, lineHeight: 1.1, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
        Last: {formatInstallLinkTimestamp(row.lastDownloadedAt, "None")}
      </Typography>
    </Box>
  );
}

function buildInstallLinkDetailColumnDefs() {
  return [
    {
      headerName: "Agent Type",
      field: "agentType",
      minWidth: 230,
      flex: 1.1,
      cellRenderer: InstallLinkAgentTypeCell,
    },
    {
      headerName: "Agent Compile Date",
      field: "agentCompileDate",
      minWidth: 230,
      flex: 1.3,
      cellRenderer: InstallLinkCompileDateCell,
    },
    {
      headerName: "Issued",
      field: "issuedAt",
      minWidth: 168,
      flex: 0.85,
      cellRenderer: InstallLinkTimestampCell,
    },
    {
      headerName: "Expires",
      field: "expiresAt",
      minWidth: 168,
      flex: 0.85,
      cellRenderer: InstallLinkTimestampCell,
    },
    {
      headerName: "Downloads",
      field: "downloadCount",
      minWidth: 150,
      flex: 0.72,
      cellRenderer: InstallLinkDownloadsCell,
    },
  ];
}

export function InstallLinksDetailRow(params) {
  const site = params?.data?.site || {};
  const context = params?.context || {};
  const rowData = siteInstallLinkGridRows(
    site,
    context.installServerUrl || "",
    context.nowSeconds || Math.floor(Date.now() / 1000),
    context.agentInstallArtifact || null
  );
  const columnDefs = useMemo(() => buildInstallLinkDetailColumnDefs(), []);
  const defaultColDef = useMemo(() => ({
    sortable: false,
    filter: false,
    resizable: true,
    suppressHeaderMenuButton: true,
    suppressHeaderContextMenu: true,
  }), []);

  return (
    <Box
      sx={{
        width: "100%",
        height: "100%",
        px: 0,
        py: 0,
        overflow: "hidden",
        "@keyframes installLinkDrawerSlideDown": {
          "0%": { opacity: 0, transform: "translateY(-12px)", clipPath: "inset(0 0 100% 0)" },
          "100%": { opacity: 1, transform: "translateY(0)", clipPath: "inset(0 0 0 0)" },
        },
      }}
    >
      <Box
        sx={{
          width: "100%",
          height: "100%",
          minWidth: 0,
          borderRadius: 0,
          border: "none",
          background: NAV_SUBSECTION_ROW_BG,
          boxShadow: "none",
          overflow: "hidden",
          transformOrigin: "top center",
          animation: "installLinkDrawerSlideDown 180ms ease-out",
          willChange: "transform, opacity, clip-path",
        }}
      >
        <Box
          className={themeClassName}
          sx={{
            height: "100%",
            minHeight: INSTALL_LINK_DETAIL_ROW_HEIGHT,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              border: "none",
              borderRadius: 0,
              background: NAV_SUBSECTION_ROW_BG,
            },
            "& .ag-header": {
              minHeight: "32px !important",
              backgroundColor: INSTALL_LINK_HEADER_BG,
              borderBottom: "1px solid rgba(125,211,252,0.22)",
            },
            "& .ag-header-cell-label": {
              color: "#e2e8f0",
              fontSize: "0.68rem",
              fontWeight: 900,
              letterSpacing: "0.03em",
              textTransform: "uppercase",
            },
            "& .ag-header-cell-resize::after": {
              backgroundColor: "rgba(226,232,240,0.26)",
            },
            "& .ag-row, & .ag-row-even, & .ag-row-odd": {
              color: SITE_DESCRIPTION_SUBTEXT_COLOR,
              borderColor: "rgba(255,255,255,0.04)",
              backgroundColor: `${NAV_SUBSECTION_ROW_BG} !important`,
            },
            "& .ag-row-hover": {
              backgroundColor: "rgba(88,166,255,0.12) !important",
            },
            "& .ag-cell": {
              display: "flex",
              alignItems: "center",
              px: 1.2,
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
            rowData={rowData}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowHeight={42}
            headerHeight={32}
            suppressCellFocus
            suppressContextMenu
            preventDefaultOnContextMenu
            getRowId={(rowParams) => String(rowParams.data?.id || "")}
            onCellContextMenu={(rowParams) => context.openInstallLinkContextMenu?.(rowParams.event, rowParams.data, rowParams.node, rowParams)}
            context={context}
            theme={myTheme}
          />
        </Box>
      </Box>
    </Box>
  );
}

function SiteInstallLinkRevokeDialog({ target, saving, onCancel, onConfirm }) {
  const siteName = String(target?.site?.name || "Selected Site").trim() || "Selected Site";
  const platformLabel = INSTALL_LINK_PLATFORM_OPTIONS.find((option) => option.platform === target?.platform)?.label || "Agent";
  return (
    <Dialog open={Boolean(target)} onClose={saving ? undefined : onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 800, fontSize: "1rem", lineHeight: 1.2 }}>
          Revoke Install Link
        </Typography>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.88rem", lineHeight: 1.45 }}>
          Revoke current {platformLabel} link for {siteName} and issue replacement.
        </Typography>
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} disabled={saving} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onConfirm} disabled={saving} sx={SITE_DIALOG_DANGER_BUTTON_SX}>
          {saving ? "Revoking..." : "Revoke"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function SiteDeleteDialog({ open, onCancel, onConfirm, sites }) {
  const siteNames = Array.isArray(sites) ? sites.map((site) => site?.name).filter(Boolean) : [];
  const previewNames = siteNames.slice(0, 4);
  const remainingCount = Math.max(siteNames.length - previewNames.length, 0);
  const deleteLabel = siteNames.length === 1 ? "Delete Site" : "Delete Sites";

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            {deleteLabel}
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Permanently remove these site records from Borealis.
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
              Sites To Delete
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

const PAGE_TITLE = "Sites";
const PAGE_SUBTITLE = "Manage sites and open device inventories by site.";
const PAGE_ICON = LocationCityIcon;

export default function SiteList() {
  const loaderData = useLoaderData();
  const navigate = useNavigate();
  const initialRows = useMemo(() => (Array.isArray(loaderData?.rows) ? loaderData.rows : []), [loaderData]);
  const initialInstallServerUrl = String(loaderData?.installServerUrl || "");
  const initialInstallEngineCA = loaderData?.installEngineCA || null;
  const initialInstallServerIPFallback = String(loaderData?.installServerIPFallback || "");
  const initialAgentInstallArtifact = loaderData?.agentInstallArtifact || null;
  const [rows, setRows] = useState(() => initialRows);
  const [siteWorkerPayload, setSiteWorkerPayload] = useState(() => ({}));
  const [installServerUrl, setInstallServerUrl] = useState(() => initialInstallServerUrl);
  const [installEngineCA, setInstallEngineCA] = useState(() => initialInstallEngineCA);
  const [installServerIPFallback, setInstallServerIPFallback] = useState(() => initialInstallServerIPFallback);
  const [agentInstallArtifact, setAgentInstallArtifact] = useState(() => initialAgentInstallArtifact);
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
  const [expandedInstallLinksSiteId, setExpandedInstallLinksSiteId] = useState(null);
  const [installLinkContextMenu, setInstallLinkContextMenu] = useState({ open: false, top: 0, left: 0, row: null });
  const [revokeInstallLinkTarget, setRevokeInstallLinkTarget] = useState(null);
  const [installLinkRevokeSaving, setInstallLinkRevokeSaving] = useState(false);
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const autoSizeHandleRef = useRef(null);
  const siteWorkerRefreshInFlightRef = useRef(false);
  const resourceHistoryRef = useRef(new Map());
  const siteConnectionSnapshotRef = useRef(new Map());
  const [siteWorkerPayloadReady, setSiteWorkerPayloadReady] = useState(false);
  const [resourceHistoryVersion, setResourceHistoryVersion] = useState(0);
  const [siteConnectionSnapshotVersion, setSiteConnectionSnapshotVersion] = useState(0);
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
  const mergedRows = useMemo(
    () => mergeSiteWorkerRows(rows, siteWorkerPayload, nowSeconds),
    [nowSeconds, rows, siteWorkerPayload]
  );

  useEffect(() => {
    const sampleNowSeconds = Math.floor(Date.now() / 1000);
    const sampleRows = mergeSiteWorkerRows(rows, siteWorkerPayload, sampleNowSeconds);
    const changed = recordSiteConnectionSnapshots(siteConnectionSnapshotRef.current, sampleRows, sampleNowSeconds);
    if (changed) {
      setSiteConnectionSnapshotVersion((version) => version + 1);
    }
  }, [rows, siteWorkerPayload]);

  const stableRows = useMemo(
    () => applySiteConnectionStability(mergedRows, siteConnectionSnapshotRef.current, nowSeconds),
    [mergedRows, nowSeconds, siteConnectionSnapshotVersion]
  );

  const siteDisplayRows = useMemo(
    () =>
      attachResourceHistoryToRows(stableRows, resourceHistoryRef.current).map((row) => ({
        ...row,
        site_worker_payload_ready: siteWorkerPayloadReady,
      })),
    [
      nowSeconds,
      resourceHistoryVersion,
      stableRows,
      siteWorkerPayloadReady,
    ]
  );
  const displayRows = useMemo(
    () => insertExpandedInstallLinkRows(siteDisplayRows, expandedInstallLinksSiteId),
    [expandedInstallLinksSiteId, siteDisplayRows]
  );

  const rowHeightSignature = useMemo(() => siteListRowHeightSignature(displayRows), [displayRows]);

  const handleOpenDevicesForSite = useCallback(
    (site) => {
      const path = buildDeviceListSiteStatusPath(site);
      if (!path) return;
      navigate(path);
    },
    [navigate]
  );

  const handleOpenDevicesForSiteStatus = useCallback(
    (site, statusKey) => {
      const path = buildDeviceListSiteStatusPath(site, statusKey);
      if (!path) return;
      navigate(path);
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

  const fetchInstallMetadataFromOverview = useCallback(async () => {
    try {
      const response = await fetch("/api/server/overview", {
        credentials: "include",
        cache: "no-store",
      });
      if (!response.ok) {
        return { installServerUrl: "", installEngineCA: null, installServerIPFallback: "" };
      }
      const payload = await response.json().catch(() => ({}));
      return {
        installServerUrl: deriveInstallServerUrl({
          public_base_url: payload?.host?.public_base_url || payload?.public_edge?.public_base_url || "",
          public_hostname: payload?.host?.public_hostname || payload?.public_edge?.fqdn || "",
        }),
        installEngineCA: normalizeInstallEngineCA(payload),
        installServerIPFallback: normalizeInstallServerIPFallback(payload),
      };
    } catch {
      return { installServerUrl: "", installEngineCA: null, installServerIPFallback: "" };
    }
  }, []);

  const fetchSites = useCallback(async () => {
    try {
      const res = await fetch("/api/sites", { credentials: "include", cache: "no-store" });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${res.status}`);
      }
      let nextInstallServerUrl = deriveInstallServerUrl(data);
      let nextInstallEngineCA = normalizeInstallEngineCA(data);
      let nextInstallServerIPFallback = normalizeInstallServerIPFallback(data);
      const requiresEngineCA = Boolean(data?.engine_ca_required || data?.deployment_profile === "internal-only");
      if (!nextInstallServerUrl || (requiresEngineCA && (!nextInstallEngineCA || !nextInstallServerIPFallback))) {
        const overviewMetadata = await fetchInstallMetadataFromOverview();
        nextInstallServerUrl = nextInstallServerUrl || overviewMetadata.installServerUrl;
        nextInstallEngineCA = nextInstallEngineCA || overviewMetadata.installEngineCA;
        nextInstallServerIPFallback = nextInstallServerIPFallback || overviewMetadata.installServerIPFallback;
      }
      setRows(Array.isArray(data?.sites) ? data.sites : []);
      setInstallServerUrl(nextInstallServerUrl);
      setInstallEngineCA(nextInstallEngineCA);
      setInstallServerIPFallback(nextInstallServerIPFallback);
      setAgentInstallArtifact(data?.agent_install_artifact || null);
      setLoadError("");
    } catch {
      setRows([]);
      setSiteWorkerPayload({});
      setInstallServerUrl("");
      setInstallEngineCA(null);
      setInstallServerIPFallback("");
      setAgentInstallArtifact(null);
      setLoadError("Unable to load sites.");
    }
  }, [fetchInstallMetadataFromOverview]);

  useEffect(() => {
    setRows(initialRows);
    setSiteWorkerPayload({});
    setSiteWorkerPayloadReady(false);
    resourceHistoryRef.current.clear();
    siteConnectionSnapshotRef.current.clear();
    setResourceHistoryVersion((version) => version + 1);
    setSiteConnectionSnapshotVersion((version) => version + 1);
    setInstallServerUrl(initialInstallServerUrl);
    setInstallEngineCA(initialInstallEngineCA);
    setInstallServerIPFallback(initialInstallServerIPFallback);
    setAgentInstallArtifact(initialAgentInstallArtifact);
    setLoadError(String(loaderData?.initialError || ""));
  }, [
    initialAgentInstallArtifact,
    initialInstallEngineCA,
    initialInstallServerUrl,
    initialInstallServerIPFallback,
    initialRows,
    loaderData,
  ]);

  useEffect(() => {
    if (!expandedInstallLinksSiteId) {
      return;
    }
    const expandedStillVisible = rows.some((row) => String(row?.id ?? row?.site_id ?? "") === String(expandedInstallLinksSiteId));
    if (!expandedStillVisible) {
      setExpandedInstallLinksSiteId(null);
      setInstallLinkContextMenu({ open: false, top: 0, left: 0, row: null });
    }
  }, [expandedInstallLinksSiteId, rows]);

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
          setSiteWorkerPayloadReady(true);
        }
      } finally {
        siteWorkerRefreshInFlightRef.current = false;
      }
    };
    void refreshSiteWorkerPayload();
    const intervalId = setInterval(refreshSiteWorkerPayload, SITE_WORKER_REFRESH_MS);
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
      api.refreshCells({ columns: [SITE_WORKER_CONTAINER_COLUMN_ID, CONNECTED_DEVICES_COLUMN_ID], force: true });
    } catch {}
  }, [
    resourceHistoryVersion,
    siteConnectionSnapshotVersion,
    siteWorkerPayloadReady,
    siteWorkerPayload,
  ]);

  useEffect(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api) return;
    if (typeof api.isDestroyed === "function" && api.isDestroyed()) return;
    try {
      api.resetRowHeights();
    } catch {}
    try {
      api.refreshCells({ columns: [RUNNING_TASKS_COLUMN_ID], force: true });
    } catch {}
  }, [rowHeightSignature]);

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
      siteConnectionSnapshotRef.current.clear();
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

  const handleToggleInstallLinksForSite = useCallback((site) => {
    const siteId = siteIdForRow(site);
    if (!siteId) {
      return;
    }
    setExpandedInstallLinksSiteId((current) => (String(current || "") === String(siteId) ? null : String(siteId)));
    setInstallLinkContextMenu({ open: false, top: 0, left: 0, row: null });
  }, []);

  const handleCloseInstallLinkContextMenu = useCallback(() => {
    setInstallLinkContextMenu({ open: false, top: 0, left: 0, row: null });
  }, []);

  const handleOpenInstallLinkContextMenu = useCallback((event, row) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    if (!row) {
      return;
    }
    setInstallLinkContextMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row,
    });
  }, []);

  const handleCopyInstallCommand = useCallback(async (site, osId) => {
    const siteName = String(site?.name || "Unknown Site").trim() || "Unknown Site";
    const osLabel = INSTALL_LINK_PLATFORM_OPTIONS.find((option) => option.id === osId)?.label || "Agent";
    const command = buildInstallCommand(
      osId,
      installServerUrl,
      site?.enrollment_code,
      installEngineCA,
      installServerIPFallback,
      {
        downloads: site?.agent_install_downloads,
      }
    );
    if (!command) {
      await sendNotification({
        title: "Install Command Unavailable",
        message: `<b>${osLabel}</b> install command for <b>${siteName}</b> is unavailable.`,
        icon: "warning",
        variant: "error",
      });
      return;
    }
    const copied = await copyTextToClipboard(command, `Copy ${osLabel} install command`);
    if (copied) {
      await sendNotification({
        title: "Install Command Copied",
        message: `<b>${osLabel}</b> Engine-compiled Agent install command for <b>${siteName}</b> copied to clipboard.`,
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
  }, [copyTextToClipboard, installEngineCA, installServerIPFallback, installServerUrl, sendNotification]);

  const handleRequestRevokeInstallLink = useCallback((site, platform) => {
    handleCloseInstallLinkContextMenu();
    setRevokeInstallLinkTarget({ site, platform });
  }, [handleCloseInstallLinkContextMenu]);

  const handleCancelRevokeInstallLink = useCallback(() => {
    if (!installLinkRevokeSaving) {
      setRevokeInstallLinkTarget(null);
    }
  }, [installLinkRevokeSaving]);

  const handleConfirmRevokeInstallLink = useCallback(async () => {
    const target = revokeInstallLinkTarget;
    const siteId = target?.site?.id;
    const platform = String(target?.platform || "").trim();
    if (!siteId || !platform) {
      setRevokeInstallLinkTarget(null);
      return;
    }
    const siteName = String(target?.site?.name || "Unknown Site").trim() || "Unknown Site";
    const platformLabel = INSTALL_LINK_PLATFORM_OPTIONS.find((option) => option.platform === platform)?.label || "Agent";
    setInstallLinkRevokeSaving(true);
    try {
      const response = await fetch(`/api/sites/${encodeURIComponent(siteId)}/agent-install-links/${encodeURIComponent(platform)}/revoke`, {
        method: "POST",
        credentials: "include",
        cache: "no-store",
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      }
      await fetchSites();
      setRevokeInstallLinkTarget(null);
      await sendNotification({
        title: "Install Link Replaced",
        message: `<b>${platformLabel}</b> install link for <b>${siteName}</b> was revoked and replaced.`,
        icon: "done",
        variant: "info",
      });
    } catch (error) {
      await sendNotification({
        title: "Install Link Not Revoked",
        message: String(error?.message || error || "Unable to revoke install link."),
        icon: "warning",
        variant: "error",
      });
    } finally {
      setInstallLinkRevokeSaving(false);
    }
  }, [fetchSites, revokeInstallLinkTarget, sendNotification]);

  const installLinkContextRow = installLinkContextMenu.row || null;
  const installLinkContextActions = useMemo(() => {
    const row = installLinkContextRow;
    const site = row?.site || null;
    const platform = row?.platform || "";
    return [
      {
        id: "revoke-installer-link",
        group: "danger",
        label: "Revoke Installer Link",
        icon: DeleteRoundedIcon,
        intent: "danger",
        disabled: !row?.active,
        disabledReason: row?.active ? "" : "Link unavailable.",
        description: "Rotate this agent download link now.",
        onClick: () => handleRequestRevokeInstallLink(site, platform),
      },
    ];
  }, [
    handleRequestRevokeInstallLink,
    installLinkContextRow,
  ]);

  const openRenameDialog = useCallback((siteOverride = null) => {
    const selId = siteOverride?.id ?? (selectedIds.size === 1 ? Array.from(selectedIds)[0] : null);
    if (selId == null) return;
    const site = siteOverride || rows.find((row) => row.id === selId);
    setRenameSiteId(selId);
    setRenameValue(site?.name || "");
    setRenameOpen(true);
  }, [rows, selectedIds]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const handleOpenSiteContextMenu = useCallback((event, row) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    setSiteContextMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row: row || null,
    });
  }, []);

  const columnDefs = useMemo(() => [
    {
      headerName: "Name",
      field: "name",
      minWidth: 260,
      flex: 1.15,
      cellStyle: {
        paddingLeft: 6,
        paddingRight: 12,
      },
      cellRendererParams: {
        suppressMouseEventHandling: () => true,
      },
      cellRenderer: (params) => {
        const description = String(params?.data?.description || "").trim();
        const siteId = siteIdForRow(params?.data);
        const expanded = siteId > 0 && String(expandedInstallLinksSiteId || "") === String(siteId);
        const linkSummary = siteInstallLinkSummary(params?.data, agentInstallArtifact);
        const linkSummaryText = `${linkSummary.activeCount}/${INSTALL_LINK_PLATFORM_OPTIONS.length} install links · ${linkSummary.totalDownloads.toLocaleString()} downloads`;
        const subtitle = description ? `${description} · ${linkSummaryText}` : linkSummaryText;
        const toggleInstallLinksLabel = expanded ? "Hide Agent Install Links" : "Display Agent Install Links";
        return (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 0.75,
              width: "100%",
              height: "100%",
              minWidth: 0,
              lineHeight: 1.25,
            }}
          >
            <Tooltip title={toggleInstallLinksLabel} placement="top">
              <IconButton
                size="small"
                aria-label={`${toggleInstallLinksLabel} for ${params.value || "site"}`}
                onMouseDown={stopGridRowSelectionEvent}
                onClick={(event) => {
                  stopGridRowSelectionEvent(event);
                  handleToggleInstallLinksForSite(params.data);
                }}
                sx={{
                  width: 28,
                  height: 28,
                  flexShrink: 0,
                  color: expanded ? "#7dd3fc" : "rgba(226,232,240,0.78)",
                  border: "none",
                  background: "transparent",
                  "&:hover": {
                    color: "#f8fbff",
                    background: "rgba(88,166,255,0.14)",
                  },
                }}
              >
                {expanded ? <KeyboardArrowDownRoundedIcon fontSize="small" /> : <DownloadRoundedIcon fontSize="small" />}
              </IconButton>
            </Tooltip>
            <Box sx={{ display: "flex", flexDirection: "column", justifyContent: "center", minWidth: 0, flex: 1 }}>
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
              <Typography
                component="span"
                title={subtitle || undefined}
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
                {subtitle}
              </Typography>
            </Box>
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
      field: CONNECTED_DEVICES_COLUMN_ID,
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
      headerName: "Running Tasks",
      field: RUNNING_TASKS_COLUMN_ID,
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
    buildRowContextMenuColumnDef(handleOpenSiteContextMenu, { tooltip: "Site Actions" }),
  ], [agentInstallArtifact, expandedInstallLinksSiteId, handleOpenDevicesForSite, handleOpenSiteContextMenu, handleToggleInstallLinksForSite]);

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

  const hasSelectedSites = selectedSiteRows.length > 0;

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

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "create-site",
        label: "Create Site",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => setCreateOpen(true),
      },
    ],
    []
  );

  const siteContextRow = siteContextMenu.row || null;
  const siteContextSubtitle = siteContextRow
    ? `${Number(siteContextRow.device_count || 0).toLocaleString()} Device${Number(siteContextRow.device_count || 0) === 1 ? "" : "s"}`
    : "Site";
  const siteContextActions = useMemo(() => {
    const row = siteContextMenu.row || null;
    const unavailableReason = row ? "" : "Select a site first.";
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
        label: "Network-Based Device Onboarding",
        icon: DevicesIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Automatically onboard devices across a network.",
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
        label: "Delete",
        icon: DeleteRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Delete this site record.",
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
      openDevicesForSiteStatus: handleOpenDevicesForSiteStatus,
      nowSeconds,
      installServerUrl,
      agentInstallArtifact,
      openInstallLinkContextMenu: handleOpenInstallLinkContextMenu,
      copyInstallCommand: handleCopyInstallCommand,
    }),
    [agentInstallArtifact, handleCopyInstallCommand, handleOpenDevicesForSiteStatus, handleOpenInstallLinkContextMenu, installServerUrl, navigate, nowSeconds]
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
              "& .ag-full-width-container, & .ag-full-width-row, & .ag-full-width-row .ag-full-width-cell": {
                width: "100% !important",
              },
              "& .ag-full-width-row": {
                overflow: "hidden",
              },
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={displayRows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              context={gridContext}
              isFullWidthRow={(params) => isInstallLinkDetailRow(params?.rowNode?.data || params?.data)}
              fullWidthCellRenderer={InstallLinksDetailRow}
              suppressCellFocus
              suppressContextMenu
              preventDefaultOnContextMenu
              rowHeight={BASE_ROW_HEIGHT}
              getRowHeight={(params) => siteListRowHeightForData(params?.data)}
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
              onCellContextMenu={(params) => {
                if (isInstallLinkDetailRow(params?.data)) return;
                handleOpenSiteContextMenu(params.event, params.data);
              }}
              onFirstDataRendered={autoSizeColumns}
              onRowDataUpdated={autoSizeColumns}
              theme={myTheme}
            />
          </Box>
        </Box>
      </PageBodyFrame>

      <RowContextMenu
        open={installLinkContextMenu.open}
        onClose={handleCloseInstallLinkContextMenu}
        position={{ top: installLinkContextMenu.top, left: installLinkContextMenu.left }}
        headerIcon={DownloadRoundedIcon}
        title={installLinkContextRow?.site?.name || "Install Link"}
        subtitle={installLinkContextRow ? `${installLinkContextRow.agentType} · ${installLinkContextRow.platform}` : "Agent install link"}
        actions={installLinkContextActions}
        widthVariant="standard"
      />

      <SiteInstallLinkRevokeDialog
        target={revokeInstallLinkTarget}
        saving={installLinkRevokeSaving}
        onCancel={handleCancelRevokeInstallLink}
        onConfirm={() => void handleConfirmRevokeInstallLink()}
      />

      <CreateSiteDialog
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreate={async (name, description) => {
          const validation = validateSiteWorkerSiteName(name, rows);
          if (validation) {
            await sendNotification({
              title: validation.title,
              message: validation.message,
              icon: "warning",
              variant: "error",
            });
            return;
          }
          try {
            const res = await fetch("/api/sites", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name, description }),
            });
            const data = await res.json().catch(() => ({}));
            if (res.ok) {
              setCreateOpen(false);
              if (name) {
                await sendNotification({
                  title: "Site Created",
                  message: `Site ${name} Created Successfully. Configure patch coverage at <b>${APP_PATHS.patchManagementSitePolicies}</b>.`,
                  icon: "success",
                  variant: "success",
                });
              }
              fetchSites();
              return;
            }
            await sendNotification({
              title: "Site Not Created",
              message: siteMutationErrorMessage(data, "Unable to create site."),
              icon: "warning",
              variant: "error",
            });
          } catch {
            await sendNotification({
              title: "Site Not Created",
              message: "Unable to reach Borealis API.",
              icon: "warning",
              variant: "error",
            });
          }
        }}
      />

      <SiteDeleteDialog
        open={deleteOpen}
        onCancel={() => {
          setDeleteOpen(false);
          setSelectedIds(new Set());
        }}
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
          const validation = validateSiteWorkerSiteName(newName, rows, selId);
          if (validation) {
            await sendNotification({
              title: validation.title,
              message: validation.message,
              icon: "warning",
              variant: "error",
            });
            return;
          }
          try {
            const res = await fetch("/api/sites/rename", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: selId, new_name: newName }),
            });
            const data = await res.json().catch(() => ({}));
            if (res.ok) {
              setRenameOpen(false);
              setRenameSiteId(null);
              await sendNotification(`Site ${oldName} Renamed as ${newName} Successfully`);
              fetchSites();
              return;
            }
            await sendNotification({
              title: "Site Not Renamed",
              message: siteMutationErrorMessage(data, "Unable to rename site."),
              icon: "warning",
              variant: "error",
            });
          } catch {
            await sendNotification({
              title: "Site Not Renamed",
              message: "Unable to reach Borealis API.",
              icon: "warning",
              variant: "error",
            });
          }
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

    </Paper>
  );
}
