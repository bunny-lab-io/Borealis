import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLoaderData } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
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
  BuildCircleRounded as MaintenanceIcon,
  ContentCopyRounded as CopyIcon,
  DashboardRounded as OverviewIcon,
  DeleteSweepRounded as RemoveIcon,
  DnsRounded as NodesIcon,
  DoneRounded as CopiedIcon,
  EmergencyRounded as EmergencyIcon,
  EngineeringRounded as MaintenanceActionIcon,
  HistoryRounded as EventsIcon,
  HubRounded as ClusterIcon,
  RefreshRounded as RefreshIcon,
  StorageRounded as DatabaseIcon,
  UpdateRounded as UpdateIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry, themeQuartz } from "ag-grid-community";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { buildRowContextMenuColumnDef } from "../Grid_Row_Context_Menu_Button.jsx";
import RowContextMenu from "../Row_Context_Menu.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useUrlTabState } from "../app/hooks/useUrlTabState.js";
import {
  createRouteRequestPlan,
  getRouteErrorMessage,
  requireAdminRequest,
  rethrowIfRouteRedirect,
} from "../app/routes/routeData.js";
import {
  FIELD_CLASS,
  sanitizeSingleLineInput,
  validateInputValue,
} from "../app/utils/inputValidation.js";

const HMR_WARNING =
  "This drains all Borealis application roles from every cluster node except specified node. Use isolation for backend and frontend development. Disable Cluster-Wide Node Isolation to restore application HA and failover.";
const TABS = [
  { key: "overview", label: "Overview", Icon: OverviewIcon },
  { key: "nodes", label: "Nodes", Icon: NodesIcon },
  { key: "database", label: "Database", Icon: DatabaseIcon },
  { key: "operations", label: "Cluster Events", Icon: EventsIcon },
  { key: "maintenance", label: "Maintenance", Icon: MaintenanceIcon },
];
const TAB_KEYS = TABS.map((tab) => tab.key);
const CARD_SX = {
  p: 2.25,
  borderRadius: 3,
  border: "1px solid rgba(125,183,255,0.18)",
  background: "linear-gradient(155deg, rgba(13,20,38,0.94), rgba(7,11,26,0.9))",
  color: "#e2e8f0",
};
const NODE_NAME_PATTERN = /^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/;
const K3S_VERSION_PATTERN = /^v\d+\.\d+\.\d+\+k3s\d+$/;
const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};
const NAV_TABS_SX = {
  borderBottom: "1px solid rgba(148,163,184,0.35)",
  minHeight: NAV_TAB_HEIGHT,
  height: NAV_TAB_HEIGHT,
  flexShrink: 0,
  "& .MuiTabs-flexContainer": {
    minHeight: NAV_TAB_HEIGHT,
    height: NAV_TAB_HEIGHT,
    alignItems: "stretch",
  },
  "& .MuiTab-root": {
    color: NAV_TAB_COLORS.text,
    fontFamily: "inherit",
    fontSize: "0.8rem",
    textTransform: "none",
    fontWeight: 400,
    minHeight: NAV_TAB_HEIGHT,
    height: NAV_TAB_HEIGHT,
    opacity: 1,
    borderRadius: 1,
    py: 0.35,
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "& .MuiTab-iconWrapper": { color: NAV_TAB_COLORS.icon },
    "&:hover": { background: NAV_TAB_COLORS.hover },
    "&:active": { transform: "translateY(0.5px)" },
  },
  "& .MuiTab-root.Mui-selected": {
    color: NAV_TAB_COLORS.textActive,
    fontWeight: 600,
    background: NAV_TAB_COLORS.activeBg,
    "& .MuiTab-iconWrapper": { color: NAV_TAB_COLORS.iconActive },
    "&:hover": { background: NAV_TAB_COLORS.activeBg },
  },
};

ModuleRegistry.registerModules([AllCommunityModule]);

const clusterGridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  chromeBackgroundColor: { ref: "foregroundColor", mix: 0.07, onto: "backgroundColor" },
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});
const clusterGridThemeClassName = clusterGridTheme.themeName || "ag-theme-quartz";
const GRID_FONT_FAMILY = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const GRID_ICON_FONT_FAMILY = '"Quartz Regular"';
const GRID_DEFAULT_COL_DEF = {
  sortable: true,
  filter: true,
  resizable: true,
  suppressHeaderMenuButton: false,
};
const GRID_WRAPPER_SX = {
  width: "100%",
  flexGrow: 1,
  minHeight: 320,
  height: "100%",
  overflow: "hidden",
  borderRadius: 2,
  border: "1px solid rgba(125,183,255,0.18)",
  fontFamily: GRID_FONT_FAMILY,
  "--ag-font-family": GRID_FONT_FAMILY,
  "--ag-cell-horizontal-padding": "18px",
  "& .ag-root-wrapper": {
    minHeight: "100%",
    border: "none",
    borderRadius: 0,
    background: "transparent",
  },
  "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
    fontFamily: GRID_FONT_FAMILY,
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
  "& .ag-sort-order, & [data-ref='eSortOrder']": { display: "none !important" },
  "& .ag-row": { borderColor: "rgba(255,255,255,0.04)" },
  "& .ag-row-hover": { backgroundColor: "rgba(73,156,196,0.2) !important" },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
  "& .ag-icon": { fontFamily: GRID_ICON_FONT_FAMILY },
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
  "& .ag-center-cols-container .ag-cell.status-pill-cell .ag-cell-wrapper": {
    overflow: "visible",
  },
};
const GRID_INLINE_STYLE = {
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
const NODE_AUTO_SIZE_COLUMNS = ["node-status", "node", "ip-address", "database", "probes"];
const OPERATION_AUTO_SIZE_COLUMNS = ["operation-node", "operation-status", "operation", "timestamp"];
const CLUSTER_EVENT_PAGE_SIZE = 500;
const SENSITIVE_CLUSTER_DETAIL_KEY = /(?:authorization|cookie|password|secret|token|invite[_-]?bundle|api[_-]?key)/i;

function validIPv4(value) {
  const parts = String(value || "").split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255 && String(Number(part)) === part);
}

export async function loadClusterManagementPageData(request) {
  const progress = createRouteRequestPlan(request, 4);
  try {
    await requireAdminRequest(request, progress);
    const cluster = await progress.fetchJson("/api/server/cluster");
    let releases = { releases: [] };
    let releaseError = "";
    let events = [];
    let eventError = "";
    await Promise.all([
      (async () => {
        try {
          releases = await progress.fetchJson("/api/server/cluster/releases");
        } catch (error) {
          // Cluster state remains usable during GitHub outage.
          releaseError = getRouteErrorMessage(error, "Stable release catalog could not be loaded.");
        }
      })(),
      (async () => {
        try {
          events = await loadClusterEventHistory((path) => progress.fetchJson(path));
        } catch (error) {
          // Cluster controls remain usable if audit history is temporarily unavailable.
          eventError = getRouteErrorMessage(error, "Cluster event details could not be loaded.");
        }
      })(),
    ]);
    return { cluster, releases, releaseError, events, eventError, initialError: "" };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return { cluster: null, releases: { releases: [] }, events: [], initialError: getRouteErrorMessage(error, "Cluster state could not be loaded.") };
  } finally {
    progress.finalize();
  }
}

async function loadClusterEventHistory(fetchPage, initialAfterID = 0) {
  const events = [];
  let afterID = Number(initialAfterID || 0);
  while (true) {
    const payload = await fetchPage(`/api/server/cluster/events?after_id=${afterID}`);
    const page = Array.isArray(payload?.events) ? payload.events : [];
    events.push(...page);
    if (page.length < CLUSTER_EVENT_PAGE_SIZE) return events;
    const nextAfterID = page.reduce((maximum, event) => Math.max(maximum, Number(event?.id || 0)), afterID);
    if (nextAfterID <= afterID) return events;
    afterID = nextAfterID;
  }
}

function valueLabel(value, fallback = "—") {
  const text = String(value ?? "").trim();
  return text || fallback;
}

function titleCase(value) {
  return String(value || "")
    .trim()
    .replaceAll("_", " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

function statusPillTheme(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized.includes("failed") || normalized.includes("degraded") || normalized.includes("removed") || normalized.includes("offline")) {
    return {
      text: "#ff8a8a",
      background: "rgba(255,79,79,0.15)",
      border: "1px solid rgba(255,79,79,0.42)",
      dot: "#ff4f4f",
    };
  }
  if (normalized.includes("drained") || normalized.includes("cordoned") || normalized.includes("queued") || normalized.includes("waiting") || normalized.includes("cancel")) {
    return {
      text: "#ffb347",
      background: "rgba(255,179,71,0.16)",
      border: "1px solid rgba(255,179,71,0.45)",
      dot: "#ffb347",
    };
  }
  if (normalized.includes("active") || normalized.includes("succeeded") || normalized.includes("healthy") || normalized.includes("passed") || normalized.includes("running")) {
    return {
      text: "#00d18c",
      background: "rgba(0,209,140,0.16)",
      border: "1px solid rgba(0,209,140,0.45)",
      dot: "#00d18c",
    };
  }
  if (normalized.includes("replica")) {
    return {
      text: "#7dd3fc",
      background: "rgba(125,211,252,0.16)",
      border: "1px solid rgba(125,211,252,0.45)",
      dot: "#7dd3fc",
    };
  }
  return {
    text: "#e2e6f0",
    background: "rgba(226,230,240,0.12)",
    border: "1px solid rgba(226,230,240,0.25)",
    dot: "#e2e6f0",
  };
}

function StatusPill({ value }) {
  const label = valueLabel(value, "Unknown");
  const theme = statusPillTheme(label);
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
        fontFamily: GRID_FONT_FAMILY,
        gap: 0.75,
        whiteSpace: "nowrap",
      }}
    >
      <Box
        component="span"
        sx={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          backgroundColor: theme.dot,
          boxShadow: "0 0 0 2px rgba(0,0,0,0.22)",
          flexShrink: 0,
        }}
      />
      {label}
    </Box>
  );
}

function operatorNodeLabel(value, nodes) {
  const id = String(value || "").trim();
  return nodes.find((node) => node?.id === id)?.node_name || value;
}

function operationErrorSummary(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const httpMatch = text.match(/^(.*?returned HTTP \d+)/);
  const summary = httpMatch?.[1] || text.split("\n", 1)[0];
  return summary.length > 220 ? `${summary.slice(0, 217)}...` : summary;
}

function KeyValueGrid({ entries }) {
  return (
    <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(3, minmax(0, 1fr))" }, gap: 1.5 }}>
      {entries.map(([label, value]) => (
        <Paper key={label} sx={CARD_SX}>
          <Typography variant="caption" sx={{ color: "#94a3b8", textTransform: "uppercase", letterSpacing: 0.8 }}>{label}</Typography>
          <Typography sx={{ mt: 0.75, fontWeight: 650, overflowWrap: "anywhere" }}>{valueLabel(value)}</Typography>
        </Paper>
      ))}
    </Box>
  );
}

export function clusterNodeStatusLabel(node) {
  const membership = titleCase(valueLabel(node?.membership_state, "Unknown"));
  const application = node?.cordoned === true || node?.unschedulable === true
    ? "Cordoned"
    : titleCase(valueLabel(node?.application_state, "Unknown"));
  return `${membership} / ${application}`;
}

export function clusterNodeRolesPresentation(node) {
  const roles = node?.roles || {};
  const roleLabels = {
    control_vip_owner: "Control VIP Owner",
    edge_vip_owner: "Edge VIP Owner",
    etcd_leader: "etcd Leader",
    scheduler_leader: "Scheduler Leader",
    wireguard_owner: "WireGuard Owner",
  };
  const ownershipLabels = Object.entries(roles)
    .filter(([role, active]) => !["k3s_version", "postgres_primary"].includes(role) && active === true)
    .map(([role]) => roleLabels[role] || titleCase(role));
  return {
    ownershipLabels,
    label: ownershipLabels.length ? ownershipLabels.join(", ") : "Standby",
  };
}

export function clusterNodeDatabaseStatus(node, postgresPrimaryNodeID = "", database = {}, activeMemberCount = 0) {
  const roles = node?.roles || {};
  const activeMember = String(node?.membership_state || "").toLowerCase() === "active";
  if (!activeMember) return "Not Active";

  const primaryID = String(postgresPrimaryNodeID || "").trim();
  const primaryNode = (primaryID !== "" && primaryID === String(node?.id || "").trim())
    || roles?.postgres_primary === true;
  if (primaryNode) return "Active";

  const configuredInstances = Number(database?.configured_instances);
  const readyInstances = Number(database?.ready_instances);
  const expectedInstances = Number(activeMemberCount);
  const completeReplicaSet = Number.isFinite(configuredInstances)
    && Number.isFinite(readyInstances)
    && Number.isFinite(expectedInstances)
    && expectedInstances > 0
    && configuredInstances === expectedInstances
    && readyInstances === configuredInstances
    && database?.fully_ready !== false;
  return completeReplicaSet ? "Replica Healthy" : "Not Ready";
}

function ClusterNodeDatabaseCell({ status, onOpenDatabase }) {
  if (status !== "Active") return status || "Not Ready";
  return (
    <Box
      component="button"
      type="button"
      onClick={(event) => {
        event.stopPropagation();
        onOpenDatabase();
      }}
      sx={{
        appearance: "none",
        background: "none",
        border: 0,
        color: "#7dd3fc",
        cursor: "pointer",
        font: "inherit",
        fontWeight: 600,
        p: 0,
        textDecoration: "none",
        "&:hover, &:focus-visible": { color: "#bae6fd", textDecoration: "none" },
      }}
    >
      Active
    </Box>
  );
}

function clusterNodeProbeSummary(node) {
  const entries = Object.entries(node?.probe_health || {});
  if (!entries.length) return { label: "Not Reported", detail: "No probe results reported." };
  const passed = entries.filter(([, status]) => String(status || "").toLowerCase() === "passed").length;
  return {
    label: `${passed}/${entries.length} Passed`,
    detail: entries.map(([name, status]) => `${titleCase(name)}: ${titleCase(status)}`).join(" · "),
  };
}

export function friendlyClusterOperationName(operation) {
  const kind = String(operation?.kind || "").trim().toLowerCase();
  const payload = operation?.payload || {};
  const labels = {
    cluster_enable: "Cluster Mode Enabled",
    membership_scale: "Cluster Nodes Added",
    membership_admit: "Cluster Node Added",
    postgres_switchover: "PostgreSQL Primary Switched",
    postgres_emergency_failover: "PostgreSQL Emergency Failover",
    hmr_start: "Cluster-Wide Node Isolation Enabled",
    hmr_exit: "Cluster-Wide Node Isolation Disabled",
    hmr_recover: "Cluster-Wide Node Isolation Recovered",
    engine_update: payload?.scope === "node" ? "Engine Node Updated" : "Engine Cluster Updated",
    k3s_update: "K3s Servers Updated",
  };
  if (kind === "node_maintenance") {
    return String(payload?.action || "").toLowerCase() === "exit"
      ? "Maintenance Mode Disabled"
      : "Maintenance Mode Enabled";
  }
  if (kind === "node_remove") {
    return payload?.emergency === true ? "Node Emergency Removed" : "Node Pair Removed";
  }
  return labels[kind] || titleCase(kind || "Cluster Operation");
}

export function formatClusterTimestamp(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "—";
  const date = new Date(numeric < 1_000_000_000_000 ? numeric * 1000 : numeric);
  if (Number.isNaN(date.getTime())) return "—";
  const pad = (part) => String(part).padStart(2, "0");
  return `${pad(date.getDate())}/${pad(date.getMonth() + 1)}/${date.getFullYear()} @ ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function redactClusterDetail(value, key = "") {
  if (SENSITIVE_CLUSTER_DETAIL_KEY.test(String(key || ""))) return "[redacted]";
  if (Array.isArray(value)) return value.map((item) => redactClusterDetail(item));
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([itemKey, itemValue]) => [itemKey, redactClusterDetail(itemValue, itemKey)]));
  }
  return value;
}

function clusterDetailJSON(value) {
  if (!value || typeof value !== "object" || Object.keys(value).length === 0) return "";
  return JSON.stringify(redactClusterDetail(value), null, 2);
}

function friendlyClusterEventName(value) {
  const eventType = String(value || "").trim().toLowerCase();
  const labels = {
    admission_pending: "Node Admission Pending",
    admission_pair_approved: "Node Pair Approved",
    operation_started: "Operation Started",
    operation_step_passed: "Operation Step Passed",
    operation_failed: "Operation Failed",
    operation_retry: "Operation Retried",
    operation_succeeded: "Operation Succeeded",
    operation_cancelled: "Operation Cancelled",
  };
  return labels[eventType] || titleCase(eventType || "Cluster Event");
}

function clusterOperationStatusLabel(operation) {
  if (String(operation?.superseded_by || "").trim()) return "Superseded";
  const state = String(operation?.state || "unknown").toLowerCase();
  return state === "running" ? "In Progress" : titleCase(state);
}

export function clusterOperationNodeLabel(operation, nodes = [], admissions = [], events = []) {
  const names = [];
  const seen = new Set();
  const nodeByID = new Map(nodes.map((node) => [String(node?.id || "").toLowerCase(), String(node?.node_name || "").trim()]));
  const admissionByID = new Map(admissions.map((admission) => [String(admission?.id || "").toLowerCase(), String(admission?.node_name || "").trim()]));
  const addName = (value) => {
    const name = String(value || "").trim();
    const key = name.toLowerCase();
    if (!name || seen.has(key)) return;
    seen.add(key);
    names.push(name);
  };
  const resolveID = (value) => {
    const id = String(value || "").trim().toLowerCase();
    const name = nodeByID.get(id) || admissionByID.get(id);
    if (name) addName(name);
  };
  const inspect = (value, key = "") => {
    if (Array.isArray(value)) {
      value.forEach((item) => inspect(item, key));
      return;
    }
    if (value && typeof value === "object") {
      Object.entries(value).forEach(([itemKey, itemValue]) => inspect(itemValue, itemKey));
      return;
    }
    const normalizedKey = String(key || "").toLowerCase();
    if (normalizedKey === "node_name" || normalizedKey === "node_names") {
      addName(value);
      return;
    }
    if (normalizedKey.includes("node_id") || normalizedKey.includes("admission_id")) resolveID(value);
    const uuidMatches = String(value || "").match(/[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/gi) || [];
    uuidMatches.forEach(resolveID);
  };

  resolveID(operation?.target_node_id);
  inspect(operation?.payload || {});
  events.forEach((event) => {
    resolveID(event?.admission_id);
    inspect(event?.details || {});
  });
  return names.length ? names.join(", ") : "Cluster-wide";
}

export function buildClusterOperationDetails(operation, events = [], nodeLabel = "Cluster-wide") {
  const sortedEvents = [...events].sort((left, right) => Number(left?.id || 0) - Number(right?.id || 0));
  const latestMessage = [...sortedEvents].reverse().find((event) => String(event?.message || "").trim())?.message || "";
  const currentStep = String(operation?.current_step || "").trim();
  const errorSummary = operationErrorSummary(operation?.error);
  const summary = errorSummary
    || String(latestMessage).trim()
    || (currentStep && currentStep !== "complete" ? `Current step: ${titleCase(currentStep)}` : "No detailed lifecycle events recorded.");
  const lines = [
    `Operation: ${friendlyClusterOperationName(operation)}`,
    `Node: ${nodeLabel}`,
    `Status: ${clusterOperationStatusLabel(operation)}`,
    `Operation ID: ${valueLabel(operation?.id)}`,
    `Current step: ${currentStep ? `${titleCase(currentStep)} (${currentStep})` : "—"}`,
    `Attempt: ${valueLabel(operation?.attempt)}`,
    `Requested by: ${valueLabel(operation?.requested_by)}`,
    operation?.target_release ? `Target release: ${operation.target_release}` : "",
    operation?.target_sha ? `Target SHA: ${operation.target_sha}` : "",
    `Created: ${formatClusterTimestamp(operation?.created_at)}`,
    operation?.started_at ? `Started: ${formatClusterTimestamp(operation.started_at)}` : "",
    operation?.updated_at ? `Updated: ${formatClusterTimestamp(operation.updated_at)}` : "",
    operation?.finished_at ? `Finished: ${formatClusterTimestamp(operation.finished_at)}` : "",
    operation?.error ? `Error: ${operation.error}` : "",
  ].filter(Boolean);
  const payloadJSON = clusterDetailJSON(operation?.payload);
  if (payloadJSON) lines.push("", "Operation payload:", payloadJSON);
  if (sortedEvents.length) {
    lines.push("", "Lifecycle events:");
    sortedEvents.forEach((event) => {
      const rawType = String(event?.event_type || "cluster_event");
      const state = titleCase(event?.state || "unknown");
      lines.push("", `#${valueLabel(event?.id)} · ${formatClusterTimestamp(event?.created_at)} · ${friendlyClusterEventName(rawType)} (${rawType}) · ${state}`);
      if (event?.message) lines.push(`Message: ${event.message}`);
      const detailsJSON = clusterDetailJSON(event?.details);
      if (detailsJSON) lines.push("Details:", detailsJSON);
    });
  }
  return { summary, copyText: lines.join("\n") };
}

async function copyClusterText(text) {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  if (typeof document === "undefined") throw new Error("Clipboard unavailable.");
  const input = document.createElement("textarea");
  input.value = text;
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.focus();
  input.select();
  document.execCommand("copy");
  input.remove();
}

function OperationDetailsCell({ data }) {
  const [copied, setCopied] = useState(false);
  const resetTimerRef = useRef(null);
  useEffect(() => () => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
  }, []);
  const copyDetails = async (event) => {
    event.preventDefault();
    event.stopPropagation();
    try {
      await copyClusterText(String(data?.detailsText || ""));
      setCopied(true);
      if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
      resetTimerRef.current = window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setCopied(false);
    }
  };
  return (
    <Box sx={{ display: "flex", alignItems: "center", width: "100%", minWidth: 0, gap: 1 }}>
      <Tooltip title={data?.detailSummary || ""} placement="top-start" arrow>
        <Typography component="span" sx={{ flex: 1, minWidth: 0, color: "#cbd5e1", fontSize: "inherit", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {data?.detailSummary || "No details recorded."}
        </Typography>
      </Tooltip>
      <Tooltip title={copied ? "Copied" : "Copy full event details"} arrow>
        <IconButton
          size="small"
          aria-label={`Copy details for ${data?.operationLabel || "cluster operation"}`}
          onMouseDown={(event) => event.stopPropagation()}
          onClick={copyDetails}
          sx={{ p: 0.4, flexShrink: 0, color: copied ? "#00d18c" : "#8fbfff" }}
        >
          {copied ? <CopiedIcon sx={{ fontSize: 17 }} /> : <CopyIcon sx={{ fontSize: 17 }} />}
        </IconButton>
      </Tooltip>
    </Box>
  );
}

function ClusterGrid({ rowData, columnDefs, autoSizeColumnIds, getRowId, onCellContextMenu, rowSelection }) {
  const gridApiRef = useRef(null);
  const autoSizeColumns = useCallback(() => {
    if (!gridApiRef.current || !rowData.length || !autoSizeColumnIds.length) return;
    const schedule = typeof window.requestAnimationFrame === "function"
      ? window.requestAnimationFrame
      : (callback) => window.setTimeout(callback, 0);
    schedule(() => {
      try {
        const api = gridApiRef.current;
        if (!api || api.isDestroyed?.()) return;
        api.autoSizeColumns(autoSizeColumnIds, true);
      } catch {
        // Grid may be leaving current tab while scheduled sizing runs.
      }
    });
  }, [autoSizeColumnIds, rowData.length]);

  useEffect(() => {
    autoSizeColumns();
  }, [autoSizeColumns, rowData]);

  return (
    <Box className={clusterGridThemeClassName} sx={GRID_WRAPPER_SX} style={GRID_INLINE_STYLE}>
      <AgGridReact
        rowData={rowData}
        columnDefs={columnDefs}
        defaultColDef={GRID_DEFAULT_COL_DEF}
        rowSelection={rowSelection}
        suppressCellFocus
        suppressContextMenu
        preventDefaultOnContextMenu
        pagination
        paginationPageSize={20}
        paginationPageSizeSelector={[20, 50, 100]}
        rowHeight={44}
        headerHeight={44}
        animateRows
        onGridReady={(params) => {
          gridApiRef.current = params.api;
          autoSizeColumns();
        }}
        onGridPreDestroyed={() => {
          gridApiRef.current = null;
        }}
        onFirstDataRendered={autoSizeColumns}
        onRowDataUpdated={autoSizeColumns}
        onCellContextMenu={onCellContextMenu}
        getRowId={getRowId}
        overlayNoRowsTemplate="No cluster records"
        theme={clusterGridTheme}
      />
    </Box>
  );
}

export default function ClusterManagement() {
  const loaderData = useLoaderData();
  const notify = useAppNotifications({ title: "Cluster Management", icon: "cluster" });
  const [cluster, setCluster] = useState(loaderData?.cluster || null);
  const [releases, setReleases] = useState(loaderData?.releases?.releases || []);
  const [releaseError, setReleaseError] = useState(loaderData?.releaseError || "");
  const [events, setEvents] = useState(Array.isArray(loaderData?.events) ? loaderData.events : []);
  const [eventError, setEventError] = useState(loaderData?.eventError || "");
  const eventCursorRef = useRef((Array.isArray(loaderData?.events) ? loaderData.events : []).reduce(
    (maximum, event) => Math.max(maximum, Number(event?.id || 0)),
    0
  ));
  const { activeKey: tab, setActiveKey: setTab } = useUrlTabState({
    defaultKey: "overview",
    allowedKeys: TAB_KEYS,
  });
  const [error, setError] = useState(loaderData?.initialError || "");
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState(null);
  const [nodeActionMenu, setNodeActionMenu] = useState({ open: false, top: 0, left: 0, node: null });
  const [confirmation, setConfirmation] = useState("");
  const [reason, setReason] = useState("");
  const [selectedRelease, setSelectedRelease] = useState("");
  const [selectedNode, setSelectedNode] = useState("");
  const [controlVIP, setControlVIP] = useState("");
  const [edgeVIP, setEdgeVIP] = useState("");
  const [nodeName, setNodeName] = useState("");
  const [managementIP, setManagementIP] = useState("");
  const [architecture, setArchitecture] = useState("amd64");
  const [desiredSize, setDesiredSize] = useState(3);
  const [inviteBundle, setInviteBundle] = useState("");
  const [pairedNode, setPairedNode] = useState("");
  const [fencingConfirmation, setFencingConfirmation] = useState("");
  const [k3sTargetVersion, setK3sTargetVersion] = useState("");

  const nodes = useMemo(() => Array.isArray(cluster?.nodes) ? cluster.nodes : [], [cluster]);
  const operations = useMemo(() => Array.isArray(cluster?.operations) ? cluster.operations : [], [cluster]);
  const admissions = useMemo(() => Array.isArray(cluster?.admissions) ? cluster.admissions : [], [cluster]);
  const selectableReleases = useMemo(() => releases.filter((release) => release?.selectable), [releases]);
  const activeSize = Number(cluster?.active_size || 1);
  const desiredMembershipSize = Number(cluster?.desired_size || activeSize);
  const database = cluster?.database || {};
  const hmrState = String(cluster?.hmr?.state || "inactive").toLowerCase();
  const isolationInactive = hmrState === "inactive";
  const isolationExitAllowed = ["active", "restore_failed"].includes(hmrState);
  const isolatedNodeID = String(cluster?.hmr?.node_id || "");
  const isolationNodeOptions = isolationInactive ? nodes : nodes.filter((node) => node?.id === isolatedNodeID);
  const isolationNodeValue = isolationInactive ? selectedNode : isolatedNodeID;
  const databaseRecoveryReady = database?.fully_ready !== false && database?.durability_quorum !== false;
  const applicationCapacityReady = nodes.filter((node) => node?.membership_state === "Active").every((node) => node?.application_state === "active");
  const normalOperationsEnabled = cluster?.status === "Healthy" && databaseRecoveryReady && applicationCapacityReady;
  const rollingUpdateEnabled = (normalOperationsEnabled || cluster?.status === "Mixed Version") && databaseRecoveryReady && applicationCapacityReady;
  const replacementRecovery = activeSize === 2 && desiredMembershipSize === 3 && cluster?.status === "Degraded Quorum";
  const canPrepareMembership = (activeSize === 1 || replacementRecovery) && applicationCapacityReady;
  const canExpandToThree = activeSize === 1;
  const expansionSizes = useMemo(() => Number(cluster?.active_size || 1) === 1 ? [3] : [], [cluster?.active_size]);

  const refresh = useCallback(async ({ quiet = false } = {}) => {
    try {
      const eventRequest = loadClusterEventHistory(async (path) => {
        const response = await fetch(path, { credentials: "include", cache: "no-store" });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload?.message || "Cluster event details request failed.");
        return payload;
      }, eventCursorRef.current)
        .then((items) => ({ items, error: "" }))
        .catch((requestError) => ({ items: null, error: requestError?.message || "Cluster event details could not be loaded." }));
      const [clusterResponse, releaseResponse, eventResult] = await Promise.all([
        fetch("/api/server/cluster", { credentials: "include", cache: "no-store" }),
        fetch("/api/server/cluster/releases", { credentials: "include", cache: "no-store" }),
        eventRequest,
      ]);
      const clusterPayload = await clusterResponse.json().catch(() => ({}));
      const releasePayload = await releaseResponse.json().catch(() => ({}));
      if (!clusterResponse.ok) throw new Error(clusterPayload?.message || "Cluster state request failed.");
      setCluster(clusterPayload);
      if (releaseResponse.ok) {
        setReleases(Array.isArray(releasePayload?.releases) ? releasePayload.releases : []);
        setReleaseError("");
      } else {
        setReleaseError(releasePayload?.message || "Stable release catalog could not be loaded.");
      }
      if (eventResult.items) {
        if (eventResult.items.length) {
          setEvents((current) => {
            const merged = new Map(current.map((event) => [Number(event?.id || 0), event]));
            eventResult.items.forEach((event) => merged.set(Number(event?.id || 0), event));
            return [...merged.values()].sort((left, right) => Number(left?.id || 0) - Number(right?.id || 0));
          });
          eventCursorRef.current = eventResult.items.reduce(
            (maximum, event) => Math.max(maximum, Number(event?.id || 0)),
            eventCursorRef.current
          );
        }
        setEventError("");
      } else {
        setEventError(eventResult.error);
      }
      setError("");
      if (!quiet) void notify("Cluster state refreshed.", { variant: "success" });
    } catch (requestError) {
      if (!quiet) setError(requestError?.message || "Cluster state request failed.");
    }
  }, [notify]);

  useEffect(() => {
    const timer = window.setInterval(() => void refresh({ quiet: true }), 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const mutate = useCallback(async (path, body) => {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(path, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        const validation = Array.isArray(payload?.errors) ? payload.errors.map((item) => item?.message || item).join(" ") : "";
        throw new Error(validation || payload?.message || payload?.error || `Request failed (${response.status}).`);
      }
      setDialog(null);
      setConfirmation("");
      setReason("");
      setPairedNode("");
      setFencingConfirmation("");
      await refresh({ quiet: true });
      void notify(`Cluster operation ${payload?.operation_id || "request"} queued.`, { variant: "success" });
      return payload;
    } catch (requestError) {
      setError(requestError?.message || "Cluster operation failed.");
    } finally {
      setBusy(false);
    }
  }, [notify, refresh]);

  const openAction = useCallback((kind, node = null) => {
    setDialog({ kind, node });
    setConfirmation("");
    setReason("");
    setFencingConfirmation("");
    if (node?.id) setSelectedNode(node.id);
    if (kind === "remove" && node?.id) setPairedNode(nodes.find((candidate) => candidate.id !== node.id && candidate.membership_state === "Active")?.id || "");
  }, [nodes]);

  const submitDialog = useCallback(() => {
    const kind = dialog?.kind;
    const node = dialog?.node;
    const sanitizedReason = sanitizeSingleLineInput(reason).slice(0, 256);
    const maintenanceDisablesIsolation = kind === "maintenance"
      && node?.application_state === "drained"
      && ["active", "restore_failed"].includes(String(cluster?.hmr?.state || "inactive").toLowerCase());
    if (maintenanceDisablesIsolation) return mutate("/api/server/cluster/hmr/exit", { confirmation });
    if (["maintenance", "scale", "remove", "emergency_remove", "switchover", "emergency_failover"].includes(kind)) {
      const validation = validateInputValue("reason", sanitizedReason, FIELD_CLASS.PLAIN_SINGLE_LINE);
      if (validation) return setError(validation);
    }
    if (kind === "hmr_start") return mutate("/api/server/cluster/hmr/start", { node_id: selectedNode, confirmation });
    if (kind === "hmr_exit") return mutate("/api/server/cluster/hmr/exit", { confirmation });
    if (kind === "update_all" || kind === "update_node") {
      const oneNode = Number(cluster?.active_size || 1) === 1;
      return mutate("/api/server/cluster/updates", {
        scope: kind === "update_all" ? "all" : "node",
        node_ids: kind === "update_all" ? [] : [selectedNode],
        release_tag: selectedRelease,
        confirmation,
        maintenance_outage_acknowledgement: oneNode ? "ACCEPT OUTAGE" : "",
      });
    }
    if (kind === "k3s_update") {
      if (!K3S_VERSION_PATTERN.test(k3sTargetVersion) || k3sTargetVersion.length > 32) return setError("K3s target must use stable vX.Y.Z+k3sN form and be no longer than 32 characters.");
      const oneNode = Number(cluster?.active_size || 1) === 1;
      return mutate("/api/server/cluster/updates", {
        update_type: "k3s",
        scope: "all",
        node_ids: [],
        k3s_version: k3sTargetVersion,
        confirmation,
        maintenance_outage_acknowledgement: oneNode ? "ACCEPT OUTAGE" : "",
      });
    }
    if (kind === "maintenance") {
      const action = node?.application_state === "drained" ? "exit" : "enter";
      return mutate(`/api/server/cluster/nodes/${node.id}/maintenance`, { action, reason: sanitizedReason });
    }
    if (kind === "cluster_enable") {
      if (!validIPv4(controlVIP) || !validIPv4(edgeVIP) || controlVIP === edgeVIP || !validIPv4(managementIP)) return setError("Distinct valid IPv4 control-plane and edge VIPs plus valid management IPv4 required.");
      if (!NODE_NAME_PATTERN.test(nodeName)) return setError("Node name must be DNS-label syntax and no longer than 63 characters.");
      return mutate("/api/server/cluster/enable", { control_plane_vip: controlVIP, edge_vip: edgeVIP, node_name: nodeName, management_ip: managementIP, architecture, confirmation });
    }
    if (kind === "invite") {
      if (!canPrepareMembership) return setError("Current release supports node invitations only for one-to-three expansion or degraded-quorum replacement.");
      if (!NODE_NAME_PATTERN.test(nodeName)) return setError("Node name must be DNS-label syntax and no longer than 63 characters.");
      return mutate("/api/server/cluster/invitations", { node_name: nodeName }).then((payload) => setInviteBundle(payload?.invite_bundle || ""));
    }
    if (kind === "scale") {
      if (!canExpandToThree || Number(desiredSize) !== 3) return setError("Current release supports only one-to-three membership expansion.");
      return mutate("/api/server/cluster/membership/scale", { desired_size: 3, reason: sanitizedReason });
    }
    if (kind === "remove") return mutate(`/api/server/cluster/nodes/${node.id}/remove`, { emergency: false, paired_node_id: pairedNode, confirmation, reason: sanitizedReason });
    if (kind === "emergency_remove") return mutate(`/api/server/cluster/nodes/${node.id}/remove`, { emergency: true, confirmation, fencing_confirmation: fencingConfirmation, reason: sanitizedReason });
    if (kind === "switchover") return mutate("/api/server/cluster/postgres/switchover", { target_node_id: selectedNode, confirmation: "", reason: sanitizedReason });
    if (kind === "emergency_failover") return mutate("/api/server/cluster/postgres/emergency-failover", { target_node_id: selectedNode, confirmation, reason: sanitizedReason });
    return undefined;
  }, [architecture, canExpandToThree, canPrepareMembership, cluster?.active_size, cluster?.hmr?.state, confirmation, controlVIP, desiredSize, dialog, edgeVIP, fencingConfirmation, k3sTargetVersion, managementIP, mutate, nodeName, pairedNode, reason, selectedNode, selectedRelease]);

  const nodeRows = useMemo(
    () => {
      const activeMemberCount = nodes.filter((member) => String(member?.membership_state || "").toLowerCase() === "active").length;
      return nodes.map((node) => {
        const rolesPresentation = clusterNodeRolesPresentation(node);
        return {
          ...node,
          statusLabel: clusterNodeStatusLabel(node),
          databaseStatus: clusterNodeDatabaseStatus(node, cluster?.leaders?.postgres_primary, database, activeMemberCount),
          rolesLabel: rolesPresentation.label,
          probeSummary: clusterNodeProbeSummary(node),
        };
      });
    },
    [cluster?.leaders?.postgres_primary, database, nodes]
  );

  const operationRows = useMemo(
    () => {
      const eventsByOperation = new Map();
      events.forEach((event) => {
        const operationID = String(event?.operation_id || "").trim();
        if (!operationID) return;
        const linkedEvents = eventsByOperation.get(operationID) || [];
        linkedEvents.push(event);
        eventsByOperation.set(operationID, linkedEvents);
      });
      return operations.map((operation) => {
        const linkedEvents = eventsByOperation.get(String(operation?.id || "")) || [];
        const nodeLabel = clusterOperationNodeLabel(operation, nodes, admissions, linkedEvents);
        const details = buildClusterOperationDetails(operation, linkedEvents, nodeLabel);
        const timestamp = Number(operation?.created_at || operation?.started_at || operation?.updated_at || operation?.finished_at || 0);
        return {
          ...operation,
          operationLabel: friendlyClusterOperationName(operation),
          nodeLabel,
          detailSummary: details.summary,
          detailsText: details.copyText,
          timestamp,
          timestampLabel: formatClusterTimestamp(timestamp),
        };
      });
    },
    [admissions, events, nodes, operations]
  );

  const closeNodeActionMenu = useCallback(() => {
    setNodeActionMenu({ open: false, top: 0, left: 0, node: null });
  }, []);

  const openNodeActionMenu = useCallback((event, node, gridNode) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    if (!node) return;
    if (gridNode && !gridNode.isSelected?.()) gridNode.setSelected?.(true, true);
    setNodeActionMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      node,
    });
  }, []);

  const nodeActionTarget = nodeActionMenu.node;
  const nodeActionMenuActions = useMemo(() => {
    if (!nodeActionTarget) return [];
    const isDrained = String(nodeActionTarget?.application_state || "").toLowerCase() === "drained";
    const membershipActive = nodeActionTarget?.membership_state === "Active";
    const maintenanceDisabled = busy || (isDrained && !isolationInactive && !isolationExitAllowed) || (!normalOperationsEnabled && !isDrained);
    const updateDisabled = busy || !normalOperationsEnabled;
    const removalDisabled = busy || !normalOperationsEnabled || activeSize !== 3 || !membershipActive;
    const emergencyDisabled = busy || activeSize !== 3 || !membershipActive;
    return [
      {
        id: "maintenance",
        label: isDrained ? "Exit Maintenance Mode" : "Enter Maintenance Mode",
        icon: MaintenanceActionIcon,
        group: "primary",
        disabled: maintenanceDisabled,
        description: isDrained ? "Return node to production use" : "Drain active roles to other nodes",
        onClick: () => openAction("maintenance", nodeActionTarget),
      },
      {
        id: "update-node",
        label: "Update Node",
        icon: UpdateIcon,
        group: "organize",
        disabled: updateDisabled,
        description: "Install selected Engine release",
        onClick: () => openAction("update_node", nodeActionTarget),
      },
      {
        id: "remove-pair",
        label: "Remove Pair",
        icon: RemoveIcon,
        group: "danger",
        intent: "danger",
        disabled: removalDisabled,
        description: "Safely remove two cluster nodes",
        onClick: () => openAction("remove", nodeActionTarget),
      },
      {
        id: "emergency-remove",
        label: "Emergency Remove",
        icon: EmergencyIcon,
        group: "danger",
        intent: "danger",
        disabled: emergencyDisabled,
        description: "Remove externally fenced node",
        onClick: () => openAction("emergency_remove", nodeActionTarget),
      },
    ];
  }, [activeSize, busy, isolationExitAllowed, isolationInactive, nodeActionTarget, normalOperationsEnabled, openAction]);

  const nodeColumnDefs = useMemo(() => [
    {
      colId: "node-status",
      field: "statusLabel",
      headerName: "Status",
      cellClass: "auto-col-tight status-pill-cell",
      cellRenderer: (params) => <StatusPill value={params.value} />,
    },
    {
      colId: "node",
      field: "node_name",
      headerName: "Node",
      minWidth: 180,
      cellClass: "auto-col-tight",
    },
    {
      colId: "ip-address",
      field: "management_ip",
      headerName: "IP Address",
      minWidth: 140,
      cellClass: "auto-col-tight",
    },
    {
      colId: "database",
      field: "databaseStatus",
      headerName: "Database",
      minWidth: 120,
      cellClass: "auto-col-tight",
      cellRenderer: (params) => <ClusterNodeDatabaseCell status={params.value} onOpenDatabase={() => setTab("database")} />,
    },
    {
      colId: "roles",
      field: "rolesLabel",
      headerName: "Roles",
      minWidth: 150,
      flex: 1,
      cellClass: "auto-col-tight",
    },
    {
      colId: "probes",
      field: "probeSummary.label",
      headerName: "Probes",
      minWidth: 180,
      cellClass: "auto-col-tight",
      cellRenderer: (params) => (
        <Tooltip title={params?.data?.probeSummary?.detail || "No probe results reported."} placement="top-start" arrow>
          <Box component="span" sx={{ color: "#cbd5e1", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
            {params?.data?.probeSummary?.label || "Not Reported"}
          </Box>
        </Tooltip>
      ),
    },
    buildRowContextMenuColumnDef(openNodeActionMenu, {
      headerName: "Actions",
      width: 96,
      tooltip: (node) => `${valueLabel(node?.node_name, "Node")} Actions`,
    }),
  ], [openNodeActionMenu, setTab]);

  const operationColumnDefs = useMemo(() => [
    {
      colId: "operation-node",
      field: "nodeLabel",
      headerName: "Node",
      minWidth: 180,
      cellClass: "auto-col-tight",
      tooltipField: "nodeLabel",
    },
    {
      colId: "operation-status",
      field: "state",
      headerName: "Status",
      minWidth: 150,
      sortable: true,
      filter: true,
      cellClass: "auto-col-tight status-pill-cell",
      cellRenderer: (params) => {
        const operation = params?.data || {};
        const state = String(operation?.state || "unknown").toLowerCase();
        const label = clusterOperationStatusLabel(operation);
        const cancellable = ["queued", "waiting"].includes(state);
        return (
          <Box sx={{ display: "flex", alignItems: "center", gap: 0.8, minWidth: 0 }}>
            <StatusPill value={label} />
            {cancellable ? (
              <>
                <Typography component="span" sx={{ color: "#64748b" }}>/</Typography>
                <Button
                  size="small"
                  color="warning"
                  disabled={busy}
                  onClick={(event) => {
                    event.stopPropagation();
                    void mutate(`/api/server/cluster/operations/${operation.id}/cancel`, { confirmation: "CANCEL OPERATION" });
                  }}
                  sx={{ minWidth: 0, px: 0.75, textTransform: "none" }}
                >
                  Cancel
                </Button>
              </>
            ) : null}
          </Box>
        );
      },
    },
    {
      colId: "operation",
      field: "operationLabel",
      headerName: "Operation",
      minWidth: 240,
      cellClass: "auto-col-tight",
      cellRenderer: (params) => (
        <Box component="span" sx={{ color: "#f4f7ff", fontWeight: 550, whiteSpace: "nowrap" }}>
          {params.value}
        </Box>
      ),
    },
    {
      colId: "operation-details",
      field: "detailSummary",
      headerName: "Details",
      minWidth: 320,
      flex: 1,
      sortable: false,
      filter: true,
      cellRenderer: OperationDetailsCell,
    },
    {
      colId: "timestamp",
      field: "timestamp",
      headerName: "Timestamp",
      minWidth: 210,
      cellClass: "auto-col-tight",
      cellRenderer: (params) => params?.data?.timestampLabel || "—",
      comparator: (left, right) => Number(left || 0) - Number(right || 0),
      sort: "desc",
    },
  ], [busy, mutate]);

  const pageActions = useMemo(() => [{ id: "cluster-refresh", label: "Refresh", icon: <RefreshIcon />, tone: "secondary", onClick: () => void refresh() }], [refresh]);
  useRoutePageChrome({
    title: "Cluster Management",
    subtitle: "Quorum, role ownership, development node isolation, node maintenance, and rolling Engine release operations.",
    Icon: ClusterIcon,
    actions: pageActions,
  });

  const leaders = cluster?.leaders || {};
  const configuredDatabaseInstances = Number(database?.configured_instances || activeSize);
  const databaseReadyObserved = database?.ready_instances !== undefined && database?.ready_instances !== null;
  const readyDatabaseInstances = databaseReadyObserved ? Number(database.ready_instances) : null;
  const databaseFullyReady = database?.fully_ready !== false;
  const databaseDurabilityReady = database?.durability_quorum !== false;
  const maintenanceExitDisablesIsolation = dialog?.kind === "maintenance"
    && dialog?.node?.application_state === "drained"
    && isolationExitAllowed;
  const owner = (value) => operatorNodeLabel(value, nodes);
  return (
    <PageBodyFrame>
      <Stack spacing={2.25} sx={{ p: { xs: 1.5, md: 2.5 }, flexGrow: 1, minHeight: 0 }}>
        {error ? <Alert severity="error" onClose={() => setError("")}>{error}</Alert> : null}
        {cluster?.status === "Degraded Quorum" ? <Alert severity="error">Cluster degraded. Failed node stays drained; retry or explicit recovery required.</Alert> : null}
        {cluster?.status === "Degraded Database" ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>PostgreSQL is not fully ready: {readyDatabaseInstances ?? "unknown"} of {configuredDatabaseInstances} instances Ready. Normal cluster-changing operations stay blocked until redundancy recovers; recovery controls remain available.</Alert> : null}
        {!databaseRecoveryReady && cluster?.status !== "Degraded Database" ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>PostgreSQL recovery remains required even while cluster lifecycle status is {valueLabel(cluster?.status)}. Normal cluster-changing operations stay blocked.</Alert> : null}
        {!applicationCapacityReady ? <Alert severity="warning">One or more active cluster members remain application-drained. Restore drained node or finish explicit recovery before starting normal cluster operations.</Alert> : null}
        <Tabs
          value={tab}
          onChange={(_, value) => setTab(value)}
          variant="scrollable"
          scrollButtons="auto"
          aria-label="Cluster Management sections"
          TabIndicatorProps={{
            style: {
              height: 3,
              borderRadius: 3,
              background: NAV_TAB_COLORS.iconActive,
            },
          }}
          sx={NAV_TABS_SX}
        >
          {TABS.map(({ key, label, Icon }) => (
            <Tab key={key} value={key} label={label} icon={<Icon fontSize="small" />} iconPosition="start" />
          ))}
        </Tabs>

        {tab === "overview" ? <KeyValueGrid entries={[
          ["Cluster status", cluster?.status], ["Active / desired", `${cluster?.active_size || 1} / ${cluster?.desired_size || 1}`], ["Baseline release", cluster?.baseline_release], ["K3s version", cluster?.k3s_version],
          ["etcd leader", owner(leaders?.etcd_leader)], ["Control VIP owner", owner(leaders?.control_vip_owner)], ["Edge VIP owner", owner(leaders?.edge_vip_owner)],
          ["PostgreSQL primary", owner(leaders?.postgres_primary)], ["Scheduler leader", owner(leaders?.scheduler_leader)], ["WireGuard owner", owner(leaders?.wireguard_owner)],
        ]} /> : null}

        {tab === "nodes" ? (
          <ClusterGrid
            rowData={nodeRows}
            columnDefs={nodeColumnDefs}
            autoSizeColumnIds={NODE_AUTO_SIZE_COLUMNS}
            getRowId={(params) => params?.data?.id || params?.data?.node_name}
            rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
            onCellContextMenu={(params) => openNodeActionMenu(params?.event, params?.data, params?.node)}
          />
        ) : null}

        {tab === "database" ? <Stack spacing={2}>
          <KeyValueGrid entries={[["Primary", owner(leaders?.postgres_primary)], ["Configured / ready", `${configuredDatabaseInstances} / ${readyDatabaseInstances ?? "not observed"}`], ["Durability", activeSize > 1 ? `${Number(database?.synchronous_acknowledgements ?? 1)} synchronous acknowledgement` : "Single instance; no standby acknowledgement"], ["Storage", "Strict-local Longhorn, one replica"], ["Snapshots", "Daily, 14 retained + pre-change"], ["Writes", activeSize > 1 ? "Blocked without durability quorum" : "Available while primary is healthy"]]} />
          {databaseReadyObserved && !databaseFullyReady ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>{readyDatabaseInstances} of {configuredDatabaseInstances} PostgreSQL instances are Ready. {databaseDurabilityReady ? "Writes retain required synchronous durability, but node redundancy is reduced." : "Required durability quorum is unavailable; writes may be blocked."} {database?.phase ? `CloudNativePG: ${database.phase}.` : ""}</Alert> : null}
          <Alert severity="info">Snapshots provide in-cluster recovery. They do not replace off-cluster disaster recovery.</Alert>
          <Paper sx={CARD_SX}><Typography variant="h6">PostgreSQL role control</Typography><Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><FormControl sx={{ minWidth: 220 }}><InputLabel id="postgres-node-label">Target node</InputLabel><Select labelId="postgres-node-label" label="Target node" value={selectedNode} onChange={(event) => setSelectedNode(event.target.value)}>{nodes.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select></FormControl><Button variant="outlined" disabled={!selectedNode || !normalOperationsEnabled} onClick={() => openAction("switchover")}>Switchover</Button><Button color="error" variant="outlined" disabled={!selectedNode} onClick={() => openAction("emergency_failover")}>Emergency Failover</Button></Stack></Paper>
        </Stack> : null}

        {tab === "operations" ? (
          <Stack spacing={1.25} sx={{ minHeight: 320, height: "100%", flexGrow: 1 }}>
            {eventError ? <Alert severity="warning">{eventError} Operation summaries remain available, but copied details may omit lifecycle events.</Alert> : null}
            <ClusterGrid
              rowData={operationRows}
              columnDefs={operationColumnDefs}
              autoSizeColumnIds={OPERATION_AUTO_SIZE_COLUMNS}
              getRowId={(params) => params?.data?.id || String(params?.data?.created_at || params?.rowIndex || "")}
            />
          </Stack>
        ) : null}

        {tab === "maintenance" ? <Stack spacing={2}>
          <Paper sx={CARD_SX}><Typography variant="h6">Cluster-Wide Node Isolation</Typography><Alert severity="warning" sx={{ mt: 1.5 }}>{HMR_WARNING}</Alert><Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><FormControl disabled={!isolationInactive} sx={{ minWidth: 220 }}><InputLabel id="hmr-node-label">Isolated Node</InputLabel><Select labelId="hmr-node-label" label="Isolated Node" value={isolationNodeValue} onChange={(event) => setSelectedNode(event.target.value)}>{isolationNodeOptions.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select></FormControl><Button color="warning" variant="contained" disabled={!normalOperationsEnabled || !selectedNode || !isolationInactive} onClick={() => openAction("hmr_start")}>Enable Isolation</Button><Button variant="outlined" disabled={!isolationExitAllowed} onClick={() => openAction("hmr_exit")}>Disable Isolation</Button></Stack></Paper>
          <Paper sx={CARD_SX}>
            <Typography variant="h6">Stable Engine Release</Typography>
            <Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>Loaded from published, non-prerelease GitHub releases that declare cluster compatibility; cluster does not generate versions. Current tags use YYYY.MM.DD.N, with final number distinguishing multiple releases for same date. Borealis pins selected tag to exact commit, then drains, updates, and verifies one node at a time.</Typography>
            {releaseError ? <Alert severity="error" sx={{ mt: 2 }}>{releaseError}</Alert> : null}
            {!releaseError && releases.length === 0 ? <Alert severity="info" sx={{ mt: 2 }}>No published stable cluster-compatible release exists at or above baseline {valueLabel(cluster?.baseline_release)}. Current release remains pinned.</Alert> : null}
            {!releaseError && releases.length > 0 && selectableReleases.length === 0 ? <Alert severity="warning" sx={{ mt: 2 }}>Stable releases were found, but none match current rolling-update and K3s compatibility requirements. Open release list for per-release reason.</Alert> : null}
            <FormControl fullWidth sx={{ mt: 2 }}>
              <InputLabel id="cluster-release-label">Release</InputLabel>
              <Select labelId="cluster-release-label" label="Release" value={selectedRelease} onChange={(event) => setSelectedRelease(event.target.value)}>
                {releases.map((release) => <MenuItem key={release.tag} value={release.tag} disabled={!release.selectable}>{release.title || release.tag}{release.selectable ? "" : ` — ${release.reason || "incompatible"}`}</MenuItem>)}
              </Select>
            </FormControl>
            <Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}>
              <Button variant="contained" disabled={!rollingUpdateEnabled || !selectedRelease || !selectableReleases.length} onClick={() => openAction("update_all")}>Update All One at a Time</Button>
              <FormControl sx={{ minWidth: 220 }}>
                <InputLabel id="update-node-label">Node</InputLabel>
                <Select labelId="update-node-label" label="Node" value={selectedNode} onChange={(event) => setSelectedNode(event.target.value)}>{nodes.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select>
              </FormControl>
              <Button variant="outlined" disabled={!rollingUpdateEnabled || !selectedRelease || !selectedNode} onClick={() => openAction("update_node", nodes.find((node) => node.id === selectedNode))}>Update Node</Button>
            </Stack>
          </Paper>
          <Paper sx={CARD_SX}><Typography variant="h6">K3s server upgrade</Typography><Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>Current: {valueLabel(cluster?.k3s_version)}. Stable target only; current minor patch or next minor. Borealis snapshots etcd, drains one application node, runs immutable system-upgrade Plan, then requires Ready/etcd voter health and probe conformance before next server.</Typography><Button sx={{ mt: 2 }} variant="outlined" color="warning" disabled={!normalOperationsEnabled || cluster?.hmr?.state !== "inactive"} onClick={() => openAction("k3s_update")}>Upgrade K3s One Server at a Time</Button></Paper>
          <Paper sx={CARD_SX}><Typography variant="h6">Pending quorum admissions</Typography><Stack spacing={1} sx={{ mt: 1.5 }}>{(cluster?.admissions || []).map((admission) => <Stack key={admission.id} direction="row" justifyContent="space-between" alignItems="center"><Typography>{admission.node_name} · {admission.state}</Typography><Button size="small" disabled={busy || !canPrepareMembership || admission.state !== "Pending Quorum"} onClick={() => void mutate(`/api/server/cluster/admissions/${admission.id}/approve`, { confirmation: "APPROVE NODE" })}>{replacementRecovery ? "Approve Replacement" : "Approve Pair"}</Button></Stack>)}</Stack></Paper>
          {!cluster?.enabled ? <Paper sx={CARD_SX}><Typography variant="h6">Enable cluster mode</Typography><Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>One-way PostgreSQL migration. Stable K3s probe conformance must pass first.</Typography><Button sx={{ mt: 2 }} variant="contained" color="warning" onClick={() => openAction("cluster_enable")}>Enable Cluster</Button></Paper> : null}
          {cluster?.enabled ? <Paper sx={CARD_SX}><Typography variant="h6">Membership</Typography>{replacementRecovery ? <Alert severity="warning" sx={{ mt: 1.5 }}>Cluster is running on two surviving members after externally fenced emergency removal. Create and approve one replacement invitation to restore three-node membership.</Alert> : !canExpandToThree ? <Alert severity="info" sx={{ mt: 1.5 }}>Three-node release limit reached. Odd-numbered expansion or shrinking beyond three nodes remains future roadmap work.</Alert> : null}<Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><Button variant="outlined" disabled={!canPrepareMembership} onClick={() => openAction("invite")}>Create Node Invitation</Button>{canExpandToThree ? <><FormControl sx={{ minWidth: 140 }}><InputLabel id="desired-size-label">Desired size</InputLabel><Select labelId="desired-size-label" label="Desired size" value={expansionSizes.includes(Number(desiredSize)) ? desiredSize : ""} onChange={(event) => setDesiredSize(event.target.value)}>{expansionSizes.map((size) => <MenuItem key={size} value={size}>{size}</MenuItem>)}</Select></FormControl><Button variant="outlined" onClick={() => openAction("scale")}>Request Pair Expansion</Button></> : null}</Stack>{inviteBundle ? <TextField sx={{ mt: 2 }} fullWidth multiline minRows={3} label="One-use invitation bundle" value={inviteBundle} InputProps={{ readOnly: true }} /> : null}</Paper> : null}
        </Stack> : null}
      </Stack>

      <RowContextMenu
        open={Boolean(nodeActionMenu.open)}
        onClose={closeNodeActionMenu}
        position={nodeActionMenu.open ? { top: nodeActionMenu.top, left: nodeActionMenu.left } : null}
        headerIcon={NodesIcon}
        title={valueLabel(nodeActionTarget?.node_name, "Node Actions")}
        subtitle={`${valueLabel(nodeActionTarget?.management_ip, "No IP Address")} · ${clusterNodeStatusLabel(nodeActionTarget)}`}
        actions={nodeActionMenuActions}
        widthVariant="standard"
      />

      <Dialog open={Boolean(dialog)} onClose={() => !busy && setDialog(null)} maxWidth="sm" fullWidth>
        <DialogTitle>Confirm cluster operation</DialogTitle>
        <DialogContent>
          {dialog?.kind === "hmr_start" ? <Alert severity="warning" sx={{ mb: 2 }}>{HMR_WARNING}</Alert> : null}
          {maintenanceExitDisablesIsolation ? <Alert severity="warning" sx={{ mb: 2 }}>Cluster-Wide Node Isolation will be disabled if the node exits maintenance mode.</Alert> : null}
          {dialog?.kind === "remove" ? <Alert severity="warning" sx={{ mb: 2 }}>Safe downscale removes two nodes sequentially. PostgreSQL replicas must vacate both targets before Borealis self-fences K3s and deletes membership.</Alert> : null}
          {dialog?.kind === "emergency_remove" ? <Alert severity="error" sx={{ mb: 2 }}>Emergency removal is only safe after external power fencing. Target must be powered off and unable to rejoin.</Alert> : null}
          {dialog?.kind === "k3s_update" ? <Alert severity="warning" sx={{ mb: 2 }}>K3s control-plane update stays separate from Engine release update. Failure halts sequence and leaves affected node drained.</Alert> : null}
          {dialog?.kind === "update_node" || dialog?.kind === "update_all" ? (
            <FormControl fullWidth sx={{ mb: 2 }}>
              <InputLabel id="dialog-release-label">Release</InputLabel>
              <Select labelId="dialog-release-label" label="Release" value={selectedRelease} onChange={(event) => setSelectedRelease(event.target.value)}>
                {releases.map((release) => <MenuItem key={release.tag} value={release.tag} disabled={!release.selectable}>{release.title || release.tag}</MenuItem>)}
              </Select>
            </FormControl>
          ) : null}
          {dialog?.kind === "cluster_enable" ? <Stack spacing={1.5} sx={{ mb: 2 }}><TextField label="Control-plane VIP" value={controlVIP} onChange={(event) => setControlVIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Borealis edge VIP" value={edgeVIP} onChange={(event) => setEdgeVIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Current node management IPv4" value={managementIP} onChange={(event) => setManagementIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Current node name" value={nodeName} onChange={(event) => setNodeName(sanitizeSingleLineInput(event.target.value).toLowerCase())} inputProps={{ maxLength: 63 }} /><FormControl><InputLabel id="cluster-architecture-label">Architecture</InputLabel><Select labelId="cluster-architecture-label" label="Architecture" value={architecture} onChange={(event) => setArchitecture(event.target.value)}><MenuItem value="amd64">amd64</MenuItem><MenuItem value="arm64">arm64</MenuItem></Select></FormControl></Stack> : null}
          {dialog?.kind === "invite" ? <TextField fullWidth label="New node name" value={nodeName} onChange={(event) => setNodeName(sanitizeSingleLineInput(event.target.value).toLowerCase())} inputProps={{ maxLength: 63 }} /> : null}
          {dialog?.kind === "k3s_update" ? <TextField fullWidth sx={{ mb: 2 }} label="Stable K3s target" value={k3sTargetVersion} onChange={(event) => setK3sTargetVersion(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 32 }} helperText="vX.Y.Z+k3sN; immutable upgrade image and source conformance required" /> : null}
          {dialog?.kind === "remove" ? <FormControl fullWidth sx={{ mb: 2 }}><InputLabel id="paired-removal-node-label">Paired removal node</InputLabel><Select labelId="paired-removal-node-label" label="Paired removal node" value={pairedNode} onChange={(event) => setPairedNode(event.target.value)}>{nodes.filter((candidate) => candidate.id !== dialog?.node?.id && candidate.membership_state === "Active").map((candidate) => <MenuItem key={candidate.id} value={candidate.id}>{candidate.node_name}</MenuItem>)}</Select></FormControl> : null}
          {dialog?.kind === "emergency_remove" ? <TextField fullWidth sx={{ mb: 2 }} label="External fencing confirmation" value={fencingConfirmation} onChange={(event) => setFencingConfirmation(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 21 }} helperText="Type TARGET IS POWERED OFF" /> : null}
          {["maintenance", "scale", "remove", "emergency_remove", "switchover", "emergency_failover"].includes(dialog?.kind) && !maintenanceExitDisablesIsolation ? <TextField fullWidth label="Reason" value={reason} onChange={(event) => setReason(sanitizeSingleLineInput(event.target.value).slice(0, 256))} inputProps={{ maxLength: 256 }} helperText={`${reason.length}/256 · single-line operational text`} /> : null}
          {maintenanceExitDisablesIsolation || !['maintenance', 'invite', 'scale', 'switchover'].includes(dialog?.kind) ? <TextField autoFocus fullWidth label="Typed confirmation" value={confirmation} onChange={(event) => setConfirmation(sanitizeSingleLineInput(event.target.value))} helperText={maintenanceExitDisablesIsolation ? "Type EXIT HMR to disable isolation" : dialog?.kind === "hmr_start" ? "Type ENABLE HMR to enable isolation" : dialog?.kind === "hmr_exit" ? "Type EXIT HMR to disable isolation" : dialog?.kind === "cluster_enable" ? "Type ENABLE CLUSTER" : dialog?.kind === "remove" ? "Type REMOVE NODE PAIR" : dialog?.kind === "emergency_remove" ? "Type EMERGENCY REMOVE NODE" : dialog?.kind === "k3s_update" ? "Type UPDATE K3S" : dialog?.kind === "emergency_failover" ? "Type EMERGENCY FAILOVER" : "Type UPDATE CLUSTER"} /> : null}
          <Typography variant="body2" sx={{ mt: 2, color: "text.secondary" }}>Administrator access required. Destructive actions also require exact typed confirmation.</Typography>
        </DialogContent>
        <DialogActions><Button onClick={() => setDialog(null)} disabled={busy}>Cancel</Button><Button variant="contained" color={dialog?.kind === "emergency_remove" ? "error" : dialog?.kind === "hmr_start" || maintenanceExitDisablesIsolation ? "warning" : "primary"} onClick={() => void submitDialog()} disabled={busy}>Submit</Button></DialogActions>
      </Dialog>
    </PageBodyFrame>
  );
}
