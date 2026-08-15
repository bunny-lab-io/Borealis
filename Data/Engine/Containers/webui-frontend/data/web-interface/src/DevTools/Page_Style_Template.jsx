import React, { useMemo, useRef, useCallback, useState } from "react";
import {
  Box,
  Button,
  Chip,
  Divider,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import {
  Add as AddIcon,
  BoltRounded as BoltRoundedIcon,
  Cached as CachedIcon,
  CheckCircleRounded as CheckCircleRoundedIcon,
  CloudDoneRounded as CloudDoneRoundedIcon,
  ContentCopyRounded as ContentCopyRoundedIcon,
  DeleteOutline as DeleteOutlineIcon,
  EditRounded as EditRoundedIcon,
  ErrorRounded as ErrorRoundedIcon,
  FactCheckRounded as FactCheckRoundedIcon,
  FolderRounded as FolderRoundedIcon,
  InfoOutlined as InfoOutlinedIcon,
  LanRounded as LanRoundedIcon,
  MemoryRounded as MemoryRoundedIcon,
  OpenInNewRounded as OpenInNewRoundedIcon,
  PauseCircleRounded as PauseCircleRoundedIcon,
  PlayArrowRounded as PlayArrowRoundedIcon,
  NotificationsActiveRounded as NotificationsActiveRoundedIcon,
  RocketLaunchRounded as RocketLaunchRoundedIcon,
  Search as SearchIcon,
  SaveRounded as SaveRoundedIcon,
  ScheduleRounded as ScheduleRoundedIcon,
  SecurityRounded as SecurityRoundedIcon,
  SettingsRounded as SettingsRoundedIcon,
  SpeedRounded as SpeedRoundedIcon,
  StorageRounded as StorageRoundedIcon,
  TerminalRounded as TerminalRoundedIcon,
  TimelineRounded as TimelineRoundedIcon,
  Tune as TuneIcon,
  WarningAmberRounded as WarningAmberRoundedIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.14), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(192, 132, 252, 0.14), transparent 60%), #040711",
  panelBg: "linear-gradient(165deg, rgba(7,10,24,0.94), rgba(8,13,32,0.9))",
  panelBgMuted: "rgba(15, 23, 42, 0.42)",
  panelBorder: "rgba(148, 163, 184, 0.28)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  health: "#34d399",
  warning: "#f59e0b",
  danger: "#fb7185",
  steel: "#94a3b8",
};

const SEMANTIC_TONES = Object.freeze({
  identity: {
    label: "Identity",
    helper: "Navigation, focus, selected rows, links, primary safe actions",
    color: MAGIC_UI.accentA,
    bg: "rgba(125, 211, 252, 0.12)",
    border: "rgba(125, 211, 252, 0.38)",
    icon: InfoOutlinedIcon,
  },
  health: {
    label: "Health",
    helper: "Online, healthy, success, connected, complete",
    color: MAGIC_UI.health,
    bg: "rgba(52, 211, 153, 0.11)",
    border: "rgba(52, 211, 153, 0.36)",
    icon: CheckCircleRoundedIcon,
  },
  warning: {
    label: "Warning",
    helper: "Warning, stale, read-only, degraded, needs attention",
    color: MAGIC_UI.warning,
    bg: "rgba(245, 158, 11, 0.12)",
    border: "rgba(245, 158, 11, 0.36)",
    icon: WarningAmberRoundedIcon,
  },
  danger: {
    label: "Danger",
    helper: "Delete, purge, terminate, failed, critical",
    color: MAGIC_UI.danger,
    bg: "rgba(251, 113, 133, 0.11)",
    border: "rgba(251, 113, 133, 0.36)",
    icon: ErrorRoundedIcon,
  },
  neutral: {
    label: "Neutral",
    helper: "Inactive, metadata, dividers, routine controls, disabled states",
    color: MAGIC_UI.steel,
    bg: "rgba(148, 163, 184, 0.1)",
    border: "rgba(148, 163, 184, 0.28)",
    icon: TuneIcon,
  },
});

const SAMPLE_ROWS = [
  {
    id: "TPL-001",
    name: "Borealis Engine",
    state: "Healthy",
    tone: "health",
    signal: "Connected",
    owner: "Runtime",
    updated: "04/24/2026 10:55 PM",
  },
  {
    id: "TPL-002",
    name: "Agent Artifact",
    state: "Needs Update",
    tone: "warning",
    signal: "Review",
    owner: "Device",
    updated: "04/24/2026 10:51 PM",
  },
  {
    id: "TPL-003",
    name: "Purge Expired Logs",
    state: "Destructive",
    tone: "danger",
    signal: "Confirm",
    owner: "Admin",
    updated: "04/20/2026 04:57 PM",
  },
  {
    id: "TPL-004",
    name: "Column Layout",
    state: "Neutral",
    tone: "neutral",
    signal: "Configure",
    owner: "Operator",
    updated: "04/20/2026 04:55 PM",
  },
  {
    id: "TPL-005",
    name: "Selected Device",
    state: "Selected",
    tone: "identity",
    signal: "Focus",
    owner: "Inventory",
    updated: "04/20/2026 04:48 PM",
  },
];

const GRID_INSPECTOR_ROWS = [
  {
    id: "DEV-018",
    name: "edge-router-den-01",
    type: "Device",
    state: "Healthy",
    tone: "health",
    site: "Denver Core",
    owner: "Network",
    signal: "Connected",
    risk: "Low",
    updated: "04/27/2026 09:42 AM",
    primaryAction: "Open Console",
    secondaryAction: "Run Diagnostics",
    health: 98,
    nextEvent: "Watchdog check in 4m",
    config: "Release channel stable, VPN enabled, SSH credential nicole",
    log: "Agent handshake accepted. Inventory payload processed.",
  },
  {
    id: "JOB-204",
    name: "Nightly Patch Audit",
    type: "Scheduled Job",
    state: "Needs Attention",
    tone: "warning",
    site: "All Sites",
    owner: "Automation",
    signal: "3 failures",
    risk: "Medium",
    updated: "04/27/2026 08:15 AM",
    primaryAction: "Review Runs",
    secondaryAction: "Run Now",
    health: 64,
    nextEvent: "Next run 11:00 PM",
    config: "Cron 0 23 * * *, 142 targets, retry once",
    log: "Three Windows targets returned timeout during patch scan.",
  },
  {
    id: "ASM-071",
    name: "Provision Linux Baseline",
    type: "Assembly",
    state: "Read-only",
    tone: "warning",
    site: "Lab",
    owner: "Platform",
    signal: "Locked",
    risk: "Medium",
    updated: "04/26/2026 05:31 PM",
    primaryAction: "Open Graph",
    secondaryAction: "Clone",
    health: 72,
    nextEvent: "Approval required",
    config: "Uses SSH credential linux-global, 9 nodes, signed revision",
    log: "Draft blocked by read-only release policy.",
  },
  {
    id: "ALT-533",
    name: "Credential Rotation Failed",
    type: "Alert",
    state: "Critical",
    tone: "danger",
    site: "Production",
    owner: "Security",
    signal: "Escalated",
    risk: "High",
    updated: "04/27/2026 10:03 AM",
    primaryAction: "Acknowledge",
    secondaryAction: "Open Incident",
    health: 18,
    nextEvent: "Escalates in 12m",
    config: "Credential nicole-prod, 16 devices affected, auto-remediation blocked",
    log: "Rotation command failed. Secret write rejected by target vault.",
  },
  {
    id: "USR-027",
    name: "nicole.rappe",
    type: "User",
    state: "Disabled",
    tone: "neutral",
    site: "Global",
    owner: "Access",
    signal: "Inactive",
    risk: "Low",
    updated: "04/25/2026 01:10 PM",
    primaryAction: "View Access",
    secondaryAction: "Export Audit",
    health: 40,
    nextEvent: "No scheduled activity",
    config: "Admin role removed, API tokens revoked, console access disabled",
    log: "User disabled by administrator. Active sessions terminated.",
  },
];

const COCKPIT_METRICS = [
  {
    label: "Fleet Health",
    value: "94%",
    helper: "1,284 online / 38 need attention",
    tone: "health",
    icon: CloudDoneRoundedIcon,
    trend: "+2.4%",
  },
  {
    label: "Automation",
    value: "312",
    helper: "jobs completed in last 24h",
    tone: "identity",
    icon: RocketLaunchRoundedIcon,
    trend: "18 queued",
  },
  {
    label: "Security",
    value: "7",
    helper: "credential or trust events",
    tone: "warning",
    icon: SecurityRoundedIcon,
    trend: "3 high",
  },
  {
    label: "Runtime Load",
    value: "68%",
    helper: "engine workers under target",
    tone: "neutral",
    icon: MemoryRoundedIcon,
    trend: "stable",
  },
];

const COCKPIT_ATTENTION = [
  {
    title: "Credential Rotation Failed",
    scope: "Production / Security",
    tone: "danger",
    time: "12m left",
    body: "16 devices blocked by vault write rejection.",
  },
  {
    title: "Nightly Patch Audit",
    scope: "All Sites / Automation",
    tone: "warning",
    time: "3 failures",
    body: "Windows targets timed out during patch scan.",
  },
  {
    title: "Agent Artifact",
    scope: "Denver Core / Inventory",
    tone: "warning",
    time: "needs update",
    body: "Staged agents behind Engine artifact by two versions.",
  },
  {
    title: "Provision Linux Baseline",
    scope: "Lab / Assemblies",
    tone: "neutral",
    time: "locked",
    body: "Signed revision waiting for approval.",
  },
];

const COCKPIT_TIMELINE = [
  { time: "10:04", label: "Engine worker pool scaled", tone: "health" },
  { time: "10:03", label: "Credential rotation alert escalated", tone: "danger" },
  { time: "09:58", label: "Watchdog sweep completed", tone: "health" },
  { time: "09:41", label: "Patch audit retried failed targets", tone: "warning" },
  { time: "09:12", label: "VPN mesh latency returned to normal", tone: "identity" },
];

const COCKPIT_DOMAINS = [
  { label: "Devices", value: 94, tone: "health" },
  { label: "Jobs", value: 76, tone: "warning" },
  { label: "Assemblies", value: 88, tone: "identity" },
  { label: "Access", value: 61, tone: "danger" },
  { label: "Logs", value: 82, tone: "neutral" },
];

const FILTER_SLIDER_OPTIONS = Object.freeze([
  { key: "active", label: "Active", count: 12 },
  { key: "archived", label: "Archived", count: 3 },
  { key: "windows_store", label: "Windows Store", count: 108 },
  { key: "local", label: "Locally Installed", count: 15 },
]);

const gridTheme = themeQuartz.withParams({
  accentColor: MAGIC_UI.accentA,
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const gridThemeClass = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans","Helvetica Neue",Arial,sans-serif';
const iconFontFamily = '"Quartz Regular"';

const ACTION_BUTTON_BASE_SX = {
  minHeight: 38,
  height: 38,
  px: 1.8,
  borderRadius: 999,
  fontFamily: gridFontFamily,
  fontWeight: 600,
  fontSize: "0.9rem",
  lineHeight: 1,
  textTransform: "none",
  whiteSpace: "nowrap",
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
    opacity: 1,
    color: "rgba(203, 213, 225, 0.45)",
    borderColor: "rgba(148, 163, 184, 0.18)",
    background: "rgba(51, 65, 85, 0.28)",
    boxShadow: "none",
  },
};

const PRIMARY_BUTTON_SX = {
  ...ACTION_BUTTON_BASE_SX,
  color: "#06101d",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 14px 30px rgba(125, 211, 252, 0.16)",
  "&:hover": {
    ...ACTION_BUTTON_BASE_SX["&:hover"],
    background: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
    boxShadow: "0 16px 32px rgba(192, 132, 252, 0.2)",
  },
};

const SECONDARY_BUTTON_SX = {
  ...ACTION_BUTTON_BASE_SX,
  color: MAGIC_UI.textBright,
  borderColor: "rgba(148, 163, 184, 0.42)",
  background: "rgba(5, 10, 24, 0.78)",
  boxShadow: "none",
  "&:hover": {
    ...ACTION_BUTTON_BASE_SX["&:hover"],
    borderColor: "rgba(125, 211, 252, 0.48)",
    background: "rgba(15, 23, 42, 0.72)",
    boxShadow: "none",
  },
};

const WARNING_BUTTON_SX = {
  ...SECONDARY_BUTTON_SX,
  color: "#ffd58a",
  borderColor: "rgba(245, 158, 11, 0.48)",
  "&:hover": {
    ...SECONDARY_BUTTON_SX["&:hover"],
    color: "#ffe4ad",
    borderColor: "rgba(245, 158, 11, 0.68)",
    background: "rgba(245, 158, 11, 0.1)",
  },
};

const DANGER_BUTTON_SX = {
  ...SECONDARY_BUTTON_SX,
  color: "#ff9aa7",
  borderColor: "rgba(251, 113, 133, 0.52)",
  "&:hover": {
    ...SECONDARY_BUTTON_SX["&:hover"],
    color: "#ffc2ca",
    borderColor: "rgba(251, 113, 133, 0.74)",
    background: "rgba(251, 113, 133, 0.1)",
  },
};

const PANEL_SX = {
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  borderRadius: 2,
  background: MAGIC_UI.panelBg,
  boxShadow: "0 18px 48px rgba(2, 6, 23, 0.48)",
};

const SECTION_HEADER_SX = {
  color: MAGIC_UI.accentA,
  fontSize: "0.78rem",
  fontWeight: 700,
  letterSpacing: 0.8,
  textTransform: "uppercase",
};

const TEXT_FIELD_SX = {
  "& .MuiInputBase-root": {
    color: MAGIC_UI.textBright,
    backgroundColor: "rgba(5, 10, 24, 0.74)",
    borderRadius: 1.4,
  },
  "& .MuiOutlinedInput-notchedOutline": {
    borderColor: "rgba(148, 163, 184, 0.28)",
  },
  "& .MuiInputBase-root:hover .MuiOutlinedInput-notchedOutline": {
    borderColor: "rgba(125, 211, 252, 0.42)",
  },
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.textMuted,
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: MAGIC_UI.accentA,
  },
};

const selectionCol = {
  headerName: "",
  field: "__select__",
  width: 52,
  maxWidth: 52,
  checkboxSelection: true,
  headerCheckboxSelection: true,
  resizable: false,
  sortable: false,
  suppressMenu: true,
  filter: false,
  pinned: "left",
  lockPosition: true,
  cellClass: "ag-selection-centered",
};

const defaultColDef = {
  sortable: true,
  filter: "agTextColumnFilter",
  resizable: true,
  minWidth: 130,
  cellClass: "auto-col-tight",
};

function getTone(toneKey) {
  return SEMANTIC_TONES[toneKey] || SEMANTIC_TONES.neutral;
}

function StatusPill({ tone = "neutral", label }) {
  const token = getTone(tone);
  return (
    <Box
      component="span"
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.65,
        height: 24,
        px: 1,
        borderRadius: 999,
        color: token.color,
        backgroundColor: token.bg,
        border: `1px solid ${token.border}`,
        fontSize: "0.72rem",
        fontWeight: 700,
        lineHeight: 1,
      }}
    >
      <Box
        component="span"
        sx={{
          width: 7,
          height: 7,
          borderRadius: "50%",
          backgroundColor: token.color,
          boxShadow: `0 0 10px ${token.color}`,
        }}
      />
      {label}
    </Box>
  );
}

function ToneCard({ toneKey }) {
  const token = getTone(toneKey);
  const Icon = token.icon;

  return (
    <Box
      sx={{
        ...PANEL_SX,
        p: 2,
        background:
          `linear-gradient(145deg, ${token.bg}, rgba(7,10,24,0.92) 48%, rgba(8,13,32,0.9))`,
        borderColor: token.border,
      }}
    >
      <Stack spacing={1.2}>
        <Stack direction="row" alignItems="center" spacing={1}>
          <Icon fontSize="small" sx={{ color: token.color }} />
          <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
            {token.label}
          </Typography>
        </Stack>
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem", lineHeight: 1.45 }}>
          {token.helper}
        </Typography>
        <Box
          sx={{
            height: 6,
            width: "100%",
            borderRadius: 999,
            backgroundColor: token.color,
            boxShadow: `0 0 18px ${token.color}33`,
          }}
        />
      </Stack>
    </Box>
  );
}

function DesignNote({ tone = "identity", title, body }) {
  const token = getTone(tone);
  return (
    <Box
      sx={{
        p: 1.6,
        borderRadius: 2,
        backgroundColor: token.bg,
        border: `1px solid ${token.border}`,
      }}
    >
      <Typography sx={{ color: token.color, fontWeight: 700, mb: 0.45 }}>
        {title}
      </Typography>
      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem", lineHeight: 1.45 }}>
        {body}
      </Typography>
    </Box>
  );
}

function FilterSliderDemo({ activeKey, onSelect }) {
  const activeOption = FILTER_SLIDER_OPTIONS.find((option) => option.key === activeKey);
  const totalCount = FILTER_SLIDER_OPTIONS.reduce((sum, option) => sum + option.count, 0);
  const summary = activeOption
    ? `Showing ${activeOption.count} ${activeOption.label.toLowerCase()} entries`
    : `Showing all ${totalCount} entries`;

  return (
    <Stack spacing={1.4}>
      <Stack
        direction={{ xs: "column", md: "row" }}
        spacing={1.2}
        alignItems={{ xs: "stretch", md: "center" }}
        justifyContent="space-between"
      >
        <Box
          sx={{
            display: "inline-flex",
            alignItems: "center",
            gap: 0.5,
            width: "fit-content",
            maxWidth: "100%",
            p: "4px",
            borderRadius: 999,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: "rgba(5, 10, 24, 0.76)",
            overflowX: "auto",
          }}
        >
          {FILTER_SLIDER_OPTIONS.map((option) => {
            const active = option.key === activeKey;
            return (
              <Box
                key={option.key}
                component="button"
                type="button"
                onClick={() => onSelect(active ? "" : option.key)}
                sx={{
                  height: 30,
                  display: "inline-flex",
                  alignItems: "center",
                  gap: 0.75,
                  px: 1.35,
                  border: 0,
                  borderRadius: 999,
                  color: active ? "#06101d" : MAGIC_UI.textBright,
                  background: active
                    ? "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)"
                    : "transparent",
                  fontFamily: gridFontFamily,
                  fontSize: "0.82rem",
                  fontWeight: 700,
                  whiteSpace: "nowrap",
                  cursor: "pointer",
                  transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
                  "&:hover": {
                    background: active
                      ? "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)"
                      : "rgba(255,255,255,0.06)",
                  },
                  "&:active": {
                    transform: "translateY(0.5px)",
                  },
                }}
              >
                {option.label}
                <Box
                  component="span"
                  sx={{
                    minWidth: 22,
                    height: 18,
                    px: 0.65,
                    borderRadius: 999,
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    color: active ? "#06101d" : MAGIC_UI.textMuted,
                    backgroundColor: active ? "rgba(6, 16, 29, 0.18)" : "rgba(148, 163, 184, 0.12)",
                    border: active ? "1px solid rgba(6, 16, 29, 0.18)" : "1px solid rgba(148, 163, 184, 0.2)",
                    fontSize: "0.72rem",
                    fontWeight: 800,
                  }}
                >
                  {option.count}
                </Box>
              </Box>
            );
          })}
        </Box>
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem" }}>
          {summary}
        </Typography>
      </Stack>
      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem" }}>
        Click active segment again to clear filter.
      </Typography>
    </Stack>
  );
}

function SearchControlPreview() {
  return (
    <Box
      sx={{
        height: 38,
        maxWidth: 420,
        display: "flex",
        alignItems: "center",
        gap: 1,
        px: 1.5,
        borderRadius: 999,
        border: "1px solid rgba(125, 211, 252, 0.22)",
        background: "rgba(5, 10, 24, 0.86)",
        color: MAGIC_UI.textMuted,
      }}
    >
      <SearchIcon fontSize="small" sx={{ color: MAGIC_UI.accentA }} />
      <Typography sx={{ flex: 1, minWidth: 0, color: MAGIC_UI.textMuted, fontSize: "0.9rem" }}>
        Search devices by hostname...
      </Typography>
      <Chip
        label="Min 3 chars"
        size="small"
        sx={{
          height: 22,
          color: MAGIC_UI.textBright,
          backgroundColor: "rgba(192, 132, 252, 0.12)",
          border: "1px solid rgba(192, 132, 252, 0.22)",
          fontWeight: 700,
          fontSize: "0.68rem",
        }}
      />
    </Box>
  );
}

function ToastStackPreview() {
  const items = [
    { tone: "identity", title: "Info", body: "Inventory refresh completed." },
    { tone: "warning", title: "Warning", body: "Agent has not checked in recently." },
    { tone: "danger", title: "Error", body: "Failed to terminate selected process." },
  ];

  return (
    <Stack spacing={1}>
      {items.map((item) => {
        const token = getTone(item.tone);
        return (
          <Box
            key={item.title}
            sx={{
              display: "grid",
              gridTemplateColumns: "24px 1fr",
              gap: 1,
              p: 1.15,
              borderRadius: 2,
              border: `1px solid ${token.border}`,
              background:
                `linear-gradient(135deg, ${token.bg}, rgba(8, 12, 24, 0.94))`,
            }}
          >
            <NotificationsActiveRoundedIcon fontSize="small" sx={{ color: token.color }} />
            <Box sx={{ minWidth: 0 }}>
              <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700, fontSize: "0.84rem" }}>
                {item.title}
              </Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem" }}>
                {item.body}
              </Typography>
            </Box>
          </Box>
        );
      })}
    </Stack>
  );
}

function ContextMenuPreview() {
  const menuGroups = [
    {
      label: "Primary",
      items: [
        { icon: <FolderRoundedIcon fontSize="small" />, label: "Open", helper: "Open selected item." },
        { icon: <ContentCopyRoundedIcon fontSize="small" />, label: "Copy Path", helper: "Copy location to clipboard." },
      ],
    },
    {
      label: "Organize",
      items: [
        { icon: <EditRoundedIcon fontSize="small" />, label: "Rename", helper: "Change display name." },
      ],
    },
    {
      label: "Danger Zone",
      tone: "danger",
      items: [
        { icon: <DeleteOutlineIcon fontSize="small" />, label: "Delete", helper: "Permanently remove selected item." },
      ],
    },
  ];

  return (
    <Box
      sx={{
        width: "100%",
        maxWidth: 360,
        borderRadius: 2,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "rgba(8,12,24,0.96)",
        boxShadow: "0 18px 42px rgba(2, 6, 23, 0.55)",
        overflow: "hidden",
      }}
    >
      <Stack direction="row" spacing={1} alignItems="center" sx={{ p: 1.35 }}>
        <FolderRoundedIcon sx={{ color: MAGIC_UI.accentA }} />
        <Box sx={{ minWidth: 0 }}>
          <Typography noWrap sx={{ color: MAGIC_UI.textBright, fontWeight: 700, fontSize: "0.86rem" }}>
            lorem_ipsum.json
          </Typography>
          <Typography noWrap sx={{ color: MAGIC_UI.textMuted, fontSize: "0.74rem" }}>
            C:\Users\nicole.rappe\Desktop\Test
          </Typography>
        </Box>
      </Stack>
      {menuGroups.map((group, groupIndex) => (
        <Box key={group.label}>
          {groupIndex > 0 ? <Divider sx={{ borderColor: "rgba(148, 163, 184, 0.14)" }} /> : null}
          <Typography
            sx={{
              px: 1.35,
              pt: 1,
              pb: 0.5,
              color: "rgba(203, 213, 225, 0.62)",
              fontSize: "0.68rem",
              fontWeight: 800,
              letterSpacing: 0.8,
              textTransform: "uppercase",
            }}
          >
            {group.label}
          </Typography>
          {group.items.map((item) => {
            const token = getTone(group.tone || "identity");
            return (
              <Stack
                key={item.label}
                direction="row"
                spacing={1}
                sx={{
                  position: "relative",
                  px: 1.35,
                  py: 0.95,
                  color: group.tone === "danger" ? token.color : MAGIC_UI.textBright,
                  "&:before": {
                    content: '""',
                    position: "absolute",
                    left: 0,
                    top: 8,
                    bottom: 8,
                    width: 3,
                    borderRadius: 999,
                    backgroundColor: group.tone === "danger" ? token.color : MAGIC_UI.accentA,
                  },
                  backgroundColor: group.tone === "danger" ? "rgba(251, 113, 133, 0.06)" : "rgba(125, 211, 252, 0.05)",
                }}
              >
                <Box sx={{ mt: 0.15, color: group.tone === "danger" ? token.color : MAGIC_UI.accentA }}>
                  {item.icon}
                </Box>
                <Box sx={{ minWidth: 0 }}>
                  <Typography sx={{ fontWeight: 700, fontSize: "0.82rem" }}>
                    {item.label}
                  </Typography>
                  <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.74rem" }}>
                    {item.helper}
                  </Typography>
                </Box>
              </Stack>
            );
          })}
        </Box>
      ))}
    </Box>
  );
}

function DialogPreview() {
  return (
    <Box
      sx={{
        borderRadius: 2,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background:
          "linear-gradient(145deg, rgba(11, 18, 34, 0.96), rgba(30, 20, 48, 0.9))",
        p: 2,
      }}
    >
      <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 750 }}>
        Edit Credential
      </Typography>
      <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.35, mb: 1.6, fontSize: "0.84rem" }}>
        Update stored authentication details.
      </Typography>
      <Stack spacing={1.35}>
        <TextField
          label="Name"
          size="small"
          value="nicole"
          InputProps={{ readOnly: true }}
          sx={TEXT_FIELD_SX}
        />
        <TextField
          label="Description"
          size="small"
          value="Global Infrastructure SSH Credential"
          InputProps={{ readOnly: true }}
          sx={TEXT_FIELD_SX}
        />
        <Stack direction="row" spacing={1} justifyContent="flex-end" sx={{ pt: 0.8 }}>
          <Button sx={SECONDARY_BUTTON_SX}>Cancel</Button>
          <Button startIcon={<SaveRoundedIcon />} sx={PRIMARY_BUTTON_SX}>
            Save
          </Button>
        </Stack>
      </Stack>
    </Box>
  );
}

function PatternLibrary({ activeFilter, onFilterSelect }) {
  return (
    <Box sx={{ ...PANEL_SX, p: 2.25 }}>
      <Stack spacing={2}>
        <Box>
          <Typography sx={SECTION_HEADER_SX}>Interaction Patterns</Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6 }}>
            Canonical controls from UI and notification guidance, sized for reuse in real pages.
          </Typography>
        </Box>
        <Box
          sx={{
            display: "grid",
            gridTemplateColumns: { xs: "1fr", xl: "1.1fr 0.9fr" },
            gap: 2,
          }}
        >
          <Stack spacing={2}>
            <Box sx={{ ...PANEL_SX, p: 2, boxShadow: "none" }}>
              <Typography sx={SECTION_HEADER_SX}>Filter Slider</Typography>
              <Box sx={{ mt: 1.2 }}>
                <FilterSliderDemo activeKey={activeFilter} onSelect={onFilterSelect} />
              </Box>
            </Box>
            <Box sx={{ ...PANEL_SX, p: 2, boxShadow: "none" }}>
              <Typography sx={SECTION_HEADER_SX}>Global Device Search</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, mb: 1.2, fontSize: "0.84rem" }}>
                Compact dark glass control, inactive until 3 hostname characters.
              </Typography>
              <SearchControlPreview />
            </Box>
            <Box sx={{ ...PANEL_SX, p: 2, boxShadow: "none" }}>
              <Typography sx={SECTION_HEADER_SX}>Toast Notifications</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, mb: 1.2, fontSize: "0.84rem" }}>
                Variants mirror info, warning, and error transport states.
              </Typography>
              <ToastStackPreview />
            </Box>
          </Stack>
          <Stack spacing={2}>
            <Box sx={{ ...PANEL_SX, p: 2, boxShadow: "none" }}>
              <Typography sx={SECTION_HEADER_SX}>Context Menu</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, mb: 1.2, fontSize: "0.84rem" }}>
                Header, semantic groups, inline helper copy, and danger section placement.
              </Typography>
              <ContextMenuPreview />
            </Box>
            <Box sx={{ ...PANEL_SX, p: 2, boxShadow: "none" }}>
              <Typography sx={SECTION_HEADER_SX}>Dialog Shell</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, mb: 1.2, fontSize: "0.84rem" }}>
                Quiet glass overlay, direct fields, pill actions, no extra separator chrome.
              </Typography>
              <DialogPreview />
            </Box>
          </Stack>
        </Box>
      </Stack>
    </Box>
  );
}

const INSPECTOR_TABS = Object.freeze(["Summary", "Activity", "Config", "Logs"]);

function InspectorStat({ icon: IconComponent, label, value, tone = "neutral" }) {
  const token = getTone(tone);

  return (
    <Box
      sx={{
        p: 1.35,
        borderRadius: 1.2,
        border: `1px solid ${token.border}`,
        background: `linear-gradient(145deg, ${token.bg}, rgba(8,12,24,0.78))`,
        minWidth: 0,
      }}
    >
      <Stack direction="row" spacing={1} alignItems="center">
        <IconComponent fontSize="small" sx={{ color: token.color }} />
        <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.76rem", fontWeight: 700 }}>
          {label}
        </Typography>
      </Stack>
      <Typography noWrap sx={{ color: MAGIC_UI.textBright, mt: 0.7, fontWeight: 750 }}>
        {value}
      </Typography>
    </Box>
  );
}

function InspectorTabPanel({ tabIndex, item }) {
  if (tabIndex === 1) {
    return (
      <Stack spacing={1.15}>
        {[
          ["10:03 AM", item.log],
          ["09:42 AM", `${item.owner} owner reviewed ${item.type.toLowerCase()}.`],
          ["Yesterday", `${item.nextEvent}.`],
        ].map(([time, body]) => (
          <Box
            key={`${time}-${body}`}
            sx={{
              display: "grid",
              gridTemplateColumns: "74px 1fr",
              gap: 1.2,
              pb: 1.1,
              borderBottom: "1px solid rgba(148, 163, 184, 0.12)",
            }}
          >
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem" }}>
              {time}
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.84rem", lineHeight: 1.45 }}>
              {body}
            </Typography>
          </Box>
        ))}
      </Stack>
    );
  }

  if (tabIndex === 2) {
    return (
      <Stack spacing={1.2}>
        <DesignNote tone={item.tone} title="Effective Configuration" body={item.config} />
        <DesignNote
          tone="neutral"
          title="Composition Rule"
          body="Grid carries scan data. Inspector carries fields, actions, relationships, and policy context."
        />
      </Stack>
    );
  }

  if (tabIndex === 3) {
    return (
      <Box
        sx={{
          p: 1.5,
          borderRadius: 1.2,
          color: "#dbeafe",
          background: "rgba(2, 6, 23, 0.78)",
          border: "1px solid rgba(125, 211, 252, 0.16)",
          fontFamily: '"IBM Plex Mono","SFMono-Regular",Consolas,monospace',
          fontSize: "0.78rem",
          lineHeight: 1.7,
          whiteSpace: "pre-wrap",
        }}
      >
        {`[${item.updated}] ${item.id} ${item.state}\n${item.log}\nnext=${item.nextEvent}\nrisk=${item.risk.toLowerCase()}`}
      </Box>
    );
  }

  return (
    <Stack spacing={1.4}>
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
          gap: 1,
        }}
      >
        <InspectorStat icon={ScheduleRoundedIcon} label="Next Event" value={item.nextEvent} tone={item.tone} />
        <InspectorStat icon={LanRoundedIcon} label="Scope" value={item.site} tone="identity" />
        <InspectorStat icon={StorageRoundedIcon} label="Owner" value={item.owner} tone="neutral" />
        <InspectorStat icon={FactCheckRoundedIcon} label="Health" value={`${item.health}%`} tone={item.tone} />
      </Box>
      <DesignNote
        tone={item.tone}
        title={`${item.type} Snapshot`}
        body={`${item.name} is ${item.state.toLowerCase()}. ${item.signal}. ${item.nextEvent}.`}
      />
    </Stack>
  );
}

export function GridInspectorLayoutTemplate() {
  const [selectedId, setSelectedId] = useState(GRID_INSPECTOR_ROWS[0]?.id || "");
  const [inspectorTab, setInspectorTab] = useState(0);
  const selectedItem =
    GRID_INSPECTOR_ROWS.find((row) => row.id === selectedId) || GRID_INSPECTOR_ROWS[0];
  const getInspectorRowId = useCallback((params) => params.data?.id || String(params.rowIndex ?? ""), []);

  const inspectorColumnDefs = useMemo(
    () => [
      selectionCol,
      {
        headerName: "Object",
        field: "name",
        minWidth: 260,
        flex: 1.25,
        cellRenderer: (params) => (
          <Stack spacing={0.2} sx={{ minWidth: 0 }}>
            <Typography noWrap sx={{ color: MAGIC_UI.accentA, fontSize: "0.84rem", fontWeight: 750 }}>
              {params.value}
            </Typography>
            <Typography noWrap sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem" }}>
              {params.data?.id} - {params.data?.type}
            </Typography>
          </Stack>
        ),
      },
      {
        headerName: "State",
        field: "state",
        minWidth: 150,
        cellRenderer: (params) => <StatusPill tone={params.data?.tone} label={params.value} />,
      },
      { headerName: "Site", field: "site", minWidth: 140 },
      { headerName: "Signal", field: "signal", minWidth: 130 },
      {
        headerName: "Risk",
        field: "risk",
        minWidth: 115,
        cellRenderer: (params) => {
          const tone = params.value === "High" ? "danger" : params.value === "Medium" ? "warning" : "health";
          return <StatusPill tone={tone} label={params.value} />;
        },
      },
      { headerName: "Updated", field: "updated", minWidth: 190 },
    ],
    []
  );

  const onGridReady = useCallback(
    (params) => {
      window.requestAnimationFrame(() => {
        params.api.getRowNode(selectedId)?.setSelected(true);
      });
    },
    [selectedId]
  );

  const onRowClicked = useCallback((event) => {
    const nextId = event.data?.id;
    if (!nextId) {
      return;
    }
    setSelectedId(nextId);
    setInspectorTab(0);
    event.node.setSelected(true, true);
  }, []);

  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        gap: 2,
        color: MAGIC_UI.textBright,
      }}
    >
      <Box
        sx={{
          ...PANEL_SX,
          p: { xs: 2, md: 2.5 },
          background:
            "linear-gradient(135deg, rgba(7,10,24,0.97), rgba(13,20,38,0.9) 52%, rgba(18,38,54,0.54))",
        }}
      >
        <Stack
          direction={{ xs: "column", lg: "row" }}
          spacing={2}
          alignItems={{ xs: "stretch", lg: "center" }}
          justifyContent="space-between"
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={SECTION_HEADER_SX}>Grid Inspector Layout</Typography>
            <Typography variant="h5" sx={{ mt: 0.6, mb: 0.8, fontWeight: 750, letterSpacing: 0 }}>
              Data grid as command center, inspector as context brain
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, maxWidth: 940, lineHeight: 1.5 }}>
              Keep dense scanning in grid. Move details, tabs, related actions, and dangerous operations
              into persistent inspector instead of modals.
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
            <Button startIcon={<CachedIcon />} sx={SECONDARY_BUTTON_SX}>
              Refresh
            </Button>
            <Button startIcon={<OpenInNewRoundedIcon />} sx={SECONDARY_BUTTON_SX}>
              Open Full Page
            </Button>
            <Button startIcon={<AddIcon />} sx={PRIMARY_BUTTON_SX}>
              New Object
            </Button>
          </Stack>
        </Stack>
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", xl: "minmax(0, 1fr) minmax(360px, 440px)" },
          gap: 2,
          alignItems: "start",
        }}
      >
        <Box sx={{ ...PANEL_SX, overflow: "hidden", minWidth: 0 }}>
          <Stack
            direction={{ xs: "column", md: "row" }}
            spacing={1.25}
            alignItems={{ xs: "stretch", md: "center" }}
            justifyContent="space-between"
            sx={{
              px: 2,
              py: 1.4,
              borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
              backgroundColor: "rgba(15, 23, 42, 0.32)",
            }}
          >
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <StatusPill tone="identity" label="All Objects" />
              <StatusPill tone="warning" label="Needs Attention" />
              <StatusPill tone="danger" label="Critical" />
              <StatusPill tone="neutral" label="Disabled" />
            </Stack>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap justifyContent="flex-end">
              <Button startIcon={<PlayArrowRoundedIcon />} sx={SECONDARY_BUTTON_SX}>
                Run
              </Button>
              <Button startIcon={<PauseCircleRoundedIcon />} sx={SECONDARY_BUTTON_SX}>
                Pause
              </Button>
              <Button startIcon={<TuneIcon />} sx={SECONDARY_BUTTON_SX}>
                Columns
              </Button>
            </Stack>
          </Stack>
          <Box
            className={gridThemeClass}
            sx={{
              height: 560,
              width: "100%",
              fontFamily: gridFontFamily,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "--ag-background-color": "#070b1a",
              "--ag-foreground-color": "#f4f7ff",
              "--ag-header-background-color": "#0f172a",
              "--ag-header-foreground-color": "#cfe0ff",
              "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
              "--ag-row-hover-color": "rgba(73,156,196,0.2)",
              "--ag-selected-row-background-color": "rgba(125,211,252,0.16)",
              "--ag-border-color": "rgba(125,183,255,0.18)",
              "--ag-row-border-color": "rgba(125,183,255,0.14)",
              "--ag-border-radius": 0,
              "--ag-checkbox-border-radius": "3px",
              "& .ag-root-wrapper": {
                border: "none",
                background: "transparent",
              },
              "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
                display: "flex",
                alignItems: "center",
                justifyContent: "flex-start",
                textAlign: "left",
                padding: "8px 12px 8px 18px",
              },
              "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
                paddingLeft: "12px",
                paddingRight: "9px",
              },
              "& .ag-cell.ag-selection-centered, & .ag-cell.ag-selection-centered .ag-cell-wrapper": {
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                paddingLeft: 0,
                paddingRight: 0,
              },
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.16) !important",
                boxShadow: "inset 4px 0 0 #7dd3fc, inset 0 0 0 1px rgba(125,211,252,0.34)",
              },
            }}
          >
            <AgGridReact
              rowData={GRID_INSPECTOR_ROWS}
              columnDefs={inspectorColumnDefs}
              defaultColDef={defaultColDef}
              rowSelection="single"
              getRowId={getInspectorRowId}
              onGridReady={onGridReady}
              onRowClicked={onRowClicked}
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              theme={gridTheme}
              rowHeight={52}
              suppressCellFocus
            />
          </Box>
        </Box>

        <Box
          sx={{
            ...PANEL_SX,
            position: { xl: "sticky" },
            top: { xl: 16 },
            overflow: "hidden",
            minWidth: 0,
          }}
        >
          <Box
            sx={{
              p: 2,
              borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
              background:
                `linear-gradient(145deg, ${getTone(selectedItem.tone).bg}, rgba(8, 12, 24, 0.95))`,
            }}
          >
            <Stack spacing={1.4}>
              <Stack direction="row" spacing={1.2} alignItems="flex-start" justifyContent="space-between">
                <Box sx={{ minWidth: 0 }}>
                  <Typography sx={SECTION_HEADER_SX}>{selectedItem.type}</Typography>
                  <Typography noWrap sx={{ color: MAGIC_UI.textBright, mt: 0.4, fontSize: "1.2rem", fontWeight: 800 }}>
                    {selectedItem.name}
                  </Typography>
                  <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.2, fontSize: "0.82rem" }}>
                    {selectedItem.id} - {selectedItem.site}
                  </Typography>
                </Box>
                <StatusPill tone={selectedItem.tone} label={selectedItem.state} />
              </Stack>
              <Box
                sx={{
                  height: 8,
                  borderRadius: 999,
                  backgroundColor: "rgba(148, 163, 184, 0.12)",
                  overflow: "hidden",
                }}
              >
                <Box
                  sx={{
                    width: `${selectedItem.health}%`,
                    height: "100%",
                    borderRadius: 999,
                    backgroundColor: getTone(selectedItem.tone).color,
                    boxShadow: `0 0 18px ${getTone(selectedItem.tone).color}55`,
                  }}
                />
              </Box>
              <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                <Button startIcon={<TerminalRoundedIcon />} sx={PRIMARY_BUTTON_SX}>
                  {selectedItem.primaryAction}
                </Button>
                <Button startIcon={<SettingsRoundedIcon />} sx={SECONDARY_BUTTON_SX}>
                  {selectedItem.secondaryAction}
                </Button>
              </Stack>
            </Stack>
          </Box>

          <Box sx={{ px: 1.5, pt: 1.2, borderBottom: `1px solid ${MAGIC_UI.panelBorder}` }}>
            <Tabs
              value={inspectorTab}
              onChange={(_, nextTab) => setInspectorTab(nextTab)}
              variant="scrollable"
              scrollButtons="auto"
              TabIndicatorProps={{
                style: {
                  height: 3,
                  borderRadius: 3,
                  background: MAGIC_UI.accentA,
                },
              }}
              sx={{
                minHeight: 34,
                "& .MuiTabs-flexContainer": {
                  minHeight: 34,
                },
                "& .MuiTab-root": {
                  minHeight: 34,
                  px: 1.15,
                  py: 0.35,
                  color: MAGIC_UI.textMuted,
                  textTransform: "none",
                  fontSize: "0.78rem",
                  fontWeight: 700,
                  borderRadius: 1,
                  "&.Mui-selected": {
                    color: MAGIC_UI.textBright,
                    background: "rgba(125, 211, 252, 0.08)",
                  },
                },
              }}
            >
              {INSPECTOR_TABS.map((label) => (
                <Tab key={label} label={label} />
              ))}
            </Tabs>
          </Box>

          <Box sx={{ p: 2 }}>
            <InspectorTabPanel tabIndex={inspectorTab} item={selectedItem} />
          </Box>

          <Box
            sx={{
              p: 2,
              borderTop: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(251, 113, 133, 0.05)",
            }}
          >
            <Typography sx={{ color: MAGIC_UI.danger, fontWeight: 800, fontSize: "0.78rem", mb: 1 }}>
              Destructive Zone
            </Typography>
            <Button startIcon={<DeleteOutlineIcon />} sx={DANGER_BUTTON_SX}>
              Remove Selected Object
            </Button>
          </Box>
        </Box>
      </Box>
    </Box>
  );
}

function CockpitMetricCard({ metric }) {
  const token = getTone(metric.tone);
  const Icon = metric.icon;

  return (
    <Box
      sx={{
        ...PANEL_SX,
        p: 1.8,
        minWidth: 0,
        borderColor: token.border,
        background:
          `linear-gradient(155deg, ${token.bg}, rgba(8, 12, 24, 0.92) 54%, rgba(6, 10, 22, 0.95))`,
        boxShadow: "0 14px 34px rgba(2, 6, 23, 0.38)",
      }}
    >
      <Stack spacing={1.4}>
        <Stack direction="row" spacing={1} alignItems="center" justifyContent="space-between">
          <Stack direction="row" spacing={1} alignItems="center" sx={{ minWidth: 0 }}>
            <Icon fontSize="small" sx={{ color: token.color }} />
            <Typography noWrap sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", fontWeight: 750 }}>
              {metric.label}
            </Typography>
          </Stack>
          <Typography sx={{ color: token.color, fontSize: "0.72rem", fontWeight: 800 }}>
            {metric.trend}
          </Typography>
        </Stack>
        <Box>
          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "2rem", lineHeight: 1, fontWeight: 850 }}>
            {metric.value}
          </Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.65, fontSize: "0.82rem" }}>
            {metric.helper}
          </Typography>
        </Box>
      </Stack>
    </Box>
  );
}

function CockpitAttentionCard({ item }) {
  const token = getTone(item.tone);

  return (
    <Box
      sx={{
        p: 1.45,
        borderRadius: 1.2,
        border: `1px solid ${token.border}`,
        background: `linear-gradient(135deg, ${token.bg}, rgba(8, 12, 24, 0.84))`,
        boxShadow: item.tone === "danger" ? "0 0 28px rgba(251, 113, 133, 0.08)" : "none",
      }}
    >
      <Stack direction="row" spacing={1.1} alignItems="flex-start" justifyContent="space-between">
        <Box sx={{ minWidth: 0 }}>
          <Typography noWrap sx={{ color: MAGIC_UI.textBright, fontWeight: 800, fontSize: "0.9rem" }}>
            {item.title}
          </Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.25, fontSize: "0.75rem" }}>
            {item.scope}
          </Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.8, fontSize: "0.82rem", lineHeight: 1.45 }}>
            {item.body}
          </Typography>
        </Box>
        <StatusPill tone={item.tone} label={item.time} />
      </Stack>
    </Box>
  );
}

function CockpitDomainBand({ domain }) {
  const token = getTone(domain.tone);

  return (
    <Box>
      <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 0.7 }}>
        <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem", fontWeight: 750 }}>
          {domain.label}
        </Typography>
        <Typography sx={{ color: token.color, fontSize: "0.78rem", fontWeight: 800 }}>
          {domain.value}%
        </Typography>
      </Stack>
      <Box
        sx={{
          height: 8,
          borderRadius: 999,
          backgroundColor: "rgba(148, 163, 184, 0.12)",
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            width: `${domain.value}%`,
            height: "100%",
            borderRadius: 999,
            backgroundColor: token.color,
            boxShadow: `0 0 16px ${token.color}44`,
          }}
        />
      </Box>
    </Box>
  );
}

function CockpitTimeline() {
  return (
    <Stack spacing={0}>
      {COCKPIT_TIMELINE.map((event, index) => {
        const token = getTone(event.tone);
        return (
          <Box
            key={`${event.time}-${event.label}`}
            sx={{
              display: "grid",
              gridTemplateColumns: "54px 18px 1fr",
              gap: 1,
              minHeight: 48,
            }}
          >
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.76rem", pt: 0.05 }}>
              {event.time}
            </Typography>
            <Box sx={{ position: "relative", display: "flex", justifyContent: "center" }}>
              <Box
                sx={{
                  width: 9,
                  height: 9,
                  borderRadius: "50%",
                  mt: 0.35,
                  backgroundColor: token.color,
                  boxShadow: `0 0 14px ${token.color}`,
                  zIndex: 1,
                }}
              />
              {index < COCKPIT_TIMELINE.length - 1 ? (
                <Box
                  sx={{
                    position: "absolute",
                    top: 14,
                    bottom: -2,
                    width: 1,
                    backgroundColor: "rgba(148, 163, 184, 0.18)",
                  }}
                />
              ) : null}
            </Box>
            <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.83rem", lineHeight: 1.35 }}>
              {event.label}
            </Typography>
          </Box>
        );
      })}
    </Stack>
  );
}

export function CockpitLayoutTemplate() {
  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        gap: 2,
        color: MAGIC_UI.textBright,
      }}
    >
      <Box
        sx={{
          ...PANEL_SX,
          p: { xs: 2, md: 2.5 },
          background:
            "linear-gradient(135deg, rgba(5,10,22,0.98), rgba(12,24,42,0.92) 44%, rgba(17,49,54,0.52))",
        }}
      >
        <Stack
          direction={{ xs: "column", xl: "row" }}
          spacing={2}
          alignItems={{ xs: "stretch", xl: "center" }}
          justifyContent="space-between"
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={SECTION_HEADER_SX}>Cockpit Layout</Typography>
            <Typography variant="h5" sx={{ mt: 0.6, mb: 0.8, fontWeight: 800, letterSpacing: 0 }}>
              Mission control page for operators
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, maxWidth: 900, lineHeight: 1.5 }}>
              Compose page around live posture, urgent work, and launchable actions. Better for home,
              server info, watchdogs, and operations overview pages.
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
            <Button startIcon={<BoltRoundedIcon />} sx={PRIMARY_BUTTON_SX}>
              Run Sweep
            </Button>
            <Button startIcon={<TimelineRoundedIcon />} sx={SECONDARY_BUTTON_SX}>
              Open Timeline
            </Button>
            <Button startIcon={<TuneIcon />} sx={SECONDARY_BUTTON_SX}>
              Customize
            </Button>
          </Stack>
        </Stack>
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: {
            xs: "1fr",
            md: "repeat(2, minmax(0, 1fr))",
            xl: "repeat(4, minmax(0, 1fr))",
          },
          gap: 1.5,
        }}
      >
        {COCKPIT_METRICS.map((metric) => (
          <CockpitMetricCard key={metric.label} metric={metric} />
        ))}
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", xl: "minmax(0, 1.45fr) minmax(340px, 0.55fr)" },
          gap: 2,
          alignItems: "stretch",
        }}
      >
        <Box
          sx={{
            ...PANEL_SX,
            p: 2,
            minWidth: 0,
            background:
              "linear-gradient(145deg, rgba(7,10,24,0.96), rgba(9,18,34,0.9) 58%, rgba(19,36,41,0.58))",
          }}
        >
          <Stack spacing={2}>
            <Stack
              direction={{ xs: "column", lg: "row" }}
              spacing={1.5}
              alignItems={{ xs: "stretch", lg: "center" }}
              justifyContent="space-between"
            >
              <Box>
                <Typography sx={SECTION_HEADER_SX}>Operational Posture</Typography>
                <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, fontSize: "0.86rem" }}>
                  Horizontal health spine replaces scattered cards. One scan shows system pressure.
                </Typography>
              </Box>
              <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                <StatusPill tone="health" label="Healthy Core" />
                <StatusPill tone="warning" label="3 Watch Items" />
                <StatusPill tone="danger" label="1 Critical" />
              </Stack>
            </Stack>

            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", lg: "1.15fr 0.85fr" },
                gap: 2,
                alignItems: "stretch",
              }}
            >
              <Box
                sx={{
                  p: 2,
                  borderRadius: 1.4,
                  border: `1px solid ${MAGIC_UI.panelBorder}`,
                  background: "rgba(5, 10, 24, 0.68)",
                }}
              >
                <Stack spacing={1.55}>
                  {COCKPIT_DOMAINS.map((domain) => (
                    <CockpitDomainBand key={domain.label} domain={domain} />
                  ))}
                </Stack>
              </Box>

              <Box
                sx={{
                  p: 2,
                  borderRadius: 1.4,
                  border: "1px solid rgba(125, 211, 252, 0.2)",
                  background:
                    "radial-gradient(circle at 50% 30%, rgba(125, 211, 252, 0.18), transparent 42%), rgba(5, 10, 24, 0.72)",
                  minHeight: 260,
                  display: "grid",
                  placeItems: "center",
                }}
              >
                <Box
                  sx={{
                    width: "min(300px, 80vw)",
                    aspectRatio: "1",
                    borderRadius: "50%",
                    border: "1px solid rgba(125, 211, 252, 0.22)",
                    display: "grid",
                    placeItems: "center",
                    position: "relative",
                    background:
                      "radial-gradient(circle, rgba(52, 211, 153, 0.16), rgba(125, 211, 252, 0.08) 42%, rgba(8,12,24,0.2) 43%)",
                    boxShadow: "inset 0 0 40px rgba(125, 211, 252, 0.08)",
                    "&:before": {
                      content: '""',
                      position: "absolute",
                      inset: "16%",
                      borderRadius: "50%",
                      border: "1px dashed rgba(148, 163, 184, 0.24)",
                    },
                    "&:after": {
                      content: '""',
                      position: "absolute",
                      inset: "33%",
                      borderRadius: "50%",
                      border: "1px solid rgba(52, 211, 153, 0.34)",
                    },
                  }}
                >
                  <Stack spacing={0.4} alignItems="center">
                    <SpeedRoundedIcon sx={{ color: MAGIC_UI.health, fontSize: 38 }} />
                    <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "2rem", lineHeight: 1, fontWeight: 850 }}>
                      94
                    </Typography>
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.76rem", fontWeight: 750 }}>
                      posture score
                    </Typography>
                  </Stack>
                </Box>
              </Box>
            </Box>
          </Stack>
        </Box>

        <Box sx={{ ...PANEL_SX, p: 2, minWidth: 0 }}>
          <Stack spacing={1.5}>
            <Box>
              <Typography sx={SECTION_HEADER_SX}>Attention Queue</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, fontSize: "0.86rem" }}>
                Actionable work ordered by urgency.
              </Typography>
            </Box>
            {COCKPIT_ATTENTION.map((item) => (
              <CockpitAttentionCard key={item.title} item={item} />
            ))}
          </Stack>
        </Box>
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", lg: "0.85fr 1.15fr" },
          gap: 2,
        }}
      >
        <Box sx={{ ...PANEL_SX, p: 2 }}>
          <Typography sx={SECTION_HEADER_SX}>Event Stream</Typography>
          <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, mb: 1.6, fontSize: "0.86rem" }}>
            Timeline stays visible without stealing main page surface.
          </Typography>
          <CockpitTimeline />
        </Box>

        <Box sx={{ ...PANEL_SX, p: 2 }}>
          <Stack spacing={1.5}>
            <Box>
              <Typography sx={SECTION_HEADER_SX}>Launch Surface</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6, fontSize: "0.86rem" }}>
                Dense command pads replace oversized page cards.
              </Typography>
            </Box>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", sm: "repeat(2, minmax(0, 1fr))", xl: "repeat(4, minmax(0, 1fr))" },
                gap: 1,
              }}
            >
              {[
                ["Diagnostics", TerminalRoundedIcon, "identity"],
                ["Patch Audit", FactCheckRoundedIcon, "warning"],
                ["Security Sweep", SecurityRoundedIcon, "danger"],
                ["Fleet Refresh", CachedIcon, "health"],
              ].map(([label, Icon, tone]) => {
                const token = getTone(tone);
                return (
                  <Button
                    key={label}
                    sx={{
                      ...SECONDARY_BUTTON_SX,
                      justifyContent: "flex-start",
                      height: 54,
                      borderRadius: 1.2,
                      color: MAGIC_UI.textBright,
                      borderColor: token.border,
                      background: `linear-gradient(135deg, ${token.bg}, rgba(5, 10, 24, 0.72))`,
                    }}
                    startIcon={<Icon sx={{ color: token.color }} />}
                  >
                    {label}
                  </Button>
                );
              })}
            </Box>
          </Stack>
        </Box>
      </Box>
    </Box>
  );
}

export default function PageStyleTemplate() {
  const gridRef = useRef(null);
  const [activeFilter, setActiveFilter] = useState("active");
  const getRowId = useCallback((params) => params.data?.id || String(params.rowIndex ?? ""), []);

  const columnDefs = useMemo(
    () => [
      selectionCol,
      { headerName: "ID", field: "id", width: 120, minWidth: 110 },
      {
        headerName: "Name",
        field: "name",
        minWidth: 220,
        flex: 1,
        cellRenderer: (params) => (
          <Box component="span" sx={{ color: MAGIC_UI.accentA, fontWeight: 650 }}>
            {params.value}
          </Box>
        ),
      },
      {
        headerName: "State",
        field: "state",
        minWidth: 160,
        cellRenderer: (params) => <StatusPill tone={params.data?.tone} label={params.value} />,
      },
      { headerName: "Signal", field: "signal", minWidth: 150 },
      { headerName: "Owner", field: "owner", minWidth: 150 },
      { headerName: "Updated", field: "updated", minWidth: 190 },
    ],
    []
  );

  return (
    <Box
      sx={{
        width: "100%",
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        gap: 2,
        color: MAGIC_UI.textBright,
      }}
    >
      <Box
        sx={{
          ...PANEL_SX,
          p: { xs: 2, md: 2.5 },
          background:
            "linear-gradient(135deg, rgba(7,10,24,0.96), rgba(15,23,42,0.86) 46%, rgba(49,22,73,0.5))",
        }}
      >
        <Stack
          direction={{ xs: "column", lg: "row" }}
          spacing={2}
          alignItems={{ xs: "stretch", lg: "center" }}
          justifyContent="space-between"
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography sx={SECTION_HEADER_SX}>Page Style Template</Typography>
            <Typography variant="h5" sx={{ mt: 0.6, mb: 0.8, fontWeight: 750, letterSpacing: 0 }}>
              Semantic color system for Borealis pages
            </Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, maxWidth: 880, lineHeight: 1.5 }}>
              Blue keeps product identity. Green, amber, red, and steel carry operational meaning.
              Routine controls stay quiet so health and risk signals win attention.
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
            <StatusPill tone="health" label="Healthy" />
            <StatusPill tone="warning" label="Needs attention" />
            <StatusPill tone="danger" label="Destructive" />
            <StatusPill tone="neutral" label="Disabled" />
          </Stack>
        </Stack>
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: {
            xs: "1fr",
            md: "repeat(2, minmax(0, 1fr))",
            xl: "repeat(5, minmax(0, 1fr))",
          },
          gap: 1.5,
        }}
      >
        {Object.keys(SEMANTIC_TONES).map((toneKey) => (
          <ToneCard key={toneKey} toneKey={toneKey} />
        ))}
      </Box>

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", xl: "0.9fr 1.1fr" },
          gap: 2,
        }}
      >
        <Box sx={{ ...PANEL_SX, p: 2.25 }}>
          <Stack spacing={1.8}>
            <Box>
              <Typography sx={SECTION_HEADER_SX}>Button Tones</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6 }}>
                Gradient belongs to page-defining action. Routine actions use steel outlines.
              </Typography>
            </Box>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <Button startIcon={<SaveRoundedIcon />} sx={PRIMARY_BUTTON_SX}>
                Save Changes
              </Button>
              <Button startIcon={<CachedIcon />} sx={SECONDARY_BUTTON_SX}>
                Refresh
              </Button>
              <Button startIcon={<TuneIcon />} sx={SECONDARY_BUTTON_SX}>
                Columns
              </Button>
              <Button startIcon={<WarningAmberRoundedIcon />} sx={WARNING_BUTTON_SX}>
                Enable Dev Mode
              </Button>
              <Button startIcon={<DeleteOutlineIcon />} sx={DANGER_BUTTON_SX}>
                Delete
              </Button>
              <Button startIcon={<AddIcon />} disabled sx={SECONDARY_BUTTON_SX}>
                Disabled
              </Button>
            </Stack>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", sm: "repeat(2, minmax(0, 1fr))" },
                gap: 1.2,
              }}
            >
              <DesignNote
                tone="identity"
                title="Blue answers interaction"
                body="Use it for selected, focused, linked, or primary safe action states."
              />
              <DesignNote
                tone="neutral"
                title="Steel answers background"
                body="Use it for passive controls, disabled states, metadata, borders, and inactive rows."
              />
            </Box>
          </Stack>
        </Box>

        <Box sx={{ ...PANEL_SX, p: 2.25 }}>
          <Stack spacing={1.6}>
            <Box>
              <Typography sx={SECTION_HEADER_SX}>Status Chips</Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, mt: 0.6 }}>
                Same shape, different meaning. Avoid new colors unless status meaning changes.
              </Typography>
            </Box>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <StatusPill tone="health" label="Online" />
              <StatusPill tone="health" label="Success" />
              <StatusPill tone="warning" label="Stale" />
              <StatusPill tone="warning" label="Read-only" />
              <StatusPill tone="danger" label="Failed" />
              <StatusPill tone="danger" label="Critical" />
              <StatusPill tone="neutral" label="Unknown" />
              <StatusPill tone="neutral" label="Inactive" />
            </Stack>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              <Chip
                label="Clickable entity"
                size="small"
                sx={{
                  color: MAGIC_UI.accentA,
                  borderColor: "rgba(125, 211, 252, 0.38)",
                  backgroundColor: "rgba(125, 211, 252, 0.08)",
                  fontWeight: 700,
                }}
                variant="outlined"
              />
              <Chip
                label="Metadata"
                size="small"
                sx={{
                  color: MAGIC_UI.textMuted,
                  borderColor: "rgba(148, 163, 184, 0.26)",
                  backgroundColor: "rgba(148, 163, 184, 0.06)",
                }}
                variant="outlined"
              />
            </Stack>
            <DesignNote
              tone="warning"
              title="Warning gets attention before danger"
              body="Use amber for inspection and intervention. Reserve red for loss, failure, or irreversible action."
            />
          </Stack>
        </Box>
      </Box>

      <PatternLibrary activeFilter={activeFilter} onFilterSelect={setActiveFilter} />

      <Box sx={{ ...PANEL_SX, overflow: "hidden" }}>
        <Stack
          direction={{ xs: "column", md: "row" }}
          alignItems={{ xs: "stretch", md: "center" }}
          justifyContent="space-between"
          spacing={1.5}
          sx={{
            px: 2,
            py: 1.5,
            borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
            backgroundColor: "rgba(15, 23, 42, 0.32)",
          }}
        >
          <Box>
            <Typography sx={SECTION_HEADER_SX}>Quartz Grid Example</Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.86rem", mt: 0.35 }}>
              Selection uses Borealis blue. Row status carries semantic color.
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
            <Button startIcon={<CachedIcon />} sx={SECONDARY_BUTTON_SX}>
              Refresh
            </Button>
            <Button startIcon={<AddIcon />} sx={PRIMARY_BUTTON_SX}>
              New Item
            </Button>
          </Stack>
        </Stack>
        <Box
          className={gridThemeClass}
          sx={{
            height: 360,
            width: "100%",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "--ag-background-color": "#070b1a",
            "--ag-foreground-color": "#f4f7ff",
            "--ag-header-background-color": "#0f172a",
            "--ag-header-foreground-color": "#cfe0ff",
            "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
            "--ag-row-hover-color": "rgba(73,156,196,0.2)",
            "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
            "--ag-border-color": "rgba(125,183,255,0.18)",
            "--ag-row-border-color": "rgba(125,183,255,0.14)",
            "--ag-border-radius": 0,
            "--ag-checkbox-border-radius": "3px",
            "& .ag-root-wrapper": {
              border: "none",
              background: "transparent",
            },
            "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
              padding: "8px 12px 8px 18px",
            },
            "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
              paddingLeft: "12px",
              paddingRight: "9px",
            },
            "& .ag-cell.ag-selection-centered, & .ag-cell.ag-selection-centered .ag-cell-wrapper": {
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              paddingLeft: 0,
              paddingRight: 0,
            },
            "& .ag-row-selected": {
              backgroundColor: "rgba(125,211,252,0.2) !important",
              boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
            },
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={SAMPLE_ROWS}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowSelection="multiple"
            rowMultiSelectWithClick
            getRowId={getRowId}
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            animateRows
            theme={gridTheme}
            rowHeight={44}
            suppressCellFocus
          />
        </Box>
      </Box>
    </Box>
  );
}
