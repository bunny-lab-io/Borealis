import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogContent,
  DialogTitle,
  FormControl,
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  ArrowBack as ArrowBackIcon,
  AutorenewRounded as ProgressActiveIcon,
  Check as CheckIcon,
  CheckCircleRounded as ProgressCompleteIcon,
  ContentCopy as ContentCopyIcon,
  Devices as DevicesIcon,
  DevicesRounded as DevicesRoundedIcon,
  DriveFileRenameOutline as DriveFileRenameOutlineIcon,
  ErrorOutlineRounded as ProgressErrorIcon,
  PlayArrow as PlayArrowIcon,
  RadioButtonUncheckedRounded as ProgressPendingIcon,
  Refresh as RefreshIcon,
  RemoveCircleOutlineRounded as ProgressSkippedIcon,
  ScheduleRounded as ScheduleRoundedIcon,
  SettingsApplicationsRounded as SettingsApplicationsRoundedIcon,
  TravelExplore as TravelExploreIcon,
  VpnKeyRounded as ProgressAuthErrorIcon,
} from "@mui/icons-material";
import { LocalizationProvider } from "@mui/x-date-pickers/LocalizationProvider";
import { DateTimePicker } from "@mui/x-date-pickers/DateTimePicker";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs from "dayjs";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import {
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { CountSliderGroup } from "../Automation/Watchdogs/shared.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import PageBodyFrame from "../PageBodyFrame.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const PAGE_TITLE = "Automatic Device Onboarding";
const PAGE_SUBTITLE = "Enroll remote devices automatically as long as they are reachable by the Borealis Engine using stored machine or domain credentials.";
const DEFAULT_BRANCH = "main";
const DEFAULT_SSH_PORT = 22;
const DEFAULT_WINDOWS_PORT = 445;
const DEFAULT_WINRM_PORT = 5985;
const DEFAULT_ONBOARDING_CONCURRENCY = 5;
const BOREALIS_GITHUB_REPO = "bunny-lab-io/Borealis";
const GITHUB_BRANCHES_API_URL = `https://api.github.com/repos/${BOREALIS_GITHUB_REPO}/branches`;
const ONBOARDING_TAB_URL_BY_KEY = Object.freeze({
  name: "job_name",
  scope: "scope",
  context: "connection_method",
  schedule: "schedule",
  targets: "discovered_devices",
});
const ONBOARDING_TAB_KEY_BY_URL = Object.freeze({
  job_name: "name",
  name: "name",
  scope: "scope",
  connection_method: "context",
  ssh_context: "context",
  context: "context",
  schedule: "schedule",
  discovered_devices: "targets",
  target_status: "targets",
  targets: "targets",
});

const MAGIC_UI = {
  panelBg: "linear-gradient(145deg, rgba(7,10,24,0.96), rgba(6,10,28,0.92) 45%, rgba(14,8,30,0.95))",
  panelBorder: "rgba(148, 163, 184, 0.32)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  danger: "#fb7185",
};

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

const PAGE_SX = {
  m: 0,
  p: 0,
  flexGrow: 1,
  minWidth: 0,
  minHeight: 0,
  height: "100%",
  boxSizing: "border-box",
  display: "flex",
  flexDirection: "column",
  borderRadius: 0,
  background: "transparent",
  border: "none",
  boxShadow: "none",
  color: MAGIC_UI.textBright,
};

const GRID_PANEL_SX = {
  width: "100%",
  height: "100%",
  minHeight: 0,
  fontFamily: gridFontFamily,
  "--ag-font-family": gridFontFamily,
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
  borderRadius: "8px",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "transparent",
  boxShadow: "none",
  overflow: "hidden",
  "& .ag-root-wrapper": {
    minHeight: "100%",
    height: "100%",
    border: "none",
    borderRadius: "8px !important",
    background: "transparent",
    overflow: "hidden",
  },
  "& .ag-root, & .ag-root-wrapper-body, & .ag-root-wrapper-body .ag-root": {
    borderRadius: "8px",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: gridFontFamily,
    background: "transparent",
  },
  "& .ag-header": {
    backgroundColor: "rgba(3,7,18,0.9)",
    borderBottom: "1px solid rgba(148,163,184,0.25)",
  },
  "& .ag-header-cell-label": {
    color: MAGIC_UI.textBright,
    fontWeight: 600,
    letterSpacing: 0.3,
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
  "& .ag-row.onboarding-target-row-selected": {
    backgroundColor: "rgba(125,211,252,0.16) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.38)",
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
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-wrapper": {
    width: "100%",
    padding: 0,
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight .ag-cell-value": {
    width: "100%",
  },
  "& .ag-center-cols-container .ag-cell.onboarding-progress-status-cell, & .ag-pinned-left-cols-container .ag-cell.onboarding-progress-status-cell, & .ag-pinned-right-cols-container .ag-cell.onboarding-progress-status-cell": {
    justifyContent: "center",
    paddingTop: 0,
    paddingBottom: 0,
    paddingLeft: "2px",
    paddingRight: "2px",
  },
  "& .ag-center-cols-container .ag-cell.onboarding-progress-status-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell.onboarding-progress-status-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell.onboarding-progress-status-cell .ag-cell-wrapper": {
    justifyContent: "center",
  },
  "@keyframes onboardingProgressSpin": {
    from: { transform: "rotate(0deg)" },
    to: { transform: "rotate(360deg)" },
  },
  "@keyframes onboardingProgressPulse": {
    "0%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0.28)" },
    "70%": { boxShadow: "0 0 0 10px rgba(125, 201, 255, 0)" },
    "100%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0)" },
  },
};

const TAB_SECTION_SX = {
  width: "100%",
  display: "flex",
  flexDirection: "column",
  gap: 2,
  px: { xs: 1.5, md: 2 },
  py: { xs: 1.25, md: 1.75 },
};

const SCOPE_TAB_SECTION_SX = {
  ...TAB_SECTION_SX,
  flexGrow: 1,
  minHeight: 0,
  pb: { xs: 1.5, md: 2 },
};

const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg: "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

const TABS_SX = {
  borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
  minHeight: 32,
  "& .MuiTabs-flexContainer": {
    minHeight: 32,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    textTransform: "none",
    fontWeight: 400,
    fontSize: "0.8rem",
    minHeight: 32,
    height: 32,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.icon,
    },
    "&:hover": {
      background: NAV_TAB_COLORS.hover,
    },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "& .MuiTab-iconWrapper": {
      color: NAV_TAB_COLORS.iconActive,
    },
  },
};

const FIELD_SX = {
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
};

const PRIMARY_BUTTON_SX = {
  borderRadius: 999,
  color: "#06101d",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  textTransform: "none",
  fontWeight: 700,
  "&:hover": {
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
  },
};

const STATUS_THEME = {
  pending: { label: "Pending", text: "#fbbf24", background: "rgba(251,191,36,0.18)", border: "1px solid rgba(251,191,36,0.35)", dot: "#f59e0b" },
  pending_approval: { label: "Pending Approval", text: "#d8b4fe", background: "rgba(192,132,252,0.18)", border: "1px solid rgba(192,132,252,0.4)", dot: "#c084fc" },
  running: { label: "Running", text: "#7dd3fc", background: "rgba(125,211,252,0.18)", border: "1px solid rgba(125,211,252,0.4)", dot: "#38bdf8" },
  waiting_approval: { label: "Waiting Approval", text: "#d8b4fe", background: "rgba(192,132,252,0.18)", border: "1px solid rgba(192,132,252,0.4)", dot: "#c084fc" },
  approved: { label: "Approved", text: "#60a5fa", background: "rgba(96,165,250,0.16)", border: "1px solid rgba(96,165,250,0.4)", dot: "#60a5fa" },
  completed: { label: "Completed", text: "#34d399", background: "rgba(52,211,153,0.18)", border: "1px solid rgba(52,211,153,0.42)", dot: "#34d399" },
  failed: { label: "Failed", text: "#fb7185", background: "rgba(251,113,133,0.18)", border: "1px solid rgba(251,113,133,0.45)", dot: "#fb7185" },
  ssh_unreachable: { label: "Unreachable", text: "#fb7185", background: "rgba(251,113,133,0.18)", border: "1px solid rgba(251,113,133,0.45)", dot: "#fb7185" },
  unreachable: { label: "Unreachable", text: "#fb7185", background: "rgba(251,113,133,0.18)", border: "1px solid rgba(251,113,133,0.45)", dot: "#fb7185" },
  skipped: { label: "Skipped", text: "#fbbf24", background: "rgba(251,191,36,0.14)", border: "1px solid rgba(251,191,36,0.32)", dot: "#f59e0b" },
  already_enrolled: { label: "Already Enrolled", text: "#fbbf24", background: "rgba(251,191,36,0.14)", border: "1px solid rgba(251,191,36,0.32)", dot: "#f59e0b" },
  already_pending: { label: "Already Pending", text: "#fbbf24", background: "rgba(251,191,36,0.14)", border: "1px solid rgba(251,191,36,0.32)", dot: "#f59e0b" },
  unsupported_os: { label: "Unsupported OS", text: "#fbbf24", background: "rgba(251,191,36,0.14)", border: "1px solid rgba(251,191,36,0.32)", dot: "#f59e0b" },
  denied: { label: "Denied", text: "#9aa0a6", background: "rgba(148,163,184,0.12)", border: "1px solid rgba(148,163,184,0.25)", dot: "#94a3b8" },
  expired: { label: "Expired", text: "#9aa0a6", background: "rgba(148,163,184,0.12)", border: "1px solid rgba(148,163,184,0.25)", dot: "#94a3b8" },
};

const SELECT_MENU_PROPS = {
  PaperProps: {
    sx: {
      bgcolor: "rgba(8,12,24,0.96)",
      color: MAGIC_UI.textBright,
      border: `1px solid ${MAGIC_UI.panelBorder}`,
    },
  },
};

const TARGET_AUTO_SIZE_COLUMNS = [];
const PROGRESSION_AUTO_SIZE_COLUMNS = ["startedLabel", "elapsedLabel"];
const AG_GRID_STANDARD_ROW_HEIGHT = 44;
const AG_GRID_STANDARD_HEADER_HEIGHT = 44;
const STATUS_ICON_BOX_SIZE = 34;
const STATUS_ICON_SIZE = 26;
const STATUS_COLUMN_SIZE = AG_GRID_STANDARD_ROW_HEIGHT;
const TARGET_STATUS_FILTER_OPTIONS = [
  { key: "pending_approval", label: "Pending Approval" },
  { key: "skipped", label: "Skipped" },
  { key: "failed", label: "Failed" },
  { key: "completed", label: "Completed" },
  { key: "unreachable", label: "Unreachable" },
];

const FILTER_LABEL_SX = {
  color: "#58a6ff",
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

const SCOPE_DISCOVERY_EXAMPLE = `# Core Linux Servers
192.168.3.10
192.168.3.20-192.168.3.30 # application nodes

# Lab Network
192.168.50.0/24

# Named Hosts
nas01.lab.local
build01.lab.local`;

const SCOPE_EXCLUSION_EXAMPLE = `# Server Network
192.168.3.0/24
192.168.3.252 # Borealis Engine

# LAN
10.0.0.0/24

# Default Gateways
192.168.3.1
10.0.0.1`;

const SCOPE_MONO_FONT = 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace';
const SCOPE_TEXT_SX = {
  m: 0,
  p: "18px 14px 14px",
  minHeight: 214,
  width: "100%",
  fontFamily: SCOPE_MONO_FONT,
  fontSize: 13,
  lineHeight: 1.55,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  tabSize: 2,
};

function toScopeText(entries) {
  if (Array.isArray(entries)) {
    return entries.map((entry) => String(entry ?? "")).join("\n");
  }
  return String(entries ?? "");
}

function stripScopeComment(line) {
  const text = String(line || "");
  const hashIndex = text.indexOf("#");
  return hashIndex >= 0 ? text.slice(0, hashIndex) : text;
}

function splitScopeTargetTokens(value) {
  return String(value || "")
    .split(/\r?\n/)
    .flatMap((line) => stripScopeComment(line).split(/[;,]+/))
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function splitScopeEntries(value) {
  const text = String(value ?? "").replace(/\r\n?/g, "\n");
  return text ? text.split("\n") : [];
}

function renderScopeHighlightedText(value, muted = false) {
  const lines = String(value || "").split(/\r?\n/);
  return lines.map((line, index) => {
    const hashIndex = line.indexOf("#");
    const hasComment = hashIndex >= 0;
    const beforeComment = hasComment ? line.slice(0, hashIndex) : line;
    const commentText = hasComment ? line.slice(hashIndex) : "";
    const isCommentLine = beforeComment.trim() === "" && hasComment;
    const entryColor = muted ? "rgba(226,232,240,0.56)" : "#e6edf3";
    const commentColor = muted ? "rgba(125,211,252,0.48)" : "rgba(125,211,252,0.78)";
    return (
      <React.Fragment key={`${index}-${line}`}>
        {isCommentLine ? (
          <Box component="span" sx={{ color: commentColor, fontStyle: "italic" }}>
            {line}
          </Box>
        ) : (
          <>
            <Box component="span" sx={{ color: entryColor }}>
              {beforeComment}
            </Box>
            {hasComment ? (
              <Box component="span" sx={{ color: commentColor, fontStyle: "italic" }}>
                {commentText}
              </Box>
            ) : null}
          </>
        )}
        {index < lines.length - 1 ? "\n" : null}
      </React.Fragment>
    );
  });
}

function ScopeEditor({ label, value, onChange, example }) {
  const hasValue = String(value || "").length > 0;
  const displayValue = hasValue ? value : example;
  return (
    <Box sx={{ flex: "1 1 0", minWidth: 0, minHeight: 0, position: "relative", pt: 0.75, display: "flex", flexDirection: "column" }}>
      <Typography
        component="label"
        variant="caption"
        sx={{
          position: "absolute",
          top: 0,
          left: 12,
          zIndex: 3,
          px: 0.5,
          color: MAGIC_UI.textMuted,
          bgcolor: "#070b1a",
          lineHeight: 1,
        }}
      >
        {label}
      </Typography>
      <Box
        sx={{
          position: "relative",
          border: "1px solid rgba(148,163,184,0.35)",
          borderRadius: 2,
          bgcolor: "rgba(5,9,18,0.85)",
          overflow: "hidden",
          minHeight: 246,
          flexGrow: 1,
          "&:hover": {
            borderColor: MAGIC_UI.accentA,
          },
          "&:focus-within": {
            borderColor: MAGIC_UI.accentB,
            boxShadow: "0 0 0 1px rgba(192,132,252,0.3)",
          },
        }}
      >
        <Box
          component="pre"
          aria-hidden="true"
          sx={{
            ...SCOPE_TEXT_SX,
            pointerEvents: "none",
            color: "#e6edf3",
          }}
        >
          {renderScopeHighlightedText(displayValue, !hasValue)}
        </Box>
        <Box
          component="textarea"
          value={value}
          onChange={onChange}
          spellCheck={false}
          aria-label={label}
          sx={{
            ...SCOPE_TEXT_SX,
            position: "absolute",
            inset: 0,
            border: 0,
            outline: 0,
            resize: "none",
            minHeight: "100%",
            color: "transparent",
            WebkitTextFillColor: "transparent",
            caretColor: MAGIC_UI.textBright,
            bgcolor: "transparent",
            "&::selection": {
              backgroundColor: "rgba(125,211,252,0.28)",
            },
          }}
        />
      </Box>
    </Box>
  );
}

function formatStatusLabel(status) {
  const key = String(status || "").trim().toLowerCase();
  return STATUS_THEME[key]?.label || key.replace(/_/g, " ").replace(/\b\w/g, (char) => char.toUpperCase()) || "Pending";
}

function targetStatusBucket(status) {
  const key = String(status || "pending").trim().toLowerCase();
  if (key === "ssh_unreachable" || key === "unreachable") return "unreachable";
  if (["completed", "approved", "success", "installed"].includes(key)) return "completed";
  if (["skipped", "denied", "expired", "already_enrolled", "already_pending", "unsupported_os"].includes(key)) return "skipped";
  if (["failed", "failure", "error"].includes(key)) return "failed";
  return "pending_approval";
}

function targetVisibleForStatusFilter(row, activeFilter) {
  const bucket = targetStatusBucket(row?.status);
  if (activeFilter) {
    return bucket === activeFilter;
  }
  return bucket !== "unreachable";
}

function datetimeLocalValue(epochSeconds) {
  if (!epochSeconds) return "";
  const date = new Date(Number(epochSeconds) * 1000);
  if (Number.isNaN(date.getTime())) return "";
  const offsetMs = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16);
}

function isoFromDatetimeLocal(value) {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return date.toISOString();
}

function defaultStartDateTime() {
  return dayjs().add(5, "minute").second(0);
}

function dateTimePickerValue(value) {
  const text = String(value || "").trim();
  const parsed = text ? dayjs(text).second(0) : defaultStartDateTime();
  return parsed?.isValid?.() ? parsed : defaultStartDateTime();
}

function datetimeLocalValueFromDayjs(value) {
  const parsed = value?.second ? value.second(0) : dayjs(value).second(0);
  return parsed?.isValid?.() ? parsed.format("YYYY-MM-DDTHH:mm") : "";
}

function targetOutputContent(target, mode) {
  return String(mode === "stderr" ? target?.stderr_snippet || "" : target?.stdout_snippet || "").trim();
}

function onboardingFailureKind(record) {
  const raw = record?.raw || record || {};
  const text = [
    raw.detail,
    raw.task,
    raw.stdout_snippet,
    raw.stderr_snippet,
    raw.stdout,
    raw.stderr,
  ].map((value) => String(value || "")).join("\n").toLowerCase();
  if (!text) return "";
  if (
    text.includes("status_logon_failure") ||
    text.includes("logon failure") ||
    text.includes("attempted logon is invalid") ||
    text.includes("bad username") ||
    text.includes("authentication information") ||
    text.includes("smb login: smb_connection_failed") ||
    text.includes("ssh authentication failed") ||
    text.includes("permission_denied") ||
    text.includes("ssh_password_required") ||
    text.includes("sudo_password_required")
  ) {
    return "auth";
  }
  return "";
}

function isOnboardingApprovalReadyTask(task) {
  return String(task || "").trim() === "Agent Ready and Awaiting Approval";
}

function isOnboardingApprovalCompleteTask(task) {
  return String(task || "").trim() === "Device Approved > Onboarding Complete";
}

function normalizeOnboardingProgressTask(task, status = "") {
  const original = String(task || "").trim();
  const normalizedTask = original.toLowerCase();
  const normalizedStatus = String(status || "").trim().toLowerCase();
  const isApprovalReady = normalizedStatus === "waiting_approval" || normalizedTask.includes("awaiting approval");
  const isApprovalComplete = ["approved", "completed", "complete", "success", "succeeded", "installed"].includes(normalizedStatus);
  if (
    (isApprovalReady && isApprovalComplete) ||
    normalizedTask.includes("enrollment approved") ||
    normalizedTask.includes("onboarding completed") ||
    normalizedTask.includes("device approved")
  ) {
    return "Device Approved > Onboarding Complete";
  }
  if (isApprovalReady) {
    return "Agent Ready and Awaiting Approval";
  }
  if (normalizedTask.includes("already enrolled") || normalizedTask.includes("already active")) {
    return "Already Enrolled and Active";
  }
  if (normalizedTask.includes("unable to repair agent") || normalizedTask.includes("re-deploying")) {
    return "Unable to Repair Agent > Re-Deploying";
  }
  if (normalizedTask.includes("successfully repaired agent") || normalizedTask.includes("agent repaired")) {
    return "Successfully Repaired Agent";
  }
  if (normalizedTask.includes("existing agent detected") || normalizedTask.includes("existing borealis agent")) {
    return "Existing Agent Detected";
  }
  if (
    normalizedTask.includes("waiting for onboarding work") ||
    normalizedTask.includes("trying windows remote enrollment") ||
    normalizedTask.includes("preparing clean onboarding workspace") ||
    normalizedTask.includes("cleaning stale onboarding processes")
  ) {
    return "Spinning-Up Site-Worker Container";
  }
  if (normalizedTask.includes("establishing connection") || normalizedTask.includes("trying windows smb service")) {
    return "Establishing Connection to Remote Device";
  }
  if (normalizedTask.includes("using smb service") || normalizedTask.includes("smb service connection established")) {
    return "Connection Established using SMB Service";
  }
  if (
    (normalizedTask.includes("scheduled task") && normalizedTask.includes("connection established")) ||
    normalizedTask.includes("establishing scheduled task")
  ) {
    return "Connection Established using Scheduled Task";
  }
  if (normalizedTask.includes("wmi") || normalizedTask.includes("dcom")) {
    return "Connection Established using WMI/DCOM";
  }
  if (normalizedTask.includes("winrm")) {
    return "Connection Established using WinRM";
  }
  if (
    normalizedTask.includes("transferring agent service bootstrapper") ||
    normalizedTask.includes("transferring agent installation files") ||
    normalizedTask.includes("uploading agent service bootstrapper")
  ) {
    return "Uploading Agent Service Bootstrapper to Remote Device";
  }
  if (normalizedTask.includes("creating windows service")) {
    if (normalizedTask.includes("using")) {
      return original;
    }
    return "Creating Windows Service to One-Shot Bootstrap Agent using SMB Service";
  }
  if (normalizedTask.includes("ensuring windows service")) {
    return "Ensuring Windows Service is Running";
  }
  if (
    normalizedTask.includes("downloading agent bootstrap") ||
    normalizedTask.includes("deploying borealis agent runtime") ||
    normalizedTask.includes("running agent service bootstrapper") ||
    normalizedTask.includes("syncing borealis repository") ||
    normalizedTask.includes("running agent bootstrap")
  ) {
    return "Running Agent Bootstrap";
  }
  if (normalizedTask.includes("dependency") || normalizedTask.includes("ensuring agent dependencies")) {
    return "Installing Agent Dependencies";
  }
  return original || "Spinning-Up Site-Worker Container";
}

function mergeOutputSnippet(first, second) {
  const left = String(first || "").trim();
  const right = String(second || "").trim();
  if (!left) return right;
  if (!right) return left;
  if (left.includes(right)) return left;
  if (right.includes(left)) return right;
  return `${left}\n\n${right}`;
}

function epochToMs(value) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) return 0;
  return parsed > 1_000_000_000_000 ? parsed : parsed * 1000;
}

function formatOnboardingProgressTimestamp(value) {
  const date = new Date(Number(value || 0));
  if (Number.isNaN(date.getTime())) return "-";
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const year = date.getFullYear();
  const suffix = date.getHours() >= 12 ? "PM" : "AM";
  const hour = date.getHours() % 12 || 12;
  const minute = String(date.getMinutes()).padStart(2, "0");
  return `${month}-${day}-${year} @ ${hour}:${minute}${suffix}`;
}

function formatElapsedDuration(startedAt, endedAt) {
  const start = Number(startedAt || 0);
  const end = Number(endedAt || 0);
  if (!Number.isFinite(start) || !Number.isFinite(end) || start <= 0 || end <= 0) return "-";
  const totalSeconds = Math.max(0, Math.floor((end - start) / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function targetProgressionKey(row) {
  const raw = row?.raw || row || {};
  return String(
    row?.id ||
      raw.id ||
      `${raw.run_id || ""}:${raw.target_hostname || raw.target_address || raw.target_input || row?.targetLabel || ""}`
  );
}

function normalizeDiscoveredDeviceName(value) {
  return String(value || "").trim().toLowerCase();
}

function buildDiscoveredDeviceLabel(target) {
  const address = String(target?.target_address || target?.hostname || target?.target_input || "").trim();
  const hostname = String(target?.target_hostname || target?.discovered_hostname || "").trim();
  const base = address || hostname || "Target";
  const port = target?.ssh_port ? `:${target.ssh_port}` : "";
  const showHostname = Boolean(
    hostname &&
      normalizeDiscoveredDeviceName(hostname) !== normalizeDiscoveredDeviceName(address) &&
      normalizeDiscoveredDeviceName(hostname) !== normalizeDiscoveredDeviceName(base)
  );
  return {
    primary: `${base}${port}`,
    hostname: showHostname ? hostname : "",
  };
}

function statusIsActiveProgress(status) {
  const normalized = String(status || "").trim().toLowerCase();
  return ["pending", "pending_approval", "running", "in_progress", "waiting_approval"].includes(normalized);
}

function normalizeBranchName(value) {
  return String(value || DEFAULT_BRANCH).trim() || DEFAULT_BRANCH;
}

function normalizeTabToken(value) {
  return String(value || "").trim().toLowerCase();
}

function resolveOnboardingTabKey(search, allowedKeys, defaultKey) {
  const fallbackKey = String(defaultKey || "name").trim() || "name";
  const allowed = Array.isArray(allowedKeys) ? allowedKeys : [];
  const params = new URLSearchParams(String(search || ""));
  const rawValue = normalizeTabToken(params.get("tab"));
  const decodedValue = ONBOARDING_TAB_KEY_BY_URL[rawValue] || rawValue || fallbackKey;
  return allowed.length && !allowed.includes(decodedValue) ? fallbackKey : decodedValue;
}

function replaceOnboardingTabQuery(nextKey) {
  if (typeof window === "undefined") return;
  const serializedKey = normalizeTabToken(ONBOARDING_TAB_URL_BY_KEY[nextKey] || nextKey);
  if (!serializedKey) return;
  const url = new URL(window.location.href);
  if (normalizeTabToken(url.searchParams.get("tab")) === serializedKey) return;
  url.searchParams.set("tab", serializedKey);
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
}

function SectionHeader({ title, detail, action }) {
  return (
    <Box sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
      <Box sx={{ minWidth: 0 }}>
        <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700, fontSize: "1rem" }}>
          {title}
        </Typography>
        {detail ? (
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, mt: 0.4 }}>
            {detail}
          </Typography>
        ) : null}
      </Box>
      {action || null}
    </Box>
  );
}

function StatusPill({ status }) {
  const key = String(status || "pending").trim().toLowerCase() || "pending";
  const theme = STATUS_THEME[key] || STATUS_THEME.pending;
  const label = STATUS_THEME[key]?.label || formatStatusLabel(key);
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
        background: theme.background,
        border: theme.border,
        color: theme.text,
        fontWeight: 700,
        fontSize: 12,
        textTransform: "uppercase",
        lineHeight: 1,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(8,12,24,0.65)",
        }}
      />
      {label}
    </Box>
  );
}

function ProgressStatusIcon({ status, failureKind = "" }) {
  const key = String(status || "pending").trim().toLowerCase() || "pending";
  const requestedAuthFailure = String(failureKind || "").trim().toLowerCase() === "auth";
  const completeStatuses = new Set(["approved", "completed", "complete", "success", "succeeded"]);
  const activeStatuses = new Set(["pending", "pending_approval", "running", "in_progress", "waiting_approval"]);
  const waitingStatuses = new Set(["pending_approval", "waiting_approval"]);
  const failedStatuses = new Set(["failed", "error", "ssh_unreachable", "unreachable", "timeout"]);
  const skippedStatuses = new Set(["skipped", "already_enrolled", "already_pending", "unsupported_os", "denied", "expired"]);
  const isComplete = completeStatuses.has(key);
  const isActive = activeStatuses.has(key);
  const isWaiting = waitingStatuses.has(key);
  const isFailed = failedStatuses.has(key);
  const isSkipped = skippedStatuses.has(key);
  const isAuthFailure = requestedAuthFailure && isFailed;
  const label = isAuthFailure ? "Authentication Failed" : (STATUS_THEME[key]?.label || formatStatusLabel(key));
  const Icon = isAuthFailure
    ? ProgressAuthErrorIcon
    : isComplete
    ? ProgressCompleteIcon
    : isActive
      ? ProgressActiveIcon
      : isWaiting
        ? ProgressPendingIcon
        : isFailed
          ? ProgressErrorIcon
          : isSkipped
            ? ProgressSkippedIcon
            : ProgressPendingIcon;
  const color = isComplete
    ? MAGIC_UI.accentC
    : isActive
      ? (isWaiting ? MAGIC_UI.accentC : MAGIC_UI.accentA)
      : isWaiting
        ? MAGIC_UI.accentC
        : isFailed
          ? MAGIC_UI.danger
          : isSkipped
            ? STATUS_THEME.skipped.dot
            : MAGIC_UI.textMuted;

  return (
    <Tooltip title={label}>
      <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%" }}>
        <Box
          component="span"
          sx={{
            width: STATUS_ICON_BOX_SIZE,
            height: STATUS_ICON_BOX_SIZE,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            color,
            borderRadius: "50%",
            ...(isActive
              ? {
                  background: isWaiting ? "rgba(52, 211, 153, 0.12)" : "rgba(125, 201, 255, 0.12)",
                  animation: "onboardingProgressPulse 1.8s ease-out infinite",
                }
              : null),
          }}
        >
          <Icon
            sx={{
              fontSize: STATUS_ICON_SIZE,
              ...(isActive ? { animation: "onboardingProgressSpin 1.15s linear infinite" } : null),
            }}
          />
        </Box>
      </Box>
    </Tooltip>
  );
}

export default function CreateOnboardingJob() {
  const location = useLocation();
  const navigate = useNavigate();
  const params = useParams();
  const jobId = params?.jobId ? Number(params.jobId) : null;
  const editing = Number.isInteger(jobId) && jobId > 0;
  const [sites, setSites] = useState([]);
  const [credentials, setCredentials] = useState([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [targetRows, setTargetRows] = useState([]);
  const [branchRows, setBranchRows] = useState([]);
  const [branchesLoading, setBranchesLoading] = useState(false);
  const [branchLoadError, setBranchLoadError] = useState("");
  const [actioningApprovalId, setActioningApprovalId] = useState("");
  const [outputOpen, setOutputOpen] = useState(false);
  const [outputTitle, setOutputTitle] = useState("");
  const [outputSections, setOutputSections] = useState([]);
  const [copiedOutputKey, setCopiedOutputKey] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [targetStatusFilter, setTargetStatusFilter] = useState("");
  const [selectedTargetId, setSelectedTargetId] = useState("");
  const [progressionClock, setProgressionClock] = useState(() => Date.now());
  const targetGridApiRef = useRef(null);
  const progressionGridApiRef = useRef(null);
  const [form, setForm] = useState({
    name: "",
    siteId: "",
    scope: "",
    exclusionScope: "",
    agentPlatform: "linux",
    credentialId: "",
    branch: DEFAULT_BRANCH,
    sshPort: DEFAULT_SSH_PORT,
    windowsPort: DEFAULT_WINDOWS_PORT,
    winrmPort: DEFAULT_WINRM_PORT,
    onboardingConcurrency: DEFAULT_ONBOARDING_CONCURRENCY,
    scheduleType: "immediately",
    start: "",
    enabled: true,
  });

  const selectedSite = useMemo(
    () => sites.find((site) => String(site.id) === String(form.siteId)) || null,
    [form.siteId, sites]
  );

  const storedCredentials = useMemo(
    () => credentials.filter((credential) => {
      const connectionType = String(credential.connection_type || "").toLowerCase();
      if (form.agentPlatform === "windows") {
        return connectionType === "windows" || connectionType === "winrm";
      }
      return connectionType === "ssh";
    }),
    [credentials, form.agentPlatform]
  );
  const credentialOptions = useMemo(() => {
    if (!form.credentialId || storedCredentials.some((credential) => String(credential.id) === String(form.credentialId))) {
      return storedCredentials;
    }
    const selected = credentials.find((credential) => String(credential.id) === String(form.credentialId));
    return selected ? [selected, ...storedCredentials] : storedCredentials;
  }, [credentials, form.credentialId, storedCredentials]);

  const branchOptions = useMemo(() => {
    const currentBranch = normalizeBranchName(form.branch);
    const rows = Array.isArray(branchRows) ? branchRows : [];
    if (rows.some((branch) => branch.name === currentBranch)) {
      return rows;
    }
    return [
      {
        name: currentBranch,
        sha: "",
        protected: false,
        default: currentBranch === DEFAULT_BRANCH,
      },
      ...rows,
    ];
  }, [branchRows, form.branch]);

  const tabDefs = useMemo(() => {
    const tabs = [
      { key: "name", label: "Job Name", icon: DriveFileRenameOutlineIcon },
      { key: "scope", label: "Scope", icon: TravelExploreIcon },
      { key: "context", label: "Connection Method", icon: SettingsApplicationsRoundedIcon },
      { key: "schedule", label: "Schedule", icon: ScheduleRoundedIcon },
    ];
    if (editing) {
      tabs.push({ key: "targets", label: "Discovered Devices", icon: DevicesRoundedIcon });
    }
    return tabs;
  }, [editing]);

  const allowedTabKeys = useMemo(() => tabDefs.map((tabDef) => tabDef.key), [tabDefs]);
  const fallbackTabKey = tabDefs[0]?.key || "name";
  const [activeTabKey, setActiveTabKey] = useState(() =>
    resolveOnboardingTabKey(location.search, allowedTabKeys, fallbackTabKey)
  );

  useEffect(() => {
    const nextKey = resolveOnboardingTabKey(location.search, allowedTabKeys, fallbackTabKey);
    setActiveTabKey(nextKey);
  }, [allowedTabKeys, fallbackTabKey, jobId, location.pathname, location.search]);

  const selectTabKey = useCallback(
    (nextTabKey) => {
      const normalizedKey = String(nextTabKey || "").trim();
      if (!normalizedKey || !allowedTabKeys.includes(normalizedKey)) return;
      setActiveTabKey(normalizedKey);
      replaceOnboardingTabQuery(normalizedKey);
    },
    [allowedTabKeys]
  );

  const setField = useCallback((key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  }, []);

  const setScheduleType = useCallback((nextScheduleType) => {
    const normalizedType = String(nextScheduleType || "immediately").trim() || "immediately";
    setForm((prev) => ({
      ...prev,
      scheduleType: normalizedType,
      start:
        normalizedType === "immediately"
          ? prev.start
          : prev.start || datetimeLocalValueFromDayjs(defaultStartDateTime()),
    }));
  }, []);

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
            default: name === DEFAULT_BRANCH,
          });
        });
        if (pageRows.length < 100) {
          break;
        }
      }
      nextRows.sort((a, b) => {
        if (a.name === DEFAULT_BRANCH) return -1;
        if (b.name === DEFAULT_BRANCH) return 1;
        return a.name.localeCompare(b.name);
      });
      setBranchRows(nextRows);
      if (!nextRows.length) {
        setBranchLoadError("No GitHub branches returned.");
      }
    } catch (err) {
      setBranchRows([]);
      setBranchLoadError(err instanceof Error ? err.message : "GitHub branch lookup failed.");
    } finally {
      setBranchesLoading(false);
    }
  }, []);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [sitesResp, credentialsResp, jobResp] = await Promise.all([
        fetch("/api/sites", { credentials: "include" }),
        fetch("/api/credentials", { credentials: "include" }),
        editing ? fetch(`/api/scheduled_jobs/${jobId}`, { credentials: "include" }) : Promise.resolve(null),
      ]);
      const sitesData = await sitesResp.json().catch(() => ({}));
      if (!sitesResp.ok) throw new Error(sitesData?.error || `Unable to load sites (${sitesResp.status})`);
      const credentialsData = await credentialsResp.json().catch(() => ({}));
      if (!credentialsResp.ok) throw new Error(credentialsData?.message || credentialsData?.error || `Unable to load credentials (${credentialsResp.status})`);
      setSites(Array.isArray(sitesData?.sites) ? sitesData.sites : []);
      setCredentials(Array.isArray(credentialsData?.credentials) ? credentialsData.credentials : []);

      if (editing && jobResp) {
        const jobData = await jobResp.json().catch(() => ({}));
        if (!jobResp.ok) throw new Error(jobData?.error || `Unable to load onboarding job (${jobResp.status})`);
        const job = jobData?.job || {};
        const firstTarget = Array.isArray(job.targets) ? job.targets.find((target) => target?.kind === "onboarding_scope") : null;
        const firstComponent = Array.isArray(job.components) ? job.components[0] || {} : {};
        const componentPlatform = String(
          firstComponent.agent_platform ||
            firstComponent.target_os ||
            firstComponent.platform ||
            firstComponent.os ||
            "linux"
        ).toLowerCase();
        const agentPlatform = ["windows", "winrm", "smb", "windows_remote"].includes(componentPlatform) ? "windows" : "linux";
        setForm({
          name: job.name || "",
          siteId: firstTarget?.site_id ? String(firstTarget.site_id) : "",
          scope: toScopeText(firstTarget?.entries || []),
          exclusionScope: toScopeText(firstTarget?.exclusions || firstTarget?.exclude_entries || []),
          agentPlatform,
          credentialId: job.credential_id ? String(job.credential_id) : "",
          branch: firstComponent.install_branch || firstComponent.repo_branch || firstComponent.branch || DEFAULT_BRANCH,
          sshPort: Number(firstComponent.ssh_port || firstComponent.port || DEFAULT_SSH_PORT),
          windowsPort: Number(firstComponent.windows_port || firstComponent.smb_port || firstComponent.port || DEFAULT_WINDOWS_PORT),
          winrmPort: Number(firstComponent.winrm_port || firstComponent.windows_winrm_port || DEFAULT_WINRM_PORT),
          onboardingConcurrency: Number(
            firstComponent.onboarding_concurrency ||
              firstComponent.device_onboarding_concurrency ||
              firstComponent.concurrency ||
              DEFAULT_ONBOARDING_CONCURRENCY
          ),
          scheduleType: job.schedule_type || "immediately",
          start: datetimeLocalValue(job.start_ts),
          enabled: Boolean(job.enabled),
        });
        setNameTouched(Boolean(job.name));
      }
    } catch (err) {
      setError(err?.message || "Unable to load onboarding form.");
    } finally {
      setLoading(false);
    }
  }, [editing, jobId]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  useEffect(() => {
    fetchInstallBranches();
  }, [fetchInstallBranches]);

  useEffect(() => {
    setSelectedTargetId("");
  }, [jobId]);

  const loadTargets = useCallback(async () => {
    if (!editing || !jobId) return;
    try {
      const resp = await fetch(`/api/onboarding/jobs/${jobId}/targets`, { credentials: "include" });
      const data = await resp.json().catch(() => ({}));
      if (resp.ok) {
        setTargetRows(Array.isArray(data?.targets) ? data.targets : []);
      }
    } catch {
      /* status refresh is best effort */
    }
  }, [editing, jobId]);

  useEffect(() => {
    if (!editing) return undefined;
    loadTargets();
    const timer = setInterval(loadTargets, 5000);
    return () => clearInterval(timer);
  }, [editing, loadTargets]);

  const copyTextToClipboard = useCallback(async (value, promptTitle = "Copy text") => {
    const normalizedValue = String(value ?? "");
    if (!normalizedValue) return false;
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

  const targetGridRows = useMemo(
    () => targetRows.map((target) => {
      const discoveredDevice = buildDiscoveredDeviceLabel(target);
      const targetLabel = discoveredDevice.primary;
      const status = String(target.status || "pending").trim().toLowerCase() || "pending";
      const approvalStatus = String(target.approval_status || "").trim().toLowerCase();
      return {
        id: target.id || `${targetLabel}-${target.run_id || ""}`,
        targetLabel,
        discoveredHostname: discoveredDevice.hostname,
        status,
        statusBucket: targetStatusBucket(status),
        statusLabel: formatStatusLabel(status),
        detail: target.detail || "",
        failureKind: onboardingFailureKind(target),
        approvalReference: target.approval_reference || "",
        approvalId: String(target.approval_id || target.approvalId || "").trim(),
        approvalStatus,
        raw: target,
      };
    }),
    [targetRows]
  );

  const targetStatusCounts = useMemo(
    () => targetGridRows.reduce((acc, row) => {
      const bucket = row.statusBucket || targetStatusBucket(row.status);
      acc[bucket] = (acc[bucket] || 0) + 1;
      return acc;
    }, {}),
    [targetGridRows]
  );

  const visibleTargetGridRows = useMemo(
    () => targetGridRows.filter((row) => targetVisibleForStatusFilter(row, targetStatusFilter)),
    [targetGridRows, targetStatusFilter]
  );

  const selectedTargetRow = useMemo(
    () => targetGridRows.find((row) => targetProgressionKey(row) === String(selectedTargetId)) || null,
    [selectedTargetId, targetGridRows]
  );

  useEffect(() => {
    const timer = setInterval(() => setProgressionClock(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!selectedTargetId) return;
    if (targetGridRows.some((row) => targetProgressionKey(row) === String(selectedTargetId))) return;
    setSelectedTargetId("");
  }, [selectedTargetId, targetGridRows]);

  const selectedProgressionRows = useMemo(() => {
    if (!selectedTargetRow) return [];
    const rawRows = Array.isArray(selectedTargetRow.raw?.timeline)
      ? selectedTargetRow.raw.timeline
      : (Array.isArray(selectedTargetRow.raw?.events) ? selectedTargetRow.raw.events : []);
    const mappedRows = rawRows.map((event, index) => {
      const startedAt = epochToMs(event?.started_at ?? event?.startedAt);
      const endedAt = epochToMs(event?.finished_at ?? event?.finishedAt) || null;
      const status = String(event?.status || "pending").trim().toLowerCase() || "pending";
      const elapsedEnd = endedAt || (statusIsActiveProgress(status) ? progressionClock : startedAt);
      const task = normalizeOnboardingProgressTask(event?.task || "Spinning-Up Site-Worker Container", status);
      return {
        id: event?.id || `${selectedTargetRow.id || selectedTargetId}-${index}`,
        status,
        statusLabel: formatStatusLabel(status),
        task,
        startedAt,
        endedAt,
        startedLabel: formatOnboardingProgressTimestamp(startedAt),
        elapsedLabel: formatElapsedDuration(startedAt, elapsedEnd),
        failureKind: onboardingFailureKind(event),
        hasStdOut: Boolean(String(event?.stdout_snippet || "").trim()),
        hasStdErr: Boolean(String(event?.stderr_snippet || "").trim()),
        raw: event,
      };
    });
    const selectedBucket = targetStatusBucket(selectedTargetRow.status);
    return mappedRows.reduce((acc, row) => {
      if (selectedBucket === "skipped" && isOnboardingApprovalCompleteTask(row.task)) {
        return acc;
      }
      const previous = acc[acc.length - 1];
      if (isOnboardingApprovalCompleteTask(row.task)) {
        let readyIndex = -1;
        for (let index = acc.length - 1; index >= 0; index -= 1) {
          if (isOnboardingApprovalReadyTask(acc[index]?.task)) {
            readyIndex = index;
            break;
          }
        }
        if (readyIndex < 0) {
          acc.push(row);
          return acc;
        }
        const previous = acc[readyIndex];
        const startedAt = previous.startedAt || row.startedAt;
        const endedAt = row.endedAt || row.startedAt || previous.endedAt;
        const status = row.status || "completed";
        const raw = {
          ...(previous.raw || {}),
          ...(row.raw || {}),
          stdout_snippet: mergeOutputSnippet(previous.raw?.stdout_snippet, row.raw?.stdout_snippet),
          stderr_snippet: mergeOutputSnippet(previous.raw?.stderr_snippet, row.raw?.stderr_snippet),
        };
        const elapsedEnd = endedAt || (statusIsActiveProgress(status) ? progressionClock : startedAt);
        acc[readyIndex] = {
          ...previous,
          ...row,
          id: previous.id,
          task: row.task,
          status,
          statusLabel: formatStatusLabel(status),
          startedAt,
          endedAt,
          startedLabel: formatOnboardingProgressTimestamp(startedAt),
          elapsedLabel: formatElapsedDuration(startedAt, elapsedEnd),
          hasStdOut: Boolean(String(raw.stdout_snippet || "").trim()),
          hasStdErr: Boolean(String(raw.stderr_snippet || "").trim()),
          failureKind: onboardingFailureKind(raw),
          raw,
        };
        return acc;
      }
      if (!previous || previous.task !== row.task) {
        acc.push(row);
        return acc;
      }
      const startedAt = previous.startedAt || row.startedAt;
      const endedAt = row.endedAt || previous.endedAt;
      const status = row.status || previous.status;
      const raw = {
        ...(previous.raw || {}),
        ...(row.raw || {}),
        stdout_snippet: mergeOutputSnippet(previous.raw?.stdout_snippet, row.raw?.stdout_snippet),
        stderr_snippet: mergeOutputSnippet(previous.raw?.stderr_snippet, row.raw?.stderr_snippet),
      };
      const elapsedEnd = endedAt || (statusIsActiveProgress(status) ? progressionClock : startedAt);
      acc[acc.length - 1] = {
        ...previous,
        ...row,
        id: previous.id,
        status,
        statusLabel: formatStatusLabel(status),
        startedAt,
        endedAt,
        startedLabel: formatOnboardingProgressTimestamp(startedAt),
        elapsedLabel: formatElapsedDuration(startedAt, elapsedEnd),
        hasStdOut: Boolean(String(raw.stdout_snippet || "").trim()),
        hasStdErr: Boolean(String(raw.stderr_snippet || "").trim()),
        failureKind: onboardingFailureKind(raw),
        raw,
      };
      return acc;
    }, []);
  }, [progressionClock, selectedTargetId, selectedTargetRow]);

  const handleCopyOutputSection = useCallback(async (section) => {
    const content = String(section?.content || "");
    if (!content) {
      setError("No output available to copy.");
      return;
    }
    const copied = await copyTextToClipboard(content, `Copy ${section?.title || "Output"}`);
    if (copied) {
      setCopiedOutputKey(String(section?.key || ""));
      setNotice("Output copied.");
      setTimeout(() => setCopiedOutputKey(""), 1400);
    } else {
      setNotice("Manual copy prompt opened.");
    }
  }, [copyTextToClipboard]);

  const handleViewProgressOutput = useCallback((row, mode = "stdout") => {
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const event = row?.raw || row || {};
    const targetLabel = selectedTargetRow?.targetLabel || "Target";
    const taskLabel = row?.task || event?.task || "Onboarding Task";
    const content = targetOutputContent(event, mode);
    setOutputTitle(`${label} - ${targetLabel} - ${taskLabel}`);
    setOutputSections([
      {
        key: `${event?.id || row?.id || targetLabel}-${mode}`,
        title: label,
        path: taskLabel,
        content,
      },
    ]);
    setCopiedOutputKey("");
    setOutputOpen(true);
  }, [selectedTargetRow]);

  const handleCopyProgressOutput = useCallback(async (row, mode = "stdout") => {
    const label = mode === "stderr" ? "StdErr" : "StdOut";
    const content = targetOutputContent(row?.raw || row, mode);
    if (!content) {
      setError(`No ${label} available for this task.`);
      return;
    }
    const copied = await copyTextToClipboard(content, `Copy ${label}`);
    setNotice(copied ? `${label} copied.` : "Manual copy prompt opened.");
  }, [copyTextToClipboard]);

  const handleApproveTarget = useCallback(async (target) => {
    const approvalId = String(target?.approval_id || target?.approvalId || "").trim();
    if (!approvalId) return;
    setActioningApprovalId(approvalId);
    setError("");
    setNotice("");
    try {
      const resp = await fetch(`/api/admin/device-approvals/${encodeURIComponent(approvalId)}/approve`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const conflictMessage = body?.error === "conflict_resolution_required"
          ? "Approval needs hostname conflict resolution from Device Approvals."
          : body?.error || `Approval failed (${resp.status})`;
        throw new Error(conflictMessage);
      }
      setNotice("Enrollment approved.");
      await loadTargets();
    } catch (err) {
      setError(err?.message || "Unable to approve enrollment.");
    } finally {
      setActioningApprovalId("");
    }
  }, [loadTargets]);

  const targetGridColumnDefs = useMemo(
    () => [
      {
        field: "statusLabel",
        headerName: "",
        width: STATUS_COLUMN_SIZE,
        minWidth: STATUS_COLUMN_SIZE,
        maxWidth: STATUS_COLUMN_SIZE,
        filter: false,
        cellClass: "auto-col-tight onboarding-progress-status-cell",
        cellRenderer: (params) => <ProgressStatusIcon status={params.data?.status} failureKind={params.data?.failureKind} />,
      },
      {
        field: "targetLabel",
        headerName: "Discovered Device",
        minWidth: 180,
        flex: 1,
        filter: "agTextColumnFilter",
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const row = params.data || {};
          return (
            <Box sx={{ display: "flex", alignItems: "center", minWidth: 0, gap: 0.75 }}>
              <Typography
                component="span"
                variant="body2"
                sx={{
                  color: "#58a6ff",
                  fontWeight: 500,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {row.targetLabel || ""}
              </Typography>
              {row.discoveredHostname ? (
                <Typography component="span" variant="body2" sx={{ color: "rgba(148, 163, 184, 0.78)", flexShrink: 0 }}>
                  ({row.discoveredHostname})
                </Typography>
              ) : null}
            </Box>
          );
        },
      },
      {
        field: "actions",
        headerName: "Actions",
        width: 130,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const row = params.data;
          const rowStatus = String(row?.status || "").trim().toLowerCase();
          const approvalStatus = String(row?.approvalStatus || "").trim().toLowerCase();
          const approvalTerminal = ["approved", "completed", "success", "installed", "denied", "expired"].includes(rowStatus);
          const canApprove = Boolean(row?.approvalId)
            && (approvalStatus === "pending" || !approvalStatus)
            && !approvalTerminal;
          const canOpenApprovals = !canApprove && !approvalTerminal && (
            rowStatus === "waiting_approval" ||
            rowStatus === "pending_approval" ||
            Boolean(row?.approvalReference)
          );
          if (!canApprove && !canOpenApprovals) {
            return <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>-</Typography>;
          }
          if (canOpenApprovals) {
            return (
              <Button
                size="small"
                sx={{ color: MAGIC_UI.accentC, textTransform: "none", minWidth: 0, p: 0 }}
                onClick={(event) => {
                  event.stopPropagation();
                  navigate(APP_PATHS.deviceApprovals);
                }}
              >
                Approvals
              </Button>
            );
          }
          const busy = actioningApprovalId === row.approvalId;
          return (
            <Button
              size="small"
              startIcon={busy ? <CircularProgress size={14} /> : <CheckIcon fontSize="small" />}
              disabled={busy}
              sx={{ color: MAGIC_UI.accentC, textTransform: "none", minWidth: 0, p: 0 }}
              onClick={(event) => {
                event.stopPropagation();
                void handleApproveTarget(row);
              }}
            >
              Approve
            </Button>
          );
        },
      },
    ],
    [actioningApprovalId, handleApproveTarget, navigate]
  );

  const progressionGridColumnDefs = useMemo(
    () => [
      {
        field: "statusLabel",
        headerName: "",
        width: STATUS_COLUMN_SIZE,
        minWidth: STATUS_COLUMN_SIZE,
        maxWidth: STATUS_COLUMN_SIZE,
        filter: false,
        cellClass: "auto-col-tight onboarding-progress-status-cell",
        cellRenderer: (params) => <ProgressStatusIcon status={params.data?.status} failureKind={params.data?.failureKind} />,
      },
      { field: "task", headerName: "Task", minWidth: 260, flex: 1, filter: false, cellClass: "auto-col-tight" },
      {
        field: "output",
        headerName: "StdOut / StdErr",
        width: 198,
        minWidth: 188,
        maxWidth: 212,
        filter: false,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const row = params.data;
          if (!row) return null;
          return (
            <Box sx={{ display: "flex", alignItems: "center", gap: 0.75, flexWrap: "wrap" }}>
              {row.hasStdOut ? (
                <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                  <Button size="small" sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }} onClick={(event) => { event.stopPropagation(); handleViewProgressOutput(row, "stdout"); }}>
                    StdOut
                  </Button>
                  <Tooltip title="Copy StdOut">
                    <IconButton size="small" sx={{ color: MAGIC_UI.accentA, p: 0.35 }} onClick={(event) => { event.stopPropagation(); void handleCopyProgressOutput(row, "stdout"); }}>
                      <ContentCopyIcon sx={{ fontSize: 15 }} />
                    </IconButton>
                  </Tooltip>
                </Box>
              ) : null}
              {row.hasStdOut && row.hasStdErr ? <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>/</Typography> : null}
              {row.hasStdErr ? (
                <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.25 }}>
                  <Button size="small" sx={{ color: MAGIC_UI.danger, textTransform: "none", minWidth: 0, p: 0 }} onClick={(event) => { event.stopPropagation(); handleViewProgressOutput(row, "stderr"); }}>
                    StdErr
                  </Button>
                  <Tooltip title="Copy StdErr">
                    <IconButton size="small" sx={{ color: MAGIC_UI.danger, p: 0.35 }} onClick={(event) => { event.stopPropagation(); void handleCopyProgressOutput(row, "stderr"); }}>
                      <ContentCopyIcon sx={{ fontSize: 15 }} />
                    </IconButton>
                  </Tooltip>
                </Box>
              ) : null}
            </Box>
          );
        },
      },
      {
        field: "startedLabel",
        headerName: "Started",
        minWidth: 185,
        filter: false,
        cellClass: "auto-col-tight",
      },
      {
        field: "elapsedLabel",
        headerName: "Time Elapsed",
        width: 128,
        minWidth: 126,
        maxWidth: 140,
        filter: false,
        cellClass: "auto-col-tight",
      },
    ],
    [handleCopyProgressOutput, handleViewProgressOutput]
  );

  const targetGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: true,
    }),
    []
  );

  const progressionGridDefaultColDef = useMemo(
    () => ({
      sortable: false,
      resizable: true,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
    }),
    []
  );

  const targetGridRowSelection = useMemo(
    () => ({
      mode: "singleRow",
      checkboxes: false,
      headerCheckbox: false,
      enableClickSelection: true,
    }),
    []
  );

  const getTargetGridRowId = useCallback(
    (params) => String(params.data?.id || params.rowIndex),
    []
  );

  const autoSizeTargetGrid = useCallback(() => {
    const api = targetGridApiRef.current;
    if (!api || !visibleTargetGridRows.length) return;
    const run = () => {
      try {
        if (TARGET_AUTO_SIZE_COLUMNS.length && typeof api.autoSizeColumns === "function") {
          api.autoSizeColumns(TARGET_AUTO_SIZE_COLUMNS, true);
        }
      } catch {
        /* grid may not be ready yet */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, [visibleTargetGridRows.length]);

  const handleTargetGridReady = useCallback((params) => {
    targetGridApiRef.current = params.api;
    autoSizeTargetGrid();
  }, [autoSizeTargetGrid]);

  const autoSizeProgressionGrid = useCallback(() => {
    const api = progressionGridApiRef.current;
    if (!api || !selectedProgressionRows.length) return;
    const run = () => {
      try {
        if (typeof api.autoSizeColumns === "function") {
          api.autoSizeColumns(PROGRESSION_AUTO_SIZE_COLUMNS, true);
        }
      } catch {
        /* grid may not be ready yet */
      }
    };
    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(run);
    } else {
      setTimeout(run, 0);
    }
  }, [selectedProgressionRows.length]);

  const handleProgressionGridReady = useCallback((params) => {
    progressionGridApiRef.current = params.api;
    autoSizeProgressionGrid();
  }, [autoSizeProgressionGrid]);

  const selectTargetRow = useCallback((row) => {
    const key = targetProgressionKey(row);
    if (key) {
      setSelectedTargetId(key);
    }
  }, []);

  const handleTargetSelectionChanged = useCallback((event) => {
    const selectedRow = event?.api?.getSelectedRows?.()?.[0];
    if (selectedRow) {
      selectTargetRow(selectedRow);
    }
  }, [selectTargetRow]);

  const handleTargetRowClicked = useCallback((params) => {
    params?.node?.setSelected?.(true, true);
    selectTargetRow(params?.data);
  }, [selectTargetRow]);

  useEffect(() => {
    const api = targetGridApiRef.current;
    if (!api) return;
    const selectedKey = String(selectedTargetId || "");
    api.forEachNode((node) => {
      const matches = selectedKey && targetProgressionKey(node?.data) === selectedKey;
      if (node?.isSelected?.() !== Boolean(matches)) {
        node?.setSelected?.(Boolean(matches), false);
      }
    });
    api.redrawRows?.();
  }, [selectedTargetId, visibleTargetGridRows]);

  useEffect(() => {
    autoSizeTargetGrid();
  }, [autoSizeTargetGrid]);

  useEffect(() => {
    autoSizeProgressionGrid();
  }, [autoSizeProgressionGrid]);

  useEffect(() => {
    if (nameTouched || !selectedSite || editing) return;
    setForm((prev) => ({
      ...prev,
      name: `Automatic Device Onboarding ${selectedSite.name || `Site ${selectedSite.id}`}`,
    }));
  }, [editing, nameTouched, selectedSite]);

  const submit = useCallback(async () => {
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const entries = splitScopeEntries(form.scope);
      const exclusions = splitScopeEntries(form.exclusionScope);
      const parsedEntries = splitScopeTargetTokens(form.scope);
      if (!form.siteId) throw new Error("Select site.");
      if (!parsedEntries.length) throw new Error("Enter at least one IP address, CIDR, range, or FQDN.");
      if (!form.credentialId) throw new Error("Select stored credential.");
      const agentPlatform = form.agentPlatform === "windows" ? "windows" : "linux";
      const sshPort = Number(form.sshPort || DEFAULT_SSH_PORT);
      const windowsPort = Number(form.windowsPort || DEFAULT_WINDOWS_PORT);
      const winrmPort = Number(form.winrmPort || DEFAULT_WINRM_PORT);
      const primaryPort = agentPlatform === "windows" ? windowsPort : sshPort;
      if (!Number.isInteger(primaryPort) || primaryPort < 1 || primaryPort > 65535) throw new Error("Remote port must be 1-65535.");
      if (agentPlatform === "windows" && (!Number.isInteger(winrmPort) || winrmPort < 1 || winrmPort > 65535)) {
        throw new Error("WinRM port must be 1-65535.");
      }
      const onboardingConcurrency = Number(form.onboardingConcurrency || DEFAULT_ONBOARDING_CONCURRENCY);
      if (!Number.isInteger(onboardingConcurrency) || onboardingConcurrency < 1 || onboardingConcurrency > 100) {
        throw new Error("Device onboarding concurrency must be 1-100.");
      }
      const siteName = selectedSite?.name || "";
      const payload = {
        job_kind: "onboarding",
        name: form.name || `Automatic Device Onboarding ${siteName || form.siteId}`,
        components: [
          {
            kind: "device_onboarding",
            name: "Device Onboarding",
            agent_platform: agentPlatform,
            install_branch: form.branch || DEFAULT_BRANCH,
            ssh_port: sshPort,
            windows_port: windowsPort,
            winrm_port: winrmPort,
            onboarding_methods: agentPlatform === "windows" ? ["smb_scm", "scheduled_task", "wmi_dcom", "winrm"] : ["ssh"],
            onboarding_concurrency: onboardingConcurrency,
          },
        ],
        targets: [
          {
            kind: "onboarding_scope",
            site_id: Number(form.siteId),
            site_name: siteName,
            entries,
            exclusions,
          },
        ],
        schedule: {
          type: form.scheduleType || "immediately",
          start: form.scheduleType === "immediately" ? null : isoFromDatetimeLocal(form.start),
        },
        duration: { expiration: "no_expire" },
        execution_context: "onboarding_local_network",
        credential_id: Number(form.credentialId),
        use_service_account: false,
        enabled: Boolean(form.enabled),
      };
      const resp = await fetch(editing ? `/api/scheduled_jobs/${jobId}` : "/api/scheduled_jobs", {
        method: editing ? "PUT" : "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const body = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(body?.message || body?.error || `Save failed (${resp.status})`);
      }
      const savedId = body?.job?.id || jobId;
      if (savedId && !editing) {
        setNotice("Onboarding started.");
        navigate(APP_PATHS.jobOnboarding(savedId), { replace: true });
      } else if (editing && savedId) {
        setTargetRows([]);
        const redeployResp = await fetch(`/api/onboarding/jobs/${encodeURIComponent(savedId)}/redeploy`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        });
        const redeployBody = await redeployResp.json().catch(() => ({}));
        if (!redeployResp.ok) {
          throw new Error(redeployBody?.message || redeployBody?.error || `Re-onboard failed (${redeployResp.status})`);
        }
        setNotice("Onboarding re-run started.");
        selectTabKey("targets");
        window.setTimeout(() => {
          void loadTargets();
        }, 1200);
      } else {
        setNotice("Onboarding job saved.");
      }
    } catch (err) {
      setError(err?.message || "Unable to save onboarding job.");
    } finally {
      setSaving(false);
    }
  }, [editing, form, jobId, loadTargets, navigate, selectTabKey, selectedSite]);

  const pageHeaderActions = useMemo(
    () => {
      const actions = [
        {
          id: "onboarding-back",
          label: "Back",
          icon: <ArrowBackIcon />,
          tone: "secondary",
          onClick: () => navigate(APP_PATHS.jobs),
        },
      ];
      if (editing) {
        actions.push({
          id: "onboarding-refresh",
          label: "Refresh",
          icon: <RefreshIcon />,
          tone: "secondary",
          onClick: () => void loadTargets(),
        });
      }
      actions.push({
        id: "onboarding-save",
        label: editing ? "Re-Onboard" : "Onboard",
        icon: <PlayArrowIcon />,
        tone: "primary",
        loading: saving,
        onClick: submit,
      });
      return actions;
    },
    [editing, loadTargets, navigate, saving, submit]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: DevicesIcon,
    actions: pageHeaderActions,
  });

  if (loading) {
    return (
      <Paper elevation={0} sx={PAGE_SX}>
        <PageBodyFrame variant="content_panel">
          <CircularProgress size={24} sx={{ color: "#7dd3fc" }} />
        </PageBodyFrame>
      </Paper>
    );
  }

  return (
    <Paper elevation={0} sx={PAGE_SX}>
      <PageBodyFrame
        variant="grid_with_stack"
        stack={(
          <>
            {error ? <Alert severity="error">{error}</Alert> : null}
            {notice ? <Alert severity="success">{notice}</Alert> : null}
            {saving ? (
              <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: MAGIC_UI.textMuted }}>
                <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />
                <Typography variant="body2">{editing ? "Re-onboarding devices..." : "Onboarding devices..."}</Typography>
              </Box>
            ) : null}

            <Tabs
              value={activeTabKey}
              onChange={(_, value) => selectTabKey(value)}
              variant="scrollable"
              scrollButtons="auto"
              TabIndicatorProps={{
                style: {
                  height: 3,
                  borderRadius: 3,
                  background: NAV_TAB_COLORS.iconActive,
                },
              }}
              sx={TABS_SX}
            >
              {tabDefs.map((tabDef) => {
                const TabIcon = tabDef.icon || null;
                return (
                  <Tab
                    key={tabDef.key}
                    value={tabDef.key}
                    label={tabDef.label}
                    icon={TabIcon ? <TabIcon sx={{ fontSize: 18 }} /> : undefined}
                    iconPosition="start"
                  />
                );
              })}
            </Tabs>
          </>
        )}
      >

            <Box
              sx={{
                flexGrow: 1,
                minHeight: 0,
                height: "100%",
                display: "flex",
                flexDirection: "column",
                overflow: {
                  xs: "auto",
                  lg: activeTabKey === "scope" || activeTabKey === "targets" ? "hidden" : "auto",
                },
              }}
            >
              {activeTabKey === "name" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="Job Name" />
                  <TextField
                    label="Job Name"
                    value={form.name}
                    onChange={(event) => {
                      setNameTouched(true);
                      setField("name", event.target.value);
                    }}
                    sx={{ maxWidth: 560, ...FIELD_SX }}
                    fullWidth
                  />
                </Box>
              ) : null}

              {activeTabKey === "scope" ? (
                <Box sx={SCOPE_TAB_SECTION_SX}>
                  <SectionHeader
                    title="Scope"
                    detail="Discovery scope defines eligible targets. Exclusion scope removes blacklisted IPs, FQDNs, CIDRs, and ranges before onboarding attempts start."
                  />
                  <FormControl fullWidth sx={FIELD_SX}>
                    <InputLabel id="onboarding-site-label">Site</InputLabel>
                    <Select
                      labelId="onboarding-site-label"
                      label="Site"
                      value={form.siteId}
                      onChange={(event) => setField("siteId", event.target.value)}
                      MenuProps={SELECT_MENU_PROPS}
                    >
                      {sites.map((site) => (
                        <MenuItem key={site.id} value={String(site.id)}>
                          {site.name || `Site ${site.id}`}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                  <Stack direction={{ xs: "column", lg: "row" }} spacing={2} sx={{ flexGrow: 1, minHeight: 0, alignItems: "stretch" }}>
                    <ScopeEditor
                      label="Discovery Scope"
                      value={form.scope}
                      onChange={(event) => setField("scope", event.target.value)}
                      example={SCOPE_DISCOVERY_EXAMPLE}
                    />
                    <ScopeEditor
                      label="Exclusion Scope"
                      value={form.exclusionScope}
                      onChange={(event) => setField("exclusionScope", event.target.value)}
                      example={SCOPE_EXCLUSION_EXAMPLE}
                    />
                  </Stack>
                </Box>
              ) : null}

              {activeTabKey === "context" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="Connection Method" detail="Choose a stored machine or domain credential to connect to the devices and trigger automatic enrollment." />
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                    <FormControl fullWidth sx={{ maxWidth: { md: 260 }, ...FIELD_SX }}>
                      <InputLabel id="onboarding-platform-label">Device OS</InputLabel>
                      <Select
                        labelId="onboarding-platform-label"
                        label="Device OS"
                        value={form.agentPlatform}
                        onChange={(event) => {
                          setForm((prev) => ({
                            ...prev,
                            agentPlatform: event.target.value,
                            credentialId: "",
                          }));
                        }}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        <MenuItem value="linux">Linux</MenuItem>
                        <MenuItem value="windows">Windows</MenuItem>
                      </Select>
                    </FormControl>
                    <FormControl fullWidth sx={FIELD_SX}>
                      <InputLabel id="onboarding-credential-label">Stored Credential</InputLabel>
                      <Select
                        labelId="onboarding-credential-label"
                        label="Stored Credential"
                        value={form.credentialId}
                        onChange={(event) => setField("credentialId", event.target.value)}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        {credentialOptions.map((credential) => (
                          <MenuItem
                            key={credential.id}
                            value={String(credential.id)}
                            disabled={Boolean(credential.secret_reset_required)}
                          >
                            {credential.name || `Credential ${credential.id}`}
                            {credential.secret_reset_required ? " (Secret Re-Entry Required)" : ""}
                          </MenuItem>
                        ))}
                      </Select>
                    </FormControl>
                  </Stack>
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                    <TextField
                      label={form.agentPlatform === "windows" ? "SMB Port" : "SSH Port"}
                      type="number"
                      value={form.agentPlatform === "windows" ? form.windowsPort : form.sshPort}
                      onChange={(event) => setField(form.agentPlatform === "windows" ? "windowsPort" : "sshPort", event.target.value)}
                      sx={{ width: { xs: "100%", md: 180 }, ...FIELD_SX }}
                    />
                    {form.agentPlatform === "windows" ? (
                      <TextField
                        label="WinRM Port"
                        type="number"
                        value={form.winrmPort}
                        onChange={(event) => setField("winrmPort", event.target.value)}
                        sx={{ width: { xs: "100%", md: 180 }, ...FIELD_SX }}
                      />
                    ) : null}
                    <TextField
                      label="Device Onboarding Concurrency"
                      type="number"
                      value={form.onboardingConcurrency}
                      onChange={(event) => setField("onboardingConcurrency", event.target.value)}
                      sx={{ width: { xs: "100%", md: 300 }, ...FIELD_SX }}
                    />
                  </Stack>
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2} alignItems={{ xs: "stretch", md: "center" }}>
                    <FormControl fullWidth error={Boolean(branchLoadError)} sx={FIELD_SX}>
                      <InputLabel id="onboarding-install-branch-label">Agent Install Branch</InputLabel>
                      <Select
                        labelId="onboarding-install-branch-label"
                        label="Agent Install Branch"
                        value={normalizeBranchName(form.branch)}
                        onChange={(event) => setField("branch", event.target.value)}
                        onOpen={() => {
                          if (!branchRows.length && !branchesLoading) {
                            void fetchInstallBranches();
                          }
                        }}
                        MenuProps={SELECT_MENU_PROPS}
                      >
                        {branchOptions.map((branch) => (
                          <MenuItem key={branch.name} value={branch.name}>
                            {branch.name}
                            {branch.default ? " (default)" : ""}
                            {branch.sha ? ` - ${branch.sha.slice(0, 12)}` : ""}
                          </MenuItem>
                        ))}
                        {branchesLoading ? <MenuItem disabled value="__loading">Loading branches...</MenuItem> : null}
                        {branchLoadError ? <MenuItem disabled value="__error">Branch lookup failed</MenuItem> : null}
                      </Select>
                      {branchLoadError ? (
                        <Typography variant="caption" sx={{ mt: 0.75, color: "#fca5a5" }}>
                          {branchLoadError}
                        </Typography>
                      ) : null}
                    </FormControl>
                    <Button
                      startIcon={branchesLoading ? <CircularProgress size={16} /> : <RefreshIcon fontSize="small" />}
                      disabled={branchesLoading}
                      onClick={() => void fetchInstallBranches()}
                      sx={{ ...PRIMARY_BUTTON_SX, minWidth: 132 }}
                    >
                      Refresh
                    </Button>
                  </Stack>
                </Box>
              ) : null}

              {activeTabKey === "schedule" ? (
                <Box sx={TAB_SECTION_SX}>
                  <SectionHeader title="Schedule" detail="Immediate jobs start now. Scheduled jobs use existing recurrence behavior." />
                  <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
                    <TextField
                      select
                      size="small"
                      label="Recurrence"
                      value={form.scheduleType}
                      onChange={(event) => setScheduleType(event.target.value)}
                      sx={{ minWidth: 240, flex: "1 1 260px", ...FIELD_SX }}
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
                    {form.scheduleType !== "immediately" ? (
                      <LocalizationProvider dateAdapter={AdapterDayjs}>
                        <DateTimePicker
                          label="Start"
                          value={dateTimePickerValue(form.start)}
                          onChange={(value) => setField("start", datetimeLocalValueFromDayjs(value))}
                          views={["year", "month", "day", "hours", "minutes"]}
                          format="YYYY-MM-DD hh:mm A"
                          slotProps={{
                            textField: {
                              size: "small",
                              sx: { minWidth: 260, flex: "1 1 280px", ...FIELD_SX },
                            },
                          }}
                        />
                      </LocalizationProvider>
                    ) : null}
                  </Stack>
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                    Remote installer creates normal pending device approvals after deployment succeeds.
                  </Typography>
                </Box>
              ) : null}

              {activeTabKey === "targets" ? (
                <Box sx={{ ...TAB_SECTION_SX, flexGrow: 1, minHeight: 0, px: 0, pt: { xs: 0.75, md: 1 }, pb: 0 }}>
                  <Box
                    sx={{
                      display: "flex",
                      alignItems: { xs: "flex-start", md: "flex-end" },
                      justifyContent: "space-between",
                      flexWrap: "wrap",
                      gap: 2,
                    }}
                  >
                    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
                      <Typography component="span" sx={FILTER_LABEL_SX}>
                        Status
                      </Typography>
                      <CountSliderGroup
                        options={TARGET_STATUS_FILTER_OPTIONS}
                        activeKey={targetStatusFilter}
                        counts={targetStatusCounts}
                        onChange={setTargetStatusFilter}
                      />
                    </Box>
                    <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                      Showing {visibleTargetGridRows.length.toLocaleString()} of {targetGridRows.length.toLocaleString()} devices
                    </Typography>
                  </Box>
                  <Box
                    sx={{
                      display: "grid",
                      gridTemplateColumns: { xs: "1fr", xl: "minmax(320px, 1fr) minmax(0, 2fr)" },
                      gap: 1.5,
                      flexGrow: 1,
                      minHeight: { xs: 940, xl: 0 },
                    }}
                  >
                    <Box sx={{ display: "flex", flexDirection: "column", gap: 1, minHeight: { xs: 460, xl: 0 }, minWidth: 0 }}>
                      <Box sx={{ minHeight: 24, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1, flexWrap: "wrap" }}>
                        <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                          Onboarding Summary
                        </Typography>
                      </Box>
                      <Box
                        className={gridThemeClass}
                        sx={{
                          ...GRID_PANEL_SX,
                          flexGrow: 1,
                          minHeight: 0,
                        }}
                      >
                        <AgGridReact
                          rowData={visibleTargetGridRows}
                          columnDefs={targetGridColumnDefs}
                          defaultColDef={targetGridDefaultColDef}
                          suppressCellFocus
                          headerHeight={AG_GRID_STANDARD_HEADER_HEIGHT}
                          rowHeight={AG_GRID_STANDARD_ROW_HEIGHT}
                          pagination
                          paginationPageSize={100}
                          paginationPageSizeSelector={[20, 50, 100]}
                          rowSelection={targetGridRowSelection}
                          overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No target attempts recorded yet.</span>"
                          getRowId={getTargetGridRowId}
                          getRowClass={(params) => (
                            targetProgressionKey(params.data) === String(selectedTargetId) ? "onboarding-target-row-selected" : ""
                          )}
                          onGridReady={handleTargetGridReady}
                          onRowClicked={handleTargetRowClicked}
                          onSelectionChanged={handleTargetSelectionChanged}
                          theme={gridTheme}
                        />
                      </Box>
                    </Box>
                    <Box sx={{ display: "flex", flexDirection: "column", gap: 1, minHeight: { xs: 460, xl: 0 }, minWidth: 0 }}>
                      <Box sx={{ minHeight: 24, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1, flexWrap: "wrap" }}>
                        <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                          Detailed Breakdown
                        </Typography>
                        <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, maxWidth: "70%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                          {selectedTargetRow?.targetLabel || ""}
                        </Typography>
                      </Box>
                      <Box
                        className={gridThemeClass}
                        sx={{
                          ...GRID_PANEL_SX,
                          flexGrow: 1,
                          minHeight: 0,
                        }}
                      >
                        <AgGridReact
                          rowData={selectedProgressionRows}
                          columnDefs={progressionGridColumnDefs}
                          defaultColDef={progressionGridDefaultColDef}
                          suppressCellFocus
                          headerHeight={AG_GRID_STANDARD_HEADER_HEIGHT}
                          rowHeight={AG_GRID_STANDARD_ROW_HEIGHT}
                          overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No progression recorded.</span>"
                          getRowId={(params) => String(params.data?.id || params.rowIndex)}
                          onGridReady={handleProgressionGridReady}
                          theme={gridTheme}
                        />
                      </Box>
                    </Box>
                  </Box>
                </Box>
              ) : null}
            </Box>
      </PageBodyFrame>

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
          <Box sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 2, flexWrap: "wrap" }}>
            <DialogHeaderBlock title={outputTitle} subtitle="Review remote onboarding output." />
            <Button onClick={() => setOutputOpen(false)} sx={PRIMARY_BUTTON_SX}>
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
          {outputSections.map((section) => (
            <Box key={section.key} sx={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0 }}>
              <Typography variant="subtitle2" sx={{ color: MAGIC_UI.textBright }}>
                {section.title}
              </Typography>
              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, display: "block", mb: 0.5 }}>
                {section.path}
              </Typography>
              <Box
                sx={{
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  borderRadius: 2,
                  bgcolor: "rgba(4,7,17,0.65)",
                  position: "relative",
                  display: "flex",
                  flexDirection: "column",
                  flex: 1,
                  minHeight: 0,
                  overflow: "auto",
                }}
              >
                <Box sx={{ position: "absolute", top: 10, right: 10, zIndex: 1 }}>
                  <Tooltip title={copiedOutputKey === section.key ? "Copied" : "Copy output"}>
                    <IconButton
                      size="small"
                      disabled={!section.content}
                      onClick={() => void handleCopyOutputSection(section)}
                      sx={{
                        color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
                        backgroundColor: "rgba(2,6,23,0.58)",
                        border: "1px solid rgba(148,163,184,0.2)",
                        "&:hover": {
                          backgroundColor: "rgba(8,15,33,0.82)",
                          color: copiedOutputKey === section.key ? MAGIC_UI.accentC : MAGIC_UI.textBright,
                        },
                      }}
                    >
                      {copiedOutputKey === section.key ? <CheckIcon fontSize="small" /> : <ContentCopyIcon fontSize="small" />}
                    </IconButton>
                  </Tooltip>
                </Box>
                <Box
                  component="pre"
                  sx={{
                    m: 0,
                    p: 1.5,
                    pr: 5.5,
                    minHeight: "100%",
                    whiteSpace: "pre",
                    color: "#e6edf3",
                    fontSize: 12,
                    lineHeight: 1.45,
                    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                  }}
                >
                  {section.content || "No output captured."}
                </Box>
              </Box>
            </Box>
          ))}
        </DialogContent>
      </Dialog>
    </Paper>
  );
}
