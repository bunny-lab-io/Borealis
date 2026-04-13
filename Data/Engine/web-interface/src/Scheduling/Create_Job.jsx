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
  Checkbox,
  FormControlLabel,
  MenuItem,
  Divider,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  GlobalStyles,
  CircularProgress
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  FilterList as FilterListIcon,
  PendingActions as PendingActionsIcon,
  WarningAmberRounded as WarningAmberRoundedIcon,
  Sync as SyncIcon,
  Timer as TimerIcon,
  Check as CheckIcon,
  Error as ErrorIcon,
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
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import ReactFlow, { Handle, Position } from "reactflow";
import "reactflow/dist/style.css";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DirtyStatePill, DomainBadge } from "../Assemblies/Assembly_Badges";
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
const JOB_HISTORY_SUBTAB_URL_BY_KEY = Object.freeze({
  current: "current_run",
  historical: "historical_runs",
});
const JOB_HISTORY_SUBTAB_KEY_BY_URL = Object.freeze({
  current_run: "current",
  historical_runs: "historical",
  current: "current",
  historical: "historical",
});

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
  if (normalized === "no devices targeted" || normalized === "no_devices_targeted") return "no_devices_targeted";
  if (normalized === "no eligible targets" || normalized === "no_eligible_targets") return "no_eligible_targets";
  return normalized;
};

const getJobStatusSortRank = (status) => {
  const key = normalizeJobStatusKey(status);
  return JOB_STATUS_SORT_RANK[key] ?? JOB_STATUS_SORT_RANK.default;
};

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

const componentExecutionDomain = (component) => {
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

const StatusPill = ({ label, theme }) => {
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
        textTransform: "uppercase",
        lineHeight: 1,
        fontFamily: gridFontFamily,
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

const hiddenHandleStyle = {
  width: 12,
  height: 12,
  border: "none",
  background: "transparent",
  opacity: 0,
  pointerEvents: "none"
};

const STATUS_META = {
  pending: { label: "Pending", color: "#aab2bf", Icon: PendingActionsIcon },
  running: { label: "Running", color: "#58a6ff", Icon: SyncIcon },
  expired: { label: "Expired", color: "#aab2bf", Icon: TimerIcon },
  success: { label: "Success", color: "#00d18c", Icon: CheckIcon },
  warning: { label: "Warning", color: "#fbbf24", Icon: WarningAmberRoundedIcon },
  failed: { label: "Failed", color: "#ff4f4f", Icon: ErrorIcon }
};

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

function StatusNode({ data }) {
  const { label, color, count, onClick, isActive, Icon } = data || {};
  const displayCount = Number.isFinite(count) ? count : Number(count) || 0;
  const borderColor = color || "#333";
  const activeGlow = color ? `${color}55` : "rgba(88,166,255,0.35)";
  const gradientLayer = color
    ? `linear-gradient(140deg, rgba(8,12,24,0.92), ${color}1f)`
    : "linear-gradient(140deg, rgba(8,12,24,0.92), rgba(14,20,38,0.85))";
  const handleClick = useCallback((event) => {
    event?.preventDefault();
    event?.stopPropagation();
    onClick && onClick();
  }, [onClick]);
  return (
    <Box
      onClick={handleClick}
      sx={{
        px: 5.4,
        py: 3.8,
        borderRadius: 2,
        border: `1px solid ${borderColor}`,
        boxShadow: isActive ? `0 0 25px ${activeGlow}` : "0 20px 40px rgba(2,6,23,0.65)",
        cursor: "pointer",
        minWidth: 324,
        textAlign: "left",
        transition: "border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease",
        transform: isActive ? "translateY(-2px)" : "none",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "flex-start",
        position: "relative",
        overflow: "hidden",
        "&::before": {
          content: '""',
          position: "absolute",
          inset: 0,
          background: gradientLayer,
          borderRadius: "inherit",
          opacity: 0.95,
          transition: "opacity 0.2s ease",
        },
        "&::after": {
          content: '""',
          position: "absolute",
          inset: "-25% -40%",
          background: color
            ? `radial-gradient(circle at 30% 20%, ${color}30, transparent 55%)`
            : "radial-gradient(circle at 30% 20%, rgba(125,183,255,0.3), transparent 55%)",
          borderRadius: "inherit",
          opacity: 0.65,
          filter: "blur(0px)",
          transition: "opacity 0.2s ease",
        },
        "&:hover::before": { opacity: 1 },
        "&:hover::after": { opacity: 0.85 },
      }}
    >
      <Handle type="target" position={Position.Left} id="left-top" style={{ ...hiddenHandleStyle, top: "32%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="target" position={Position.Left} id="left-bottom" style={{ ...hiddenHandleStyle, top: "68%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="source" position={Position.Right} id="right-top" style={{ ...hiddenHandleStyle, top: "32%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Handle type="source" position={Position.Right} id="right-bottom" style={{ ...hiddenHandleStyle, top: "68%", transform: "translateY(-50%)" }} isConnectable={false} />
      <Box sx={{ display: "flex", alignItems: "center", gap: 1.2, position: "relative", zIndex: 2 }}>
        {Icon ? <Icon sx={{ color: color || "#e6edf3", fontSize: 32 }} /> : null}
        <Typography variant="subtitle2" sx={{ fontWeight: 600, color: color || "#e6edf3", userSelect: "none", fontSize: "1.3rem" }}>
          {`${displayCount} ${label || ""}`}
        </Typography>
      </Box>
    </Box>
  );
}

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
  const [jobName, setJobName] = useState("");
  const [pageTitleJobName, setPageTitleJobName] = useState("");
  // Components the job will run: {type:'script'|'workflow', path, name, description}
  const [components, setComponents] = useState([]);
  const workflowComponentCount = useMemo(
    () => components.filter((component) => isWorkflowComponentRecord(component)).length,
    [components]
  );
  const isWorkflowJob = workflowComponentCount > 0;
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
    if (isWorkflowJob) {
      return "Workflow-backed jobs execute one saved workflow. Targets and execution context are defined inside the workflow itself.";
    }
    if (scheduleType === "immediately") {
      return "Launch immediately or save as a quick job with your selected assemblies.";
    }
    return PAGE_SUBTITLE;
  }, [isWorkflowJob, scheduleType]);
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

  useEffect(() => {
    setQuickJobDraft(location.state?.quickJobDraft || null);
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
  const [deviceRows, setDeviceRows] = useState([]);
  const [deviceStatusFilter, setDeviceStatusFilter] = useState(null);
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

  const deviceFiltered = useMemo(() => {
    const matchStatusFilter = (status, filterKey) => {
      if (filterKey === "pending") return status === "pending";
      if (filterKey === "running") return status === "running";
      if (filterKey === "success") return status === "success";
      if (filterKey === "warning") return status === "warning";
      if (filterKey === "failed") return status === "failed" || status === "timed_out";
      if (filterKey === "expired") return status === "expired";
      return true;
    };

    return deviceRows.filter((row) => {
      const normalizedStatus = normalizeJobStatusKey(row?.job_status || "");
      if (deviceStatusFilter && !matchStatusFilter(normalizedStatus, deviceStatusFilter)) {
        return false;
      }
      return true;
    });
  }, [deviceRows, deviceStatusFilter]);

  const jobHistoryGridRows = useMemo(
    () =>
      deviceFiltered.map((row, index) => ({
        id: `${row.hostname || "device"}-${index}`,
        hostname: row.hostname || "",
        online: Boolean(row.online),
        onlineLabel: row?.online ? "Online" : "Offline",
        site: row.site || "",
        ranOn: row.ran_on,
        jobStatus: row.job_status || "",
        jobStatusLabel: JOB_RESULT_THEME[normalizeJobStatusKey(row?.job_status || "")]?.label || (row.job_status || ""),
        jobStatusSortRank: getJobStatusSortRank(row?.job_status || ""),
        hasStdOut:
          Boolean(row.has_stdout) ||
          (Array.isArray(row.activities) && row.activities.some((activity) => Boolean(activity?.has_stdout))),
        hasStdErr:
          Boolean(row.has_stderr) ||
          (Array.isArray(row.activities) && row.activities.some((activity) => Boolean(activity?.has_stderr))),
        raw: row,
      })).map((row) => ({
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
    [deviceFiltered]
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
    const needsCredential = !isWorkflowJob && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount);
    if (needsCredential && !selectedCredentialId) return false;
    if (scheduleType !== "immediately") {
      return !!startDateTime;
    }
    return true;
  }, [jobName, components.length, isWorkflowJob, targets.length, scheduleType, startDateTime, remoteExec, selectedCredentialId, execContext, useSvcAccount]);

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
  const [clearingHistory, setClearingHistory] = useState(false);

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
      const occurrence = Number(jobPayload?.latest_occurrence || jobPayload?.last_run_ts || 0);
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
      setJobSummary(jobPayload);
      const devices = Array.isArray(dev.devices) ? dev.devices.map((device) => ({
        ...device,
        activities: Array.isArray(device.activities) ? device.activities : [],
      })) : [];
      setDeviceRows(devices);
    } catch {
      setHistoryRows([]);
      setJobSummary({});
      setDeviceRows([]);
    }
  }, [editing, initialJob?.id]);

  useEffect(() => {
    if (!editing) return;
    let t;
    (async () => { try { await loadHistory(); } catch {} })();
    t = setInterval(loadHistory, 10000);
    return () => { if (t) clearInterval(t); };
  }, [editing, loadHistory]);

  const clearJobHistory = useCallback(async () => {
    if (!editing || !initialJob?.id || clearingHistory) return;
    setClearingHistory(true);
    try {
      await fetch(`/api/scheduled_jobs/${initialJob.id}/runs`, { method: "DELETE" });
      await loadHistory();
    } catch {
      // no-op; page poll will reconcile if the delete fails silently
    } finally {
      setClearingHistory(false);
    }
  }, [clearingHistory, editing, initialJob?.id, loadHistory]);

  const resultChip = useCallback((status) => {
    const key = normalizeJobStatusKey(status);
    const theme = JOB_RESULT_THEME[key] || JOB_RESULT_THEME.default;
    const label = JOB_RESULT_THEME[key]?.label || status || "Status";
    return <StatusPill label={label} theme={theme} />;
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
        statuses: new Set()
      };
      if (!existing.scheduled_ts && row?.scheduled_ts) existing.scheduled_ts = row.scheduled_ts;
      if (row?.started_ts) {
        existing.started_ts = existing.started_ts == null ? row.started_ts : Math.min(existing.started_ts, row.started_ts);
      }
      if (row?.finished_ts) {
        existing.finished_ts = existing.finished_ts == null ? row.finished_ts : Math.max(existing.finished_ts, row.finished_ts);
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
      const hasInFlight = statuses.some((s) => s === "running" || s === "pending");
      if (hasInFlight) return;
      const allSkipped = statuses.every((s) => ["skipped", "no_devices_targeted", "no_eligible_targets"].includes(s));
      const allNoDevicesTargeted = statuses.every((s) => s === "no_devices_targeted");
      const allNoEligibleTargets = statuses.every((s) => s === "no_eligible_targets");
      const hasFailure = statuses.some((s) => ["failed", "expired", "timed_out"].includes(s));
      const hasWarning = statuses.some((s) => s === "warning");
      const hasSuccess = statuses.some((s) => s === "success");
      const statusLabel = allSkipped
        ? allNoEligibleTargets
          ? "No Eligible Targets"
          : allNoDevicesTargeted
            ? "No Devices Targeted"
            : "Skipped"
        : hasFailure
          ? "Failed"
          : hasWarning
            ? "Warning"
            : hasSuccess
              ? "Success"
              : "Failed";
      summaries.push({
        key: entry.key,
        scheduled_ts: entry.scheduled_ts,
        started_ts: entry.started_ts,
        finished_ts: entry.finished_ts,
        status: statusLabel
      });
    });
    return summaries;
  }, [historyRows]);

  const sortedHistory = useMemo(() => {
    return [...aggregatedHistory].sort(
      (a, b) => Number(b?.finished_ts || 0) - Number(a?.finished_ts || 0)
    );
  }, [aggregatedHistory]);

  const historySummaryComponents = useMemo(
    () => ({
      HistoryStatusRenderer: (params) => resultChip(params.value || ""),
    }),
    [resultChip]
  );
  const historySummaryColumnDefs = useMemo(
    () => [
      {
        field: "scheduled_ts",
        headerName: "Scheduled",
        minWidth: 180,
        valueFormatter: (params) => (params.value ? fmtTs(params.value) : ""),
      },
      {
        field: "started_ts",
        headerName: "Started",
        minWidth: 180,
        valueFormatter: (params) => (params.value ? fmtTs(params.value) : ""),
      },
      {
        field: "finished_ts",
        headerName: "Finished",
        minWidth: 180,
        valueFormatter: (params) => (params.value ? fmtTs(params.value) : ""),
      },
      {
        field: "status",
        headerName: "Status",
        minWidth: 140,
        cellRenderer: "HistoryStatusRenderer",
        cellClass: "status-pill-cell",
        sortable: false,
        suppressHeaderMenuButton: true,
      },
    ],
    [fmtTs]
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
  const HISTORY_SUMMARY_AUTO_COLS = useRef(["scheduled_ts", "started_ts", "finished_ts", "status"]);
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

  // --- Job Progress (summary) ---
  const [jobSummary, setJobSummary] = useState({});
  const counts = jobSummary?.result_counts || {};

  const deviceStatusCounts = useMemo(() => {
    const base = { pending: 0, running: 0, success: 0, warning: 0, failed: 0, expired: 0 };
    deviceRows.forEach((row) => {
      const normalized = normalizeJobStatusKey(row?.job_status || "");
      if (normalized === "pending") {
        base.pending += 1;
      } else if (normalized === "running") {
        base.running += 1;
      } else if (normalized === "success") {
        base.success += 1;
      } else if (normalized === "warning") {
        base.warning += 1;
      } else if (normalized === "expired") {
        base.expired += 1;
      } else if (normalized === "failed" || normalized === "timed_out") {
        base.failed += 1;
      } else {
        base.pending += 1;
      }
    });
    return base;
  }, [deviceRows]);

  const statusCounts = useMemo(() => {
    const summaryKeys = ["pending", "running", "success", "warning", "failed", "expired", "timed_out", "skipped"];
    const hasSummaryCounts =
      Number((counts || {}).total_targets ?? 0) > 0 ||
      summaryKeys.some((key) => Number((counts || {})[key] ?? 0) > 0);
    if (hasSummaryCounts) {
      return {
        pending: Number((counts || {}).pending ?? 0),
        running: Number((counts || {}).running ?? 0),
        success: Number((counts || {}).success ?? 0),
        warning: Number((counts || {}).warning ?? 0),
        failed: Number((counts || {}).failed ?? 0) + Number((counts || {}).timed_out ?? 0),
        expired: Number((counts || {}).expired ?? 0),
      };
    }
    return deviceStatusCounts;
  }, [counts, deviceStatusCounts]);

  const statusNodeTypes = useMemo(() => ({ statusNode: StatusNode }), []);

  const handleStatusNodeClick = useCallback((key) => {
    setDeviceStatusFilter((prev) => (prev === key ? null : key));
  }, []);

  const statusNodes = useMemo(() => [
    {
      id: "pending",
      type: "statusNode",
      position: { x: -420, y: 170 },
      data: {
        label: STATUS_META.pending.label,
        color: STATUS_META.pending.color,
        count: statusCounts.pending,
        Icon: STATUS_META.pending.Icon,
        onClick: () => handleStatusNodeClick("pending"),
        isActive: deviceStatusFilter === "pending"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "running",
      type: "statusNode",
      position: { x: 0, y: 170 },
      data: {
        label: STATUS_META.running.label,
        color: STATUS_META.running.color,
        count: statusCounts.running,
        Icon: STATUS_META.running.Icon,
        onClick: () => handleStatusNodeClick("running"),
        isActive: deviceStatusFilter === "running"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "expired",
      type: "statusNode",
      position: { x: 0, y: 340 },
      data: {
        label: STATUS_META.expired.label,
        color: STATUS_META.expired.color,
        count: statusCounts.expired,
        Icon: STATUS_META.expired.Icon,
        onClick: () => handleStatusNodeClick("expired"),
        isActive: deviceStatusFilter === "expired"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "success",
      type: "statusNode",
      position: { x: 420, y: 0 },
      data: {
        label: STATUS_META.success.label,
        color: STATUS_META.success.color,
        count: statusCounts.success,
        Icon: STATUS_META.success.Icon,
        onClick: () => handleStatusNodeClick("success"),
        isActive: deviceStatusFilter === "success"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "warning",
      type: "statusNode",
      position: { x: 420, y: 170 },
      data: {
        label: STATUS_META.warning.label,
        color: STATUS_META.warning.color,
        count: statusCounts.warning,
        Icon: STATUS_META.warning.Icon,
        onClick: () => handleStatusNodeClick("warning"),
        isActive: deviceStatusFilter === "warning"
      },
      draggable: false,
      selectable: false
    },
    {
      id: "failed",
      type: "statusNode",
      position: { x: 420, y: 340 },
      data: {
        label: STATUS_META.failed.label,
        color: STATUS_META.failed.color,
        count: statusCounts.failed,
        Icon: STATUS_META.failed.Icon,
        onClick: () => handleStatusNodeClick("failed"),
        isActive: deviceStatusFilter === "failed"
      },
      draggable: false,
      selectable: false
    }
  ], [statusCounts, handleStatusNodeClick, deviceStatusFilter]);

  const statusEdges = useMemo(() => [
    {
      id: "pending-running",
      source: "pending",
      target: "running",
      sourceHandle: "right-top",
      targetHandle: "left-top",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "pending-expired",
      source: "pending",
      target: "expired",
      sourceHandle: "right-bottom",
      targetHandle: "left-bottom",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "running-success",
      source: "running",
      target: "success",
      sourceHandle: "right-top",
      targetHandle: "left-top",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "running-warning",
      source: "running",
      target: "warning",
      sourceHandle: "right-top",
      targetHandle: "left-top",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    },
    {
      id: "running-failed",
      source: "running",
      target: "failed",
      sourceHandle: "right-bottom",
      targetHandle: "left-bottom",
      type: "smoothstep",
      animated: true,
      className: "status-flow-edge"
    }
  ], []);

  const JobStatusFlow = () => (
    <Box sx={{ mb: 2 }}>
      <GlobalStyles
        styles={{
          "@keyframes statusFlowDash": {
            "0%": { strokeDashoffset: 0 },
            "100%": { strokeDashoffset: -24 },
          },
          ".status-flow-edge .react-flow__edge-path": {
            strokeDasharray: "10 6",
            animation: "statusFlowDash 1.2s linear infinite",
            strokeWidth: 2,
            stroke: MAGIC_UI.accentA,
          },
        }}
      />
      <Box sx={{ height: 380, p: 0, background: "transparent" }}>
        <ReactFlow
          nodes={statusNodes}
          edges={statusEdges}
          nodeTypes={statusNodeTypes}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          panOnDrag={false}
          zoomOnScroll={false}
          zoomOnPinch={false}
          panOnScroll={false}
          zoomOnDoubleClick={false}
          preventScrolling={false}
          onNodeClick={(_, node) => {
            if (node?.id && STATUS_META[node.id]) handleStatusNodeClick(node.id);
          }}
          selectionOnDrag={false}
          proOptions={{ hideAttribution: true }}
          style={{ background: "transparent" }}
        />
      </Box>
      {deviceStatusFilter ? (
        <Box sx={{ pt: 1.25, display: "flex", alignItems: "center", gap: 1.5 }}>
          <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
            Showing devices with {STATUS_META[deviceStatusFilter]?.label || deviceStatusFilter} results
          </Typography>
          <Button size="small" sx={{ color: MAGIC_UI.accentA, textTransform: "none", p: 0 }} onClick={() => setDeviceStatusFilter(null)}>
            Clear Filter
          </Button>
        </Box>
      ) : null}
    </Box>
  );
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

  const handleViewDeviceOutput = useCallback(async (row, mode = "stdout") => {
    if (!row) return;
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const activities = Array.isArray(row.activities) ? row.activities : [];
    const relevant = activities.filter((act) => (mode === "stderr" ? act.has_stderr : act.has_stdout));
    setOutputTitle(`${label} - ${row.hostname || ""}`);
    setOutputSections([]);
    setOutputError("");
    setOutputLoading(true);
    setOutputOpen(true);
    if (!relevant.length) {
      setOutputError(`No ${label} available for this device.`);
      setOutputLoading(false);
      return;
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
    if (!sections.length) {
      setOutputError(`No ${label} available for this device.`);
    }
    setOutputSections(sections);
    setOutputLoading(false);
  }, [inferLanguage, loadActivity]);

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
      JobStatusRenderer: (params) => resultChip(params.data?.jobStatus || ""),
      OutputActionsRenderer: (params) => {
        const row = params.data;
        if (!row) return null;
        return (
          <Box sx={{ display: "flex", gap: 1, justifyContent: "flex-start", width: "100%" }}>
            {row.hasStdOut ? (
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
            ) : null}
            {row.hasStdErr ? (
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
            ) : null}
          </Box>
        );
      },
    }),
    [resultChip]
  );
  const jobHistoryGridColumnDefs = useMemo(
    () => [
      { field: "hostname", headerName: "Hostname", minWidth: 200, filter: "agTextColumnFilter" },
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
        minWidth: 150,
        cellRenderer: "JobStatusRenderer",
        cellClass: "status-pill-cell",
        filter: "agSetColumnFilter",
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
    [fmtTs]
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
        jobHistoryGridApiRef.current.refreshCells({ columns: ["outputState"], force: true });
      } catch {}
    });
  }, [jobHistoryGridRows]);

  useEffect(() => {
    let canceled = false;
    const hydrate = async () => {
      if (initialJob && initialJob.id) {
        if (!hasHydratedJobPayload(resolvedInitialJob)) {
          return;
        }

        setJobName(resolvedInitialJob.name || "");
        setPageTitleJobName(
          typeof resolvedInitialJob.name === "string" ? resolvedInitialJob.name.trim() : ""
        );
        setTargets(normalizeTargetList(resolvedInitialJob.targets || []));
        setScheduleType(
          resolvedInitialJob.schedule_type || resolvedInitialJob.schedule?.type || "immediately"
        );
        setStartDateTime(
          resolvedInitialJob.start_ts
            ? dayjs(Number(resolvedInitialJob.start_ts) * 1000).second(0)
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
      } else if (!initialJob) {
        setPageTitleJobName("");
        setComponents([]);
        setComponentVarErrors({});
        setSelectedCredentialId("");
        setUseSvcAccount(true);
        setExpiration("1h");
      }
    };
    hydrate();
    return () => {
      canceled = true;
    };
  }, [hydrateExistingComponents, initialJob, normalizeTargetList, resolvedInitialJob]);

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
    if (workflowComponents.length > 1) {
      alert("Workflow-backed scheduled jobs currently support exactly one workflow component.");
      return;
    }
    if (workflowComponents.length === 1 && workflowComponents.length !== components.length) {
      alert("Workflow-backed scheduled jobs cannot mix workflow, script, or Ansible components.");
      return;
    }
    if (componentDomains.has("script") && componentDomains.has("ansible")) {
      alert("Scheduled jobs cannot mix script assemblies with Ansible playbook assemblies. Remove the cross-domain assemblies or split them into separate jobs.");
      return;
    }
    const workflowMode = workflowComponents.length === 1;
    const requiresAnsibleOnly = !workflowMode && ANSIBLE_EXEC_CONTEXTS.includes(execContext);
    if (requiresAnsibleOnly) {
      const hasNonAnsibleComponent = components.some((component) => !isAnsibleComponentRecord(component));
      if (hasNonAnsibleComponent) {
        alert("Jobs using SSH or WinRM execution contexts must contain only Ansible playbook assemblies.");
        return;
      }
    }
    const requiresScriptOnly = !workflowMode && SCRIPT_EXEC_CONTEXTS.includes(execContext);
    if (requiresScriptOnly) {
      const hasNonScriptComponent = components.some(
        (component) => isWorkflowComponentRecord(component) || isAnsibleComponentRecord(component)
      );
      if (hasNonScriptComponent) {
        alert("Jobs using agent execution contexts must contain only script assemblies.");
        return;
      }
    }
    if (!workflowMode && remoteExec && !(isWinRMExecContext(execContext) && useSvcAccount) && !selectedCredentialId) {
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
    const payloadComponents = sanitizeComponentsForSave(components);
    const payload = {
      name: jobName,
      components: payloadComponents,
      targets: workflowMode ? [] : serializeTargetsForSave(targets),
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? (() => { try { const d = startDateTime?.toDate?.() || new Date(startDateTime); d.setSeconds(0,0); return d.toISOString(); } catch { return startDateTime; } })() : null },
      duration: { stopAfterEnabled: expiration !== "no_expire", expiration },
      execution_context: workflowMode ? "system" : execContext,
      credential_id: workflowMode ? null : (remoteExec && !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null),
      use_service_account: workflowMode ? false : (isWinRMExecContext(execContext) ? Boolean(useSvcAccount) : false)
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
      navigate(APP_PATHS.jobs);
    } catch (err) {
      alert(String(err.message || err));
    }
  };

  const tabDefs = useMemo(() => {
    const base = [
      { key: "name", label: "Job Name", icon: DriveFileRenameOutlineIcon },
      { key: "components", label: "Assemblies", icon: AppsIcon },
    ];
    if (!isWorkflowJob) {
      base.push({ key: "targets", label: "Targets", icon: DevicesRoundedIcon });
    }
    base.push({ key: "schedule", label: "Schedule", icon: ScheduleRoundedIcon });
    if (!isWorkflowJob) {
      base.push({ key: "context", label: "Execution Context", icon: SettingsApplicationsRoundedIcon });
    }
    if (editing) base.push({ key: "history", label: "Job History", icon: HistoryRoundedIcon });
    return base;
  }, [editing, isWorkflowJob]);
  const { activeKey: activeTabUrlKey, setActiveKey: setActiveTabUrlKey } = useUrlTabState({
    param: "tab",
    defaultKey: tabDefs[0]?.key || "name",
    allowedKeys: tabDefs.map((tabDef) => tabDef.key),
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
  const { activeKey: historySubTabKey, setActiveKey: setHistorySubTabKey } = useUrlTabState({
    param: "history_view",
    defaultKey: "current",
    allowedKeys: ["current", "historical"],
    keyByUrl: JOB_HISTORY_SUBTAB_KEY_BY_URL,
    urlByKey: JOB_HISTORY_SUBTAB_URL_BY_KEY,
  });
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
      {
        id: "scheduled-job-save",
        label: editing ? "Save Changes" : "Create Job",
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
    [clearJobHistory, clearingHistory, editing, isValid, navigate]
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
                <MenuItem value="every_5_minutes">Every 5 Minutes</MenuItem>
                <MenuItem value="every_10_minutes">Every 10 Minutes</MenuItem>
                <MenuItem value="every_15_minutes">Every 15 Minutes</MenuItem>
                <MenuItem value="every_30_minutes">Every 30 Minutes</MenuItem>
                <MenuItem value="every_hour">Every Hour</MenuItem>
                <MenuItem value="daily">Daily</MenuItem>
                <MenuItem value="weekly">Weekly</MenuItem>
                <MenuItem value="monthly">Monthly</MenuItem>
                <MenuItem value="yearly">Yearly</MenuItem>
              </TextField>
              {scheduleType !== "immediately" && (
                <LocalizationProvider dateAdapter={AdapterDayjs}>
                  <DateTimePicker
                    value={startDateTime}
                    onChange={(val) => setStartDateTime(val?.second ? val.second(0) : val)}
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
              value={historySubTabKey}
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
            </Tabs>

            {historySubTabKey === "current" ? (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5, flexGrow: 1, minHeight: 0 }}>
                <GlassPanel>
                  <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, mb: 0.5 }}>
                    Devices
                  </Typography>
                  <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                    Devices targeted by this scheduled job. Use the built-in AG Grid column filters from each header to narrow the current run.
                  </Typography>
                  <Box
                    className={gridThemeClass}
                    sx={{
                      ...GRID_PANEL_SX,
                      mt: 1.5,
                      height: { xs: 520, md: 680 },
                    }}
                  >
                    <AgGridReact
                      rowData={jobHistoryGridRows}
                      columnDefs={jobHistoryGridColumnDefs}
                      defaultColDef={jobHistoryGridDefaultColDef}
                      components={jobHistoryGridComponents}
                      context={{ viewOutput: handleViewDeviceOutput }}
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
                </GlassPanel>

                <JobStatusFlow />
              </Box>
            ) : (
              <GlassPanel sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
                <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, mb: 0.5 }}>
                  Historical Runs
                </Typography>
                <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                  Historical job history summaries from the last 30 days.
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
                    theme={gridTheme}
                  />
                </Box>
              </GlassPanel>
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
                      component="pre"
                      className={`language-${section.lang || "markup"}`}
                      sx={{
                        m: 0,
                        p: 1.5,
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
          <Box
            sx={{
              display: "flex",
              flexWrap: "wrap",
              gap: 2,
              mb: 2,
              alignItems: "flex-start",
              justifyContent: "space-between",
            }}
          >
            <Box
              sx={{
                display: "flex",
                flexWrap: "wrap",
                alignItems: "flex-start",
                columnGap: 1,
                rowGap: 1.2,
              }}
            >
              <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
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
                  Assembly Type
                </Typography>
                <CountSliderGroup
                  options={ASSEMBLY_TYPE_FILTER_OPTIONS}
                  activeKey={assemblyTypeFilterMode}
                  counts={assemblyTypeCounts}
                  onChange={setAssemblyTypeFilterMode}
                />
              </Box>
              <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
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
                  Operating System
                </Typography>
                <CountSliderGroup
                  options={ASSEMBLY_OS_FILTER_OPTIONS}
                  activeKey={assemblyOsFilterMode}
                  counts={assemblyOsCounts}
                  onChange={setAssemblyOsFilterMode}
                />
              </Box>
            </Box>
            <Box sx={{ display: "flex", flexDirection: "column", gap: 1, minWidth: { xs: "100%", md: 360 }, ml: "auto" }}>
              <Box
                sx={{
                  position: "relative",
                  display: "flex",
                  alignItems: "center",
                  gap: 1.2,
                  width: "100%",
                  minWidth: 0,
                  height: 42,
                  px: 1.6,
                  borderRadius: "999px",
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  background:
                    "linear-gradient(135deg, rgba(10, 16, 31, 0.94) 0%, rgba(8, 13, 28, 0.92) 60%, rgba(20, 8, 33, 0.92) 100%)",
                  boxShadow: "0 10px 28px rgba(4, 8, 24, 0.38)",
                  backdropFilter: "blur(16px) saturate(145%)",
                  transition: "border-color 160ms ease, box-shadow 180ms ease, transform 120ms ease",
                  "&:hover": {
                    borderColor: "rgba(125, 183, 255, 0.34)",
                  },
                  "&:focus-within": {
                    borderColor: "rgba(125, 183, 255, 0.34)",
                    transform: "translateY(-0.5px)",
                  },
                }}
              >
                <SearchIcon sx={{ color: MAGIC_UI.accentA, fontSize: 19, flexShrink: 0 }} />
                <InputBase
                  value={assemblyFilterText}
                  onChange={(e) => setAssemblyFilterText(e.target.value)}
                  placeholder="Search Assemblies..."
                  inputProps={{ "aria-label": "Search assemblies" }}
                  sx={{
                    flex: 1,
                    minWidth: 0,
                    color: MAGIC_UI.textBright,
                    fontSize: "0.95rem",
                    fontWeight: 500,
                    "& input::placeholder": {
                      color: "rgba(148, 163, 184, 0.92)",
                      opacity: 1,
                    },
                  }}
                />
              </Box>
            </Box>
          </Box>
          {assembliesError ? (
            <Typography variant="body2" sx={{ color: "#f87171", mb: 1 }}>
              {assembliesError}
            </Typography>
          ) : null}
          {assembliesLoading && (
            <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: MAGIC_UI.accentA, mb: 1 }}>
              <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />
              <Typography variant="body2">Loading assemblies…</Typography>
            </Box>
          )}
          <Box
            className={gridThemeClass}
            sx={{
              ...GRID_PANEL_SX,
              flexGrow: 1,
              minHeight: 0,
              height: "100%",
            }}
          >
            <AgGridReact
              rowData={filteredAssemblyRows}
              columnDefs={assemblyColumnDefs}
              defaultColDef={assemblyDefaultColDef}
              suppressCellFocus
              headerHeight={44}
              rowHeight={48}
              domLayout="normal"
              overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No assemblies match the current filters.</span>"
              pagination
              paginationPageSize={100}
              paginationPageSizeSelector={[25, 50, 100]}
              theme={gridTheme}
              rowSelection={SINGLE_ROW_SELECTION}
              animateRows
              getRowId={(params) => params.data?.id || params.rowIndex}
              onGridReady={handleAssemblyGridReady}
              onFirstDataRendered={handleAssemblyGridFirstDataRendered}
              onRowClicked={handleAssemblyRowClick}
              onRowDoubleClicked={handleAssemblyRowDoubleClick}
              onSelectionChanged={handleAssemblySelectionChanged}
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
            title={initialJob && initialJob.id ? "Save Job Changes" : "Create Job"}
            subtitle={initialJob && initialJob.id ? "Confirm the changes before updating this scheduled job." : "Confirm the job configuration before creating it."}
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
