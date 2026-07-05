import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
  Menu,
  MenuItem,
  Stack,
  Switch,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import AddRoundedIcon from "@mui/icons-material/AddRounded";
import DeleteRoundedIcon from "@mui/icons-material/DeleteRounded";
import DevicesRoundedIcon from "@mui/icons-material/DevicesRounded";
import FilterAltIcon from "@mui/icons-material/FilterAlt";
import LocationCityIcon from "@mui/icons-material/LocationCity";
import PlayArrowRoundedIcon from "@mui/icons-material/PlayArrowRounded";
import PlayCircleOutlineRoundedIcon from "@mui/icons-material/PlayCircleOutlineRounded";
import PublicRoundedIcon from "@mui/icons-material/PublicRounded";
import SaveRoundedIcon from "@mui/icons-material/SaveRounded";
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
import WarningAmberRoundedIcon from "@mui/icons-material/WarningAmberRounded";
import { AgGridReact } from "ag-grid-react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { BOREALIS_BLUE, CountSliderGroup, buildNavTabsSx } from "../Automation/Watchdogs/shared.jsx";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "../Devices/Tabs/Shared.jsx";

const PAGE_TITLE = "Windows Patch Management";
const PAGE_SUBTITLE =
  "Ad-hoc installation of Windows Updates and patch policies. Policies are applied on a hierarchal granular level, where the deepest nested policies apply last.";

const STATE_FILTER_OPTIONS = [
  { key: "pending", label: "Pending" },
  { key: "installed", label: "Installed" },
];

const SEVERITY_FILTER_OPTIONS = [
  { key: "critical", label: "Critical" },
  { key: "important", label: "Important" },
  { key: "moderate", label: "Moderate" },
  { key: "low", label: "Low" },
  { key: "unspecified", label: "Unspecified" },
];

const OS_PAGE_ICON_CLASSES = Object.freeze({
  windows: "fa-brands fa-windows",
  linux: "fa-brands fa-linux",
  macos: "fa-brands fa-apple",
});

function PatchOSPageIcon({ os = "windows", sx, className = "", ...props }) {
  return (
    <Box
      component="i"
      aria-hidden="true"
      className={`${OS_PAGE_ICON_CLASSES[os] || OS_PAGE_ICON_CLASSES.windows} ${className}`.trim()}
      sx={{
        width: 22,
        lineHeight: 1,
        textAlign: "center",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        ...sx,
      }}
      {...props}
    />
  );
}

export function WindowsPatchPageIcon(props) {
  return <PatchOSPageIcon os="windows" {...props} />;
}

export function LinuxPatchPageIcon(props) {
  return <PatchOSPageIcon os="linux" {...props} />;
}

export function MacOSPatchPageIcon(props) {
  return <PatchOSPageIcon os="macos" {...props} />;
}

export const PATCH_PAGE_TABS = [
  { key: "patch_list", label: "Patch List" },
  { key: "policies", label: "Patch Management Policies" },
];

export const POLICY_MATCH_TYPES = [
  { value: "severity", label: "Severity" },
  { value: "classification", label: "Classification" },
  { value: "category", label: "Category" },
  { value: "kb", label: "KB" },
  { value: "update_id", label: "Update ID" },
  { value: "patch_key", label: "Patch Key" },
  { value: "title_contains", label: "Title Contains" },
];

export const POLICY_RULE_TYPES = [
  { value: "approve", label: "Approve" },
  { value: "block", label: "Block" },
];

export const POLICY_ROLE_SCOPES = [
  { value: "Server", label: "Server" },
  { value: "Workstation", label: "Workstation" },
];

export const POLICY_SCHEDULE_TYPES = [
  { value: "weekly", label: "Weekly" },
  { value: "daily", label: "Daily" },
  { value: "once", label: "Once" },
  { value: "immediately", label: "Immediate" },
];

const POLICY_EDITOR_TABS = [
  { key: "details", label: "Details" },
  { key: "schedule", label: "Schedule" },
  { key: "rules", label: "Allow / Block" },
  { key: "exclusions", label: "Exclusions" },
];

export const POLICY_EXCLUSION_TYPES = [
  { value: "unmanaged", label: "Unmanaged" },
  { value: "frozen", label: "Frozen" },
  { value: "managed_override", label: "Managed Override" },
];

const FILTER_LABEL_SX = {
  color: BOREALIS_BLUE,
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

const PATCH_CHIP_TOKEN_SX = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  height: 20,
  minHeight: 20,
  maxWidth: "100%",
  borderRadius: 999,
  boxSizing: "border-box",
  whiteSpace: "nowrap",
  verticalAlign: "middle",
  fontSize: 11.5,
  fontWeight: 700,
  lineHeight: 1,
  px: 0.85,
  py: 0,
  minWidth: 0,
  overflow: "hidden",
  "& .patch-chip-label": {
    display: "block",
    minWidth: 0,
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    lineHeight: 1,
  },
};

export const PATCH_GRID_SX = {
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
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    height: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    paddingTop: 0,
    paddingBottom: 0,
    minWidth: 0,
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
    width: "100%",
    height: "100%",
    display: "flex",
    alignItems: "center",
    minWidth: 0,
  },
  "& .patch-chip-cell .ag-cell-wrapper, & .patch-chip-cell .ag-cell-value": {
    alignItems: "center",
  },
};

export function text(value) {
  return String(value ?? "").trim();
}

export function valueArray(value) {
  if (Array.isArray(value)) return value;
  const raw = text(value);
  return raw ? raw.split(",") : [];
}

function normalizeSeverity(value) {
  const normalized = text(value).toLowerCase();
  if (["critical", "important", "moderate", "low"].includes(normalized)) {
    return normalized;
  }
  return "unspecified";
}

function formatState(value) {
  const normalized = text(value).toLowerCase();
  if (normalized === "pending") return "Pending";
  if (normalized === "installed") return "Installed";
  return text(value) || "Unknown";
}

function formatSource(value) {
  const normalized = text(value).toLowerCase();
  if (normalized === "wua_pending") return "Windows Update";
  if (normalized === "wua_history") return "WUA History";
  if (normalized === "quick_fix_engineering") return "Get-HotFix";
  return text(value) || "Unknown";
}

export function formatTimestamp(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  return new Date(numeric * 1000).toLocaleString();
}

export function datetimeLocalFromUnix(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  const date = new Date(numeric * 1000);
  const offset = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

export function unixFromDatetimeLocal(value) {
  const raw = text(value);
  if (!raw) return null;
  const numeric = Math.floor(new Date(raw).getTime() / 1000);
  return Number.isFinite(numeric) && numeric > 0 ? numeric : null;
}

function formatDeviceCount(value) {
  const count = Number(value || 0);
  const safeCount = Number.isFinite(count) && count > 0 ? count : 0;
  return `${safeCount.toLocaleString()} ${safeCount === 1 ? "Device" : "Devices"}`;
}

export function formatScheduleType(value) {
  const raw = text(value);
  if (!raw) return "";
  return raw
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((segment) => `${segment.charAt(0).toUpperCase()}${segment.slice(1).toLowerCase()}`)
    .join(" ");
}

const WEEKDAY_PLURAL_LABELS = Object.freeze([
  "Sundays",
  "Mondays",
  "Tuesdays",
  "Wednesdays",
  "Thursdays",
  "Fridays",
  "Saturdays",
]);

function formatClockTime(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  return new Date(numeric * 1000)
    .toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
    .replace(/\s+/g, "");
}

function formatDateTimeShort(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  const date = new Date(numeric * 1000);
  const time = formatClockTime(value);
  return time ? `${date.toLocaleDateString()} @ ${time}` : date.toLocaleDateString();
}

function policyScheduleParts(policy = {}) {
  const scheduleType = text(policy?.install_schedule_type).toLowerCase() || "weekly";
  const label = formatScheduleType(scheduleType) || "Weekly";
  const startTS = Number(policy?.install_start_ts || 0);
  if (!Number.isFinite(startTS) || startTS <= 0) {
    return { label, detail: "Not Scheduled" };
  }
  const date = new Date(startTS * 1000);
  const time = formatClockTime(startTS);
  if (scheduleType === "weekly") {
    return { label, detail: `${WEEKDAY_PLURAL_LABELS[date.getDay()] || "Weekly"} @ ${time}` };
  }
  if (scheduleType === "daily") {
    return { label, detail: `Every day @ ${time}` };
  }
  if (scheduleType === "once") {
    return { label, detail: formatDateTimeShort(startTS) };
  }
  if (scheduleType === "immediately") {
    return { label, detail: "When invoked" };
  }
  return { label, detail: time || "Not Scheduled" };
}

function policyScheduleText(policy = {}) {
  const { label, detail } = policyScheduleParts(policy);
  return detail ? `${label}: ${detail}` : label;
}

function PolicyScheduleCell({ policy = {} }) {
  const { label, detail } = policyScheduleParts(policy);
  return (
    <Box sx={{ display: "inline-flex", alignItems: "baseline", gap: 0.55, minWidth: 0, maxWidth: "100%", overflow: "hidden" }}>
      <Typography component="span" noWrap sx={{ color: MAGIC_UI.textBright, fontSize: 12.5, fontWeight: 700, flexShrink: 0 }}>
        {`${label}:`}
      </Typography>
      <Typography component="span" noWrap sx={{ color: "rgba(148,163,184,0.78)", fontSize: 12, fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis" }}>
        {detail || "Not Scheduled"}
      </Typography>
    </Box>
  );
}

function PolicyBooleanCell({ value = false }) {
  return (
    <Box sx={{ width: "100%", height: "100%", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <Checkbox
        checked={Boolean(value)}
        disabled
        size="small"
        sx={{
          p: 0,
          color: "rgba(148,163,184,0.62)",
          "&.Mui-checked": { color: "rgba(148,163,184,0.68)" },
          "&.Mui-disabled": { color: "rgba(148,163,184,0.48)" },
        }}
      />
    </Box>
  );
}

export function policyTypeLabel(policyType) {
  if (policyType === "global") return "Global Policy";
  return policyType === "device_filter" ? "Device Filter Policy" : "Site Policy";
}

function policyTableTypeLabel(policyType) {
  const normalized = text(policyType).toLowerCase();
  if (normalized === "global") return "Global";
  if (normalized === "device_filter") return "Device Filter Policy";
  return "Site-Level Override";
}

const POLICY_PENDING_LAYERS = Object.freeze([
  { policy_type: "global", label: "Global" },
  { policy_type: "site", label: "Site" },
  { policy_type: "device_filter", label: "Filter" },
]);

const POLICY_TREE_LEVEL_WIDTH = 32;
const POLICY_TREE_ANCHOR_X = 10;

function pendingLayerTypesForPolicy(policy = {}) {
  const policyType = normalizePolicyTypeValue(policy);
  const maxIndex = Math.max(0, policyLayerSortIndex(policyType));
  return POLICY_PENDING_LAYERS.slice(0, Math.min(maxIndex + 1, POLICY_PENDING_LAYERS.length));
}

function pendingLayerLabel(policyType) {
  const normalized = normalizePolicyTypeValue({ policy_type: policyType });
  return POLICY_PENDING_LAYERS.find((layer) => layer.policy_type === normalized)?.label || policyTableTypeLabel(policyType);
}

function policyIconForType(policyType) {
  const normalized = text(policyType).toLowerCase();
  if (normalized === "global") return PublicRoundedIcon;
  if (normalized === "device_filter") return FilterAltIcon;
  return LocationCityIcon;
}

function policyRowTypeLabel(row = {}) {
  const source = row && typeof row === "object" ? row : {};
  return policyTableTypeLabel(text(source.policy_type) || "site");
}

function microsoftUpdateCatalogURL(kb) {
  const normalized = text(kb).toUpperCase();
  if (!/^KB\d{4,9}$/.test(normalized)) return "";
  return `https://www.catalog.update.microsoft.com/Search.aspx?q=${encodeURIComponent(normalized)}`;
}

function normalizedPatchKey(row = {}) {
  return (
    text(row.patch_key) ||
    (text(row.kb) ? `kb:${text(row.kb).toUpperCase()}:state:${text(row.state).toLowerCase()}` : "") ||
    `${text(row.title).toLowerCase()}|${text(row.state).toLowerCase()}`
  );
}

function patchSelectionIdentity(row = {}) {
  return (
    (text(row.kb) ? `kb:${text(row.kb).toUpperCase()}` : "") ||
    (text(row.patch_key) ? `patch:${text(row.patch_key).toLowerCase()}` : "") ||
    (text(row.title) ? `title:${text(row.title).toLowerCase()}` : "")
  );
}

function canSelectPatchForInstall(row = {}) {
  return text(row.state).toLowerCase() === "pending" && Boolean(text(row.patch_key)) && !row.active_install_job;
}

function patchDraftJobName(prefix, patchLabel, title, count) {
  return `[${prefix}] - ${patchLabel} - ${title} - ${count.toLocaleString()} ${count === 1 ? "Device" : "Devices"}`;
}

function fleetPatchDraftItem(row = {}, { triggerLabel = "Ad-Hoc Install" } = {}) {
  const patchKey = text(row.patch_key);
  if (!patchKey || text(row.state).toLowerCase() !== "pending" || row.active_install_job) return null;
  const childRows = Array.isArray(row.childRows) ? row.childRows : [];
  const targetsByHost = new Map();
  childRows.forEach((item) => {
    const hostname = text(item.hostname);
    if (!hostname || targetsByHost.has(hostname.toLowerCase())) return;
    targetsByHost.set(hostname.toLowerCase(), {
      kind: "device",
      hostname,
      device_guid: text(item.device_guid),
      site_id: item.site_id ?? null,
      site_name: text(item.site_name),
      operating_system: text(item.operating_system),
    });
  });
  const targets = Array.from(targetsByHost.values());
  if (!targets.length) return null;
  const first = childRows[0] || {};
  const patch = {
    patch_key: patchKey,
    kb: text(row.kb),
    title: text(row.title),
    state: text(row.state) || "pending",
    source: text(row.source),
    classification: text(row.classification),
    severity: text(row.severity),
    metadata: first.metadata || {},
  };
  const patchLabel = text(row.kb) || text(row.title) || "Patch";
  const title = text(row.title) || patchLabel;
  const count = targets.length;
  return {
    id: `${patchKey}-${patchSelectionIdentity(row) || text(row.id)}`,
    source: "fleet",
    trigger: triggerLabel === "Bulk Ad-Hoc Install" ? "bulk_ad_hoc" : "ad_hoc",
    trigger_label: triggerLabel,
    patch,
    targets,
    target_count: count,
    job_name:
      triggerLabel === "Bulk Ad-Hoc Install"
        ? patchDraftJobName(triggerLabel, patchLabel, title, count)
        : `[${triggerLabel}] ${patchLabel} - ${title} - ${count.toLocaleString()} ${count === 1 ? "Device" : "Devices"}`,
  };
}

function FilterSliderBlock({ label = "", children }) {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
      <Typography component="span" sx={FILTER_LABEL_SX}>
        {label}
      </Typography>
      {children}
    </Box>
  );
}

function buildPatchFleetRows(rows = []) {
  const groups = new Map();
  rows.forEach((row) => {
    const key = `${normalizedPatchKey(row)}|${text(row.state).toLowerCase()}`;
    if (!groups.has(key)) {
      groups.set(key, {
        id: key,
        patch_key: text(row.patch_key),
        kb: text(row.kb),
        title: text(row.title),
        state: text(row.state).toLowerCase(),
        severity: text(row.severity),
        classification: text(row.classification),
        source: text(row.source),
        captured_at: Number(row.captured_at || 0) || 0,
        published_at: Number(row.published_at || 0) || 0,
        installed_on: Number(row.installed_on || 0) || 0,
        device_count: 0,
        hostnames: [],
        site_names: [],
        childRows: [],
        active_install_job: row.active_install_job || null,
      });
    }
    const current = groups.get(key);
    current.childRows.push(row);
    if (!current.active_install_job && row.active_install_job) current.active_install_job = row.active_install_job;
    current.device_count += 1;
    if (Number(row.captured_at || 0) > current.captured_at) current.captured_at = Number(row.captured_at || 0);
    if (!current.severity && text(row.severity)) current.severity = text(row.severity);
    if (!current.classification && text(row.classification)) current.classification = text(row.classification);
    if (!current.source && text(row.source)) current.source = text(row.source);
    const hostname = text(row.hostname);
    if (hostname && !current.hostnames.includes(hostname)) current.hostnames.push(hostname);
    const siteName = text(row.site_name);
    if (siteName && !current.site_names.includes(siteName)) current.site_names.push(siteName);
  });
  return [...groups.values()].sort((left, right) => {
    if (left.state !== right.state) return left.state === "pending" ? -1 : 1;
    return text(left.kb || left.title).localeCompare(text(right.kb || right.title), undefined, {
      numeric: true,
      sensitivity: "base",
    });
  });
}

function ownPendingUpdateBreakdown(policy = {}) {
  const raw = Array.isArray(policy?.pending_update_breakdown) ? policy.pending_update_breakdown : [];
  const byType = new Map();
  raw.forEach((item) => {
    const policyType = normalizePolicyTypeValue({ policy_type: item?.policy_type });
    if (!policyType) return;
    byType.set(policyType, item);
  });
  return pendingLayerTypesForPolicy(policy).map((layer) => {
    const item = byType.get(layer.policy_type) || {};
    return {
      policy_type: layer.policy_type,
      label: text(item?.label) || layer.label,
      count: Number(item?.count || 0) || 0,
      device_count: Number(item?.device_count || item?.deviceCount || 0) || 0,
      source_policy_id: Number(policy?.id || 0) || 0,
      source_policy_name: text(policy?.name),
    };
  });
}

function pendingUpdateBreakdown(policy = {}) {
  const source = Array.isArray(policy?.__pendingUpdateBreakdown)
    ? policy.__pendingUpdateBreakdown
    : ownPendingUpdateBreakdown(policy);
  return source
    .map((item) => ({
      policy_type: normalizePolicyTypeValue({ policy_type: item?.policy_type }),
      label: text(item?.label) || policyTableTypeLabel(item?.policy_type),
      count: Number(item?.count || 0) || 0,
      device_count: Number(item?.device_count || item?.deviceCount || 0) || 0,
      source_policy_id: Number(item?.source_policy_id || item?.policy_id || policy?.id || 0) || 0,
      source_policy_name: text(item?.source_policy_name || item?.policy_name) || text(policy?.name),
    }))
    .filter((item) => item.policy_type && item.count > 0)
    .map((item) => ({ ...item, label: pendingLayerLabel(item.policy_type) }))
    .sort((left, right) => policyLayerSortIndex(left.policy_type) - policyLayerSortIndex(right.policy_type));
}

function policyLayerSortIndex(policyType) {
  const normalized = text(policyType).toLowerCase();
  if (normalized === "global") return 0;
  if (normalized === "site") return 1;
  if (normalized === "device_filter") return 2;
  return 10;
}

function pendingUpdateBreakdownText(policy = {}) {
  const breakdown = pendingUpdateBreakdown(policy);
  if (!breakdown.length) return "";
  return breakdown
    .map((item) => `${item.label}: ${item.count.toLocaleString()} ${Number(item.count || 0) === 1 ? "Update" : "Updates"} (${Number(item.device_count || 0).toLocaleString()} ${Number(item.device_count || 0) === 1 ? "Device" : "Devices"})`)
    .join(" / ");
}

function PendingUpdatesCell({ policy = {}, onSelect }) {
  const breakdown = pendingUpdateBreakdown(policy);
  if (!breakdown.length) {
    return null;
  }
  return (
    <Box
      sx={{
        display: "flex",
        alignItems: "center",
        gap: 0.6,
        minWidth: 0,
        width: "100%",
        height: "100%",
        overflow: "hidden",
      }}
    >
      {breakdown.map((item, index) => (
        <React.Fragment key={item.policy_type}>
          {index > 0 ? (
            <Typography component="span" sx={{ color: "rgba(100,116,139,0.72)", fontSize: 12, fontWeight: 700, flexShrink: 0 }}>
              /
            </Typography>
          ) : null}
          <Tooltip
            title={`Show ${item.count.toLocaleString()} pending ${item.label} update${item.count === 1 ? "" : "s"} spanning ${Number(item.device_count || 0).toLocaleString()} ${Number(item.device_count || 0) === 1 ? "device" : "devices"} in Patch List`}
          >
            <Typography
              component="button"
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onSelect?.(policy, item);
              }}
              sx={{
                p: 0,
                border: "none",
                background: "transparent",
                display: "inline-flex",
                alignItems: "baseline",
                gap: 0.42,
                color: "inherit",
                cursor: "pointer",
                font: "inherit",
                fontSize: 12.5,
                fontWeight: 700,
                lineHeight: 1.2,
                whiteSpace: "nowrap",
                minWidth: 0,
                flexShrink: 0,
                textDecoration: "none",
                "&:hover": {
                  textDecoration: "none",
                  "& .patch-pending-count": {
                    color: "#93c5fd",
                  },
                },
              }}
            >
              <Box component="span" sx={{ color: MAGIC_UI.textBright, flexShrink: 0 }}>
                {item.label}:
              </Box>
              <Box component="span" className="patch-pending-count" sx={{ color: BOREALIS_BLUE, fontWeight: 800, flexShrink: 0 }}>
                {item.count.toLocaleString()}
              </Box>
              <Box component="span" sx={{ color: MAGIC_UI.textBright, flexShrink: 0 }}>
                {Number(item.count || 0) === 1 ? "Update" : "Updates"}
              </Box>
              <Box component="span" sx={{ color: "rgba(148,163,184,0.72)", fontSize: 11, fontWeight: 600, flexShrink: 0 }}>
                {`(${Number(item.device_count || 0).toLocaleString()} ${Number(item.device_count || 0) === 1 ? "Device" : "Devices"})`}
              </Box>
            </Typography>
          </Tooltip>
        </React.Fragment>
      ))}
    </Box>
  );
}

function patchPolicyHierarchyIDs(row = {}) {
  const source = row && typeof row === "object" ? row : {};
  const ids = Array.isArray(source.patch_policy_hierarchy_policy_ids) ? source.patch_policy_hierarchy_policy_ids : [];
  return ids.map((id) => Number(id || 0)).filter((id) => Number.isFinite(id) && id > 0);
}

function patchRowMatchesPolicyPendingFilter(row = {}, filter = {}) {
  if (!filter?.active) return true;
  if (text(row.state).toLowerCase() !== "pending") return false;
  if (!row.patch_policy_install_candidate) return false;
  const sourcePolicyType = row.patch_policy_source_policy_type || row.patch_policy_effective_policy_type;
  if (filter.layer && normalizePolicyTypeValue({ policy_type: sourcePolicyType }) !== filter.layer) return false;
  if (filter.scopeID > 0 && !patchPolicyHierarchyIDs(row).includes(filter.scopeID)) return false;
  return true;
}

function patchPolicySourceGroups(row = {}) {
  const children = Array.isArray(row?.childRows) ? row.childRows : [row];
  const groups = new Map();
  children.forEach((child) => {
    if (!child?.patch_policy_install_candidate) return;
    const policyType = normalizePolicyTypeValue({
      policy_type: child.patch_policy_source_policy_type || child.patch_policy_effective_policy_type,
    });
    const policyID = Number(child.patch_policy_source_policy_id || child.patch_policy_effective_policy_id || 0) || 0;
    const policyName = text(child.patch_policy_source_policy_name || child.patch_policy_effective_policy_name) || policyTableTypeLabel(policyType);
    const key = `${policyType}:${policyID || policyName}`;
    const current = groups.get(key) || {
      policy_type: policyType,
      label: policyTableTypeLabel(policyType),
      policy_name: policyName,
      count: 0,
    };
    current.count += 1;
    groups.set(key, current);
  });
  return Array.from(groups.values()).sort((left, right) => {
    const layerDelta = policyLayerSortIndex(left.policy_type) - policyLayerSortIndex(right.policy_type);
    if (layerDelta !== 0) return layerDelta;
    return text(left.policy_name).localeCompare(text(right.policy_name));
  });
}

function patchPolicySourceText(row = {}) {
  const groups = patchPolicySourceGroups(row);
  if (!groups.length) return text(row.state).toLowerCase() === "pending" ? "No linked policy" : "";
  return groups.map((group) => `${group.label}: ${group.policy_name} (${group.count})`).join(", ");
}

function PatchPolicySourceCell({ row = {} }) {
  const groups = patchPolicySourceGroups(row);
  if (!groups.length) {
    return (
      <Typography component="span" sx={{ color: MAGIC_UI.textMuted, fontSize: 12.5 }}>
        {text(row.state).toLowerCase() === "pending" ? "No linked policy" : ""}
      </Typography>
    );
  }
  const tooltip = groups.map((group) => `${group.label}: ${group.policy_name} (${group.count.toLocaleString()} update${group.count === 1 ? "" : "s"})`).join(", ");
  return (
    <Tooltip title={tooltip}>
      <Box sx={{ display: "flex", alignItems: "center", gap: 0.55, overflow: "hidden", minWidth: 0, width: "100%", height: "100%" }}>
        {groups.slice(0, 3).map((group) => (
          <Box
            key={`${group.policy_type}-${group.policy_name}`}
            component="span"
            sx={{
              ...PATCH_CHIP_TOKEN_SX,
              maxWidth: 145,
              color: BOREALIS_BLUE,
              background: "rgba(88,166,255,0.12)",
              border: "1px solid rgba(88,166,255,0.28)",
              fontWeight: 800,
            }}
          >
            <Box component="span" className="patch-chip-label">
              {`${group.label} ${group.count.toLocaleString()}`}
            </Box>
          </Box>
        ))}
        {groups.length > 3 ? (
          <Typography component="span" sx={{ color: MAGIC_UI.textMuted, fontSize: 12, fontWeight: 700, lineHeight: 1 }}>
            +{groups.length - 3}
          </Typography>
        ) : null}
      </Box>
    </Tooltip>
  );
}

function normalizePolicyTypeValue(policy = {}) {
  const source = policy && typeof policy === "object" ? policy : {};
  const normalized = text(source.policy_type).toLowerCase();
  if (normalized === "global" || normalized === "device_filter") return normalized;
  return "site";
}

function normalizePolicyRoleValue(policy = {}) {
  const source = policy && typeof policy === "object" ? policy : {};
  const role = text(source.role_scope);
  return role.toLowerCase() === "server" ? "Server" : "Workstation";
}

function normalizeTargetSites(policy = {}) {
  const sourcePolicy = policy && typeof policy === "object" ? policy : {};
  const policyType = normalizePolicyTypeValue(sourcePolicy);
  const source =
    Array.isArray(sourcePolicy.target_sites) && sourcePolicy.target_sites.length
      ? sourcePolicy.target_sites
      : policyType === "site" && Array.isArray(sourcePolicy.sites)
        ? sourcePolicy.sites
        : policyType === "global"
          ? [{ id: 0, site_id: 0, name: "All Sites", scope: "all" }]
          : [];
  const seen = new Map();
  source.forEach((site) => {
    const siteID = Number(site?.site_id ?? site?.id ?? 0) || 0;
    const name = text(site?.name) || (siteID > 0 ? `Site ${siteID}` : "");
    if (!name && siteID <= 0) return;
    const key = siteID > 0 ? `id:${siteID}` : `name:${name.toLowerCase()}`;
    if (seen.has(key)) return;
    seen.set(key, { id: siteID, site_id: siteID, name, scope: text(site?.scope) });
  });
  return Array.from(seen.values()).sort((left, right) =>
    text(left.name).localeCompare(text(right.name), undefined, { sensitivity: "base", numeric: true })
  );
}

function policySiteKey(site = {}) {
  const siteID = Number(site?.site_id ?? site?.id ?? 0) || 0;
  if (siteID > 0) return `id:${siteID}`;
  const name = text(site?.name).toLowerCase();
  return name ? `name:${name}` : "";
}

function policyTargetSiteText(policy = {}) {
  return normalizeTargetSites(policy).map((site) => text(site.name)).filter(Boolean).join(", ");
}

function policyEditPath(policy = {}) {
  const source = policy && typeof policy === "object" ? policy : {};
  if (!source.id) return "#";
  const policyType = normalizePolicyTypeValue(source);
  if (policyType === "global") return APP_PATHS.patchPolicyGlobal(source.id);
  if (policyType === "device_filter") return APP_PATHS.patchPolicyDeviceFilter(source.id);
  return APP_PATHS.patchPolicySite(source.id);
}

function policySortLabel(policy = {}) {
  return `${normalizePolicyRoleValue(policy)}|${policyTargetSiteText(policy)}|${text(policy.name)}`.toLowerCase();
}

function policyRowDepth(policy = {}) {
  const depth = Number(policy.__depth ?? 0);
  return Number.isFinite(depth) && depth > 0 ? depth : 0;
}

function policyTreeLevelContinues(rows = [], rowIndex = 0, level = 1) {
  for (let index = rowIndex + 1; index < rows.length; index += 1) {
    const nextDepth = policyRowDepth(rows[index]);
    if (nextDepth < level) return false;
    if (nextDepth >= level) return true;
  }
  return false;
}

function annotatePolicyTreeRows(rows = []) {
  return rows.map((row, index) => {
    const depth = policyRowDepth(row);
    const nextDepth = policyRowDepth(rows[index + 1] || {});
    const continues = {};
    for (let level = 1; level <= depth; level += 1) {
      continues[level] = policyTreeLevelContinues(rows, index, level);
    }
    return {
      ...row,
      __treeHasChild: nextDepth > depth,
      __treeContinues: continues,
    };
  });
}

function makePolicyHierarchyRow(policy = {}, depth = 0, parentKey = "", branchSite = null) {
  const sourcePolicy = policy && typeof policy === "object" ? policy : {};
  const targetSites = normalizeTargetSites(sourcePolicy);
  const branchSiteName = text(branchSite?.name);
  return {
    ...sourcePolicy,
    __hierarchyKey: `${normalizePolicyTypeValue(sourcePolicy)}:${sourcePolicy.id || text(sourcePolicy.name)}:${parentKey || "root"}:${branchSiteName || "all"}`,
    __depth: depth,
    __parentKey: parentKey,
    __branchSite: branchSite,
    __isReference: normalizePolicyTypeValue(sourcePolicy) === "device_filter" && Boolean(parentKey),
    __policyTypeLabel: policyTableTypeLabel(normalizePolicyTypeValue(sourcePolicy)),
    __targetSites: targetSites,
    __targetSitesText: targetSites.map((site) => text(site.name)).filter(Boolean).join(", "),
    __pendingUpdateBreakdown: pendingUpdateBreakdown(sourcePolicy),
  };
}

function buildPatchPolicyHierarchyRows(policies = []) {
  const normalized = (Array.isArray(policies) ? policies : [])
    .filter((policy) => policy && typeof policy === "object")
    .map((policy) => ({
      ...policy,
      policy_type: normalizePolicyTypeValue(policy),
      role_scope: normalizePolicyRoleValue(policy),
    }));
  const globalPolicies = normalized
    .filter((policy) => policy.policy_type === "global")
    .sort((left, right) => policySortLabel(left).localeCompare(policySortLabel(right)));
  const sitePolicies = normalized
    .filter((policy) => policy.policy_type === "site")
    .sort((left, right) => policySortLabel(left).localeCompare(policySortLabel(right)));
  const devicePolicies = normalized
    .filter((policy) => policy.policy_type === "device_filter")
    .sort((left, right) => policySortLabel(left).localeCompare(policySortLabel(right)));
  const roles = Array.from(
    new Set([
      ...globalPolicies.map((policy) => policy.role_scope),
      ...sitePolicies.map((policy) => policy.role_scope),
      ...devicePolicies.map((policy) => policy.role_scope),
    ])
  ).sort((left, right) => left.localeCompare(right));
  const rows = [];
  roles.forEach((role) => {
    const roleGlobals = globalPolicies.filter((policy) => policy.role_scope === role);
    const roots = roleGlobals.length ? roleGlobals : [{ id: `missing-${role}`, name: `${role} Global Policy`, policy_type: "global", role_scope: role, locked: true }];
    roots.forEach((globalPolicy) => {
      const globalKey = `global:${globalPolicy.id || role}`;
      rows.push(makePolicyHierarchyRow(globalPolicy, 0, ""));
      const roleSitePolicies = sitePolicies.filter((policy) => policy.role_scope === role);
      const coveredSiteKeys = new Set();
      roleSitePolicies.forEach((sitePolicy) => {
        const sitePolicyKey = `site:${sitePolicy.id}`;
        const siteTargets = normalizeTargetSites(sitePolicy);
        siteTargets.forEach((site) => {
          const key = policySiteKey(site);
          if (key) coveredSiteKeys.add(key);
        });
        rows.push(makePolicyHierarchyRow(sitePolicy, 1, globalKey, null, [globalPolicy]));
        const siteKeySet = new Set(siteTargets.map(policySiteKey).filter(Boolean));
        devicePolicies
          .filter((policy) => policy.role_scope === role)
          .forEach((devicePolicy) => {
            const deviceSites = normalizeTargetSites(devicePolicy);
            const matchedSites = deviceSites.filter((site) => siteKeySet.has(policySiteKey(site)));
            matchedSites.forEach((site) => {
              rows.push(makePolicyHierarchyRow(devicePolicy, 2, `${sitePolicyKey}:${policySiteKey(site)}`, site));
            });
          });
      });
      devicePolicies
        .filter((policy) => policy.role_scope === role)
        .forEach((devicePolicy) => {
          const deviceSites = normalizeTargetSites(devicePolicy);
          const uncoveredSites = deviceSites.filter((site) => {
            const key = policySiteKey(site);
            return !key || !coveredSiteKeys.has(key);
          });
          if (!deviceSites.length || !uncoveredSites.length && !roleSitePolicies.length) {
            rows.push(makePolicyHierarchyRow(devicePolicy, 1, globalKey, null));
            return;
          }
          uncoveredSites.forEach((site) => {
            rows.push(makePolicyHierarchyRow(devicePolicy, 1, `${globalKey}:${policySiteKey(site)}`, site));
          });
        });
    });
  });
  return annotatePolicyTreeRows(rows);
}

export function defaultPolicyDraft(policyType) {
  return {
    policy_type: policyType,
    name: policyTypeLabel(policyType),
    description: "",
    enabled: true,
    role_scope: "Workstation",
    deferral_days: 14,
    managed_update_mode: true,
    install_schedule_type: "weekly",
    install_start_ts: null,
    reboot_after_install: false,
    reboot_schedule_enabled: false,
    reboot_schedule_type: "weekly",
    reboot_start_ts: null,
    force_reboot_logged_in: false,
    site_ids: [],
    targets: [],
    exclusions: [],
    rules: [
      { rule_type: "approve", match_type: "severity", match_value: "Critical" },
      { rule_type: "approve", match_type: "severity", match_value: "Important" },
      { rule_type: "approve", match_type: "classification", match_value: "Security Updates" },
      { rule_type: "approve", match_type: "classification", match_value: "Critical Updates" },
      { rule_type: "block", match_type: "classification", match_value: "Drivers" },
      { rule_type: "block", match_type: "classification", match_value: "Feature Packs" },
    ],
  };
}

export function policyDraftFromRecord(policy = {}, policyType = "site") {
  const source = policy && typeof policy === "object" ? policy : {};
  return {
    ...defaultPolicyDraft(policyType),
    ...source,
    policy_type: text(source.policy_type) || policyType,
    enabled: source.enabled === undefined ? true : Boolean(source.enabled),
    managed_update_mode: source.managed_update_mode === undefined ? true : Boolean(source.managed_update_mode),
    reboot_after_install: Boolean(source.reboot_after_install),
    reboot_schedule_enabled: Boolean(source.reboot_schedule_enabled),
    force_reboot_logged_in: Boolean(source.force_reboot_logged_in),
    site_ids: Array.isArray(source.site_ids) ? source.site_ids.map(Number).filter(Boolean) : [],
    targets: Array.isArray(source.targets) ? source.targets : [],
    exclusions: Array.isArray(source.exclusions) ? source.exclusions : [],
    rules: Array.isArray(source.rules) ? source.rules : [],
  };
}

export function policySavePayload(draft = {}, policyType = "site") {
  const payload = {
    name: text(draft.name),
    description: text(draft.description),
    policy_type: policyType,
    enabled: Boolean(draft.enabled),
    role_scope: text(draft.role_scope) || "Workstation",
    deferral_days: Number(draft.deferral_days || 0) || 14,
    managed_update_mode: Boolean(draft.managed_update_mode),
    install_schedule_type: text(draft.install_schedule_type) || "weekly",
    install_start_ts: draft.install_start_ts || null,
    reboot_after_install: Boolean(draft.reboot_after_install),
    reboot_schedule_enabled: Boolean(draft.reboot_schedule_enabled),
    reboot_schedule_type: text(draft.reboot_schedule_type) || "weekly",
    reboot_start_ts: draft.reboot_start_ts || null,
    force_reboot_logged_in: Boolean(draft.force_reboot_logged_in),
    rules: (Array.isArray(draft.rules) ? draft.rules : [])
      .map((rule) => ({
        rule_type: text(rule.rule_type) || "approve",
        match_type: text(rule.match_type) || "severity",
        match_value: text(rule.match_value),
        override_parent_block: Boolean(rule.override_parent_block),
        notes: text(rule.notes),
      }))
      .filter((rule) => rule.match_value),
    exclusions: (Array.isArray(draft.exclusions) ? draft.exclusions : [])
      .map((item) => {
        const targetType = text(item.target_type) || "device";
        const siteId = targetType === "filter" ? "" : Number(item.site_id || 0) || "";
        return {
          exclusion_type: text(item.exclusion_type) || "unmanaged",
          target_type: targetType,
          hostname: targetType === "filter" ? "" : text(item.hostname),
          device_guid: targetType === "filter" ? "" : text(item.device_guid),
          site_id: siteId,
          filter_id: targetType === "filter" ? Number(item.filter_id || 0) || "" : "",
          reason: text(item.reason),
        };
      })
      .filter((item) => text(item.hostname || item.device_guid || item.filter_id)),
  };
  if (policyType === "site") {
    payload.site_ids = (Array.isArray(draft.site_ids) ? draft.site_ids : []).map(Number).filter(Boolean);
  } else {
    payload.targets = (Array.isArray(draft.targets) ? draft.targets : [])
      .map((item) => {
        const targetType = text(item.target_type) || "device";
        return {
          target_type: targetType,
          hostname: targetType === "filter" ? "" : text(item.hostname),
          device_guid: targetType === "filter" ? "" : text(item.device_guid),
          filter_id: targetType === "filter" ? Number(item.filter_id || 0) || "" : "",
        };
      })
      .filter((item) => text(item.hostname || item.device_guid || item.filter_id));
  }
  return payload;
}

function PatchPolicyDialog({ open, policyType, metadata, policy, onClose, onSaved }) {
  const [draft, setDraft] = useState(() => policyDraftFromRecord(policy, policyType));
  const [editorTab, setEditorTab] = useState("details");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [warnings, setWarnings] = useState([]);

  useEffect(() => {
    if (open) {
      setDraft(policyDraftFromRecord(policy, policyType));
      setEditorTab("details");
      setSaveError("");
      setWarnings([]);
    }
  }, [open, policy, policyType]);

  const setField = useCallback((field, value) => {
    setDraft((previous) => ({ ...previous, [field]: value }));
  }, []);

  const addRule = useCallback((ruleType) => {
    setDraft((previous) => ({
      ...previous,
      rules: [
        ...(Array.isArray(previous.rules) ? previous.rules : []),
        { rule_type: ruleType, match_type: "kb", match_value: "", notes: "" },
      ],
    }));
  }, []);

  const updateRule = useCallback((index, field, value) => {
    setDraft((previous) => {
      const rules = [...(Array.isArray(previous.rules) ? previous.rules : [])];
      rules[index] = { ...(rules[index] || {}), [field]: value };
      return { ...previous, rules };
    });
  }, []);

  const deleteRule = useCallback((index) => {
    setDraft((previous) => {
      const rules = [...(Array.isArray(previous.rules) ? previous.rules : [])];
      rules.splice(index, 1);
      return { ...previous, rules };
    });
  }, []);

  const addDeviceTarget = useCallback(() => {
    setDraft((previous) => ({
      ...previous,
      targets: [...(Array.isArray(previous.targets) ? previous.targets : []), { target_type: "device", hostname: "" }],
    }));
  }, []);

  const addFilterTarget = useCallback(() => {
    const firstFilter = Array.isArray(metadata?.filters) ? metadata.filters[0] : null;
    setDraft((previous) => ({
      ...previous,
      targets: [
        ...(Array.isArray(previous.targets) ? previous.targets : []),
        { target_type: "filter", filter_id: Number(firstFilter?.id || 0) || "" },
      ],
    }));
  }, [metadata]);

  const updateTarget = useCallback((index, field, value) => {
    setDraft((previous) => {
      const targets = [...(Array.isArray(previous.targets) ? previous.targets : [])];
      targets[index] = { ...(targets[index] || {}), [field]: value };
      return { ...previous, targets };
    });
  }, []);

  const deleteTarget = useCallback((index) => {
    setDraft((previous) => {
      const targets = [...(Array.isArray(previous.targets) ? previous.targets : [])];
      targets.splice(index, 1);
      return { ...previous, targets };
    });
  }, []);

  const addExclusion = useCallback((exclusionType) => {
    setDraft((previous) => ({
      ...previous,
      exclusions: [
        ...(Array.isArray(previous.exclusions) ? previous.exclusions : []),
        { exclusion_type: exclusionType, target_type: "device", hostname: "", site_id: "", reason: "" },
      ],
    }));
  }, []);

  const updateExclusion = useCallback((index, field, value) => {
    setDraft((previous) => {
      const exclusions = [...(Array.isArray(previous.exclusions) ? previous.exclusions : [])];
      exclusions[index] = { ...(exclusions[index] || {}), [field]: value };
      return { ...previous, exclusions };
    });
  }, []);

  const deleteExclusion = useCallback((index) => {
    setDraft((previous) => {
      const exclusions = [...(Array.isArray(previous.exclusions) ? previous.exclusions : [])];
      exclusions.splice(index, 1);
      return { ...previous, exclusions };
    });
  }, []);

  const savePolicy = useCallback(
    async ({ confirmParentOverrides = false } = {}) => {
      setSaving(true);
      setSaveError("");
      try {
        const payload = {
          ...policySavePayload(draft, policyType),
          confirm_parent_overrides: confirmParentOverrides,
        };
        const method = draft.id ? "PUT" : "POST";
        const url = draft.id ? `/api/patches/policies/${draft.id}` : "/api/patches/policies";
        const response = await fetch(url, {
          method,
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) {
          if (body?.error === "parent_block_override_confirmation_required") {
            setWarnings(Array.isArray(body?.warnings) ? body.warnings : []);
            setSaveError("Parent block override confirmation required.");
            return;
          }
          throw new Error(body?.message || body?.error || `HTTP ${response.status}`);
        }
        onSaved?.(body?.policy || payload);
      } catch (error) {
        setSaveError(String(error?.message || error));
      } finally {
        setSaving(false);
      }
    },
    [draft, onSaved, policyType]
  );

  const ruleRows = Array.isArray(draft.rules) ? draft.rules : [];
  const targetRows = Array.isArray(draft.targets) ? draft.targets : [];
  const exclusionRows = Array.isArray(draft.exclusions) ? draft.exclusions : [];
  const siteOptions = Array.isArray(metadata?.sites) ? metadata.sites : [];
  const filterOptions = Array.isArray(metadata?.filters) ? metadata.filters : [];

  return (
    <Dialog open={open} onClose={saving ? undefined : onClose} maxWidth="lg" fullWidth>
      <DialogTitle sx={{ background: "#0b1120", color: MAGIC_UI.textBright, borderBottom: "1px solid rgba(148,163,184,0.18)" }}>
        {draft.id ? `Edit ${policyTypeLabel(policyType)}` : `New ${policyTypeLabel(policyType)}`}
      </DialogTitle>
      <DialogContent sx={{ background: "#0b1120", color: MAGIC_UI.textBright, pt: 2.5 }}>
        <Stack spacing={2.2}>
          {saveError ? (
            <Alert
              severity={warnings.length ? "warning" : "error"}
              action={
                warnings.length ? (
                  <Button color="inherit" size="small" onClick={() => savePolicy({ confirmParentOverrides: true })}>
                    Confirm
                  </Button>
                ) : null
              }
            >
              {saveError}
            </Alert>
          ) : null}
          <Tabs
            value={editorTab}
            onChange={(_, value) => setEditorTab(value)}
            sx={buildNavTabsSx()}
            TabIndicatorProps={{ sx: { background: "linear-gradient(90deg, #7dd3fc, #c084fc)" } }}
          >
            {POLICY_EDITOR_TABS.map((tab) => (
              <Tab key={tab.key} value={tab.key} label={tab.label} />
            ))}
          </Tabs>
          {editorTab === "details" ? (
            <>
          <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
            <TextField
              label="Name"
              value={draft.name || ""}
              onChange={(event) => setField("name", event.target.value)}
              fullWidth
              size="small"
            />
            <TextField
              label="Deferral Days"
              type="number"
              value={draft.deferral_days || 14}
              onChange={(event) => setField("deferral_days", event.target.value)}
              size="small"
              sx={{ width: { xs: "100%", md: 160 } }}
            />
          </Stack>
          <TextField
            label="Description"
            value={draft.description || ""}
            onChange={(event) => setField("description", event.target.value)}
            fullWidth
            size="small"
          />
            </>
          ) : null}
          {editorTab === "schedule" ? (
            <>
          <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
            <TextField
              label="Install Schedule"
              select
              value={draft.install_schedule_type || "weekly"}
              onChange={(event) => setField("install_schedule_type", event.target.value)}
              size="small"
              sx={{ minWidth: 180 }}
            >
              {POLICY_SCHEDULE_TYPES.map((option) => (
                <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
              ))}
            </TextField>
            <TextField
              label="Install Start"
              type="datetime-local"
              value={datetimeLocalFromUnix(draft.install_start_ts)}
              onChange={(event) => setField("install_start_ts", unixFromDatetimeLocal(event.target.value))}
              size="small"
              InputLabelProps={{ shrink: true }}
              sx={{ minWidth: 230 }}
            />
            <FormControlLabel
              control={<Switch checked={Boolean(draft.enabled)} onChange={(event) => setField("enabled", event.target.checked)} />}
              label="Enabled"
            />
            <FormControlLabel
              control={<Switch checked={Boolean(draft.managed_update_mode)} onChange={(event) => setField("managed_update_mode", event.target.checked)} />}
              label="Managed Windows Update"
            />
          </Stack>
          <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
            <FormControlLabel
              control={<Switch checked={Boolean(draft.reboot_after_install)} onChange={(event) => setField("reboot_after_install", event.target.checked)} />}
              label="Reboot after install"
            />
            <FormControlLabel
              control={<Switch checked={Boolean(draft.reboot_schedule_enabled)} onChange={(event) => setField("reboot_schedule_enabled", event.target.checked)} />}
              label="Separate reboot schedule"
            />
            <TextField
              label="Reboot Start"
              type="datetime-local"
              value={datetimeLocalFromUnix(draft.reboot_start_ts)}
              onChange={(event) => setField("reboot_start_ts", unixFromDatetimeLocal(event.target.value))}
              size="small"
              InputLabelProps={{ shrink: true }}
              sx={{ minWidth: 230 }}
            />
            <FormControlLabel
              control={<Switch checked={Boolean(draft.force_reboot_logged_in)} onChange={(event) => setField("force_reboot_logged_in", event.target.checked)} />}
              label="Force logged-in user"
            />
          </Stack>
            </>
          ) : null}
          {editorTab === "details" ? (
            policyType === "site" ? (
            <Stack direction={{ xs: "column", md: "row" }} spacing={2}>
              <TextField
                label="Policy Type"
                select
                value={draft.role_scope || "Workstation"}
                onChange={(event) => setField("role_scope", event.target.value)}
                size="small"
                sx={{ minWidth: 180 }}
              >
                {POLICY_ROLE_SCOPES.map((option) => (
                  <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                ))}
              </TextField>
              <TextField
                label="Sites"
                select
                SelectProps={{ multiple: true }}
                value={Array.isArray(draft.site_ids) ? draft.site_ids : []}
                onChange={(event) => setField("site_ids", valueArray(event.target.value).map(Number).filter(Boolean))}
                size="small"
                sx={{ minWidth: 320, flex: 1 }}
              >
                {siteOptions.map((site) => (
                  <MenuItem key={site.id} value={Number(site.id)}>{site.name || `Site ${site.id}`}</MenuItem>
                ))}
              </TextField>
            </Stack>
          ) : (
            <Stack spacing={1.1}>
              <Stack direction="row" spacing={1}>
                <Button size="small" startIcon={<AddRoundedIcon />} onClick={addDeviceTarget}>Device Target</Button>
                <Button size="small" startIcon={<AddRoundedIcon />} onClick={addFilterTarget}>Filter Target</Button>
              </Stack>
              {targetRows.map((target, index) => (
                <Stack key={`target-${index}`} direction={{ xs: "column", md: "row" }} spacing={1}>
                  <TextField
                    label="Target Type"
                    select
                    value={target.target_type || "device"}
                    onChange={(event) => updateTarget(index, "target_type", event.target.value)}
                    size="small"
                    sx={{ minWidth: 150 }}
                  >
                    <MenuItem value="device">Device</MenuItem>
                    <MenuItem value="filter">Filter</MenuItem>
                  </TextField>
                  {target.target_type === "filter" ? (
                    <TextField
                      label="Filter"
                      select
                      value={target.filter_id || ""}
                      onChange={(event) => updateTarget(index, "filter_id", event.target.value)}
                      size="small"
                      sx={{ minWidth: 300, flex: 1 }}
                    >
                      {filterOptions.map((filter) => (
                        <MenuItem key={filter.id} value={Number(filter.id)}>{filter.name || `Filter ${filter.id}`}</MenuItem>
                      ))}
                    </TextField>
                  ) : (
                    <TextField
                      label="Hostname"
                      value={target.hostname || ""}
                      onChange={(event) => updateTarget(index, "hostname", event.target.value)}
                      size="small"
                      sx={{ flex: 1 }}
                    />
                  )}
                  <Button size="small" startIcon={<DeleteRoundedIcon />} onClick={() => deleteTarget(index)}>
                    Remove
                  </Button>
                </Stack>
              ))}
            </Stack>
            )
          ) : null}
          {editorTab === "exclusions" ? (
            <Stack spacing={1.1}>
              <Stack direction="row" spacing={1} alignItems="center">
                <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>Exclusions</Typography>
                <Button size="small" startIcon={<AddRoundedIcon />} onClick={() => addExclusion("unmanaged")}>Unmanaged</Button>
                <Button size="small" startIcon={<AddRoundedIcon />} onClick={() => addExclusion("frozen")}>Frozen</Button>
              </Stack>
              {exclusionRows.map((exclusion, index) => (
                <Stack key={`exclusion-${index}`} direction={{ xs: "column", md: "row" }} spacing={1}>
                  <TextField
                    label="Mode"
                    select
                    value={exclusion.exclusion_type || "unmanaged"}
                    onChange={(event) => updateExclusion(index, "exclusion_type", event.target.value)}
                    size="small"
                    sx={{ minWidth: 150 }}
                  >
                    {POLICY_EXCLUSION_TYPES.map((option) => (
                      <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                    ))}
                  </TextField>
                  <TextField
                    label="Target Type"
                    select
                    value={exclusion.target_type || "device"}
                    onChange={(event) => updateExclusion(index, "target_type", event.target.value)}
                    size="small"
                    sx={{ minWidth: 150 }}
                  >
                    <MenuItem value="device">Device</MenuItem>
                    <MenuItem value="filter">Filter</MenuItem>
                  </TextField>
                  {exclusion.target_type === "filter" ? (
                    <TextField
                      label="Filter"
                      select
                      value={exclusion.filter_id || ""}
                      onChange={(event) => updateExclusion(index, "filter_id", event.target.value)}
                      size="small"
                      sx={{ minWidth: 260, flex: 1 }}
                    >
                      {filterOptions.map((filter) => (
                        <MenuItem key={filter.id} value={Number(filter.id)}>{filter.name || `Filter ${filter.id}`}</MenuItem>
                      ))}
                    </TextField>
                  ) : (
                    <Stack direction={{ xs: "column", md: "row" }} spacing={1} sx={{ minWidth: { md: 440 }, flex: 1 }}>
                      <TextField
                        label="Site"
                        select
                        value={Number(exclusion.site_id || 0) || ""}
                        onChange={(event) => updateExclusion(index, "site_id", Number(event.target.value || 0) || "")}
                        size="small"
                        sx={{ minWidth: 200, flex: 0.8 }}
                      >
                        <MenuItem value="">Select site</MenuItem>
                        {siteOptions.map((site) => (
                          <MenuItem key={site.id} value={Number(site.id)}>{site.name || `Site ${site.id}`}</MenuItem>
                        ))}
                      </TextField>
                      <TextField
                        label="Hostname"
                        value={exclusion.hostname || ""}
                        onChange={(event) => updateExclusion(index, "hostname", event.target.value)}
                        size="small"
                        sx={{ minWidth: 220, flex: 1 }}
                      />
                    </Stack>
                  )}
                  <TextField
                    label="Reason"
                    value={exclusion.reason || ""}
                    onChange={(event) => updateExclusion(index, "reason", event.target.value)}
                    size="small"
                    sx={{ minWidth: 220, flex: 1 }}
                  />
                  <Button size="small" startIcon={<DeleteRoundedIcon />} onClick={() => deleteExclusion(index)}>
                    Remove
                  </Button>
                </Stack>
              ))}
            </Stack>
          ) : null}
          {editorTab === "rules" ? (
          <Stack spacing={1}>
            <Stack direction="row" spacing={1} alignItems="center">
              <Typography sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>Allow / Block Rules</Typography>
              <Button size="small" startIcon={<AddRoundedIcon />} onClick={() => addRule("approve")}>Approve</Button>
              <Button size="small" startIcon={<AddRoundedIcon />} onClick={() => addRule("block")}>Block</Button>
            </Stack>
            <GridShell sx={{ height: 280, borderRadius: 0 }}>
              <AgGridReact
                rowData={ruleRows}
                columnDefs={[
                  {
                    field: "match_type",
                    headerName: "Type",
                    width: 150,
                    cellRenderer: (params) => (
                      <TextField select size="small" value={params.value || "severity"} onChange={(event) => updateRule(params.node.rowIndex, "match_type", event.target.value)} fullWidth>
                        {POLICY_MATCH_TYPES.map((option) => (
                          <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                        ))}
                      </TextField>
                    ),
                  },
                  { field: "match_value", headerName: "KB / Value", flex: 1, minWidth: 180, cellRenderer: (params) => (
                    <TextField size="small" value={params.value || ""} onChange={(event) => updateRule(params.node.rowIndex, "match_value", event.target.value)} fullWidth />
                  ) },
                  { field: "classification", headerName: "Classification", width: 170, valueGetter: (params) => params.data?.match_type === "classification" ? params.data?.match_value : "" },
                  { field: "severity", headerName: "Severity", width: 130, valueGetter: (params) => params.data?.match_type === "severity" ? params.data?.match_value : "" },
                  {
                    field: "rule_type",
                    headerName: "Action",
                    width: 145,
                    cellRenderer: (params) => (
                      <TextField select size="small" value={params.value || "approve"} onChange={(event) => updateRule(params.node.rowIndex, "rule_type", event.target.value)} fullWidth>
                        {POLICY_RULE_TYPES.map((option) => (
                          <MenuItem key={option.value} value={option.value}>{option.label}</MenuItem>
                        ))}
                      </TextField>
                    ),
                  },
                  { field: "created_by", headerName: "Created By", width: 140 },
                  { field: "created_at", headerName: "Created At", width: 180, valueFormatter: (params) => formatTimestamp(params.value) },
                  {
                    field: "override_parent_block",
                    headerName: "Parent Override",
                    width: 160,
                    cellRenderer: (params) => (
                      <Checkbox
                        size="small"
                        checked={Boolean(params.value)}
                        onChange={(event) => updateRule(params.node.rowIndex, "override_parent_block", event.target.checked)}
                        sx={{ color: BOREALIS_BLUE, "&.Mui-checked": { color: BOREALIS_BLUE } }}
                      />
                    ),
                  },
                  {
                    colId: "delete",
                    headerName: "",
                    width: 110,
                    cellRenderer: (params) => (
                      <Button size="small" startIcon={<DeleteRoundedIcon />} onClick={() => deleteRule(params.node.rowIndex)}>
                        Remove
                      </Button>
                    ),
                  },
                ]}
                defaultColDef={DEFAULT_GRID_COL_DEF}
                suppressCellFocus
                getRowId={(params) => String(params.data?.id || `${params.data?.match_type}-${params.data?.match_value}-${params.rowIndex}`)}
                theme={DEVICE_DETAILS_GRID_THEME}
              />
            </GridShell>
          </Stack>
          ) : null}
        </Stack>
      </DialogContent>
      <DialogActions sx={{ background: "#0b1120", borderTop: "1px solid rgba(148,163,184,0.18)", p: 2 }}>
        <Button onClick={onClose} disabled={saving}>Cancel</Button>
        <Button startIcon={<SaveRoundedIcon />} variant="contained" onClick={() => savePolicy()} disabled={saving || !text(draft.name)}>
          Save Policy
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function PatchPolicyTab({ onPendingUpdatesClick }) {
  const navigate = useNavigate();
  const [policies, setPolicies] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [busyId, setBusyId] = useState("");
  const [runUpdatesPolicy, setRunUpdatesPolicy] = useState(null);
  const [runUpdatesError, setRunUpdatesError] = useState("");

  const loadPolicies = useCallback(async () => {
    try {
      const policyResponse = await fetch("/api/patches/policies", { credentials: "include", cache: "no-store" });
      const policyPayload = await policyResponse.json().catch(() => ({}));
      if (!policyResponse.ok) throw new Error(policyPayload?.message || policyPayload?.error || `HTTP ${policyResponse.status}`);
      setPolicies(Array.isArray(policyPayload?.policies) ? policyPayload.policies : []);
      setLoadError("");
    } catch (error) {
      setLoadError(String(error?.message || error));
    }
  }, []);

  useEffect(() => {
    void loadPolicies();
  }, [loadPolicies]);

  const openEdit = useCallback((policy) => {
    if (!policy?.id) return;
    navigate(policyEditPath(policy));
  }, [navigate]);

  const deletePolicy = useCallback(async (policy = {}) => {
    if (!policy?.id || policy.locked) return;
    setBusyId(`delete-${policy.id}`);
    try {
      const response = await fetch(`/api/patches/policies/${policy.id}`, { method: "DELETE", credentials: "include" });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      await loadPolicies();
    } catch (error) {
      setLoadError(String(error?.message || error));
    } finally {
      setBusyId("");
    }
  }, [loadPolicies]);

  const openRunUpdatesDialog = useCallback((policy = {}) => {
    if (!policy?.id) return;
    setRunUpdatesError("");
    setRunUpdatesPolicy(policy);
  }, []);

  const closeRunUpdatesDialog = useCallback(() => {
    if (runUpdatesPolicy?.id && busyId === `run-updates-${runUpdatesPolicy.id}`) return;
    setRunUpdatesError("");
    setRunUpdatesPolicy(null);
  }, [busyId, runUpdatesPolicy]);

  const runUpdatesNow = useCallback(async (policy = {}) => {
    if (!policy?.id) return;
    setBusyId(`run-updates-${policy.id}`);
    setRunUpdatesError("");
    try {
      const response = await fetch("/api/patches/policies/evaluate", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ policy_id: policy.id }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      await loadPolicies();
      setRunUpdatesPolicy(null);
    } catch (error) {
      setRunUpdatesError(`Run Updates Now failed: ${String(error?.message || error)}`);
    } finally {
      setBusyId("");
    }
  }, [loadPolicies]);

  const actionCellRenderer = useCallback(
    (params) => {
      const policy = params.data || {};
      const deleteDisabled =
        !policy?.id ||
        Boolean(policy?.locked) ||
        normalizePolicyTypeValue(policy) === "global" ||
        busyId === `delete-${policy?.id}`;
      const deleteTooltip =
        Boolean(policy?.locked) || normalizePolicyTypeValue(policy) === "global"
          ? "Global policies are locked"
          : "Delete Policy";
      return (
        <Tooltip title={deleteTooltip}>
          <span>
            <IconButton
              size="small"
              disabled={deleteDisabled}
              onClick={(event) => {
                event.stopPropagation();
                void deletePolicy(policy);
              }}
              sx={{
                color: "rgba(148,163,184,0.78)",
                width: 32,
                height: 32,
                "&:hover": { color: "#f87171", background: "rgba(248,113,113,0.1)" },
                "&.Mui-disabled": { color: "rgba(71,85,105,0.48)" },
              }}
            >
              <DeleteRoundedIcon fontSize="small" />
            </IconButton>
          </span>
        </Tooltip>
      );
    },
    [busyId, deletePolicy]
  );

  const hierarchyRows = useMemo(() => buildPatchPolicyHierarchyRows(policies), [policies]);

  const policyNameCellRenderer = useCallback((params) => {
    const row = params.data || {};
    const depth = policyRowDepth(row);
    const treeGutterWidth = depth > 0 ? depth * POLICY_TREE_LEVEL_WIDTH : row.__treeHasChild ? 20 : 0;
    const policyName = text(row.name) || "Patch Policy";
    const editPath = policyEditPath(row);
    const PolicyTypeIcon = policyIconForType(normalizePolicyTypeValue(row));
    const referenceSiteName = text(row.__branchSite?.name) || "Linked";
    const runDisabled = !row?.id || busyId === `run-updates-${row.id}`;
    const handleClick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      openEdit(row);
    };
    return (
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          gap: 0.8,
          height: "100%",
          minWidth: 0,
          width: "100%",
        }}
      >
        {treeGutterWidth > 0 ? (
          <Box
            aria-hidden="true"
            sx={{
              position: "relative",
              width: treeGutterWidth,
              alignSelf: "stretch",
              flexShrink: 0,
            }}
          >
            {depth === 0 && row.__treeHasChild ? (
              <>
                <Box
                  sx={{
                    position: "absolute",
                    left: POLICY_TREE_ANCHOR_X,
                    top: "50%",
                    bottom: -2,
                    borderLeft: "1px solid rgba(88,166,255,0.42)",
                  }}
                />
                <Box
                  sx={{
                    position: "absolute",
                    left: POLICY_TREE_ANCHOR_X,
                    top: "50%",
                    width: 18,
                    borderTop: "1px solid rgba(88,166,255,0.42)",
                  }}
                />
              </>
            ) : null}
            {Array.from({ length: depth }, (_, index) => {
              const level = index + 1;
              const isCurrentLevel = level === depth;
              const left = index * POLICY_TREE_LEVEL_WIDTH + POLICY_TREE_ANCHOR_X;
              const continues = Boolean(row.__treeContinues?.[level] || (isCurrentLevel && row.__treeHasChild));
              return (
                <React.Fragment key={level}>
                  <Box
                    sx={{
                      position: "absolute",
                      left,
                      top: -2,
                      bottom: continues ? -2 : "50%",
                      borderLeft: "1px solid rgba(88,166,255,0.42)",
                    }}
                  />
                  {isCurrentLevel ? (
                    <Box
                      sx={{
                        position: "absolute",
                        left,
                        top: "50%",
                        width: Math.max(12, treeGutterWidth - left + 8),
                        borderTop: "1px solid rgba(88,166,255,0.42)",
                      }}
                    />
                  ) : null}
                </React.Fragment>
              );
            })}
          </Box>
        ) : null}
        <PolicyTypeIcon
          sx={{
            fontSize: 18,
            color: BOREALIS_BLUE,
            flexShrink: 0,
          }}
        />
        <Tooltip title="Run Updates Now">
          <span>
            <IconButton
              size="small"
              disabled={runDisabled}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                openRunUpdatesDialog(row);
              }}
              sx={{
                width: 24,
                height: 24,
                color: "rgba(100,116,139,0.92)",
                flexShrink: 0,
                "&:hover": {
                  color: BOREALIS_BLUE,
                  background: "rgba(125,183,255,0.1)",
                },
                "&.Mui-disabled": {
                  color: "rgba(71,85,105,0.5)",
                },
              }}
            >
              <PlayCircleOutlineRoundedIcon sx={{ fontSize: 18 }} />
            </IconButton>
          </span>
        </Tooltip>
        <Box
          component="a"
          href={editPath}
          onClick={handleClick}
          title={policyName}
          sx={{
            color: "#58a6ff",
            textDecoration: "none",
            fontSize: 13,
            fontWeight: 500,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            "&:hover": {
              textDecoration: "none",
            },
          }}
        >
          {policyName}
        </Box>
        {row.__isReference ? (
          <Tooltip title={referenceSiteName}>
            <Box
              component="span"
              sx={{
                ...PATCH_CHIP_TOKEN_SX,
                height: 18,
                minHeight: 18,
                maxWidth: 115,
                px: 0.7,
                color: "#d4b5ff",
                border: "1px solid rgba(180,137,255,0.35)",
                background: "rgba(180,137,255,0.14)",
                fontSize: 11,
                fontWeight: 600,
                flexShrink: 0,
              }}
            >
              <Box component="span" className="patch-chip-label">
                {referenceSiteName}
              </Box>
            </Box>
          </Tooltip>
        ) : null}
      </Box>
    );
  }, [busyId, openEdit, openRunUpdatesDialog]);

  const targetSitesCellRenderer = useCallback((params) => {
    const sites = Array.isArray(params.data?.__targetSites) ? params.data.__targetSites : normalizeTargetSites(params.data || {});
    const tooltipText = sites.map((site) => text(site.name)).filter(Boolean).join(", ") || "No Site Targets";
    if (!sites.length) {
      return (
        <Tooltip title={tooltipText} arrow>
          <Typography component="span" sx={{ color: MAGIC_UI.textMuted, fontSize: 12, lineHeight: 1 }}>
            No Site Targets
          </Typography>
        </Tooltip>
      );
    }
    const visibleSites = sites.slice(0, 3);
    const hiddenCount = sites.length - visibleSites.length;
    return (
      <Tooltip title={tooltipText} arrow>
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.55, minWidth: 0, width: "100%", height: "100%", overflow: "hidden" }}>
          {visibleSites.map((site) => (
          <Box
            key={`${site.site_id || site.id || 0}-${site.name}`}
            component="span"
            sx={{
              ...PATCH_CHIP_TOKEN_SX,
              maxWidth: 145,
              px: 0.8,
              color: "#89c2ff",
              border: "1px solid rgba(88,166,255,0.42)",
              backgroundColor: "rgba(88,166,255,0.14)",
              fontWeight: 600,
            }}
          >
            <Box component="span" className="patch-chip-label">
              {text(site.name)}
            </Box>
          </Box>
          ))}
          {hiddenCount > 0 ? (
          <Box
            component="span"
            sx={{
              ...PATCH_CHIP_TOKEN_SX,
              px: 0.75,
              color: "#cbd5e1",
              border: "1px solid rgba(148,163,184,0.34)",
              backgroundColor: "rgba(15,23,42,0.62)",
              fontWeight: 600,
              flexShrink: 0,
            }}
          >
            <Box component="span" className="patch-chip-label">
              +{hiddenCount}
            </Box>
          </Box>
          ) : null}
        </Box>
      </Tooltip>
    );
  }, []);

  const columnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Policy Name",
        minWidth: 370,
        flex: 1,
        valueGetter: (params) => text(params.data?.name),
        cellRenderer: policyNameCellRenderer,
      },
      {
        field: "policy_type",
        headerName: "Policy Scope",
        width: 170,
        minWidth: 170,
        flex: 0,
        valueGetter: (params) => params.data?.__policyTypeLabel || policyTableTypeLabel(normalizePolicyTypeValue(params.data || {})),
      },
      {
        field: "enabled",
        headerName: "Enabled",
        width: 115,
        flex: 0,
        cellRenderer: (params) => <PolicyBooleanCell value={params.value} />,
        valueFormatter: (params) => (params.value ? "Enabled" : "Disabled"),
      },
      {
        field: "reboot_after_install",
        headerName: "Reboot?",
        width: 115,
        flex: 0,
        cellRenderer: (params) => <PolicyBooleanCell value={params.value} />,
        valueFormatter: (params) => (params.value ? "Enabled" : "Disabled"),
      },
      { field: "role_scope", headerName: "Device Type", width: 130, flex: 0 },
      {
        colId: "targeted_sites",
        headerName: "Targeted Sites",
        width: 200,
        minWidth: 200,
        flex: 0,
        cellClass: "patch-chip-cell",
        valueGetter: (params) => params.data?.__targetSitesText || policyTargetSiteText(params.data || {}),
        cellRenderer: targetSitesCellRenderer,
      },
      {
        colId: "pending_updates",
        headerName: "Pending Updates",
        width: 550,
        minWidth: 550,
        flex: 0,
        valueGetter: (params) => pendingUpdateBreakdownText(params.data || {}),
        cellRenderer: (params) => (
          <PendingUpdatesCell
            policy={params.data || {}}
            onSelect={(policy, item) => onPendingUpdatesClick?.({ policy, item })}
          />
        ),
      },
      { field: "deferral_days", headerName: "Deferrment", width: 140, flex: 0, valueFormatter: (params) => `${Number(params.value || 0)} days` },
      {
        colId: "schedule",
        headerName: "Schedule",
        width: 245,
        minWidth: 220,
        flex: 0,
        valueGetter: (params) => policyScheduleText(params.data || {}),
        cellRenderer: (params) => <PolicyScheduleCell policy={params.data || {}} />,
      },
      {
        colId: "actions",
        headerName: "Actions",
        width: 35,
        flex: 0,
        sortable: false,
        filter: false,
        resizable: false,
        cellRenderer: actionCellRenderer,
      },
    ],
    [actionCellRenderer, onPendingUpdatesClick, policyNameCellRenderer, targetSitesCellRenderer]
  );

  const RunUpdatesPolicyIcon = policyIconForType(normalizePolicyTypeValue(runUpdatesPolicy));
  const runUpdatesBusy = Boolean(runUpdatesPolicy?.id && busyId === `run-updates-${runUpdatesPolicy.id}`);

  return (
    <>
      <Dialog
        open={Boolean(runUpdatesPolicy)}
        onClose={runUpdatesBusy ? undefined : closeRunUpdatesDialog}
        maxWidth="sm"
        fullWidth
        PaperProps={{
          sx: {
            borderRadius: 3,
            background:
              "radial-gradient(110% 120% at 0% 0%, rgba(250, 204, 21, 0.14), transparent 52%), " +
              "radial-gradient(110% 120% at 100% 0%, rgba(125, 211, 252, 0.14), transparent 58%), rgba(8,12,24,0.98)",
            border: "1px solid rgba(245, 158, 11, 0.35)",
            boxShadow: "0 24px 70px rgba(2,8,23,0.74)",
            color: MAGIC_UI.textBright,
            overflow: "hidden",
          },
        }}
      >
        <DialogTitle sx={{ px: 3, pt: 3, pb: 1.25 }}>
          <Box sx={{ display: "flex", alignItems: "flex-start", gap: 1.4 }}>
            <Box
              sx={{
                width: 36,
                height: 36,
                borderRadius: 2,
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                color: "#facc15",
                background: "rgba(113,63,18,0.42)",
                border: "1px solid rgba(245,158,11,0.42)",
                flexShrink: 0,
              }}
            >
              <WarningAmberRoundedIcon fontSize="small" />
            </Box>
            <Box sx={{ minWidth: 0 }}>
              <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "1.05rem", fontWeight: 800, lineHeight: 1.25 }}>
                Run Updates Now
              </Typography>
              <Box sx={{ display: "flex", alignItems: "center", gap: 0.75, mt: 0.7, minWidth: 0 }}>
                <RunUpdatesPolicyIcon sx={{ color: BOREALIS_BLUE, fontSize: 18, flexShrink: 0 }} />
                <Typography noWrap sx={{ color: MAGIC_UI.textMuted, fontSize: "0.84rem" }}>
                  {text(runUpdatesPolicy?.name) || "Patch Policy"}
                </Typography>
              </Box>
            </Box>
          </Box>
        </DialogTitle>
        <DialogContent sx={{ px: 3, pt: 1.5, pb: 2 }}>
          <Stack spacing={1.6}>
            <Alert
              severity="warning"
              icon={<WarningAmberRoundedIcon />}
              sx={{
                background: "rgba(113, 63, 18, 0.42)",
                color: MAGIC_UI.textBright,
                border: "1px solid rgba(245, 158, 11, 0.45)",
                "& .MuiAlert-icon": { color: "#facc15" },
              }}
            >
              Proceeding will create patch install jobs immediately.
            </Alert>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.92rem", lineHeight: 1.65 }}>
              Devices that belong to this policy and its parent policies will download and install approved updates immediately. If reboot after install is configured, reboot will take place after the update finishes.
            </Typography>
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", sm: "repeat(3, minmax(0, 1fr))" },
                gap: 1,
                p: 1.25,
                borderRadius: 2,
                border: "1px solid rgba(148,163,184,0.18)",
                background: "rgba(5,10,24,0.58)",
              }}
            >
              {[
                ["Policy Scope", policyRowTypeLabel(runUpdatesPolicy)],
                ["Device Type", text(runUpdatesPolicy?.role_scope) || "Not Configured"],
                ["Targeted Devices", formatDeviceCount(runUpdatesPolicy?.target_count)],
              ].map(([label, value]) => (
                <Box key={label} sx={{ minWidth: 0 }}>
                  <Typography sx={{ color: BOREALIS_BLUE, fontSize: 11, fontWeight: 700, textTransform: "uppercase", letterSpacing: 0 }}>
                    {label}
                  </Typography>
                  <Typography noWrap sx={{ color: MAGIC_UI.textBright, fontSize: "0.9rem", fontWeight: 700, mt: 0.35 }}>
                    {value}
                  </Typography>
                </Box>
              ))}
            </Box>
            {runUpdatesError ? <Alert severity="error">{runUpdatesError}</Alert> : null}
          </Stack>
        </DialogContent>
        <DialogActions sx={{ px: 3, pt: 0.5, pb: 2.5, gap: 1 }}>
          <Button
            onClick={closeRunUpdatesDialog}
            disabled={runUpdatesBusy}
            sx={{
              borderRadius: 999,
              px: 2,
              textTransform: "none",
              color: MAGIC_UI.textBright,
              border: "1px solid rgba(148,163,184,0.3)",
              background: "rgba(5,10,24,0.72)",
              "&:hover": { background: "rgba(15,23,42,0.86)", borderColor: "rgba(125,211,252,0.44)" },
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={() => runUpdatesNow(runUpdatesPolicy)}
            disabled={runUpdatesBusy || !runUpdatesPolicy?.id}
            startIcon={<PlayArrowRoundedIcon />}
            sx={{
              borderRadius: 999,
              px: 2,
              textTransform: "none",
              fontWeight: 800,
              color: "#06101d",
              backgroundImage: "linear-gradient(135deg, #facc15 0%, #7dd3fc 100%)",
              "&:hover": { backgroundImage: "linear-gradient(135deg, #fde047 0%, #91dcff 100%)" },
              "&.Mui-disabled": { color: "rgba(15,23,42,0.52)", opacity: 0.68 },
            }}
          >
            {runUpdatesBusy ? "Creating Jobs..." : "Run Updates Now"}
          </Button>
        </DialogActions>
      </Dialog>
      {loadError ? <Box sx={{ p: 2 }}><Alert severity="error">{loadError}</Alert></Box> : null}
      <GridShell
        sx={{
          ...PATCH_GRID_SX,
          flexGrow: 1,
          minHeight: loadError ? 460 : 520,
          height: "100%",
          borderRadius: 0,
          border: "none",
        }}
      >
        <AgGridReact
          rowData={hierarchyRows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
          suppressCellFocus
          pagination
          paginationPageSize={50}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          rowHeight={44}
          headerHeight={44}
          getRowId={(params) => String(params.data?.__hierarchyKey || params.data?.id || params.rowIndex)}
          rowClassRules={{
            "patch-policy-linked-reference": (params) => Boolean(params.data?.__isReference),
          }}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
    </>
  );
}

export default function PatchManagement() {
  const gridRef = useRef(null);
  const patchRefreshTimersRef = useRef([]);
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const [patchRows, setPatchRows] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [stateFilter, setStateFilter] = useState("pending");
  const [severityFilter, setSeverityFilter] = useState("");
  const [selectedPatchRows, setSelectedPatchRows] = useState({});
  const [newPolicyMenuAnchor, setNewPolicyMenuAnchor] = useState(null);
  const selectedSiteId = useMemo(
    () => String(searchParams.get("site") || "").trim(),
    [searchParams]
  );
  const policyPendingFilter = useMemo(() => {
    const scopeID = Number(searchParams.get("policy_scope") || 0) || 0;
    const layerRaw = text(searchParams.get("policy_layer")).toLowerCase();
    const layer = ["global", "site", "device_filter"].includes(layerRaw) ? layerRaw : "";
    return {
      active: scopeID > 0 || Boolean(layer),
      scopeID,
      layer,
      scopeName: text(searchParams.get("policy_scope_name")),
      layerLabel: text(searchParams.get("policy_layer_label")) || (layer ? policyTableTypeLabel(layer) : ""),
    };
  }, [searchParams]);
  const activeTab = useMemo(() => {
    const requested = text(searchParams.get("tab")) || "patch_list";
    return PATCH_PAGE_TABS.some((tab) => tab.key === requested) ? requested : "patch_list";
  }, [searchParams]);
  const setActiveTab = useCallback(
    (nextTab) => {
      const nextParams = new URLSearchParams(searchParams);
      if (nextTab === "patch_list") {
        nextParams.delete("tab");
      } else {
        nextParams.set("tab", nextTab);
      }
      setSearchParams(nextParams, { replace: true });
    },
    [searchParams, setSearchParams]
  );
  const clearPolicyPendingFilter = useCallback(() => {
    const nextParams = new URLSearchParams(searchParams);
    nextParams.delete("policy_scope");
    nextParams.delete("policy_layer");
    nextParams.delete("policy_scope_name");
    nextParams.delete("policy_layer_label");
    setSearchParams(nextParams, { replace: true });
  }, [searchParams, setSearchParams]);
  const applyPolicyPendingFilter = useCallback(
    ({ policy = {}, item = {}, policyType = "", label = "" } = {}) => {
      const selectedPolicyType = normalizePolicyTypeValue({ policy_type: item?.policy_type || policyType });
      const selectedLabel = text(item?.label || label) || policyTableTypeLabel(selectedPolicyType);
      const selectedPolicyID = Number(item?.source_policy_id || policy?.id || 0) || 0;
      const selectedPolicyName = text(item?.source_policy_name) || text(policy?.name) || "Patch Policy";
      if (!selectedPolicyID || !selectedPolicyType) return;
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("tab", "patch_list");
      nextParams.set("policy_scope", String(selectedPolicyID));
      nextParams.set("policy_layer", selectedPolicyType);
      nextParams.set("policy_scope_name", selectedPolicyName);
      nextParams.set("policy_layer_label", selectedLabel);
      setStateFilter("pending");
      setSeverityFilter("");
      setSearchParams(nextParams, { replace: false });
    },
    [searchParams, setSearchParams]
  );

  const loadPatchRows = useCallback(async () => {
    try {
      const response = await fetch("/api/patches/audit", { credentials: "include", cache: "no-store" });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      setPatchRows(Array.isArray(payload?.patches) ? payload.patches : []);
      setLoadError("");
    } catch (error) {
      setLoadError(String(error?.message || error));
      setPatchRows([]);
    }
  }, []);

  useEffect(() => {
    void loadPatchRows();
  }, [loadPatchRows]);

  const clearPatchRefreshTimers = useCallback(() => {
    if (typeof window === "undefined") return;
    patchRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
    patchRefreshTimersRef.current = [];
  }, []);

  const requestPatchAuditRefresh = useCallback(
    ({ burst = false } = {}) => {
      clearPatchRefreshTimers();
      void loadPatchRows();
      if (!burst || typeof window === "undefined") return;
      [1200, 2500, 5000, 10000, 20000].forEach((delayMs) => {
        const timerId = window.setTimeout(() => {
          void loadPatchRows();
        }, delayMs);
        patchRefreshTimersRef.current.push(timerId);
      });
    },
    [clearPatchRefreshTimers, loadPatchRows]
  );

  useEffect(() => () => clearPatchRefreshTimers(), [clearPatchRefreshTimers]);

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (!socket) return undefined;
    const handleInventoryChanged = (payload = {}) => {
      const payloadChange = text(payload?.change).toLowerCase();
      if (payloadChange && payloadChange !== "patches_updated" && payloadChange !== "updated") return;
      requestPatchAuditRefresh({ burst: true });
    };
    socket.on("device_inventory_changed", handleInventoryChanged);
    return () => {
      socket.off("device_inventory_changed", handleInventoryChanged);
    };
  }, [requestPatchAuditRefresh]);

  const siteName = useMemo(() => {
    if (!selectedSiteId) return "";
    const row = patchRows.find((item) => String(item?.site_id ?? "") === selectedSiteId);
    return text(row?.site_name) || `Site ${selectedSiteId}`;
  }, [patchRows, selectedSiteId]);

  const siteScopedRows = useMemo(
    () =>
      selectedSiteId
        ? patchRows.filter((row) => String(row?.site_id ?? "") === selectedSiteId)
        : patchRows,
    [patchRows, selectedSiteId]
  );

  const policyScopedRows = useMemo(
    () => siteScopedRows.filter((row) => patchRowMatchesPolicyPendingFilter(row, policyPendingFilter)),
    [policyPendingFilter, siteScopedRows]
  );

  const severityCountRows = useMemo(
    () =>
      stateFilter
        ? policyScopedRows.filter((row) => text(row.state).toLowerCase() === stateFilter)
        : policyScopedRows,
    [policyScopedRows, stateFilter]
  );

  const stateCountRows = useMemo(
    () =>
      severityFilter
        ? policyScopedRows.filter((row) => normalizeSeverity(row.severity) === severityFilter)
        : policyScopedRows,
    [policyScopedRows, severityFilter]
  );

  const stateCounts = useMemo(
    () =>
      stateCountRows.reduce(
        (counts, row) => {
          const state = text(row.state).toLowerCase();
          if (state in counts) counts[state] += 1;
          return counts;
        },
        { pending: 0, installed: 0 }
      ),
    [stateCountRows]
  );

  const severityCounts = useMemo(
    () =>
      severityCountRows.reduce(
        (counts, row) => {
          const severity = normalizeSeverity(row.severity);
          counts[severity] = (counts[severity] || 0) + 1;
          return counts;
        },
        SEVERITY_FILTER_OPTIONS.reduce((counts, option) => ({ ...counts, [option.key]: 0 }), {})
      ),
    [severityCountRows]
  );

  const visibleRows = useMemo(
    () =>
      policyScopedRows.filter((row) => {
        if (stateFilter && text(row.state).toLowerCase() !== stateFilter) return false;
        if (severityFilter && normalizeSeverity(row.severity) !== severityFilter) return false;
        return true;
      }),
    [policyScopedRows, severityFilter, stateFilter]
  );

  const displayRows = useMemo(() => buildPatchFleetRows(visibleRows), [visibleRows]);

  useEffect(() => {
    setSelectedPatchRows((previous) => {
      const validIds = new Set(displayRows.filter(canSelectPatchForInstall).map((row) => text(row.id)));
      const next = {};
      Object.entries(previous).forEach(([key, value]) => {
        if (value && validIds.has(key)) next[key] = true;
      });
      const previousKeys = Object.keys(previous);
      const nextKeys = Object.keys(next);
      if (previousKeys.length === nextKeys.length && nextKeys.every((key) => previous[key])) return previous;
      return next;
    });
  }, [displayRows]);

  const selectedBulkRows = useMemo(
    () => displayRows.filter((row) => selectedPatchRows[text(row.id)] && canSelectPatchForInstall(row)),
    [displayRows, selectedPatchRows]
  );

  const selectedBulkPatchCount = useMemo(
    () => new Set(selectedBulkRows.map(patchSelectionIdentity).filter(Boolean)).size,
    [selectedBulkRows]
  );
  const patchReturnTo = useMemo(
    () => `${location.pathname || APP_PATHS.patchManagementWindows}${location.search || ""}`,
    [location.pathname, location.search]
  );

  const togglePatchSelection = useCallback((row = {}) => {
    const rowID = text(row.id);
    if (!rowID || !canSelectPatchForInstall(row)) return;
    setSelectedPatchRows((previous) => {
      const next = { ...previous };
      if (next[rowID]) {
        delete next[rowID];
      } else {
        next[rowID] = true;
      }
      return next;
    });
  }, []);

  const openDevicesForPatch = useCallback(
    (row = {}) => {
      const hostnames = Array.isArray(row.hostnames) ? row.hostnames.filter(Boolean) : [];
      if (!hostnames.length) return;
      try {
        window.localStorage.setItem("device_list_initial_hostnames_filter", JSON.stringify(hostnames));
        if (text(row.kb)) {
          window.localStorage.setItem("device_list_initial_filters", JSON.stringify({ patch: text(row.kb) }));
        }
      } catch {
        /* noop */
      }
      navigate(APP_PATHS.devices);
    },
    [navigate]
  );

  const handleInstallPatchFleet = useCallback(
    (row = {}) => {
      const item = fleetPatchDraftItem(row);
      if (!item) return;
      navigate(`${APP_PATHS.jobNew}?tab=schedule`, {
        state: {
          patchJobDraft: {
            id: `patch-fleet-${text(row.patch_key)}-${Date.now()}`,
            source: "fleet",
            trigger: item.trigger,
            trigger_label: item.trigger_label,
            patch: item.patch,
            targets: item.targets,
            target_count: item.target_count,
            expiration: "2h",
            job_name: item.job_name,
            return_to: patchReturnTo,
          },
        },
      });
    },
    [navigate, patchReturnTo]
  );

  const handleBulkInstallFleet = useCallback(() => {
    if (selectedBulkPatchCount < 2) return;
    const items = selectedBulkRows
      .map((row) => fleetPatchDraftItem(row, { triggerLabel: "Bulk Ad-Hoc Install" }))
      .filter(Boolean);
    if (items.length < 2) return;
    navigate(`${APP_PATHS.jobNew}?tab=schedule`, {
      state: {
        patchJobDraft: {
          id: `patch-bulk-fleet-${Date.now()}`,
          source: "fleet",
          trigger: "bulk_ad_hoc",
          trigger_label: "Bulk Ad-Hoc Install",
          mode: "bulk",
          bulk: true,
          items,
          expiration: "2h",
          return_to: patchReturnTo,
        },
      },
    });
  }, [navigate, patchReturnTo, selectedBulkPatchCount, selectedBulkRows]);

  const handleNewPolicyOption = useCallback(
    (policyType) => {
      setNewPolicyMenuAnchor(null);
      navigate(
        policyType === "device_filter"
          ? APP_PATHS.patchPolicyDeviceFilterNew
          : APP_PATHS.patchPolicySiteNew
      );
    },
    [navigate]
  );

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "patch-management-bulk-install",
        label: "Bulk Install",
        icon: <SystemUpdateAltRoundedIcon />,
        tone: "secondary",
        disabled: activeTab !== "patch_list" || selectedBulkPatchCount < 2,
        onClick: handleBulkInstallFleet,
      },
      {
        id: "patch-management-new-policy",
        label: "New Policy",
        icon: <AddRoundedIcon />,
        tone: "primary",
        onClick: (event) => setNewPolicyMenuAnchor(event.currentTarget),
      },
    ],
    [activeTab, handleBulkInstallFleet, selectedBulkPatchCount]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: WindowsPatchPageIcon,
    actions: pageHeaderActions,
  });

  const columnDefs = useMemo(
    () => [
      {
        colId: "select",
        headerName: "",
        width: 52,
        minWidth: 52,
        maxWidth: 52,
        sortable: false,
        filter: false,
        resizable: false,
        cellRenderer: (params) => {
          const row = params.data || {};
          const rowID = text(row.id);
          const eligible = canSelectPatchForInstall(row);
          return (
            <Checkbox
              size="small"
              checked={Boolean(eligible && selectedPatchRows[rowID])}
              disabled={!eligible}
              onClick={(event) => event.stopPropagation()}
              onChange={() => togglePatchSelection(row)}
              sx={{
                p: 0.2,
                color: "rgba(148,163,184,0.65)",
                "&.Mui-checked": { color: BOREALIS_BLUE },
                "&.Mui-disabled": { color: "rgba(71,85,105,0.5)" },
              }}
            />
          );
        },
      },
      {
        field: "kb",
        headerName: "KB",
        width: 120,
        minWidth: 120,
        valueGetter: (params) => text(params.data?.kb) || "No KB",
        cellRenderer: (params) => {
          const kb = text(params.value);
          const catalogURL = microsoftUpdateCatalogURL(kb);
          if (!catalogURL) {
            return <Typography component="span" sx={{ color: MAGIC_UI.textBright, fontSize: 13 }}>{kb || "No KB"}</Typography>;
          }
          return (
            <Tooltip title={`Open ${kb} in Microsoft Update Catalog`}>
              <Typography
                component="a"
                href={catalogURL}
                target="_blank"
                rel="noopener noreferrer"
                onClick={(event) => event.stopPropagation()}
                sx={{
                  color: BOREALIS_BLUE,
                  fontSize: 13,
                  fontWeight: 700,
                  textDecoration: "none",
                  "&:hover": { textDecoration: "underline" },
                }}
              >
                {kb}
              </Typography>
            </Tooltip>
          );
        },
      },
      {
        field: "title",
        headerName: "Title",
        flex: 1.6,
        minWidth: 320,
        tooltipField: "title",
      },
      {
        field: "state",
        headerName: "State",
        width: 135,
        minWidth: 135,
        valueFormatter: (params) => formatState(params.value),
      },
      {
        colId: "policy_source",
        headerName: "Linked Policy(s)",
        width: 260,
        minWidth: 230,
        cellClass: "patch-chip-cell",
        valueGetter: (params) => patchPolicySourceText(params.data || {}),
        cellRenderer: (params) => <PatchPolicySourceCell row={params.data || {}} />,
      },
      {
        field: "install",
        headerName: "Install",
        width: 240,
        minWidth: 240,
        sortable: false,
        filter: false,
        resizable: false,
        cellRenderer: (params) => {
          const row = params.data || {};
          const patchKey = text(row.patch_key);
          const activeJob = row.active_install_job || null;
          if (activeJob?.id) {
            const label = text(activeJob.label) || `Scheduled - Job ID: ${activeJob.id}`;
            const jobPath = text(activeJob.path) || `${APP_PATHS.job(activeJob.id)}?tab=job_history`;
            return (
              <Tooltip title="This patch already has an ad-hoc deployment job. Let that job finish, time out, or delete it before scheduling this KB again.">
                <Button
                  size="small"
                  onClick={() => navigate(jobPath)}
                  sx={{
                    minWidth: 190,
                    px: 0.8,
                    py: 0.25,
                    color: BOREALIS_BLUE,
                    textTransform: "none",
                    fontWeight: 700,
                    justifyContent: "flex-start",
                    "&:hover": { background: "rgba(88,166,255,0.1)" },
                  }}
                >
                  {label}
                </Button>
              </Tooltip>
            );
          }
          const disabled = text(row.state).toLowerCase() !== "pending" || !patchKey;
          return (
            <Button
              size="small"
              startIcon={<SystemUpdateAltRoundedIcon fontSize="small" />}
              disabled={disabled}
              onClick={() => handleInstallPatchFleet(row)}
              sx={{
                minWidth: 92,
                borderRadius: 999,
                px: 1.15,
                py: 0.25,
                color: "#031525",
                textTransform: "none",
                fontWeight: 700,
                background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
                "&:hover": { background: "linear-gradient(135deg, #93ddff 0%, #d0a0ff 100%)" },
                "&.Mui-disabled": {
                  color: "rgba(148,163,184,0.65)",
                  background: "rgba(15,23,42,0.55)",
                  border: "1px solid rgba(148,163,184,0.18)",
                },
              }}
            >
              Install
            </Button>
          );
        },
      },
      {
        field: "severity",
        headerName: "Severity",
        width: 150,
        minWidth: 135,
        valueFormatter: (params) => text(params.value) || "Unspecified",
      },
      {
        field: "classification",
        headerName: "Classification",
        width: 190,
        minWidth: 170,
      },
      {
        field: "source",
        headerName: "Source",
        width: 150,
        minWidth: 135,
        valueFormatter: (params) => formatSource(params.value),
      },
      {
        field: "device_count",
        headerName: "Devices",
        width: 145,
        minWidth: 130,
        filter: "agNumberColumnFilter",
        cellRenderer: (params) => (
          <Button
            size="small"
            startIcon={<DevicesRoundedIcon fontSize="small" />}
            onClick={() => openDevicesForPatch(params.data)}
            sx={{
              minWidth: 110,
              borderRadius: 999,
              px: 1.25,
              py: 0.25,
              color: MAGIC_UI.textBright,
              textTransform: "none",
              border: "1px solid rgba(148,163,184,0.3)",
              "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
            }}
          >
            {Number(params.value || 0).toLocaleString()}
          </Button>
        ),
      },
      {
        field: "captured_at",
        headerName: "Updated",
        width: 215,
        minWidth: 190,
        valueFormatter: (params) => formatTimestamp(params.value),
      },
    ],
    [handleInstallPatchFleet, navigate, openDevicesForPatch, selectedPatchRows, togglePatchSelection]
  );

  return (
    <>
      <Menu
        anchorEl={newPolicyMenuAnchor}
        open={Boolean(newPolicyMenuAnchor)}
        onClose={() => setNewPolicyMenuAnchor(null)}
        PaperProps={{
          sx: {
            mt: 1,
            borderRadius: 2,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: "rgba(8,13,30,0.98)",
            color: MAGIC_UI.textBright,
            boxShadow: "0 24px 60px rgba(2,8,23,0.7)",
            "& .MuiMenuItem-root": {
              fontSize: "0.88rem",
              color: MAGIC_UI.textBright,
              minHeight: 34,
            },
            "& .MuiMenuItem-root:hover": {
              background: "rgba(125, 183, 255, 0.12)",
            },
          },
        }}
      >
        <MenuItem onClick={() => handleNewPolicyOption("site")}>New Site Policy</MenuItem>
        <MenuItem onClick={() => handleNewPolicyOption("device_filter")}>
          New Device Filter Policy
        </MenuItem>
      </Menu>
      <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Stack spacing={1.2}>
          <Tabs
            value={activeTab}
            onChange={(_, value) => setActiveTab(value)}
            sx={buildNavTabsSx()}
            TabIndicatorProps={{ sx: { background: "linear-gradient(90deg, #7dd3fc, #c084fc)" } }}
          >
            {PATCH_PAGE_TABS.map((tab) => (
              <Tab key={tab.key} value={tab.key} label={tab.label} />
            ))}
          </Tabs>
          {activeTab === "patch_list" ? (
            <Box sx={{ display: "flex", alignItems: "flex-start", columnGap: 1, rowGap: 1, flexWrap: "wrap" }}>
              <FilterSliderBlock label="State">
                <CountSliderGroup
                  options={STATE_FILTER_OPTIONS}
                  activeKey={stateFilter}
                  counts={stateCounts}
                  onChange={(value) => {
                    setStateFilter(value);
                    if (policyPendingFilter.active && value !== "pending") {
                      clearPolicyPendingFilter();
                    }
                  }}
                />
              </FilterSliderBlock>
              <FilterSliderBlock label="Severity">
                <CountSliderGroup
                  options={SEVERITY_FILTER_OPTIONS}
                  activeKey={severityFilter}
                  counts={severityCounts}
                  onChange={setSeverityFilter}
                />
              </FilterSliderBlock>
              {policyPendingFilter.active ? (
                <Box
                  sx={{
                    display: "inline-flex",
                    alignItems: "center",
                    alignSelf: "flex-end",
                    gap: 0.8,
                    borderRadius: 999,
                    border: "1px solid rgba(88,166,255,0.35)",
                    background: "rgba(8,12,24,0.78)",
                    px: 1.35,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: "rgba(191,219,254,0.92)", fontSize: "0.82rem", fontWeight: 700 }}>
                    {`Policy: ${policyPendingFilter.scopeName || "Patch Policy"} - ${policyPendingFilter.layerLabel || "Pending Updates"}`}
                  </Typography>
                  <Button
                    size="small"
                    onClick={clearPolicyPendingFilter}
                    sx={{
                      minWidth: 0,
                      px: 1,
                      py: 0.15,
                      borderRadius: 999,
                      color: MAGIC_UI.textBright,
                      textTransform: "none",
                      fontSize: "0.76rem",
                      border: "1px solid rgba(148,163,184,0.28)",
                      "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
                    }}
                  >
                    Clear
                  </Button>
                </Box>
              ) : null}
              {siteName ? (
                <Box
                  sx={{
                    display: "inline-flex",
                    alignItems: "center",
                    alignSelf: "flex-end",
                    gap: 0.8,
                    borderRadius: 999,
                    border: "1px solid rgba(148,163,184,0.35)",
                    background: "rgba(8,12,24,0.78)",
                    px: 1.35,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: "rgba(191,219,254,0.92)", fontSize: "0.82rem", fontWeight: 600 }}>
                    {`Site: ${siteName}`}
                  </Typography>
                  <Button
                    size="small"
                    onClick={() => {
                      const nextParams = new URLSearchParams(searchParams);
                      nextParams.delete("site");
                      setSearchParams(nextParams, { replace: true });
                    }}
                    sx={{
                      minWidth: 0,
                      px: 1,
                      py: 0.15,
                      borderRadius: 999,
                      color: MAGIC_UI.textBright,
                      textTransform: "none",
                      fontSize: "0.76rem",
                      border: "1px solid rgba(148,163,184,0.28)",
                      "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
                    }}
                  >
                    Clear
                  </Button>
                </Box>
              ) : null}
            </Box>
          ) : null}
        </Stack>
      }
    >
      {activeTab === "patch_list" ? (
        <>
          {loadError ? (
            <Box sx={{ p: 2, color: "rgba(248,113,113,0.95)" }}>Failed to load patch audit: {loadError}</Box>
          ) : null}
          <GridShell
            sx={{
              ...PATCH_GRID_SX,
              flexGrow: 1,
              minHeight: 520,
              borderRadius: 0,
              border: "none",
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={displayRows}
              columnDefs={columnDefs}
              defaultColDef={DEFAULT_GRID_COL_DEF}
              rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
              suppressCellFocus
              pagination
              paginationPageSize={100}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              rowHeight={44}
              headerHeight={44}
              getRowId={(params) => String(params.data?.id || params.rowIndex)}
              theme={DEVICE_DETAILS_GRID_THEME}
            />
          </GridShell>
        </>
      ) : (
        <PatchPolicyTab onPendingUpdatesClick={applyPolicyPendingFilter} />
      )}
      </PageBodyFrame>
    </>
  );
}
