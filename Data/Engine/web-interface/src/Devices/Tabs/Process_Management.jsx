import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Checkbox,
  Divider,
  FormControlLabel,
  IconButton,
  LinearProgress,
  Menu,
  MenuItem,
  Stack,
  Tooltip,
  Typography,
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import { themeQuartz } from "ag-grid-community";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import TerminalRoundedIcon from "@mui/icons-material/TerminalRounded";
import KeyboardArrowRightRoundedIcon from "@mui/icons-material/KeyboardArrowRightRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import StopCircleRoundedIcon from "@mui/icons-material/StopCircleRounded";
import { DEFAULT_GRID_COL_DEF, GridShell, MAGIC_UI } from "./Shared.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";

const PROCESS_POLL_INTERVAL_MS = 5000;
const AUTO_SIZE_COLUMNS = ["name", "cpu_percent", "memory_percent"];
const NAME_LINK_COLOR = "#58a6ff";
const TREE_BOUNDARY_PROCESS_NAMES = new Set([
  "csrss.exe",
  "lsaiso.exe",
  "lsass.exe",
  "memcompression",
  "memory compression",
  "registry",
  "services.exe",
  "smss.exe",
  "svchost.exe",
  "system",
  "system idle process",
  "wininit.exe",
  "winlogon.exe",
]);
const FRIENDLY_PROCESS_NAMES = {
  memcompression: "Memory Compression",
};

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

const REFRESH_ICON_BUTTON_SX = {
  width: 42,
  height: 42,
  borderRadius: "50%",
  flexShrink: 0,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  color: MAGIC_UI.textBright,
  transition: "background-color 0.18s ease, border-color 0.18s ease, transform 0.18s ease",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&:active": {
    transform: "scale(0.98)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.76)",
    borderColor: "rgba(148,163,184,0.22)",
    background: "rgba(15,23,42,0.42)",
  },
};

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
    command_line: normalizeText(row?.command_line),
    executable_path: normalizeText(row?.executable_path),
    child_count: Math.max(0, Math.trunc(coerceNumber(row?.child_count, 0))),
    has_children: Boolean(row?.has_children || coerceNumber(row?.child_count, 0) > 0),
  };
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

function getSortValue(row, columnId) {
  if (columnId === "cpu_percent") return coerceNumber(row?.group_cpu_peak ?? row?.cpu_percent, 0);
  if (columnId === "memory_percent") return coerceNumber(row?.group_memory_peak ?? row?.memory_percent, 0);
  if (columnId === "command_line") return normalizeText(row?.command_line).toLowerCase();
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
    let cpuPeak = row.cpu_percent;
    let memoryPeak = row.memory_percent;
    children.forEach((child) => {
      annotateGroupMetrics(child, seen);
      cpuPeak = Math.max(cpuPeak, coerceNumber(child.group_cpu_peak ?? child.cpu_percent, 0));
      memoryPeak = Math.max(memoryPeak, coerceNumber(child.group_memory_peak ?? child.memory_percent, 0));
    });
    row.group_cpu_peak = cpuPeak;
    row.group_memory_peak = memoryPeak;
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
    tone === "memory"
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
          }}
        >
          {row?.name || "Process"}
        </Typography>
      </Tooltip>
      {row?.pid ? (
        <Typography sx={{ color: "rgba(148,163,184,0.72)", fontSize: "0.72rem", whiteSpace: "nowrap" }}>
          PID {row.pid}
        </Typography>
      ) : null}
    </Box>
  );
}

function CommandLineCell({ value }) {
  const text = normalizeText(value) || "-";
  return (
    <Tooltip title={text} placement="top-start">
      <Typography
        component="span"
        sx={{
          color: "rgba(226,232,240,0.88)",
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

  const hostname = useMemo(() => getHostname(device), [device]);
  const [processRows, setProcessRows] = useState([]);
  const [reportedAt, setReportedAt] = useState(0);
  const [refreshIntervalMs, setRefreshIntervalMs] = useState(PROCESS_POLL_INTERVAL_MS);
  const [expandedPids, setExpandedPids] = useState(() => new Set());
  const [selectedProcessId, setSelectedProcessId] = useState("");
  const [sortModel, setSortModel] = useState([{ colId: "cpu_percent", sort: "desc" }]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [actionBusy, setActionBusy] = useState("");
  const [contextMenuState, setContextMenuState] = useState(null);
  const [hideSvchostProcesses, setHideSvchostProcesses] = useState(true);

  const filteredProcessRows = useMemo(() => {
    const rows = Array.isArray(processRows) ? processRows : [];
    if (!hideSvchostProcesses) return rows;
    return rows.filter((row) => normalizeText(row?.name).toLowerCase() !== "svchost.exe");
  }, [hideSvchostProcesses, processRows]);

  const metricMaxima = useMemo(() => {
    return filteredProcessRows.reduce(
      (accumulator, row) => ({
        cpu: Math.max(accumulator.cpu, coerceNumber(row?.cpu_percent, 0)),
        memory: Math.max(accumulator.memory, coerceNumber(row?.memory_percent, 0)),
      }),
      { cpu: 0, memory: 0 }
    );
  }, [filteredProcessRows]);

  const visibleRows = useMemo(() => {
    const rows = buildVisibleProcessRows(filteredProcessRows, expandedPids, sortModel);
    return rows.map((row) => ({
      ...row,
      is_expanded: expandedPids.has(row.pid),
      cpu_heat: metricMaxima.cpu > 0 ? coerceNumber(row.group_cpu_peak ?? row.cpu_percent, 0) / metricMaxima.cpu : 0,
      memory_heat:
        metricMaxima.memory > 0 ? coerceNumber(row.group_memory_peak ?? row.memory_percent, 0) / metricMaxima.memory : 0,
    }));
  }, [expandedPids, filteredProcessRows, metricMaxima.cpu, metricMaxima.memory, sortModel]);

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
      subtitle: [`PID ${row.pid || "-"}`, formatPercent(row.cpu_percent), formatBytes(row.memory_bytes)].join(" • "),
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

  const applyProcessPayload = useCallback((payload = {}) => {
    const rows = Array.isArray(payload?.processes) ? payload.processes.map((row, index) => normalizeProcessRow(row, index)) : [];
    setProcessRows(rows);
    setReportedAt(Number(payload?.reported_at || 0) || 0);
    setRefreshIntervalMs(Math.max(PROCESS_POLL_INTERVAL_MS, Number(payload?.refresh_interval_ms || PROCESS_POLL_INTERVAL_MS) || PROCESS_POLL_INTERVAL_MS));
    setError("");
    setExpandedPids((current) => {
      const available = new Set(rows.filter((row) => row.pid > 0).map((row) => row.pid));
      return new Set([...current].filter((pid) => available.has(pid)));
    });
    setSelectedProcessId((current) => {
      if (!current) return "";
      return rows.some((row) => row.id === current) ? current : "";
    });
  }, []);

  const loadProcesses = useCallback(
    async ({ silent = false } = {}) => {
      if (!hostname || inFlightRef.current) return;
      inFlightRef.current = true;
      if (!silent) {
        setLoading(true);
      } else {
        setRefreshing(true);
      }
      try {
        const response = await fetch(`/api/device/processes/${encodeURIComponent(hostname)}`, {
          credentials: "include",
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(payload?.message) || normalizeText(payload?.error) || `HTTP ${response.status}`);
        }
        applyProcessPayload(payload);
      } catch (err) {
        setError(String(err?.message || err || "Failed to load processes."));
      } finally {
        inFlightRef.current = false;
        if (!silent) {
          setLoading(false);
        } else {
          setRefreshing(false);
        }
      }
    },
    [applyProcessPayload, hostname]
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
    const unavailableReason = !row ? "Select a process first." : "";
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
        id: "end-task",
        group: "danger",
        label: "End Task",
        icon: StopCircleRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason || actionBusy),
        disabledReason: unavailableReason || (actionBusy ? "A process action is already running." : ""),
        description: "Terminate the selected process on the remote device.",
        onClick: endSelectedTask,
      },
    ];
  }, [actionBusy, contextMenuSubject.row, copyLocationToClipboard, endSelectedTask]);

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
        minWidth: 280,
        width: 310,
        sortable: true,
        comparator: () => 0,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <ProcessNameCell row={params.data} onToggle={params.context?.toggleProcessExpansion} />,
      },
      {
        headerName: "CPU Usage",
        field: "cpu_percent",
        colId: "cpu_percent",
        minWidth: 138,
        width: 150,
        sortable: true,
        sort: "desc",
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.cpu_percent}
            label={formatPercent(params.data?.cpu_percent)}
            intensity={params.data?.cpu_heat}
            tone="cpu"
          />
        ),
      },
      {
        headerName: "Memory Usage",
        field: "memory_percent",
        colId: "memory_percent",
        minWidth: 170,
        width: 180,
        sortable: true,
        comparator: () => 0,
        filter: "agNumberColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <MetricHeatCell
            value={params.data?.memory_percent}
            label={formatBytes(params.data?.memory_bytes)}
            subLabel={formatPercent(params.data?.memory_percent)}
            intensity={params.data?.memory_heat}
            tone="memory"
          />
        ),
      },
      {
        headerName: "Command Line",
        field: "command_line",
        colId: "command_line",
        minWidth: 360,
        flex: 1,
        sortable: true,
        comparator: () => 0,
        filter: "agTextColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => <CommandLineCell value={params.value} />,
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
    setProcessRows([]);
    setReportedAt(0);
    setExpandedPids(new Set());
    setSelectedProcessId("");
    setError("");
    setLoading(Boolean(hostname));
  }, [hostname]);

  useEffect(() => {
    let cancelled = false;
    let timerId = null;

    const scheduleNext = (delay) => {
      if (cancelled) return;
      timerId = window.setTimeout(async () => {
        await loadProcesses({ silent: true });
        if (cancelled) return;
        scheduleNext(Math.max(PROCESS_POLL_INTERVAL_MS, refreshIntervalMs || PROCESS_POLL_INTERVAL_MS));
      }, delay);
    };

    void loadProcesses({ silent: false });
    scheduleNext(Math.max(PROCESS_POLL_INTERVAL_MS, refreshIntervalMs || PROCESS_POLL_INTERVAL_MS));

    return () => {
      cancelled = true;
      if (timerId) {
        window.clearTimeout(timerId);
      }
    };
  }, [hostname, loadProcesses, refreshIntervalMs]);

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
        alignItems={{ xs: "flex-start", md: "center" }}
      >
        <Stack direction="row" spacing={1.15} alignItems="center" sx={{ minWidth: 0 }}>
          <Typography variant="caption" sx={{ display: "block", color: "rgba(203,213,225,0.78)" }}>
            {formatUpdatedAt(reportedAt)}
          </Typography>
          {refreshing ? (
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
        <Stack direction="row" spacing={1} alignItems="center" sx={{ flexWrap: "wrap", rowGap: 0.8 }}>
          <FormControlLabel
            control={
              <Checkbox
                checked={hideSvchostProcesses}
                onChange={(event) => setHideSvchostProcesses(Boolean(event.target.checked))}
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
            label="Hide svchost.exe"
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
          <Tooltip title="Refresh processes" arrow>
            <span>
              <IconButton
                aria-label="Refresh processes"
                onClick={() => {
                  void loadProcesses({ silent: false });
                }}
                disabled={!hostname || loading || Boolean(actionBusy)}
                sx={REFRESH_ICON_BUTTON_SX}
              >
                <RefreshRoundedIcon />
              </IconButton>
            </span>
          </Tooltip>
        </Stack>
      </Stack>

      {(loading || actionBusy) && (
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
          overlayNoRowsTemplate="<span>No processes reported.</span>"
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
