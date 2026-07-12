import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Checkbox,
  Divider,
  FormControlLabel,
  LinearProgress,
  Menu,
  MenuItem,
  Stack,
  Tooltip,
  Typography,
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import { themeQuartz } from "ag-grid-community";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import TerminalRoundedIcon from "@mui/icons-material/TerminalRounded";
import KeyboardArrowRightRoundedIcon from "@mui/icons-material/KeyboardArrowRightRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import StopCircleRoundedIcon from "@mui/icons-material/StopCircleRounded";
import { DEFAULT_GRID_COL_DEF, GridShell, MAGIC_UI } from "./Shared.jsx";
import { CountSliderGroup } from "../../Automation/Watchdogs/shared.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";

const PROCESS_POLL_INTERVAL_MS = 5000;
const PROCESS_REFRESH_OPTIONS = [
  { key: "live", label: "Live" },
  { key: "normal", label: "Normal" },
  { key: "quiet", label: "Quiet" },
];
const PROCESS_REFRESH_INTERVALS_MS = {
  live: 1000,
  normal: 5000,
  quiet: 15000,
};
const PROCESS_REFRESH_COUNTS = {
  live: "1s",
  normal: "5s",
  quiet: "15s",
};
const PROCESS_INITIAL_EMPTY_RETRY_LIMIT = 3;
const PROCESS_INITIAL_EMPTY_RETRY_DELAY_MS = 1200;
const PROCESS_COLLECTING_MESSAGE = "Collecting Active Process Data...";
const AUTO_SIZE_COLUMNS = ["username", "cpu_percent", "memory_percent", "disk_bytes_per_second", "network_bytes_per_second"];
const NAME_LINK_COLOR = "#58a6ff";
const LOW_SIGNAL_CPU_PERCENT = 0.5;
const LOW_SIGNAL_MEMORY_PERCENT = 1;
const TREE_BOUNDARY_PROCESS_NAMES = new Set([
  "csrss.exe",
  "cmd.exe",
  "conhost.exe",
  "explorer.exe",
  "lsaiso.exe",
  "lsass.exe",
  "memcompression",
  "memory compression",
  "openconsole.exe",
  "powershell.exe",
  "pwsh.exe",
  "registry",
  "services.exe",
  "smss.exe",
  "svchost.exe",
  "system",
  "system idle process",
  "windowsterminal.exe",
  "wininit.exe",
  "winlogon.exe",
  "wt.exe",
]);
const FRIENDLY_PROCESS_NAMES = {
  memcompression: "Memory Compression",
};
const ALWAYS_HIDE_SYSTEM_PROCESS_NAMES = new Set(["memcompression", "memory compression", "wslservice.exe"]);
const LOW_SIGNAL_SYSTEM_PROCESS_NAMES = new Set([
  "aggregatorhost.exe",
  "audiodg.exe",
  "backgroundtaskhost.exe",
  "comppkgsrv.exe",
  "conhost.exe",
  "csrss.exe",
  "ctfmon.exe",
  "dashost.exe",
  "dllhost.exe",
  "dwm.exe",
  "fontdrvhost.exe",
  "lsaiso.exe",
  "lsass.exe",
  "memory compression",
  "memcompression",
  "registry",
  "runtimebroker.exe",
  "searchfilterhost.exe",
  "searchhost.exe",
  "searchindexer.exe",
  "securityhealthservice.exe",
  "services.exe",
  "sihost.exe",
  "smartscreen.exe",
  "smss.exe",
  "spoolsv.exe",
  "startmenuexperiencehost.exe",
  "svchost.exe",
  "system",
  "system idle process",
  "taskhostw.exe",
  "textinputhost.exe",
  "wininit.exe",
  "winlogon.exe",
  "wmiprvse.exe",
  "wudfhost.exe",
]);
const LOW_SIGNAL_SYSTEM_PROCESS_PREFIXES = ["kworker/", "ksoftirqd/", "migration/", "idle_inject/", "rcu_"];

const PROCESS_GRID_THEME = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const ACTION_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const ACTION_MENU_ITEM_SX = {
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
    transition: "background-color 0.16s ease",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_DANGER_ITEM_SX = {
  ...ACTION_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const ACTION_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const ACTION_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const ACTION_MENU_HEADER_ICON_SX = {
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

const ACTION_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const ACTION_MENU_LABEL_SX = {
  color: MAGIC_UI.textBright,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ACTION_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const ACTION_MENU_TITLE_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ACTION_MENU_GROUP_LABELS = {
  primary: "Primary",
  danger: "Danger Zone",
};

const ACTION_MENU_GROUP_ORDER = ["primary", "danger"];

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function isGuidLike(value) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalizeText(value));
}

function getHostname(device) {
  const candidates = [device?.summary?.hostname, device?.device_hostname, device?.hostname]
    .map((value) => normalizeText(value))
    .filter(Boolean);
  return candidates.find((value) => !isGuidLike(value)) || "";
}

function coerceNumber(value, fallback = 0) {
  const numeric = Number(value);
  return Number.isFinite(numeric) ? numeric : fallback;
}

function coerceNullableNumber(value) {
  if (value == null || value === "") return null;
  const numeric = Number(value);
  return Number.isFinite(numeric) ? numeric : null;
}

function normalizeRateMetric(value) {
  const numeric = coerceNullableNumber(value);
  return numeric == null ? null : Math.max(0, numeric);
}

function formatUpdatedAt(epochSeconds) {
  const value = Number(epochSeconds || 0);
  if (!value) return "Last Updated Awaiting process telemetry";
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return "Last Updated Awaiting process telemetry";
  const dateText = date.toLocaleDateString();
  const timeText = date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" });
  return `Last Updated ${dateText} @ ${timeText}`;
}

function formatBytes(sizeBytes) {
  const value = Number(sizeBytes || 0);
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(size >= 10 || index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatBytesPerSecond(sizeBytes) {
  if (sizeBytes == null) return "N/A";
  return `${formatBytes(sizeBytes)}/s`;
}

function formatPercent(value) {
  const numeric = Math.max(0, coerceNumber(value, 0));
  if (numeric >= 10) return `${numeric.toFixed(0)}%`;
  if (numeric >= 1) return `${numeric.toFixed(1)}%`;
  return `${numeric.toFixed(2)}%`;
}

function normalizeProcessName(value) {
  const name = normalizeText(value);
  const friendlyName = FRIENDLY_PROCESS_NAMES[name.toLowerCase()];
  return friendlyName || name;
}

function isTreeBoundaryProcess(row = {}) {
  return TREE_BOUNDARY_PROCESS_NAMES.has(normalizeText(row?.name).toLowerCase());
}

function normalizeProcessOwner(row = {}) {
  const owner = normalizeText(row?.username || row?.user || row?.owner);
  if (!owner) return "";
  return owner.replace(/^nt authority\\/i, "");
}

function isLowSignalSystemProcess(row = {}) {
  const name = normalizeText(row?.name).toLowerCase();
  if (!name) return false;
  if (ALWAYS_HIDE_SYSTEM_PROCESS_NAMES.has(name)) return true;
  const isKnownNoise =
    LOW_SIGNAL_SYSTEM_PROCESS_NAMES.has(name) || LOW_SIGNAL_SYSTEM_PROCESS_PREFIXES.some((prefix) => name.startsWith(prefix));
  if (!isKnownNoise) return false;
  return (
    coerceNumber(row?.cpu_percent, 0) < LOW_SIGNAL_CPU_PERCENT &&
    coerceNumber(row?.memory_percent, 0) < LOW_SIGNAL_MEMORY_PERCENT
  );
}

function normalizeProcessRow(row = {}, index = 0) {
  const pid = Math.max(0, Math.trunc(coerceNumber(row?.pid, 0)));
  const parentPid = Math.max(0, Math.trunc(coerceNumber(row?.parent_pid ?? row?.ppid, 0)));
  const name = normalizeProcessName(row?.name) || (pid ? `PID ${pid}` : "Process");
  const id = normalizeText(row?.id) || `${pid}:${normalizeText(row?.created_at) || index}`;
  return {
    ...row,
    id,
    pid,
    parent_pid: parentPid,
    name,
    cpu_percent: Math.max(0, coerceNumber(row?.cpu_percent, 0)),
    memory_percent: Math.max(0, coerceNumber(row?.memory_percent, 0)),
    memory_bytes: Math.max(0, coerceNumber(row?.memory_bytes, 0)),
    disk_bytes_per_second: normalizeRateMetric(row?.disk_bytes_per_second),
    network_bytes_per_second: normalizeRateMetric(row?.network_bytes_per_second),
    command_line: normalizeText(row?.command_line),
    executable_path: normalizeText(row?.executable_path),
    username: normalizeProcessOwner(row),
    status: normalizeText(row?.status),
    child_count: Math.max(0, Math.trunc(coerceNumber(row?.child_count, 0))),
    has_children: Boolean(row?.has_children || coerceNumber(row?.child_count, 0) > 0),
  };
}

function createTerminatedProcessRow(row = {}, terminatedAt = Math.floor(Date.now() / 1000), reason = "terminated") {
  const reportedAt = Math.max(0, Math.trunc(coerceNumber(terminatedAt, 0))) || Math.floor(Date.now() / 1000);
  return normalizeProcessRow({
    ...row,
    cpu_percent: 0,
    disk_bytes_per_second: 0,
    network_bytes_per_second: coerceNullableNumber(row?.network_bytes_per_second) == null ? null : 0,
    parent_pid: 0,
    status: "terminated",
    terminated: true,
    terminated_reason: reason,
    terminated_at: reportedAt,
    has_children: false,
    child_count: 0,
  });
}

function getProcessCommandText(row = {}) {
  const name = normalizeText(row?.name).toLowerCase();
  const commandLine = normalizeText(row?.command_line);
  if (commandLine && commandLine.toLowerCase() !== name) return commandLine;
  const executablePath = normalizeText(row?.executable_path);
  if (executablePath && executablePath.toLowerCase() !== name) return executablePath;
  return "";
}

function getProcessLocation(row = {}) {
  const direct = normalizeText(row?.executable_path);
  if (direct) return direct;
  const commandLine = normalizeText(row?.command_line);
  if (!commandLine) return "";
  const quotedMatch = commandLine.match(/^\s*"([^"]+)"/);
  if (quotedMatch) return normalizeText(quotedMatch[1]);
  return normalizeText(commandLine.split(/\s+/, 1)[0]);
}

function getDisplayCpuPercent(row = {}) {
  return coerceNumber(row?.has_children ? row?.group_cpu_total : row?.cpu_percent, row?.cpu_percent || 0);
}

function getDisplayMemoryBytes(row = {}) {
  return coerceNumber(row?.has_children ? row?.group_memory_bytes_total : row?.memory_bytes, row?.memory_bytes || 0);
}

function getDisplayMemoryPercent(row = {}) {
  return coerceNumber(row?.has_children ? row?.group_memory_percent_total : row?.memory_percent, row?.memory_percent || 0);
}

function getDisplayDiskBytesPerSecond(row = {}) {
  const value = row?.has_children ? row?.group_disk_bytes_per_second_total : row?.disk_bytes_per_second;
  return coerceNullableNumber(value);
}

function getDisplayNetworkBytesPerSecond(row = {}) {
  const value = row?.has_children ? row?.group_network_bytes_per_second_total : row?.network_bytes_per_second;
  return coerceNullableNumber(value);
}

function getSortValue(row, columnId) {
  if (columnId === "cpu_percent") return getDisplayCpuPercent(row);
  if (columnId === "memory_percent") return getDisplayMemoryBytes(row);
  if (columnId === "disk_bytes_per_second") return coerceNumber(getDisplayDiskBytesPerSecond(row), -1);
  if (columnId === "network_bytes_per_second") return coerceNumber(getDisplayNetworkBytesPerSecond(row), -1);
  if (columnId === "username") return normalizeText(row?.username).toLowerCase();
  if (columnId === "command_line") return getProcessCommandText(row).toLowerCase();
  return normalizeText(row?.name).toLowerCase();
}

function compareProcessRows(left, right, sortModel = []) {
  const activeSorts = Array.isArray(sortModel) && sortModel.length ? sortModel : [{ colId: "cpu_percent", sort: "desc" }];
  for (const descriptor of activeSorts) {
    const columnId = normalizeText(descriptor?.colId) || "cpu_percent";
    const direction = normalizeText(descriptor?.sort).toLowerCase() === "asc" ? 1 : -1;
    const leftValue = getSortValue(left, columnId);
    const rightValue = getSortValue(right, columnId);
    let comparison = 0;
    if (typeof leftValue === "number" || typeof rightValue === "number") {
      comparison = coerceNumber(leftValue, 0) - coerceNumber(rightValue, 0);
    } else {
      comparison = String(leftValue).localeCompare(String(rightValue), undefined, {
        sensitivity: "base",
        numeric: true,
      });
    }
    if (comparison !== 0) return comparison * direction;
  }
  return normalizeText(left?.name).localeCompare(normalizeText(right?.name), undefined, {
    sensitivity: "base",
    numeric: true,
  });
}

function buildVisibleProcessRows(processes = [], expandedPids = new Set(), sortModel = []) {
  const rows = (Array.isArray(processes) ? processes : []).map((row, index) => normalizeProcessRow(row, index));
  const byPid = new Map(rows.filter((row) => row.pid > 0).map((row) => [row.pid, row]));
  const displayParentByPid = new Map();
  const childrenByDisplayParent = new Map();
  rows.forEach((row) => {
    const parentPid = row.parent_pid;
    if (!parentPid || parentPid === row.pid || !byPid.has(parentPid)) return;
    const parent = byPid.get(parentPid);
    if (!parent || isTreeBoundaryProcess(parent)) return;
    displayParentByPid.set(row.pid, parentPid);
    const existing = childrenByDisplayParent.get(parentPid) || [];
    existing.push(row);
    childrenByDisplayParent.set(parentPid, existing);
  });

  const annotateGroupMetrics = (row, seen = new Set()) => {
    if (!row || seen.has(row.pid)) return row;
    seen.add(row.pid);
    const children = childrenByDisplayParent.get(row.pid) || [];
    let cpuTotal = coerceNumber(row.cpu_percent, 0);
    let memoryBytesTotal = coerceNumber(row.memory_bytes, 0);
    let memoryPercentTotal = coerceNumber(row.memory_percent, 0);
    const ownDiskBytesPerSecond = coerceNullableNumber(row.disk_bytes_per_second);
    const ownNetworkBytesPerSecond = coerceNullableNumber(row.network_bytes_per_second);
    let hasDiskBytesPerSecond = ownDiskBytesPerSecond != null;
    let hasNetworkBytesPerSecond = ownNetworkBytesPerSecond != null;
    let diskBytesPerSecondTotal = ownDiskBytesPerSecond || 0;
    let networkBytesPerSecondTotal = ownNetworkBytesPerSecond || 0;
    children.forEach((child) => {
      annotateGroupMetrics(child, seen);
      cpuTotal += coerceNumber(child.group_cpu_total ?? child.cpu_percent, 0);
      memoryBytesTotal += coerceNumber(child.group_memory_bytes_total ?? child.memory_bytes, 0);
      memoryPercentTotal += coerceNumber(child.group_memory_percent_total ?? child.memory_percent, 0);
      const childDiskBytesPerSecond = coerceNullableNumber(child.group_disk_bytes_per_second_total ?? child.disk_bytes_per_second);
      const childNetworkBytesPerSecond = coerceNullableNumber(
        child.group_network_bytes_per_second_total ?? child.network_bytes_per_second
      );
      if (childDiskBytesPerSecond != null) {
        hasDiskBytesPerSecond = true;
        diskBytesPerSecondTotal += childDiskBytesPerSecond;
      }
      if (childNetworkBytesPerSecond != null) {
        hasNetworkBytesPerSecond = true;
        networkBytesPerSecondTotal += childNetworkBytesPerSecond;
      }
    });
    row.group_cpu_total = cpuTotal;
    row.group_memory_bytes_total = memoryBytesTotal;
    row.group_memory_percent_total = memoryPercentTotal;
    row.group_disk_bytes_per_second_total = hasDiskBytesPerSecond ? diskBytesPerSecondTotal : null;
    row.group_network_bytes_per_second_total = hasNetworkBytesPerSecond ? networkBytesPerSecondTotal : null;
    row.has_children = children.length > 0;
    row.child_count = children.length;
    return row;
  };

  rows.forEach((row) => annotateGroupMetrics(row));
  const topLevelRows = rows.filter((row) => !displayParentByPid.has(row.pid));
  const visible = [];
  const visit = (row, depth) => {
    visible.push({ ...row, depth, tree_id: `${row.id}:${depth}` });
    if (!row.has_children || !expandedPids.has(row.pid)) return;
    const children = [...(childrenByDisplayParent.get(row.pid) || [])].sort((left, right) =>
      compareProcessRows(left, right, sortModel)
    );
    children.forEach((child) => visit(child, depth + 1));
  };
  topLevelRows.sort((left, right) => compareProcessRows(left, right, sortModel)).forEach((row) => visit(row, 0));
  return visible;
}

function MetricHeatCell({ value, label, subLabel = "", intensity = 0, tone = "cpu" }) {
  const normalizedIntensity = Math.max(0, Math.min(1, Number(intensity || 0)));
  const gradient =
    tone === "network"
      ? `linear-gradient(90deg, rgba(52,211,153,${0.08 + normalizedIntensity * 0.34}) ${Math.max(
          6,
          normalizedIntensity * 100
        )}%, transparent ${Math.max(6, normalizedIntensity * 100)}%)`
      : tone === "disk"
      ? `linear-gradient(90deg, rgba(251,191,36,${0.08 + normalizedIntensity * 0.34}) ${Math.max(
          6,
          normalizedIntensity * 100
        )}%, transparent ${Math.max(6, normalizedIntensity * 100)}%)`
      : tone === "memory"
      ? `linear-gradient(90deg, rgba(192,132,252,${0.08 + normalizedIntensity * 0.34}) ${Math.max(
          6,
          normalizedIntensity * 100
        )}%, transparent ${Math.max(6, normalizedIntensity * 100)}%)`
      : `linear-gradient(90deg, rgba(125,211,252,${0.08 + normalizedIntensity * 0.34}) ${Math.max(
          6,
          normalizedIntensity * 100
        )}%, transparent ${Math.max(6, normalizedIntensity * 100)}%)`;
  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        height: 30,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 1,
        px: 1,
        borderRadius: 1.2,
        background: gradient,
        border: "1px solid rgba(148,163,184,0.08)",
      }}
    >
      <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem", fontWeight: 700, lineHeight: 1 }}>
        {label || formatPercent(value)}
      </Typography>
      {subLabel ? (
        <Typography
          sx={{
            color: "rgba(203,213,225,0.74)",
            fontSize: "0.72rem",
            lineHeight: 1,
            whiteSpace: "nowrap",
          }}
        >
          {subLabel}
        </Typography>
      ) : null}
    </Box>
  );
}

function ProcessNameCell({ row = {}, onToggle }) {
  const canExpand = Boolean(row?.has_children);
  const isExpanded = Boolean(row?.is_expanded);
  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        gap: 0.8,
        height: "100%",
        minWidth: 0,
        width: "100%",
        pl: `${Number(row?.depth || 0) * 18}px`,
      }}
    >
      <Box
        component={canExpand ? "button" : "span"}
        type={canExpand ? "button" : undefined}
        aria-label={canExpand ? (isExpanded ? "Collapse process" : "Expand process") : undefined}
        onClick={
          canExpand
            ? (event) => {
                event.preventDefault();
                event.stopPropagation();
                onToggle?.(row);
              }
            : undefined
        }
        sx={{
          width: 22,
          height: 22,
          border: 0,
          borderRadius: 1,
          p: 0,
          m: 0,
          flexShrink: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          color: canExpand ? "#8fd3ff" : "rgba(148,163,184,0.28)",
          background: canExpand ? "rgba(88,166,255,0.09)" : "transparent",
          cursor: canExpand ? "pointer" : "default",
          transition: "background-color 0.16s ease, color 0.16s ease",
          "&:hover": canExpand
            ? {
                background: "rgba(88,166,255,0.16)",
                color: "#b6e4ff",
              }
            : undefined,
        }}
      >
        <KeyboardArrowRightRoundedIcon
          sx={{
            fontSize: 19,
            transform: isExpanded ? "rotate(90deg)" : "rotate(0deg)",
            transition: "transform 0.16s ease",
          }}
        />
      </Box>
      {canExpand ? (
        <AccountTreeRoundedIcon sx={{ fontSize: 22, color: "#8fd3ff", flexShrink: 0 }} />
      ) : (
        <TerminalRoundedIcon sx={{ fontSize: 22, color: "#cbd5e1", flexShrink: 0 }} />
      )}
      <Tooltip title={row?.name || ""} placement="top-start">
        <Typography
          component="span"
          sx={{
            color: NAME_LINK_COLOR,
            fontWeight: 500,
            lineHeight: 1.25,
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
            minWidth: 0,
          }}
        >
          {row?.name || "Process"}
        </Typography>
      </Tooltip>
      {row?.pid ? (
        <Typography sx={{ color: "rgba(148,163,184,0.72)", fontSize: "0.72rem", whiteSpace: "nowrap", flexShrink: 0 }}>
          PID {row.pid}
        </Typography>
      ) : null}
      {row?.terminated ? (
        <Typography
          component="span"
          sx={{
            color: "rgba(254,202,202,0.95)",
            fontSize: "0.66rem",
            fontWeight: 700,
            lineHeight: 1,
            whiteSpace: "nowrap",
            flexShrink: 0,
            border: "1px solid rgba(248,113,113,0.26)",
            borderRadius: 999,
            px: 0.65,
            py: 0.28,
            background: "rgba(127,29,29,0.22)",
          }}
        >
          Terminated
        </Typography>
      ) : null}
    </Box>
  );
}

function ProcessOwnerCell({ value }) {
  const text = normalizeText(value) || "-";
  return (
    <Tooltip title={text} placement="top-start">
      <Typography
        component="span"
        sx={{
          color: "rgba(226,232,240,0.84)",
          fontSize: "0.8rem",
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          minWidth: 0,
        }}
      >
        {text}
      </Typography>
    </Tooltip>
  );
}

function CommandLineCell({ row }) {
  const commandText = getProcessCommandText(row);
  const text = commandText || "-";
  return (
    <Tooltip title={commandText || "No command line reported"} placement="top-start">
      <Typography
        component="span"
        sx={{
          color: "rgba(148,163,184,0.72)",
          fontFamily: '"IBM Plex Mono", "Consolas", monospace',
          fontSize: "0.78rem",
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
          minWidth: 0,
        }}
      >
        {text}
      </Typography>
    </Tooltip>
  );
}

export default function ProcessManagement({ device }) {
  const notifyOperator = useAppNotifications();
  const gridApiRef = useRef(null);
  const inFlightRef = useRef(false);
  const previousLiveProcessRowsRef = useRef(new Map());
  const emptyInitialRetryCountRef = useRef(0);
  const collectingRetryTimerRef = useRef(null);

  const hostname = useMemo(() => getHostname(device), [device]);
  const [processRows, setProcessRows] = useState([]);
  const [terminatedProcessRows, setTerminatedProcessRows] = useState(() => new Map());
  const [reportedAt, setReportedAt] = useState(0);
  const [refreshRateKey, setRefreshRateKey] = useState("normal");
  const [expandedPids, setExpandedPids] = useState(() => new Set());
  const [selectedProcessId, setSelectedProcessId] = useState("");
  const [sortModel, setSortModel] = useState([{ colId: "cpu_percent", sort: "desc" }]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [collectingInitialSnapshot, setCollectingInitialSnapshot] = useState(false);
  const [error, setError] = useState("");
  const [actionBusy, setActionBusy] = useState("");
  const [contextMenuState, setContextMenuState] = useState(null);
  const [showSystemProcesses, setShowSystemProcesses] = useState(false);
  const [showTerminatedProcesses, setShowTerminatedProcesses] = useState(true);

  const effectiveRefreshIntervalMs = PROCESS_REFRESH_INTERVALS_MS[refreshRateKey] || PROCESS_POLL_INTERVAL_MS;

  const allProcessRows = useMemo(() => {
    const liveRows = Array.isArray(processRows) ? processRows : [];
    if (!showTerminatedProcesses || !terminatedProcessRows.size) return liveRows;
    return [...liveRows, ...terminatedProcessRows.values()];
  }, [processRows, showTerminatedProcesses, terminatedProcessRows]);

  const filteredProcessRows = useMemo(() => {
    const rows = Array.isArray(allProcessRows) ? allProcessRows : [];
    if (showSystemProcesses) return rows;
    return rows.filter((row) => row?.terminated || !isLowSignalSystemProcess(row));
  }, [allProcessRows, showSystemProcesses]);

  const hiddenProcessCount = useMemo(
    () => Math.max(0, allProcessRows.length - filteredProcessRows.length),
    [allProcessRows.length, filteredProcessRows.length]
  );

  const visibleRows = useMemo(() => {
    const rows = buildVisibleProcessRows(filteredProcessRows, expandedPids, sortModel);
    const metricMaxima = rows.reduce(
      (accumulator, row) => ({
        cpu: Math.max(accumulator.cpu, getDisplayCpuPercent(row)),
        memory: Math.max(accumulator.memory, getDisplayMemoryPercent(row)),
        disk: Math.max(accumulator.disk, coerceNumber(getDisplayDiskBytesPerSecond(row), 0)),
        network: Math.max(accumulator.network, coerceNumber(getDisplayNetworkBytesPerSecond(row), 0)),
      }),
      { cpu: 0, memory: 0, disk: 0, network: 0 }
    );
    return rows.map((row) => ({
      ...row,
      is_expanded: expandedPids.has(row.pid),
      display_cpu_percent: getDisplayCpuPercent(row),
      display_memory_bytes: getDisplayMemoryBytes(row),
      display_memory_percent: getDisplayMemoryPercent(row),
      display_disk_bytes_per_second: getDisplayDiskBytesPerSecond(row),
      display_network_bytes_per_second: getDisplayNetworkBytesPerSecond(row),
      cpu_heat: metricMaxima.cpu > 0 ? getDisplayCpuPercent(row) / metricMaxima.cpu : 0,
      memory_heat: metricMaxima.memory > 0 ? getDisplayMemoryPercent(row) / metricMaxima.memory : 0,
      disk_heat: metricMaxima.disk > 0 ? coerceNumber(getDisplayDiskBytesPerSecond(row), 0) / metricMaxima.disk : 0,
      network_heat:
        metricMaxima.network > 0 ? coerceNumber(getDisplayNetworkBytesPerSecond(row), 0) / metricMaxima.network : 0,
    }));
  }, [expandedPids, filteredProcessRows, sortModel]);

  const selectedProcess = useMemo(
    () => visibleRows.find((row) => row.id === selectedProcessId) || null,
    [selectedProcessId, visibleRows]
  );

  const contextMenuSubject = useMemo(() => {
    const row = contextMenuState?.row || selectedProcess || null;
    if (!row) {
      return {
        title: "Process",
        subtitle: "No process selected",
        row: null,
      };
    }
    return {
      title: row.name || "Process",
      subtitle: [
        `PID ${row.pid || "-"}`,
        row.username,
        row.terminated ? "Terminated" : "",
        formatPercent(getDisplayCpuPercent(row)),
        formatBytes(getDisplayMemoryBytes(row)),
      ]
        .filter(Boolean)
        .join(" • "),
      row,
    };
  }, [contextMenuState?.row, selectedProcess]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current;
    if (!api || !visibleRows.length) return;
    const run = () => {
      try {
        api.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {
        // Ignore auto-size timing errors.
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, [visibleRows.length]);

  const clearCollectingRetry = useCallback(() => {
    if (collectingRetryTimerRef.current && typeof window !== "undefined") {
      window.clearTimeout(collectingRetryTimerRef.current);
    }
    collectingRetryTimerRef.current = null;
    emptyInitialRetryCountRef.current = 0;
    setCollectingInitialSnapshot(false);
  }, []);

  const applyProcessPayload = useCallback((payload = {}) => {
    const rows = Array.isArray(payload?.processes) ? payload.processes.map((row, index) => normalizeProcessRow(row, index)) : [];
    const liveRowsById = new Map(rows.map((row) => [row.id, row]).filter(([id]) => Boolean(id)));
    const liveIds = new Set(liveRowsById.keys());
    const previousLiveRows = previousLiveProcessRowsRef.current;
    const reportedAtSeconds = Number(payload?.reported_at || 0) || Math.floor(Date.now() / 1000);
    setProcessRows(rows);
    setTerminatedProcessRows((current) => {
      let changed = false;
      const next = new Map();
      current.forEach((row, id) => {
        if (liveIds.has(id)) {
          changed = true;
          return;
        }
        next.set(id, row);
      });
      if (previousLiveRows.size) {
        previousLiveRows.forEach((previousRow, id) => {
          if (!id || liveIds.has(id) || next.has(id) || previousRow?.terminated) return;
          next.set(id, createTerminatedProcessRow(previousRow, reportedAtSeconds, "missing_from_snapshot"));
          changed = true;
        });
      }
      return changed ? next : current;
    });
    previousLiveProcessRowsRef.current = liveRowsById;
    setReportedAt(Number(payload?.reported_at || 0) || 0);
    setError("");
    setExpandedPids((current) => {
      const available = new Set(rows.filter((row) => row.pid > 0).map((row) => row.pid));
      return new Set([...current].filter((pid) => available.has(pid)));
    });
    setSelectedProcessId((current) => {
      if (!current) return "";
      return liveIds.has(current) || previousLiveRows.has(current) ? current : "";
    });
  }, []);

  const loadProcesses = useCallback(
    async ({ silent = false } = {}) => {
      if (!hostname || inFlightRef.current) return;
      inFlightRef.current = true;
      let keepInitialLoading = false;
      let retryDelayMs = 0;
      if (!silent) {
        setLoading(true);
      } else {
        setRefreshing(true);
      }
      try {
        const initialSnapshotWanted = !previousLiveProcessRowsRef.current.size;
        const maxAgeSeconds = initialSnapshotWanted ? 0.25 : Math.max(0.25, effectiveRefreshIntervalMs / 1000);
        const response = await fetch(
          `/api/device/processes/${encodeURIComponent(hostname)}?max_age_seconds=${encodeURIComponent(maxAgeSeconds)}`,
          {
            credentials: "include",
          }
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(payload?.message) || normalizeText(payload?.error) || `HTTP ${response.status}`);
        }
        const payloadRows = Array.isArray(payload?.processes) ? payload.processes : [];
        const collectionState = normalizeText(payload?.collection_state).toLowerCase();
        const emptyInitialSnapshot =
          payloadRows.length === 0 && !previousLiveProcessRowsRef.current.size && collectionState !== "ready";
        const shouldRetryEmptyInitial =
          !silent && emptyInitialSnapshot && emptyInitialRetryCountRef.current < PROCESS_INITIAL_EMPTY_RETRY_LIMIT;
        applyProcessPayload(payload);
        if (shouldRetryEmptyInitial) {
          emptyInitialRetryCountRef.current += 1;
          setCollectingInitialSnapshot(true);
          keepInitialLoading = true;
          retryDelayMs = Math.max(
            500,
            Math.min(3000, coerceNumber(payload?.retry_after_ms, PROCESS_INITIAL_EMPTY_RETRY_DELAY_MS))
          );
        } else if (payloadRows.length > 0 || !silent) {
          clearCollectingRetry();
        }
      } catch (err) {
        clearCollectingRetry();
        setError(String(err?.message || err || "Failed to load processes."));
      } finally {
        inFlightRef.current = false;
        if (!silent) {
          setLoading(keepInitialLoading);
        } else {
          setRefreshing(false);
        }
        if (retryDelayMs && typeof window !== "undefined") {
          if (collectingRetryTimerRef.current) {
            window.clearTimeout(collectingRetryTimerRef.current);
          }
          collectingRetryTimerRef.current = window.setTimeout(() => {
            collectingRetryTimerRef.current = null;
            void loadProcesses({ silent: false });
          }, retryDelayMs);
        }
      }
    },
    [applyProcessPayload, clearCollectingRetry, effectiveRefreshIntervalMs, hostname]
  );

  const toggleProcessExpansion = useCallback((row) => {
    const pid = Number(row?.pid || 0);
    if (!pid) return;
    setExpandedPids((previous) => {
      const next = new Set(previous);
      if (next.has(pid)) {
        next.delete(pid);
      } else {
        next.add(pid);
      }
      return next;
    });
  }, []);

  const handleSortChanged = useCallback((event) => {
    const columnState = event?.api?.getColumnState?.() || [];
    const activeSorts = columnState
      .filter((column) => column?.sort)
      .sort((left, right) => Number(left.sortIndex ?? 0) - Number(right.sortIndex ?? 0))
      .map((column) => ({ colId: column.colId, sort: column.sort }));
    setSortModel(activeSorts.length ? activeSorts : [{ colId: "cpu_percent", sort: "desc" }]);
  }, []);

  const handleCloseContextMenu = useCallback(() => {
    setContextMenuState(null);
  }, []);

  const handleCellContextMenu = useCallback((params) => {
    const event = params?.event;
    event?.preventDefault?.();
    event?.stopPropagation?.();
    const row = params?.data || null;
    if (!row) return;
    if (!params?.node?.isSelected?.()) {
      params?.api?.deselectAll?.();
      params?.node?.setSelected?.(true);
    }
    setSelectedProcessId(row.id || "");
    setContextMenuState({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row,
    });
  }, []);

  const copyLocationToClipboard = useCallback(async () => {
    const row = contextMenuSubject.row;
    const location = getProcessLocation(row);
    handleCloseContextMenu();
    if (!location) {
      await notifyOperator({
        title: "Process Management",
        message: "Borealis could not determine a process location to copy.",
        icon: "content_copy",
        variant: "warning",
      });
      return;
    }
    try {
      await navigator.clipboard.writeText(location);
      await notifyOperator({
        title: "Process Management",
        message: `Copied <b>${location}</b> to the clipboard.`,
        icon: "content_copy",
        variant: "info",
      });
    } catch (err) {
      await notifyOperator({
        title: "Process Management",
        message: `Borealis could not copy the location: ${String(err?.message || err)}`,
        icon: "error",
        variant: "error",
      });
    }
  }, [contextMenuSubject.row, handleCloseContextMenu, notifyOperator]);

  const copyCommandToClipboard = useCallback(async () => {
    const row = contextMenuSubject.row;
    const commandLine = getProcessCommandText(row);
    handleCloseContextMenu();
    if (!commandLine) {
      await notifyOperator({
        title: "Process Management",
        message: "Borealis could not determine a process command line to copy.",
        icon: "content_copy",
        variant: "warning",
      });
      return;
    }
    try {
      await navigator.clipboard.writeText(commandLine);
      await notifyOperator({
        title: "Process Management",
        message: "Copied process command line to the clipboard.",
        icon: "content_copy",
        variant: "info",
      });
    } catch (err) {
      await notifyOperator({
        title: "Process Management",
        message: `Borealis could not copy the command line: ${String(err?.message || err)}`,
        icon: "error",
        variant: "error",
      });
    }
  }, [contextMenuSubject.row, handleCloseContextMenu, notifyOperator]);

  const endSelectedTask = useCallback(async () => {
    const row = contextMenuSubject.row;
    const pid = Number(row?.pid || 0);
    handleCloseContextMenu();
    if (!hostname || !pid || actionBusy) return;
    setActionBusy(`terminate:${pid}`);
    setError("");
    try {
      const response = await fetch(`/api/device/processes/${encodeURIComponent(hostname)}/terminate`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pid }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(normalizeText(payload?.message) || normalizeText(payload?.error) || `HTTP ${response.status}`);
      }
      const terminatedRow = createTerminatedProcessRow(row, Math.floor(Date.now() / 1000), "operator_end_task");
      setTerminatedProcessRows((current) => {
        const next = new Map(current);
        next.set(terminatedRow.id, terminatedRow);
        return next;
      });
      applyProcessPayload(payload);
      await notifyOperator({
        title: "Process Management",
        message: `End Task was sent for <b>${normalizeText(row?.name) || `PID ${pid}`}</b>.`,
        icon: "stop_circle",
        variant: "info",
      });
    } catch (err) {
      setError(String(err?.message || err || "End Task failed."));
      await notifyOperator({
        title: "Process Management",
        message: `Borealis could not end <b>${normalizeText(row?.name) || `PID ${pid}`}</b>: ${String(
          err?.message || err
        )}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setActionBusy("");
    }
  }, [actionBusy, applyProcessPayload, contextMenuSubject.row, handleCloseContextMenu, hostname, notifyOperator]);

  const contextMenuActions = useMemo(() => {
    const row = contextMenuSubject.row;
    const processLocation = getProcessLocation(row);
    const commandLine = getProcessCommandText(row);
    const unavailableReason = !row ? "Select a process first." : "";
    const terminatedReason = row?.terminated ? "This process has already terminated." : "";
    return [
      {
        id: "copy-location",
        group: "primary",
        label: "Copy Location to Clipboard",
        icon: ContentCopyRoundedIcon,
        disabled: Boolean(unavailableReason || !processLocation),
        disabledReason: unavailableReason || (!processLocation ? "This process did not report an executable location." : ""),
        description: "Copy the executable path for the selected process.",
        onClick: copyLocationToClipboard,
      },
      {
        id: "copy-command",
        group: "primary",
        label: "Copy Command to Clipboard",
        icon: ContentCopyRoundedIcon,
        disabled: Boolean(unavailableReason || !commandLine),
        disabledReason: unavailableReason || (!commandLine ? "This process did not report a command line." : ""),
        description: "Copy the commandline used to inoke the executable.",
        onClick: copyCommandToClipboard,
      },
      {
        id: "end-task",
        group: "danger",
        label: "End Task",
        icon: StopCircleRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason || terminatedReason || actionBusy),
        disabledReason: unavailableReason || terminatedReason || (actionBusy ? "A process action is already running." : ""),
        description: "Terminate the selected process on the remote device.",
        onClick: endSelectedTask,
      },
    ];
  }, [actionBusy, contextMenuSubject.row, copyCommandToClipboard, copyLocationToClipboard, endSelectedTask]);

  const renderContextMenuItems = useCallback(
    (closeMenu) => {
      const groups = ACTION_MENU_GROUP_ORDER
        .map((groupId) => ({
          id: groupId,
          label: ACTION_MENU_GROUP_LABELS[groupId],
          actions: contextMenuActions.filter((action) => action.group === groupId && !action.hidden),
        }))
        .filter((group) => group.actions.length);
      const nodes = [
        <Box key="context-header" component="li" role="presentation" sx={ACTION_MENU_HEADER_SX}>
          <Box sx={ACTION_MENU_HEADER_ICON_SX}>
            {contextMenuSubject.row?.has_children ? (
              <AccountTreeRoundedIcon sx={{ fontSize: 19, color: "currentColor" }} />
            ) : (
              <TerminalRoundedIcon sx={{ fontSize: 19, color: "currentColor" }} />
            )}
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Tooltip title={contextMenuSubject.title || ""} placement="top-start">
              <Typography
                sx={{
                  ...ACTION_MENU_TITLE_TRUNCATE_SX,
                  color: MAGIC_UI.textBright,
                  fontSize: "0.88rem",
                  fontWeight: 600,
                  lineHeight: 1.2,
                  maxWidth: 240,
                }}
              >
                {contextMenuSubject.title}
              </Typography>
            </Tooltip>
            <Tooltip title={contextMenuSubject.subtitle || ""} placement="top-start">
              <Typography
                sx={{
                  ...ACTION_MENU_TITLE_TRUNCATE_SX,
                  color: "rgba(148,163,184,0.82)",
                  fontSize: "0.73rem",
                  lineHeight: 1.25,
                  mt: 0.22,
                  maxWidth: 240,
                }}
              >
                {contextMenuSubject.subtitle}
              </Typography>
            </Tooltip>
          </Box>
        </Box>,
      ];
      groups.forEach((group) => {
        nodes.push(<Divider key={`divider-before-${group.id}`} component="li" sx={ACTION_MENU_DIVIDER_SX} />);
        nodes.push(
          <Box key={`label-${group.id}`} component="li" role="presentation" sx={ACTION_MENU_SECTION_LABEL_SX}>
            {group.label}
          </Box>
        );
        group.actions.forEach((action) => {
          const IconComponent = action.icon;
          const helperText = action.disabledReason || action.description || "";
          nodes.push(
            <MenuItem
              key={action.id}
              disabled={Boolean(action.disabled)}
              onClick={() => {
                closeMenu();
                action.onClick?.();
              }}
              sx={action.intent === "danger" ? ACTION_MENU_DANGER_ITEM_SX : ACTION_MENU_ITEM_SX}
            >
              <IconComponent
                sx={{
                  ...ACTION_MENU_ROW_ICON_SX,
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
                <Typography sx={ACTION_MENU_LABEL_SX}>{action.label}</Typography>
                {helperText ? <Typography sx={ACTION_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
              </Box>
            </MenuItem>
          );
        });
      });
      return nodes;
    },
    [contextMenuActions, contextMenuSubject]
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        colId: "name",
        minWidth: 260,
        width: 310,
        sortable: true,
        comparator: () => 0,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <ProcessNameCell row={params.data} onToggle={params.context?.toggleProcessExpansion} />,
      },
      {
        headerName: "Owner",
        field: "username",
        colId: "username",
        minWidth: 180,
        width: 180,
        sortable: true,
        comparator: () => 0,
        filter: "agTextColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <ProcessOwnerCell value={params.value} />,
      },
      {
        headerName: "CPU Usage",
        field: "cpu_percent",
        colId: "cpu_percent",
        valueGetter: (params) => getDisplayCpuPercent(params.data),
        minWidth: 180,
        width: 180,
        sortable: true,
        sort: "desc",
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.display_cpu_percent}
            label={formatPercent(params.data?.display_cpu_percent)}
            intensity={params.data?.cpu_heat}
            tone="cpu"
          />
        ),
      },
      {
        headerName: "Memory Usage",
        field: "memory_percent",
        colId: "memory_percent",
        valueGetter: (params) => getDisplayMemoryBytes(params.data),
        minWidth: 180,
        width: 180,
        sortable: true,
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.display_memory_percent}
            label={formatBytes(params.data?.display_memory_bytes)}
            subLabel={formatPercent(params.data?.display_memory_percent)}
            intensity={params.data?.memory_heat}
            tone="memory"
          />
        ),
      },
      {
        headerName: "Disk",
        field: "disk_bytes_per_second",
        colId: "disk_bytes_per_second",
        valueGetter: (params) => getDisplayDiskBytesPerSecond(params.data),
        minWidth: 150,
        width: 155,
        sortable: true,
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.display_disk_bytes_per_second}
            label={formatBytesPerSecond(params.data?.display_disk_bytes_per_second)}
            intensity={params.data?.disk_heat}
            tone="disk"
          />
        ),
      },
      {
        headerName: "Network",
        field: "network_bytes_per_second",
        colId: "network_bytes_per_second",
        valueGetter: (params) => getDisplayNetworkBytesPerSecond(params.data),
        minWidth: 150,
        width: 155,
        sortable: true,
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.display_network_bytes_per_second}
            label={formatBytesPerSecond(params.data?.display_network_bytes_per_second)}
            intensity={params.data?.network_heat}
            tone="network"
          />
        ),
      },
      {
        headerName: "Command Line",
        field: "command_line",
        colId: "command_line",
        valueGetter: (params) => getProcessCommandText(params.data),
        minWidth: 360,
        flex: 1,
        sortable: true,
        comparator: () => 0,
        filter: "agTextColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <CommandLineCell row={params.data} />,
      },
    ],
    []
  );

  const defaultColDef = useMemo(
    () => ({
      ...DEFAULT_GRID_COL_DEF,
      filter: true,
      sortable: true,
      resizable: true,
      minWidth: 120,
    }),
    []
  );

  const gridContext = useMemo(
    () => ({
      toggleProcessExpansion,
    }),
    [toggleProcessExpansion]
  );

  useEffect(() => {
    return () => {
      if (collectingRetryTimerRef.current && typeof window !== "undefined") {
        window.clearTimeout(collectingRetryTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    setProcessRows([]);
    setTerminatedProcessRows(new Map());
    previousLiveProcessRowsRef.current = new Map();
    clearCollectingRetry();
    setReportedAt(0);
    setExpandedPids(new Set());
    setSelectedProcessId("");
    setError("");
    setLoading(Boolean(hostname));
  }, [clearCollectingRetry, hostname]);

  useEffect(() => {
    let cancelled = false;
    let timerId = null;

    const scheduleNext = (delay) => {
      if (cancelled) return;
      timerId = window.setTimeout(async () => {
        await loadProcesses({ silent: true });
        if (cancelled) return;
        scheduleNext(effectiveRefreshIntervalMs);
      }, delay);
    };

    void loadProcesses({ silent: false });
    scheduleNext(effectiveRefreshIntervalMs);

    return () => {
      cancelled = true;
      if (timerId) {
        window.clearTimeout(timerId);
      }
    };
  }, [effectiveRefreshIntervalMs, hostname, loadProcesses]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !hostname) return undefined;
    const normalizedHostname = hostname.toLowerCase();
    const handleProcessesChanged = (payload = {}) => {
      const payloadHostname = normalizeText(payload?.hostname).toLowerCase();
      if (!payloadHostname || payloadHostname !== normalizedHostname) return;
      void loadProcesses({ silent: true });
    };
    socket.on("device_processes_changed", handleProcessesChanged);
    return () => {
      socket.off("device_processes_changed", handleProcessesChanged);
    };
  }, [hostname, loadProcesses]);

  useEffect(() => {
    autoSizeColumns();
  }, [autoSizeColumns, visibleRows]);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (!selectedProcessId) {
      api.deselectAll();
      return;
    }
    api.forEachNode((node) => {
      node.setSelected(node.data?.id === selectedProcessId);
    });
  }, [selectedProcessId, visibleRows]);

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <Stack
        direction={{ xs: "column", md: "row" }}
        spacing={1.5}
        justifyContent="space-between"
        alignItems={{ xs: "flex-start", md: "flex-start" }}
      >
        <Stack spacing={0.7} alignItems="flex-start" sx={{ minWidth: 0 }}>
          <Typography
            component="span"
            sx={{
              color: NAME_LINK_COLOR,
              fontSize: 11,
              fontWeight: 600,
              lineHeight: 1.1,
              pl: 1,
            }}
          >
            Refresh Rate
          </Typography>
          <CountSliderGroup
            options={PROCESS_REFRESH_OPTIONS}
            activeKey={refreshRateKey}
            counts={PROCESS_REFRESH_COUNTS}
            onChange={(key) => {
              setRefreshRateKey(key || "normal");
            }}
          />
        </Stack>
        <Stack spacing={0.7} alignItems={{ xs: "flex-start", md: "flex-end" }} sx={{ ml: "auto", maxWidth: "100%" }}>
          <Stack direction="row" spacing={1} alignItems="center" sx={{ flexWrap: "wrap", rowGap: 0.8, justifyContent: "flex-end" }}>
            <FormControlLabel
              control={
                <Checkbox
                  checked={showSystemProcesses}
                  onChange={(event) => setShowSystemProcesses(Boolean(event.target.checked))}
                  size="small"
                  sx={{
                    color: "rgba(148,163,184,0.86)",
                    "&.Mui-checked": {
                      color: "#7dd3fc",
                    },
                    "& .MuiSvgIcon-root": {
                      fontSize: 18,
                    },
                  }}
                />
              }
              label="Show System Processes"
              sx={{
                m: 0,
                userSelect: "none",
                "& .MuiFormControlLabel-label": {
                  color: MAGIC_UI.textMuted,
                  fontSize: "0.84rem",
                  fontWeight: 500,
                },
              }}
            />
            <FormControlLabel
              control={
                <Checkbox
                  checked={showTerminatedProcesses}
                  onChange={(event) => setShowTerminatedProcesses(Boolean(event.target.checked))}
                  size="small"
                  sx={{
                    color: "rgba(148,163,184,0.86)",
                    "&.Mui-checked": {
                      color: "#7dd3fc",
                    },
                    "& .MuiSvgIcon-root": {
                      fontSize: 18,
                    },
                  }}
                />
              }
              label="Show Terminated Processes"
              sx={{
                m: 0,
                userSelect: "none",
                "& .MuiFormControlLabel-label": {
                  color: MAGIC_UI.textMuted,
                  fontSize: "0.84rem",
                  fontWeight: 500,
                },
              }}
            />
            <Typography
              variant="caption"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                color: "rgba(191,219,254,0.86)",
                border: "1px solid rgba(125,211,252,0.18)",
                borderRadius: 999,
                px: 0.7,
                py: 0.18,
                background: "rgba(15,23,42,0.62)",
                whiteSpace: "nowrap",
              }}
            >
              {filteredProcessRows.length} shown{hiddenProcessCount ? ` • ${hiddenProcessCount} hidden` : ""}
            </Typography>
          </Stack>
          <Stack direction="row" spacing={1} alignItems="center" justifyContent="flex-end" sx={{ flexWrap: "wrap" }}>
            <Typography variant="caption" sx={{ display: "block", color: "rgba(203,213,225,0.78)", textAlign: "right" }}>
              {formatUpdatedAt(reportedAt)}
            </Typography>
            {collectingInitialSnapshot ? (
              <Typography
                variant="caption"
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  color: "#dbeafe",
                  border: "1px solid rgba(125,211,252,0.24)",
                  borderRadius: 999,
                  px: 0.7,
                  py: 0.18,
                  background: "rgba(15,23,42,0.72)",
                  whiteSpace: "nowrap",
                }}
              >
                {PROCESS_COLLECTING_MESSAGE}
              </Typography>
            ) : refreshing ? (
              <Typography
                variant="caption"
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  color: "#c7f9cc",
                  border: "1px solid rgba(74,222,128,0.22)",
                  borderRadius: 999,
                  px: 0.7,
                  py: 0.18,
                  background: "rgba(15,23,42,0.72)",
                }}
              >
                Updating
              </Typography>
            ) : null}
          </Stack>
        </Stack>
      </Stack>

      {(loading || collectingInitialSnapshot || actionBusy) && (
        <LinearProgress
          sx={{
            borderRadius: 999,
            backgroundColor: "rgba(15,23,42,0.75)",
            "& .MuiLinearProgress-bar": {
              backgroundImage: "linear-gradient(90deg,#7dd3fc,#c084fc)",
            },
          }}
        />
      )}

      {error ? (
        <Alert severity="error" sx={{ borderRadius: 2 }}>
          {error}
        </Alert>
      ) : null}

      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 420,
          "--ag-cell-horizontal-padding": "18px",
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "& .ag-row-hover": {
            backgroundColor: "rgba(73,156,196,0.2) !important",
          },
          "& .ag-row-selected": {
            backgroundColor: "rgba(125,211,252,0.2) !important",
            boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
          },
          "& .ag-row.process-row-terminated": {
            background:
              "linear-gradient(90deg, rgba(127,29,29,0.32), rgba(127,29,29,0.16) 46%, rgba(15,23,42,0.2)) !important",
            boxShadow: "inset 3px 0 0 rgba(248,113,113,0.72), inset 0 0 0 1px rgba(248,113,113,0.18)",
          },
          "& .ag-row.process-row-terminated .ag-cell": {
            color: "rgba(254,226,226,0.88)",
          },
          "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            textAlign: "left",
            padding: "8px 12px 8px 18px",
          },
          "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-start",
            padding: 0,
          },
          "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
            paddingLeft: "12px",
            paddingRight: "9px",
          },
        }}
      >
        <AgGridReact
          rowData={visibleRows}
          columnDefs={columnDefs}
          defaultColDef={defaultColDef}
          context={gridContext}
          theme={PROCESS_GRID_THEME}
          getRowId={(params) => params.data?.tree_id || params.data?.id || `${params.data?.pid || "process"}-${params.rowIndex}`}
          getRowClass={(params) => (params.data?.terminated ? "process-row-terminated" : "")}
          rowSelection={{
            mode: "singleRow",
            checkboxes: false,
            headerCheckbox: false,
            enableClickSelection: true,
          }}
          suppressCellFocus
          suppressContextMenu
          preventDefaultOnContextMenu
          pagination
          paginationPageSize={100}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          onGridReady={(params) => {
            gridApiRef.current = params.api;
            autoSizeColumns();
          }}
          onSortChanged={handleSortChanged}
          onCellContextMenu={handleCellContextMenu}
          onSelectionChanged={(event) => {
            const selected = event.api.getSelectedRows?.() || [];
            setSelectedProcessId(selected[0]?.id || "");
          }}
          onRowClicked={(event) => {
            if (event?.event?.defaultPrevented) return;
            if (event?.data?.has_children) {
              toggleProcessExpansion(event.data);
            }
          }}
          overlayNoRowsTemplate={`<span>${loading || collectingInitialSnapshot ? PROCESS_COLLECTING_MESSAGE : "No processes reported."}</span>`}
        />
      </GridShell>

      <Menu
        open={Boolean(contextMenuState?.open)}
        onClose={handleCloseContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={
          contextMenuState?.open
            ? {
                top: Number(contextMenuState?.top || 0),
                left: Number(contextMenuState?.left || 0),
              }
            : undefined
        }
        PaperProps={{ sx: ACTION_MENU_PAPER_SX }}
      >
        {renderContextMenuItems(handleCloseContextMenu)}
      </Menu>
    </Box>
  );
}
