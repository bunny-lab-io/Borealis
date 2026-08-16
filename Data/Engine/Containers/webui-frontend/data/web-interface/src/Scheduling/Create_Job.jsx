import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Box,
  Typography,
  Tabs,
  Tab,
  TextField,
  InputBase,
  Button,
  IconButton,
  Tooltip,
  Checkbox,
  FormControlLabel,
  MenuItem,
  Divider,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  CircularProgress
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  ContentCopy as ContentCopyIcon,
  FilterList as FilterListIcon,
  PendingActions as PendingActionsIcon,
  Check as CheckIcon,
  PlayArrow as PlayArrowIcon,
  Refresh as RefreshIcon,
  Search as SearchIcon,
  Apps as AppsIcon,
  DriveFileRenameOutline as DriveFileRenameOutlineIcon,
  DevicesRounded as DevicesRoundedIcon,
  ScheduleRounded as ScheduleRoundedIcon,
  SettingsApplicationsRounded as SettingsApplicationsRoundedIcon,
  HistoryRounded as HistoryRoundedIcon,
  TerminalRounded as TerminalRoundedIcon,
  MenuBookRounded as MenuBookRoundedIcon,
  AccountTreeRounded as AccountTreeRoundedIcon
} from "@mui/icons-material";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";
import {
  dayjsFromEngineClock,
  dayjsFromEpochInTimezone,
  fetchEngineScheduleClock,
  wallClockStringFromEnginePickerValue,
} from "./scheduleTime.js";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DirtyStatePill, DomainBadge } from "../Assemblies/Assembly_Badges";
import AssemblyPicker from "../Assemblies/Assembly_Picker.jsx";
import {
  buildAssemblyIndex,
  parseAssembliesCollectionPayload,
  normalizeAssemblyPath,
  parseAssemblyExport,
  resolveAssemblyForComponent
} from "../Assemblies/assemblyUtils";
import {
  extractWorkflowCanvasDocument,
  inspectWorkflowRuntimeDocument,
} from "../Flow_Editor/workflowDocuments";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useUrlTabState } from "../app/hooks/useUrlTabState.js";
import { useAuth } from "../app/providers/AuthContext.jsx";
import { APP_PATHS } from "../app/routes/paths.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg:
    "linear-gradient(145deg, rgba(7,10,24,0.96), rgba(6,10,28,0.92) 45%, rgba(14,8,30,0.95))",
  panelBorder: "rgba(148, 163, 184, 0.32)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  glow: "0 30px 70px rgba(2,6,23,0.85)",
};

const PAGE_ICON = PendingActionsIcon;
const PAGE_TITLE = "Create Job";
const PAGE_SUBTITLE = "Configure scheduled or immediate jobs against targeted devices or filters.";
const CREATE_JOB_TAB_URL_BY_KEY = Object.freeze({
  name: "job_name",
  components: "assemblies",
  targets: "targets",
  schedule: "schedule",
  context: "execution_context",
  history: "job_history",
});
const CREATE_JOB_TAB_KEY_BY_URL = Object.freeze({
  job_name: "name",
  name: "name",
  assemblies: "components",
  components: "components",
  targets: "targets",
  schedule: "schedule",
  execution_context: "context",
  context: "context",
  job_history: "history",
  history: "history",
});

function normalizePatchJobReturnPath(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  if (raw.startsWith("/") && !raw.startsWith("//")) {
    return raw;
  }
  if (typeof window === "undefined") return "";
  try {
    const url = new URL(raw, window.location.origin);
    if (url.origin !== window.location.origin) return "";
    return `${url.pathname}${url.search}${url.hash}`;
  } catch {
    return "";
  }
}

function patchJobSourceFallbackPath(value) {
  const source = String(value || "").trim().toLowerCase();
  if (source === "fleet" || source === "global" || source === "patch_management") {
    return APP_PATHS.patchManagementWindows;
  }
  return "";
}

function resolvePatchJobReturnPath({ current = "", draft = null, batch = null } = {}) {
  const explicit = normalizePatchJobReturnPath(current);
  if (explicit) return explicit;
  const batchReturn = normalizePatchJobReturnPath(batch?.return_to);
  if (batchReturn) return batchReturn;
  const draftReturn = normalizePatchJobReturnPath(draft?.return_to);
  if (draftReturn) return draftReturn;
  return patchJobSourceFallbackPath(batch?.source || draft?.source);
}

const JOB_HISTORY_SUBTAB_URL_BY_KEY = Object.freeze({
  current: "current_run",
  historical: "historical_runs",
  historical_run: "historical_run",
});
const JOB_HISTORY_SUBTAB_KEY_BY_URL = Object.freeze({
  current_run: "current",
  historical_runs: "historical",
  historical_run: "historical_run",
  current: "current",
  historical: "historical",
  historical_run: "historical_run",
});
const JOB_HISTORY_SUBTAB_KEYS = Object.freeze(["current", "historical", "historical_run"]);

const ASSEMBLY_TYPE_FILTER_OPTIONS = [
  { key: "applications", label: "Applications", match: "applications" },
  { key: "playbooks", label: "Playbooks", match: "playbooks" },
  { key: "scripts", label: "Scripts", match: "scripts" },
  { key: "workflows", label: "Workflows", match: "workflows" },
];
const ASSEMBLY_OS_FILTER_OPTIONS = [
  { key: "windows", label: "Windows", match: "windows" },
  { key: "linux", label: "Linux", match: "linux" },
  { key: "macos", label: "MacOS", match: "macos" },
];

function hasHydratedJobPayload(job) {
  if (!job || typeof job !== "object") {
    return false;
  }

  return (
    typeof job.name === "string" ||
    Array.isArray(job.components) ||
    Array.isArray(job.targets) ||
    typeof job.execution_context === "string" ||
    typeof job.schedule_type === "string" ||
    Boolean(job.schedule)
  );
}

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const gridThemeClass = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans","Helvetica Neue",Arial,sans-serif';
const iconFontFamily = '"Quartz Regular"';
const BOREALIS_BLUE = "#58a6ff";
const LEFT_ALIGN_CELL_STYLE = {
  display: "flex",
  alignItems: "center",
  justifyContent: "flex-start",
  textAlign: "left",
  color: "#e2e8f0",
};

const GRID_STYLE_BASE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "--ag-cell-horizontal-padding": "18px",
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

const GRID_WRAPPER_SX = {
  width: "100%",
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "linear-gradient(170deg, rgba(5,8,20,0.92), rgba(8,13,32,0.9))",
  boxShadow: "0 22px 60px rgba(2,6,23,0.75)",
  position: "relative",
  overflow: "hidden",
  "& .ag-root-wrapper": {
    borderRadius: 3,
    minHeight: "100%",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container": {
    fontFamily: gridFontFamily,
    background: "transparent",
  },
  "& .ag-header": {
    backgroundColor: "rgba(3,7,18,0.9)",
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
  "& .ag-cell": {
    color: MAGIC_UI.textBright,
  },
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.32)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(125,183,255,0.08) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-icon": {
    fontFamily: iconFontFamily,
  },
  "& .ag-checkbox-input-wrapper": {
    borderRadius: "3px",
  },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    paddingTop: "8px",
    paddingBottom: "8px",
    paddingLeft: "18px",
    paddingRight: "12px",
    gap: 0,
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    gap: 0,
    paddingTop: 0,
    paddingBottom: 0,
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
    flexGrow: 1,
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
    justifyContent: "flex-start",
    textAlign: "left",
    alignItems: "center",
    gap: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
    flex: 1,
    justifyContent: "flex-start",
    alignItems: "center",
    gap: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
    width: "100%",
    textAlign: "left",
    display: "flex",
    justifyContent: "flex-start",
    alignItems: "center",
  },
  "& .ag-center-cols-container .ag-cell.output-actions-cell, & .ag-pinned-left-cols-container .ag-cell.output-actions-cell, & .ag-pinned-right-cols-container .ag-cell.output-actions-cell": {
    paddingRight: "18px",
    paddingLeft: "12px",
    justifyContent: "flex-start",
    textAlign: "left",
  },
  "& .ag-center-cols-container .ag-cell.output-actions-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.output-actions-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.output-actions-cell .ag-cell-wrapper": {
    width: "100%",
    justifyContent: "flex-start",
    alignItems: "center",
  },
  "& .ag-center-cols-container .ag-cell.output-actions-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.output-actions-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.output-actions-cell .ag-cell-value": {
    width: "100%",
    justifyContent: "flex-start",
    textAlign: "left",
    display: "flex",
    alignItems: "center",
  },
  "& .status-pill-cell": {
    display: "flex",
    alignItems: "center",
  },
  "& .status-pill-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    height: "100%",
    paddingTop: 0,
    paddingBottom: 0,
    lineHeight: "normal",
  },
  "& .status-pill-cell .ag-cell-value": {
    width: "100%",
    display: "flex",
    justifyContent: "center",
    alignItems: "center",
    height: "100%",
  },
};

const GRID_PANEL_SX = {
  ...GRID_WRAPPER_SX,
  ...GRID_STYLE_BASE,
};

export const JOB_STATUS_COLUMN_MIN_WIDTH = 340;
export const STATUS_PILL_LAYOUT_SX = Object.freeze({
  whiteSpace: "nowrap",
  flexShrink: 0,
});

const SINGLE_ROW_SELECTION = {
  mode: "singleRow",
  checkboxes: false,
  headerCheckbox: false,
  enableClickSelection: true,
};

const MULTI_ROW_SELECTION = {
  mode: "multiRow",
  checkboxes: true,
  headerCheckbox: true,
  enableSelectionWithoutKeys: true,
  enableClickSelection: true,
};

const PICKER_SELECTION_COLUMN_DEF = {
  width: 52,
  minWidth: 52,
  maxWidth: 52,
  resizable: false,
  sortable: false,
  suppressHeaderMenuButton: true,
  suppressHeaderContextMenu: true,
  filter: false,
  pinned: "left",
  lockPosition: true,
  suppressMovable: true,
};

const DEVICE_STATUS_THEME = {
  online: {
    label: "Online",
    text: "#00d18c",
    background: "rgba(0,209,140,0.16)",
    border: "1px solid rgba(0,209,140,0.35)",
    dot: "#00d18c",
  },
  offline: {
    label: "Offline",
    text: "#b0b8c8",
    background: "rgba(176,184,200,0.14)",
    border: "1px solid rgba(176,184,200,0.35)",
    dot: "#c3cada",
  },
};

const JOB_RESULT_THEME = {
  success: {
    label: "Success",
    text: "#34d399",
    background: "linear-gradient(120deg, rgba(52,211,153,0.22), rgba(30,64,175,0.12))",
    border: "1px solid rgba(52,211,153,0.45)",
    dot: "#34d399",
  },
  running: {
    label: "Running",
    text: "#7dd3fc",
    background: "linear-gradient(120deg, rgba(125,211,252,0.25), rgba(14,165,233,0.18))",
    border: "1px solid rgba(125,211,252,0.45)",
    dot: "#38bdf8",
  },
  failed: {
    label: "Failed",
    text: "#fb7185",
    background: "rgba(251,113,133,0.18)",
    border: "1px solid rgba(251,113,133,0.45)",
    dot: "#fb7185",
  },
  warning: {
    label: "Warning",
    text: "#fbbf24",
    background: "linear-gradient(120deg, rgba(251,191,36,0.22), rgba(249,115,22,0.14))",
    border: "1px solid rgba(251,191,36,0.38)",
    dot: "#f59e0b",
  },
  pending: {
    label: "Pending",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.18)",
    border: "1px solid rgba(251,191,36,0.35)",
    dot: "#f59e0b",
  },
  timed_out: {
    label: "Timed Out",
    text: "#d8b4fe",
    background: "rgba(192,132,252,0.18)",
    border: "1px solid rgba(192,132,252,0.4)",
    dot: "#c084fc",
  },
  expired: {
    label: "Expired",
    text: "#e5e7eb",
    background: "rgba(226,232,240,0.14)",
    border: "1px solid rgba(226,232,240,0.32)",
    dot: "#cbd5f5",
  },
  skipped: {
    label: "Skipped",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.14)",
    border: "1px solid rgba(251,191,36,0.32)",
    dot: "#f59e0b",
  },
  establishing_connection: {
    label: "Establishing Connection",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.14)",
    border: "1px solid rgba(251,191,36,0.32)",
    dot: "#f59e0b",
  },
  no_devices_targeted: {
    label: "No Devices Targeted",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.14)",
    border: "1px solid rgba(251,191,36,0.32)",
    dot: "#f59e0b",
  },
  no_eligible_targets: {
    label: "No Eligible Targets",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.14)",
    border: "1px solid rgba(251,191,36,0.32)",
    dot: "#f59e0b",
  },
  default: {
    label: "Status",
    text: "#e2e8f0",
    background: "rgba(226,232,240,0.12)",
    border: "1px solid rgba(226,232,240,0.2)",
    dot: "#94a3b8",
  },
};

const JOB_STATUS_SORT_RANK = Object.freeze({
  success: 0,
  warning: 1,
  failed: 2,
  running: 3,
  establishing_connection: 3,
  expired: 4,
  timed_out: 4,
  skipped: 4,
  no_devices_targeted: 4,
  no_eligible_targets: 4,
  pending: 5,
  default: 6,
});

const normalizeJobStatusKey = (status) => {
  const normalized = String(status || "").trim().toLowerCase();
  if (!normalized || normalized === "scheduled" || normalized === "queued") return "pending";
  if (normalized === "failure") return "failed";
  if (normalized === "timed out" || normalized === "timed_out") return "timed_out";
  if (normalized === "establishing connection" || normalized === "establishing_connection") return "establishing_connection";
  if (normalized === "no devices targeted" || normalized === "no_devices_targeted") return "no_devices_targeted";
  if (normalized === "no eligible targets" || normalized === "no_eligible_targets") return "no_eligible_targets";
  return normalized;
};

export const JOB_HISTORY_STATUS_FILTER_GROUPS = Object.freeze([
  Object.freeze({
    key: "not_started",
    label: "Not Started",
    options: Object.freeze([
      Object.freeze({ key: "pending", label: "Pending" }),
      Object.freeze({ key: "expired", label: "Expired" }),
      Object.freeze({ key: "skipped", label: "Skipped" }),
    ]),
  }),
  Object.freeze({
    key: "started",
    label: "Started",
    options: Object.freeze([
      Object.freeze({ key: "running", label: "Running" }),
      Object.freeze({ key: "failed", label: "Failed" }),
      Object.freeze({ key: "warning", label: "Warning" }),
      Object.freeze({ key: "success", label: "Success" }),
    ]),
  }),
]);

export const jobHistoryStatusFilterKey = (status) => {
  const normalized = normalizeJobStatusKey(status);
  if (normalized === "running" || normalized === "establishing_connection") return "running";
  if (normalized === "failed" || normalized === "timed_out") return "failed";
  if (["skipped", "no_devices_targeted", "no_eligible_targets"].includes(normalized)) return "skipped";
  if (["expired", "warning", "success"].includes(normalized)) return normalized;
  return "pending";
};

export const buildJobHistoryStatusCounts = (rows = []) => {
  const totals = JOB_HISTORY_STATUS_FILTER_GROUPS
    .flatMap((group) => group.options)
    .reduce((acc, option) => ({ ...acc, [option.key]: 0 }), {});
  (Array.isArray(rows) ? rows : []).forEach((row) => {
    const key = jobHistoryStatusFilterKey(row?.job_status || row?.status || "");
    totals[key] += 1;
  });
  return totals;
};

export const filterJobHistoryRowsByStatus = (rows = [], activeKey = "") => {
  const entries = Array.isArray(rows) ? rows : [];
  if (!activeKey) return entries;
  return entries.filter((row) => jobHistoryStatusFilterKey(row?.job_status || row?.status || "") === activeKey);
};

const getJobStatusSortRank = (status) => {
  const key = normalizeJobStatusKey(status);
  return JOB_STATUS_SORT_RANK[key] ?? JOB_STATUS_SORT_RANK.default;
};

const summarizeHistoricalRunStatus = (statuses = []) => {
  const normalized = statuses.map((status) => normalizeJobStatusKey(status)).filter(Boolean);
  if (!normalized.length) return "";
  const count = (key) => normalized.filter((status) => status === key).length;
  if (count("establishing_connection")) return "establishing_connection";
  if (count("running")) return "running";
  if (count("failed")) return "failed";
  if (count("timed_out")) return "timed_out";
  if (count("warning")) return "warning";
  if (count("expired")) return "expired";
  if (count("pending")) return "pending";
  if (count("success")) return "success";
  const allNoEligibleTargets = normalized.every((status) => status === "no_eligible_targets");
  const allNoDevicesTargeted = normalized.every((status) => status === "no_devices_targeted");
  if (allNoEligibleTargets) return "No Eligible Targets";
  if (allNoDevicesTargeted) return "No Devices Targeted";
  if (normalized.every((status) => ["skipped", "no_devices_targeted", "no_eligible_targets"].includes(status))) {
    return "skipped";
  }
  return normalized[0] || "";
};

export const connectionProbeStatusLabel = (status, deadlineTs, nowMs) => {
  if (normalizeJobStatusKey(status) !== "establishing_connection") return "";
  const deadline = Number(deadlineTs || 0);
  if (!Number.isFinite(deadline) || deadline <= 0) return JOB_RESULT_THEME.establishing_connection.label;
  const remainingSeconds = Math.max(0, Math.ceil(deadline - Number(nowMs || Date.now()) / 1000));
  return `Establishing Connection - ${remainingSeconds}s Until Timeout`;
};

export const statusPillTextTransform = (preserveCase = false) => (preserveCase ? "none" : "uppercase");

const isWorkflowComponentRecord = (component) => {
  const typeRaw = String(
    component?.kind ||
    component?.type ||
    component?.component_type ||
    component?.assembly_type ||
    component?.assemblyType ||
    ""
  ).trim().toLowerCase();
  const subtypeRaw = String(
    component?.assembly_subtype ||
    component?.assemblySubtype ||
    component?.script_type ||
    ""
  ).trim().toLowerCase();
  return typeRaw === "workflow" || subtypeRaw === "workflow";
};

const isAnsibleComponentRecord = (component) => {
  const typeRaw = String(
    component?.kind ||
    component?.type ||
    component?.component_type ||
    component?.assembly_type ||
    component?.assemblyType ||
    ""
  ).trim().toLowerCase();
  const subtypeRaw = String(
    component?.assembly_subtype ||
    component?.assemblySubtype ||
    component?.script_type ||
    ""
  ).trim().toLowerCase();
  return typeRaw === "ansible" || typeRaw === "playbook" || subtypeRaw === "ansible" || subtypeRaw === "playbook";
};

const isPatchComponentRecord = (component) => {
  const typeRaw = String(
    component?.kind ||
    component?.type ||
    component?.component_type ||
    component?.assembly_type ||
    component?.assemblyType ||
    ""
  ).trim().toLowerCase();
  return typeRaw === "patch_install" || typeRaw === "patch_management";
};

const componentExecutionDomain = (component) => {
  if (isPatchComponentRecord(component)) {
    return "patch";
  }
  if (isWorkflowComponentRecord(component)) {
    return "workflow";
  }
  if (isAnsibleComponentRecord(component)) {
    return "ansible";
  }
  if (component && typeof component === "object") {
    return "script";
  }
  return "";
};

const StatusPill = ({ label, theme, preserveCase = false }) => {
  if (!label) return null;
  const pillTheme = theme || JOB_RESULT_THEME.default;
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.6,
        px: 1.2,
        py: 0.25,
        borderRadius: 999,
        background: pillTheme.background,
        border: pillTheme.border,
        color: pillTheme.text,
        fontWeight: 600,
        fontSize: "12px",
        letterSpacing: 0.35,
        textTransform: statusPillTextTransform(preserveCase),
        lineHeight: 1,
        fontFamily: gridFontFamily,
        ...STATUS_PILL_LAYOUT_SX,
      }}
    >
      {pillTheme.dot ? (
        <Box
          component="span"
          sx={{
            width: 8,
            height: 8,
            borderRadius: "50%",
            backgroundColor: pillTheme.dot,
            boxShadow: "0 0 0 2px rgba(8,12,24,0.65)",
          }}
        />
      ) : null}
      {label}
    </Box>
  );
};

const patchProgressLabel = (progress, status) => {
  if (!progress || typeof progress !== "object") return "";
  if (normalizeJobStatusKey(status) !== "running") return "";
  const direct = String(progress.display_label || "").trim();
  if (direct) return direct;
  const percentValue = Number(progress.percent);
  const percent = Number.isFinite(percentValue) ? Math.max(0, Math.min(100, Math.round(percentValue))) : null;
  const phase = String(progress.phase || "").trim().toLowerCase();
  if (phase === "download") return percent == null ? "Downloading" : `Downloading ${percent}%`;
  if (phase === "install") return percent == null ? "Installing" : `Installing ${percent}%`;
  if (phase === "prepare") return "Preparing";
  if (phase === "finalize") return "Finalizing";
  return "";
};

const patchProgressTooltip = (progress) => {
  if (!progress || typeof progress !== "object") return "";
  const lines = [];
  const label = String(progress.display_label || "").trim();
  const kb = String(progress.kb || "").trim();
  const title = String(progress.title || "").trim();
  const message = String(progress.message || "").trim();
  if (label) lines.push(label);
  if (kb) lines.push(kb);
  if (title) lines.push(title);
  if (message) lines.push(message);
  const capturedAt = Number(progress.captured_at || 0);
  if (capturedAt > 0) lines.push(`Updated ${new Date(capturedAt * 1000).toLocaleString()}`);
  return lines.join("\n");
};

const GLASS_PANEL_BASE_SX = {
  background: MAGIC_UI.panelBg,
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: MAGIC_UI.glow,
  p: { xs: 2, md: 3 },
};

const TAB_SECTION_SX = {
  width: "100%",
  display: "flex",
  flexDirection: "column",
  gap: 1.5,
  px: { xs: 1.5, md: 2 },
  py: { xs: 1.25, md: 1.75 },
};

const NAV_TAB_HEIGHT = 32;
const NAV_TAB_HEIGHT_COMPACT = 28;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const buildNavTabsSx = (minHeight = NAV_TAB_HEIGHT) => ({
  borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
  minHeight,
  height: minHeight,
  "& .MuiTabs-flexContainer": {
    minHeight,
    height: minHeight,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontFamily: "inherit",
    fontSize: "0.8rem",
    minHeight,
    height: minHeight,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.icon,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.hover,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.iconActive,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.activeBg,
    },
  },
});

const ACTION_BUTTON_BASE_SX = {
  minHeight: 38,
  height: 38,
  px: 1.8,
  borderRadius: 999,
  fontFamily: gridFontFamily,
  fontWeight: 600,
  fontSize: "0.92rem",
  lineHeight: 1,
  textTransform: "none",
  whiteSpace: "nowrap",
  boxSizing: "border-box",
  borderWidth: "1px",
  borderStyle: "solid",
  transition:
    "background 160ms ease, border-color 160ms ease, color 160ms ease, box-shadow 160ms ease, transform 120ms ease, opacity 120ms ease",
  "& .MuiButton-startIcon": {
    mr: 0.8,
  },
  "&:hover": {
    transform: "translateY(-0.5px)",
  },
  "&:active": {
    transform: "translateY(0)",
  },
  "&.Mui-disabled": {
    opacity: 0.42,
    color: "rgba(226, 232, 240, 0.7)",
    borderColor: "rgba(148, 163, 184, 0.22)",
    background: "rgba(51, 65, 85, 0.5)",
  },
};

const PRIMARY_CTA_SX = {
  ...ACTION_BUTTON_BASE_SX,
  borderRadius: 999,
  color: "#06101d",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 16px 34px rgba(125, 211, 252, 0.18)",
  "&:hover": {
    ...ACTION_BUTTON_BASE_SX["&:hover"],
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
    boxShadow: "0 18px 36px rgba(192, 132, 252, 0.22)",
  },
  "&.Mui-disabled": {
    ...ACTION_BUTTON_BASE_SX["&.Mui-disabled"],
    color: "rgba(9, 18, 33, 0.6)",
    background: "linear-gradient(135deg, rgba(125, 211, 252, 0.38) 0%, rgba(192, 132, 252, 0.34) 100%)",
    borderColor: "transparent",
  },
};

const PRIMARY_CTA_FLAT_SX = {
  ...PRIMARY_CTA_SX,
  boxShadow: "none",
  "&:hover": {
    ...PRIMARY_CTA_SX["&:hover"],
    boxShadow: "none",
  },
};

const OUTLINE_BUTTON_SX = {
  ...ACTION_BUTTON_BASE_SX,
  borderColor: "rgba(148,163,184,0.45)",
  color: MAGIC_UI.textBright,
  background: "rgba(5, 10, 24, 0.84)",
  boxShadow: "0 10px 24px rgba(2, 6, 23, 0.3)",
  "&:hover": {
    ...ACTION_BUTTON_BASE_SX["&:hover"],
    borderColor: "rgba(125, 211, 252, 0.52)",
    background: "rgba(8, 14, 30, 0.92)",
    boxShadow: "0 14px 32px rgba(2, 6, 23, 0.42)",
  },
};

const INPUT_FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    bgcolor: "rgba(5,9,18,0.85)",
    color: MAGIC_UI.textBright,
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.35)",
    },
    "&:hover fieldset": {
      borderColor: MAGIC_UI.accentA,
    },
    "&.Mui-focused fieldset": {
      borderColor: MAGIC_UI.accentB,
      boxShadow: "0 0 0 1px rgba(192,132,252,0.3)",
    },
  },
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.textMuted,
  },
  "& .MuiFormHelperText-root": {
    color: "#fda4af",
  },
};

const SELECT_FIELD_SX = {
  ...INPUT_FIELD_SX,
  "& .MuiOutlinedInput-root": {
    ...INPUT_FIELD_SX["& .MuiOutlinedInput-root"],
    minHeight: 42,
    bgcolor: "rgba(8,13,30,0.94)",
    background: "linear-gradient(160deg, rgba(8,13,30,0.96), rgba(11,18,38,0.94))",
    boxShadow: "inset 0 1px 0 rgba(255,255,255,0.03)",
  },
  "& .MuiSelect-select": {
    display: "flex",
    alignItems: "center",
    minHeight: "20px !important",
    paddingTop: "7px !important",
    paddingBottom: "7px !important",
  },
  "& .MuiSvgIcon-root": {
    color: MAGIC_UI.accentA,
  },
};

const SELECT_MENU_PROPS = {
  PaperProps: {
    sx: {
      mt: 1,
      borderRadius: 2.5,
      border: `1px solid ${MAGIC_UI.panelBorder}`,
      background:
        "linear-gradient(180deg, rgba(8, 13, 30, 0.98) 0%, rgba(7, 11, 24, 0.98) 55%, rgba(10, 16, 34, 0.98) 100%)",
      boxShadow: "0 24px 60px rgba(2, 8, 23, 0.7)",
      backdropFilter: "blur(18px)",
      color: MAGIC_UI.textBright,
      overflow: "hidden",
      "& .MuiMenu-list": {
        p: 0.5,
      },
      "& .MuiMenuItem-root": {
        minHeight: 27,
        px: 1.3,
        py: 0.35,
        borderRadius: 1.25,
        fontSize: "0.88rem",
        color: MAGIC_UI.textBright,
        transition: "background 140ms ease, color 140ms ease",
      },
      "& .MuiMenuItem-root:hover": {
        background: "rgba(125, 183, 255, 0.12)",
      },
      "& .MuiMenuItem-root.Mui-selected": {
        background: "rgba(125, 183, 255, 0.2)",
        color: "#f8fbff",
      },
      "& .MuiMenuItem-root.Mui-selected:hover": {
        background: "rgba(125, 183, 255, 0.28)",
      },
    },
  },
};

const GlassPanel = ({ children, sx }) => (
  <Box sx={{ ...GLASS_PANEL_BASE_SX, ...(sx || {}) }}>{children}</Box>
);

function CountSliderGroup({ options, activeKey, counts, onChange }) {
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.75,
        background: "linear-gradient(120deg, rgba(8,12,24,0.92), rgba(4,7,17,0.85))",
        borderRadius: 999,
        border: "1px solid rgba(148,163,184,0.35)",
        boxShadow: "0 18px 48px rgba(2,8,23,0.45)",
        padding: "4px",
      }}
    >
      {options.map((option) => {
        const active = activeKey === option.key;
        return (
          <Box
            key={option.key}
            component="button"
            type="button"
            aria-pressed={active}
            onClick={() => onChange(active ? "" : option.key)}
            sx={{
              border: "none",
              outline: "none",
              background: active ? "linear-gradient(135deg,#7dd3fc,#c084fc)" : "transparent",
              color: active ? "#041224" : "#cbd5e1",
              fontWeight: 600,
              fontSize: 13,
              px: 2,
              py: 0.5,
              borderRadius: 999,
              cursor: "pointer",
              display: "inline-flex",
              alignItems: "center",
              gap: 0.6,
              boxShadow: active ? "0 0 18px rgba(125,211,252,0.35)" : "none",
              transition: "all 0.2s ease",
            }}
          >
            <Box component="span" sx={{ userSelect: "none" }}>
              {option.label}
            </Box>
            <Box
              component="span"
              sx={{
                minWidth: 28,
                textAlign: "center",
                borderRadius: 999,
                fontSize: 12,
                fontWeight: 600,
                px: 0.75,
                py: 0.1,
                color: active ? "#041224" : "#94a3b8",
                backgroundColor: active ? "rgba(4,18,36,0.2)" : "rgba(15,23,42,0.65)",
                border: active ? "1px solid rgba(4,18,36,0.3)" : "1px solid rgba(148,163,184,0.3)",
              }}
            >
              {counts?.[option.key] ?? 0}
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

const EXEC_CONTEXT_OPTIONS = Object.freeze([
  {
    value: "system",
    title: "Execute Assembly via AGENT (SYSTEM / root)",
    detail: "Runs script assemblies on the target device as SYSTEM or root.",
    domain: "script",
  },
  {
    value: "current_user",
    title: "Execute Assembly via AGENT (CurrentUser)",
    detail: "Runs script assemblies in the logged-in user session.",
    domain: "script",
  },
  {
    value: "ssh_individual",
    title: "Ansible Playbook via SSH (Run Individually)",
    detail: "Runs the selected playbook assemblies from the Engine one target at a time with bounded concurrency.",
    domain: "ansible",
  },
  {
    value: "ssh",
    title: "Ansible Playbook via SSH (Run Together)",
    detail: "Runs the selected playbook assemblies from the Engine in one shared SSH batch.",
    domain: "ansible",
  },
  {
    value: "winrm_individual",
    title: "Ansible Playbook via WinRM (Run Individually)",
    detail: "Runs the selected playbook assemblies from the Engine one target at a time with bounded concurrency.",
    domain: "ansible",
  },
  {
    value: "winrm",
    title: "Ansible Playbook via WinRM (Run Together)",
    detail: "Runs the selected playbook assemblies from the Engine in one shared WinRM batch.",
    domain: "ansible",
  },
]);

const LEGACY_EXEC_CONTEXT_COPY = Object.freeze({
  local: {
    title: "Run on Engine via Ansible localhost (Legacy)",
    detail: "Legacy/internal Engine-side Ansible mode kept only so older jobs can still be reviewed and migrated.",
    domain: "ansible",
  },
});

const EXEC_CONTEXT_COPY = Object.freeze(
  {
    ...EXEC_CONTEXT_OPTIONS.reduce((acc, item) => {
      acc[item.value] = { title: item.title, detail: item.detail, domain: item.domain };
      return acc;
    }, {}),
    ...LEGACY_EXEC_CONTEXT_COPY,
  }
);

const SCRIPT_EXEC_CONTEXTS = Object.freeze(["system", "current_user"]);
const ANSIBLE_EXEC_CONTEXTS = Object.freeze(["ssh", "ssh_individual", "winrm", "winrm_individual"]);

const normalizeRemoteTransport = (value) => {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "winrm" || normalized === "winrm_individual") return "winrm";
  if (normalized === "ssh" || normalized === "ssh_individual") return "ssh";
  return "";
};

const isWinRMExecContext = (value) => normalizeRemoteTransport(value) === "winrm";

const normalizeFilterCatalog = (raw) => {
  if (!Array.isArray(raw)) return [];
  return raw
    .map((item, idx) => {
      const idValue = item?.id ?? item?.filter_id ?? idx;
      const id = Number(idValue);
      if (!Number.isFinite(id)) return null;
      const siteMode = String(item?.site_mode || "global").trim().toLowerCase() || "global";
      const siteNames = Array.isArray(item?.site_names)
        ? item.site_names.filter(Boolean)
        : Array.isArray(item?.sites)
          ? item.sites.map((site) => site?.name || site).filter(Boolean)
          : [];
      const scopeLabel =
        siteMode === "specific_sites"
          ? "Specific Sites"
          : siteMode === "global_exclusions"
            ? "Global w/ Exclusions"
            : "Global";
      const deviceCount =
        typeof item?.matching_device_count === "number" && Number.isFinite(item.matching_device_count)
          ? item.matching_device_count
          : null;
      return {
        id,
        name: item?.name || `Filter ${idx + 1}`,
        description: item?.description || "",
        site_mode: siteMode,
        scope: scopeLabel,
        siteSummary: siteNames.join(", "),
        deviceCount,
      };
    })
    .filter(Boolean);
};

function SectionHeader({ title, action, sx }) {
  return (
    <Box
      sx={{
        mb: 1.5,
        display: "flex",
        alignItems: action ? "flex-start" : "center",
        justifyContent: "space-between",
        gap: 2,
        ...sx,
      }}
    >
      <Typography
        variant="subtitle2"
        sx={{
          color: MAGIC_UI.textBright,
          fontWeight: 400,
          letterSpacing: 0.4,
          textTransform: "uppercase",
          fontSize: 13,
        }}
      >
        {title}
      </Typography>
      {action || null}
    </Box>
  );
}

const formatPathSegment = (segment) => {
  const value = String(segment || "").trim();
  if (!value) return "";
  return value.charAt(0).toUpperCase() + value.slice(1);
};

const formatPathDisplay = (folderPath) => {
  const segments = String(folderPath || "")
    .split("/")
    .map((segment) => formatPathSegment(segment))
    .filter(Boolean);
  return segments.join(" \u203A ");
};

const buildPathSearchValue = (folderPath) => {
  const rawPath = String(folderPath || "").replace(/\\/g, "/");
  const formattedPath = formatPathDisplay(rawPath);
  return [rawPath, formattedPath]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
};

const buildFilterFlags = (pathSearchValue, options) =>
  options.reduce((acc, option) => {
    acc[option.key] = pathSearchValue.includes(String(option.match || "").toLowerCase());
    return acc;
  }, {});

const resolveAssemblyPickerIcon = (typeKey) => {
  if (typeKey === "workflow") return AccountTreeRoundedIcon;
  if (typeKey === "ansible") return MenuBookRoundedIcon;
  return TerminalRoundedIcon;
};

const AssemblyPickerSourceCellRenderer = React.memo(function AssemblyPickerSourceCellRenderer(props) {
  const { data } = props;
  if (!data) return null;
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1.25 }}>
      <DomainBadge domain={data.domain} size="small" />
      {data.isDirty ? <DirtyStatePill compact /> : null}
    </Box>
  );
});

const AssemblyPickerNameCellRenderer = React.memo(function AssemblyPickerNameCellRenderer(props) {
  const { data } = props;
  if (!data) return null;
  return (
    <Typography
      component="span"
      sx={{
        color: "#58a6ff",
        fontSize: 14,
        fontWeight: 600,
        whiteSpace: "nowrap",
        overflow: "hidden",
        textOverflow: "ellipsis",
      }}
    >
      {data.name || ""}
    </Typography>
  );
});

const AssemblyPickerPathCellRenderer = React.memo(function AssemblyPickerPathCellRenderer(props) {
  const { data } = props;
  if (!data) return null;
  const Icon = resolveAssemblyPickerIcon(data.typeKey);
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1.1, minWidth: 0 }}>
      <Icon sx={{ fontSize: 19, color: "#58a6ff", flexShrink: 0 }} />
      <Typography
        component="span"
        sx={{
          fontSize: 13,
          color: MAGIC_UI.textMuted,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {data.pathDisplay || ""}
      </Typography>
    </Box>
  );
});

const AssemblyPickerUpdateCellRenderer = React.memo(function AssemblyPickerUpdateCellRenderer(props) {
  const available = Boolean(props?.data?.officialUpdateAvailable);
  if (!available) return null;
  return (
    <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%" }}>
      <StatusPill
        label="Available"
        theme={{
          text: "#7dd3fc",
          background: "rgba(125, 211, 252, 0.18)",
          border: "1px solid rgba(125, 211, 252, 0.42)",
          dot: "#7dd3fc",
        }}
      />
    </Box>
  );
});

function normalizeVariableDefinitions(vars = []) {
  return (Array.isArray(vars) ? vars : [])
    .map((raw) => {
      if (!raw || typeof raw !== "object") return null;
      const name = typeof raw.name === "string" ? raw.name.trim() : typeof raw.key === "string" ? raw.key.trim() : "";
      if (!name) return null;
      const label = typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name;
      const type = typeof raw.type === "string" ? raw.type.toLowerCase() : "string";
      const required = Boolean(raw.required);
      const description = typeof raw.description === "string" ? raw.description : "";
      let defaultValue = "";
      if (Object.prototype.hasOwnProperty.call(raw, "default")) defaultValue = raw.default;
      else if (Object.prototype.hasOwnProperty.call(raw, "defaultValue")) defaultValue = raw.defaultValue;
      else if (Object.prototype.hasOwnProperty.call(raw, "default_value")) defaultValue = raw.default_value;
      return { name, label, type, required, description, default: defaultValue };
    })
    .filter(Boolean);
}

function coerceVariableValue(type, value) {
  if (type === "boolean") {
    if (typeof value === "boolean") return value;
    if (typeof value === "number") return value !== 0;
    if (value == null) return false;
    const str = String(value).trim().toLowerCase();
    if (!str) return false;
    return ["true", "1", "yes", "on"].includes(str);
  }
  if (type === "number") {
    if (value == null || value === "") return "";
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
    const parsed = Number(value);
    return Number.isFinite(parsed) ? String(parsed) : "";
  }
  return value == null ? "" : String(value);
}

function mergeComponentVariables(docVars = [], storedVars = [], storedValueMap = {}) {
  const definitions = normalizeVariableDefinitions(docVars);
  const overrides = {};
  const storedMeta = {};
  (Array.isArray(storedVars) ? storedVars : []).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name) return;
    if (Object.prototype.hasOwnProperty.call(raw, "value")) overrides[name] = raw.value;
    else if (Object.prototype.hasOwnProperty.call(raw, "default")) overrides[name] = raw.default;
    storedMeta[name] = {
      label: typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name,
      type: typeof raw.type === "string" ? raw.type.toLowerCase() : undefined,
      required: Boolean(raw.required),
      description: typeof raw.description === "string" ? raw.description : "",
      default: Object.prototype.hasOwnProperty.call(raw, "default") ? raw.default : ""
    };
  });
  if (storedValueMap && typeof storedValueMap === "object") {
    Object.entries(storedValueMap).forEach(([key, val]) => {
      const name = typeof key === "string" ? key.trim() : "";
      if (name) overrides[name] = val;
    });
  }

  const used = new Set();
  const merged = definitions.map((def) => {
    const override = Object.prototype.hasOwnProperty.call(overrides, def.name) ? overrides[def.name] : undefined;
    used.add(def.name);
    return {
      ...def,
      value: override !== undefined ? coerceVariableValue(def.type, override) : coerceVariableValue(def.type, def.default)
    };
  });

  (Array.isArray(storedVars) ? storedVars : []).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name || used.has(name)) return;
    const meta = storedMeta[name] || {};
    const type = meta.type || (typeof overrides[name] === "boolean" ? "boolean" : typeof overrides[name] === "number" ? "number" : "string");
    const defaultValue = Object.prototype.hasOwnProperty.call(meta, "default") ? meta.default : "";
    const override = Object.prototype.hasOwnProperty.call(overrides, name)
      ? overrides[name]
      : Object.prototype.hasOwnProperty.call(raw, "value")
        ? raw.value
        : defaultValue;
    merged.push({
      name,
      label: meta.label || name,
      type,
      required: Boolean(meta.required),
      description: meta.description || "",
      default: defaultValue,
      value: coerceVariableValue(type, override)
    });
    used.add(name);
  });

  Object.entries(overrides).forEach(([nameRaw, val]) => {
    const name = typeof nameRaw === "string" ? nameRaw.trim() : "";
    if (!name || used.has(name)) return;
    const type = typeof val === "boolean" ? "boolean" : typeof val === "number" ? "number" : "string";
    merged.push({
      name,
      label: name,
      type,
      required: false,
      description: "",
      default: "",
      value: coerceVariableValue(type, val)
    });
    used.add(name);
  });

  return merged;
}

function ComponentCard({ comp, onRemove, onVariableChange, errors = {} }) {
  const variables = Array.isArray(comp.variables)
    ? comp.variables.filter((v) => v && typeof v.name === "string" && v.name)
    : [];
  const description = comp.description || comp.path || "";
  return (
    <GlassPanel sx={{ mb: 2, p: { xs: 2, md: 2.5 } }}>
      <Box
        sx={{
          display: "flex",
          gap: 2.5,
          flexWrap: { xs: "wrap", md: "nowrap" },
          alignItems: "stretch",
        }}
      >
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Box sx={{ display: "flex", alignItems: "center", gap: 1.2 }}>
            <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
              {comp.name}
            </Typography>
            {comp.domain ? <DomainBadge domain={comp.domain} size="small" /> : null}
          </Box>
          <Typography variant="body2" sx={{ color: "#7c879d", mt: 0.5 }}>
            {description}
          </Typography>
        </Box>
        <Divider orientation="vertical" flexItem sx={{ borderColor: "rgba(148,163,184,0.25)" }} />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography variant="subtitle2" sx={{ color: MAGIC_UI.textBright, mb: 1 }}>
            Variables
          </Typography>
          {variables.length ? (
            <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
              {variables.map((variable) => (
                <Box key={variable.name}>
                  {variable.type === "boolean" ? (
                    <>
                      <FormControlLabel
                        sx={{
                          color: MAGIC_UI.textBright,
                          "& .MuiTypography-root": { color: MAGIC_UI.textBright, fontWeight: 500 },
                        }}
                        control={
                          <Checkbox
                            size="small"
                            checked={Boolean(variable.value)}
                            onChange={(e) => onVariableChange(comp.localId, variable.name, e.target.checked)}
                            sx={{
                              color: MAGIC_UI.accentA,
                              "&.Mui-checked": { color: MAGIC_UI.accentB },
                            }}
                          />
                        }
                        label={
                          <>
                            {variable.label}
                            {variable.required ? " *" : ""}
                          </>
                        }
                      />
                      {variable.description ? (
                        <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, display: "block", ml: 4 }}>
                          {variable.description}
                        </Typography>
                      ) : null}
                    </>
                  ) : (
                    <TextField
                      fullWidth
                      size="small"
                      label={`${variable.label}${variable.required ? " *" : ""}`}
                      type={variable.type === "number" ? "number" : variable.type === "credential" ? "password" : "text"}
                      value={variable.value ?? ""}
                      onChange={(e) => onVariableChange(comp.localId, variable.name, e.target.value)}
                      InputLabelProps={{ shrink: true }}
                      sx={{ ...INPUT_FIELD_SX }}
                      error={Boolean(errors[variable.name])}
                      helperText={errors[variable.name] || variable.description || ""}
                    />
                  )}
                </Box>
              ))}
            </Box>
          ) : (
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
              No variables defined for this assembly.
            </Typography>
          )}
        </Box>
        <Box sx={{ display: "flex", alignItems: "flex-start" }}>
          <IconButton
            onClick={() => onRemove(comp.localId)}
            size="small"
            sx={{
              color: "#f87171",
              border: "1px solid rgba(248,113,113,0.4)",
              borderRadius: 1.5,
              "&:hover": { borderColor: "#fb7185", color: "#fb7185" },
            }}
          >
            <DeleteIcon fontSize="small" />
          </IconButton>
        </Box>
      </Box>
    </GlassPanel>
  );
}

export default function CreateJob() {
  const location = useLocation();
  const navigate = useNavigate();
  const { jobId } = useParams();
  const { aegisStatus } = useAuth();
  const initialJob = useMemo(() => {
    if (location.state?.initialJob && Number(location.state.initialJob?.id) > 0) {
      return location.state.initialJob;
    }
    const parsedId = Number(jobId);
    return Number.isInteger(parsedId) && parsedId > 0 ? { id: parsedId } : null;
  }, [jobId, location.state]);
  const [hydratedInitialJob, setHydratedInitialJob] = useState(() =>
    hasHydratedJobPayload(initialJob) ? initialJob : null
  );
  const [quickJobDraft, setQuickJobDraft] = useState(() => location.state?.quickJobDraft || null);
  const [patchJobDraft, setPatchJobDraft] = useState(() => location.state?.patchJobDraft || null);
  const [patchJobReturnTo, setPatchJobReturnTo] = useState(() =>
    resolvePatchJobReturnPath({ draft: location.state?.patchJobDraft })
  );
  const [patchJobBatch, setPatchJobBatch] = useState(null);
  const patchJobReturnPath = useMemo(
    () => resolvePatchJobReturnPath({ current: patchJobReturnTo, draft: patchJobDraft, batch: patchJobBatch }),
    [patchJobBatch, patchJobDraft, patchJobReturnTo]
  );
  const [jobKind, setJobKind] = useState("automation");
  const [jobName, setJobName] = useState("");
  const [pageTitleJobName, setPageTitleJobName] = useState("");
  // Components the job will run: {type:'script'|'workflow', path, name, description}
  const [components, setComponents] = useState([]);
  const workflowComponentCount = useMemo(
    () => components.filter((component) => isWorkflowComponentRecord(component)).length,
    [components]
  );
  const isWorkflowJob = workflowComponentCount > 0;
  const hasPendingPatchDraft = Boolean(!(initialJob && initialJob.id) && patchJobDraft?.id);
  const isPatchJob = jobKind === "patch_install" || hasPendingPatchDraft;
  const isPatchBatchJob = isPatchJob && Array.isArray(patchJobBatch?.items) && patchJobBatch.items.length > 1;
  const [targets, setTargets] = useState([]); // array of target descriptors
  const [filterCatalog, setFilterCatalog] = useState([]);
  const [, setLoadingFilterCatalog] = useState(false);
  const filterCatalogMapRef = useRef({});
  const loadFilterCatalog = useCallback(async () => {
    setLoadingFilterCatalog(true);
    try {
      const resp = await fetch("/api/device_filters");
      if (resp.ok) {
        const data = await resp.json();
        setFilterCatalog(normalizeFilterCatalog(data?.filters || data || []));
      } else {
        setFilterCatalog([]);
      }
    } catch {
      setFilterCatalog([]);
    } finally {
      setLoadingFilterCatalog(false);
    }
  }, []);
  useEffect(() => {
    loadFilterCatalog();
  }, [loadFilterCatalog]);
  useEffect(() => {
    const nextMap = {};
    filterCatalog.forEach((entry) => {
      nextMap[entry.id] = entry;
      nextMap[String(entry.id)] = entry;
    });
    filterCatalogMapRef.current = nextMap;
  }, [filterCatalog]);
  const [scheduleType, setScheduleType] = useState("immediately");
  const [startDateTime, setStartDateTime] = useState(() => dayjs().add(5, "minute").second(0));
  const [expiration, setExpiration] = useState("1h");
  const [execContext, setExecContext] = useState("system");
  const [credentials, setCredentials] = useState([]);
  const [credentialLoading, setCredentialLoading] = useState(false);
  const [credentialError, setCredentialError] = useState("");
  const [selectedCredentialId, setSelectedCredentialId] = useState("");
  const aegisLocked = Boolean(aegisStatus?.configured && aegisStatus?.locked);

  const resolvedPageTitle = useMemo(
    () => (pageTitleJobName ? `Scheduled Job: ${pageTitleJobName}` : PAGE_TITLE),
    [pageTitleJobName]
  );
  const resolvedPageSubtitle = useMemo(() => {
    if (isPatchBatchJob) {
      return "Schedule separate one-KB Windows patch jobs with shared timing.";
    }
    if (isPatchJob) {
      return "Schedule Windows patch deployment through Borealis job lanes.";
    }
    if (isWorkflowJob) {
      return "Workflow-backed jobs execute one saved workflow. Targets and execution context are defined inside the workflow itself.";
    }
    if (scheduleType === "immediately") {
      return "Launch immediately or save as a quick job with your selected assemblies.";
    }
    return PAGE_SUBTITLE;
  }, [isPatchBatchJob, isPatchJob, isWorkflowJob, scheduleType]);
  const sendNotification = useAppNotifications({
    title: resolvedPageTitle,
    icon: "pendingactions",
    variant: "info",
  });
  const [useSvcAccount, setUseSvcAccount] = useState(true);
  const [assembliesPayload, setAssembliesPayload] = useState({ items: [], queue: [] });
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const assemblyExportCacheRef = useRef(new Map());
  const quickDraftAppliedRef = useRef(null);
  const startDateTimeTouchedRef = useRef(false);
  const hydratedFormKeyRef = useRef("");
  const [engineScheduleClock, setEngineScheduleClock] = useState({
    timezone: "",
    epoch: null,
    loaded: false,
  });
  const engineTimezone = engineScheduleClock.timezone || "";

  useEffect(() => {
    let canceled = false;
    fetchEngineScheduleClock()
      .then((clock) => {
        if (canceled) return;
        const nextClock = { ...clock, loaded: true };
        setEngineScheduleClock(nextClock);
        if (!initialJob && !quickJobDraft && !startDateTimeTouchedRef.current) {
          setStartDateTime(dayjsFromEngineClock(nextClock, 5 * 60, dayjs().add(5, "minute").second(0)));
        }
      })
      .catch(() => {
        if (!canceled) {
          setEngineScheduleClock((prev) => ({ ...prev, loaded: true }));
        }
      });
    return () => {
      canceled = true;
    };
  }, []);

  useEffect(() => {
    hydratedFormKeyRef.current = "";
    startDateTimeTouchedRef.current = false;
  }, [initialJob?.id]);

  useEffect(() => {
    const nextQuickDraft = location.state?.quickJobDraft || null;
    setQuickJobDraft(
      nextQuickDraft && quickDraftAppliedRef.current !== nextQuickDraft.id
        ? nextQuickDraft
        : null
    );
    const nextPatchDraft = location.state?.patchJobDraft || null;
    setPatchJobDraft(
      nextPatchDraft && quickDraftAppliedRef.current !== nextPatchDraft.id
        ? nextPatchDraft
        : null
    );
    setPatchJobReturnTo(resolvePatchJobReturnPath({ draft: nextPatchDraft }));
    if (!nextPatchDraft) {
      setPatchJobBatch(null);
      setPatchJobReturnTo("");
    }
  }, [location.key, location.state]);

  useEffect(() => {
    if (!(initialJob && initialJob.id)) {
      setHydratedInitialJob(null);
      return;
    }

    if (hasHydratedJobPayload(initialJob)) {
      setHydratedInitialJob(initialJob);
      return;
    }

    let canceled = false;

    const hydrateScheduledJob = async () => {
      try {
        const resp = await fetch(`/api/scheduled_jobs/${initialJob.id}`);
        const data = await resp.json();
        if (!resp.ok) {
          throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
        }

        if (!canceled && data?.job && typeof data.job === "object") {
          setHydratedInitialJob(data.job);
        }
      } catch (error) {
        console.error("Failed to hydrate scheduled job for editing:", error);
        if (!canceled) {
          setHydratedInitialJob(null);
        }
      }
    };

    void hydrateScheduledJob();

    return () => {
      canceled = true;
    };
  }, [initialJob]);

  const resolvedInitialJob = useMemo(() => {
    if (hasHydratedJobPayload(initialJob)) {
      return initialJob;
    }
    if (
      hydratedInitialJob &&
      Number(hydratedInitialJob.id || 0) === Number(initialJob?.id || 0)
    ) {
      return hydratedInitialJob;
    }
    return initialJob;
  }, [hydratedInitialJob, initialJob]);

  const loadCredentials = useCallback(async () => {
    setCredentialLoading(true);
    setCredentialError("");
    try {
      const resp = await fetch("/api/credentials");
      const data = await resp.json();
      if (!resp.ok) throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      const list = Array.isArray(data?.credentials) ? data.credentials : [];
      list.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));
      setCredentials(list);
    } catch (err) {
      setCredentials([]);
      setCredentialError(String(err.message || err));
    } finally {
      setCredentialLoading(false);
    }
  }, []);

  const loadAssemblies = useCallback(async () => {
    setAssembliesLoading(true);
    setAssembliesError("");
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const detail = await resp.text();
        throw new Error(detail || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      const normalized = parseAssembliesCollectionPayload(data);
      assemblyExportCacheRef.current.clear();
      setAssembliesPayload({
        items: normalized.items,
        queue: normalized.queue
      });
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setAssembliesPayload({ items: [], queue: [] });
      setAssembliesError(err?.message || "Failed to load assemblies");
    } finally {
      setAssembliesLoading(false);
    }
  }, []);

  const assemblyIndex = useMemo(
    () => buildAssemblyIndex(assembliesPayload.items, assembliesPayload.queue),
    [assembliesPayload.items, assembliesPayload.queue]
  );
  const assemblyPickerRows = useMemo(
    () =>
      (assemblyIndex.records || []).map((record) => {
        const sourcePath = String(record.path || "").replace(/\\/g, "/");
        const pathParts = sourcePath.split("/");
        const folder = pathParts.length > 1 ? pathParts.slice(0, -1).join("/") : "";
        const pathDisplay = formatPathDisplay(folder);
        const pathSearchValue = buildPathSearchValue(folder);
        let typeKey = "script";
        if (record.kind === "workflow" || record.type === "workflow") {
          typeKey = "workflow";
        } else if (record.kind === "ansible" || record.type === "ansible") {
          typeKey = "ansible";
        }

        return {
          id: record.assemblyGuid || record.pathLower || record.displayName,
          assemblyGuid: record.assemblyGuid,
          name: record.displayName || record.path || record.assemblyGuid,
          domain: record.domain || "user",
          domainLabel: record.domainLabel || record.domain || "General",
          isDirty: Boolean(record.isDirty),
          officialUpdateAvailable: Boolean(record.raw?.official_update_available),
          pathDisplay,
          pathSearchValue,
          typeKey,
          description: record.summary || "",
          rawPath: sourcePath,
          assemblyTypeFlags: buildFilterFlags(pathSearchValue, ASSEMBLY_TYPE_FILTER_OPTIONS),
          targetOsFlags: buildFilterFlags(pathSearchValue, ASSEMBLY_OS_FILTER_OPTIONS),
          record,
        };
      }),
    [assemblyIndex.records]
  );

  const loadAssemblyExport = useCallback(
    async (assemblyGuid) => {
      const cacheKey = assemblyGuid.toLowerCase();
      if (assemblyExportCacheRef.current.has(cacheKey)) {
        return assemblyExportCacheRef.current.get(cacheKey);
      }
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(assemblyGuid)}/export`);
      if (!resp.ok) {
        throw new Error(`Failed to load assembly (HTTP ${resp.status})`);
      }
      const data = await resp.json();
      assemblyExportCacheRef.current.set(cacheKey, data);
      return data;
    },
    []
  );

  useEffect(() => {
    loadCredentials();
  }, [loadCredentials]);
  useEffect(() => {
    loadAssemblies();
  }, [loadAssemblies]);

  // dialogs state
  const [addCompOpen, setAddCompOpen] = useState(false);
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [assemblyFilterText, setAssemblyFilterText] = useState("");
  const [assemblyTypeFilterMode, setAssemblyTypeFilterMode] = useState("");
  const [assemblyOsFilterMode, setAssemblyOsFilterMode] = useState("");
  const selectedAssemblyRecord = useMemo(() => {
    if (!selectedNodeId) return null;
    const key = String(selectedNodeId).toLowerCase();
    return assemblyIndex.byGuid?.get(key) || null;
  }, [selectedNodeId, assemblyIndex]);
  const assemblyTypeScopedRows = useMemo(() => {
    if (!assemblyOsFilterMode) return assemblyPickerRows;
    return assemblyPickerRows.filter((row) => row?.targetOsFlags?.[assemblyOsFilterMode]);
  }, [assemblyOsFilterMode, assemblyPickerRows]);
  const assemblyOsScopedRows = useMemo(() => {
    if (!assemblyTypeFilterMode) return assemblyPickerRows;
    return assemblyPickerRows.filter((row) => row?.assemblyTypeFlags?.[assemblyTypeFilterMode]);
  }, [assemblyPickerRows, assemblyTypeFilterMode]);
  const assemblyTypeCounts = useMemo(() => {
    const totals = ASSEMBLY_TYPE_FILTER_OPTIONS.reduce((acc, option) => {
      acc[option.key] = 0;
      return acc;
    }, {});
    assemblyTypeScopedRows.forEach((row) => {
      ASSEMBLY_TYPE_FILTER_OPTIONS.forEach((option) => {
        if (row?.assemblyTypeFlags?.[option.key]) {
          totals[option.key] += 1;
        }
      });
    });
    return totals;
  }, [assemblyTypeScopedRows]);
  const assemblyOsCounts = useMemo(() => {
    const totals = ASSEMBLY_OS_FILTER_OPTIONS.reduce((acc, option) => {
      acc[option.key] = 0;
      return acc;
    }, {});
    assemblyOsScopedRows.forEach((row) => {
      ASSEMBLY_OS_FILTER_OPTIONS.forEach((option) => {
        if (row?.targetOsFlags?.[option.key]) {
          totals[option.key] += 1;
        }
      });
    });
    return totals;
  }, [assemblyOsScopedRows]);
  const filteredAssemblyRows = useMemo(() => {
    const query = assemblyFilterText.trim().toLowerCase();
    return assemblyPickerRows.filter((row) => {
      if (assemblyTypeFilterMode && !row?.assemblyTypeFlags?.[assemblyTypeFilterMode]) {
        return false;
      }
      if (assemblyOsFilterMode && !row?.targetOsFlags?.[assemblyOsFilterMode]) {
        return false;
      }
      if (!query) return true;
      const fields = [row.name, row.domainLabel, row.pathDisplay, row.rawPath, row.description];
      return fields.some((value) => typeof value === "string" && value.toLowerCase().includes(query));
    });
  }, [assemblyFilterText, assemblyOsFilterMode, assemblyPickerRows, assemblyTypeFilterMode]);
  const assemblyColumnDefs = useMemo(
    () => [
      {
        colId: "source",
        field: "domain",
        headerName: "Source",
        valueGetter: (params) => params?.data?.domain || "",
        cellRenderer: AssemblyPickerSourceCellRenderer,
        minWidth: 132,
        width: 132,
        flex: 0,
        sortable: true,
        resizable: true,
        filter: "agTextColumnFilter",
      },
      {
        colId: "name",
        field: "name",
        headerName: "Name",
        valueGetter: (params) => params?.data?.name || "",
        cellRenderer: AssemblyPickerNameCellRenderer,
        minWidth: 420,
        flex: 1,
        sortable: true,
        sort: "asc",
        resizable: true,
        filter: "agTextColumnFilter",
      },
      {
        colId: "path",
        field: "pathDisplay",
        headerName: "Path",
        valueGetter: (params) => params?.data?.pathDisplay || "",
        cellRenderer: AssemblyPickerPathCellRenderer,
        minWidth: 280,
        width: 280,
        flex: 0,
        sortable: true,
        resizable: true,
        filter: "agTextColumnFilter",
      },
      {
        colId: "officialUpdate",
        field: "officialUpdateAvailable",
        headerName: "Update Available",
        minWidth: 160,
        width: 160,
        sortable: false,
        filter: false,
        resizable: true,
        cellRenderer: AssemblyPickerUpdateCellRenderer,
      },
    ],
    []
  );
  const assemblyDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      flex: 0,
      filter: "agTextColumnFilter",
      floatingFilter: false,
      cellClass: "auto-col-tight",
      cellStyle: LEFT_ALIGN_CELL_STYLE,
    }),
    []
  );
  const ASSEMBLY_AUTO_COLUMNS = useRef(["source", "name", "path"]);
  const assemblyGridApiRef = useRef(null);
  const runAssemblyGridLayoutPass = useCallback((apiOverride = null) => {
    const api = apiOverride || assemblyGridApiRef.current;
    if (!api) return;
    requestAnimationFrame(() => {
      try {
        if (typeof api.resetColumnState === "function") {
          api.resetColumnState();
        }
      } catch {}
      try {
        if (typeof api.sizeColumnsToFit === "function") {
          api.sizeColumnsToFit();
          return;
        }
      } catch {}
      try {
        if (typeof api.autoSizeColumns === "function") {
          api.autoSizeColumns(ASSEMBLY_AUTO_COLUMNS.current, false);
        }
      } catch {}
    });
  }, []);
  const handleAssemblyGridReady = useCallback((params) => {
    assemblyGridApiRef.current = params.api;
    runAssemblyGridLayoutPass(params.api);
  }, [runAssemblyGridLayoutPass]);
  const handleAssemblyGridFirstDataRendered = useCallback((params) => {
    runAssemblyGridLayoutPass(params.api);
  }, [runAssemblyGridLayoutPass]);
  const handleAssemblyDialogEntered = useCallback(() => {
    runAssemblyGridLayoutPass();
  }, [runAssemblyGridLayoutPass]);
  useEffect(() => {
    if (!addCompOpen || !assemblyGridApiRef.current) return;
    runAssemblyGridLayoutPass();
  }, [addCompOpen, filteredAssemblyRows, runAssemblyGridLayoutPass]);
  useEffect(() => {
    const api = assemblyGridApiRef.current;
    if (!api) return;
    api.paginationGoToFirstPage();
  }, [assemblyFilterText, assemblyOsFilterMode, assemblyTypeFilterMode]);

  const componentDomainSummary = useMemo(() => {
    const domains = new Set(
      components
        .map((component) => componentExecutionDomain(component))
        .filter(Boolean)
    );
    if (!domains.size) {
      return { kind: "none", domains: [] };
    }
    const ordered = Array.from(domains.values()).sort();
    if (ordered.includes("workflow")) {
      return {
        kind: ordered.length === 1 ? "workflow" : "mixed",
        domains: ordered,
      };
    }
    if (ordered.length === 1) {
      return { kind: ordered[0], domains: ordered };
    }
    return { kind: "mixed", domains: ordered };
  }, [components]);

  const mixedDomainWarning = useMemo(() => {
    if (componentDomainSummary.kind !== "mixed") return "";
    if (componentDomainSummary.domains.includes("workflow")) {
      return "Workflow-backed scheduled jobs cannot mix workflow, script, or Ansible assemblies. Remove the cross-domain assemblies before saving this job.";
    }
    if (
      componentDomainSummary.domains.includes("script") &&
      componentDomainSummary.domains.includes("ansible")
    ) {
      return "Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. Remove the cross-domain assemblies or split them into separate jobs.";
    }
    return "Scheduled jobs cannot mix assemblies from different execution domains.";
  }, [componentDomainSummary]);

  const availableExecContextOptions = useMemo(() => {
    if (componentDomainSummary.kind === "workflow" || componentDomainSummary.kind === "mixed") {
      return [];
    }
    const legacyLocalOption = execContext === "local"
      ? [{ value: "local", ...LEGACY_EXEC_CONTEXT_COPY.local }]
      : [];
    if (componentDomainSummary.kind === "script") {
      return EXEC_CONTEXT_OPTIONS.filter((option) => option.domain === "script");
    }
    if (componentDomainSummary.kind === "ansible") {
      return [
        ...legacyLocalOption,
        ...EXEC_CONTEXT_OPTIONS.filter((option) => option.domain === "ansible"),
      ];
    }
    return [...legacyLocalOption, ...EXEC_CONTEXT_OPTIONS];
  }, [componentDomainSummary, execContext]);

  const defaultExecContext = useMemo(() => {
    if (componentDomainSummary.kind === "ansible") return "ssh_individual";
    if (componentDomainSummary.kind === "script") return "system";
    return "system";
  }, [componentDomainSummary]);

  const remoteExec = useMemo(
    () => ANSIBLE_EXEC_CONTEXTS.includes(execContext),
    [execContext]
  );
  const handleExecContextChange = useCallback((value) => {
    const normalized = String(value || "system").toLowerCase();
    setExecContext(normalized);
    if (isWinRMExecContext(normalized)) {
      setUseSvcAccount(true);
      setSelectedCredentialId("");
    } else {
      setUseSvcAccount(false);
    }
  }, []);
  const filteredCredentials = useMemo(() => {
    if (!remoteExec) return credentials;
    const target = normalizeRemoteTransport(execContext) || "ssh";
    return credentials.filter((cred) => String(cred.connection_type || "").toLowerCase() === target);
  }, [credentials, remoteExec, execContext]);
  const credentialNeedsReset = useCallback((credential) => {
    if (!credential) return false;
    if (Boolean(credential.secret_reset_required)) return true;
    const state = String(credential?.metadata?.aegis_secret_state || "").trim().toLowerCase();
    const lostFields = Array.isArray(credential?.lost_secret_fields)
      ? credential.lost_secret_fields
      : credential?.metadata?.aegis_lost_secret_fields;
    return state === "reset_required" && Array.isArray(lostFields) && lostFields.length > 0;
  }, []);
  const selectedCredentialRecord = useMemo(
    () => filteredCredentials.find((cred) => String(cred.id) === String(selectedCredentialId)) || null,
    [filteredCredentials, selectedCredentialId]
  );
  const selectedCredentialResetRequired = credentialNeedsReset(selectedCredentialRecord);
  const selectedCredentialResetMessage =
    "The credential associated with this scheduled job can no longer be decrypted due to the Aegis Cipher being reset, please update the credential with the data it is missing.";

  useEffect(() => {
    if (!remoteExec) {
      return;
    }
    if (isWinRMExecContext(execContext) && useSvcAccount) {
      setSelectedCredentialId("");
      return;
    }
    if (!filteredCredentials.length) {
      setSelectedCredentialId("");
      return;
    }
    if (!selectedCredentialId || !filteredCredentials.some((cred) => String(cred.id) === String(selectedCredentialId))) {
      setSelectedCredentialId(String(filteredCredentials[0].id));
    }
  }, [remoteExec, filteredCredentials, selectedCredentialId, execContext, useSvcAccount]);

  useEffect(() => {
    if (componentDomainSummary.kind === "workflow" || componentDomainSummary.kind === "mixed") {
      return;
    }
    const validContexts = new Set(availableExecContextOptions.map((option) => option.value));
    if (!validContexts.size) {
      return;
    }
    if (!validContexts.has(execContext)) {
      handleExecContextChange(defaultExecContext);
    }
  }, [
    availableExecContextOptions,
    componentDomainSummary,
    defaultExecContext,
    execContext,
    handleExecContextChange,
  ]);

  useEffect(() => {
    if (!isWorkflowJob) return;
    if (targets.length) {
      setTargets([]);
    }
    if (execContext !== "system") {
      setExecContext("system");
    }
    if (selectedCredentialId) {
      setSelectedCredentialId("");
    }
    if (useSvcAccount) {
      setUseSvcAccount(false);
    }
  }, [isWorkflowJob, targets.length, execContext, selectedCredentialId, useSvcAccount]);

  const [addTargetOpen, setAddTargetOpen] = useState(false);
  const [availableDevices, setAvailableDevices] = useState([]); // [{hostname, display, online, deviceGuid, siteId, site}]
  const [selectedDeviceTargets, setSelectedDeviceTargets] = useState({});
  const [selectedFilterTargets, setSelectedFilterTargets] = useState({});
  const [selectedFilterRows, setSelectedFilterRows] = useState([]);
  const [deviceSearch, setDeviceSearch] = useState("");
  const [filterSearch, setFilterSearch] = useState("");
  const [targetPickerTab, setTargetPickerTab] = useState("devices");
  const [componentVarErrors, setComponentVarErrors] = useState({});
  const [quickJobMeta, setQuickJobMeta] = useState(null);
  const primaryComponentName = useMemo(() => {
    if (!components.length) return "";
    const first = components[0] || {};
    const candidates = [
      first.displayName,
      first.name,
      first.component_name,
      first.script_name,
      first.script_path,
      first.path
    ];
    for (const candidate of candidates) {
      if (typeof candidate === "string" && candidate.trim()) {
        return candidate.trim();
      }
    }
    return "";
  }, [components]);
  const patchJobSummary = useMemo(() => {
    if (!isPatchJob) return null;
    if (isPatchBatchJob) {
      return {
        bulk: true,
        items: patchJobBatch.items.map((item) => {
          const patch = item.patch && typeof item.patch === "object" ? item.patch : {};
          return {
            kb: String(patch.kb || "").trim() || "No KB",
            title: String(patch.title || item.name || "Patch Install").trim(),
            targetCount: Number(item.target_count || (Array.isArray(item.targets) ? item.targets.length : 0)) || 0,
            jobName: String(item.job_name || "").trim(),
          };
        }),
        targetCount: patchJobBatch.items.reduce((total, item) => {
          return total + (Number(item.target_count || (Array.isArray(item.targets) ? item.targets.length : 0)) || 0);
        }, 0),
      };
    }
    const component = components.find((item) => isPatchComponentRecord(item)) || {};
    const patch = component.patch && typeof component.patch === "object" ? component.patch : {};
    return {
      kb: String(patch.kb || "").trim() || "No KB",
      title: String(patch.title || component.name || "Patch Install").trim(),
      patchKey: String(patch.patch_key || "").trim(),
      targetCount: targets.length,
    };
  }, [components, isPatchBatchJob, isPatchJob, patchJobBatch, targets.length]);
  const [deviceRows, setDeviceRows] = useState([]);
  const [historyClockMs, setHistoryClockMs] = useState(() => Date.now());
  const [deviceStatusFilter, setDeviceStatusFilter] = useState("");
  const devicePickerGridApiRef = useRef(null);
  const filterPickerGridApiRef = useRef(null);

  const deviceIdentityKey = useCallback((value) => {
    if (!value || typeof value !== "object") return "";
    const guid = String(value.device_guid || value.deviceGuid || value.guid || value.agent_guid || "").trim().toLowerCase();
    if (guid) return `guid:${guid}`;
    const hostname = String(value.hostname || value.agent_hostname || value.display || "").trim().toLowerCase();
    if (!hostname) return "";
    const rawSiteId = value.site_id ?? value.siteId ?? value.siteID ?? "";
    const siteId = rawSiteId === "" || rawSiteId == null ? "" : String(rawSiteId).trim();
    if (siteId) return `site:${siteId}:${hostname}`;
    return `host:${hostname}`;
  }, []);

  const normalizeTarget = useCallback((rawTarget) => {
    if (!rawTarget) return null;
    if (typeof rawTarget === "string") {
      const host = rawTarget.trim();
      return host ? { kind: "device", hostname: host } : null;
    }
    if (typeof rawTarget === "object") {
      const rawKind = String(rawTarget.kind || "").toLowerCase();
      if (rawKind === "device" || rawTarget.hostname) {
        const host = String(rawTarget.hostname || "").trim();
        if (!host) return null;
        const osValue =
          rawTarget.os ||
          rawTarget.operating_system ||
          rawTarget.device_os ||
          rawTarget.platform ||
          rawTarget.agent_os ||
          rawTarget.system ||
          rawTarget.os_name ||
          (rawTarget.summary && (rawTarget.summary.os || rawTarget.summary.operating_system)) ||
          "";
        const siteValue = rawTarget.site || rawTarget.site_name || rawTarget.site_scope || rawTarget.siteScope || "";
        const siteIdValue = rawTarget.site_id ?? rawTarget.siteId ?? null;
        const deviceGuidValue =
          rawTarget.device_guid ||
          rawTarget.deviceGuid ||
          rawTarget.guid ||
          rawTarget.agent_guid ||
          "";
        return {
          kind: "device",
          hostname: host,
          os: osValue,
          site: siteValue,
          site_name: siteValue,
          site_id: siteIdValue,
          device_guid: deviceGuidValue,
        };
      }
      if (rawKind === "filter" || rawTarget.filter_id != null || rawTarget.id != null) {
        const idValue = rawTarget.filter_id ?? rawTarget.id;
        const filterId = Number(idValue);
        if (!Number.isFinite(filterId)) return null;
        const catalogEntry =
          filterCatalogMapRef.current[filterId] || filterCatalogMapRef.current[String(filterId)] || {};
        const deviceCount =
          typeof rawTarget.deviceCount === "number" && Number.isFinite(rawTarget.deviceCount)
            ? rawTarget.deviceCount
            : typeof rawTarget.matching_device_count === "number" && Number.isFinite(rawTarget.matching_device_count)
            ? rawTarget.matching_device_count
            : typeof catalogEntry.deviceCount === "number"
            ? catalogEntry.deviceCount
            : null;
        return {
          kind: "filter",
          filter_id: filterId,
          name: rawTarget.name || catalogEntry.name || `Filter #${filterId}`,
          site_mode: rawTarget.site_mode || catalogEntry.site_mode || "global",
          scope: rawTarget.scope || catalogEntry.scope || "Global",
          description: rawTarget.description || catalogEntry.description || "",
          siteSummary: rawTarget.siteSummary || catalogEntry.siteSummary || "",
          deviceCount,
        };
      }
    }
    return null;
  }, []);

  const targetKey = useCallback((target) => {
    if (!target) return "";
    if (target.kind === "filter") return `filter-${target.filter_id}`;
    if (target.kind === "device") return `device-${deviceIdentityKey(target)}`;
    return "";
  }, [deviceIdentityKey]);

  const normalizeTargetList = useCallback(
    (list) => {
      if (!Array.isArray(list)) return [];
      const seen = new Set();
      const next = [];
      list.forEach((entry) => {
        const normalized = normalizeTarget(entry);
        if (!normalized) return;
        const key = targetKey(normalized);
        if (!key || seen.has(key)) return;
        seen.add(key);
        next.push(normalized);
      });
      return next;
    },
    [normalizeTarget, targetKey]
  );

  const serializeTargetsForSave = useCallback((list) => {
    if (!Array.isArray(list)) return [];
    return list
      .map((target) => {
        if (!target) return null;
        if (target.kind === "filter") {
          return {
            kind: "filter",
            filter_id: target.filter_id,
            name: target.name,
          };
        }
        if (target.kind === "device") {
          return {
            kind: "device",
            device_guid: target.device_guid || "",
            hostname: target.hostname,
            site_id: target.site_id ?? null,
            site_name: target.site_name || target.site || "",
          };
        }
        return null;
      })
      .filter(Boolean);
  }, []);

  const addTargets = useCallback(
    (entries) => {
      const candidateList = Array.isArray(entries) ? entries : [entries];
      setTargets((prev) => {
        const seen = new Set(prev.map((existing) => targetKey(existing)).filter(Boolean));
        const additions = [];
        candidateList.forEach((entry) => {
          const normalized = normalizeTarget(entry);
          if (!normalized) return;
          const key = targetKey(normalized);
          if (!key || seen.has(key)) return;
          seen.add(key);
          additions.push(normalized);
        });
        if (!additions.length) return prev;
        return [...prev, ...additions];
      });
    },
    [normalizeTarget, targetKey]
  );

  const removeTarget = useCallback(
    (targetToRemove) => {
      const removalKey = targetKey(targetToRemove);
      if (!removalKey) return;
      setTargets((prev) => prev.filter((target) => targetKey(target) !== removalKey));
    },
    [targetKey]
  );
  const availableDeviceMap = useMemo(() => {
    const map = new Map();
    availableDevices.forEach((device) => {
      const key = deviceIdentityKey(device);
      if (!key) return;
      map.set(key, device);
    });
    return map;
  }, [availableDevices, deviceIdentityKey]);
  const devicePickerRows = useMemo(() => {
    const query = deviceSearch.trim().toLowerCase();
    if (query.length < 3) return [];
    return availableDevices
      .filter((device) => {
        const display = String(device?.display || device?.hostname || "").toLowerCase();
        return display.includes(query);
      })
      .map((device, index) => ({
        id: deviceIdentityKey(device) || String(device?.hostname || device?.display || `device-${index}`).toLowerCase(),
        name: device?.display || device?.hostname || "Device",
        status: device?.online ? "Online" : "Offline",
        os: device?.os || "",
        site: device?.site || "",
        raw: device,
      }));
  }, [availableDevices, deviceIdentityKey, deviceSearch]);
  const devicePickerOverlay = useMemo(
    () =>
      deviceSearch.trim().length < 3
        ? "Type 3+ characters to search devices."
        : "No devices match your search.",
    [deviceSearch]
  );
  const filterPickerRows = useMemo(() => {
    const query = filterSearch.trim().toLowerCase();
    if (query.length < 3) return [];
    return (filterCatalog || [])
      .filter((f) => {
        if (!query) return true;
        const haystack = [
          f?.name,
          f?.description,
          f?.scope,
          f?.siteSummary,
        ]
          .filter(Boolean)
          .join(" ")
          .toLowerCase();
        return haystack.includes(query);
      })
      .map((f, index) => {
        const deviceCount =
          typeof f.deviceCount === "number"
            ? f.deviceCount
            : f.devices_targeted ?? f.matching_device_count ?? null;
        return {
          id: String(f.id ?? f.filter_id ?? index),
          name: f.name || `Filter ${index + 1}`,
          description: f.description || "",
          deviceCount,
          scope: f.scope || "Global",
          scopeKey: f.site_mode || "global",
          site: f.siteSummary || "",
          raw: f,
        };
      });
  }, [filterCatalog, filterSearch]);
  const filterPickerRowMap = useMemo(() => {
    const map = new Map();
    filterPickerRows.forEach((row) => {
      map.set(String(row.id), row);
    });
    return map;
  }, [filterPickerRows]);
  const filterPickerOverlay = useMemo(() => {
    if (!filterCatalog?.length) return "No device filters available.";
    const query = filterSearch.trim();
    if (query.length < 3) return "Type 3+ characters to search filters.";
    if (query && filterPickerRows.length === 0) return "No filters match your search.";
    return "Select filters to target.";
  }, [filterCatalog?.length, filterPickerRows.length, filterSearch]);
  const deviceRowsMap = useMemo(() => {
    const map = new Map();
    deviceRows.forEach((device) => {
      const key = deviceIdentityKey(device);
      if (!key) return;
      const osValue =
        device?.os ||
        device?.operating_system ||
        device?.agent_os ||
        device?.system ||
        device?.os_name ||
        (device?.summary && (device.summary.os || device.summary.operating_system)) ||
        "";
      const siteValue = device?.site || device?.site_name || device?.site_scope || device?.summary?.site || "";
      map.set(key, { ...device, os: osValue, site: siteValue });
    });
    return map;
  }, [deviceIdentityKey, deviceRows]);
  const targetGridRows = useMemo(() => {
    const resolveOs = (target) => {
      if (!target || target.kind !== "device") return "";
      const explicit =
        target.os ||
        target.operating_system ||
        target.device_os ||
        target.platform ||
        target.agent_os ||
        target.system ||
        target.os_name;
      if (explicit) return explicit;
      const key = deviceIdentityKey(target);
      if (!key) return "";
      const fromAvailable = availableDeviceMap.get(key);
      if (fromAvailable?.os) return fromAvailable.os;
      const fromHistory = deviceRowsMap.get(key);
      if (fromHistory?.os) return fromHistory.os;
      return "";
    };
    return targets.map((target) => {
      const key = targetKey(target) || `${target?.kind || "target"}-${Math.random().toString(36).slice(2, 8)}`;
      const isFilter = target?.kind === "filter";
      const siteLabel = isFilter
        ? ""
        : target?.site ||
          target?.site_name ||
          (() => {
            const key = deviceIdentityKey(target);
            return availableDeviceMap.get(key)?.site || deviceRowsMap.get(key)?.site || "";
          })();
      const deviceCount =
        typeof target?.deviceCount === "number" && Number.isFinite(target.deviceCount) ? target.deviceCount : null;
      const detailText = isFilter
        ? [
            `${deviceCount != null ? deviceCount.toLocaleString() : "—"} device${deviceCount === 1 ? "" : "s"}`,
            target?.scope || "",
            target?.description || "",
          ]
            .filter(Boolean)
            .join(" • ")
        : "—";
      const osLabel = isFilter ? "—" : resolveOs(target) || "Unknown";
      return {
        id: key,
        typeLabel: isFilter ? "Device Filter" : "Device",
        siteLabel,
        targetLabel: isFilter ? target?.name || `Filter #${target?.filter_id}` : target?.hostname,
        detailText,
        osLabel,
        rawTarget: target,
      };
    });
  }, [targets, targetKey, availableDeviceMap, deviceIdentityKey, deviceRowsMap]);
  const targetGridColumnDefs = useMemo(
    () => [
      { field: "typeLabel", headerName: "Type", minWidth: 120, flex: 0.9, filter: "agTextColumnFilter" },
      {
        field: "siteLabel",
        headerName: "Site",
        minWidth: 160,
        flex: 1,
        filter: "agTextColumnFilter",
        cellRenderer: (params) => (
          <Box component="span" sx={{ color: "#e2e8f0" }}>
            {params.value || ""}
          </Box>
        ),
        cellClass: "auto-col-tight",
      },
      { field: "targetLabel", headerName: "Target", minWidth: 200, flex: 1.2, filter: "agTextColumnFilter" },
      { field: "osLabel", headerName: "Operating System", minWidth: 170, flex: 1.1, filter: "agTextColumnFilter" },
      { field: "detailText", headerName: "Details", minWidth: 200, flex: 1, filter: "agTextColumnFilter" },
      {
        field: "actions",
        headerName: "",
        minWidth: 100,
        flex: 1.5,
        cellRenderer: "TargetActionsRenderer",
        sortable: false,
        suppressHeaderMenuButton: true,
        filter: false,
      },
    ],
    []
  );
  const targetGridComponents = useMemo(
    () => ({
      TargetActionsRenderer: (params) => (
        <Box sx={{ display: "flex", justifyContent: "flex-end", width: "100%" }}>
          <IconButton
            size="small"
            onClick={(e) => {
              e.stopPropagation();
              params.context?.removeTarget?.(params.data?.rawTarget);
            }}
            sx={{
              color: "#fb7185",
              "&:hover": { color: "#fecdd3" },
            }}
          >
            <DeleteIcon fontSize="small" />
          </IconButton>
        </Box>
      ),
    }),
    []
  );
  const targetGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 1,
      filter: true,
      floatingFilter: false,
      cellClass: "auto-col-tight",
      cellStyle: LEFT_ALIGN_CELL_STYLE,
    }),
    []
  );
  const targetGridApiRef = useRef(null);
  const TARGET_AUTO_COLS = useRef(["typeLabel", "targetLabel", "osLabel", "detailText"]);
  const handleTargetGridReady = useCallback((params) => {
    targetGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(TARGET_AUTO_COLS.current, true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    if (!targetGridApiRef.current) return;
    requestAnimationFrame(() => {
      try {
        targetGridApiRef.current.autoSizeColumns(TARGET_AUTO_COLS.current, true);
      } catch {}
    });
  }, [targetGridRows]);

  const devicePickerColumnDefs = useMemo(
    () => [
      { field: "site", headerName: "Site", minWidth: 160, flex: 1, cellClass: "auto-col-tight" },
      { field: "name", headerName: "Name", minWidth: 200, flex: 1.2, cellClass: "auto-col-tight" },
      { field: "status", headerName: "Status", minWidth: 140, flex: 0.9, cellClass: "auto-col-tight" },
      { field: "os", headerName: "OS", minWidth: 180, flex: 1.8, cellClass: "auto-col-tight" },
    ],
    []
  );
  const devicePickerDefaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      flex: 1,
      cellClass: "auto-col-tight",
      cellStyle: LEFT_ALIGN_CELL_STYLE,
    }),
    []
  );
  const handleDevicePickerReady = useCallback((params) => {
    devicePickerGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(["name", "status", "os"], true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    const api = devicePickerGridApiRef.current;
    if (!api) return;
    api.forEachNode((node) => {
      const selected = !!selectedDeviceTargets[node?.data?.id];
      if (node.isSelected() !== selected) {
        node.setSelected(selected);
      }
    });
  }, [selectedDeviceTargets, devicePickerRows]);
  const handleDevicePickerSelectionChanged = useCallback(() => {
    const api = devicePickerGridApiRef.current;
    if (!api) return;
    const next = {};
    api.getSelectedNodes().forEach((node) => {
      if (node?.data?.id) next[node.data.id] = true;
    });
    setSelectedDeviceTargets(next);
  }, []);

  const filterPickerColumnDefs = useMemo(
    () => [
      { field: "name", headerName: "Filter", minWidth: 220, flex: 1.4, cellClass: "auto-col-tight" },
      {
        field: "deviceCount",
        headerName: "Devices",
        minWidth: 140,
        flex: 0.8,
        valueFormatter: (params) => (typeof params.value === "number" ? params.value.toLocaleString() : "—"),
        cellClass: "auto-col-tight",
      },
      { field: "scope", headerName: "Scope", minWidth: 140, flex: 0.9, cellClass: "auto-col-tight" },
      { field: "description", headerName: "Description", minWidth: 200, flex: 1.2, cellClass: "auto-col-tight" },
    ],
    []
  );
  const filterPickerDefaultColDef = devicePickerDefaultColDef;
  const handleFilterPickerReady = useCallback((params) => {
    filterPickerGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(["name", "deviceCount", "scope", "description"], true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    const api = filterPickerGridApiRef.current;
    if (!api) return;
    api.forEachNode((node) => {
      const selected = !!selectedFilterTargets[node?.data?.id];
      if (node.isSelected() !== selected) {
        node.setSelected(selected);
      }
    });
  }, [selectedFilterTargets, filterPickerRows]);
  const handleFilterPickerSelectionChanged = useCallback((params) => {
    const api = params?.api || filterPickerGridApiRef.current;
    if (!api) return;
    const next = {};
    const rows = typeof api.getSelectedRows === "function" ? api.getSelectedRows() : [];
    if (rows.length) {
      rows.forEach((row) => {
        if (row?.id != null) next[row.id] = true;
      });
      setSelectedFilterRows(rows);
    } else {
      const nodes = api.getSelectedNodes ? api.getSelectedNodes() : [];
      const collectedRows = [];
      nodes.forEach((node) => {
        if (node?.data?.id != null) {
          next[node.data.id] = true;
          collectedRows.push(node.data);
        }
      });
      setSelectedFilterRows(collectedRows);
    }
    setSelectedFilterTargets(next);
  }, []);
  const handleAddSelectedTargets = useCallback(() => {
    if (targetPickerTab === "filters") {
      const gridNodes =
        (filterPickerGridApiRef.current &&
          filterPickerGridApiRef.current.getSelectedNodes()) ||
        [];
      const additions = [];
      const api = filterPickerGridApiRef.current;
      const fromStateIds = new Set(
        Object.entries(selectedFilterTargets)
          .filter(([, checked]) => checked)
          .map(([id]) => String(id))
      );

      const rowsToUse =
        (selectedFilterRows && selectedFilterRows.length
          ? selectedFilterRows
          : null) ||
        (() => {
          if (api && typeof api.getSelectedRows === "function") {
            const rows = api.getSelectedRows();
            if (Array.isArray(rows) && rows.length) return rows;
          }
          if (gridNodes.length) {
            const rows = gridNodes.map((n) => n?.data).filter(Boolean);
            if (rows.length) return rows;
          }
          if (api && typeof api.forEachNode === "function") {
            const rows = [];
            api.forEachNode((node) => {
              if (node && node.isSelected && node.isSelected()) {
                rows.push(node.data);
              }
            });
            if (rows.length) return rows;
          }
          return [];
        })();

      rowsToUse.forEach((row) => {
        const parsedId = Number(row?.id ?? row?.filter_id);
        if (!Number.isFinite(parsedId)) return;
        additions.push({
          kind: "filter",
          filter_id: parsedId,
          name: row?.name || `Filter #${parsedId}`,
          site_scope: row?.scopeKey || row?.scope || "global",
          site: null,
          deviceCount: row?.deviceCount,
        });
      });

      if (!additions.length && fromStateIds.size) {
        fromStateIds.forEach((id) => {
          const catalog =
            filterCatalogMapRef.current[id] || filterCatalogMapRef.current[String(id)] || null;
          const source = catalog || null;
          const parsedId = Number(source?.id ?? source?.filter_id ?? id);
          if (!Number.isFinite(parsedId)) return;
          additions.push({
            kind: "filter",
            filter_id: parsedId,
            name: source?.name || `Filter #${parsedId}`,
            site_scope: source?.site_scope || source?.scope || source?.type || "global",
            site: null,
            deviceCount: source?.deviceCount ?? source?.devices_targeted ?? source?.matching_device_count,
          });
        });
      }
      if (!additions.length && filterPickerRows.length === 1) {
        const row = filterPickerRows[0];
        const parsedId = Number(row?.id ?? row?.filter_id);
        if (Number.isFinite(parsedId)) {
          additions.push({
            kind: "filter",
            filter_id: parsedId,
            name: row?.name || `Filter #${parsedId}`,
            site_scope: row?.scopeKey || row?.scope || "global",
            site: null,
            deviceCount: row?.deviceCount,
          });
        }
      }
      if (additions.length) {
        addTargets(additions);
      } else {
        alert("Select at least one filter to add.");
        return;
      }
    } else {
      const chosenDevices = Object.keys(selectedDeviceTargets)
        .filter((deviceKey) => selectedDeviceTargets[deviceKey])
        .map((deviceKey) => {
          const lookup = availableDeviceMap.get(deviceKey);
          if (lookup) {
            return {
              kind: "device",
              hostname: lookup.hostname,
              os: lookup.os,
              site: lookup.site,
              site_name: lookup.site_name || lookup.site,
              site_id: lookup.site_id ?? lookup.siteId ?? null,
              device_guid: lookup.device_guid || lookup.deviceGuid || "",
            };
          }
          return null;
        })
        .filter(Boolean);

      if (!chosenDevices.length) {
        return;
      }

      addTargets(chosenDevices);
    }

    setAddTargetOpen(false);
  }, [
    addTargets,
    availableDeviceMap,
    filterPickerRows,
    selectedDeviceTargets,
    selectedFilterRows,
    selectedFilterTargets,
    targetPickerTab,
  ]);

  useEffect(() => {
    setTargets((prev) => {
      let changed = false;
      const next = prev.map((target) => {
        if (target?.kind === "filter") {
          const normalized = normalizeTarget(target);
          if (normalized) {
            const sameKey = targetKey(normalized) === targetKey(target);
            if (!sameKey || normalized.name !== target.name || normalized.deviceCount !== target.deviceCount || normalized.site !== target.site) {
              changed = true;
              return normalized;
            }
          }
        }
        return target;
      });
      return changed ? next : prev;
    });
  }, [filterCatalog, normalizeTarget, targetKey]);

  const generateLocalId = useCallback(
    () => `${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    []
  );

  const fmtTs = useCallback((ts) => {
    if (!ts) return "";
    try {
      const d = new Date(Number(ts) * 1000);
      return d.toLocaleString(undefined, {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "numeric",
        minute: "2-digit"
      });
    } catch {
      return "";
    }
  }, []);

  const deviceFiltered = useMemo(
    () => filterJobHistoryRowsByStatus(deviceRows, deviceStatusFilter),
    [deviceRows, deviceStatusFilter]
  );

  const jobHistoryGridRows = useMemo(
    () =>
      deviceFiltered.map((row, index) => {
        const progress = row?.patch_progress && typeof row.patch_progress === "object" ? row.patch_progress : null;
        const canonicalLabel = JOB_RESULT_THEME[normalizeJobStatusKey(row?.job_status || "")]?.label || (row.job_status || "");
        const probeLabel = connectionProbeStatusLabel(row?.job_status, row?.connection_probe_deadline_ts, historyClockMs);
        const displayLabel = probeLabel || String(row?.display_status_label || "").trim() || patchProgressLabel(progress, row?.job_status || "") || canonicalLabel;
        const progressTooltip = String(row?.status_tooltip || "").trim() || (probeLabel ? "Waiting for WireGuard and target port readiness before playbook execution." : patchProgressTooltip(progress));
        return {
          id: `${row.hostname || "device"}-${index}`,
          hostname: row.hostname || "",
          online: Boolean(row.online),
          onlineLabel: row?.online ? "Online" : "Offline",
          site: row.site || "",
          ranOn: row.ran_on,
          jobStatus: row.job_status || "",
          jobStatusLabel: displayLabel,
          jobStatusFilterLabel: canonicalLabel,
          jobStatusTooltip: progressTooltip,
          jobStatusSortRank: getJobStatusSortRank(row?.job_status || ""),
          patchProgress: progress,
          hasStdOut:
            Boolean(row.has_stdout) ||
            (Array.isArray(row.activities) && row.activities.some((activity) => Boolean(activity?.has_stdout))),
          hasStdErr:
            Boolean(row.has_stderr) ||
            (Array.isArray(row.activities) && row.activities.some((activity) => Boolean(activity?.has_stderr))),
          raw: row,
        };
      }).map((row) => ({
        ...row,
        outputState:
          row.hasStdOut && row.hasStdErr
            ? "StdOut & StdErr"
            : row.hasStdOut
            ? "StdOut"
            : row.hasStdErr
            ? "StdErr"
            : "None",
      })),
    [deviceFiltered, historyClockMs]
  );
  const hydrateExistingComponents = useCallback(async (rawComponents = []) => {
    const results = [];
    for (const raw of rawComponents) {
      if (!raw || typeof raw !== "object") continue;
      const typeRaw = String(
        raw.type ||
        raw.component_type ||
        raw.assembly_type ||
        raw.assemblyType ||
        "script"
      ).trim().toLowerCase();
      const subtypeRaw = String(
        raw.assembly_subtype ||
        raw.assemblySubtype ||
        raw.script_type ||
        ""
      ).trim().toLowerCase();
      const isWorkflow = typeRaw === "workflow" || subtypeRaw === "workflow";
      const isAnsible = typeRaw === "ansible" || typeRaw === "playbook" || subtypeRaw === "ansible" || subtypeRaw === "playbook";
      if (isWorkflow) {
        results.push({
          ...raw,
          type: "workflow",
          assembly_type: "workflow",
          assembly_subtype: "workflow",
          variables: Array.isArray(raw.variables) ? raw.variables : [],
          localId: generateLocalId()
        });
        continue;
      }
      const kind = isAnsible ? "ansible" : "script";
      const assemblyGuidRaw = raw.assembly_guid || raw.assemblyGuid;
      let record = null;
      if (assemblyGuidRaw) {
        const guidKey = String(assemblyGuidRaw).trim().toLowerCase();
        record = assemblyIndex.byGuid?.get(guidKey) || null;
      }
      if (!record) {
        record = resolveAssemblyForComponent(assemblyIndex, raw, kind);
      }
      if (!record) {
        const fallbackPath =
          raw.path ||
          raw.script_path ||
          raw.playbook_path ||
          raw.rel_path ||
          raw.scriptPath ||
          raw.playbookPath ||
          "";
        const normalizedFallback = normalizeAssemblyPath(
          kind,
          fallbackPath,
          raw.name || raw.file_name || raw.tab_name || ""
        );
        record = assemblyIndex.byPath?.get(normalizedFallback.toLowerCase()) || null;
      }
      if (!record) {
        const mergedFallback = mergeComponentVariables([], raw.variables, raw.variable_values);
        results.push({
          ...raw,
          type: kind,
          assembly_type: kind,
          assembly_subtype: kind === "ansible" ? "ansible" : "powershell",
          path: normalizeAssemblyPath(
            kind,
            raw.path || raw.script_path || raw.playbook_path || "",
            raw.name || raw.file_name || ""
          ),
          name: raw.name || raw.file_name || raw.tab_name || raw.path || "Assembly",
          description: raw.description || raw.path || "",
          variables: mergedFallback,
          localId: generateLocalId()
        });
        continue;
      }
      const exportDoc = await loadAssemblyExport(record.assemblyGuid);
      const parsed = parseAssemblyExport(exportDoc);
      const docVars = Array.isArray(parsed.rawVariables) ? parsed.rawVariables : [];
      const mergedVariables = mergeComponentVariables(docVars, raw.variables, raw.variable_values);
      results.push({
        ...raw,
        type: kind,
        assembly_type: record.kind || kind,
        assembly_subtype: record.type || (kind === "ansible" ? "ansible" : "powershell"),
        path: normalizeAssemblyPath(kind, record.path || "", record.displayName),
        name: raw.name || record.displayName,
        description: raw.description || record.summary || record.path,
        variables: mergedVariables,
        localId: generateLocalId(),
        assembly_guid: record.assemblyGuid,
        assemblyGuid: record.assemblyGuid,
        domain: record.domain,
        domainLabel: record.domainLabel
      });
    }
    return results;
  }, [assemblyIndex, loadAssemblyExport, mergeComponentVariables, generateLocalId]);

  const sanitizeComponentsForSave = useCallback((items) => {
    return (Array.isArray(items) ? items : []).map((comp) => {
      if (!comp || typeof comp !== "object") return comp;
      const { localId, ...rest } = comp;
      const sanitized = { ...rest };
      if (isPatchComponentRecord(comp)) {
        sanitized.kind = "patch_install";
        sanitized.type = "patch_install";
        sanitized.component_type = "patch_install";
        sanitized.assembly_type = "patch_install";
        delete sanitized.assemblyGuid;
        return sanitized;
      }
      const guidRaw = comp.assembly_guid || comp.assemblyGuid || "";
      if (guidRaw) {
        sanitized.assembly_guid = String(guidRaw).trim().toLowerCase();
      }
      delete sanitized.assemblyGuid;
      const typeRaw = String(
        comp.type ||
        comp.component_type ||
        comp.assembly_type ||
        ""
      ).trim().toLowerCase();
      const subtypeRaw = String(comp.assembly_subtype || "").trim().toLowerCase();
      const isWorkflow = typeRaw === "workflow" || subtypeRaw === "workflow";
      const isAnsible = typeRaw === "ansible" || typeRaw === "playbook" || subtypeRaw === "ansible";
      const normalizedKind = isWorkflow ? "workflow" : isAnsible ? "ansible" : "script";
      sanitized.type = normalizedKind;
      sanitized.assembly_type = normalizedKind;
      sanitized.assembly_subtype =
        normalizedKind === "workflow"
          ? "workflow"
          : normalizedKind === "ansible"
            ? "ansible"
            : subtypeRaw || "powershell";
      if (Array.isArray(comp.variables)) {
        const valuesMap = {};
        sanitized.variables = comp.variables
          .filter((v) => v && typeof v.name === "string" && v.name)
          .map((v) => {
            const entry = {
              name: v.name,
              label: v.label || v.name,
              type: v.type || "string",
              required: Boolean(v.required),
              description: v.description || ""
            };
            if (Object.prototype.hasOwnProperty.call(v, "default")) entry.default = v.default;
            if (Object.prototype.hasOwnProperty.call(v, "value")) {
              entry.value = v.value;
              valuesMap[v.name] = v.value;
            }
            return entry;
          });
        if (!sanitized.variables.length) sanitized.variables = [];
        if (Object.keys(valuesMap).length) sanitized.variable_values = valuesMap;
        else delete sanitized.variable_values;
      }
      return sanitized;
    });
  }, []);

  const updateComponentVariable = useCallback((localId, name, value) => {
    if (!localId || !name) return;
    setComponents((prev) => prev.map((comp) => {
      if (!comp || comp.localId !== localId) return comp;
      const vars = Array.isArray(comp.variables) ? comp.variables : [];
      const nextVars = vars.map((variable) => {
        if (!variable || variable.name !== name) return variable;
        return { ...variable, value: coerceVariableValue(variable.type || "string", value) };
      });
      return { ...comp, variables: nextVars };
    }));
    setComponentVarErrors((prev) => {
      if (!prev[localId] || !prev[localId][name]) return prev;
      const next = { ...prev };
      const compErrors = { ...next[localId] };
      delete compErrors[name];
      if (Object.keys(compErrors).length) next[localId] = compErrors;
      else delete next[localId];
      return next;
    });
  }, []);

  const removeComponent = useCallback((localId) => {
    setComponents((prev) => prev.filter((comp) => comp.localId !== localId));
    setComponentVarErrors((prev) => {
      if (!prev[localId]) return prev;
      const next = { ...prev };
      delete next[localId];
      return next;
    });
  }, []);

  const isValid = useMemo(() => {
    const hasRequiredTargets = isWorkflowJob ? true : targets.length > 0;
    const base = jobName.trim().length > 0 && components.length > 0 && hasRequiredTargets;
    if (!base) return false;
    if (isPatchBatchJob) {
      if (!["immediately", "once"].includes(scheduleType)) return false;
      const hasValidBatchItems = patchJobBatch.items.every((item) => {
        return Array.isArray(item.targets) && item.targets.length > 0 && item.patch && (item.patch.patch_key || item.patch.kb || item.patch.title);
      });
      if (!hasValidBatchItems) return false;
      if (scheduleType !== "immediately") {
        return !!startDateTime;
      }
      return true;
    }
    if (isPatchJob) {
      if (!["immediately", "once"].includes(scheduleType)) return false;
      if (scheduleType !== "immediately") {
        return !!startDateTime;
      }
      return true;
    }
    const needsCredential = !isWorkflowJob && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount);
    if (needsCredential && !selectedCredentialId) return false;
    if (scheduleType !== "immediately") {
      return !!startDateTime;
    }
    return true;
  }, [jobName, components.length, isWorkflowJob, isPatchBatchJob, isPatchJob, patchJobBatch, targets.length, scheduleType, startDateTime, remoteExec, selectedCredentialId, execContext, useSvcAccount]);

  const handleJobNameInputChange = useCallback((value) => {
    setJobName(value);
    setQuickJobMeta((prev) => {
      if (!prev?.allowAutoRename) return prev;
      if (!prev.currentAutoName) return prev;
      if (value.trim() !== prev.currentAutoName.trim()) {
        return { ...prev, allowAutoRename: false };
      }
      return prev;
    });
  }, []);

  const [confirmOpen, setConfirmOpen] = useState(false);
  const editing = !!(initialJob && initialJob.id);

  useEffect(() => {
    if (editing) {
      quickDraftAppliedRef.current = null;
      setQuickJobMeta(null);
    }
  }, [editing]);

  // --- Job History (only when editing) ---
  const [historyRows, setHistoryRows] = useState([]);
  const activityCacheRef = useRef(new Map());
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputSections, setOutputSections] = useState([]);
  const [outputLoading, setOutputLoading] = useState(false);
  const [outputError, setOutputError] = useState("");
  const [copiedOutputKey, setCopiedOutputKey] = useState("");
  const outputCopyResetRef = useRef(null);
  const probeRefreshAtRef = useRef(0);
  const [clearingHistory, setClearingHistory] = useState(false);
  const [rerunningJob, setRerunningJob] = useState(false);
  const [selectedHistoryOccurrence, setSelectedHistoryOccurrence] = useState(null);
  const { activeKey: historySubTabKey, setActiveKey: setHistorySubTabKey } = useUrlTabState({
    param: "history_view",
    defaultKey: "current",
    allowedKeys: JOB_HISTORY_SUBTAB_KEYS,
    keyByUrl: JOB_HISTORY_SUBTAB_KEY_BY_URL,
    urlByKey: JOB_HISTORY_SUBTAB_URL_BY_KEY,
  });
  const effectiveHistorySubTabKey =
    historySubTabKey === "historical_run" && !selectedHistoryOccurrence ? "historical" : historySubTabKey;
  const selectedHistoryRunLabel = useMemo(
    () => (selectedHistoryOccurrence ? fmtTs(selectedHistoryOccurrence) : "Historical Run"),
    [fmtTs, selectedHistoryOccurrence]
  );

  useEffect(() => () => {
    if (outputCopyResetRef.current) {
      clearTimeout(outputCopyResetRef.current);
      outputCopyResetRef.current = null;
    }
  }, []);

  const loadHistory = useCallback(async () => {
    if (!editing) return;
    try {
      const [runsResp, jobResp] = await Promise.all([
        fetch(`/api/scheduled_jobs/${initialJob.id}/runs?days=30`),
        fetch(`/api/scheduled_jobs/${initialJob.id}`)
      ]);
      const runs = await runsResp.json();
      const job = await jobResp.json();
      if (!runsResp.ok) throw new Error(runs.error || `HTTP ${runsResp.status}`);
      if (!jobResp.ok) throw new Error(job.error || `HTTP ${jobResp.status}`);
      const jobPayload = job.job || {};
      const latestOccurrence = Number(jobPayload?.latest_occurrence || jobPayload?.last_run_ts || 0);
      const selectedOccurrence = Number(selectedHistoryOccurrence || 0);
      const occurrence =
        effectiveHistorySubTabKey === "historical_run" && selectedOccurrence
          ? selectedOccurrence
          : latestOccurrence;
      const devicesUrl = occurrence
        ? `/api/scheduled_jobs/${initialJob.id}/devices?occurrence=${occurrence}`
        : `/api/scheduled_jobs/${initialJob.id}/devices`;
      const devResp = await fetch(devicesUrl);
      const dev = await devResp.json();
      if (!devResp.ok) throw new Error(dev.error || `HTTP ${devResp.status}`);
      setHistoryRows(
        Array.isArray(runs.runs)
          ? runs.runs.map((entry) => {
              const skipReason = String(entry?.skip_reason || "").toLowerCase();
              if (String(entry?.status || "").toLowerCase() === "skipped" && skipReason === "no_devices_targeted") {
                return { ...entry, status: "No Devices Targeted" };
              }
              if (String(entry?.status || "").toLowerCase() === "skipped" && skipReason === "no_eligible_targets") {
                return { ...entry, status: "No Eligible Targets" };
              }
              return entry;
            })
          : []
      );
      const devices = Array.isArray(dev.devices) ? dev.devices.map((device) => ({
        ...device,
        activities: Array.isArray(device.activities) ? device.activities : [],
      })) : [];
      setDeviceRows(devices);
    } catch {
      setHistoryRows([]);
      setDeviceRows([]);
    }
  }, [editing, effectiveHistorySubTabKey, initialJob?.id, selectedHistoryOccurrence]);

  const activeConnectionProbeDeadlines = useMemo(
    () => [...historyRows, ...deviceRows]
      .filter((row) => normalizeJobStatusKey(row?.status || row?.job_status || "") === "establishing_connection")
      .map((row) => Number(row?.connection_probe_deadline_ts || 0))
      .filter((deadline) => Number.isFinite(deadline) && deadline > 0),
    [deviceRows, historyRows]
  );

  useEffect(() => {
    if (!editing || activeConnectionProbeDeadlines.length === 0) return undefined;
    const earliestDeadlineMs = Math.min(...activeConnectionProbeDeadlines) * 1000;
    const tick = () => {
      const now = Date.now();
      setHistoryClockMs(now);
      if (now >= earliestDeadlineMs && now - probeRefreshAtRef.current >= 1000) {
        probeRefreshAtRef.current = now;
        void loadHistory();
      }
    };
    tick();
    const timer = setInterval(tick, 1000);
    return () => clearInterval(timer);
  }, [activeConnectionProbeDeadlines, editing, loadHistory]);

  useEffect(() => {
    if (!editing) return;
    let t;
    (async () => { try { await loadHistory(); } catch {} })();
    t = setInterval(loadHistory, 10000);
    return () => { if (t) clearInterval(t); };
  }, [editing, loadHistory]);

  useEffect(() => {
    if (!editing || !initialJob?.id) return undefined;
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket || typeof socket.on !== "function") return undefined;
    const handlePatchProgress = (payload = {}) => {
      const jobId = String(payload?.scheduled_job_id || payload?.progress?.scheduled_job_id || "");
      if (jobId && jobId !== String(initialJob.id)) return;
      void loadHistory();
    };
    socket.on("scheduled_job_patch_progress", handlePatchProgress);
    return () => {
      socket.off("scheduled_job_patch_progress", handlePatchProgress);
    };
  }, [editing, initialJob?.id, loadHistory]);

  const clearJobHistory = useCallback(async () => {
    if (!editing || !initialJob?.id || clearingHistory) return;
    setClearingHistory(true);
    try {
      await fetch(`/api/scheduled_jobs/${initialJob.id}/runs`, { method: "DELETE" });
      setSelectedHistoryOccurrence(null);
      await loadHistory();
    } catch {
      // no-op; page poll will reconcile if the delete fails silently
    } finally {
      setClearingHistory(false);
    }
  }, [clearingHistory, editing, initialJob?.id, loadHistory]);

  const resultChip = useCallback((status, displayLabel = "", tooltip = "") => {
    const key = normalizeJobStatusKey(status);
    const theme = JOB_RESULT_THEME[key] || JOB_RESULT_THEME.default;
    const label = displayLabel || JOB_RESULT_THEME[key]?.label || status || "Status";
    const pill = <StatusPill label={label} theme={theme} preserveCase={key === "establishing_connection"} />;
    if (!tooltip) return pill;
    return (
      <Tooltip title={<span style={{ whiteSpace: "pre-line" }}>{tooltip}</span>}>
        <span>{pill}</span>
      </Tooltip>
    );
  }, []);

  const aggregatedHistory = useMemo(() => {
    if (!Array.isArray(historyRows) || historyRows.length === 0) return [];
    const map = new Map();
    historyRows.forEach((row) => {
      const key = row?.scheduled_ts || row?.started_ts || row?.finished_ts || row?.id;
      if (!key) return;
      const strKey = String(key);
      const existing = map.get(strKey) || {
        key: strKey,
        scheduled_ts: row?.scheduled_ts || null,
        started_ts: null,
        finished_ts: null,
        connection_probe_deadline_ts: null,
        statuses: new Set()
      };
      if (!existing.scheduled_ts && row?.scheduled_ts) existing.scheduled_ts = row.scheduled_ts;
      if (row?.started_ts) {
        existing.started_ts = existing.started_ts == null ? row.started_ts : Math.min(existing.started_ts, row.started_ts);
      }
      if (row?.finished_ts) {
        existing.finished_ts = existing.finished_ts == null ? row.finished_ts : Math.max(existing.finished_ts, row.finished_ts);
      }
      if (normalizeJobStatusKey(row?.status || "") === "establishing_connection" && row?.connection_probe_deadline_ts) {
        existing.connection_probe_deadline_ts = Math.max(
          Number(existing.connection_probe_deadline_ts || 0),
          Number(row.connection_probe_deadline_ts || 0)
        );
      }
      if (row?.status) existing.statuses.add(String(row.status));
      map.set(strKey, existing);
    });
    const summaries = [];
    map.forEach((entry) => {
      const statuses = Array.from(entry.statuses)
        .map((s) => normalizeJobStatusKey(s))
        .filter(Boolean);
      if (!statuses.length) return;
      const ranOn = entry.started_ts || entry.scheduled_ts || entry.finished_ts || Number(entry.key || 0);
      const status = summarizeHistoricalRunStatus(statuses);
      summaries.push({
        key: entry.key,
        ran_on: ranOn,
        scheduled_ts: entry.scheduled_ts,
        started_ts: entry.started_ts,
        finished_ts: entry.finished_ts,
        status,
        connection_probe_deadline_ts: entry.connection_probe_deadline_ts,
        status_label: connectionProbeStatusLabel(status, entry.connection_probe_deadline_ts, historyClockMs),
      });
    });
    return summaries;
  }, [historyClockMs, historyRows]);

  const sortedHistory = useMemo(() => {
    return [...aggregatedHistory].sort(
      (a, b) => Number(b?.ran_on || b?.finished_ts || 0) - Number(a?.ran_on || a?.finished_ts || 0)
    );
  }, [aggregatedHistory]);

  const openHistoricalRun = useCallback(
    (row) => {
      const occurrence = Number(row?.key || row?.scheduled_ts || row?.started_ts || row?.finished_ts || row?.ran_on || 0);
      if (!Number.isFinite(occurrence) || occurrence <= 0) return;
      setSelectedHistoryOccurrence(occurrence);
      setHistorySubTabKey("historical_run");
    },
    [setHistorySubTabKey]
  );

  const historySummaryComponents = useMemo(
    () => ({
      HistoryRanOnRenderer: (params) => (
        <Box
          component="a"
          href="#"
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            openHistoricalRun(params?.data);
          }}
          sx={{
            color: "#58a6ff",
            cursor: "pointer",
            font: "inherit",
            fontWeight: 700,
            textDecoration: "none",
            "&:hover": {
              color: "#a8d4ff",
              textDecoration: "none",
            },
          }}
        >
          {params.value ? fmtTs(params.value) : "-"}
        </Box>
      ),
      HistoryStatusRenderer: (params) => resultChip(params.value || "", params?.data?.status_label || ""),
    }),
    [fmtTs, openHistoricalRun, resultChip]
  );
  const historySummaryColumnDefs = useMemo(
    () => [
      {
        field: "ran_on",
        headerName: "Ran On",
        minWidth: 220,
        cellRenderer: "HistoryRanOnRenderer",
      },
      {
        field: "status",
        headerName: "Job Status",
        minWidth: JOB_STATUS_COLUMN_MIN_WIDTH,
        cellRenderer: "HistoryStatusRenderer",
        cellClass: "status-pill-cell",
        sortable: false,
        suppressHeaderMenuButton: true,
      },
    ],
    []
  );
  const historySummaryDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 1,
      cellClass: "auto-col-tight",
    }),
    []
  );
  const historySummaryGridApiRef = useRef(null);
  const HISTORY_SUMMARY_AUTO_COLS = useRef(["ran_on", "status"]);
  const handleHistorySummaryGridReady = useCallback((params) => {
    historySummaryGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(HISTORY_SUMMARY_AUTO_COLS.current, true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    if (!historySummaryGridApiRef.current) return;
    requestAnimationFrame(() => {
      try {
        historySummaryGridApiRef.current.autoSizeColumns(HISTORY_SUMMARY_AUTO_COLS.current, true);
      } catch {}
    });
  }, [sortedHistory]);

  const statusCounts = useMemo(() => buildJobHistoryStatusCounts(deviceRows), [deviceRows]);
  const activeStatusFilterLabel = useMemo(
    () => JOB_HISTORY_STATUS_FILTER_GROUPS
      .flatMap((group) => group.options)
      .find((option) => option.key === deviceStatusFilter)?.label || "",
    [deviceStatusFilter]
  );
  const statusFilterSummary = useMemo(() => {
    const count = jobHistoryGridRows.length;
    const noun = count === 1 ? "device" : "devices";
    return activeStatusFilterLabel
      ? `Showing ${count} ${noun} with ${activeStatusFilterLabel} status`
      : `Showing ${count} ${noun}`;
  }, [activeStatusFilterLabel, jobHistoryGridRows.length]);

  const inferLanguage = useCallback((path = "") => {
    const lower = String(path || "").toLowerCase();
    if (lower.endsWith(".ps1")) return "powershell";
    if (lower.endsWith(".bat")) return "batch";
    if (lower.endsWith(".sh")) return "bash";
    if (lower.endsWith(".yml") || lower.endsWith(".yaml")) return "yaml";
    return "powershell";
  }, []);

  const highlightCode = useCallback((code, lang) => {
    const raw = String(code || "");
    try {
      return Prism.highlight(raw, Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return raw
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;");
    }
  }, []);

  const loadActivity = useCallback(async (activityId) => {
    const idNum = Number(activityId || 0);
    if (!idNum) return null;
    if (activityCacheRef.current.has(idNum)) {
      return activityCacheRef.current.get(idNum);
    }
    try {
      const resp = await fetch(`/api/device/activity/job/${idNum}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      activityCacheRef.current.set(idNum, data);
      return data;
    } catch {
      return null;
    }
  }, []);

  const copyTextToClipboard = useCallback(async (value, promptTitle = "Copy text") => {
    const normalizedValue = String(value ?? "");
    if (!normalizedValue) {
      return false;
    }
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(normalizedValue);
        return true;
      }
      throw new Error("clipboard_unavailable");
    } catch {
      if (typeof window !== "undefined" && typeof window.prompt === "function") {
        window.prompt(promptTitle, normalizedValue);
      }
      return false;
    }
  }, []);

  const handleCopyOutputSection = useCallback(async (section) => {
    const content = String(section?.content ?? "");
    if (!content) {
      await sendNotification({
        title: "No Output Available",
        message: "This output panel does not contain any text to copy.",
        icon: "warning",
        variant: "warning",
      });
      return;
    }

    const sectionLabel = String(section?.title || outputTitle || "output").trim() || "output";
    const copied = await copyTextToClipboard(content, `Copy ${sectionLabel}`);
    if (copied) {
      setCopiedOutputKey(String(section?.key || ""));
      if (outputCopyResetRef.current) {
        clearTimeout(outputCopyResetRef.current);
      }
      outputCopyResetRef.current = setTimeout(() => {
        setCopiedOutputKey("");
        outputCopyResetRef.current = null;
      }, 1400);
      await sendNotification({
        title: "Output Copied",
        message: `Copied <b>${sectionLabel}</b> to the clipboard.`,
        icon: "done",
        variant: "info",
      });
      return;
    }

    await sendNotification({
      title: "Manual Copy Required",
      message: `Clipboard access was blocked, so Borealis opened a manual copy prompt for <b>${sectionLabel}</b>.`,
      icon: "warning",
      variant: "warning",
    });
  }, [copyTextToClipboard, outputTitle, sendNotification]);

  const loadDeviceOutputSections = useCallback(async (row, mode = "stdout") => {
    if (!row) return;
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const activities = Array.isArray(row.activities) ? row.activities : [];
    const relevant = activities.filter((act) => (mode === "stderr" ? act.has_stderr : act.has_stdout));
    if (!relevant.length) {
      return {
        label,
        hostname: row.hostname || "",
        sections: [],
        error: `No ${label} available for this device.`,
      };
    }
    const sections = [];
    for (const act of relevant) {
      const activityId = Number(act.activity_id || act.id || 0);
      if (!activityId) continue;
      const data = await loadActivity(activityId);
      if (!data) continue;
      const content = mode === "stderr" ? (data.stderr || "") : (data.stdout || "");
      const sectionTitle = act.component_name || data.script_name || data.script_path || `Activity ${activityId}`;
      sections.push({
        key: `${activityId}-${mode}`,
        title: sectionTitle,
        path: data.script_path || "",
        lang: inferLanguage(data.script_path || ""),
        content,
      });
    }
    return {
      label,
      hostname: row.hostname || "",
      sections,
      error: sections.length ? "" : `No ${label} available for this device.`,
    };
  }, [inferLanguage, loadActivity]);

  const handleViewDeviceOutput = useCallback(async (row, mode = "stdout") => {
    if (!row) return;
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    setOutputTitle(`${label} - ${row.hostname || ""}`);
    setOutputSections([]);
    setOutputError("");
    setOutputLoading(true);
    setCopiedOutputKey("");
    setOutputOpen(true);
    const result = await loadDeviceOutputSections(row, mode);
    setOutputSections(Array.isArray(result?.sections) ? result.sections : []);
    setOutputError(String(result?.error || ""));
    setOutputLoading(false);
  }, [loadDeviceOutputSections]);

  const handleCopyDeviceOutput = useCallback(async (row, mode = "stdout") => {
    if (!row) return;
    const result = await loadDeviceOutputSections(row, mode);
    const label = String(result?.label || (mode === "stderr" ? "StdErr" : "StdOut"));
    const hostname = String(result?.hostname || row?.hostname || "").trim();
    const sections = Array.isArray(result?.sections) ? result.sections : [];
    if (!sections.length) {
      await sendNotification({
        title: "No Output Available",
        message: result?.error || `No ${label} available for <b>${hostname || "this device"}</b>.`,
        icon: "warning",
        variant: "warning",
      });
      return;
    }

    const copyLabel = `${label}${hostname ? ` - ${hostname}` : ""}`;
    const combinedContent =
      sections.length === 1
        ? String(sections[0]?.content ?? "")
        : sections
            .map((section) => {
              const title = String(section?.title || "Output").trim() || "Output";
              const path = String(section?.path || "").trim();
              const heading = path ? `${title} (${path})` : title;
              return `===== ${heading} =====\n${String(section?.content ?? "")}`;
            })
            .join("\n\n");

    const copied = await copyTextToClipboard(combinedContent, `Copy ${copyLabel}`);
    if (copied) {
      await sendNotification({
        title: "Output Copied",
        message: `Copied <b>${copyLabel}</b> to the clipboard.`,
        icon: "done",
        variant: "info",
      });
      return;
    }

    await sendNotification({
      title: "Manual Copy Required",
      message: `Clipboard access was blocked, so Borealis opened a manual copy prompt for <b>${copyLabel}</b>.`,
      icon: "warning",
      variant: "warning",
    });
  }, [copyTextToClipboard, loadDeviceOutputSections, sendNotification]);

  const jobHistoryGridComponents = useMemo(
    () => ({
      DeviceStatusRenderer: (params) => {
        const online = Boolean(params.data?.online);
        const theme = online ? DEVICE_STATUS_THEME.online : DEVICE_STATUS_THEME.offline;
        return (
          <StatusPill
            label={online ? DEVICE_STATUS_THEME.online.label : DEVICE_STATUS_THEME.offline.label}
            theme={theme}
          />
        );
      },
      JobStatusRenderer: (params) => resultChip(params.data?.jobStatus || "", params.data?.jobStatusLabel || "", params.data?.jobStatusTooltip || ""),
      OutputActionsRenderer: (params) => {
        const row = params.data;
        if (!row) return null;
        return (
          <Box sx={{ display: "flex", alignItems: "center", gap: 0.75, justifyContent: "flex-start", width: "100%", flexWrap: "wrap" }}>
            {row.hasStdOut ? (
              <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                <Button
                  size="small"
                  sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }}
                  onClick={(e) => {
                    e.stopPropagation();
                    params.context?.viewOutput?.(row.raw, "stdout");
                  }}
                >
                  StdOut
                </Button>
                <Tooltip title="Copy StdOut">
                  <IconButton
                    size="small"
                    sx={{ color: MAGIC_UI.accentA, p: 0.35 }}
                    onClick={(e) => {
                      e.stopPropagation();
                      void params.context?.copyOutput?.(row.raw, "stdout");
                    }}
                  >
                    <ContentCopyIcon sx={{ fontSize: 15 }} />
                  </IconButton>
                </Tooltip>
              </Box>
            ) : null}
            {row.hasStdOut && row.hasStdErr ? (
              <Typography component="span" variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                /
              </Typography>
            ) : null}
            {row.hasStdErr ? (
              <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                <Button
                  size="small"
                  sx={{ color: "#fb7185", textTransform: "none", minWidth: 0, p: 0 }}
                  onClick={(e) => {
                    e.stopPropagation();
                    params.context?.viewOutput?.(row.raw, "stderr");
                  }}
                >
                  StdErr
                </Button>
                <Tooltip title="Copy StdErr">
                  <IconButton
                    size="small"
                    sx={{ color: "#fb7185", p: 0.35 }}
                    onClick={(e) => {
                      e.stopPropagation();
                      void params.context?.copyOutput?.(row.raw, "stderr");
                    }}
                  >
                    <ContentCopyIcon sx={{ fontSize: 15 }} />
                  </IconButton>
                </Tooltip>
              </Box>
            ) : null}
          </Box>
        );
      },
    }),
    [resultChip]
  );
  const jobHistoryGridColumnDefs = useMemo(
    () => [
      {
        field: "hostname",
        headerName: "Hostname",
        minWidth: 200,
        filter: "agTextColumnFilter",
        cellRenderer: (params) => {
          const hostname = String(params?.value || params?.data?.hostname || "").trim();
          if (!hostname) return null;
          const raw = params?.data?.raw || {};
          const deviceRouteId =
            raw.device_guid ||
            raw.guid ||
            raw.agent_guid ||
            params?.data?.deviceGuid ||
            params?.data?.device_guid ||
            hostname;
          const targetPath = APP_PATHS.device(deviceRouteId);
          const handleClick = (event) => {
            event.preventDefault();
            event.stopPropagation();
            navigate(targetPath, {
              state: {
                initialDevice: {
                  ...raw,
                  hostname,
                  site_id: raw.site_id ?? params?.data?.site_id ?? null,
                  site_name: raw.site_name || params?.data?.site || "",
                },
              },
            });
          };
          return (
            <a
              href={targetPath}
              onClick={handleClick}
              title={hostname}
              style={{ color: BOREALIS_BLUE, textDecoration: "none", fontWeight: 500 }}
            >
              {hostname}
            </a>
          );
        },
      },
      {
        field: "onlineLabel",
        headerName: "Status",
        minWidth: 140,
        cellRenderer: "DeviceStatusRenderer",
        cellClass: "status-pill-cell",
        filter: "agSetColumnFilter",
      },
      { field: "site", headerName: "Site", minWidth: 180, filter: "agTextColumnFilter" },
      {
        field: "ranOn",
        headerName: "Ran On",
        minWidth: 200,
        valueFormatter: (params) => (params.value ? fmtTs(params.value) : ""),
        comparator: (a, b) => Number(a || 0) - Number(b || 0),
        filter: "agTextColumnFilter",
      },
      {
        field: "jobStatusLabel",
        headerName: "Job Status",
        minWidth: JOB_STATUS_COLUMN_MIN_WIDTH,
        cellRenderer: "JobStatusRenderer",
        cellClass: "status-pill-cell",
        filter: "agSetColumnFilter",
        filterValueGetter: (params) => params.data?.jobStatusFilterLabel || params.data?.jobStatusLabel || "",
        comparator: (valueA, valueB, nodeA, nodeB) => {
          const rankA = nodeA?.data?.jobStatusSortRank ?? JOB_STATUS_SORT_RANK.default;
          const rankB = nodeB?.data?.jobStatusSortRank ?? JOB_STATUS_SORT_RANK.default;
          if (rankA !== rankB) {
            return rankA - rankB;
          }
          return String(valueA || "").localeCompare(String(valueB || ""));
        },
      },
      {
        field: "outputState",
        headerName: "StdOut / StdErr",
        minWidth: 220,
        flex: 1,
        cellRenderer: "OutputActionsRenderer",
        cellClass: "output-actions-cell",
        filter: "agSetColumnFilter",
      },
    ],
    [fmtTs, navigate]
  );
  const jobHistoryGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 0,
      cellClass: "auto-col-tight",
    }),
    []
  );
  const jobHistoryGridApiRef = useRef(null);
  const JOB_HISTORY_AUTO_COLS = useRef(["hostname", "onlineLabel", "site", "ranOn", "jobStatusLabel"]);
  const handleJobHistoryGridReady = useCallback((params) => {
    jobHistoryGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(JOB_HISTORY_AUTO_COLS.current, true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    const api = jobHistoryGridApiRef.current;
    if (!api) return;
    try {
      api.paginationGoToFirstPage();
    } catch {}
  }, [deviceStatusFilter]);
  useEffect(() => {
    if (!jobHistoryGridApiRef.current) return;
    requestAnimationFrame(() => {
      try {
        jobHistoryGridApiRef.current.autoSizeColumns(JOB_HISTORY_AUTO_COLS.current, true);
      } catch {}
    });
  }, [jobHistoryGridRows]);
  useEffect(() => {
    if (!jobHistoryGridApiRef.current) return;
    requestAnimationFrame(() => {
      try {
        jobHistoryGridApiRef.current.refreshCells({ columns: ["jobStatusLabel", "outputState"], force: true });
      } catch {}
    });
  }, [jobHistoryGridRows]);

  useEffect(() => {
    let canceled = false;
    const hydrate = async () => {
      if (initialJob && initialJob.id) {
        const formKey = String(initialJob.id);
        if (hydratedFormKeyRef.current === formKey) {
          return;
        }
        if (!hasHydratedJobPayload(resolvedInitialJob)) {
          return;
        }
        if (resolvedInitialJob.start_ts && !engineScheduleClock.loaded) {
          return;
        }

        hydratedFormKeyRef.current = formKey;
        setJobName(resolvedInitialJob.name || "");
        setPageTitleJobName(
          typeof resolvedInitialJob.name === "string" ? resolvedInitialJob.name.trim() : ""
        );
        setTargets(normalizeTargetList(resolvedInitialJob.targets || []));
        setJobKind(String(resolvedInitialJob.job_kind || resolvedInitialJob.kind || "automation").trim().toLowerCase() || "automation");
        setScheduleType(
          resolvedInitialJob.schedule_type || resolvedInitialJob.schedule?.type || "immediately"
        );
        setStartDateTime(
          resolvedInitialJob.start_ts
            ? dayjsFromEpochInTimezone(
                resolvedInitialJob.start_ts,
                engineTimezone,
                dayjs().add(5, "minute").second(0)
              )
            : resolvedInitialJob.schedule?.start
              ? dayjs(resolvedInitialJob.schedule.start).second(0)
              : dayjs().add(5, "minute").second(0)
        );
        setExpiration(resolvedInitialJob.expiration || "1h");
        setExecContext(resolvedInitialJob.execution_context || "system");
        setSelectedCredentialId(
          resolvedInitialJob.credential_id ? String(resolvedInitialJob.credential_id) : ""
        );
        if (isWinRMExecContext(resolvedInitialJob.execution_context || "")) {
          setUseSvcAccount(resolvedInitialJob.use_service_account !== false);
        } else {
          setUseSvcAccount(false);
        }
        const comps = Array.isArray(resolvedInitialJob.components)
          ? resolvedInitialJob.components
          : [];
        const hydrated = await hydrateExistingComponents(comps);
        if (!canceled) {
          setComponents(hydrated);
          setComponentVarErrors({});
        }
      } else if (!initialJob && hydratedFormKeyRef.current !== "new") {
        hydratedFormKeyRef.current = "new";
        setPageTitleJobName("");
        setJobKind("automation");
        setComponents([]);
        setComponentVarErrors({});
        setSelectedCredentialId("");
        setUseSvcAccount(true);
        setExpiration("1h");
        if (engineScheduleClock.loaded && !startDateTimeTouchedRef.current) {
          setStartDateTime(dayjsFromEngineClock(engineScheduleClock, 5 * 60, dayjs().add(5, "minute").second(0)));
        }
      }
    };
    hydrate();
    return () => {
      canceled = true;
    };
  }, [engineScheduleClock, engineTimezone, hydrateExistingComponents, initialJob, normalizeTargetList, resolvedInitialJob]);

  const openAddComponent = async () => {
    setAddCompOpen(true);
    setSelectedNodeId("");
    setAssemblyFilterText("");
    setAssemblyTypeFilterMode("");
    setAssemblyOsFilterMode("");
    if (!assembliesPayload.items.length && !assembliesLoading) {
      loadAssemblies();
    }
  };

  const addSelectedComponent = useCallback(async (recordOverride = null) => {
    const record = recordOverride || selectedAssemblyRecord;
    if (!record || !record.assemblyGuid) return false;
    try {
      const exportDoc = await loadAssemblyExport(record.assemblyGuid);
      const parsed = parseAssemblyExport(exportDoc);
      const nextKind = isWorkflowComponentRecord(parsed)
        ? "workflow"
        : isAnsibleComponentRecord(parsed) || record.kind === "ansible" || record.type === "ansible"
          ? "ansible"
          : record.kind === "workflow"
            ? "workflow"
            : "script";

      if (nextKind === "workflow") {
        const workflowDocument = extractWorkflowCanvasDocument(exportDoc);
        const workflowDiagnostics = inspectWorkflowRuntimeDocument({
          nodes: workflowDocument.nodes,
          edges: workflowDocument.edges,
          sourceType: "scheduled_job",
        });
        if (workflowDiagnostics.errors.length) {
          const intro = workflowDiagnostics.hasLegacyPortIssues
            ? "This workflow still uses older Borealis workflow wiring and must be repaired in the Flow Editor before a Scheduled Job can use it."
            : "This workflow does not meet the current Workflow Runtime v1 requirements for Scheduled Jobs.";
          alert([intro, "", ...workflowDiagnostics.errors].join("\n"));
          return false;
        }
        if (components.some((component) => isWorkflowComponentRecord(component))) {
          alert("Workflow-backed scheduled jobs currently support exactly one workflow component.");
          return false;
        }
        if (components.length) {
          alert("Workflow-backed scheduled jobs cannot mix workflows with script or Ansible assemblies. Remove the existing assemblies first.");
          return false;
        }
      } else if (components.some((component) => isWorkflowComponentRecord(component))) {
        alert("Remove the workflow component before adding script or Ansible assemblies to this job.");
        return false;
      }

      const existingDomains = new Set(
        components
          .map((component) => componentExecutionDomain(component))
          .filter((domain) => domain && domain !== "workflow")
      );
      if ((nextKind === "script" || nextKind === "ansible") && existingDomains.size && !existingDomains.has(nextKind)) {
        alert("Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. Remove the cross-domain assemblies or split them into separate jobs.");
        return false;
      }

      const docVars = Array.isArray(parsed.rawVariables) ? parsed.rawVariables : [];
      const mergedVariables = mergeComponentVariables(docVars, [], {});
      const type = nextKind;
      const subtype = nextKind === "workflow" ? "workflow" : parsed.type || record.type || (type === "ansible" ? "ansible" : "powershell");
      const normalizedPath = nextKind === "workflow"
        ? (record.path || record.displayName || "")
        : normalizeAssemblyPath(type, record.path || "", record.displayName);
      setComponents((prev) => [
        ...prev,
        {
          type,
          assembly_type: type,
          assembly_subtype: subtype,
          path: normalizedPath,
          name: record.displayName,
          description: record.summary || normalizedPath || record.displayName,
          variables: mergedVariables,
          localId: generateLocalId(),
          assembly_guid: record.assemblyGuid,
          assemblyGuid: record.assemblyGuid,
          domain: record.domain,
          domainLabel: record.domainLabel
        }
      ]);
      setSelectedNodeId("");
      return true;
    } catch (err) {
      console.error("Failed to load assembly export:", err);
      alert(err?.message || "Failed to load assembly details.");
      return false;
    }
  }, [selectedAssemblyRecord, components, loadAssemblyExport, mergeComponentVariables, normalizeAssemblyPath, generateLocalId]);

  const handleAssemblyRowClick = useCallback((event) => {
    const record = event?.data?.record;
    if (!record?.assemblyGuid) return;
    setSelectedNodeId((record.assemblyGuid || "").toLowerCase());
  }, []);

  const handleAssemblyRowDoubleClick = useCallback(
    async (event) => {
      const record = event?.data?.record;
      if (!record) return;
      setSelectedNodeId((record.assemblyGuid || "").toLowerCase());
      await addSelectedComponent(record);
    },
    [addSelectedComponent]
  );
  const handleAssemblySelectionChanged = useCallback((event) => {
    const selectedNode = event.api.getSelectedNodes()[0];
    if (selectedNode?.data?.record?.assemblyGuid) {
      setSelectedNodeId(selectedNode.data.record.assemblyGuid.toLowerCase());
    } else {
      setSelectedNodeId("");
    }
  }, []);
  const syncAssemblySelection = useCallback(() => {
    if (!assemblyGridApiRef.current) return;
    const targetId = String(selectedNodeId || "").toLowerCase();
    assemblyGridApiRef.current.forEachNode((node) => {
      const guid = String(node.data?.record?.assemblyGuid || "").toLowerCase();
      node.setSelected(Boolean(targetId) && guid === targetId);
    });
  }, [selectedNodeId]);
  useEffect(() => {
    syncAssemblySelection();
  }, [syncAssemblySelection, filteredAssemblyRows]);

  const openAddTargets = async () => {
    setAddTargetOpen(true);
    setTargetPickerTab("devices");
    setSelectedDeviceTargets({});
    setSelectedFilterTargets({});
    setSelectedFilterRows([]);
    loadFilterCatalog();
    try {
      const resp = await fetch("/api/devices");
      if (resp.ok) {
        const data = await resp.json();
        const rawDevices = Array.isArray(data?.devices) ? data.devices : [];
        const list = rawDevices.map((a) => ({
          hostname: a.hostname || a.agent_hostname || a.id || "unknown",
          display: a.hostname || a.agent_hostname || a.id || "unknown",
          online: String(a.status || "").trim().toLowerCase() === "online" || String(a.status || "").trim().toLowerCase() === "active",
          site: a.site || a.site_name || a.site_scope || a.group || "",
          siteId: a.site_id ?? null,
          site_id: a.site_id ?? null,
          site_name: a.site_name || a.site || "",
          deviceGuid: a.device_guid || a.guid || a.agent_guid || "",
          device_guid: a.device_guid || a.guid || a.agent_guid || "",
          connection_type: a.connection_type || "",
          os:
            a.os ||
            a.operating_system ||
            a.platform ||
            a.agent_os ||
            a.system ||
            a.os_name ||
            (a.summary && (a.summary.os || a.summary.operating_system)) ||
            "",
        }));
        list.sort((a, b) => a.display.localeCompare(b.display));
        setAvailableDevices(list);
      } else {
        setAvailableDevices([]);
      }
    } catch {
      setAvailableDevices([]);
    }
  };

  const handleCreate = async () => {
    const workflowComponents = components.filter((component) => isWorkflowComponentRecord(component));
    const componentDomains = new Set(
      components
        .map((component) => componentExecutionDomain(component))
        .filter(Boolean)
    );
    if (!isPatchJob && workflowComponents.length > 1) {
      alert("Workflow-backed scheduled jobs currently support exactly one workflow component.");
      return;
    }
    if (!isPatchJob && workflowComponents.length === 1 && workflowComponents.length !== components.length) {
      alert("Workflow-backed scheduled jobs cannot mix workflow, script, or Ansible components.");
      return;
    }
    if (!isPatchJob && componentDomains.has("script") && componentDomains.has("ansible")) {
      alert("Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. Remove the cross-domain assemblies or split them into separate jobs.");
      return;
    }
    const workflowMode = !isPatchJob && workflowComponents.length === 1;
    const requiresAnsibleOnly = !workflowMode && ANSIBLE_EXEC_CONTEXTS.includes(execContext);
    if (!isPatchJob && requiresAnsibleOnly) {
      const hasNonAnsibleComponent = components.some((component) => !isAnsibleComponentRecord(component));
      if (hasNonAnsibleComponent) {
        alert("Jobs using SSH or WinRM execution contexts must contain only Ansible playbook assemblies.");
        return;
      }
    }
    const requiresScriptOnly = !workflowMode && SCRIPT_EXEC_CONTEXTS.includes(execContext);
    if (!isPatchJob && requiresScriptOnly) {
      const hasNonScriptComponent = components.some(
        (component) => isWorkflowComponentRecord(component) || isAnsibleComponentRecord(component)
      );
      if (hasNonScriptComponent) {
        alert("Jobs using agent execution contexts must contain only script assemblies.");
        return;
      }
    }
    if (!isPatchJob && !workflowMode && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount) && !selectedCredentialId) {
      alert("Please select a credential for this execution context.");
      return;
    }
    const requiredErrors = {};
    components.forEach((comp) => {
      if (!comp || !comp.localId) return;
      (Array.isArray(comp.variables) ? comp.variables : []).forEach((variable) => {
        if (!variable || !variable.name || !variable.required) return;
        if ((variable.type || "string") === "boolean") return;
        const value = variable.value;
        if (value == null || value === "") {
          if (!requiredErrors[comp.localId]) requiredErrors[comp.localId] = {};
          requiredErrors[comp.localId][variable.name] = "Required";
        }
      });
    });
    if (Object.keys(requiredErrors).length) {
      setComponentVarErrors(requiredErrors);
      selectTabKey("components");
      alert("Please fill in all required variable values.");
      return;
    }
    setComponentVarErrors({});
    if (isPatchBatchJob) {
      const schedulePayload = {
        type: scheduleType,
        start: scheduleType !== "immediately" ? wallClockStringFromEnginePickerValue(startDateTime) : null,
      };
      const durationPayload = { stopAfterEnabled: expiration !== "no_expire", expiration };
      let createdCount = 0;
      try {
        for (const item of patchJobBatch.items) {
          const patch = item.patch && typeof item.patch === "object" ? item.patch : {};
          const patchLabel = String(patch.kb || patch.title || patch.patch_key || item.name || "Patch").trim();
          const payload = {
            name: item.job_name || `[${item.trigger_label || "Bulk Ad-Hoc Install"}] - ${patchLabel}`,
            components: [{
              kind: "patch_install",
              type: "patch_install",
              component_type: "patch_install",
              assembly_type: "patch_install",
              name: patchLabel,
              patch,
              trigger: item.trigger || patchJobBatch.trigger || "bulk_ad_hoc",
              source: item.source || patchJobBatch.source || "patch_management",
            }],
            targets: serializeTargetsForSave(item.targets),
            schedule: schedulePayload,
            duration: durationPayload,
            execution_context: "system",
            credential_id: null,
            use_service_account: false,
            job_kind: "patch_install",
          };
          const resp = await fetch("/api/scheduled_jobs", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload),
          });
          const data = await resp.json().catch(() => ({}));
          if (!resp.ok) throw new Error(data.error || data.message || `HTTP ${resp.status}`);
          createdCount += 1;
        }
        sendNotification(`${createdCount.toLocaleString()} Patch Install Jobs Created Successfully`);
        navigate(resolvePatchJobReturnPath({ current: patchJobReturnPath, batch: patchJobBatch, draft: patchJobDraft }) || APP_PATHS.patchManagementWindows);
      } catch (err) {
        const prefix = createdCount > 0 ? `${createdCount.toLocaleString()} job(s) were created before failure. ` : "";
        alert(`${prefix}${String(err.message || err)}`);
      }
      return;
    }
    const payloadComponents = sanitizeComponentsForSave(components);
    const payload = {
      name: jobName,
      components: payloadComponents,
      targets: workflowMode ? [] : serializeTargetsForSave(targets),
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? wallClockStringFromEnginePickerValue(startDateTime) : null },
      duration: { stopAfterEnabled: expiration !== "no_expire", expiration },
      execution_context: workflowMode || isPatchJob ? "system" : execContext,
      credential_id: workflowMode || isPatchJob ? null : (remoteExec && !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null),
      use_service_account: workflowMode || isPatchJob ? false : (isWinRMExecContext(execContext) ? Boolean(useSvcAccount) : false),
      job_kind: isPatchJob ? "patch_install" : "automation"
    };
    try {
      const resp = await fetch(initialJob && initialJob.id ? `/api/scheduled_jobs/${initialJob.id}` : "/api/scheduled_jobs", {
        method: initialJob && initialJob.id ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      const savedJob = data.job || payload;
      if (!(initialJob && initialJob.id)) {
        const createdName = savedJob?.name || jobName || "Job";
        sendNotification(`Job ${createdName} Created Successfully`);
      }
      navigate(
        isPatchJob && !(initialJob && initialJob.id)
          ? (resolvePatchJobReturnPath({ current: patchJobReturnPath, batch: patchJobBatch, draft: patchJobDraft }) || APP_PATHS.patchManagementWindows)
          : APP_PATHS.jobs
      );
    } catch (err) {
      alert(String(err.message || err));
    }
  };

  const tabDefs = useMemo(() => {
    if (isPatchJob) {
      const patchTabs = [{ key: "schedule", label: "Schedule", icon: ScheduleRoundedIcon }];
      if (editing) patchTabs.push({ key: "history", label: "Job History", icon: HistoryRoundedIcon });
      return patchTabs;
    }
    const base = [];
    base.push({ key: "name", label: "Job Name", icon: DriveFileRenameOutlineIcon });
    base.push({ key: "components", label: "Assemblies", icon: AppsIcon });
    if (!isWorkflowJob) {
      base.push({ key: "targets", label: "Targets", icon: DevicesRoundedIcon });
    }
    base.push({ key: "schedule", label: "Schedule", icon: ScheduleRoundedIcon });
    if (!isWorkflowJob) {
      base.push({ key: "context", label: "Execution Context", icon: SettingsApplicationsRoundedIcon });
    }
    if (editing) base.push({ key: "history", label: "Job History", icon: HistoryRoundedIcon });
    return base;
  }, [editing, isPatchJob, isWorkflowJob]);
  const tabDefKeys = useMemo(() => tabDefs.map((tabDef) => tabDef.key), [tabDefs]);
  const { activeKey: activeTabUrlKey, setActiveKey: setActiveTabUrlKey } = useUrlTabState({
    param: "tab",
    defaultKey: tabDefs[0]?.key || "name",
    allowedKeys: tabDefKeys,
    keyByUrl: CREATE_JOB_TAB_KEY_BY_URL,
    urlByKey: CREATE_JOB_TAB_URL_BY_KEY,
  });
  const activeTabKey = useMemo(() => {
    const fallbackKey = tabDefs[0]?.key || "name";
    return tabDefs.some((tabDef) => tabDef.key === activeTabUrlKey) ? activeTabUrlKey : fallbackKey;
  }, [activeTabUrlKey, tabDefs]);
  const tab = useMemo(() => {
    const index = tabDefs.findIndex((tabDef) => tabDef.key === activeTabKey);
    return index >= 0 ? index : 0;
  }, [activeTabKey, tabDefs]);
  const selectTabKey = useCallback(
    (nextTabKey) => {
      const normalizedKey = String(nextTabKey || "").trim();
      if (!normalizedKey) {
        return;
      }
      if (!tabDefs.some((tabDef) => tabDef.key === normalizedKey)) {
        return;
      }
      if (normalizedKey === activeTabKey) {
        return;
      }
      setActiveTabUrlKey(normalizedKey);
    },
    [activeTabKey, setActiveTabUrlKey, tabDefs]
  );

  const handleRerunJob = useCallback(async () => {
    if (!editing || !initialJob?.id || rerunningJob) return;
    setRerunningJob(true);
    try {
      const resp = await fetch(`/api/scheduled_jobs/${encodeURIComponent(initialJob.id)}/rerun`, {
        method: "POST",
        credentials: "include",
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(body?.message || body?.error || `HTTP ${resp.status}`);
      }
      sendNotification(`Job ${jobName || initialJob.id} Re-Run Queued`);
      setSelectedHistoryOccurrence(null);
      await loadHistory();
    } catch (err) {
      alert(String(err?.message || err || "Unable to re-run job"));
    } finally {
      setRerunningJob(false);
    }
  }, [editing, initialJob?.id, jobName, loadHistory, rerunningJob, sendNotification]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "scheduled-job-cancel",
        label: "Cancel",
        tone: "secondary",
        onClick: () => navigate(APP_PATHS.jobs),
      },
      {
        id: "scheduled-job-clear-history",
        label: "Clear Job History",
        icon: <HistoryRoundedIcon />,
        tone: "secondary",
        disabled: !editing || clearingHistory,
        loading: clearingHistory,
        onClick: clearJobHistory,
      },
      ...(editing
        ? [{
            id: "scheduled-job-rerun",
            label: "Re-Run Job",
            icon: <PlayArrowIcon />,
            tone: "primary",
            disabled: resolvedInitialJob?.enabled === false || rerunningJob,
            loading: rerunningJob,
            onClick: handleRerunJob,
          }]
        : []),
      {
        id: "scheduled-job-save",
        label: editing ? "Save Changes" : isPatchBatchJob ? "Create Jobs" : "Create Job",
        icon: editing ? <CheckIcon /> : <AddIcon />,
        tone: "primary",
        disabled: !isValid,
        onClick: () => {
          if (isValid) {
            setConfirmOpen(true);
          }
        },
      },
    ],
    [clearJobHistory, clearingHistory, editing, handleRerunJob, isPatchBatchJob, isValid, navigate, rerunningJob, resolvedInitialJob?.enabled]
  );

  useRoutePageChrome({
    title: resolvedPageTitle,
    subtitle: resolvedPageSubtitle,
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  useEffect(() => {
    if (editing) return;
    if (!quickJobDraft || !quickJobDraft.id) return;
    if (quickDraftAppliedRef.current === quickJobDraft.id) return;
    quickDraftAppliedRef.current = quickJobDraft.id;
    const incoming = Array.isArray(quickJobDraft.hostnames) ? quickJobDraft.hostnames : [];
    const normalizedTargets = normalizeTargetList(incoming);
    setTargets(normalizedTargets);
    setSelectedDeviceTargets({});
    setSelectedFilterTargets({});
    setComponents([]);
    setComponentVarErrors({});
    const normalizedSchedule = String(quickJobDraft.scheduleType || "immediately").trim().toLowerCase() || "immediately";
    setScheduleType(normalizedSchedule);
    const placeholderAssembly = (quickJobDraft.placeholderAssemblyLabel || "Choose Assembly").trim() || "Choose Assembly";
    const defaultDeviceLabel = normalizedTargets[0]?.hostname || incoming[0] || "Selected Device";
    const deviceLabel = (quickJobDraft.deviceLabel || defaultDeviceLabel).trim() || "Selected Device";
    const initialName = `Quick Job - ${placeholderAssembly} - ${deviceLabel}`;
    setJobName(initialName);
    setPageTitleJobName(initialName.trim());
    setQuickJobMeta({
      id: quickJobDraft.id,
      deviceLabel,
      allowAutoRename: true,
      currentAutoName: initialName
    });
    const targetTabKey = quickJobDraft.initialTabKey || "components";
    if (tabDefs.some((tabDef) => tabDef.key === targetTabKey)) {
      selectTabKey(targetTabKey);
    } else if (tabDefs.length > 1) {
      selectTabKey(tabDefs[1]?.key || tabDefs[0]?.key || "name");
    }
    setQuickJobDraft((previous) => {
      if (!previous) return previous;
      return previous.id === quickJobDraft.id ? null : previous;
    });
  }, [editing, normalizeTargetList, quickJobDraft, selectTabKey, tabDefs]);

  useEffect(() => {
    if (editing) return;
    if (!patchJobDraft || !patchJobDraft.id) return;
    if (quickDraftAppliedRef.current === patchJobDraft.id) return;
    quickDraftAppliedRef.current = patchJobDraft.id;
    const triggerLabel = String(patchJobDraft.trigger_label || "Ad-Hoc Install").trim() || "Ad-Hoc Install";
    const normalizedReturnTo = resolvePatchJobReturnPath({ draft: patchJobDraft });
    setPatchJobReturnTo(normalizedReturnTo);
    const rawItems = Array.isArray(patchJobDraft.items) ? patchJobDraft.items : [];
    if (rawItems.length > 1 || patchJobDraft.bulk) {
      const normalizedItems = rawItems
        .map((item, index) => {
          const patch = item?.patch && typeof item.patch === "object" ? item.patch : {};
          const itemTargets = normalizeTargetList(Array.isArray(item?.targets) ? item.targets : []);
          const patchLabel = String(patch.kb || patch.title || patch.patch_key || "Patch").trim();
          const title = String(patch.title || patchLabel).trim();
          const count = Number(item?.target_count || itemTargets.length || 0) || 0;
          return {
            id: String(item?.id || `patch-bulk-item-${index}`),
            patch,
            targets: itemTargets,
            target_count: count,
            trigger: String(item?.trigger || patchJobDraft.trigger || "bulk_ad_hoc").trim() || "bulk_ad_hoc",
            trigger_label: String(item?.trigger_label || triggerLabel).trim() || triggerLabel,
            source: String(item?.source || patchJobDraft.source || "patch_management").trim() || "patch_management",
            name: patchLabel,
            job_name: String(
              item?.job_name ||
                `[${triggerLabel}] - ${patchLabel} - ${title} - ${count.toLocaleString()} ${count === 1 ? "Device" : "Devices"}`
            ).trim(),
          };
        })
        .filter((item) => item.targets.length > 0 && item.patch && (item.patch.patch_key || item.patch.kb || item.patch.title));
      if (normalizedItems.length > 1) {
        const firstItem = normalizedItems[0];
        const targetsByKey = new Map();
        normalizedItems.forEach((item) => {
          item.targets.forEach((target) => {
            const key = String(target.device_guid || target.hostname || JSON.stringify(target)).trim().toLowerCase();
            if (key && !targetsByKey.has(key)) targetsByKey.set(key, target);
          });
        });
        const unionTargets = Array.from(targetsByKey.values());
        const initialName = `Bulk Patch Install - ${normalizedItems.length.toLocaleString()} Updates`;
        setPatchJobBatch({
          id: patchJobDraft.id,
          source: String(patchJobDraft.source || "patch_management").trim() || "patch_management",
          trigger: String(patchJobDraft.trigger || "bulk_ad_hoc").trim() || "bulk_ad_hoc",
          trigger_label: triggerLabel,
          return_to: normalizedReturnTo,
          items: normalizedItems,
        });
        setJobKind("patch_install");
        setTargets(unionTargets);
        setSelectedDeviceTargets({});
        setSelectedFilterTargets({});
        setComponents([
          {
            kind: "patch_install",
            type: "patch_install",
            name: firstItem.name,
            patch: firstItem.patch,
            trigger: firstItem.trigger,
            source: firstItem.source,
            localId: `patch-install-${patchJobDraft.id}`,
          },
        ]);
        setComponentVarErrors({});
        setScheduleType(String(patchJobDraft.scheduleType || "immediately").trim().toLowerCase() || "immediately");
        setExpiration(String(patchJobDraft.expiration || "2h").trim() || "2h");
        setExecContext("system");
        setSelectedCredentialId("");
        setUseSvcAccount(false);
        setJobName(initialName);
        setPageTitleJobName(initialName);
        selectTabKey("schedule");
        setPatchJobDraft((previous) => {
          if (!previous) return previous;
          return previous.id === patchJobDraft.id ? null : previous;
        });
        return;
      }
    }
    setPatchJobBatch(null);
    const incomingTargets = Array.isArray(patchJobDraft.targets)
      ? patchJobDraft.targets
      : Array.isArray(patchJobDraft.hostnames)
        ? patchJobDraft.hostnames
        : [];
    const normalizedTargets = normalizeTargetList(incomingTargets);
    const patch = patchJobDraft.patch && typeof patchJobDraft.patch === "object" ? patchJobDraft.patch : {};
    const patchLabel = String(patch.kb || patch.title || patch.patch_key || "Patch").trim();
    const title = String(patch.title || patchLabel).trim();
    const count = Number(patchJobDraft.target_count || normalizedTargets.length || 0) || 0;
    const initialName = String(
      patchJobDraft.job_name ||
        `[${triggerLabel}] ${patchLabel} - ${title} - ${count.toLocaleString()} ${count === 1 ? "Device" : "Devices"}`
    ).trim();
    setJobKind("patch_install");
    setTargets(normalizedTargets);
    setSelectedDeviceTargets({});
    setSelectedFilterTargets({});
    setComponents([
      {
        kind: "patch_install",
        type: "patch_install",
        name: patchLabel,
        patch,
        trigger: String(patchJobDraft.trigger || "ad_hoc").trim() || "ad_hoc",
        source: String(patchJobDraft.source || "patch_management").trim() || "patch_management",
        localId: `patch-install-${patchJobDraft.id}`,
      },
    ]);
    setComponentVarErrors({});
    setScheduleType(String(patchJobDraft.scheduleType || "immediately").trim().toLowerCase() || "immediately");
    setExpiration(String(patchJobDraft.expiration || "2h").trim() || "2h");
    setExecContext("system");
    setSelectedCredentialId("");
    setUseSvcAccount(false);
    setJobName(initialName);
    setPageTitleJobName(initialName);
    selectTabKey("schedule");
    setPatchJobDraft((previous) => {
      if (!previous) return previous;
      return previous.id === patchJobDraft.id ? null : previous;
    });
  }, [editing, normalizeTargetList, patchJobDraft, selectTabKey]);

  useEffect(() => {
    if (!quickJobMeta?.allowAutoRename) return;
    if (!primaryComponentName) return;
    const deviceLabel = quickJobMeta.deviceLabel || "Selected Device";
    const newName = `Quick Job - ${primaryComponentName} - ${deviceLabel}`;
    if (jobName === newName) return;
    setJobName(newName);
    setPageTitleJobName(newName.trim());
    setQuickJobMeta((prev) => {
      if (!prev) return prev;
      if (!prev.allowAutoRename) return prev;
      return { ...prev, currentAutoName: newName };
    });
  }, [primaryComponentName, quickJobMeta, jobName]);

  return (
    <Box
      sx={{
        m: 0,
        p: { xs: 2, md: 3 },
        flexGrow: 1,
        minWidth: 0,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        gap: 3,
        borderRadius: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
      }}
    >
      <Tabs
        value={tab}
        onChange={(_, v) => selectTabKey(tabDefs[v]?.key || tabDefs[0]?.key || "name")}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: NAV_TAB_COLORS.iconActive,
          },
        }}
        sx={buildNavTabsSx()}
      >
        {tabDefs.map((t) => (
          <Tab
            key={t.key}
            label={t.label}
            icon={t.icon ? <t.icon sx={{ fontSize: 18 }} /> : undefined}
            iconPosition="start"
          />
        ))}
      </Tabs>

      <Box sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column", gap: 3 }}>
        {activeTabKey === "name" && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Job Name" />
          <TextField
            fullWidth
            sx={{ width: { xs: "100%", md: "60%" }, maxWidth: 540, ...INPUT_FIELD_SX }}
            placeholder="Example Job Name"
            value={jobName}
            onChange={(e) => handleJobNameInputChange(e.target.value)}
            onBlur={(e) => setPageTitleJobName(e.target.value.trim())}
            InputLabelProps={{ shrink: true }}
            error={jobName.trim().length === 0}
            helperText={jobName.trim().length === 0 ? "Job name is required" : ""}
            inputProps={{ sx: { py: 0.9 } }}
          />
        </Box>
        )}

        {activeTabKey === "components" && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader
              title="Assemblies"
              action={
                <Button
                  startIcon={<AddIcon />}
                  onClick={openAddComponent}
                  sx={PRIMARY_CTA_FLAT_SX}
                >
                  Add Assembly
                </Button>
              }
            />
            {isWorkflowJob ? (
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mb: 1.5 }}>
                Workflow-backed scheduled jobs run one saved workflow and report the workflow's final status back into job history. Scheduler-level targets and execution context are ignored for this job type.
              </Typography>
            ) : null}
            {components.length === 0 && (
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mb: 1 }}>
                No assemblies added yet.
              </Typography>
            )}
            {components.map((c) => (
              <ComponentCard
                key={c.localId || `${c.type}-${c.path}`}
                comp={c}
                onRemove={removeComponent}
                onVariableChange={updateComponentVariable}
                errors={componentVarErrors[c.localId] || {}}
              />
            ))}
            {components.length === 0 && (
              <Typography variant="caption" sx={{ color: "#f87171" }}>
                At least one assembly is required.
              </Typography>
            )}
          </Box>
        )}

        {activeTabKey === "targets" && (
          <Box sx={{ ...TAB_SECTION_SX, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column", gap: 1.5 }}>
            <SectionHeader
              title="Targets"
              action={
                <Button
                  size="small"
                  startIcon={<AddIcon />}
                  onClick={openAddTargets}
                  sx={PRIMARY_CTA_FLAT_SX}
                >
                  Add Target
                </Button>
              }
            />
            <Box
              className={gridThemeClass}
              sx={{
                ...GRID_PANEL_SX,
                flexGrow: 1,
                minHeight: 420,
                height: "100%",
                maxHeight: "100%",
              }}
            >
              <AgGridReact
                rowData={targetGridRows}
                columnDefs={targetGridColumnDefs}
                defaultColDef={targetGridDefaultColDef}
                components={targetGridComponents}
                context={{ removeTarget }}
                suppressCellFocus
                headerHeight={44}
                rowHeight={48}
                pagination
                paginationPageSize={20}
                paginationPageSizeSelector={[20, 50, 100]}
                overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No targets selected.</span>"
                getRowId={(params) => params.data?.id || params.rowIndex}
                onGridReady={handleTargetGridReady}
                theme={gridTheme}
              />
            </Box>
            {targets.length === 0 && (
              <Typography variant="caption" sx={{ color: "#f87171" }}>At least one target is required.</Typography>
            )}
          </Box>
        )}

        {activeTabKey === "schedule" && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Schedule" />
            {patchJobSummary ? (
              patchJobSummary.bulk ? (
                <Box
                  sx={{
                    mb: 2,
                    display: "flex",
                    flexDirection: "column",
                    gap: 0.75,
                    maxHeight: 180,
                    overflow: "auto",
                    pr: 0.5,
                  }}
                >
                  {patchJobSummary.items.map((item, index) => (
                    <Box
                      key={`${item.kb}-${item.title}-${index}`}
                      sx={{
                        display: "grid",
                        gridTemplateColumns: { xs: "1fr", md: "140px minmax(220px, 1fr) 130px" },
                        gap: 1,
                        alignItems: "center",
                        color: MAGIC_UI.textBright,
                        borderBottom: "1px solid rgba(148,163,184,0.12)",
                        pb: 0.65,
                      }}
                    >
                      <Typography sx={{ fontSize: 13, fontWeight: 700, color: BOREALIS_BLUE }}>
                        {item.kb}
                      </Typography>
                      <Typography sx={{ fontSize: 13, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {item.title}
                      </Typography>
                      <Typography sx={{ fontSize: 13, color: MAGIC_UI.textMuted }}>
                        {item.targetCount.toLocaleString()} {item.targetCount === 1 ? "Device" : "Devices"}
                      </Typography>
                    </Box>
                  ))}
                </Box>
              ) : (
                <Box
                  sx={{
                    mb: 2,
                    display: "grid",
                    gridTemplateColumns: { xs: "1fr", md: "140px minmax(220px, 1fr) 130px" },
                    gap: 1,
                    alignItems: "center",
                    color: MAGIC_UI.textBright,
                  }}
                >
                  <Typography sx={{ fontSize: 13, fontWeight: 700, color: BOREALIS_BLUE }}>
                    {patchJobSummary.kb}
                  </Typography>
                  <Typography sx={{ fontSize: 13, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {patchJobSummary.title}
                  </Typography>
                  <Typography sx={{ fontSize: 13, color: MAGIC_UI.textMuted }}>
                    {patchJobSummary.targetCount.toLocaleString()} {patchJobSummary.targetCount === 1 ? "Device" : "Devices"}
                  </Typography>
                </Box>
              )
            ) : null}
            <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
              <TextField
                select
                size="small"
                label="Recurrence"
                value={scheduleType}
                onChange={(e) => setScheduleType(e.target.value)}
                sx={{ minWidth: 240, flex: "1 1 260px", ...SELECT_FIELD_SX }}
                SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
              >
                <MenuItem value="immediately">Immediately</MenuItem>
                <MenuItem value="once">At selected date and time</MenuItem>
                {!isPatchJob ? [
                  <MenuItem key="every_5_minutes" value="every_5_minutes">Every 5 Minutes</MenuItem>,
                  <MenuItem key="every_10_minutes" value="every_10_minutes">Every 10 Minutes</MenuItem>,
                  <MenuItem key="every_15_minutes" value="every_15_minutes">Every 15 Minutes</MenuItem>,
                  <MenuItem key="every_30_minutes" value="every_30_minutes">Every 30 Minutes</MenuItem>,
                  <MenuItem key="every_hour" value="every_hour">Every Hour</MenuItem>,
                  <MenuItem key="daily" value="daily">Daily</MenuItem>,
                  <MenuItem key="weekly" value="weekly">Weekly</MenuItem>,
                  <MenuItem key="monthly" value="monthly">Monthly</MenuItem>,
                  <MenuItem key="yearly" value="yearly">Yearly</MenuItem>,
                ] : null}
              </TextField>
              {scheduleType !== "immediately" && (
                <LocalizationProvider dateAdapter={AdapterDayjs}>
                  <DateTimePicker
                    value={startDateTime}
                    onChange={(val) => {
                      startDateTimeTouchedRef.current = true;
                      setStartDateTime(val?.second ? val.second(0) : val);
                    }}
                    views={["year", "month", "day", "hours", "minutes"]}
                    format="YYYY-MM-DD hh:mm A"
                    slotProps={{
                      textField: {
                        size: "small",
                        sx: { minWidth: 260, flex: "1 1 280px", ...INPUT_FIELD_SX },
                      },
                    }}
                  />
                </LocalizationProvider>
              )}
            </Box>
            <Box sx={{ mt: 1.5, display: "flex", flexDirection: "column", gap: 1, maxWidth: 560 }}>
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                Jobs expire after one hour by default. Choose a different expiration window here, or select
                {" "}
                <Box component="span" sx={{ color: MAGIC_UI.textBright }}>
                  Does not Expire
                </Box>
                {" "}
                to let the job run indefinitely.
              </Typography>
              <TextField
                select
                size="small"
                label="Expiration"
                value={expiration}
                onChange={(e) => setExpiration(e.target.value)}
                sx={{ maxWidth: 260, ...SELECT_FIELD_SX }}
                SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
              >
                <MenuItem value="no_expire">Does not Expire</MenuItem>
                <MenuItem value="30m">30 Minutes</MenuItem>
                <MenuItem value="1h">1 Hour</MenuItem>
                <MenuItem value="2h">2 Hours</MenuItem>
                <MenuItem value="6h">6 Hours</MenuItem>
                <MenuItem value="12h">12 Hours</MenuItem>
                <MenuItem value="1d">1 Day</MenuItem>
                <MenuItem value="2d">2 Days</MenuItem>
                <MenuItem value="3d">3 Days</MenuItem>
              </TextField>
            </Box>
          </Box>
        )}

        {activeTabKey === "context" && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Execution Context" />
            {isWorkflowJob ? (
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                Workflow-backed scheduled jobs ignore scheduler-level execution context. Configure execution inside the saved workflow instead.
              </Typography>
            ) : mixedDomainWarning ? (
              <Typography variant="body2" sx={{ color: "#fda4af" }}>
                {mixedDomainWarning}
              </Typography>
            ) : (
              <>
                <TextField
                  select
                  size="small"
                  label="Context"
                  value={execContext}
                  onChange={(e) => handleExecContextChange(e.target.value)}
                  sx={{ minWidth: 320, ...SELECT_FIELD_SX }}
                  SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                >
                  {availableExecContextOptions.map((option) => (
                    <MenuItem key={option.value} value={option.value}>
                      {option.title}
                    </MenuItem>
                  ))}
                </TextField>
                <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 1 }}>
                  {componentDomainSummary.kind === "none"
                    ? "Add script or Ansible playbook assemblies to automatically narrow the execution contexts shown here."
                    : (EXEC_CONTEXT_COPY[execContext]?.detail || "")}
                </Typography>
              </>
            )}
            {!isWorkflowJob && !mixedDomainWarning && remoteExec && (
              <Box sx={{ mt: 2, display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
                {isWinRMExecContext(execContext) && (
                  <FormControlLabel
                    sx={{ color: MAGIC_UI.textBright, "& .MuiTypography-root": { color: MAGIC_UI.textBright } }}
                    control={
                      <Checkbox
                        checked={useSvcAccount}
                        onChange={(e) => {
                          const checked = e.target.checked;
                          setUseSvcAccount(checked);
                          if (checked) {
                            setSelectedCredentialId("");
                          } else if (!selectedCredentialId && filteredCredentials.length) {
                            setSelectedCredentialId(String(filteredCredentials[0].id));
                          }
                        }}
                        sx={{
                          color: MAGIC_UI.accentA,
                          "&.Mui-checked": { color: MAGIC_UI.accentB },
                        }}
                      />
                    }
                    label="Use Configured svcBorealis Account"
                  />
                )}
                <TextField
                  select
                  size="small"
                  label="Credential"
                  value={selectedCredentialId}
                  onChange={(e) => setSelectedCredentialId(e.target.value)}
                  sx={{ minWidth: 280, ...SELECT_FIELD_SX }}
                  SelectProps={{ MenuProps: SELECT_MENU_PROPS }}
                  disabled={credentialLoading || !filteredCredentials.length || (isWinRMExecContext(execContext) && useSvcAccount)}
                >
                  {filteredCredentials.map((cred) => (
                    <MenuItem key={cred.id} value={String(cred.id)}>
                      {cred.name}
                      {credentialNeedsReset(cred) ? " (Secret Re-Entry Required)" : ""}
                    </MenuItem>
                  ))}
                </TextField>
                <Button
                  size="small"
                  variant="outlined"
                  startIcon={<RefreshIcon fontSize="small" />}
                  onClick={loadCredentials}
                  disabled={credentialLoading}
                  sx={OUTLINE_BUTTON_SX}
                >
                  Refresh
                </Button>
                {credentialLoading && <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />}
                {!credentialLoading && credentialError && (
                  <Typography variant="body2" sx={{ color: "#f87171" }}>
                    {credentialError}
                  </Typography>
                )}
                {isWinRMExecContext(execContext) && useSvcAccount && (
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                    Runs with the agent&apos;s svcBorealis account.
                  </Typography>
                )}
                {aegisLocked && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount) && (
                  <Typography variant="body2" sx={{ color: "#f0c36d" }}>
                    Aegis Cipher is locked. Remote jobs can still be saved, but credential-backed execution will remain disabled until the cipher is entered from Access Management &gt; Credentials.
                  </Typography>
                )}
                {!aegisLocked && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount) && selectedCredentialResetRequired && (
                  <Typography variant="body2" sx={{ color: "#f0c36d" }}>
                    {selectedCredentialResetMessage}
                  </Typography>
                )}
                {!credentialLoading &&
                  !credentialError &&
                  !filteredCredentials.length &&
                  !(isWinRMExecContext(execContext) && useSvcAccount) && (
                    <Typography variant="body2" sx={{ color: "#f87171" }}>
                      No {normalizeRemoteTransport(execContext) === "winrm" ? "WinRM" : "SSH"} credentials available. Create one under Access Management &gt; Credentials.
                    </Typography>
                  )}
              </Box>
            )}
          </Box>
        )}

        {editing && activeTabKey === "history" && (
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5, flexGrow: 1, minHeight: 0 }}>
            <Tabs
              value={effectiveHistorySubTabKey}
              onChange={(_, value) => setHistorySubTabKey(value)}
              variant="scrollable"
              scrollButtons="auto"
              TabIndicatorProps={{
                style: {
                  height: 3,
                  borderRadius: 3,
                  background: NAV_TAB_COLORS.iconActive,
                },
              }}
              sx={buildNavTabsSx(NAV_TAB_HEIGHT_COMPACT)}
            >
              <Tab label="Current Run" value="current" />
              <Tab label="Historical Runs" value="historical" />
              {selectedHistoryOccurrence ? (
                <Tab label={selectedHistoryRunLabel} value="historical_run" />
              ) : null}
            </Tabs>

            {effectiveHistorySubTabKey === "historical" ? (
              <GlassPanel sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
                <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, mb: 0.5 }}>
                  Historical Runs
                </Typography>
                <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                  Historical job run summaries from the last 30 days.
                </Typography>
                <Box
                  className={gridThemeClass}
                  sx={{
                    ...GRID_PANEL_SX,
                    mt: 1.5,
                    flexGrow: 1,
                    minHeight: { xs: 420, md: 640 },
                    height: "100%",
                  }}
                >
                  <AgGridReact
                    rowData={sortedHistory}
                    columnDefs={historySummaryColumnDefs}
                    defaultColDef={historySummaryDefaultColDef}
                    components={historySummaryComponents}
                    suppressCellFocus
                    headerHeight={44}
                    rowHeight={48}
                    pagination
                    paginationPageSize={20}
                    paginationPageSizeSelector={[20, 50, 100]}
                    overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No runs in the last 30 days.</span>"
                    getRowId={(params) => params.data?.key || params.rowIndex}
                    onGridReady={handleHistorySummaryGridReady}
                    onRowDoubleClicked={(event) => openHistoricalRun(event?.data)}
                    theme={gridTheme}
                  />
                </Box>
              </GlassPanel>
            ) : (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, flexGrow: 1, minHeight: 0 }}>
                <Box
                  sx={{
                    display: "flex",
                    flexWrap: "wrap",
                    alignItems: "flex-end",
                    justifyContent: "space-between",
                    columnGap: 1.5,
                    rowGap: 1,
                  }}
                >
                  <Box
                    sx={{
                      display: "flex",
                      flexWrap: "wrap",
                      alignItems: "flex-start",
                      columnGap: 1,
                      rowGap: 1,
                    }}
                  >
                    {JOB_HISTORY_STATUS_FILTER_GROUPS.map((group) => (
                      <Box
                        key={group.key}
                        sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}
                      >
                        <Typography
                          component="span"
                          sx={{
                            color: "#58a6ff",
                            fontSize: 11,
                            fontWeight: 600,
                            lineHeight: 1.1,
                            pl: 1,
                          }}
                        >
                          {group.label}
                        </Typography>
                        <CountSliderGroup
                          options={group.options}
                          activeKey={deviceStatusFilter}
                          counts={statusCounts}
                          onChange={setDeviceStatusFilter}
                        />
                      </Box>
                    ))}
                  </Box>
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, ml: "auto" }}>
                    {statusFilterSummary}
                  </Typography>
                </Box>

                <Box
                  className={gridThemeClass}
                  sx={{
                    ...GRID_PANEL_SX,
                    flex: "1 1 0",
                    minHeight: { xs: 520, md: 0 },
                    height: "100%",
                  }}
                >
                  <AgGridReact
                    rowData={jobHistoryGridRows}
                    columnDefs={jobHistoryGridColumnDefs}
                    defaultColDef={jobHistoryGridDefaultColDef}
                    components={jobHistoryGridComponents}
                    context={{ viewOutput: handleViewDeviceOutput, copyOutput: handleCopyDeviceOutput }}
                    suppressCellFocus
                    headerHeight={44}
                    rowHeight={50}
                    pagination
                    paginationPageSize={100}
                    paginationPageSizeSelector={[20, 50, 100]}
                    overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No targets found for this job.</span>"
                    getRowId={(params) => params.data?.id || params.rowIndex}
                    onGridReady={handleJobHistoryGridReady}
                    theme={gridTheme}
                  />
                </Box>
              </Box>
            )}
          </Box>
        )}
      </Box>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth={false}
        PaperProps={{
          sx: {
            ...DIALOG_PAPER_SX,
            display: "flex",
            flexDirection: "column",
            width: "95vw",
            maxWidth: "95vw",
            height: "95vh",
            maxHeight: "95vh",
          },
        }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <Box
            sx={{
              display: "flex",
              alignItems: "flex-start",
              justifyContent: "space-between",
              gap: 2,
              flexWrap: "wrap",
            }}
          >
            <DialogHeaderBlock title={outputTitle} subtitle="Review generated output, logs, and command results." />
            <Button onClick={() => setOutputOpen(false)} sx={PRIMARY_CTA_FLAT_SX}>
              Close
            </Button>
          </Box>
        </DialogTitle>
        <DialogContent
          sx={{
            ...DIALOG_CONTENT_SX,
            display: "flex",
            flexDirection: "column",
            gap: 2,
            flex: 1,
            minHeight: 0,
            pb: 3,
            overflow: "hidden",
          }}
        >
          {outputLoading ? (
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
              Loading output…
            </Typography>
          ) : null}
          {!outputLoading && outputError ? (
            <Typography variant="body2" sx={{ color: "#f87171" }}>
              {outputError}
            </Typography>
          ) : null}
          {!outputLoading && !outputError
            ? outputSections.map((section) => (
                <Box
                  key={section.key}
                  sx={{
                    mb: 2,
                    "&:last-of-type": { mb: 0 },
                    display: "flex",
                    flexDirection: "column",
                    minHeight: 0,
                    flex: outputSections.length === 1 ? 1 : "0 0 auto",
                  }}
                >
                  <Typography variant="subtitle2" sx={{ color: MAGIC_UI.textBright }}>
                    {section.title}
                  </Typography>
                  {section.path ? (
                    <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, display: "block", mb: 0.5 }}>
                      {section.path}
                    </Typography>
                  ) : null}
                  <Box
                    sx={{
                      border: `1px solid ${MAGIC_UI.panelBorder}`,
                      borderRadius: 2,
                      bgcolor: "rgba(4,7,17,0.65)",
                      position: "relative",
                      display: "flex",
                      flexDirection: "column",
                      flex: outputSections.length === 1 ? 1 : "0 0 auto",
                      minHeight: 0,
                      maxHeight: outputSections.length === 1 ? "none" : "calc(95vh - 230px)",
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
                    <Box
                      sx={{
                        position: "absolute",
                        top: 10,
                        right: 10,
                        zIndex: 1,
                      }}
                    >
                      <Tooltip
                        title={
                          section.content
                            ? copiedOutputKey === section.key
                              ? "Copied"
                              : "Copy output"
                            : "No output to copy"
                        }
                      >
                        <span>
                          <IconButton
                            size="small"
                            disabled={!section.content}
                            onClick={() => {
                              void handleCopyOutputSection(section);
                            }}
                            sx={{
                              color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
                              backgroundColor: "rgba(2,6,23,0.58)",
                              border: "1px solid rgba(148,163,184,0.2)",
                              "&:hover": {
                                backgroundColor: "rgba(8,15,33,0.82)",
                                color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textBright,
                              },
                              "&.Mui-disabled": {
                                color: "rgba(148,163,184,0.42)",
                                borderColor: "rgba(148,163,184,0.12)",
                              },
                            }}
                          >
                            {copiedOutputKey === section.key ? (
                              <CheckIcon fontSize="small" />
                            ) : (
                              <ContentCopyIcon fontSize="small" />
                            )}
                          </IconButton>
                        </span>
                      </Tooltip>
                    </Box>
                    <Box
                      component="pre"
                      className={`language-${section.lang || "markup"}`}
                      sx={{
                        m: 0,
                        p: 1.5,
                        pr: 5.5,
                        minHeight: outputSections.length === 1 ? "100%" : 160,
                        backgroundColor: "transparent !important",
                        background: "transparent !important",
                        whiteSpace: "pre",
                        overflowWrap: "normal",
                        wordBreak: "normal",
                        color: "#e6edf3",
                        fontSize: 12,
                        lineHeight: 1.45,
                        fontFamily:
                          'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                        "& code": {
                          display: "block",
                          fontFamily: "inherit",
                          fontSize: "inherit",
                          lineHeight: "inherit",
                          color: "inherit",
                          whiteSpace: "inherit",
                          backgroundColor: "transparent !important",
                          background: "transparent !important",
                        },
                        "&[class*='language-']": {
                          backgroundColor: "transparent !important",
                          background: "transparent !important",
                        },
                        "& code[class*='language-']": {
                          backgroundColor: "transparent !important",
                          background: "transparent !important",
                        },
                      }}
                    >
                      <Box
                        component="code"
                        dangerouslySetInnerHTML={{ __html: highlightCode(section.content ?? "", section.lang) }}
                      />
                    </Box>
                  </Box>
                </Box>
              ))
            : null}
        </DialogContent>
      </Dialog>
      {/* Add Component Dialog */}
      <Dialog
        open={addCompOpen}
        onClose={() => setAddCompOpen(false)}
        TransitionProps={{
          onEntered: handleAssemblyDialogEntered,
        }}
        fullWidth
        maxWidth={false}
        PaperProps={{
          sx: {
            ...DIALOG_PAPER_SX,
            display: "flex",
            flexDirection: "column",
            width: "95vw",
            maxWidth: "95vw",
            height: "95vh",
            maxHeight: "95vh",
          },
        }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <Box
            sx={{
              display: "flex",
              alignItems: "flex-start",
              justifyContent: "space-between",
              gap: 2,
              flexWrap: "wrap",
            }}
          >
            <DialogHeaderBlock title="Select an Assembly" subtitle="Choose the script, playbook, or workflow to add to this job." />
            <Box sx={{ display: "flex", alignItems: "center", gap: 1, ml: "auto" }}>
              <Button onClick={() => setAddCompOpen(false)} sx={OUTLINE_BUTTON_SX}>
                Cancel
              </Button>
              <Button
                onClick={async () => {
                  const ok = await addSelectedComponent();
                  if (ok) setAddCompOpen(false);
                }}
                sx={PRIMARY_CTA_SX}
                disabled={!selectedAssemblyRecord}
              >
                Add Assembly
              </Button>
            </Box>
          </Box>
        </DialogTitle>
        <DialogContent
          sx={{
            ...DIALOG_CONTENT_SX,
            display: "flex",
            flexDirection: "column",
            gap: 2,
            flex: 1,
            minHeight: 0,
            pb: 3,
            pt: 3.5,
            overflow: "hidden",
          }}
        >
          <Box sx={{ flex: 1, minHeight: 0, height: "100%" }}>
            <AssemblyPicker
              records={assemblyIndex.records}
              loading={assembliesLoading}
              error={assembliesError}
              allowedKinds={["script", "ansible", "workflow"]}
              selectedAssemblyGuid={selectedNodeId}
              onSelectionChange={(record) => {
                setSelectedNodeId(record?.assemblyGuid ? String(record.assemblyGuid).toLowerCase() : "");
              }}
              onChoose={async (record) => {
                if (!record) return;
                setSelectedNodeId(String(record.assemblyGuid || "").toLowerCase());
                const ok = await addSelectedComponent(record);
                if (ok) setAddCompOpen(false);
              }}
              height="100%"
            />
          </Box>
        </DialogContent>
      </Dialog>

      {/* Add Targets Dialog */}
      <Dialog
        open={addTargetOpen}
        onClose={() => setAddTargetOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{
          sx: {
            ...DIALOG_PAPER_SX,
            display: "flex",
            flexDirection: "column",
          },
        }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <Box
            sx={{
              display: "flex",
              alignItems: "flex-start",
              justifyContent: "space-between",
              gap: 2,
              flexWrap: "wrap",
            }}
          >
            <DialogHeaderBlock title="Select Targets" subtitle="Pick devices or filters that this scheduled job should run against." />
            <Box sx={{ display: "flex", alignItems: "center", gap: 1, ml: "auto" }}>
              <Button onClick={() => setAddTargetOpen(false)} sx={OUTLINE_BUTTON_SX}>
                Cancel
              </Button>
              <Button onClick={handleAddSelectedTargets} sx={PRIMARY_CTA_SX}>
                Add Selected
              </Button>
            </Box>
          </Box>
        </DialogTitle>
        <DialogContent sx={{ ...DIALOG_CONTENT_SX, pb: 3 }}>
          <Tabs
            value={targetPickerTab}
            onChange={(_, value) => setTargetPickerTab(value)}
            TabIndicatorProps={{
              style: {
                height: 3,
                borderRadius: 3,
                background: NAV_TAB_COLORS.iconActive,
              },
            }}
            sx={{
              mb: 2,
              ...buildNavTabsSx(),
            }}
          >
            <Tab
              label="Devices"
              value="devices"
              icon={<DevicesRoundedIcon sx={{ fontSize: 16 }} />}
              iconPosition="start"
            />
            <Tab
              label="Filters"
              value="filters"
              icon={<FilterListIcon sx={{ fontSize: 16 }} />}
              iconPosition="start"
            />
          </Tabs>

          {targetPickerTab === "devices" ? (
            <>
              <Box sx={{ mb: 1.25, display: "flex", gap: 2 }}>
                <TextField
                  size="small"
                  placeholder="Search devices..."
                  value={deviceSearch}
                  onChange={(e) => setDeviceSearch(e.target.value)}
                  sx={{ flex: 1, ...INPUT_FIELD_SX }}
                  helperText={deviceSearch.trim().length < 3 ? "Type at least 3 characters to search devices." : ""}
                  FormHelperTextProps={{ sx: { color: MAGIC_UI.textMuted } }}
                />
              </Box>
              <Box
                className={gridThemeClass}
                sx={{
                  ...GRID_PANEL_SX,
                  height: 420,
                }}
              >
                <AgGridReact
                  rowData={devicePickerRows}
                  columnDefs={devicePickerColumnDefs}
                  defaultColDef={devicePickerDefaultColDef}
                  animateRows
                  rowHeight={46}
                  headerHeight={44}
                  suppressCellFocus
                  rowSelection={MULTI_ROW_SELECTION}
                  selectionColumnDef={PICKER_SELECTION_COLUMN_DEF}
                  overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${devicePickerOverlay}</span>`}
                  getRowId={(params) => params.data?.id || params.rowIndex}
                  onGridReady={handleDevicePickerReady}
                  onSelectionChanged={handleDevicePickerSelectionChanged}
                  pagination
                  paginationPageSize={20}
                  paginationPageSizeSelector={[20, 50, 100]}
                  theme={gridTheme}
                />
              </Box>
            </>
          ) : (
            <>
              <Box sx={{ mb: 1.25, display: "flex", gap: 2 }}>
                <TextField
                  size="small"
                  placeholder="Search filters..."
                  value={filterSearch}
                  onChange={(e) => setFilterSearch(e.target.value)}
                  sx={{ flex: 1, ...INPUT_FIELD_SX }}
                  helperText={filterSearch.trim().length < 3 ? "Type at least 3 characters to search filters." : ""}
                  FormHelperTextProps={{ sx: { color: MAGIC_UI.textMuted } }}
                />
              </Box>
              <Box
                className={gridThemeClass}
                sx={{
                  ...GRID_PANEL_SX,
                  height: 420,
                }}
              >
                <AgGridReact
                  rowData={filterPickerRows}
                  columnDefs={filterPickerColumnDefs}
                  defaultColDef={filterPickerDefaultColDef}
                  animateRows
                  rowHeight={46}
                  headerHeight={44}
                  suppressCellFocus
                  rowSelection={MULTI_ROW_SELECTION}
                  selectionColumnDef={PICKER_SELECTION_COLUMN_DEF}
                  overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${filterPickerOverlay}</span>`}
                  getRowId={(params) => params.data?.id || params.rowIndex}
                  onGridReady={handleFilterPickerReady}
                  onSelectionChanged={handleFilterPickerSelectionChanged}
                  pagination
                  paginationPageSize={20}
                  paginationPageSizeSelector={[20, 50, 100]}
                  theme={gridTheme}
                />
              </Box>
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* Confirm Create Dialog */}
      <Dialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        maxWidth="xs"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={initialJob && initialJob.id ? "Save Job Changes" : isPatchBatchJob ? "Create Patch Jobs" : "Create Job"}
            subtitle={
              initialJob && initialJob.id
                ? "Confirm the changes before updating this scheduled job."
                : isPatchBatchJob
                  ? `Confirm ${patchJobBatch.items.length.toLocaleString()} one-KB patch jobs using the selected schedule.`
                  : "Confirm the job configuration before creating it."
            }
          />
        </DialogTitle>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setConfirmOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              setConfirmOpen(false);
              handleCreate();
            }}
            sx={DIALOG_PRIMARY_BUTTON_SX}
          >
            Confirm
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
