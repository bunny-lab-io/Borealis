import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { AgGridReact } from "ag-grid-react";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  DEVICE_GRID_STYLE,
  GridShell,
  MAGIC_UI,
  gridFontFamily,
} from "./Shared.jsx";
import UninstallProgressDialog from "./Uninstall_Progress_Dialog.jsx";
import { APP_PATHS } from "../../app/routes/paths.js";

const HISTORY_STATUS_THEME = {
  queued: {
    text: "#8fbfff",
    background: "rgba(96,165,250,0.14)",
    border: "1px solid rgba(96,165,250,0.34)",
    dot: "#8fbfff",
  },
  running: {
    text: "#58a6ff",
    background: "rgba(88,166,255,0.15)",
    border: "1px solid rgba(88,166,255,0.4)",
    dot: "#58a6ff",
  },
  success: {
    text: "#00d18c",
    background: "rgba(0,209,140,0.16)",
    border: "1px solid rgba(0,209,140,0.35)",
    dot: "#00d18c",
  },
  failed: {
    text: "#ff7b89",
    background: "rgba(255,123,137,0.16)",
    border: "1px solid rgba(255,123,137,0.35)",
    dot: "#ff7b89",
  },
  default: {
    text: "#e2e8f0",
    background: "rgba(226,232,240,0.12)",
    border: "1px solid rgba(226,232,240,0.25)",
    dot: "#e2e8f0",
  },
};

const ACTIVITY_LINK_COLOR = "#58a6ff";
const ACTIVITY_CONNECTOR_COLOR = ACTIVITY_LINK_COLOR;

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function positiveInteger(value) {
  const numberValue = Number(value || 0);
  return Number.isFinite(numberValue) && numberValue > 0 ? Math.trunc(numberValue) : 0;
}

function internalAppPath(value) {
  const text = String(value || "").trim();
  if (!text || !text.startsWith("/") || text.startsWith("//")) return "";
  return text;
}

function scheduledJobAppPath(value) {
  const text = internalAppPath(value);
  if (!text) return "";
  if (text === APP_PATHS.jobs || text.startsWith(`${APP_PATHS.jobs}/`) || text.startsWith(`${APP_PATHS.jobs}?`)) {
    return text;
  }
  return "";
}

export function formatHistoryScriptType(raw) {
  const value = String(raw || "").toLowerCase();
  if (value === "ansible") return "Ansible Playbook";
  if (value === "reverse_tunnel" || value === "vpn_tunnel") return "Reverse VPN Tunnel";
  return "Script";
}

export function scheduledJobActivityId(row = {}) {
  const metadata = objectValue(row?.metadata);
  const taskLink = objectValue(row?.task_link || metadata?.task_link);
  return (
    positiveInteger(row?.scheduled_job_id) ||
    positiveInteger(row?.scheduledJobId) ||
    positiveInteger(metadata?.scheduled_job_id) ||
    positiveInteger(metadata?.scheduledJobId) ||
    positiveInteger(taskLink?.job_id)
  );
}

export function scheduledJobActivityPath(row = {}) {
  const jobId = scheduledJobActivityId(row);
  if (!jobId) return "";
  const metadata = objectValue(row?.metadata);
  const taskLink = objectValue(row?.task_link || metadata?.task_link);
  return scheduledJobAppPath(taskLink?.path || metadata?.scheduled_job_path) || `${APP_PATHS.job(jobId)}?tab=job_history`;
}

export function historyActivityLabel(row = {}) {
  const jobId = scheduledJobActivityId(row);
  const rawLabel = String(row?.scheduled_job_name || row?.scheduledJobName || row?.script_display_name || row?.script_name || "").trim();
  if (rawLabel) return rawLabel;
  if (jobId) return `#${jobId}`;
  const activityKind = String(row?.activity_kind || "").trim().toLowerCase();
  if (activityKind === "software_uninstall") return "Activity";
  const scriptType = String(row?.script_type || "").trim().toLowerCase();
  if (scriptType === "powershell" || scriptType === "batch" || scriptType === "bash" || scriptType === "script") {
    return "Activity";
  }
  return formatHistoryScriptType(row?.script_type);
}

export function historyActivityGroupKey(row = {}) {
  const metadata = objectValue(row?.metadata);
  const jobId = scheduledJobActivityId(row);
  if (jobId) {
    const runId =
      positiveInteger(row?.scheduled_job_run_id) ||
      positiveInteger(row?.scheduledJobRunId) ||
      positiveInteger(metadata?.scheduled_job_run_id) ||
      positiveInteger(metadata?.scheduledJobRunId) ||
      positiveInteger(metadata?.scheduled_run_id) ||
      positiveInteger(metadata?.scheduledRunId);
    const scheduledTs = positiveInteger(row?.scheduled_ts) || positiveInteger(metadata?.scheduled_ts);
    const occurrenceId = runId || scheduledTs || positiveInteger(row?.ran_at) || positiveInteger(row?.id);
    return `scheduled:${jobId}:${occurrenceId}`;
  }
  const rowId = positiveInteger(row?.id) || positiveInteger(row?.jobId);
  if (rowId) return `activity:${rowId}`;
  return `activity:${historyActivityLabel(row)}`;
}

function browserTextWidth(text) {
  if (typeof document === "undefined") return String(text || "").length * 8;
  const canvas = browserTextWidth.canvas || document.createElement("canvas");
  browserTextWidth.canvas = canvas;
  const context = canvas.getContext("2d");
  if (!context) return String(text || "").length * 8;
  context.font = `600 13px ${gridFontFamily}`;
  return context.measureText(String(text || "")).width;
}

export function historyActivityColumnWidth(rows = [], measureText = browserTextWidth) {
  const labels = ["Activity", ...(Array.isArray(rows) ? rows.map((row) => row?.activity_label || historyActivityLabel(row)) : [])];
  const widest = labels.reduce((maxWidth, label) => Math.max(maxWidth, Number(measureText(label) || 0)), 0);
  return Math.max(190, Math.ceil(widest + 72));
}

export function decorateHistoryActivityRows(rows = []) {
  const decorated = (Array.isArray(rows) ? rows : []).map((row) => {
    const activityLabel = historyActivityLabel(row);
    return {
      ...row,
      activity_label: activityLabel,
      activity_group_key: historyActivityGroupKey(row),
    };
  });
  const groupCounts = decorated.reduce((counts, row) => {
    const key = row.activity_group_key;
    counts.set(key, (counts.get(key) || 0) + 1);
    return counts;
  }, new Map());
  const groupIndexes = new Map();
  return decorated.map((row) => {
    const key = row.activity_group_key;
    const index = groupIndexes.get(key) || 0;
    groupIndexes.set(key, index + 1);
    return {
      ...row,
      activity_group_size: groupCounts.get(key) || 1,
      activity_group_index: index,
    };
  });
}

function connectorCoordinate(value) {
  const numberValue = Number(value || 0);
  if (!Number.isFinite(numberValue)) return "0";
  const rounded = Math.round(numberValue * 100) / 100;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
}

export function activityConnectorPaths(rowCount = 0, width = 100, height = 0, sourceY = 50) {
  const count = positiveInteger(rowCount);
  if (count <= 1) return [];
  const connectorWidth = Math.max(1, Number(width || 0));
  const connectorHeight = Math.max(1, Number(height || count * 100));
  const boundedSourceY = Math.min(Math.max(Number(sourceY || 0), 0), connectorHeight);
  const controlX1 = connectorWidth * 0.32;
  const controlX2 = connectorWidth * 0.68;
  const rowHeight = connectorHeight / count;
  return Array.from({ length: count }, (_unused, index) => {
    const targetY = index * rowHeight + rowHeight / 2;
    return [
      `M 0 ${connectorCoordinate(boundedSourceY)}`,
      `C ${connectorCoordinate(controlX1)} ${connectorCoordinate(boundedSourceY)},`,
      `${connectorCoordinate(controlX2)} ${connectorCoordinate(targetY)},`,
      `${connectorCoordinate(connectorWidth)} ${connectorCoordinate(targetY)}`,
    ].join(" ");
  });
}

function sameConnectorGeometry(left, right) {
  if (!left || !right) return left === right;
  return left.left === right.left && left.width === right.width && left.height === right.height && left.sourceY === right.sourceY;
}

export function formatActivityTimelineTimestamp(epochSec = 0) {
  const ts = positiveInteger(epochSec);
  if (!ts) return "";
  const date = new Date(ts * 1000);
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  const yyyy = date.getFullYear();
  let hh = date.getHours();
  const ampm = hh >= 12 ? "PM" : "AM";
  hh = hh % 12 || 12;
  const min = String(date.getMinutes()).padStart(2, "0");
  return `${mm}/${dd}/${yyyy} @ ${hh}:${min}${ampm}`;
}

export function formatActivityTimelineDuration(startEpoch = 0, endEpoch = 0) {
  const start = positiveInteger(startEpoch);
  const end = positiveInteger(endEpoch);
  if (!start || !end) return "";
  const totalSeconds = Math.max(0, end - start);
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const parts = [];
  if (days) parts.push(`${days}d`);
  if (hours) parts.push(`${hours}h`);
  if (minutes) parts.push(`${minutes}m`);
  if (seconds || parts.length === 0) parts.push(`${seconds}s`);
  return parts.join(" ");
}

function isActivityRunning(row = {}) {
  return String(row?.status || "").trim().toLowerCase() === "running";
}

export function activityTimelineSortValue(row = {}) {
  return (
    positiveInteger(row?.started_at) ||
    positiveInteger(row?.startedAt) ||
    positiveInteger(row?.ran_at) ||
    positiveInteger(row?.ranAt) ||
    positiveInteger(row?.updated_at) ||
    positiveInteger(row?.updatedAt) ||
    positiveInteger(row?.id)
  );
}

export function activityTimelineParts(row = {}, nowEpoch = 0) {
  const running = isActivityRunning(row);
  const startAt = positiveInteger(row?.started_at) || positiveInteger(row?.startedAt) || positiveInteger(row?.ran_at) || positiveInteger(row?.ranAt);
  const terminalEndAt =
    positiveInteger(row?.finished_at) ||
    positiveInteger(row?.finishedAt) ||
    (!running ? positiveInteger(row?.updated_at) || positiveInteger(row?.updatedAt) : 0);
  const liveEndAt = running ? positiveInteger(nowEpoch) || Math.floor(Date.now() / 1000) : 0;
  const durationEndAt = running ? liveEndAt : terminalEndAt;
  return {
    running,
    startAt,
    endAt: terminalEndAt,
    startText: formatActivityTimelineTimestamp(startAt),
    endText: running ? "" : formatActivityTimelineTimestamp(terminalEndAt),
    durationText: formatActivityTimelineDuration(startAt, durationEndAt),
  };
}

const StatusPillCell = React.memo(function StatusPillCell(props) {
  const value = String(props?.value || "");
  if (!value) return null;
  const theme = HISTORY_STATUS_THEME[value.toLowerCase()] || HISTORY_STATUS_THEME.default;
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        minWidth: 76,
        px: 1.5,
        py: 0.4,
        borderRadius: 999,
        backgroundColor: theme.background,
        border: theme.border,
        color: theme.text,
        fontWeight: 600,
        fontSize: "13px",
        lineHeight: 1,
        fontFamily: gridFontFamily,
        textTransform: "capitalize",
        gap: 0.75,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(0, 0, 0, 0.22)",
        }}
      />
      {value}
    </Box>
  );
});

const ActivityConnectorSvg = React.memo(function ActivityConnectorSvg({ rowCount = 0, geometry = null }) {
  const width = Number(geometry?.width || 0);
  const height = Number(geometry?.height || 0);
  const paths = width > 12 && height > 1 ? activityConnectorPaths(rowCount, width, height, geometry?.sourceY) : [];
  if (!paths.length) return null;
  return (
    <Box
      component="svg"
      viewBox={`0 0 ${connectorCoordinate(width)} ${connectorCoordinate(height)}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      sx={{
        position: "absolute",
        left: geometry?.left || 0,
        top: 0,
        width,
        height,
        zIndex: 0,
        pointerEvents: "none",
        opacity: 0.55,
      }}
    >
      {paths.map((path, index) => (
        <path
          key={`${index}:${path}`}
          d={path}
          fill="none"
          stroke={ACTIVITY_CONNECTOR_COLOR}
          strokeWidth="1.8"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
        />
      ))}
    </Box>
  );
});

const HistoryActivityCell = React.memo(function HistoryActivityCell(props) {
  const row = props.data || {};
  const label = String(row?.activity_label || historyActivityLabel(row)).trim() || "Activity";
  const jobPath = scheduledJobActivityPath(row);
  const groupSize = positiveInteger(row?.activity_group_size);
  const cellRef = useRef(null);
  const labelRef = useRef(null);
  const [connectorGeometry, setConnectorGeometry] = useState(null);
  useLayoutEffect(() => {
    if (groupSize <= 1) {
      setConnectorGeometry((previous) => (previous ? null : previous));
      return undefined;
    }

    const updateConnectorGeometry = () => {
      const cellNode = cellRef.current;
      const labelNode = labelRef.current;
      if (!cellNode || !labelNode) {
        setConnectorGeometry((previous) => (previous ? null : previous));
        return;
      }

      const cellRect = cellNode.getBoundingClientRect();
      const labelRect = labelNode.getBoundingClientRect();
      const left = Math.ceil(labelRect.right - cellRect.left + 8);
      const width = Math.floor(cellRect.width - left);
      const height = Math.floor(cellRect.height);
      const sourceY = Math.round(labelRect.top - cellRect.top + labelRect.height / 2);
      const nextGeometry = width > 12 && height > 1 ? { left, width, height, sourceY } : null;
      setConnectorGeometry((previous) => (sameConnectorGeometry(previous, nextGeometry) ? previous : nextGeometry));
    };

    updateConnectorGeometry();

    const resizeObserver = typeof ResizeObserver !== "undefined" ? new ResizeObserver(updateConnectorGeometry) : null;
    if (resizeObserver && cellRef.current && labelRef.current) {
      resizeObserver.observe(cellRef.current);
      resizeObserver.observe(labelRef.current);
    }
    if (typeof window !== "undefined") window.addEventListener("resize", updateConnectorGeometry);

    return () => {
      if (resizeObserver) resizeObserver.disconnect();
      if (typeof window !== "undefined") window.removeEventListener("resize", updateConnectorGeometry);
    };
  }, [groupSize, label, jobPath]);
  const labelSx = {
    position: "relative",
    zIndex: 1,
    display: "inline-flex",
    minWidth: 0,
    maxWidth: "100%",
    alignItems: "center",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    fontWeight: 600,
    fontFamily: gridFontFamily,
    lineHeight: 1.2,
  };
  const cellSx = {
    position: "relative",
    display: "block",
    boxSizing: "border-box",
    height: "100%",
    minWidth: 0,
    overflow: "hidden",
  };
  const labelRowSx = {
    position: "relative",
    zIndex: 1,
    display: "flex",
    alignItems: "center",
    height: "min(100%, var(--ag-row-height, 42px))",
    minWidth: 0,
  };
  if (!jobPath) {
    return (
      <Box
        ref={cellRef}
        component="span"
        sx={cellSx}
      >
        <ActivityConnectorSvg rowCount={groupSize} geometry={connectorGeometry} />
        <Box component="span" sx={labelRowSx}>
          <Box ref={labelRef} component="span" sx={{ ...labelSx, color: "#dbeafe" }}>
            {label}
          </Box>
        </Box>
      </Box>
    );
  }
  return (
    <Box
      ref={cellRef}
      sx={cellSx}
    >
      <ActivityConnectorSvg rowCount={groupSize} geometry={connectorGeometry} />
      <Box component="span" sx={labelRowSx}>
        <Box
          ref={labelRef}
          component={Link}
          to={jobPath}
          title={`Open ${label}`}
          onPointerDown={(event) => event.stopPropagation()}
          onClick={(event) => event.stopPropagation()}
          sx={{
            ...labelSx,
            color: ACTIVITY_LINK_COLOR,
            textDecoration: "none",
            "&:hover": {
              color: "#8fbfff",
              textDecoration: "underline",
            },
          }}
        >
          {label}
        </Box>
      </Box>
    </Box>
  );
});

const HistoryTaskCell = React.memo(function HistoryTaskCell(props) {
  const row = props.data || {};
  const onOpenUninstall = props.context?.onOpenUninstall;
  const label = String(props?.value || row?.script_display_name || row?.script_name || "Activity").trim() || "Activity";
  const isUninstall = String(row?.activity_kind || "").trim().toLowerCase() === "software_uninstall";
  if (!isUninstall) {
    return (
      <Box
        component="span"
        sx={{
          display: "inline-block",
          minWidth: 0,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {label}
      </Box>
    );
  }
  return (
    <Button
      size="small"
      onClick={() => onOpenUninstall && onOpenUninstall(row)}
      sx={{
        p: 0,
        minWidth: 0,
        justifyContent: "flex-start",
        textTransform: "none",
        color: "#dbeafe",
        fontWeight: 600,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
        "&:hover": {
          backgroundColor: "transparent",
          color: "#dbeafe",
        },
      }}
    >
      {label}
    </Button>
  );
});

const HistoryTimelineCell = React.memo(function HistoryTimelineCell(props) {
  const row = props.data || {};
  const running = isActivityRunning(row);
  const [nowEpoch, setNowEpoch] = useState(() => Math.floor(Date.now() / 1000));

  useEffect(() => {
    if (!running) return undefined;
    const tick = () => setNowEpoch(Math.floor(Date.now() / 1000));
    tick();
    const intervalId = setInterval(tick, 1000);
    return () => clearInterval(intervalId);
  }, [running]);

  const timeline = activityTimelineParts(row, nowEpoch);
  if (!timeline.startText) {
    return (
      <Box component="span" sx={{ color: "#6b7280" }}>
        —
      </Box>
    );
  }

  const rangeText = timeline.endText ? `${timeline.startText} - ${timeline.endText}` : timeline.startText;
  const title = timeline.durationText ? `${rangeText} ${timeline.durationText}` : rangeText;
  return (
    <Box
      component="span"
      title={title}
      sx={{
        display: "flex",
        alignItems: "center",
        minWidth: 0,
        overflow: "hidden",
        whiteSpace: "nowrap",
      }}
    >
      <Box
        component="span"
        sx={{
          minWidth: 0,
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {rangeText}
      </Box>
      {timeline.durationText ? (
        <Box
          component="span"
          sx={{
            flex: "0 0 auto",
            ml: 0.75,
            color: "#6b7280",
          }}
        >
          {timeline.durationText}
        </Box>
      ) : null}
    </Box>
  );
});

const HistoryActionsCell = React.memo(function HistoryActionsCell(props) {
  const row = props.data || {};
  const onViewOutput = props.context?.onViewOutput;

  return (
    <Box sx={{ display: "flex", gap: 1, alignItems: "center", height: "100%", minWidth: 0 }}>
      {row.has_stdout ? (
        <Button
          size="small"
          sx={{
            alignItems: "center",
            color: MAGIC_UI.accentA,
            display: "inline-flex",
            lineHeight: 1,
            minWidth: 0,
            p: 0,
            textTransform: "none",
          }}
          onClick={() => onViewOutput && onViewOutput(row, "stdout")}
        >
          StdOut
        </Button>
      ) : null}
      {row.has_stderr ? (
        <Button
          size="small"
          sx={{
            alignItems: "center",
            color: "#ff7b89",
            display: "inline-flex",
            lineHeight: 1,
            minWidth: 0,
            p: 0,
            textTransform: "none",
          }}
          onClick={() => onViewOutput && onViewOutput(row, "stderr")}
        >
          StdErr
        </Button>
      ) : null}
    </Box>
  );
});

const GRID_COMPONENTS = {
  StatusPillCell,
  HistoryActionsCell,
  HistoryTaskCell,
  HistoryActivityCell,
  HistoryTimelineCell,
};

export default function ActivityHistoryTab({ hostname = "", refreshToken = 0 }) {
  const [historyRows, setHistoryRows] = useState([]);
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputContent, setOutputContent] = useState("");
  const [outputLang, setOutputLang] = useState("powershell");
  const [assemblyNameMap, setAssemblyNameMap] = useState({});
  const [selectedUninstallJob, setSelectedUninstallJob] = useState(null);
  const [selectedUninstallJobId, setSelectedUninstallJobId] = useState(0);

  const normalizedHostname = useMemo(() => String(hostname || "").trim(), [hostname]);

  const buildUninstallDialogJob = useCallback(
    (payload = {}) => {
      const metadata = payload?.metadata && typeof payload.metadata === "object" ? payload.metadata : {};
      return {
        jobId: Number(payload?.id || 0) || 0,
        hostname: String(payload?.hostname || normalizedHostname || "").trim(),
        softwareName:
          String(metadata?.software_name || "").trim() ||
          (String(payload?.script_name || "").trim().toLowerCase().startsWith("uninstall - ")
            ? String(payload?.script_name || "").trim().slice("Uninstall - ".length).trim()
            : ""),
        softwareVersion: String(metadata?.software_version || "").trim(),
        softwareSource: String(metadata?.software_source || "").trim(),
        commandPreview: String(metadata?.command_preview || "").trim(),
        status: payload?.status,
        stdout: String(payload?.stdout || ""),
        stderr: String(payload?.stderr || ""),
        queueLane: String(payload?.queue_lane || "").trim(),
        activityKind: String(payload?.activity_kind || "").trim(),
        ranAt: Number(payload?.ran_at || 0) || 0,
        queuedAt: Number(payload?.ran_at || 0) || 0,
        startedAt: Number(payload?.started_at || 0) || 0,
        updatedAt: Number(payload?.updated_at || 0) || 0,
        finishedAt: Number(payload?.finished_at || 0) || 0,
      };
    },
    [normalizedHostname]
  );

  useEffect(() => {
    let canceled = false;

    const loadAssemblyNames = async () => {
      const next = {};
      const storeName = (rawPath, rawName) => {
        const name = typeof rawName === "string" ? rawName.trim() : "";
        if (!name) return;
        const normalizedPath = String(rawPath || "")
          .replace(/\\/g, "/")
          .replace(/^\/+/, "")
          .trim();
        if (!normalizedPath) return;
        if (!next[normalizedPath]) next[normalizedPath] = name;
        const base = normalizedPath.split("/").pop() || "";
        if (base && !next[base]) next[base] = name;
        const dot = base.lastIndexOf(".");
        if (dot > 0) {
          const baseNoExt = base.slice(0, dot);
          if (baseNoExt && !next[baseNoExt]) next[baseNoExt] = name;
        }
      };

      try {
        const resp = await fetch("/api/assemblies");
        if (!resp.ok) return;
        const data = await resp.json();
        const items = Array.isArray(data?.items) ? data.items : [];
        items.forEach((item) => {
          if (!item || typeof item !== "object") return;
          const displayName = (item.display_name || "").trim() || item.assembly_guid || "";
          if (!displayName) return;
          storeName(item.virtual_path || item.path || "", displayName);
          if (item.assembly_guid && !next[item.assembly_guid]) {
            next[item.assembly_guid] = displayName;
          }
          if (item.payload_guid && !next[item.payload_guid]) {
            next[item.payload_guid] = displayName;
          }
        });
      } catch {
        // Ignore enrichment failures and fall back to raw names.
      }

      if (!canceled) {
        setAssemblyNameMap(next);
      }
    };

    loadAssemblyNames();

    return () => {
      canceled = true;
    };
  }, []);

  const resolveAssemblyName = useCallback(
    (scriptName, scriptPath) => {
      const normalized = String(scriptPath || "").replace(/\\/g, "/").trim();
      const base = normalized ? normalized.split("/").pop() || "" : "";
      const baseNoExt = base && base.includes(".") ? base.slice(0, base.lastIndexOf(".")) : base;
      return (
        assemblyNameMap[normalized] ||
        (base ? assemblyNameMap[base] : "") ||
        (baseNoExt ? assemblyNameMap[baseNoExt] : "") ||
        scriptName ||
        base ||
        scriptPath ||
        ""
      );
    },
    [assemblyNameMap]
  );

  const historyDisplayRows = useMemo(
    () =>
      decorateHistoryActivityRows(
        (historyRows || []).map((row) => ({
          ...row,
          script_display_name: resolveAssemblyName(row.script_name, row.script_path),
        }))
      ),
    [historyRows, resolveAssemblyName]
  );

  const activityColumnMinWidth = useMemo(
    () => historyActivityColumnWidth(historyDisplayRows),
    [historyDisplayRows]
  );
  const hasRunningHistoryRows = useMemo(
    () => historyDisplayRows.some((row) => isActivityRunning(row)),
    [historyDisplayRows]
  );

  const loadHistory = useCallback(async () => {
    if (!normalizedHostname) {
      setHistoryRows([]);
      return;
    }
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(normalizedHostname)}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setHistoryRows(data.history || []);
    } catch (error) {
      console.warn("Failed to load activity history", error);
      setHistoryRows([]);
    }
  }, [normalizedHostname]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory, refreshToken]);

  useEffect(() => {
    if (!normalizedHostname || !hasRunningHistoryRows) return undefined;
    const intervalId = setInterval(loadHistory, 10000);
    return () => clearInterval(intervalId);
  }, [hasRunningHistoryRows, loadHistory, normalizedHostname]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedHostname) return undefined;

    let refreshTimer = null;
    const expectedHost = normalizedHostname.toLowerCase();
    const scheduleRefresh = (delay = 200) => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        loadHistory();
      }, delay);
    };

    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      if (!payloadHost || payloadHost !== expectedHost) return;
      scheduleRefresh(payload?.change === "updated" ? 150 : 0);
    };

    socket.on("device_activity_changed", handleActivityChanged);

    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [loadHistory, normalizedHostname]);

  const historyColumnDefs = useMemo(
    () => [
      {
        headerName: "Activity",
        field: "activity_group_key",
        colId: "activity",
        flex: 1,
        minWidth: activityColumnMinWidth,
        valueGetter: (params) => params.data?.activity_group_key || historyActivityGroupKey(params.data || {}),
        filterValueGetter: (params) => params.data?.activity_label || historyActivityLabel(params.data || {}),
        tooltipValueGetter: (params) => params.data?.activity_label || historyActivityLabel(params.data || {}),
        comparator: (_left, _right, nodeA, nodeB) =>
          String(nodeA?.data?.activity_label || "").localeCompare(String(nodeB?.data?.activity_label || "")),
        spanRows: ({ valueA, valueB }) => Boolean(valueA && valueA === valueB),
        cellRenderer: "HistoryActivityCell",
      },
      {
        headerName: "Task",
        field: "script_display_name",
        flex: 0,
        width: 280,
        minWidth: 240,
        filter: "agTextColumnFilter",
        cellRenderer: "HistoryTaskCell",
      },
      {
        headerName: "Timeline",
        colId: "timeline",
        field: "started_at",
        flex: 0,
        width: 390,
        minWidth: 340,
        valueGetter: (params) => activityTimelineSortValue(params.data || {}),
        tooltipValueGetter: (params) => {
          const timeline = activityTimelineParts(params.data || {});
          if (!timeline.startText) return "—";
          const rangeText = timeline.endText ? `${timeline.startText} - ${timeline.endText}` : timeline.startText;
          return timeline.durationText ? `${rangeText} ${timeline.durationText}` : rangeText;
        },
        sort: "desc",
        comparator: (a, b) => (a || 0) - (b || 0),
        cellRenderer: "HistoryTimelineCell",
      },
      {
        headerName: "Job Status",
        field: "status",
        flex: 0,
        width: 160,
        cellRenderer: "StatusPillCell",
      },
      {
        headerName: "StdOut / StdErr",
        colId: "stdout",
        flex: 0,
        width: 220,
        sortable: false,
        filter: false,
        cellRenderer: "HistoryActionsCell",
      },
    ],
    [activityColumnMinWidth]
  );

  const highlightCode = useCallback((code, lang) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
    }
  }, []);

  const handleViewOutput = useCallback(
    async (row, which) => {
      if (!row || !row.id) return;
      try {
        const resp = await fetch(`/api/device/activity/job/${row.id}`);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        const scriptPath = String(data.script_path || "").toLowerCase();
        const lang = scriptPath.endsWith(".ps1")
          ? "powershell"
          : scriptPath.endsWith(".bat")
          ? "batch"
          : scriptPath.endsWith(".sh")
          ? "bash"
          : scriptPath.endsWith(".yml")
          ? "yaml"
          : "powershell";
        const friendly = resolveAssemblyName(data.script_name, data.script_path);
        setOutputLang(lang);
        setOutputTitle(`${which === "stderr" ? "StdErr" : "StdOut"} - ${friendly}`);
        setOutputContent(which === "stderr" ? data.stderr || "" : data.stdout || "");
        setOutputOpen(true);
      } catch (error) {
        console.warn("Failed to load output", error);
      }
    },
    [resolveAssemblyName]
  );

  const handleOpenUninstall = useCallback(
    async (row) => {
      const jobId = Number(row?.id || row?.jobId || 0);
      if (!Number.isFinite(jobId) || jobId <= 0) return;
      try {
        const resp = await fetch(`/api/device/activity/job/${jobId}`);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const payload = await resp.json().catch(() => ({}));
        setSelectedUninstallJobId(jobId);
        setSelectedUninstallJob(buildUninstallDialogJob(payload));
      } catch (error) {
        console.warn("Failed to load uninstall activity", error);
      }
    },
    [buildUninstallDialogJob]
  );

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || !normalizedHostname || !selectedUninstallJobId) return undefined;
    const expectedHost = normalizedHostname.toLowerCase();
    const handleActivityChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      const activityId = Number(payload?.activity_id || 0);
      if (!payloadHost || payloadHost !== expectedHost || activityId !== Number(selectedUninstallJobId || 0)) return;
      void handleOpenUninstall({ id: activityId });
    };
    socket.on("device_activity_changed", handleActivityChanged);
    return () => {
      socket.off("device_activity_changed", handleActivityChanged);
    };
  }, [handleOpenUninstall, normalizedHostname, selectedUninstallJobId]);

  const getHistoryRowId = useCallback((params) => String(params.data?.id || params.rowIndex), []);

  const historyGridContext = useMemo(
    () => ({
      onViewOutput: handleViewOutput,
      onOpenUninstall: handleOpenUninstall,
    }),
    [handleOpenUninstall, handleViewOutput]
  );

  return (
    <>
      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 360,
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "& .ag-row-hover": {
            backgroundColor: "rgba(73,156,196,0.2) !important",
          },
        }}
      >
        <AgGridReact
          rowData={historyDisplayRows}
          columnDefs={historyColumnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          enableCellSpan
          components={GRID_COMPONENTS}
          context={historyGridContext}
          getRowId={getHistoryRowId}
          suppressCellFocus
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={outputTitle} subtitle="Review command output and captured response data." />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box
            sx={{
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              bgcolor: "rgba(4,7,17,0.65)",
              maxHeight: "56vh",
              overflowY: "auto",
              overflowX: "auto",
              overscrollBehavior: "contain",
              scrollbarGutter: "stable both-edges",
              "&::-webkit-scrollbar": {
                width: 10,
                height: 10,
              },
              "&::-webkit-scrollbar-track": {
                background: "rgba(15,23,42,0.45)",
                borderRadius: 999,
              },
              "&::-webkit-scrollbar-thumb": {
                background: "rgba(125,183,255,0.35)",
                borderRadius: 999,
                border: "2px solid rgba(15,23,42,0.45)",
              },
            }}
          >
            <Editor
              value={outputContent}
              onValueChange={() => {}}
              highlight={(code) => highlightCode(code, outputLang)}
              padding={12}
              style={{
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                fontSize: 12,
                color: "#e6edf3",
                minHeight: 200,
                whiteSpace: "pre",
                overflowWrap: "normal",
                wordBreak: "normal",
              }}
              textareaProps={{ readOnly: true, wrap: "off", spellCheck: false }}
            />
          </Box>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setOutputOpen(false)} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>
      <UninstallProgressDialog
        open={Boolean(selectedUninstallJob)}
        job={selectedUninstallJob}
        onClose={() => {
          setSelectedUninstallJob(null);
          setSelectedUninstallJobId(0);
        }}
      />
    </>
  );
}
