import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import {
  Box,
  Typography,
  Tabs,
  Tab,
  TextField,
  Button,
  IconButton,
  Checkbox,
  FormControlLabel,
  Select,
  Menu,
  MenuItem,
  Divider,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Table,
  TableHead,
  TableRow,
  TableCell,
  TableBody,
  GlobalStyles,
  CircularProgress
} from "@mui/material";
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  FilterList as FilterListIcon,
  PendingActions as PendingActionsIcon,
  Sync as SyncIcon,
  Timer as TimerIcon,
  Check as CheckIcon,
  Error as ErrorIcon,
  Refresh as RefreshIcon
} from "@mui/icons-material";
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
import Editor from "react-simple-code-editor";
import ReactFlow, { Handle, Position } from "reactflow";
import "reactflow/dist/style.css";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { DomainBadge } from "../Assemblies/Assembly_Badges";
import {
  buildAssemblyIndex,
  normalizeAssemblyPath,
  parseAssemblyExport,
  resolveAssemblyForComponent
} from "../Assemblies/assemblyUtils";

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

const gridTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",
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
};

const GRID_STYLE_BASE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
  "--ag-icon-font-family": iconFontFamily,
  "--ag-cell-horizontal-padding": "18px",
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
  "& .ag-row:nth-of-type(even)": {
    backgroundColor: "rgba(15,23,42,0.32)",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(125,183,255,0.08) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(56,189,248,0.14) !important",
    boxShadow: "inset 0 0 0 1px rgba(56,189,248,0.3)",
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
  pending: {
    label: "Pending",
    text: "#fbbf24",
    background: "rgba(251,191,36,0.18)",
    border: "1px solid rgba(251,191,36,0.35)",
    dot: "#f59e0b",
  },
  expired: {
    label: "Expired",
    text: "#e5e7eb",
    background: "rgba(226,232,240,0.14)",
    border: "1px solid rgba(226,232,240,0.32)",
    dot: "#cbd5f5",
  },
  default: {
    label: "Status",
    text: "#e2e8f0",
    background: "rgba(226,232,240,0.12)",
    border: "1px solid rgba(226,232,240,0.2)",
    dot: "#94a3b8",
  },
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

const TAB_HOVER_GRADIENT = "linear-gradient(120deg, rgba(125,211,252,0.18), rgba(192,132,252,0.22))";

const PRIMARY_CTA_SX = {
  borderRadius: 999,
  px: 3,
  py: 1,
  fontWeight: 600,
  textTransform: "none",
  color: "#041317",
  backgroundImage: "linear-gradient(120deg,#34d399,#22d3ee)",
  "&:hover": {
    backgroundImage: "linear-gradient(120deg,#22d3ee,#34d399)",
  },
};

const OUTLINE_BUTTON_SX = {
  borderRadius: 999,
  px: 2.5,
  textTransform: "none",
  borderColor: "rgba(148,163,184,0.45)",
  color: MAGIC_UI.textBright,
  "&:hover": {
    borderColor: MAGIC_UI.accentA,
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

const HERO_CARD_SX = {
  display: "flex",
  flexDirection: "column",
  gap: 0.2,
  px: 0,
  py: 0,
  minWidth: 160,
};
const GlassPanel = ({ children, sx }) => (
  <Box sx={{ ...GLASS_PANEL_BASE_SX, ...(sx || {}) }}>{children}</Box>
);

const EXEC_CONTEXT_COPY = {
  system: { title: "Windows (System)", detail: "Runs on device as SYSTEM" },
  current_user: { title: "Windows (Logged-In User)", detail: "Runs on device as user session" },
  ssh: { title: "Remote SSH", detail: "Executes from engine host" },
  winrm: { title: "Remote WinRM", detail: "Executes from engine host" },
};

const SCHEDULE_LABELS = {
  immediately: "Immediate",
  once: "Single run",
  every_5_minutes: "Every 5 minutes",
  every_10_minutes: "Every 10 minutes",
  every_15_minutes: "Every 15 minutes",
  every_30_minutes: "Every 30 minutes",
  every_15: "Every 15 minutes",
  every_hour: "Hourly cadence",
  daily: "Daily cadence",
  weekly: "Weekly cadence",
  monthly: "Monthly cadence",
  yearly: "Yearly cadence",
};

const TABLE_BASE_SX = {
  "& .MuiTableCell-root": {
    borderColor: "rgba(148,163,184,0.18)",
    color: MAGIC_UI.textBright,
  },
  "& .MuiTableHead-root .MuiTableCell-root": {
    color: MAGIC_UI.textMuted,
    fontWeight: 600,
    backgroundColor: "rgba(8,12,24,0.7)",
  },
  "& .MuiTableBody-root .MuiTableRow-root:hover": {
    backgroundColor: "rgba(56,189,248,0.08)",
  },
};

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
  failed: { label: "Failed", color: "#ff4f4f", Icon: ErrorIcon }
};

const DEVICE_COLUMNS = [
  { key: "hostname", label: "Hostname" },
  { key: "online", label: "Status" },
  { key: "site", label: "Site" },
  { key: "ran_on", label: "Ran On" },
  { key: "job_status", label: "Job Status" },
  { key: "output", label: "StdOut / StdErr" }
];

const normalizeFilterCatalog = (raw) => {
  if (!Array.isArray(raw)) return [];
  return raw
    .map((item, idx) => {
      const idValue = item?.id ?? item?.filter_id ?? idx;
      const id = Number(idValue);
      if (!Number.isFinite(id)) return null;
      const scopeText = String(item?.site_scope || item?.scope || item?.type || "global").toLowerCase();
      const scope = scopeText === "scoped" ? "scoped" : "global";
      const deviceCount =
        typeof item?.matching_device_count === "number" && Number.isFinite(item.matching_device_count)
          ? item.matching_device_count
          : null;
      return {
        id,
        name: item?.name || `Filter ${idx + 1}`,
        scope,
        site: item?.site || item?.site_name || item?.target_site || null,
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

export default function CreateJob({ onCancel, onCreated, initialJob = null, quickJobDraft = null, onConsumeQuickJobDraft }) {
  const [tab, setTab] = useState(0);
  const [jobName, setJobName] = useState("");
  const [pageTitleJobName, setPageTitleJobName] = useState("");
  // Components the job will run: {type:'script'|'workflow', path, name, description}
  const [components, setComponents] = useState([]);
  const [targets, setTargets] = useState([]); // array of target descriptors
  const [filterCatalog, setFilterCatalog] = useState([]);
  const [loadingFilterCatalog, setLoadingFilterCatalog] = useState(false);
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
  const [stopAfterEnabled, setStopAfterEnabled] = useState(false);
  const [expiration, setExpiration] = useState("no_expire");
  const [execContext, setExecContext] = useState("system");
  const [credentials, setCredentials] = useState([]);
  const [credentialLoading, setCredentialLoading] = useState(false);
  const [credentialError, setCredentialError] = useState("");
  const [selectedCredentialId, setSelectedCredentialId] = useState("");
  const [useSvcAccount, setUseSvcAccount] = useState(true);
  const [assembliesPayload, setAssembliesPayload] = useState({ items: [], queue: [] });
  const [assembliesLoading, setAssembliesLoading] = useState(false);
  const [assembliesError, setAssembliesError] = useState("");
  const assemblyExportCacheRef = useRef(new Map());
  const quickDraftAppliedRef = useRef(null);

  const loadCredentials = useCallback(async () => {
    setCredentialLoading(true);
    setCredentialError("");
    try {
      const resp = await fetch("/api/credentials");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
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
      assemblyExportCacheRef.current.clear();
      setAssembliesPayload({
        items: Array.isArray(data?.items) ? data.items : [],
        queue: Array.isArray(data?.queue) ? data.queue : []
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
  const assemblyGridRows = useMemo(() => {
    const toRow = (record) => ({
      id: record.assemblyGuid || record.pathLower || record.displayName,
      name: record.displayName || record.path || record.assemblyGuid,
      domain: record.domainLabel || record.domain || "General",
      path: record.path || "",
      summary: record.summary || "",
      kind: record.kind || "script",
      record
    });
    const grouped = assemblyIndex.grouped || {};
    return {
      scripts: (grouped.scripts || []).map(toRow),
      ansible: (grouped.ansible || []).map(toRow),
      workflows: (grouped.workflows || []).map(toRow)
    };
  }, [assemblyIndex]);

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
  const [compTab, setCompTab] = useState("scripts");
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [assemblyFilterText, setAssemblyFilterText] = useState("");

  useEffect(() => {
    setSelectedNodeId("");
  }, [compTab]);
  const selectedAssemblyRecord = useMemo(() => {
    if (!selectedNodeId) return null;
    const key = String(selectedNodeId).toLowerCase();
    return assemblyIndex.byGuid?.get(key) || null;
  }, [selectedNodeId, assemblyIndex]);
  const assemblyRowData = useMemo(() => assemblyGridRows[compTab] || [], [assemblyGridRows, compTab]);
  const filteredAssemblyRows = useMemo(() => {
    const query = assemblyFilterText.trim().toLowerCase();
    if (!query) return assemblyRowData;
    return assemblyRowData.filter((row) => {
      const fields = [row.name, row.domain, row.path, row.summary];
      return fields.some((value) => typeof value === "string" && value.toLowerCase().includes(query));
    });
  }, [assemblyRowData, assemblyFilterText]);
  const assemblyColumnDefs = useMemo(
    () => [
      { field: "name", headerName: "Name", minWidth: 200, flex: 1.1 },
      { field: "domain", headerName: "Domain", minWidth: 140 },
      { field: "path", headerName: "Path", minWidth: 220, flex: 1.2 },
      { field: "summary", headerName: "Summary", minWidth: 260, flex: 1.4 }
    ],
    []
  );
  const assemblyDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 1,
      suppressMenu: false,
      filter: true,
      floatingFilter: false,
      cellClass: "auto-col-tight",
      cellStyle: LEFT_ALIGN_CELL_STYLE,
    }),
    []
  );
  const ASSEMBLY_AUTO_COLUMNS = useRef(["name", "domain", "path", "summary"]);
  const assemblyGridApiRef = useRef(null);
  const handleAssemblyGridReady = useCallback((params) => {
    assemblyGridApiRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(ASSEMBLY_AUTO_COLUMNS.current, true);
      } catch {}
    });
  }, []);
  useEffect(() => {
    if (!assemblyGridApiRef.current) return;
    requestAnimationFrame(() => {
      try {
        assemblyGridApiRef.current.autoSizeColumns(ASSEMBLY_AUTO_COLUMNS.current, true);
      } catch {}
    });
  }, [assemblyRowData, compTab]);

  const remoteExec = useMemo(() => execContext === "ssh" || execContext === "winrm", [execContext]);
  const handleExecContextChange = useCallback((value) => {
    const normalized = String(value || "system").toLowerCase();
    setExecContext(normalized);
    if (normalized === "winrm") {
      setUseSvcAccount(true);
      setSelectedCredentialId("");
    } else {
      setUseSvcAccount(false);
    }
  }, []);
  const filteredCredentials = useMemo(() => {
    if (!remoteExec) return credentials;
    const target = execContext === "winrm" ? "winrm" : "ssh";
    return credentials.filter((cred) => String(cred.connection_type || "").toLowerCase() === target);
  }, [credentials, remoteExec, execContext]);

  useEffect(() => {
    if (!remoteExec) {
      return;
    }
    if (execContext === "winrm" && useSvcAccount) {
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

  const [addTargetOpen, setAddTargetOpen] = useState(false);
  const [availableDevices, setAvailableDevices] = useState([]); // [{hostname, display, online}]
  const [selectedDeviceTargets, setSelectedDeviceTargets] = useState({});
  const [selectedFilterTargets, setSelectedFilterTargets] = useState({});
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
  const [deviceFilters, setDeviceFilters] = useState({});
  const [filterAnchorEl, setFilterAnchorEl] = useState(null);
  const [activeFilterColumn, setActiveFilterColumn] = useState(null);
  const [pendingFilterValue, setPendingFilterValue] = useState("");

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
        return host ? { kind: "device", hostname: host } : null;
      }
      if (rawKind === "filter" || rawTarget.filter_id != null || rawTarget.id != null) {
        const idValue = rawTarget.filter_id ?? rawTarget.id;
        const filterId = Number(idValue);
        if (!Number.isFinite(filterId)) return null;
        const catalogEntry =
          filterCatalogMapRef.current[filterId] || filterCatalogMapRef.current[String(filterId)] || {};
        const scopeText = String(rawTarget.site_scope || rawTarget.scope || rawTarget.type || catalogEntry.scope || "global").toLowerCase();
        const scope = scopeText === "scoped" ? "scoped" : "global";
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
          site_scope: scope,
          site: rawTarget.site || rawTarget.site_name || catalogEntry.site || null,
          deviceCount,
        };
      }
    }
    return null;
  }, []);

  const targetKey = useCallback((target) => {
    if (!target) return "";
    if (target.kind === "filter") return `filter-${target.filter_id}`;
    if (target.kind === "device") return `device-${(target.hostname || "").toLowerCase()}`;
    return "";
  }, []);

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
            site_scope: target.site_scope,
            site: target.site,
          };
        }
        if (target.kind === "device") {
          return target.hostname;
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
  const targetGridRows = useMemo(() => {
    return targets.map((target) => {
      const key = targetKey(target) || `${target?.kind || "target"}-${Math.random().toString(36).slice(2, 8)}`;
      const isFilter = target?.kind === "filter";
      const deviceCount =
        typeof target?.deviceCount === "number" && Number.isFinite(target.deviceCount) ? target.deviceCount : null;
      const detailText = isFilter
        ? `${deviceCount != null ? deviceCount.toLocaleString() : "—"} device${deviceCount === 1 ? "" : "s"}${
            target?.site_scope === "scoped" ? ` • ${target?.site || "Specific site"}` : ""
          }`
        : "—";
      return {
        id: key,
        typeLabel: isFilter ? "Filter" : "Device",
        targetLabel: isFilter ? target?.name || `Filter #${target?.filter_id}` : target?.hostname,
        detailText,
        rawTarget: target,
      };
    });
  }, [targets, targetKey]);
  const targetGridColumnDefs = useMemo(
    () => [
      { field: "typeLabel", headerName: "Type", minWidth: 120, filter: "agTextColumnFilter" },
      { field: "targetLabel", headerName: "Target", minWidth: 200, flex: 1.1, filter: "agTextColumnFilter" },
      { field: "detailText", headerName: "Details", minWidth: 200, flex: 1.4, filter: "agTextColumnFilter" },
      {
        field: "actions",
        headerName: "",
        minWidth: 80,
        maxWidth: 100,
        cellRenderer: "TargetActionsRenderer",
        sortable: false,
        suppressMenu: true,
        filter: false,
      },
    ],
    []
  );
  const targetGridComponents = useMemo(
    () => ({
      TargetActionsRenderer: (params) => (
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
      ),
    }),
    []
  );
  const targetGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 1,
      suppressMenu: false,
      filter: true,
      floatingFilter: false,
      cellClass: "auto-col-tight",
      cellStyle: LEFT_ALIGN_CELL_STYLE,
    }),
    []
  );
  const targetGridApiRef = useRef(null);
  const TARGET_AUTO_COLS = useRef(["typeLabel", "targetLabel", "detailText"]);
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

  const getDefaultFilterValue = useCallback((key) => (["online", "job_status", "output"].includes(key) ? "all" : ""), []);

  const isColumnFiltered = useCallback((key) => {
    if (!deviceFilters || typeof deviceFilters !== "object") return false;
    const value = deviceFilters[key];
    if (value == null) return false;
    if (typeof value === "string") {
      const trimmed = value.trim();
      if (!trimmed || trimmed === "all") return false;
      return true;
    }
    return true;
  }, [deviceFilters]);

  const openFilterMenu = useCallback((event, columnKey) => {
    setActiveFilterColumn(columnKey);
    setPendingFilterValue(deviceFilters[columnKey] ?? getDefaultFilterValue(columnKey));
    setFilterAnchorEl(event.currentTarget);
  }, [deviceFilters, getDefaultFilterValue]);

  const closeFilterMenu = useCallback(() => {
    setFilterAnchorEl(null);
    setActiveFilterColumn(null);
  }, []);

  const applyFilter = useCallback(() => {
    if (!activeFilterColumn) {
      closeFilterMenu();
      return;
    }
    const value = pendingFilterValue;
    setDeviceFilters((prev) => {
      const next = { ...(prev || {}) };
      if (!value || value === "all" || (typeof value === "string" && !value.trim())) {
        delete next[activeFilterColumn];
      } else {
        next[activeFilterColumn] = value;
      }
      return next;
    });
    closeFilterMenu();
  }, [activeFilterColumn, pendingFilterValue, closeFilterMenu]);

  const clearFilter = useCallback(() => {
    if (!activeFilterColumn) {
      closeFilterMenu();
      return;
    }
    setDeviceFilters((prev) => {
      const next = { ...(prev || {}) };
      delete next[activeFilterColumn];
      return next;
    });
    setPendingFilterValue(getDefaultFilterValue(activeFilterColumn));
    closeFilterMenu();
  }, [activeFilterColumn, closeFilterMenu, getDefaultFilterValue]);

  const renderFilterControl = () => {
    const columnKey = activeFilterColumn;
    if (!columnKey) return null;
    if (columnKey === "online") {
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Statuses</MenuItem>
          <MenuItem value="online">Online</MenuItem>
          <MenuItem value="offline">Offline</MenuItem>
        </Select>
      );
    }
    if (columnKey === "job_status") {
      const options = ["success", "failed", "running", "pending", "expired", "timed out"];
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Results</MenuItem>
          {options.map((opt) => (
            <MenuItem key={opt} value={opt}>{opt.replace(/\b\w/g, (m) => m.toUpperCase())}</MenuItem>
          ))}
        </Select>
      );
    }
    if (columnKey === "output") {
      return (
        <Select
          size="small"
          fullWidth
          value={typeof pendingFilterValue === "string" && pendingFilterValue ? pendingFilterValue : "all"}
          onChange={(e) => setPendingFilterValue(e.target.value)}
        >
          <MenuItem value="all">All Output</MenuItem>
          <MenuItem value="stdout">StdOut Only</MenuItem>
          <MenuItem value="stderr">StdErr Only</MenuItem>
          <MenuItem value="both">StdOut & StdErr</MenuItem>
          <MenuItem value="none">No Output</MenuItem>
        </Select>
      );
    }
    const placeholders = {
      hostname: "Filter hostname",
      site: "Filter site",
      ran_on: "Filter date/time"
    };
    const value = typeof pendingFilterValue === "string" ? pendingFilterValue : "";
    return (
      <TextField
        size="small"
        autoFocus
        fullWidth
        placeholder={placeholders[columnKey] || "Filter value"}
        value={value}
        onChange={(e) => setPendingFilterValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            applyFilter();
          }
        }}
      />
    );
  };

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
      if (filterKey === "pending") return status === "pending" || status === "scheduled" || status === "queued" || status === "";
      if (filterKey === "running") return status === "running";
      if (filterKey === "success") return status === "success";
      if (filterKey === "failed") return status === "failed" || status === "failure" || status === "timed out" || status === "timed_out" || status === "warning";
      if (filterKey === "expired") return status === "expired";
      return true;
    };

    return deviceRows.filter((row) => {
      const normalizedStatus = String(row?.job_status || "").trim().toLowerCase();
      if (deviceStatusFilter && !matchStatusFilter(normalizedStatus, deviceStatusFilter)) {
        return false;
      }
      if (deviceFilters && typeof deviceFilters === "object") {
        for (const [key, rawValue] of Object.entries(deviceFilters)) {
          if (rawValue == null) continue;
          if (typeof rawValue === "string") {
            const trimmed = rawValue.trim();
            if (!trimmed || trimmed === "all") continue;
          }
          if (key === "hostname") {
            const expected = String(rawValue || "").toLowerCase();
            if (!String(row?.hostname || "").toLowerCase().includes(expected)) return false;
          } else if (key === "online") {
            if (rawValue === "online" && !row?.online) return false;
            if (rawValue === "offline" && row?.online) return false;
          } else if (key === "site") {
            const expected = String(rawValue || "").toLowerCase();
            if (!String(row?.site || "").toLowerCase().includes(expected)) return false;
          } else if (key === "ran_on") {
            const expected = String(rawValue || "").toLowerCase();
            const formatted = fmtTs(row?.ran_on).toLowerCase();
            if (!formatted.includes(expected)) return false;
          } else if (key === "job_status") {
            const expected = String(rawValue || "").toLowerCase();
            if (!normalizedStatus.includes(expected)) return false;
          } else if (key === "output") {
            if (rawValue === "stdout" && !row?.has_stdout) return false;
            if (rawValue === "stderr" && !row?.has_stderr) return false;
            if (rawValue === "both" && (!row?.has_stdout || !row?.has_stderr)) return false;
            if (rawValue === "none" && (row?.has_stdout || row?.has_stderr)) return false;
          }
        }
      }
      return true;
    });
  }, [deviceRows, deviceStatusFilter, deviceFilters, fmtTs]);

  const jobHistoryGridRows = useMemo(
    () =>
      deviceFiltered.map((row, index) => ({
        id: `${row.hostname || "device"}-${index}`,
        hostname: row.hostname || "",
        online: Boolean(row.online),
        site: row.site || "",
        ranOn: row.ran_on,
        jobStatus: row.job_status || "",
        hasStdOut: Boolean(row.has_stdout),
        hasStdErr: Boolean(row.has_stderr),
        raw: row,
      })),
    [deviceFiltered]
  );
  const hydrateExistingComponents = useCallback(async (rawComponents = []) => {
    const results = [];
    for (const raw of rawComponents) {
      if (!raw || typeof raw !== "object") continue;
      const typeRaw = raw.type || raw.component_type || "script";
      if (typeRaw === "workflow") {
        results.push({
          ...raw,
          type: "workflow",
          variables: Array.isArray(raw.variables) ? raw.variables : [],
          localId: generateLocalId()
        });
        continue;
      }
      const kind = typeRaw === "ansible" ? "ansible" : "script";
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
        path: normalizeAssemblyPath(kind, record.path || "", record.displayName),
        name: raw.name || record.displayName,
        description: raw.description || record.summary || record.path,
        variables: mergedVariables,
        localId: generateLocalId(),
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
    const base = jobName.trim().length > 0 && components.length > 0 && targets.length > 0;
    if (!base) return false;
    const needsCredential = remoteExec && !(execContext === "winrm" && useSvcAccount);
    if (needsCredential && !selectedCredentialId) return false;
    if (scheduleType !== "immediately") {
      return !!startDateTime;
    }
    return true;
  }, [jobName, components.length, targets.length, scheduleType, startDateTime, remoteExec, selectedCredentialId, execContext, useSvcAccount]);

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

  const loadHistory = useCallback(async () => {
    if (!editing) return;
    try {
      const [runsResp, jobResp, devResp] = await Promise.all([
        fetch(`/api/scheduled_jobs/${initialJob.id}/runs?days=30`),
        fetch(`/api/scheduled_jobs/${initialJob.id}`),
        fetch(`/api/scheduled_jobs/${initialJob.id}/devices`)
      ]);
      const runs = await runsResp.json();
      const job = await jobResp.json();
      const dev = await devResp.json();
      if (!runsResp.ok) throw new Error(runs.error || `HTTP ${runsResp.status}`);
      if (!jobResp.ok) throw new Error(job.error || `HTTP ${jobResp.status}`);
      if (!devResp.ok) throw new Error(dev.error || `HTTP ${devResp.status}`);
      setHistoryRows(Array.isArray(runs.runs) ? runs.runs : []);
      setJobSummary(job.job || {});
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

  const resultChip = useCallback((status) => {
    const key = String(status || "").toLowerCase();
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
      const statuses = Array.from(entry.statuses).map((s) => String(s || "").trim().toLowerCase()).filter(Boolean);
      if (!statuses.length) return;
      const hasInFlight = statuses.some((s) => s === "running" || s === "pending" || s === "scheduled");
      if (hasInFlight) return;
      const hasFailure = statuses.some((s) => ["failed", "failure", "expired", "timed out", "timed_out", "warning"].includes(s));
      const allSuccess = statuses.every((s) => s === "success");
      const statusLabel = hasFailure ? "Failed" : (allSuccess ? "Success" : "Failed");
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
        suppressMenu: true,
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
    const base = { pending: 0, running: 0, success: 0, failed: 0, expired: 0 };
    deviceRows.forEach((row) => {
      const normalized = String(row?.job_status || "").trim().toLowerCase();
      if (!normalized || normalized === "pending" || normalized === "scheduled" || normalized === "queued") {
        base.pending += 1;
      } else if (normalized === "running") {
        base.running += 1;
      } else if (normalized === "success") {
        base.success += 1;
      } else if (normalized === "expired") {
        base.expired += 1;
      } else if (normalized === "failed" || normalized === "failure" || normalized === "timed out" || normalized === "timed_out" || normalized === "warning") {
        base.failed += 1;
      } else {
        base.pending += 1;
      }
    });
    return base;
  }, [deviceRows]);

  const statusCounts = useMemo(() => {
    const merged = { pending: 0, running: 0, success: 0, failed: 0, expired: 0 };
    Object.keys(merged).forEach((key) => {
      const summaryVal = Number((counts || {})[key] ?? 0);
      const fallback = deviceStatusCounts[key] ?? 0;
      merged[key] = summaryVal > 0 ? summaryVal : fallback;
    });
    return merged;
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
      position: { x: 0, y: 0 },
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
    <GlassPanel sx={{ mb: 2, p: 0, overflow: "hidden" }}>
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
      <Box sx={{ height: 380, p: { xs: 2, md: 3 }, background: "rgba(4,7,18,0.6)" }}>
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
        <Box sx={{ px: { xs: 2, md: 3 }, pb: 2.5, display: "flex", alignItems: "center", gap: 1.5 }}>
          <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
            Showing devices with {STATUS_META[deviceStatusFilter]?.label || deviceStatusFilter} results
          </Typography>
          <Button size="small" sx={{ color: MAGIC_UI.accentA, textTransform: "none", p: 0 }} onClick={() => setDeviceStatusFilter(null)}>
            Clear Filter
          </Button>
        </Box>
      ) : null}
    </GlassPanel>
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
    try {
      return Prism.highlight(code ?? "", Prism.languages[lang] || Prism.languages.markup, lang);
    } catch {
      return String(code || "");
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
        const online = Boolean(params.value);
        const theme = online ? DEVICE_STATUS_THEME.online : DEVICE_STATUS_THEME.offline;
        return (
          <StatusPill
            label={online ? DEVICE_STATUS_THEME.online.label : DEVICE_STATUS_THEME.offline.label}
            theme={theme}
          />
        );
      },
      JobStatusRenderer: (params) => resultChip(params.value || ""),
      OutputActionsRenderer: (params) => {
        const row = params.data?.raw;
        if (!row) return null;
        return (
          <Box sx={{ display: "flex", gap: 1 }}>
            {row.has_stdout ? (
              <Button
                size="small"
                sx={{ color: MAGIC_UI.accentA, textTransform: "none", minWidth: 0, p: 0 }}
                onClick={(e) => {
                  e.stopPropagation();
                  params.context?.viewOutput?.(row, "stdout");
                }}
              >
                StdOut
              </Button>
            ) : null}
            {row.has_stderr ? (
              <Button
                size="small"
                sx={{ color: "#fb7185", textTransform: "none", minWidth: 0, p: 0 }}
                onClick={(e) => {
                  e.stopPropagation();
                  params.context?.viewOutput?.(row, "stderr");
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
      { field: "hostname", headerName: "Hostname", minWidth: 180 },
      {
        field: "online",
        headerName: "Status",
        minWidth: 140,
        cellRenderer: "DeviceStatusRenderer",
        cellClass: "status-pill-cell",
        sortable: false,
        suppressMenu: true,
      },
      { field: "site", headerName: "Site", minWidth: 160 },
      {
        field: "ranOn",
        headerName: "Ran On",
        minWidth: 200,
        valueFormatter: (params) => (params.value ? fmtTs(params.value) : ""),
        comparator: (a, b) => Number(a || 0) - Number(b || 0),
      },
      {
        field: "jobStatus",
        headerName: "Job Status",
        minWidth: 150,
        cellRenderer: "JobStatusRenderer",
        cellClass: "status-pill-cell",
        sortable: false,
        suppressMenu: true,
      },
      {
        field: "output",
        headerName: "StdOut / StdErr",
        minWidth: 210,
        cellRenderer: "OutputActionsRenderer",
        sortable: false,
        suppressMenu: true,
      },
    ],
    [fmtTs]
  );
  const jobHistoryGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: false,
      flex: 1,
      cellClass: "auto-col-tight",
    }),
    []
  );
  const jobHistoryGridApiRef = useRef(null);
  const JOB_HISTORY_AUTO_COLS = useRef(["hostname", "online", "site", "ranOn", "jobStatus"]);
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
    let canceled = false;
    const hydrate = async () => {
      if (initialJob && initialJob.id) {
        setJobName(initialJob.name || "");
        setPageTitleJobName(typeof initialJob.name === "string" ? initialJob.name.trim() : "");
        setTargets(normalizeTargetList(initialJob.targets || []));
        setScheduleType(initialJob.schedule_type || initialJob.schedule?.type || "immediately");
        setStartDateTime(initialJob.start_ts ? dayjs(Number(initialJob.start_ts) * 1000).second(0) : (initialJob.schedule?.start ? dayjs(initialJob.schedule.start).second(0) : dayjs().add(5, "minute").second(0)));
        setStopAfterEnabled(Boolean(initialJob.duration_stop_enabled));
        setExpiration(initialJob.expiration || "no_expire");
        setExecContext(initialJob.execution_context || "system");
        setSelectedCredentialId(initialJob.credential_id ? String(initialJob.credential_id) : "");
        if ((initialJob.execution_context || "").toLowerCase() === "winrm") {
          setUseSvcAccount(initialJob.use_service_account !== false);
        } else {
          setUseSvcAccount(false);
        }
        const comps = Array.isArray(initialJob.components) ? initialJob.components : [];
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
      }
    };
    hydrate();
    return () => {
      canceled = true;
    };
  }, [initialJob, hydrateExistingComponents, normalizeTargetList]);

  const openAddComponent = async () => {
    setAddCompOpen(true);
    setSelectedNodeId("");
    if (!assembliesPayload.items.length && !assembliesLoading) {
      loadAssemblies();
    }
  };

  const addSelectedComponent = useCallback(async (recordOverride = null) => {
    const record = recordOverride || selectedAssemblyRecord;
    if (!record || !record.assemblyGuid) return false;
    if (record.kind === "workflow") {
      alert("Workflows within Scheduled Jobs are not supported yet");
      return false;
    }
    try {
      const exportDoc = await loadAssemblyExport(record.assemblyGuid);
      const parsed = parseAssemblyExport(exportDoc);
      const docVars = Array.isArray(parsed.rawVariables) ? parsed.rawVariables : [];
      const mergedVariables = mergeComponentVariables(docVars, [], {});
      const type = record.kind === "ansible" || record.type === "ansible" || compTab === "ansible" ? "ansible" : "script";
      const normalizedPath = normalizeAssemblyPath(type, record.path || "", record.displayName);
      setComponents((prev) => [
        ...prev,
        {
          type,
          path: normalizedPath,
          name: record.displayName,
          description: record.summary || normalizedPath,
          variables: mergedVariables,
          localId: generateLocalId(),
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
  }, [selectedAssemblyRecord, compTab, loadAssemblyExport, mergeComponentVariables, normalizeAssemblyPath, generateLocalId]);

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
    loadFilterCatalog();
    try {
      const resp = await fetch("/api/agents");
      if (resp.ok) {
        const data = await resp.json();
        const list = Object.values(data || {}).map((a) => ({
          hostname: a.hostname || a.agent_hostname || a.id || "unknown",
          display: a.hostname || a.agent_hostname || a.id || "unknown",
          online: !!a.collector_active
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
    if (remoteExec && !(execContext === "winrm" && useSvcAccount) && !selectedCredentialId) {
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
      setTab(1);
      alert("Please fill in all required variable values.");
      return;
    }
    setComponentVarErrors({});
    const payloadComponents = sanitizeComponentsForSave(components);
    const payload = {
      name: jobName,
      components: payloadComponents,
      targets: serializeTargetsForSave(targets),
      schedule: { type: scheduleType, start: scheduleType !== "immediately" ? (() => { try { const d = startDateTime?.toDate?.() || new Date(startDateTime); d.setSeconds(0,0); return d.toISOString(); } catch { return startDateTime; } })() : null },
      duration: { stopAfterEnabled, expiration },
      execution_context: execContext,
      credential_id: remoteExec && !useSvcAccount && selectedCredentialId ? Number(selectedCredentialId) : null,
      use_service_account: execContext === "winrm" ? Boolean(useSvcAccount) : false
    };
    try {
      const resp = await fetch(initialJob && initialJob.id ? `/api/scheduled_jobs/${initialJob.id}` : "/api/scheduled_jobs", {
        method: initialJob && initialJob.id ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      onCreated && onCreated(data.job || payload);
      onCancel && onCancel();
    } catch (err) {
      alert(String(err.message || err));
    }
  };

  const tabDefs = useMemo(() => {
    const base = [
      { key: "name", label: "Job Name" },
      { key: "components", label: "Assemblies" },
      { key: "targets", label: "Targets" },
      { key: "schedule", label: "Schedule" },
      { key: "context", label: "Execution Context" }
    ];
    if (editing) base.push({ key: 'history', label: 'Job History' });
    return base;
  }, [editing]);
  const historyTabIndex = useMemo(() => tabDefs.findIndex((t) => t.key === "history"), [tabDefs]);

  const scheduleSummary = useMemo(() => {
    const base = SCHEDULE_LABELS[scheduleType] || "Scheduled run";
    if (scheduleType === "immediately") {
      return "Runs as soon as the job is created";
    }
    const dt = startDateTime ? dayjs(startDateTime) : null;
    if (dt && dt.isValid()) {
      return `${base} • ${dt.format("MMM D, YYYY h:mm A")}`;
    }
    return base;
  }, [scheduleType, startDateTime]);

  const targetSummary = useMemo(() => {
    if (!targets.length) return "No targets selected";
    let deviceCount = 0;
    let filterCount = 0;
    targets.forEach((target) => {
      if (target?.kind === "filter") filterCount += 1;
      else deviceCount += 1;
    });
    const segments = [];
    if (deviceCount) segments.push(`${deviceCount} device${deviceCount === 1 ? "" : "s"}`);
    if (filterCount) segments.push(`${filterCount} filter${filterCount === 1 ? "" : "s"}`);
    return segments.join(" • ") || `${targets.length} target${targets.length === 1 ? "" : "s"}`;
  }, [targets]);

const heroTiles = useMemo(() => {
    const execMeta = EXEC_CONTEXT_COPY[execContext] || EXEC_CONTEXT_COPY.system;
    return [
      {
        key: "assemblies",
        label: "Assemblies",
        value: components.length ? components.length.toString() : "0",
      },
      {
        key: "targets",
        label: "Targets",
        value: targets.length ? targets.length.toString() : "0",
      },
      {
        key: "schedule",
        label: "Schedule",
        value: SCHEDULE_LABELS[scheduleType] || "Schedule",
      },
      {
        key: "context",
        label: "Execution",
        value: execMeta.title,
      },
    ];
  }, [components.length, targets.length, scheduleType, scheduleSummary, targetSummary, execContext]);

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
    const tabIndex = tabDefs.findIndex((t) => t.key === targetTabKey);
    if (tabIndex >= 0) setTab(tabIndex);
    else if (tabDefs.length > 1) setTab(1);
    if (typeof onConsumeQuickJobDraft === "function") {
      onConsumeQuickJobDraft(quickJobDraft.id);
    }
  }, [editing, quickJobDraft, tabDefs, onConsumeQuickJobDraft, normalizeTargetList]);

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
        background: MAGIC_UI.shellBg,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        boxShadow: MAGIC_UI.glow,
      }}
    >
      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          gap: 2,
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <Box sx={{ display: "flex", flexDirection: "column", gap: 0.75 }}>
          <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
            <PendingActionsIcon sx={{ color: MAGIC_UI.accentA }} />
            <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
              Scheduled Job
              {pageTitleJobName ? (
                <Box component="span" sx={{ color: "rgba(226,232,240,0.65)", fontWeight: 500 }}>
                  {`: "${pageTitleJobName}"`}
                </Box>
              ) : null}
            </Typography>
          </Box>
          <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
            Configure advanced scheduled jobs against one or several targeted devices or device filters.
          </Typography>
        </Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
          <Button onClick={onCancel} sx={OUTLINE_BUTTON_SX}>
            Cancel
          </Button>
          <Button
            variant="contained"
            startIcon={<AddIcon />}
            onClick={() => (isValid ? setConfirmOpen(true) : null)}
            disabled={!isValid}
            sx={{
              ...PRIMARY_CTA_SX,
              color: isValid ? "#041317" : "#ffffff",
              backgroundImage: isValid
                ? PRIMARY_CTA_SX.backgroundImage
                : "linear-gradient(135deg, rgba(148,163,184,0.35), rgba(51,65,85,0.45))",
              boxShadow: isValid ? PRIMARY_CTA_SX.boxShadow : "none",
              opacity: 1,
            }}
          >
            {initialJob && initialJob.id ? "Save Changes" : "Create Job"}
          </Button>
        </Box>
      </Box>

      <Box
        sx={{
          display: "flex",
          flexWrap: "wrap",
          gap: 1.5,
          justifyContent: { xs: "flex-start", md: "flex-end" },
        }}
      >
        {heroTiles.map((tile) => {
          let mainValue = tile.value || "";
          let qualifier = "";
          if (tile.key === "context") {
            const match = mainValue.match(/^(.*?)\s*\((.+)\)$/);
            if (match) {
              mainValue = match[1].trim();
              qualifier = match[2];
            }
          }
          return (
            <Box key={tile.key} sx={HERO_CARD_SX}>
              <Typography
                variant="caption"
                sx={{ color: MAGIC_UI.textMuted, textTransform: "uppercase", fontWeight: 600, letterSpacing: 0.6 }}
              >
                {tile.label}
              </Typography>
              <Typography variant="h5" sx={{ color: MAGIC_UI.textBright, display: "flex", gap: 0.4, alignItems: "baseline" }}>
                <Box component="span">{mainValue}</Box>
                {qualifier ? (
                  <Box component="span" sx={{ color: "rgba(226,232,240,0.65)", fontSize: "0.9rem" }}>
                    ({qualifier})
                  </Box>
                ) : null}
              </Typography>
            </Box>
          );
        })}
      </Box>

      <Tabs
        value={tab}
        onChange={(_, v) => setTab(v)}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: "linear-gradient(90deg, #7dd3fc, #c084fc)",
          },
        }}
        sx={{
          borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
          "& .MuiTab-root": {
            color: MAGIC_UI.textMuted,
            textTransform: "none",
            fontWeight: 600,
            opacity: 1,
            borderRadius: 1,
            transition: "background 0.2s ease, color 0.2s ease, box-shadow 0.2s ease",
            "&:hover": {
              color: MAGIC_UI.textBright,
              backgroundImage: TAB_HOVER_GRADIENT,
              opacity: 1,
              boxShadow: "0 0 0 1px rgba(148,163,184,0.25) inset",
            },
          },
          "& .Mui-selected": {
            color: MAGIC_UI.textBright,
            "&:hover": {
              backgroundImage: TAB_HOVER_GRADIENT,
            },
          },
        }}
      >
        {tabDefs.map((t) => (
          <Tab key={t.key} label={t.label} />
        ))}
      </Tabs>

      <Box sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column", gap: 3 }}>
        {tab === 0 && (
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
            />
          </Box>
        )}

        {tab === 1 && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader
              title="Assemblies"
              action={
                <Button
                  size="small"
                  startIcon={<AddIcon />}
                  onClick={openAddComponent}
                  variant="outlined"
                  sx={{ ...OUTLINE_BUTTON_SX, borderColor: MAGIC_UI.accentB, color: MAGIC_UI.accentB }}
                >
                  Add Assembly
                </Button>
              }
            />
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

        {tab === 2 && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader
              title="Targets"
              action={
                <Button
                  size="small"
                  startIcon={<AddIcon />}
                  onClick={openAddTargets}
                  variant="outlined"
                  sx={{ ...OUTLINE_BUTTON_SX, borderColor: MAGIC_UI.accentA, color: MAGIC_UI.accentA }}
                >
                  Add Target
                </Button>
              }
            />
            <Box className={gridThemeClass} sx={{ ...GRID_WRAPPER_SX, height: 320 }}>
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
                style={GRID_STYLE_BASE}
              />
            </Box>
            {targets.length === 0 && (
              <Typography variant="caption" sx={{ color: "#f87171" }}>At least one target is required.</Typography>
            )}
          </Box>
        )}

        {tab === 3 && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Schedule" />
            <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
              <TextField
                select
                size="small"
                label="Recurrence"
                value={scheduleType}
                onChange={(e) => setScheduleType(e.target.value)}
                sx={{ minWidth: 240, flex: "1 1 260px", ...INPUT_FIELD_SX }}
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

            <Divider sx={{ my: 2, borderColor: MAGIC_UI.panelBorder, opacity: 0.6 }} />
            <SectionHeader title="Duration" />
            <FormControlLabel
              sx={{
                color: MAGIC_UI.textBright,
                alignItems: "center",
                "& .MuiTypography-root": { color: MAGIC_UI.textBright, fontSize: 13 },
              }}
              control={
                <Checkbox
                  checked={stopAfterEnabled}
                  onChange={(e) => setStopAfterEnabled(e.target.checked)}
                  sx={{
                    color: MAGIC_UI.accentA,
                    "&.Mui-checked": { color: MAGIC_UI.accentB },
                  }}
                />
              }
              label="Stop running this job after"
            />
            <TextField
              select
              size="small"
              label="Expiration"
              value={expiration}
              onChange={(e) => setExpiration(e.target.value)}
              sx={{ mt: 1, maxWidth: 260, ...INPUT_FIELD_SX }}
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
        )}

        {tab === 4 && (
          <Box sx={TAB_SECTION_SX}>
            <SectionHeader title="Execution Context" />
            <TextField
              select
              size="small"
              label="Context"
              value={execContext}
              onChange={(e) => handleExecContextChange(e.target.value)}
              sx={{ minWidth: 320, ...INPUT_FIELD_SX }}
            >
              <MenuItem value="system">Run on agent as SYSTEM (device-local)</MenuItem>
              <MenuItem value="current_user">Run on agent as logged-in user (device-local)</MenuItem>
              <MenuItem value="ssh">Run from server via SSH (remote)</MenuItem>
              <MenuItem value="winrm">Run from server via WinRM (remote)</MenuItem>
            </TextField>
            {remoteExec && (
              <Box sx={{ mt: 2, display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
                {execContext === "winrm" && (
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
                  sx={{ minWidth: 280, ...INPUT_FIELD_SX }}
                  disabled={credentialLoading || !filteredCredentials.length || (execContext === "winrm" && useSvcAccount)}
                >
                  {filteredCredentials.map((cred) => (
                    <MenuItem key={cred.id} value={String(cred.id)}>
                      {cred.name}
                    </MenuItem>
                  ))}
                </TextField>
                <Button
                  size="small"
                  variant="outlined"
                  startIcon={<RefreshIcon fontSize="small" />}
                  onClick={loadCredentials}
                  disabled={credentialLoading}
                  sx={{ ...OUTLINE_BUTTON_SX, borderColor: MAGIC_UI.accentA, color: MAGIC_UI.accentA }}
                >
                  Refresh
                </Button>
                {credentialLoading && <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />}
                {!credentialLoading && credentialError && (
                  <Typography variant="body2" sx={{ color: "#f87171" }}>
                    {credentialError}
                  </Typography>
                )}
                {execContext === "winrm" && useSvcAccount && (
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                    Runs with the agent&apos;s svcBorealis account.
                  </Typography>
                )}
                {!credentialLoading &&
                  !credentialError &&
                  !filteredCredentials.length &&
                  !(execContext === "winrm" && useSvcAccount) && (
                    <Typography variant="body2" sx={{ color: "#f87171" }}>
                      No {execContext === "winrm" ? "WinRM" : "SSH"} credentials available. Create one under Access Management &gt; Credentials.
                    </Typography>
                  )}
              </Box>
            )}
          </Box>
        )}

        {editing && tab === historyTabIndex && (
          <Box sx={{ display: "flex", flexDirection: "column", gap: 3 }}>
            <GlassPanel>
              <Box sx={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                <Typography variant="h6" sx={{ color: MAGIC_UI.textBright }}>
                  Job History
                </Typography>
                <Button
                  size="small"
                  variant="outlined"
                  sx={{ ...OUTLINE_BUTTON_SX, borderColor: "#fb7185", color: "#fb7185" }}
                  onClick={async () => {
                    try {
                      await fetch(`/api/scheduled_jobs/${initialJob.id}/runs`, { method: "DELETE" });
                      await loadHistory();
                    } catch {}
                  }}
                >
                  Clear Job History
                </Button>
              </Box>
              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                Showing the last 30 days of runs.
              </Typography>
            </GlassPanel>

            <JobStatusFlow />

            <GlassPanel>
              <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, mb: 0.5 }}>
                Devices
              </Typography>
              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                Devices targeted by this scheduled job. Individual job history is listed here.
              </Typography>
              <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1, justifyContent: "flex-end", mt: 1 }}>
                {DEVICE_COLUMNS.map((col) => (
                  <Button
                    key={col.key}
                    size="small"
                    startIcon={<FilterListIcon fontSize="inherit" />}
                    onClick={(event) => openFilterMenu(event, col.key)}
                    sx={{
                      textTransform: "none",
                      borderRadius: 999,
                      borderColor: isColumnFiltered(col.key) ? MAGIC_UI.accentA : "rgba(148,163,184,0.3)",
                      color: isColumnFiltered(col.key) ? MAGIC_UI.accentA : MAGIC_UI.textMuted,
                    }}
                    variant="outlined"
                  >
                    {col.label}
                  </Button>
                ))}
              </Box>
              <Box className={gridThemeClass} sx={{ ...GRID_WRAPPER_SX, height: 360 }}>
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
                  paginationPageSize={20}
                  paginationPageSizeSelector={[20, 50, 100]}
                  overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No targets found for this job.</span>"
                  getRowId={(params) => params.data?.id || params.rowIndex}
                  onGridReady={handleJobHistoryGridReady}
                  theme={gridTheme}
                  style={GRID_STYLE_BASE}
                />
              </Box>
              <Menu
                anchorEl={filterAnchorEl}
                open={Boolean(filterAnchorEl)}
                onClose={closeFilterMenu}
                disableAutoFocusItem
                PaperProps={{
                  sx: {
                    bgcolor: "rgba(8,12,24,0.96)",
                    color: MAGIC_UI.textBright,
                    minWidth: 240,
                    border: `1px solid ${MAGIC_UI.panelBorder}`,
                  },
                }}
              >
                <Box sx={{ p: 1.5, display: "flex", flexDirection: "column", gap: 1 }}>
                  {renderFilterControl()}
                  <Box sx={{ display: "flex", justifyContent: "space-between", gap: 1 }}>
                    <Button size="small" sx={{ color: "#fb7185", textTransform: "none" }} onClick={clearFilter}>
                      Clear
                    </Button>
                    <Button size="small" sx={{ color: MAGIC_UI.accentA, textTransform: "none" }} onClick={applyFilter}>
                      Apply
                    </Button>
                  </Box>
                </Box>
              </Menu>
            </GlassPanel>

            <GlassPanel>
              <Typography variant="subtitle1" sx={{ color: MAGIC_UI.textBright, mb: 0.5 }}>
                Past Job History
              </Typography>
              <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                Historical job history summaries. Detailed job history is not recorded.
              </Typography>
              <Box className={gridThemeClass} sx={{ ...GRID_WRAPPER_SX, mt: 1, height: 300 }}>
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
                  style={GRID_STYLE_BASE}
                />
              </Box>
            </GlassPanel>
          </Box>
        )}
      </Box>

      <Dialog
        open={outputOpen}
        onClose={() => setOutputOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{
          sx: {
            background: MAGIC_UI.panelBg,
            color: MAGIC_UI.textBright,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            boxShadow: MAGIC_UI.glow,
          },
        }}
      >
        <DialogTitle>{outputTitle}</DialogTitle>
        <DialogContent dividers sx={{ borderColor: MAGIC_UI.panelBorder }}>
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
                <Box key={section.key} sx={{ mb: 2 }}>
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
                    }}
                  >
                    <Editor
                      value={section.content ?? ""}
                      onValueChange={() => {}}
                      highlight={(code) => highlightCode(code, section.lang)}
                      padding={12}
                      style={{
                        fontFamily:
                          'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                        fontSize: 12,
                        color: "#e6edf3",
                        minHeight: 160,
                      }}
                      textareaProps={{ readOnly: true }}
                    />
                  </Box>
                </Box>
              ))
            : null}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setOutputOpen(false)} sx={{ color: MAGIC_UI.accentA, textTransform: "none" }}>
            Close
          </Button>
        </DialogActions>
      </Dialog>

      {/* Bottom actions removed per design; actions live next to tabs. */}

      {/* Add Component Dialog */}
      <Dialog
        open={addCompOpen}
        onClose={() => setAddCompOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{
          sx: {
            background: MAGIC_UI.panelBg,
            color: MAGIC_UI.textBright,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            boxShadow: MAGIC_UI.glow,
          },
        }}
      >
        <DialogTitle>Select an Assembly</DialogTitle>
        <DialogContent>
          <Box
            sx={{
              display: "flex",
              flexWrap: "wrap",
              gap: 2,
              mb: 2,
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <Box sx={{ display: "flex", gap: 1 }}>
              {["scripts", "ansible", "workflows"].map((key) => (
                <Button
                  key={key}
                  size="small"
                  variant={compTab === key ? "outlined" : "text"}
                  onClick={() => setCompTab(key)}
                  sx={{
                    textTransform: "none",
                    color: compTab === key ? MAGIC_UI.accentA : MAGIC_UI.textMuted,
                    borderColor: MAGIC_UI.accentA,
                  }}
                >
                  {key.charAt(0).toUpperCase() + key.slice(1)}
                </Button>
              ))}
            </Box>
            <Box sx={{ display: "flex", gap: 1, alignItems: "center" }}>
              <TextField
                size="small"
                placeholder="Search assemblies…"
                value={assemblyFilterText}
                onChange={(e) => setAssemblyFilterText(e.target.value)}
                sx={{ minWidth: 220, ...INPUT_FIELD_SX }}
              />
              <IconButton
                size="small"
                onClick={() => loadAssemblies()}
                sx={{
                  color: MAGIC_UI.accentA,
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  borderRadius: 2,
                  width: 38,
                  height: 38,
                }}
              >
                <RefreshIcon fontSize="small" />
              </IconButton>
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
          <Box className={gridThemeClass} sx={{ ...GRID_WRAPPER_SX, height: 420 }}>
            <AgGridReact
              rowData={filteredAssemblyRows}
              columnDefs={assemblyColumnDefs}
              defaultColDef={assemblyDefaultColDef}
              suppressCellFocus
              headerHeight={44}
              rowHeight={48}
              domLayout="normal"
              overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No assemblies available.</span>"
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              theme={gridTheme}
              style={GRID_STYLE_BASE}
              rowSelection="single"
              animateRows
              getRowId={(params) => params.data?.id || params.rowIndex}
              onGridReady={handleAssemblyGridReady}
              onRowClicked={handleAssemblyRowClick}
              onRowDoubleClicked={handleAssemblyRowDoubleClick}
              onSelectionChanged={handleAssemblySelectionChanged}
            />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddCompOpen(false)} sx={{ color: MAGIC_UI.textMuted, textTransform: "none" }}>
            Close
          </Button>
          <Button
            onClick={async () => {
              const ok = await addSelectedComponent();
              if (ok) setAddCompOpen(false);
            }}
            sx={{ color: MAGIC_UI.accentA, textTransform: "none" }}
            disabled={!selectedAssemblyRecord}
          >
            Add
          </Button>
        </DialogActions>
      </Dialog>

      {/* Add Targets Dialog */}
      <Dialog
        open={addTargetOpen}
        onClose={() => setAddTargetOpen(false)}
        fullWidth
        maxWidth="md"
        PaperProps={{
          sx: {
            background: MAGIC_UI.panelBg,
            color: MAGIC_UI.textBright,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            boxShadow: MAGIC_UI.glow,
          },
        }}
      >
        <DialogTitle>Select Targets</DialogTitle>
        <DialogContent>
          <Tabs
            value={targetPickerTab}
            onChange={(_, value) => setTargetPickerTab(value)}
            sx={{
              mb: 2,
              borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
              "& .MuiTab-root": { textTransform: "none", color: MAGIC_UI.textMuted },
              "& .Mui-selected": { color: MAGIC_UI.textBright },
            }}
            textColor="inherit"
            indicatorColor="primary"
          >
            <Tab label="Devices" value="devices" />
            <Tab label="Filters" value="filters" />
          </Tabs>

          {targetPickerTab === "devices" ? (
            <>
              <Box sx={{ mb: 2, display: "flex", gap: 2 }}>
                <TextField
                  size="small"
                  placeholder="Search devices..."
                  value={deviceSearch}
                  onChange={(e) => setDeviceSearch(e.target.value)}
                  sx={{ flex: 1, ...INPUT_FIELD_SX }}
                />
              </Box>
              <Table size="small" sx={TABLE_BASE_SX}>
                <TableHead>
                  <TableRow>
                    <TableCell width={40}></TableCell>
                    <TableCell>Name</TableCell>
                    <TableCell>Status</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {availableDevices
                    .filter((d) => d.display.toLowerCase().includes(deviceSearch.toLowerCase()))
                    .map((d) => (
                      <TableRow key={d.hostname} hover onClick={() => setSelectedDeviceTargets((prev) => ({ ...prev, [d.hostname]: !prev[d.hostname] }))}>
                        <TableCell>
                          <Checkbox
                            size="small"
                            checked={!!selectedDeviceTargets[d.hostname]}
                            onChange={(e) => {
                              e.stopPropagation();
                              setSelectedDeviceTargets((prev) => ({ ...prev, [d.hostname]: e.target.checked }));
                            }}
                            sx={{
                              color: MAGIC_UI.accentA,
                              "&.Mui-checked": { color: MAGIC_UI.accentB },
                            }}
                          />
                        </TableCell>
                        <TableCell>{d.display}</TableCell>
                        <TableCell>
                          <span style={{ display: "inline-block", width: 10, height: 10, borderRadius: 10, background: d.online ? "#00d18c" : "#ff4f4f", marginRight: 8, verticalAlign: "middle" }} />
                          {d.online ? "Online" : "Offline"}
                        </TableCell>
                      </TableRow>
                    ))}
                  {availableDevices.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={3} sx={{ color: MAGIC_UI.textMuted }}>
                        No devices available.
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </>
          ) : (
            <>
              <Box sx={{ mb: 2, display: "flex", gap: 2 }}>
                <TextField
                  size="small"
                  placeholder="Search filters..."
                  value={filterSearch}
                  onChange={(e) => setFilterSearch(e.target.value)}
                  sx={{ flex: 1, ...INPUT_FIELD_SX }}
                />
              </Box>
              <Table size="small" sx={TABLE_BASE_SX}>
                <TableHead>
                  <TableRow>
                    <TableCell width={40}></TableCell>
                    <TableCell>Filter</TableCell>
                    <TableCell>Devices</TableCell>
                    <TableCell>Scope</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {(filterCatalog || [])
                    .filter((f) => (f.name || "").toLowerCase().includes(filterSearch.toLowerCase()))
                    .map((f) => (
                      <TableRow key={f.id} hover onClick={() => setSelectedFilterTargets((prev) => ({ ...prev, [f.id]: !prev[f.id] }))}>
                        <TableCell>
                          <Checkbox
                            size="small"
                            checked={!!selectedFilterTargets[f.id]}
                            onChange={(e) => {
                              e.stopPropagation();
                              setSelectedFilterTargets((prev) => ({ ...prev, [f.id]: e.target.checked }));
                            }}
                            sx={{
                              color: MAGIC_UI.accentA,
                              "&.Mui-checked": { color: MAGIC_UI.accentB },
                            }}
                          />
                        </TableCell>
                        <TableCell>{f.name}</TableCell>
                        <TableCell>{typeof f.deviceCount === "number" ? f.deviceCount.toLocaleString() : "—"}</TableCell>
                        <TableCell>{f.scope === "scoped" ? (f.site || "Specific Site") : "All Sites"}</TableCell>
                      </TableRow>
                    ))}
                  {!loadingFilterCatalog && (!filterCatalog || filterCatalog.length === 0) && (
                    <TableRow>
                      <TableCell colSpan={4} sx={{ color: MAGIC_UI.textMuted }}>
                        No filters available.
                      </TableCell>
                    </TableRow>
                  )}
                  {loadingFilterCatalog && (
                    <TableRow>
                      <TableCell colSpan={4} sx={{ color: MAGIC_UI.textMuted }}>
                        Loading filters…
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAddTargetOpen(false)} sx={{ color: MAGIC_UI.textMuted, textTransform: "none" }}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              if (targetPickerTab === "filters") {
                const additions = filterCatalog
                  .filter((f) => selectedFilterTargets[f.id])
                  .map((f) => ({
                    kind: "filter",
                    filter_id: f.id,
                    name: f.name,
                    site_scope: f.scope,
                    site: f.site,
                    deviceCount: f.deviceCount,
                  }));
                if (additions.length) addTargets(additions);
              } else {
                const chosenHosts = Object.keys(selectedDeviceTargets).filter((hostname) => selectedDeviceTargets[hostname]);
                if (chosenHosts.length) addTargets(chosenHosts);
              }
              setAddTargetOpen(false);
            }}
            sx={{ color: MAGIC_UI.accentA, textTransform: "none" }}
          >
            Add Selected
          </Button>
        </DialogActions>
      </Dialog>

      {/* Confirm Create Dialog */}
      <Dialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        PaperProps={{
          sx: {
            background: MAGIC_UI.panelBg,
            color: MAGIC_UI.textBright,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            boxShadow: MAGIC_UI.glow,
          },
        }}
      >
        <DialogTitle sx={{ pb: 0 }}>
          {initialJob && initialJob.id ? "Are you sure you wish to save changes?" : "Are you sure you wish to create this Job?"}
        </DialogTitle>
        <DialogActions sx={{ p: 2 }}>
          <Button onClick={() => setConfirmOpen(false)} sx={{ color: MAGIC_UI.textMuted, textTransform: "none" }}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              setConfirmOpen(false);
              handleCreate();
            }}
            variant="outlined"
            sx={{ ...OUTLINE_BUTTON_SX, borderColor: MAGIC_UI.accentA, color: MAGIC_UI.accentA }}
          >
            Confirm
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
