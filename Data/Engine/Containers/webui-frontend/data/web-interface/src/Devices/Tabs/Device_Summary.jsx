////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Device_Summary.jsx

import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";
import { useLoaderData, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Alert,
  Box,
  Stack,
  Tooltip,
  Typography,
  Button,
  ListItemButton,
  ListItemText,
  Menu,
  MenuItem,
  TextField,
} from "@mui/material";
import InfoOutlinedIcon from "@mui/icons-material/InfoOutlined";
import StorageRoundedIcon from "@mui/icons-material/StorageRounded";
import MemoryRoundedIcon from "@mui/icons-material/MemoryRounded";
import LanRoundedIcon from "@mui/icons-material/LanRounded";
import AppsRoundedIcon from "@mui/icons-material/AppsRounded";
import SettingsRoundedIcon from "@mui/icons-material/SettingsRounded";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import ListAltRoundedIcon from "@mui/icons-material/ListAltRounded";
import TerminalRoundedIcon from "@mui/icons-material/TerminalRounded";
import DesktopWindowsRoundedIcon from "@mui/icons-material/DesktopWindowsRounded";
import PolicyIcon from "@mui/icons-material/Policy";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import FolderRoundedIcon from "@mui/icons-material/FolderRounded";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";
import LabelRoundedIcon from "@mui/icons-material/LabelRounded";
import ChevronLeftIcon from "@mui/icons-material/ChevronLeft";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import WarningAmberRoundedIcon from "@mui/icons-material/WarningAmberRounded";
import ErrorOutlineRoundedIcon from "@mui/icons-material/ErrorOutlineRounded";
import HelpOutlineRoundedIcon from "@mui/icons-material/HelpOutlineRounded";
import { ClearDeviceActivityDialog } from "../../Dialogs.jsx";
import { AgGridReact } from "ag-grid-react";
import ActivityHistoryTab from "./Activity_History.jsx";
import InstalledSoftwareTab from "./Installed_Software.jsx";
import DeviceWatchdogsTab from "./Device_Watchdogs.jsx";
import RemoteShellTab from "./Remote_Shell.jsx";
import RemoteFileManagementTab from "./Remote_File_Management.jsx";
import ProcessManagementTab from "./Process_Management.jsx";
import { buildAgentHealthRows } from "./Agent_Health.jsx";
import { RuntimeRoleHealthBreakdown } from "./Agent_Startup_Flow.jsx";
import DeviceMetadataTab from "./Device_Metadata.jsx";
import { DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI, gridFontFamily } from "./Shared.jsx";
import ServiceList from "./Service_List.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../../app/providers/AuthContext.jsx";
import { APP_PATHS } from "../../app/routes/paths.js";
import QuickJobDialog from "../../Assemblies/Quick_Job_Dialog.jsx";
import {
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../../app/routes/routeData.js";
import { normalizeQuickJobTargets } from "../../app/utils/quickJob.js";
import AgentBranchChannelDialog, {
  fetchAgentBranchRows,
  normalizeAgentBranch,
  normalizeAgentReleaseChannel,
} from "../../AgentBranchChannelDialog.jsx";

const TUNNEL_STATUS_POLL_INTERVAL_MS = 15000;
const DEVICE_DETAILS_POLL_INTERVAL_MS = 60000;

const PAGE_ICON = DeveloperBoardRoundedIcon;

const getOsIconClass = (osName) => {
  const value = (osName || "").toString().toLowerCase();
  if (!value) return "";

  if (value.includes("mac") || value.includes("os x") || value.includes("darwin")) {
    return "fa-brands fa-apple";
  }

  if (value.includes("win")) {
    return "fa-brands fa-windows";
  }

  if (
    value.includes("linux") ||
    value.includes("ubuntu") ||
    value.includes("debian") ||
    value.includes("fedora") ||
    value.includes("red hat") ||
    value.includes("centos") ||
    value.includes("suse") ||
    value.includes("rhel")
  ) {
    return "fa-brands fa-linux";
  }

  return "";
};

function OperatingSystemPageIcon({ osName, sx }) {
  const iconClass = getOsIconClass(osName);
  if (!iconClass) {
    return <PAGE_ICON sx={sx} />;
  }

  return (
    <Box
      component="i"
      className={iconClass}
      aria-hidden="true"
      sx={[
        {
          color: "#7dd3fc",
          fontSize: 22,
          lineHeight: 1,
          textAlign: "center",
          width: 22,
        },
        ...(Array.isArray(sx) ? sx : [sx]).filter(Boolean),
      ]}
    />
  );
}

const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};
const BOREALIS_LINK_COLOR = "#7db7ff";
const BOREALIS_LINK_HOVER_COLOR = "#a8d4ff";
const BOREALIS_PRIMARY_GRADIENT = "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)";
const HARDWARE_SUMMARY_SECTION_SX = {
  p: 0,
  border: "none",
  borderRadius: 0,
  background: "transparent",
  boxShadow: "none",
  mb: 0,
};
const DEVICE_NAV_SIDEBAR_SX = {
  minWidth: 260,
  maxWidth: 260,
  width: 260,
  position: "relative",
  zIndex: 2,
  borderRight: "1px solid rgba(125,183,255,0.14)",
  borderRadius: 0,
  background:
    "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), #0f141c",
  boxShadow: "none",
  display: "flex",
  flexDirection: "column",
  flexShrink: 0,
  height: "100%",
  overflow: "hidden",
  backdropFilter: "blur(8px) saturate(130%)",
};
const DEVICE_NAV_ACCORDION_SX = {
  "&:before": { display: "none" },
  m: 0,
  bgcolor: "transparent",
  border: 0,
  boxShadow: "none",
};
const DEVICE_NAV_ACCORDION_SUMMARY_SX = {
  minHeight: 38,
  px: 1.5,
  background: "none",
  backgroundColor: "rgba(255,255,255,0.02)",
  borderTopRightRadius: 8,
  borderBottomRightRadius: 8,
  "&:hover": {
    background: "none",
    backgroundColor: "rgba(255,255,255,0.02)",
  },
  "& .MuiAccordionSummary-content": {
    m: 0,
    py: 0.5,
    display: "flex",
    alignItems: "center",
    minWidth: 0,
  },
  "&.Mui-expanded": {
    minHeight: 38,
  },
  "& .MuiAccordionSummary-content.Mui-expanded": {
    my: 0,
  },
};
const DEVICE_NAV_ACCORDION_DETAILS_SX = {
  p: 0,
};

function deviceSidebarNavRowSx(active, disabled = false, indent = 0) {
  return {
    pl: indent ? 4 : 2,
    pr: 2,
    py: 1,
    color: active ? NAV_TAB_COLORS.textActive : NAV_TAB_COLORS.text,
    position: "relative",
    background: active ? NAV_TAB_COLORS.activeBg : "transparent",
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    justifyContent: "space-between",
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "&:hover": {
      background: active ? NAV_TAB_COLORS.activeBg : NAV_TAB_COLORS.hover,
    },
    '&.Mui-selected, &[data-summary-active="true"]': {
      color: NAV_TAB_COLORS.textActive,
      background: NAV_TAB_COLORS.activeBg,
    },
    '&.Mui-selected:hover, &[data-summary-active="true"]:hover': {
      background: NAV_TAB_COLORS.activeBg,
    },
    '&.Mui-selected .device-summary-nav-icon, &[data-summary-active="true"] .device-summary-nav-icon': {
      color: NAV_TAB_COLORS.iconActive,
    },
    '&.Mui-selected .device-summary-nav-label, &[data-summary-active="true"] .device-summary-nav-label': {
      fontWeight: 600,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
    "&.Mui-disabled": {
      color: "rgba(203,213,225,0.38)",
    },
    ...(disabled ? { cursor: "not-allowed" } : null),
  };
}

function deviceSidebarNavIconSx(active, disabled = false) {
  return {
    mr: 1,
    display: "flex",
    alignItems: "center",
    color: disabled ? "rgba(143,191,255,0.35)" : active ? NAV_TAB_COLORS.iconActive : NAV_TAB_COLORS.icon,
    transition: "color 160ms ease",
  };
}

const BASE_GRID_HEIGHTS = {
  topLevel: 300,
  storage: 300,
  memory: 260,
  network: 260,
};
const TUNNEL_INFO_IDLE = Object.freeze({
  status: "idle",
  tunnel_id: "",
  virtual_ip: "",
  agent_socket: false,
  listener_healthy: false,
  recovery_in_progress: false,
  last_recovery_attempt_at: null,
  last_recovery_attempt_at_iso: "",
});

const WORKSPACES = [
  { key: "inventory", label: "Inventory", icon: AppsRoundedIcon },
  { key: "remote_ops", label: "Backend Tools", icon: TerminalRoundedIcon },
  { key: "protection", label: "Protection", icon: PolicyIcon },
  { key: "history", label: "History", icon: ListAltRoundedIcon },
  { key: "config", label: "Metadata", icon: LabelRoundedIcon },
];
const WORKSPACE_KEYS = new Set(WORKSPACES.map((workspace) => workspace.key));
const WORKSPACE_VIEW_DEFAULTS = Object.freeze({
  remote_ops: "shell",
  inventory: "summary",
  config: "metadata",
});
const WORKSPACE_VIEW_OPTIONS = Object.freeze({
  remote_ops: ["shell", "files", "processes", "services"],
  inventory: ["summary", "software"],
  config: ["metadata"],
});
const LEGACY_TAB_TO_WORKSPACE = Object.freeze({
  command: { workspace: "inventory", view: "summary" },
  device_summary: { workspace: "inventory", view: "summary" },
  summary: { workspace: "inventory", view: "summary" },
  file_management: { workspace: "remote_ops", view: "files" },
  installed_software: { workspace: "inventory", view: "software" },
  software: { workspace: "inventory", view: "software" },
  metadata_fields: { workspace: "config", view: "metadata" },
  metadata: { workspace: "config", view: "metadata" },
  services: { workspace: "remote_ops", view: "services" },
  process_management: { workspace: "remote_ops", view: "processes" },
  processes: { workspace: "remote_ops", view: "processes" },
  watchdogs: { workspace: "protection" },
  activity_history: { workspace: "history" },
  activity: { workspace: "history" },
  remote_shell: { workspace: "remote_ops", view: "shell" },
  shell: { workspace: "remote_ops", view: "shell" },
  agent_health: { workspace: "inventory", view: "summary" },
  health: { workspace: "inventory", view: "summary" },
});

const normalizeWorkspaceKey = (value, fallback = "inventory") => {
  const normalized = String(value || "").trim().toLowerCase();
  if (WORKSPACE_KEYS.has(normalized)) return normalized;
  const legacyTarget = LEGACY_TAB_TO_WORKSPACE[normalized];
  if (legacyTarget?.workspace && WORKSPACE_KEYS.has(legacyTarget.workspace)) {
    return legacyTarget.workspace;
  }
  return fallback;
};

const normalizeWorkspaceView = (workspaceKey, value) => {
  const allowedViews = WORKSPACE_VIEW_OPTIONS[workspaceKey] || [];
  const fallback = WORKSPACE_VIEW_DEFAULTS[workspaceKey] || "";
  const normalized = String(value || "").trim().toLowerCase();
  if (allowedViews.includes(normalized)) return normalized;
  return fallback;
};

const createDeviceWorkspaceSearch = (currentSearch, workspaceKey, viewKey = "") => {
  const params = new URLSearchParams(currentSearch || "");
  const normalizedWorkspace = normalizeWorkspaceKey(workspaceKey);
  const normalizedView = normalizeWorkspaceView(normalizedWorkspace, viewKey);
  params.set("tab", normalizedWorkspace);
  if (normalizedView) {
    params.set("view", normalizedView);
  } else {
    params.delete("view");
  }
  return params.toString() ? `?${params.toString()}` : "";
};

const resolveDeviceId = (device) =>
  device?.agent_guid ||
  device?.guid ||
  device?.summary?.agent_guid ||
  device?.hostname ||
  device?.id ||
  null;

const SUMMARY_SECTIONS = [
  { key: "top-level", label: "Overview", icon: InfoOutlinedIcon },
  { key: "storage", label: "Storage", icon: StorageRoundedIcon },
  { key: "memory", label: "Memory", icon: MemoryRoundedIcon },
  { key: "network", label: "Network", icon: LanRoundedIcon },
];
const DEFAULT_SUMMARY_SECTION_KEY = SUMMARY_SECTIONS[0]?.key || "top-level";
const normalizeSummarySectionKey = (sectionKey) =>
  SUMMARY_SECTIONS.some((section) => section.key === sectionKey) ? sectionKey : DEFAULT_SUMMARY_SECTION_KEY;

function normalizeTunnelInfoState(data = {}) {
  return {
    status: data?.status || "idle",
    tunnel_id: data?.tunnel_id || "",
    virtual_ip: data?.virtual_ip || "",
    agent_socket: Boolean(data?.agent_socket),
    listener_healthy: data?.listener_healthy !== false,
    recovery_in_progress: Boolean(data?.recovery_in_progress),
    last_recovery_attempt_at: data?.last_recovery_attempt_at ?? null,
    last_recovery_attempt_at_iso: data?.last_recovery_attempt_at_iso || "",
  };
}

function tunnelInfoMatches(left, right) {
  if (!left || !right) return false;
  return (
    String(left.status || "") === String(right.status || "") &&
    String(left.tunnel_id || "") === String(right.tunnel_id || "") &&
    String(left.virtual_ip || "") === String(right.virtual_ip || "") &&
    Boolean(left.agent_socket) === Boolean(right.agent_socket) &&
    Boolean(left.listener_healthy) === Boolean(right.listener_healthy) &&
    Boolean(left.recovery_in_progress) === Boolean(right.recovery_in_progress) &&
    (left.last_recovery_attempt_at ?? null) === (right.last_recovery_attempt_at ?? null) &&
    String(left.last_recovery_attempt_at_iso || "") === String(right.last_recovery_attempt_at_iso || "")
  );
}

function describeAgentUpdateState(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) return "No recent updater activity";
  if (normalized === "applied") return "Update applied successfully";
  if (normalized === "up_to_date") return "Checked and already current";
  if (normalized === "deferred") return "Update queued until the device is idle";
  if (normalized === "checking") return "Checking for channel updates";
  if (normalized === "downloading") return "Downloading the update package";
  if (normalized === "staging") return "Staging the update package";
  if (normalized === "staged") return "Update staged and awaiting runtime refresh";
  if (normalized === "failed") return "Last update attempt failed";
  if (normalized === "idle") return "No recent updater activity";
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

const SUMMARY_GRID_STYLE = {
  width: "100%",
  height: "100%",
  fontFamily: gridFontFamily,
};
const SUMMARY_FIELD_TEXT_COLOR = "#58a6ff"; // matches hostname blue in Device_List.jsx
const SUMMARY_DEFAULT_TEXT_COLOR = NAV_TAB_COLORS.textActive;
const UNABLE_TO_RETRIEVE_SN = "<Unable to Retrieve S/N>";
const STORAGE_USAGE_ALERT_THRESHOLD_PCT = 90;
const STORAGE_USAGE_ALERT_LABEL = `Usage Exceeding ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}%`;
const STORAGE_USAGE_ALERT_COLOR = "#facc15";

function statusFromHeartbeat(tsSec, offlineAfter = 300) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
}

function formatDeviceSummaryUtcTimestamp(dateObj) {
  if (!dateObj || Number.isNaN(dateObj.getTime())) return "";
  const pad = (value) => String(value).padStart(2, "0");
  return `${dateObj.getUTCFullYear()}-${pad(dateObj.getUTCMonth() + 1)}-${pad(dateObj.getUTCDate())} ${pad(dateObj.getUTCHours())}:${pad(dateObj.getUTCMinutes())}:${pad(dateObj.getUTCSeconds())}`;
}

function readFirstNonEmptyValue(...values) {
  for (const value of values) {
    if (value === undefined || value === null) continue;
    const text = String(value).trim();
    if (text) return text;
  }
  return "";
}

function normalizeDeviceSummarySnapshot(detailData, { device = {}, deviceId = "", agentRecord = null } = {}) {
  const guid =
    device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid || deviceId || "";
  const agentId = device?.agentId || device?.summary?.agent_id || device?.id || "";
  const hostname = device?.hostname || device?.summary?.hostname || deviceId || "";

  const summary =
    detailData?.summary && typeof detailData.summary === "object"
      ? detailData.summary
      : detailData?.details?.summary || {};
  const normalizedSummary = { ...(summary || {}) };
  if (detailData?.description) {
    normalizedSummary.description = detailData.description;
  }

  const connectionType =
    (normalizedSummary.connection_type || normalizedSummary.remote_type || "").toLowerCase();
  const connectionEndpoint =
    normalizedSummary.connection_endpoint ||
    normalizedSummary.connection_address ||
    detailData?.connection_endpoint ||
    "";

  const details = {
    summary: normalizedSummary,
    memory: Array.isArray(detailData?.memory)
      ? detailData.memory
      : Array.isArray(detailData?.details?.memory)
        ? detailData.details.memory
        : [],
    network: Array.isArray(detailData?.network)
      ? detailData.network
      : Array.isArray(detailData?.details?.network)
        ? detailData.details.network
        : [],
    software: Array.isArray(detailData?.software)
      ? detailData.software
      : Array.isArray(detailData?.details?.software)
        ? detailData.details.software
        : [],
    storage: Array.isArray(detailData?.storage)
      ? detailData.storage
      : Array.isArray(detailData?.details?.storage)
        ? detailData.details.storage
        : [],
    cpu: detailData?.cpu || detailData?.details?.cpu || {},
  };

  const cpuIdentity = details.cpu && typeof details.cpu === "object" ? details.cpu : {};
  const manufacturerValue = readFirstNonEmptyValue(
    detailData?.manufacturer,
    normalizedSummary.manufacturer,
    normalizedSummary.vendor,
    detailData?.details?.summary?.manufacturer,
    detailData?.details?.summary?.vendor,
    cpuIdentity.system_manufacturer
  );
  const systemModelValue = readFirstNonEmptyValue(
    detailData?.system_model,
    normalizedSummary.system_model,
    normalizedSummary.device_model,
    detailData?.details?.summary?.system_model,
    detailData?.details?.summary?.device_model,
    cpuIdentity.system_model_raw
  );
  const combinedModelValue =
    readFirstNonEmptyValue(
      detailData?.model,
      normalizedSummary.model,
      detailData?.details?.summary?.model,
      cpuIdentity.system_model
    ) || [manufacturerValue, systemModelValue].filter(Boolean).join(" ").trim();
  const serialNumberValue =
    readFirstNonEmptyValue(
      detailData?.serial_number,
      normalizedSummary.serial_number,
      normalizedSummary.serial,
      normalizedSummary.bios_serial,
      detailData?.details?.summary?.serial_number,
      detailData?.details?.summary?.serial,
      detailData?.details?.summary?.bios_serial,
      cpuIdentity.system_serial_number,
      cpuIdentity.serial_number,
      detailData?.asset_tag,
      normalizedSummary.asset_tag
    ) || UNABLE_TO_RETRIEVE_SN;

  if (!normalizedSummary.manufacturer && manufacturerValue) {
    normalizedSummary.manufacturer = manufacturerValue;
  }
  if (!normalizedSummary.system_model && systemModelValue) {
    normalizedSummary.system_model = systemModelValue;
  }
  if (!normalizedSummary.device_model && systemModelValue) {
    normalizedSummary.device_model = systemModelValue;
  }
  if (!normalizedSummary.model && combinedModelValue) {
    normalizedSummary.model = combinedModelValue;
  }
  if (!normalizedSummary.serial_number && serialNumberValue) {
    normalizedSummary.serial_number = serialNumberValue;
  }
  if (!normalizedSummary.serial && serialNumberValue) {
    normalizedSummary.serial = serialNumberValue;
  }
  if (!normalizedSummary.bios_serial && serialNumberValue) {
    normalizedSummary.bios_serial = serialNumberValue;
  }

  if (details.cpu && typeof details.cpu === "object") {
    if (manufacturerValue && !details.cpu.system_manufacturer) {
      details.cpu.system_manufacturer = manufacturerValue;
    }
    if (systemModelValue && !details.cpu.system_model_raw) {
      details.cpu.system_model_raw = systemModelValue;
    }
    if (combinedModelValue && !details.cpu.system_model) {
      details.cpu.system_model = combinedModelValue;
    }
    if (serialNumberValue && !details.cpu.system_serial_number) {
      details.cpu.system_serial_number = serialNumberValue;
    }
  }

  let createdDisplay = normalizedSummary.created || "";
  if (!createdDisplay) {
    if (detailData?.created_at && Number(detailData.created_at)) {
      createdDisplay = formatDeviceSummaryUtcTimestamp(new Date(Number(detailData.created_at) * 1000));
    } else if (detailData?.created_at_iso) {
      createdDisplay = formatDeviceSummaryUtcTimestamp(new Date(detailData.created_at_iso));
    }
  }

  const lastEnrollmentAtValue =
    detailData?.last_enrollment_at ||
    normalizedSummary.last_enrollment_at ||
    detailData?.details?.summary?.last_enrollment_at ||
    detailData?.created_at ||
    normalizedSummary.created_at ||
    0;
  const lastEnrollmentAtIso =
    detailData?.last_enrollment_at_iso ||
    normalizedSummary.last_enrollment_at_iso ||
    detailData?.details?.summary?.last_enrollment_at_iso ||
    detailData?.created_at_iso ||
    "";

  const meta = {
    hostname: detailData?.hostname || normalizedSummary.hostname || hostname || "",
    lastUser: detailData?.last_user || normalizedSummary.last_user || "",
    deviceType: detailData?.device_type || normalizedSummary.device_type || "",
    created: createdDisplay,
    createdAtIso: detailData?.created_at_iso || "",
    lastEnrollmentAt: lastEnrollmentAtValue,
    lastEnrollmentAtIso: lastEnrollmentAtIso,
    lastSeen: detailData?.last_seen || normalizedSummary.last_seen || 0,
    lastReboot: detailData?.last_reboot || normalizedSummary.last_reboot || "",
    operatingSystem:
      detailData?.operating_system ||
      normalizedSummary.operating_system ||
      normalizedSummary.agent_operating_system ||
      "",
    agentId: detailData?.agent_id || normalizedSummary.agent_id || agentId || "",
    agentGuid: detailData?.agent_guid || normalizedSummary.agent_guid || guid || "",
    agentBuildId:
      detailData?.agent_build_id ||
      detailData?.agent_hash ||
      normalizedSummary.agent_build_id ||
      normalizedSummary.agent_hash ||
      "",
    agentVersionStatus:
      detailData?.agent_version_status ||
      normalizedSummary.agent_version_status ||
      detailData?.details?.summary?.agent_version_status ||
      "Needs Updated",
    agentReleaseChannelOverride:
      detailData?.agent_release_channel_override ||
      normalizedSummary.agent_release_channel_override ||
      detailData?.details?.summary?.agent_release_channel_override ||
      "",
    agentReleaseChannel:
      detailData?.agent_release_channel ||
      normalizedSummary.agent_release_channel ||
      detailData?.details?.summary?.agent_release_channel ||
      "",
    agentBranch:
      detailData?.agent_branch ||
      normalizedSummary.agent_branch ||
      detailData?.details?.summary?.agent_branch ||
      "",
    agentReleaseChannelEffective:
      detailData?.agent_release_channel_effective ||
      normalizedSummary.agent_release_channel_effective ||
      detailData?.details?.summary?.agent_release_channel_effective ||
      "",
    agentTargetBuildId:
      detailData?.agent_target_build_id ||
      normalizedSummary.agent_target_build_id ||
      detailData?.details?.summary?.agent_target_build_id ||
      detailData?.agent_update_target_build_id ||
      normalizedSummary.agent_update_target_build_id ||
      "",
    agentTargetPublishedAt:
      detailData?.agent_target_published_at ||
      normalizedSummary.agent_target_published_at ||
      detailData?.details?.summary?.agent_target_published_at ||
      "",
    agentUpdateState:
      detailData?.agent_update_state ||
      normalizedSummary.agent_update_state ||
      detailData?.details?.summary?.agent_update_state ||
      "",
    agentUpdateError:
      detailData?.agent_update_error ||
      normalizedSummary.agent_update_error ||
      detailData?.details?.summary?.agent_update_error ||
      "",
    agentUpdateSource:
      detailData?.agent_update_source ||
      normalizedSummary.agent_update_source ||
      detailData?.details?.summary?.agent_update_source ||
      "",
    internalIp: detailData?.internal_ip || normalizedSummary.internal_ip || "",
    externalIp: detailData?.external_ip || normalizedSummary.external_ip || "",
    manufacturer: manufacturerValue || "",
    systemModel: systemModelValue || "",
    model: combinedModelValue || "",
    serialNumber: serialNumberValue || UNABLE_TO_RETRIEVE_SN,
    siteId: detailData?.site_id,
    siteName: detailData?.site_name || "",
    siteDescription: detailData?.site_description || "",
    status: detailData?.status || "",
    connectionType,
    connectionEndpoint,
    agentRoleHealth:
      detailData?.agent_role_health ||
      normalizedSummary.agent_role_health ||
      detailData?.details?.summary?.agent_role_health ||
      { roles: [], reported_at: 0 },
  };

  const description = normalizedSummary.description || detailData?.description || "";
  const baseAgent =
    agentRecord && meta.agentId && typeof agentRecord === "object"
      ? { id: meta.agentId, ...agentRecord }
      : { ...(device || {}) };
  const agent = {
    ...(baseAgent || {}),
    id: meta.agentId || baseAgent?.id || "",
    hostname: meta.hostname || baseAgent?.hostname || "",
    agent_guid: meta.agentGuid || baseAgent?.agent_guid || baseAgent?.guid || guid || "",
    guid: meta.agentGuid || baseAgent?.guid || baseAgent?.agent_guid || guid || "",
    agent_hash: meta.agentBuildId || baseAgent?.agent_hash || baseAgent?.agent_build_id || "",
    agent_build_id: meta.agentBuildId || baseAgent?.agent_build_id || baseAgent?.agent_hash || "",
    agent_operating_system: meta.operatingSystem || baseAgent?.agent_operating_system || "",
    device_type: meta.deviceType || baseAgent?.device_type || "",
    last_seen: meta.lastSeen || baseAgent?.last_seen || 0,
  };

  return {
    deviceId: String(deviceId || meta.agentGuid || meta.hostname || "").trim(),
    agent,
    details,
    meta,
    description,
    connectionType,
    connectionEndpoint,
    lockedStatus: meta.status || statusFromHeartbeat(meta.lastSeen),
  };
}

async function fetchDeviceSummarySnapshot({ request, device, deviceId, includeAgents = true, progress = null } = {}) {
  const guid =
    device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid || deviceId || "";
  const hostname = device?.hostname || device?.summary?.hostname || deviceId || "";
  const agentId = device?.agentId || device?.summary?.agent_id || device?.id || "";
  const fetchJsonWithProgress =
    progress?.fetchJson ||
    ((input, options = {}) =>
      fetchRouteJson(input, {
        ...options,
        request: options.request || request,
      }));

  const agentsPromise = includeAgents
    ? fetchJsonWithProgress("/api/agents").catch(() => null)
    : Promise.resolve(null);

  let detailPayload = null;
  let lastError = null;
  if (guid) {
    try {
      detailPayload = await fetchJsonWithProgress(`/api/devices/${encodeURIComponent(guid)}`);
    } catch (error) {
      rethrowIfRouteRedirect(error);
      lastError = error;
    }
  }
  if (!detailPayload && hostname) {
    try {
      detailPayload = await fetchJsonWithProgress(`/api/device/details/${encodeURIComponent(hostname)}`);
    } catch (error) {
      rethrowIfRouteRedirect(error);
      lastError = error;
    }
  } else if (progress && guid) {
    progress.skip(1);
  }

  if (!detailPayload) {
    throw lastError || new Error("Unable to load device details.");
  }

  const agentsData = await agentsPromise;
  const agentRecord =
    agentId && agentsData && typeof agentsData === "object" ? agentsData[agentId] || null : null;

  return normalizeDeviceSummarySnapshot(detailPayload, {
    device,
    deviceId,
    agentRecord,
  });
}

export async function loadDeviceSummaryPageData(request, routeDeviceId) {
  const deviceId = String(routeDeviceId || "").trim();
  const initialDevice = {
    agent_guid: deviceId || null,
    hostname: deviceId || null,
  };
  const progress = createRouteRequestPlan(request, 5);

  try {
    await requireAuthenticatedRequest(request, progress);
    if (!deviceId) {
      progress.skip(3);
      return {
        snapshot: null,
        initialError: "Unable to identify the selected device.",
      };
    }

    return {
      snapshot: await fetchDeviceSummarySnapshot({
        request,
        device: initialDevice,
        deviceId,
        includeAgents: true,
        progress,
      }),
      initialError: "",
    };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return {
      snapshot: null,
      initialError: getRouteErrorMessage(error, "Unable to load device details."),
    };
  } finally {
    progress.finalize();
  }
}

const isStorageUsageAlert = (usageValue) =>
  typeof usageValue === "number" &&
  !Number.isNaN(usageValue) &&
  usageValue > STORAGE_USAGE_ALERT_THRESHOLD_PCT;

const formatHostnameForDisplay = (value) => {
  const text = typeof value === "string" ? value.trim() : value == null ? "" : String(value).trim();
  if (!text) return "";
  const dotIndex = text.indexOf(".");
  return dotIndex > 0 ? text.slice(0, dotIndex) : text;
};

const SUMMARY_GRID_DEBUG_STORAGE_KEY = "borealis.debug.summaryGrid";

const isSummaryGridDebugEnabled = () => {
  try {
    if (typeof window === "undefined") return false;
    const params = new URLSearchParams(window.location.search || "");
    const queryValue = String(params.get("debugSummaryGrid") || "").toLowerCase();
    if (queryValue === "1" || queryValue === "true") return true;
    return window.localStorage?.getItem(SUMMARY_GRID_DEBUG_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
};

const summaryGridDebugLog = (...parts) => {
  if (!isSummaryGridDebugEnabled()) return;
  const stamp = new Date().toISOString();
  console.debug(`[DeviceSummary][SummaryDebug][${stamp}]`, ...parts);
};

const summarySectionRowId = (params) => params.data?.id ?? params.rowIndex;

const SummarySectionGrid = React.memo(function SummarySectionGrid({
  sectionKey,
  rowData,
  columnDefs,
  defaultColDef,
  height,
  rowHeight = 36,
}) {
  const renderCountRef = useRef(0);
  const modelUpdateCountRef = useRef(0);
  renderCountRef.current += 1;

  const rowCount = Array.isArray(rowData) ? rowData.length : 0;
  const summaryDefaultColDef = useMemo(() => {
    const baseColDef = defaultColDef || {};
    const existingCellStyle = baseColDef.cellStyle;
    return {
      ...baseColDef,
      cellStyle: (params) => {
        const resolvedStyle =
          typeof existingCellStyle === "function"
            ? existingCellStyle(params)
            : existingCellStyle || {};
        return {
          ...(resolvedStyle || {}),
          color: SUMMARY_DEFAULT_TEXT_COLOR,
        };
      },
    };
  }, [defaultColDef]);

  const handleGridReady = useCallback(
    (params) => {
      summaryGridDebugLog("gridReady", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        rowCount,
      });
    },
    [sectionKey, rowCount]
  );

  const handleFirstDataRendered = useCallback(
    (params) => {
      summaryGridDebugLog("firstDataRendered", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
      });
    },
    [sectionKey]
  );

  const handleRowDataUpdated = useCallback(
    (params) => {
      summaryGridDebugLog("rowDataUpdated", {
        sectionKey,
        displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        rowCount,
      });
    },
    [sectionKey, rowCount]
  );

  const handleModelUpdated = useCallback(
    (params) => {
      modelUpdateCountRef.current += 1;
      const updateCount = modelUpdateCountRef.current;
      if (updateCount <= 12 || updateCount % 25 === 0) {
        summaryGridDebugLog("modelUpdated", {
          sectionKey,
          updateCount,
          displayedRows: params?.api?.getDisplayedRowCount?.() ?? null,
        });
      }
    },
    [sectionKey]
  );

  useEffect(() => {
    summaryGridDebugLog("mount", { sectionKey });
    return () => summaryGridDebugLog("unmount", { sectionKey });
  }, [sectionKey]);

  useEffect(() => {
    summaryGridDebugLog("render", {
      sectionKey,
      renderCount: renderCountRef.current,
      rowCount,
      height,
    });
  });

  return (
    <GridShell
      sx={{
        height,
        "& .ag-cell": {
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          textAlign: "left",
          paddingTop: 0,
          paddingBottom: 0,
        },
        "& .ag-cell-wrapper": {
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          width: "100%",
          minHeight: "100%",
        },
        "& .ag-cell-value": {
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-start",
          textAlign: "left",
          width: "100%",
          minHeight: "100%",
        },
      }}
    >
      <AgGridReact
        rowData={rowData}
        columnDefs={columnDefs}
        defaultColDef={summaryDefaultColDef}
        pagination={false}
        animateRows={false}
        suppressCellFocus
        rowHeight={rowHeight}
        onGridReady={handleGridReady}
        onFirstDataRendered={handleFirstDataRendered}
        onRowDataUpdated={handleRowDataUpdated}
        onModelUpdated={handleModelUpdated}
        getRowId={summarySectionRowId}
        theme={DEVICE_DETAILS_GRID_THEME}
        style={SUMMARY_GRID_STYLE}
      />
    </GridShell>
  );
});

const SummaryGridPlaceholder = React.memo(function SummaryGridPlaceholder({ height }) {
  return (
    <GridShell
      sx={{
        height,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted, letterSpacing: 0.2 }}>
        Loading telemetry...
      </Typography>
    </GridShell>
  );
});

export default function DeviceSummary() {
  const loaderData = useLoaderData();
  const location = useLocation();
  const navigate = useNavigate();
  const { deviceId } = useParams();
  const { isAdmin } = useAuth();
  const initialDevice = location.state?.initialDevice;
  const device = useMemo(
    () =>
      initialDevice &&
      resolveDeviceId(initialDevice) &&
      String(resolveDeviceId(initialDevice)) === String(deviceId || "")
        ? initialDevice
        : { agent_guid: deviceId || null, hostname: deviceId || null },
    [deviceId, initialDevice]
  );
  const notifyOperator = useAppNotifications();
  const deviceSearchParams = useMemo(
    () => new URLSearchParams(location.search || ""),
    [location.search]
  );
  const rawTabParam = useMemo(
    () => String(deviceSearchParams.get("tab") || "").trim().toLowerCase(),
    [deviceSearchParams]
  );
  const rawViewParam = useMemo(
    () => String(deviceSearchParams.get("view") || "").trim().toLowerCase(),
    [deviceSearchParams]
  );
  const legacyWorkspaceTarget = LEGACY_TAB_TO_WORKSPACE[rawTabParam] || null;
  const activeWorkspaceKey = normalizeWorkspaceKey(
    WORKSPACE_KEYS.has(rawTabParam) ? rawTabParam : legacyWorkspaceTarget?.workspace,
    "inventory"
  );
  const activeWorkspaceView = normalizeWorkspaceView(
    activeWorkspaceKey,
    rawViewParam || legacyWorkspaceTarget?.view || ""
  );
  const setActiveWorkspace = useCallback(
    (workspaceKey, viewKey = "") => {
      const search = createDeviceWorkspaceSearch(location.search, workspaceKey, viewKey);
      navigate(
        {
          pathname: location.pathname,
          search,
        },
        { replace: false, state: location.state }
      );
    },
    [location.pathname, location.search, location.state, navigate]
  );
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const legacyTab = String(params.get("tab") || "").trim().toLowerCase();
    if (!deviceId) {
      return;
    }
    if (legacyTab === "remote_desktop" || legacyTab === "vnc") {
      params.delete("tab");
      navigate(
        {
          pathname: APP_PATHS.deviceRemoteDesktop(deviceId),
          search: params.toString() ? `?${params.toString()}` : "",
        },
        { replace: true, state: location.state }
      );
      return;
    }
    if (!legacyTab || WORKSPACE_KEYS.has(legacyTab)) {
      return;
    }
    const target = LEGACY_TAB_TO_WORKSPACE[legacyTab];
    if (!target?.workspace) {
      return;
    }
    params.set("tab", target.workspace);
    if (target.view && !String(params.get("view") || "").trim()) {
      params.set("view", target.view);
    }
    navigate(
      {
        pathname: location.pathname,
        search: params.toString() ? `?${params.toString()}` : "",
      },
      { replace: true, state: location.state }
    );
  }, [deviceId, location.pathname, location.search, location.state, navigate]);

  const loaderSnapshot = useMemo(() => {
    const snapshot = loaderData?.snapshot;
    if (!snapshot || typeof snapshot !== "object") {
      return null;
    }
    const snapshotDeviceId = String(snapshot.deviceId || "").trim();
    if (!snapshotDeviceId || snapshotDeviceId === String(deviceId || "").trim()) {
      return snapshot;
    }
    return null;
  }, [deviceId, loaderData]);

  const [agent, setAgent] = useState(() => loaderSnapshot?.agent || device || {});
  const [details, setDetails] = useState(() => loaderSnapshot?.details || {});
  const [meta, setMeta] = useState(() => loaderSnapshot?.meta || {});
  const [description, setDescription] = useState(() => String(loaderSnapshot?.description || ""));
  const [connectionType, setConnectionType] = useState(() => String(loaderSnapshot?.connectionType || ""));
  const [connectionEndpoint, setConnectionEndpoint] = useState(
    () => String(loaderSnapshot?.connectionEndpoint || "")
  );
  const [connectionDraft, setConnectionDraft] = useState(
    () => String(loaderSnapshot?.connectionEndpoint || "")
  );
  const [connectionSaving, setConnectionSaving] = useState(false);
  const [connectionMessage, setConnectionMessage] = useState("");
  const [connectionError, setConnectionError] = useState("");
  const [loadError, setLoadError] = useState(() => String(loaderData?.initialError || ""));
  const [summaryDataReady, setSummaryDataReady] = useState(() => Boolean(loaderSnapshot));
  const [tunnelInfo, setTunnelInfo] = useState(TUNNEL_INFO_IDLE);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [agentManagementAnchor, setAgentManagementAnchor] = useState(null);
  const [roleHealthAnchor, setRoleHealthAnchor] = useState(null);
  const [expandedDeviceNavSections, setExpandedDeviceNavSections] = useState({
    inventory: true,
    backend: true,
    protection: true,
    history: true,
    metadata: true,
  });
  const [releaseChannelMenuPosition, setReleaseChannelMenuPosition] = useState(null);
  const [agentBranchMenuPosition, setAgentBranchMenuPosition] = useState(null);
  const [agentBranchDraft, setAgentBranchDraft] = useState("");
  const [agentBranchChannelDialogOpen, setAgentBranchChannelDialogOpen] = useState(false);
  const [agentBranchRows, setAgentBranchRows] = useState([]);
  const [agentBranchesLoading, setAgentBranchesLoading] = useState(false);
  const [agentBranchLoadError, setAgentBranchLoadError] = useState("");
  const [draftAgentChannel, setDraftAgentChannel] = useState("stable");
  const [draftAgentBranch, setDraftAgentBranch] = useState("main");
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [updateAgentBusy, setUpdateAgentBusy] = useState(false);
  const [releaseChannelSaving, setReleaseChannelSaving] = useState(false);
  const [agentBranchSaving, setAgentBranchSaving] = useState(false);
  const [agentBranchChannelSaving, setAgentBranchChannelSaving] = useState(false);
  const [historyRefreshToken, setHistoryRefreshToken] = useState(0);
  const softwareRefreshTimersRef = useRef([]);
  // Snapshotted status for the lifetime of this page
  const [lockedStatus, setLockedStatus] = useState(() => {
    if (loaderSnapshot?.lockedStatus) {
      return loaderSnapshot.lockedStatus;
    }
    // Prefer status provided by the device list row if available
    if (device?.status) return device.status;
    // Fallback: compute once from the provided lastSeen timestamp
    const tsSec = device?.lastSeen;
    return statusFromHeartbeat(tsSec);
  });
  const descriptionDraftRef = useRef(String(loaderSnapshot?.description || ""));
  const loadedDescriptionRef = useRef(String(loaderSnapshot?.description || ""));
  const connectionDraftRef = useRef(String(loaderSnapshot?.connectionEndpoint || ""));
  const loadedConnectionEndpointRef = useRef(String(loaderSnapshot?.connectionEndpoint || ""));
  const pageRenderCountRef = useRef(0);
  const summaryObserverEventCountRef = useRef(0);
  const pendingSummaryScrollSectionRef = useRef("");
  const activeSummaryNavSectionRef = useRef(DEFAULT_SUMMARY_SECTION_KEY);
  pageRenderCountRef.current += 1;
  const summary = details.summary || {};

  useEffect(() => {
    descriptionDraftRef.current = description;
  }, [description]);

  useEffect(() => {
    connectionDraftRef.current = connectionDraft;
  }, [connectionDraft]);

  useEffect(() => {
    if (!isSummaryGridDebugEnabled()) return;
    summaryGridDebugLog("debugEnabled", {
      queryParam: "debugSummaryGrid=1",
      storageKey: SUMMARY_GRID_DEBUG_STORAGE_KEY,
    });
  }, []);
  const tunnelDevice = useMemo(
    () => ({
      ...(device || {}),
      ...(agent || {}),
      summary,
      hostname: meta.hostname || summary.hostname || device?.hostname || agent?.hostname,
      agent_id: meta.agentId || summary.agent_id || agent?.agent_id || agent?.id || device?.agent_id || device?.agent_guid,
      agent_guid: meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || agent?.agent_guid || agent?.guid,
    }),
    [agent, device, meta.agentGuid, meta.agentId, meta.hostname, summary]
  );
  const tunnelAgentId = useMemo(() => {
    const raw = tunnelDevice?.agent_id ?? "";
    try {
      return String(raw).trim();
    } catch {
      return "";
    }
  }, [tunnelDevice]);
  const quickJobTargets = useMemo(() => {
    return normalizeQuickJobTargets([agent?.hostname, device?.hostname], {
      excludeValues: [deviceId, resolveDeviceId(agent), resolveDeviceId(device)],
    });
  }, [agent, device, deviceId]);
  const canLaunchQuickJob = quickJobTargets.length > 0;
  const [quickJobOpen, setQuickJobOpen] = useState(false);
  const openQuickJobDialog = useCallback(() => {
    setQuickJobOpen(true);
  }, []);
  const shouldPollTunnelStatus = Boolean(tunnelAgentId);
  useEffect(() => {
    setConnectionError("");
  }, [connectionDraft]);

  useEffect(() => {
    if (connectionType !== "ssh") {
      setConnectionMessage("");
      setConnectionError("");
    }
  }, [connectionType]);

  const applyDeviceSummarySnapshot = useCallback(
    (snapshot, { silent = false } = {}) => {
      if (!snapshot || typeof snapshot !== "object") {
        return;
      }

      setDetails(snapshot.details || {});
      setMeta(snapshot.meta || {});
      setConnectionType(String(snapshot.connectionType || ""));
      setConnectionEndpoint(String(snapshot.connectionEndpoint || ""));
      if (
        !silent ||
        String(connectionDraftRef.current || "").trim() ===
          String(loadedConnectionEndpointRef.current || "").trim()
      ) {
        setConnectionDraft(String(snapshot.connectionEndpoint || ""));
      }
      loadedConnectionEndpointRef.current = String(snapshot.connectionEndpoint || "");
      setConnectionMessage("");
      setConnectionError("");

      if (
        !silent ||
        String(descriptionDraftRef.current || "").trim() === String(loadedDescriptionRef.current || "").trim()
      ) {
        setDescription(String(snapshot.description || ""));
      }
      loadedDescriptionRef.current = String(snapshot.description || "");

      setAgent((previous) => ({
        ...(previous || {}),
        ...(snapshot.agent || {}),
      }));

      if (snapshot.lockedStatus) {
        setLockedStatus(snapshot.lockedStatus);
      } else if (snapshot.meta?.status) {
        setLockedStatus(snapshot.meta.status);
      } else if (snapshot.meta?.lastSeen) {
        setLockedStatus(statusFromHeartbeat(snapshot.meta.lastSeen));
      }

      setLoadError("");
    },
    []
  );

  const reloadDeviceSummarySnapshot = useCallback(
    async ({ silent = false, includeAgents = false } = {}) => {
      const guid = device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid;
      const hostname = device?.hostname || device?.summary?.hostname;
      if (!device || (!guid && !hostname)) {
        return;
      }
      if (!silent) {
        setSummaryDataReady(false);
      }
      try {
        const snapshot = await fetchDeviceSummarySnapshot({
          device,
          deviceId,
          includeAgents,
        });
        applyDeviceSummarySnapshot(snapshot, { silent });
        if (!silent) {
          setSummaryDataReady(true);
        }
      } catch (e) {
        console.warn("Failed to reload device info", e);
        if (!silent) {
          setLoadError(String(e?.message || "Unable to load device details."));
          setSummaryDataReady(true);
        }
      }
    },
    [applyDeviceSummarySnapshot, device, deviceId]
  );

  const requestSoftwareDataRefresh = useCallback(
    ({ burst = false } = {}) => {
      void reloadDeviceSummarySnapshot({ silent: true, includeAgents: false });
      softwareRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
      softwareRefreshTimersRef.current = [];
      if (!burst) {
        return;
      }
      [1200, 2500, 5000, 10000, 20000].forEach((delayMs) => {
        const timerId = window.setTimeout(() => {
          void reloadDeviceSummarySnapshot({ silent: true, includeAgents: false });
        }, delayMs);
        softwareRefreshTimersRef.current.push(timerId);
      });
    },
    [reloadDeviceSummarySnapshot]
  );

  useEffect(
    () => () => {
      softwareRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
      softwareRefreshTimersRef.current = [];
    },
    []
  );

  useEffect(() => {
    if (!loaderSnapshot) {
      setLoadError(String(loaderData?.initialError || ""));
      return;
    }
    applyDeviceSummarySnapshot(loaderSnapshot, { silent: false });
    setSummaryDataReady(true);
    setLoadError(String(loaderData?.initialError || ""));
  }, [applyDeviceSummarySnapshot, loaderData, loaderSnapshot]);

  useEffect(() => {
    let canceled = false;
    let inFlight = false;
    let pollingTimer = null;
    if (!tunnelAgentId) {
      setTunnelInfo((prev) => (tunnelInfoMatches(prev, TUNNEL_INFO_IDLE) ? prev : TUNNEL_INFO_IDLE));
      return () => {};
    }
    const loadTunnelStatus = async () => {
      if (inFlight) return;
      inFlight = true;
      try {
        const resp = await fetch(
          `/api/tunnel/status?agent_id=${encodeURIComponent(tunnelAgentId)}`
        );
        const data = await resp.json().catch(() => ({}));
        if (canceled) return;
        if (!resp.ok) {
          const nextTunnelInfo = {
            status: "error",
            tunnel_id: "",
            virtual_ip: "",
            agent_socket: false,
            listener_healthy: false,
            recovery_in_progress: false,
            last_recovery_attempt_at: null,
            last_recovery_attempt_at_iso: "",
          };
          setTunnelInfo((prev) => (tunnelInfoMatches(prev, nextTunnelInfo) ? prev : nextTunnelInfo));
          return;
        }
        const nextTunnelInfo =
          data?.status === "down"
            ? {
                status: "down",
                tunnel_id: "",
                virtual_ip: "",
                agent_socket: Boolean(data?.agent_socket),
                listener_healthy: Boolean(data?.listener_healthy),
                recovery_in_progress: Boolean(data?.recovery_in_progress),
                last_recovery_attempt_at: data?.last_recovery_attempt_at ?? null,
                last_recovery_attempt_at_iso: data?.last_recovery_attempt_at_iso || "",
              }
            : normalizeTunnelInfoState(data?.status ? data : { ...data, status: "up" });
        setTunnelInfo((prev) => (tunnelInfoMatches(prev, nextTunnelInfo) ? prev : nextTunnelInfo));
      } catch {
        if (!canceled) {
          const nextTunnelInfo = {
            status: "error",
            tunnel_id: "",
            virtual_ip: "",
            agent_socket: false,
            listener_healthy: false,
            recovery_in_progress: false,
            last_recovery_attempt_at: null,
            last_recovery_attempt_at_iso: "",
          };
          setTunnelInfo((prev) => (tunnelInfoMatches(prev, nextTunnelInfo) ? prev : nextTunnelInfo));
        }
      } finally {
        inFlight = false;
      }
    };
    loadTunnelStatus();
    if (shouldPollTunnelStatus) {
      pollingTimer = setInterval(loadTunnelStatus, TUNNEL_STATUS_POLL_INTERVAL_MS);
    }
    return () => {
      canceled = true;
      if (pollingTimer) clearInterval(pollingTimer);
    };
  }, [tunnelAgentId, shouldPollTunnelStatus]);

  useEffect(() => {
    let canceled = false;
    let inFlight = false;
    let pollingTimer = null;

    if (loaderSnapshot?.lockedStatus) {
      setLockedStatus(loaderSnapshot.lockedStatus);
    } else if (device) {
      setLockedStatus(device.status || statusFromHeartbeat(device.lastSeen));
    }

    const guid = device?.agent_guid || device?.guid || device?.agentGuid || device?.summary?.agent_guid;
    const hostname = device?.hostname || device?.summary?.hostname;
    if (!device || (!guid && !hostname)) {
      setSummaryDataReady(true);
      return () => {
        canceled = true;
      };
    }

    if (!loaderSnapshot) {
      setSummaryDataReady(false);
    }

    const load = async ({ silent = false, includeAgents = false } = {}) => {
      if (inFlight) return;
      inFlight = true;
      try {
        await reloadDeviceSummarySnapshot({ silent, includeAgents });
        if (canceled) return;
      } catch (e) {
        if (canceled) return;
        console.warn("Failed to load device info", e);
        if (!silent) {
          setMeta({});
          setLoadError(String(e?.message || "Unable to load device details."));
        }
      } finally {
        inFlight = false;
        if (!canceled && !silent) {
          setSummaryDataReady(true);
        }
      }
    };
    if (!loaderSnapshot) {
      load({ silent: false, includeAgents: true });
    }
    pollingTimer = setInterval(() => {
      load({ silent: true, includeAgents: false });
    }, DEVICE_DETAILS_POLL_INTERVAL_MS);
    return () => {
      canceled = true;
      if (pollingTimer) clearInterval(pollingTimer);
    };
  }, [applyDeviceSummarySnapshot, device, deviceId, loaderSnapshot]);

  const activityHostname = useMemo(() => {
    return (meta?.hostname || summary.hostname || agent?.hostname || device?.hostname || "").trim();
  }, [meta?.hostname, summary.hostname, agent?.hostname, device?.hostname]);
  const quickJobTargetRecords = useMemo(
    () =>
      quickJobTargets.map((hostname) => ({
        hostname,
        device_guid: meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || "",
        site_id: meta.siteId ?? summary.site_id ?? device?.site_id ?? agent?.site_id ?? null,
        site_name: meta.siteName || summary.site_name || device?.site_name || agent?.site_name || "",
      })),
    [
      agent?.agent_guid,
      agent?.site_id,
      agent?.site_name,
      device?.agent_guid,
      device?.site_id,
      device?.site_name,
      meta.agentGuid,
      meta.siteId,
      meta.siteName,
      quickJobTargets,
      summary.agent_guid,
      summary.site_id,
      summary.site_name,
    ]
  );

  useEffect(() => {
    const socket = typeof window !== "undefined" ? window.BorealisSocket : null;
    const expectedHost = String(activityHostname || "").trim().toLowerCase();
    if (!socket || !expectedHost) return undefined;
    let reloadTimer = null;

    const handleInventoryChanged = (payload = {}) => {
      const payloadHost = String(payload?.hostname || "").trim().toLowerCase();
      const payloadChange = String(payload?.change || "").trim().toLowerCase();
      if (!payloadHost || payloadHost !== expectedHost) return;
      if (payloadChange && payloadChange !== "software_updated" && payloadChange !== "updated") return;
      if (reloadTimer) {
        window.clearTimeout(reloadTimer);
      }
      reloadTimer = window.setTimeout(() => {
        void reloadDeviceSummarySnapshot({ silent: true, includeAgents: false });
      }, 250);
    };

    socket.on("device_inventory_changed", handleInventoryChanged);
    return () => {
      if (reloadTimer) {
        window.clearTimeout(reloadTimer);
      }
      socket.off("device_inventory_changed", handleInventoryChanged);
    };
  }, [activityHostname, reloadDeviceSummarySnapshot]);

  const saveConnectionEndpoint = useCallback(async () => {
    if (connectionType !== "ssh") return;
    const host = activityHostname;
    if (!host) return;
    const trimmed = connectionDraft.trim();
    if (!trimmed) {
      setConnectionError("Address is required.");
      return;
    }
    if (trimmed === connectionEndpoint.trim()) {
      setConnectionMessage("No changes to save.");
      return;
    }
    setConnectionSaving(true);
    setConnectionError("");
    setConnectionMessage("");
    try {
      const resp = await fetch(`/api/ssh_devices/${encodeURIComponent(host)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ address: trimmed })
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      const updated = data?.device?.connection_endpoint || trimmed;
      setConnectionEndpoint(updated);
      setConnectionDraft(updated);
      loadedConnectionEndpointRef.current = updated;
      setMeta((prev) => ({ ...(prev || {}), connectionEndpoint: updated }));
      setConnectionMessage("SSH endpoint updated.");
      setTimeout(() => setConnectionMessage(""), 3000);
    } catch (err) {
      setConnectionError(String(err.message || err));
    } finally {
      setConnectionSaving(false);
    }
  }, [connectionType, connectionDraft, connectionEndpoint, activityHostname]);

  const clearHistory = useCallback(async () => {
    if (!activityHostname) return;
    try {
      const resp = await fetch(`/api/device/activity/${encodeURIComponent(activityHostname)}`, { method: "DELETE" });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      setHistoryRefreshToken((current) => current + 1);
    } catch (e) {
      console.warn("Failed to clear activity history", e);
    }
  }, [activityHostname]);

  const requestAgentUpdate = useCallback(async () => {
    const targetGuid = meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || "";
    const targetHost = activityHostname || targetGuid;
    if (!targetGuid || updateAgentBusy) return;
    setUpdateAgentBusy(true);
    try {
      const resp = await fetch("/api/devices/agent-maintenance", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: "update_now",
          guids: [targetGuid],
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        let message = String(data?.message || data?.error || `HTTP ${resp.status}`);
        if (String(data?.error || "").trim() === "agent_unavailable") {
          message = "The agent management socket is not connected, so Borealis could not start the local AutoUpdater.";
        }
        throw new Error(message);
      }
      await notifyOperator({
        title: "AutoUpdater Requested",
        message: `Queued ${targetHost} to start its local AutoUpdater task immediately.`,
        icon: "update",
        variant: "info",
      });
    } catch (err) {
      await notifyOperator({
        title: "Agent Update Failed",
        message: `Could not start the AutoUpdater for ${targetHost}: ${String(err?.message || err)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setUpdateAgentBusy(false);
    }
  }, [activityHostname, device?.agent_guid, device?.guid, meta.agentGuid, notifyOperator, summary.agent_guid, updateAgentBusy]);

  const fetchAgentBranches = useCallback(async () => {
    setAgentBranchesLoading(true);
    setAgentBranchLoadError("");
    try {
      const nextRows = await fetchAgentBranchRows();
      setAgentBranchRows(nextRows);
      if (!nextRows.length) {
        setAgentBranchLoadError("No GitHub branches returned.");
      }
    } catch (error) {
      setAgentBranchRows([]);
      setAgentBranchLoadError(error instanceof Error ? error.message : "GitHub branch lookup failed.");
    } finally {
      setAgentBranchesLoading(false);
    }
  }, []);

  const openAgentBranchChannelDialog = useCallback(() => {
    if (!isAdmin || agentBranchChannelSaving) return;
    const currentChannel = normalizeAgentReleaseChannel(
      meta.agentReleaseChannel ||
        summary.agent_release_channel ||
        meta.agentReleaseChannelEffective ||
        summary.agent_release_channel_effective ||
        meta.agentReleaseChannelOverride ||
        summary.agent_release_channel_override ||
        ""
    );
    const currentBranch = normalizeAgentBranch(currentChannel, meta.agentBranch || summary.agent_branch || "");
    setDraftAgentChannel(currentChannel);
    setDraftAgentBranch(currentBranch);
    setAgentBranchLoadError("");
    setAgentBranchChannelDialogOpen(true);
    void fetchAgentBranches();
  }, [
    agentBranchChannelSaving,
    fetchAgentBranches,
    isAdmin,
    meta.agentBranch,
    meta.agentReleaseChannel,
    meta.agentReleaseChannelEffective,
    meta.agentReleaseChannelOverride,
    summary.agent_branch,
    summary.agent_release_channel,
    summary.agent_release_channel_effective,
    summary.agent_release_channel_override,
  ]);

  const closeAgentBranchChannelDialog = useCallback(() => {
    if (agentBranchChannelSaving) return;
    setAgentBranchChannelDialogOpen(false);
  }, [agentBranchChannelSaving]);

  const applyAgentBranchChannel = useCallback(async () => {
    const targetGuid = meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || "";
    if (!isAdmin || !targetGuid || agentBranchChannelSaving) return;
    const targetChannel = normalizeAgentReleaseChannel(draftAgentChannel);
    if (!["stable", "unstable"].includes(targetChannel)) {
      setAgentBranchLoadError("Choose stable or unstable before applying.");
      return;
    }
    const targetBranch = normalizeAgentBranch(targetChannel, draftAgentBranch);
    setAgentBranchChannelSaving(true);
    setAgentBranchLoadError("");
    try {
      const resp = await fetch("/api/devices/agent-maintenance", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: "switch_branch_channel",
          guids: [targetGuid],
          release_channel: targetChannel,
          branch: targetBranch,
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
      }
      setAgentBranchChannelDialogOpen(false);
      const snapshot = await fetchDeviceSummarySnapshot({
        device,
        deviceId,
        includeAgents: false,
      });
      applyDeviceSummarySnapshot(snapshot, { silent: true });
      await notifyOperator({
        title: "Agent Branch/Channel Queued",
        message: `<b>${activityHostname || targetGuid}</b> queued for <b>${targetChannel} - ${targetBranch}</b>.`,
        icon: "update",
        variant: "success",
      });
    } catch (error) {
      setAgentBranchLoadError(String(error?.message || error || "Agent branch/channel switch failed."));
      await notifyOperator({
        title: "Agent Branch/Channel Failed",
        message: `Could not queue the branch/channel switch: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setAgentBranchChannelSaving(false);
    }
  }, [
    activityHostname,
    agentBranchChannelSaving,
    applyDeviceSummarySnapshot,
    device,
    deviceId,
    draftAgentBranch,
    draftAgentChannel,
    isAdmin,
    meta.agentGuid,
    notifyOperator,
    summary.agent_guid,
  ]);

  const openReleaseChannelMenu = useCallback(
    (event) => {
      if (!isAdmin || releaseChannelSaving) return;
      if (event?.preventDefault) event.preventDefault();
      if (event?.stopPropagation) event.stopPropagation();
      const rect = event?.currentTarget?.getBoundingClientRect?.();
      if (rect) {
        setReleaseChannelMenuPosition({
          top: Math.round(rect.bottom + 4),
          left: Math.round(rect.left),
        });
        return;
      }
      setReleaseChannelMenuPosition(
        typeof event?.clientX === "number" && typeof event?.clientY === "number"
          ? { top: Math.round(event.clientY + 4), left: Math.round(event.clientX) }
          : null
      );
    },
    [isAdmin, releaseChannelSaving]
  );

  const closeReleaseChannelMenu = useCallback(() => {
    setReleaseChannelMenuPosition(null);
  }, []);

  const openAgentBranchMenu = useCallback(
    (event) => {
      if (!isAdmin || agentBranchSaving) return;
      if (event?.preventDefault) event.preventDefault();
      if (event?.stopPropagation) event.stopPropagation();
      setAgentBranchDraft(
        String(meta.agentBranch || summary.agent_branch || "main").trim() || "main"
      );
      const rect = event?.currentTarget?.getBoundingClientRect?.();
      if (rect) {
        setAgentBranchMenuPosition({
          top: Math.round(rect.bottom + 4),
          left: Math.round(rect.left),
        });
        return;
      }
      setAgentBranchMenuPosition(
        typeof event?.clientX === "number" && typeof event?.clientY === "number"
          ? { top: Math.round(event.clientY + 4), left: Math.round(event.clientX) }
          : null
      );
    },
    [agentBranchSaving, isAdmin, meta.agentBranch, summary.agent_branch]
  );

  const closeAgentBranchMenu = useCallback(() => {
    setAgentBranchMenuPosition(null);
  }, []);

  const applyAgentReleaseChannelOverride = useCallback(
    async (channel) => {
      const targetGuid = meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || "";
      if (!isAdmin || !targetGuid || releaseChannelSaving) return;
      const normalizedChannel = String(channel || "").trim().toLowerCase();
      setReleaseChannelSaving(true);
      try {
        const resp = await fetch(`/api/devices/${encodeURIComponent(targetGuid)}/agent-release-channel`, {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ channel: normalizedChannel || null }),
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
        }
        const snapshot = await fetchDeviceSummarySnapshot({
          device,
          deviceId,
          includeAgents: false,
        });
        applyDeviceSummarySnapshot(snapshot, { silent: true });
        const resolvedChannel =
          String(data?.agent_release_channel || normalizedChannel || "stable")
            .trim()
            .toLowerCase() || "stable";
        const targetLabel = activityHostname || targetGuid;
        await notifyOperator({
          title: "Agent Channel Updated",
          message: `<b>${targetLabel}</b> Release Channel changed to <b>${resolvedChannel}</b>`,
          icon: "info",
          variant: "success",
        });
      } catch (err) {
        await notifyOperator({
          title: "Agent Channel Update Failed",
          message: `Could not update the agent release channel: ${String(err?.message || err)}`,
          icon: "error",
          variant: "error",
        });
      } finally {
        setReleaseChannelSaving(false);
      }
    },
    [
      activityHostname,
      applyDeviceSummarySnapshot,
      device,
      deviceId,
      isAdmin,
      meta.agentGuid,
      notifyOperator,
      releaseChannelSaving,
      summary.agent_guid,
    ]
  );

  const handleReleaseChannelSelection = useCallback(
    (channel) => {
      closeReleaseChannelMenu();
      applyAgentReleaseChannelOverride(channel);
    },
    [applyAgentReleaseChannelOverride, closeReleaseChannelMenu]
  );

  const applyAgentBranchOverride = useCallback(
    async () => {
      const targetGuid = meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || "";
      const normalizedBranch = String(agentBranchDraft || "").trim();
      if (!isAdmin || !targetGuid || agentBranchSaving) return;
      if (!normalizedBranch) {
        await notifyOperator({
          title: "Agent Branch Required",
          message: "Enter an unstable branch before applying the agent branch change.",
          icon: "error",
          variant: "error",
        });
        return;
      }
      setAgentBranchSaving(true);
      try {
        const resp = await fetch(`/api/devices/${encodeURIComponent(targetGuid)}/agent-release-channel`, {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ channel: "unstable", branch: normalizedBranch }),
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.message || data?.error || `HTTP ${resp.status}`);
        }
        setAgentBranchMenuPosition(null);
        const snapshot = await fetchDeviceSummarySnapshot({
          device,
          deviceId,
          includeAgents: false,
        });
        applyDeviceSummarySnapshot(snapshot, { silent: true });
        const resolvedBranch = String(data?.agent_branch || normalizedBranch).trim() || normalizedBranch;
        setMeta((prev) => ({
          ...(prev || {}),
          agentBranch: resolvedBranch,
          agentReleaseChannel: data?.agent_release_channel || "unstable",
          agentReleaseChannelOverride: data?.agent_release_channel_override || "unstable",
          agentReleaseChannelEffective: data?.agent_release_channel_effective || "unstable",
        }));
        setDetails((prev) => ({
          ...(prev || {}),
          summary: {
            ...(prev?.summary || {}),
            agent_branch: resolvedBranch,
            agent_release_channel: data?.agent_release_channel || "unstable",
            agent_release_channel_override: data?.agent_release_channel_override || "unstable",
            agent_release_channel_effective: data?.agent_release_channel_effective || "unstable",
          },
        }));
        const targetLabel = activityHostname || targetGuid;
        await notifyOperator({
          title: "Agent Branch Updated",
          message: `<b>${targetLabel}</b> Unstable Branch changed to <b>${resolvedBranch}</b>`,
          icon: "info",
          variant: "success",
        });
      } catch (err) {
        await notifyOperator({
          title: "Agent Branch Update Failed",
          message: `Could not update the agent unstable branch: ${String(err?.message || err)}`,
          icon: "error",
          variant: "error",
        });
      } finally {
        setAgentBranchSaving(false);
      }
    },
    [
      activityHostname,
      agentBranchDraft,
      agentBranchSaving,
      applyDeviceSummarySnapshot,
      device,
      deviceId,
      isAdmin,
      meta.agentGuid,
      notifyOperator,
      summary.agent_guid,
    ]
  );

  const saveDescription = async () => {
    const targetHost = meta.hostname || details.summary?.hostname;
    if (!targetHost) return;
    try {
      await fetch(`/api/device/description/${targetHost}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description })
      });
      loadedDescriptionRef.current = description;
      setDetails((d) => ({
        ...d,
        summary: { ...(d.summary || {}), description }
      }));
      setMeta((m) => ({ ...(m || {}), hostname: targetHost }));
    } catch (e) {
      console.warn("Failed to save description", e);
    }
  };

  const formatMac = (mac) => (mac ? mac.replace(/-/g, ":").toUpperCase() : "unknown");

  const formatBytes = (val) => {
    if (val === undefined || val === null || val === "unknown") return "unknown";
    let num = Number(val);
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    while (num >= 1024 && i < units.length - 1) {
      num /= 1024;
      i++;
    }
    return `${num.toFixed(1)} ${units[i]}`;
  };

  const formatTimestamp = useCallback((epochSec) => {
    const ts = Number(epochSec || 0);
    if (!ts) return "unknown";
    const d = new Date(ts * 1000);
    const mm = String(d.getMonth() + 1).padStart(2, "0");
    const dd = String(d.getDate()).padStart(2, "0");
    const yyyy = d.getFullYear();
    let hh = d.getHours();
    const ampm = hh >= 12 ? "PM" : "AM";
    hh = hh % 12 || 12;
    const min = String(d.getMinutes()).padStart(2, "0");
    return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
  }, []);

  const formatDateValue = useCallback((rawValue, emptyFallback = "unknown") => {
    if (rawValue === undefined || rawValue === null || String(rawValue).trim() === "") {
      return emptyFallback;
    }
    const rawText = String(rawValue).trim();
    const asNumber = Number(rawText);
    if (Number.isFinite(asNumber) && /^[0-9]+(\.[0-9]+)?$/.test(rawText)) {
      const ms = asNumber > 1e12 ? asNumber : asNumber * 1000;
      const date = new Date(ms);
      if (!Number.isNaN(date.getTime())) {
        const mm = String(date.getMonth() + 1).padStart(2, "0");
        const dd = String(date.getDate()).padStart(2, "0");
        const yyyy = date.getFullYear();
        let hh = date.getHours();
        const ampm = hh >= 12 ? "PM" : "AM";
        hh = hh % 12 || 12;
        const min = String(date.getMinutes()).padStart(2, "0");
        return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
      }
    }
    const date = new Date(rawText);
    if (!Number.isNaN(date.getTime())) {
      const mm = String(date.getMonth() + 1).padStart(2, "0");
      const dd = String(date.getDate()).padStart(2, "0");
      const yyyy = date.getFullYear();
      let hh = date.getHours();
      const ampm = hh >= 12 ? "PM" : "AM";
      hh = hh % 12 || 12;
      const min = String(date.getMinutes()).padStart(2, "0");
      return `${mm}/${dd}/${yyyy} @ ${hh}:${min} ${ampm}`;
    }
    return rawText;
  }, []);

  const markActiveSummaryNavSection = useCallback((sectionKey) => {
    if (typeof document === "undefined") return;
    const normalizedSectionKey = sectionKey ? normalizeSummarySectionKey(sectionKey) : "";
    activeSummaryNavSectionRef.current = normalizedSectionKey;
    document.querySelectorAll("[data-summary-nav-section]").forEach((node) => {
      if (!(node instanceof HTMLElement)) return;
      const isActive = Boolean(normalizedSectionKey) && node.dataset.summaryNavSection === normalizedSectionKey;
      node.dataset.summaryActive = isActive ? "true" : "false";
      node.classList.toggle("Mui-selected", isActive);
    });
  }, []);

  const scrollToSummarySection = useCallback((sectionKey) => {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    const normalizedSectionKey = normalizeSummarySectionKey(sectionKey);
    const target = document.getElementById(`device-summary-${normalizedSectionKey}`);
    if (!target) return;

    const docScroller = document.scrollingElement || document.documentElement;
    const scrollHost = document.getElementById("device-summary-workspace-scrollhost") || docScroller;

    summaryGridDebugLog("scrollToSection", {
      sectionKey: normalizedSectionKey,
      scrollHostTag: scrollHost?.tagName || "document",
    });

    markActiveSummaryNavSection(normalizedSectionKey);

    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        const nextTarget = document.getElementById(`device-summary-${normalizedSectionKey}`);
        nextTarget?.scrollIntoView?.({ behavior: "smooth", block: "start", inline: "nearest" });
      });
    });
  }, [markActiveSummaryNavSection]);

  const scrollHardwareSummaryToTop = useCallback(() => {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    const docScroller = document.scrollingElement || document.documentElement;
    const scrollHost = document.getElementById("device-summary-workspace-scrollhost") || docScroller;
    markActiveSummaryNavSection(DEFAULT_SUMMARY_SECTION_KEY);
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        if (scrollHost === docScroller) {
          window.scrollTo({ top: 0, behavior: "smooth" });
        } else {
          scrollHost.scrollTo?.({ top: 0, behavior: "smooth" });
        }
        document
          .getElementById("device-summary-top-level")
          ?.scrollIntoView?.({ behavior: "smooth", block: "start", inline: "nearest" });
      });
    });
  }, [markActiveSummaryNavSection]);

  const requestSummarySectionScroll = useCallback(
    (sectionKey = "top-level") => {
      const normalizedSectionKey = normalizeSummarySectionKey(sectionKey);
      summaryGridDebugLog("sidebarSummarySectionClick", { sectionKey: normalizedSectionKey });
      markActiveSummaryNavSection(normalizedSectionKey);
      if (activeWorkspaceKey === "inventory" && activeWorkspaceView === "summary") {
        if (normalizedSectionKey === DEFAULT_SUMMARY_SECTION_KEY) {
          scrollHardwareSummaryToTop();
        } else {
          scrollToSummarySection(normalizedSectionKey);
        }
        return;
      }
      pendingSummaryScrollSectionRef.current = normalizedSectionKey;
      setActiveWorkspace("inventory", "summary");
    },
    [
      activeWorkspaceKey,
      activeWorkspaceView,
      markActiveSummaryNavSection,
      scrollHardwareSummaryToTop,
      scrollToSummarySection,
      setActiveWorkspace,
    ]
  );

  useEffect(() => {
    if (activeWorkspaceKey !== "inventory" || activeWorkspaceView !== "summary") {
      markActiveSummaryNavSection("");
      return;
    }
    markActiveSummaryNavSection(activeSummaryNavSectionRef.current || DEFAULT_SUMMARY_SECTION_KEY);
  }, [activeWorkspaceKey, activeWorkspaceView, markActiveSummaryNavSection]);

  useEffect(() => {
    const pendingSummaryScrollSection = pendingSummaryScrollSectionRef.current;
    if (!pendingSummaryScrollSection) return undefined;
    if (activeWorkspaceKey !== "inventory" || activeWorkspaceView !== "summary") return undefined;
    if (typeof window === "undefined") return undefined;
    const frame = window.requestAnimationFrame(() => {
      pendingSummaryScrollSectionRef.current = "";
      if (pendingSummaryScrollSection === "top-level") {
        scrollHardwareSummaryToTop();
      } else {
        scrollToSummarySection(pendingSummaryScrollSection);
      }
    });
    return () => window.cancelAnimationFrame(frame);
  }, [
    activeWorkspaceKey,
    activeWorkspaceView,
    scrollHardwareSummaryToTop,
    scrollToSummarySection,
  ]);

  useEffect(() => {
    if (typeof window === "undefined" || typeof document === "undefined" || typeof IntersectionObserver === "undefined") {
      return undefined;
    }
    if (activeWorkspaceKey !== "inventory" || activeWorkspaceView !== "summary") return undefined;
    const sectionElements = SUMMARY_SECTIONS
      .map((section) => document.getElementById(`device-summary-${section.key}`))
      .filter(Boolean);
    if (!sectionElements.length) return undefined;
    summaryGridDebugLog("sidebarSummaryObserverStart", {
      sections: sectionElements.map((el) => el.id),
    });

    const observer = new IntersectionObserver(
      (entries) => {
        summaryObserverEventCountRef.current += 1;
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio);
        if (!visible.length) return;
        const nextKey = String(visible[0].target.id || "").replace("device-summary-", "");
        if (!nextKey) return;
        summaryGridDebugLog("sidebarSummaryObserverSectionChange", {
          to: nextKey,
          eventCount: summaryObserverEventCountRef.current,
          ratio: Number(visible[0].intersectionRatio || 0).toFixed(3),
        });
        markActiveSummaryNavSection(nextKey);
      },
      {
        root: null,
        rootMargin: "-16px 0px -60% 0px",
        threshold: [0.15, 0.35, 0.6],
      }
    );

    sectionElements.forEach((el) => observer.observe(el));
    return () => {
      summaryGridDebugLog("sidebarSummaryObserverStop");
      observer.disconnect();
    };
  }, [activeWorkspaceKey, activeWorkspaceView, markActiveSummaryNavSection]);

  const softwareRows = useMemo(() => details.software || [], [details.software]);

  // Build a best-effort CPU display from summary fields
  const cpuInfo = useMemo(() => {
    const cpu = details.cpu || summary.cpu || {};
    const cores = cpu.logical_cores || cpu.cores || cpu.physical_cores;
    let ghz = cpu.base_clock_ghz;
    if (!ghz && typeof (summary.processor || '') === 'string') {
      const m = String(summary.processor).match(/\(([^)]*?)ghz\)/i);
      if (m && m[1]) {
        const n = parseFloat(m[1]);
        if (!Number.isNaN(n)) ghz = n;
      }
    }
    const name = (cpu.name || '').trim();
    const fromProcessor = (summary.processor || '').trim();
    const display = fromProcessor || [name, ghz ? `(${Number(ghz).toFixed(1)}GHz)` : null, cores ? `@ ${cores} Cores` : null].filter(Boolean).join(' ');
    return { cores, ghz, name, display };
  }, [summary]);

  const defaultGridColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: "agTextColumnFilter",
      flex: 1,
      minWidth: 140,
    }),
    []
  );

  const Island = ({ title = "", icon = null, meta = "", children, sx }) => (
    <Box
      sx={{
        p: 2,
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: "rgba(7,11,24,0.58)",
        boxShadow: "0 18px 40px rgba(2,6,23,0.55)",
        mb: 1.5,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        ...(sx || {}),
      }}
    >
      {title || icon || meta ? (
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 1,
            mb: 1.4,
            flexWrap: "wrap",
          }}
        >
          <Stack direction="row" spacing={0.75} alignItems="center">
            {icon ? (
              <Box sx={{ color: MAGIC_UI.accentA, display: "inline-flex", alignItems: "center" }}>
                {icon}
              </Box>
            ) : null}
            {title ? (
              <Typography
                variant="caption"
                sx={{
                  color: MAGIC_UI.accentA,
                  fontWeight: 600,
                  fontSize: "0.82rem",
                  letterSpacing: 0.3,
                  textTransform: "uppercase",
                  display: "block",
                }}
              >
                {title}
              </Typography>
            ) : null}
          </Stack>
          {meta ? (
            <Typography
              variant="caption"
              sx={{ color: "rgba(226,232,240,0.72)", fontSize: "0.74rem", letterSpacing: 0.15 }}
            >
              {meta}
            </Typography>
          ) : null}
        </Box>
      ) : null}
      <Box sx={{ flexGrow: 1, minHeight: 0 }}>{children}</Box>
    </Box>
  );

  const deviceMetricData = useMemo(() => {
    const cpuMain = (cpuInfo.name || (summary.processor || "") || "").split("\n")[0] || "Unknown CPU";
    const cpuSub =
      cpuInfo.ghz || cpuInfo.cores
        ? `${cpuInfo.ghz ? `${Number(cpuInfo.ghz).toFixed(2)}GHz ` : ""}${
            cpuInfo.cores ? `(${cpuInfo.cores}-Cores)` : ""
          }`.trim()
        : "";
    let totalRam = summary.total_ram;
    if (!totalRam && Array.isArray(details.memory)) {
      totalRam = details.memory.reduce((a, m) => a + (Number(m.capacity || 0) || 0), 0);
    }
    const memVal = totalRam ? `${formatBytes(totalRam)}` : "Unknown";
    let memSpeed = "";
    try {
      const speeds = (details.memory || [])
        .map((m) => parseInt(String(m.speed || "").replace(/[^0-9]/g, ""), 10))
        .filter((v) => !Number.isNaN(v) && v > 0);
      if (speeds.length) memSpeed = `Speed: ${Math.max(...speeds)} MT/s`;
    } catch {}
    const toNum = (val) => {
      if (val === undefined || val === null) return undefined;
      if (typeof val === "number") return Number.isNaN(val) ? undefined : val;
      const parsed = parseFloat(String(val).replace(/[^0-9.]+/g, ""));
      return Number.isNaN(parsed) ? undefined : parsed;
    };
    let storageTotalBytes = 0;
    let storageVolumeCount = 0;
    if (Array.isArray(details.storage)) {
      details.storage.forEach((drive) => {
        const total = toNum(drive?.total);
        if (total === undefined) return;
        storageTotalBytes += total;
        storageVolumeCount += 1;
      });
    }
    const storageMain = storageTotalBytes > 0 ? `${formatBytes(storageTotalBytes)}` : "Unknown";
    const storageSub =
      storageVolumeCount > 0
        ? `${storageVolumeCount} ${storageVolumeCount === 1 ? "Volume" : "Volumes"}`
        : "";
    const primaryIp = (summary.internal_ip || "").trim();
    let nic = null;
    if (Array.isArray(details.network)) {
      nic = details.network.find((n) => (n.ips || []).includes(primaryIp)) || details.network[0] || null;
    }
    const normalizeSpeed = (val) => {
      const s = String(val || "").trim();
      if (!s) return "Unknown";
      const low = s.toLowerCase();
      if (low.includes("gbps") || low.includes("mbps")) return s;
      const m = low.match(/(\d+\.?\d*)\s*([gmk]?)(bps)/);
      if (!m) return s;
      let num = parseFloat(m[1]);
      const unit = m[2];
      if (unit === "g") return `${num} Gbps`;
      if (unit === "m") return `${num} Mbps`;
      if (unit === "k") return `${(num / 1000).toFixed(1)} Mbps`;
      if (num >= 1e9) return `${(num / 1e9).toFixed(1)} Gbps`;
      if (num >= 1e6) return `${(num / 1e6).toFixed(0)} Mbps`;
      return s;
    };
    const netVal = nic ? normalizeSpeed(nic.link_speed || nic.speed) : "Unknown";
    return {
      cpuMain,
      cpuSub,
      memVal,
      memSpeed,
      storageMain,
      storageSub,
      netVal,
      nicLabel: nic?.adapter || " ",
    };
  }, [summary, details.memory, details.storage, details.network, cpuInfo]);

  const renderDeviceSummaryTab = () => {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 3, minHeight: 0, minWidth: 0, width: "100%" }}>
        <Box
          sx={{
            borderRadius: 3,
            background: "transparent",
            p: { xs: 2, md: 3 },
            display: "flex",
            flexDirection: "column",
            gap: 2,
            minHeight: 0,
            minWidth: 0,
            overflowX: "hidden",
          }}
        >
          <Box sx={{ display: "flex", flexDirection: "column", gap: 1.4, mt: 0.4 }}>
            <Box sx={{ display: "flex", flexDirection: "column", gap: { xs: 3, md: 4 }, minWidth: 0, width: "100%" }}>
              <Box id="device-summary-top-level" sx={{ scrollMarginTop: 0 }}>
                <Island sx={HARDWARE_SUMMARY_SECTION_SX}>
                    {connectionType === "ssh" && (
                      <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1.1, alignItems: "center", mb: 1.9 }}>
                        <TextField
                          size="small"
                          label="SSH Endpoint"
                          value={connectionDraft}
                          onChange={(e) => setConnectionDraft(e.target.value)}
                          placeholder="user@host or host"
                          sx={{
                            minWidth: 250,
                            maxWidth: 360,
                            input: { color: "#fff" },
                            "& .MuiOutlinedInput-root": {
                              backgroundColor: "rgba(4,7,17,0.65)",
                              "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                              "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
                            },
                            label: { color: MAGIC_UI.textMuted },
                          }}
                        />
                        <Button
                          size="small"
                          variant="outlined"
                          onClick={saveConnectionEndpoint}
                          disabled={connectionSaving || connectionDraft.trim() === connectionEndpoint.trim()}
                          sx={{
                            textTransform: "none",
                            borderColor: MAGIC_UI.accentA,
                            color: MAGIC_UI.accentA,
                            borderRadius: 999,
                            px: 2,
                          }}
                        >
                          {connectionSaving ? "Saving..." : "Save"}
                        </Button>
                        <Box sx={{ display: "flex", flexDirection: "column" }}>
                          {connectionMessage && (
                            <Typography variant="caption" sx={{ color: MAGIC_UI.accentA }}>
                              {connectionMessage}
                            </Typography>
                          )}
                          {connectionError && (
                            <Typography variant="caption" sx={{ color: "#ff7b89" }}>
                              {connectionError}
                            </Typography>
                          )}
                        </Box>
                      </Box>
                    )}

                    <Box
                      sx={{
                        display: "grid",
                        gridTemplateColumns: { xs: "1fr", lg: "repeat(2, minmax(0,1fr))" },
                        gap: 1.4,
                        minWidth: 0,
                        width: "100%",
                      }}
                    >
                      <Box sx={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 0.7 }}>
                        <Stack direction="row" spacing={0.75} alignItems="center">
                          <InfoOutlinedIcon sx={{ color: MAGIC_UI.accentA, fontSize: 16 }} />
                          <Typography
                            variant="caption"
                            sx={{
                              color: MAGIC_UI.accentA,
                              fontWeight: 600,
                              fontSize: "0.82rem",
                              letterSpacing: 0.3,
                              textTransform: "none",
                              display: "block",
                            }}
                          >
                            OVERVIEW
                          </Typography>
                        </Stack>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="top-level-overview"
                            rowData={overviewInfoRows}
                            columnDefs={topLevelSplitColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={topLevelSplitGridHeight}
                            rowHeight={36}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={topLevelSplitGridHeight} />
                        )}
                      </Box>
                      <Box sx={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 0.7 }}>
                        <Typography
                          variant="caption"
                          sx={{
                            color: MAGIC_UI.accentA,
                            fontWeight: 600,
                            fontSize: "0.82rem",
                            letterSpacing: 0.3,
                            textTransform: "none",
                            display: "block",
                          }}
                        >
                          Borealis Agent
                        </Typography>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="top-level-agent"
                            rowData={borealisAgentRows}
                            columnDefs={topLevelSplitColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={topLevelSplitGridHeight}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={topLevelSplitGridHeight} />
                        )}
                        {meta.agentUpdateError ? (
                          <Typography sx={{ mt: 0.8, color: "#ffb7b7", fontSize: "0.78rem", lineHeight: 1.45 }}>
                            {meta.agentUpdateError}
                          </Typography>
                        ) : null}
                      </Box>
                    </Box>
                </Island>
              </Box>

              <Box id="device-summary-storage" sx={{ scrollMarginTop: 0 }}>
                <Island
                  title="Storage"
                  icon={<StorageRoundedIcon sx={{ fontSize: 18 }} />}
                  sx={HARDWARE_SUMMARY_SECTION_SX}
                >
                  {summaryDataReady ? (
                    <SummarySectionGrid
                      sectionKey="storage"
                      rowData={storageRows}
                      columnDefs={storageColumnDefs}
                      defaultColDef={defaultGridColDef}
                      height={storageGridHeight}
                    />
                  ) : (
                    <SummaryGridPlaceholder height={storageGridHeight} />
                  )}
                </Island>
              </Box>

              <Box id="device-summary-memory" sx={{ scrollMarginTop: 0 }}>
                <Island
                  title="Memory"
                  icon={<MemoryRoundedIcon sx={{ fontSize: 18 }} />}
                  sx={HARDWARE_SUMMARY_SECTION_SX}
                >
                  {summaryDataReady ? (
                    <SummarySectionGrid
                      sectionKey="memory"
                      rowData={memoryRows}
                      columnDefs={memoryColumnDefs}
                      defaultColDef={defaultGridColDef}
                      height={memoryGridHeight}
                    />
                  ) : (
                    <SummaryGridPlaceholder height={memoryGridHeight} />
                  )}
                </Island>
              </Box>

              <Box id="device-summary-network" sx={{ scrollMarginTop: 0 }}>
                <Island
                  title="Network"
                  icon={<LanRoundedIcon sx={{ fontSize: 18 }} />}
                  sx={HARDWARE_SUMMARY_SECTION_SX}
                >
                  {summaryDataReady ? (
                    <SummarySectionGrid
                      sectionKey="network"
                      rowData={networkRows}
                      columnDefs={networkColumnDefs}
                      defaultColDef={defaultGridColDef}
                      height={networkGridHeight}
                    />
                  ) : (
                    <SummaryGridPlaceholder height={networkGridHeight} />
                  )}
                </Island>
              </Box>
              <Box sx={{ width: "100%", height: "65vh", flexShrink: 0 }} />
            </Box>
          </Box>
        </Box>
      </Box>
    );
  };

  const renderSoftware = () => (
      <InstalledSoftwareTab
      softwareRows={softwareRows}
      hostname={activityHostname}
      operatingSystem={summary.operating_system || meta.operatingSystem || ""}
      onSoftwareDataRefresh={requestSoftwareDataRefresh}
    />
  );

  const renderServicesTab = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <ServiceList device={tunnelDevice} />
    </Box>
  );

  const renderProcessManagementTab = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <ProcessManagementTab device={tunnelDevice} />
    </Box>
  );

  const renderRemoteShellTab = () => (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <RemoteShellTab device={tunnelDevice} />
    </Box>
  );

  const memoryRows = useMemo(
    () =>
      (details.memory || []).map((m, idx) => ({
        id: `${m.slot || `BANK ${idx}`}::${m.serial || "unknown"}::${idx}`,
        slot: m.slot || `BANK ${idx}`,
        speed: m.speed || "unknown",
        serial: m.serial || "unknown",
        capacity: m.capacity,
      })),
    [details.memory]
  );

  const memoryColumnDefs = useMemo(
    () => [
      {
        field: "slot",
        headerName: "Slot",
        width: 120,
        flex: 0,
        sortable: false,
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      },
      { field: "speed", headerName: "Speed", width: 130, flex: 0, sortable: false },
      { field: "serial", headerName: "Serial Number", width: 170, flex: 0, sortable: false },
      {
        field: "capacity",
        headerName: "Capacity",
        width: 140,
        minWidth: 140,
        flex: 1,
        valueFormatter: (params) => formatBytes(params.value),
      },
    ],
    []
  );

  const storageRows = useMemo(() => {
    const toNum = (val) => {
      if (val === undefined || val === null) return undefined;
      if (typeof val === "number") {
        return Number.isNaN(val) ? undefined : val;
      }
      const n = parseFloat(String(val).replace(/[^0-9.]+/g, ""));
      return Number.isNaN(n) ? undefined : n;
    };
    return (details.storage || []).map((d, idx) => {
      const total = toNum(d.total);
      let usagePct = toNum(d.usage);
      let usedBytes = toNum(d.used);
      let freeBytes = toNum(d.free);
      if (usagePct !== undefined && usagePct <= 1) usagePct *= 100;
      if (usedBytes === undefined && total !== undefined && usagePct !== undefined) {
        usedBytes = (usagePct / 100) * total;
      }
      if (freeBytes === undefined && total !== undefined && usedBytes !== undefined) {
        freeBytes = total - usedBytes;
      }
      if (usagePct === undefined && total && usedBytes !== undefined) {
        usagePct = (usedBytes / total) * 100;
      }
      return {
        id: `${d.drive || idx}`,
        driveLabel: String(d.drive || "").replace("\\\\", ""),
        disk_type: d.disk_type || "Fixed Disk",
        total,
        used: usedBytes,
        freeBytes,
        usage: usagePct,
      };
    });
  }, [details.storage]);

  const storageColumnDefs = useMemo(
    () => [
      {
        field: "driveLabel",
        headerName: "Drive",
        width: 130,
        flex: 0,
        sortable: false,
        filter: "agTextColumnFilter",
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      },
      { field: "disk_type", headerName: "Type", width: 120, flex: 0, sortable: false },
      {
        field: "total",
        headerName: "Capacity",
        valueFormatter: (params) => formatBytes(params.value),
        width: 140,
        flex: 0,
      },
      {
        field: "used",
        headerName: "Used",
        valueFormatter: (params) => formatBytes(params.value),
        width: 130,
        flex: 0,
      },
      {
        field: "freeBytes",
        headerName: "Free",
        valueFormatter: (params) => formatBytes(params.value),
        width: 130,
        flex: 0,
      },
      {
        field: "usage",
        headerName: "Usage",
        valueFormatter: (params) =>
          params.value === undefined || Number.isNaN(params.value) ? "unknown" : `${Math.round(params.value)}%`,
        width: 110,
        minWidth: 110,
        flex: 0,
      },
      {
        field: "alerts",
        headerName: "Alerts",
        width: 240,
        minWidth: 240,
        flex: 1,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          if (!isStorageUsageAlert(params?.data?.usage)) return "";
          return (
            <Box
              component="span"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                gap: 0.8,
                whiteSpace: "nowrap",
                lineHeight: 1.1,
              }}
              title={STORAGE_USAGE_ALERT_LABEL}
            >
              <Box
                component="i"
                className="fa-solid fa-triangle-exclamation fa-exclamation-triangle"
                sx={{ color: STORAGE_USAGE_ALERT_COLOR, fontSize: "1.15rem", lineHeight: 1 }}
              />
              <Box component="span">{STORAGE_USAGE_ALERT_LABEL}</Box>
            </Box>
          );
        },
      },
    ],
    []
  );

  const networkRows = useMemo(
    () =>
      (details.network || []).map((n, idx) => ({
        id: `${n.adapter || idx}`,
        adapter: n.adapter || "Adapter",
        ips: (n.ips || []).join(", "),
        mac: formatMac(n.mac),
        link_speed: n.link_speed || n.speed || "unknown",
      })),
    [details.network]
  );

  const networkColumnDefs = useMemo(
    () => [
      { field: "adapter", headerName: "Adapter", width: 170, flex: 0, cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR } },
      { field: "ips", headerName: "IP Address(es)", width: 260, flex: 0 },
      { field: "mac", headerName: "MAC Address", width: 180, flex: 0 },
      { field: "link_speed", headerName: "Link Speed", width: 140, flex: 1, minWidth: 140 },
    ],
    []
  );

  const hardwareOverview = useMemo(() => {
    const storageCritical = storageRows.filter(
      (row) => isStorageUsageAlert(row.usage)
    ).length;
    const installedMemory = memoryRows.filter((row) => {
      if (typeof row.capacity === "number") return row.capacity > 0;
      const parsed = parseFloat(String(row.capacity || "").replace(/[^0-9.]+/g, ""));
      return !Number.isNaN(parsed) && parsed > 0;
    }).length;
    const internalIp = String(meta.internalIp || summary.internal_ip || "unknown").trim() || "unknown";
    const externalIp = String(meta.externalIp || summary.external_ip || "unknown").trim() || "unknown";
    return {
      storageCount: storageRows.length,
      storageCritical,
      memoryCount: memoryRows.length,
      installedMemory,
      internalIp,
      externalIp,
    };
  }, [
    storageRows,
    memoryRows,
    meta.internalIp,
    meta.externalIp,
    summary.internal_ip,
    summary.external_ip,
  ]);

  const overviewInfoRows = useMemo(
    () => {
      const cpuIdentity = details.cpu && typeof details.cpu === "object" ? details.cpu : {};
      const manufacturerValue = String(
        summary.manufacturer || summary.vendor || meta.manufacturer || cpuIdentity.system_manufacturer || ""
      ).trim();
      const systemModelValue = String(
        summary.system_model || summary.device_model || meta.systemModel || cpuIdentity.system_model_raw || ""
      ).trim();
      const preCombinedModel = String(summary.model || meta.model || cpuIdentity.system_model || "").trim();
      const combinedModel = [manufacturerValue, systemModelValue].filter(Boolean).join(" ").trim() || preCombinedModel || "unknown";
      const serialValue =
        String(
          summary.serial_number ||
            summary.serial ||
            summary.bios_serial ||
            cpuIdentity.system_serial_number ||
            summary.asset_tag ||
            meta.serialNumber ||
            UNABLE_TO_RETRIEVE_SN
        ).trim() || UNABLE_TO_RETRIEVE_SN;
      return [
        {
          id: "description",
          label: "Description",
          value: description,
          editableDescription: true,
        },
        {
          id: "processor",
          label: "Processor",
          value: deviceMetricData.cpuMain,
          detail: deviceMetricData.cpuSub,
        },
        {
          id: "ram",
          label: "RAM",
          value: deviceMetricData.memVal,
          detail: deviceMetricData.memSpeed,
        },
        {
          id: "storage-summary",
          label: "Storage",
          value: deviceMetricData.storageMain,
          detail: deviceMetricData.storageSub,
        },
        {
          id: "network-summary",
          label: "Network",
          value: deviceMetricData.netVal,
          detail: deviceMetricData.nicLabel,
        },
        {
          id: "site",
          label: "Site",
          value: meta.siteName || summary.site_name || summary.site || device?.site_name || "placeholder",
        },
        {
          id: "device-type",
          label: "Device Type",
          value: meta.deviceType || summary.device_type || agent.device_type || device?.device_type || "unknown",
        },
        {
          id: "last-user",
          label: "Last User",
          value: meta.lastUser || summary.last_user || "unknown",
        },
        {
          id: "operating-system",
          label: "Operating System",
          value: meta.operatingSystem || summary.operating_system || agent.agent_operating_system || "unknown",
        },
        {
          id: "model",
          label: "Model",
          value: combinedModel,
        },
        {
          id: "serial-number",
          label: "Serial Number",
          value: serialValue,
        },
        {
          id: "last-reboot",
          label: "Last Reboot",
          value: formatDateValue(meta.lastReboot || summary.last_reboot || "", "unknown"),
        },
      ];
    },
    [
      meta.siteName,
      meta.deviceType,
      meta.lastUser,
      meta.operatingSystem,
      meta.manufacturer,
      meta.systemModel,
      meta.model,
      meta.serialNumber,
      meta.lastReboot,
      details.cpu,
      description,
      deviceMetricData.cpuMain,
      deviceMetricData.cpuSub,
      deviceMetricData.memVal,
      deviceMetricData.memSpeed,
      deviceMetricData.storageMain,
      deviceMetricData.storageSub,
      deviceMetricData.netVal,
      deviceMetricData.nicLabel,
      summary.site_name,
      summary.site,
      summary.device_type,
      summary.manufacturer,
      summary.vendor,
      summary.model,
      summary.system_model,
      summary.device_model,
      summary.serial_number,
      summary.serial,
      summary.bios_serial,
      summary.asset_tag,
      summary.operating_system,
      summary.last_user,
      summary.last_reboot,
      device?.site_name,
      device?.device_type,
      agent.device_type,
      agent.agent_operating_system,
      formatDateValue,
    ]
  );

  const borealisAgentRows = useMemo(
    () => {
      const configuredReleaseChannel = String(
        meta.agentReleaseChannel ||
          summary.agent_release_channel ||
          ""
      )
        .trim()
        .toLowerCase();
      const rawEffectiveChannel =
        String(
          configuredReleaseChannel ||
            meta.agentReleaseChannelEffective ||
            summary.agent_release_channel_effective ||
            meta.agentReleaseChannelOverride ||
            summary.agent_release_channel_override ||
            "stable"
        )
          .trim()
          .toLowerCase() || "stable";
      const effectiveChannel = normalizeAgentReleaseChannel(rawEffectiveChannel);
      const currentBranch =
        normalizeAgentBranch(effectiveChannel, meta.agentBranch || summary.agent_branch || "");
      const lastChannelUpdateValue = formatDateValue(
        meta.agentTargetPublishedAt || summary.agent_target_published_at || "",
        "unknown"
      );
      const targetBuildId = meta.agentTargetBuildId || summary.agent_target_build_id || "unknown";
      const rawUpdateState = meta.agentUpdateState || summary.agent_update_state || "idle";
      return [
        {
          id: "agent-guid",
          label: "AGENT_GUID",
          value: meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || "unknown",
        },
        {
          id: "agent-version",
          label: "Agent Version",
          value: meta.agentVersionStatus || summary.agent_version_status || "Needs Updated",
        },
        {
          id: "agent-last-seen",
          label: "Last Seen",
          value: formatDateValue(meta.lastSeen || summary.last_seen || device?.last_seen || "", "unknown"),
        },
        {
          id: "agent-branch-channel",
          label: "Agent Branch/Channel",
          value: `${effectiveChannel} - ${currentBranch}`,
          changeActionVisible: Boolean(isAdmin),
          changeActionText: agentBranchChannelSaving ? "[switching...]" : "[switch]",
        },
        {
          id: "agent-last-channel-update",
          label: "Last Channel Update",
          labelTooltip: `Target Build: ${targetBuildId}`,
          value: lastChannelUpdateValue,
        },
        {
          id: "agent-update-state",
          label: "Agent Update Status",
          labelTooltip: "Shows the updater's most recent result or current activity on this device.",
          value: describeAgentUpdateState(rawUpdateState),
        },
        {
          id: "reenrollment-date",
          label: "(Re)Enrollment Date",
          value: formatDateValue(
            meta.lastEnrollmentAt ||
              meta.lastEnrollmentAtIso ||
              summary.last_enrollment_at ||
              device?.last_enrollment_at ||
              meta.created ||
              summary.created ||
              device?.created ||
              device?.created_at ||
              "",
            "placeholder"
          ),
        },
      ];
    },
    [
      meta.agentGuid,
      meta.agentVersionStatus,
      meta.lastSeen,
      meta.agentReleaseChannel,
      meta.agentReleaseChannelEffective,
      meta.agentReleaseChannelOverride,
      meta.agentBranch,
      meta.agentTargetBuildId,
      meta.agentTargetPublishedAt,
      meta.agentUpdateState,
      meta.lastEnrollmentAt,
      meta.lastEnrollmentAtIso,
      meta.created,
      isAdmin,
      agentBranchChannelSaving,
      summary.agent_guid,
      summary.agent_version_status,
      summary.last_seen,
      summary.agent_release_channel,
      summary.agent_release_channel_effective,
      summary.agent_release_channel_override,
      summary.agent_branch,
      summary.agent_target_build_id,
      summary.agent_target_published_at,
      summary.agent_update_state,
      summary.last_enrollment_at,
      summary.created,
      device?.agent_guid,
      device?.last_seen,
      device?.last_enrollment_at,
      device?.created,
      device?.created_at,
      agent?.agent_guid,
      formatDateValue,
    ]
  );

  const agentRoleHealthPayload = useMemo(
    () =>
      meta.agentRoleHealth && typeof meta.agentRoleHealth === "object"
        ? meta.agentRoleHealth
        : summary.agent_role_health && typeof summary.agent_role_health === "object"
          ? summary.agent_role_health
          : {},
    [meta.agentRoleHealth, summary.agent_role_health]
  );
  const agentHealthRows = useMemo(
    () => buildAgentHealthRows(Array.isArray(agentRoleHealthPayload?.roles) ? agentRoleHealthPayload.roles : [], formatTimestamp),
    [agentRoleHealthPayload, formatTimestamp]
  );

  const renderTopLevelFieldCell = useCallback((params) => {
    const row = params?.data && typeof params.data === "object" ? params.data : {};
    const label = String(row?.label || params?.value || "").trim() || "unknown";
    const content = (
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          width: "100%",
          minHeight: "100%",
        }}
      >
        <Typography
          sx={{
            color: SUMMARY_FIELD_TEXT_COLOR,
            fontSize: "0.82rem",
            lineHeight: 1.45,
            whiteSpace: "normal",
            wordBreak: "break-word",
          }}
        >
          {label}
        </Typography>
      </Box>
    );
    if (!row?.labelTooltip) {
      return content;
    }
    return (
      <Tooltip title={String(row.labelTooltip)} arrow placement="top">
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            width: "100%",
            minHeight: "100%",
          }}
        >
          {content}
        </Box>
      </Tooltip>
    );
  }, []);

  const renderTopLevelValueCell = useCallback(
    (params) => {
      const row = params?.data && typeof params.data === "object" ? params.data : {};
      const value = String(params?.value ?? row?.value ?? "").trim() || "unknown";
      if (row?.editableDescription) {
        return (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              width: "100%",
              minHeight: "100%",
              py: 0.4,
            }}
          >
            <TextField
              size="small"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              onBlur={saveDescription}
              placeholder="Add a friendly label"
              variant="outlined"
              sx={{
                width: "100%",
                maxWidth: 520,
                input: { color: MAGIC_UI.textBright, fontSize: "0.84rem", py: 0.7 },
                "& .MuiOutlinedInput-root": {
                  backgroundColor: "rgba(4,7,17,0.65)",
                  borderRadius: 2,
                  "& fieldset": {
                    borderColor: "rgba(148,163,184,0.45)",
                  },
                  "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
                  "&.Mui-focused fieldset": { borderColor: MAGIC_UI.accentA },
                },
              }}
            />
          </Box>
        );
      }
      if (row?.id === "agent-branch-channel") {
        const actionBusy = agentBranchChannelSaving;
        const actionHandler = openAgentBranchChannelDialog;
        return (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 0.8,
              flexWrap: "wrap",
              minWidth: 0,
              width: "100%",
              minHeight: "100%",
            }}
          >
            <Typography
              sx={{
                color: MAGIC_UI.textBright,
                fontSize: "0.88rem",
                lineHeight: 1.45,
                whiteSpace: "normal",
                wordBreak: "break-word",
              }}
            >
              {value}
            </Typography>
            {row?.changeActionVisible ? (
              <Box
                component="button"
                type="button"
                disabled={actionBusy}
                onClick={actionHandler}
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  p: 0,
                  m: 0,
                  border: 0,
                  background: "transparent",
                  color: BOREALIS_LINK_COLOR,
                  cursor: actionBusy ? "default" : "pointer",
                  font: "inherit",
                  fontSize: "0.82rem",
                  lineHeight: 1.45,
                  textDecoration: "none",
                  transition: "color 160ms ease, opacity 160ms ease",
                  "&:hover": actionBusy
                    ? undefined
                    : {
                        color: BOREALIS_LINK_HOVER_COLOR,
                        textDecoration: "underline",
                      },
                  "&:disabled": {
                    opacity: 0.6,
                  },
                }}
              >
                {String(row.changeActionText || "[change]")}
              </Box>
            ) : null}
          </Box>
        );
      }
      return (
        <Box
          sx={{
            display: "flex",
            flexDirection: "row",
            justifyContent: "flex-start",
            alignItems: "center",
            gap: row?.detail ? 0.75 : 0,
            width: "100%",
            minHeight: "100%",
            minWidth: 0,
          }}
        >
          <Typography
            sx={{
              color: MAGIC_UI.textBright,
              fontSize: "0.88rem",
              lineHeight: 1.45,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
              minWidth: 0,
            }}
          >
            {value}
          </Typography>
            {row?.detail ? (
              <Typography
                sx={{
                  color: "rgba(148,163,184,0.72)",
                  fontSize: "0.72rem",
                  lineHeight: 1.25,
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  minWidth: 0,
                }}
              >
                {String(row.detail)}
              </Typography>
            ) : null}
          </Box>
        );
      },
    [agentBranchChannelSaving, description, openAgentBranchChannelDialog, saveDescription]
  );

  const topLevelSplitColumnDefs = useMemo(
    () => [
      {
        field: "label",
        headerName: "Field",
        width: 170,
        flex: 0,
        sortable: false,
        filter: false,
        cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
        cellRenderer: renderTopLevelFieldCell,
      },
      {
        field: "value",
        headerName: "Value",
        flex: 1,
        minWidth: 220,
        sortable: false,
        filter: false,
        cellStyle: { textAlign: "left" },
        cellRenderer: renderTopLevelValueCell,
      },
    ],
    [renderTopLevelFieldCell, renderTopLevelValueCell]
  );

  const resolveGridHeight = useCallback((rowCount, options = {}) => {
    const minHeight = Number(options.minHeight || 200);
    const maxHeight = Number(options.maxHeight || 420);
    const rowHeight = Number(options.rowHeight || 36);
    const headerHeight = Number(options.headerHeight || 42);
    const padding = Number(options.padding || 14);
    const rows = Math.max(1, Number(rowCount || 0));
    return Math.max(minHeight, Math.min(maxHeight, headerHeight + rows * rowHeight + padding));
  }, []);

  const topLevelSplitGridHeight = useMemo(() => {
    const baseHeight = resolveGridHeight(Math.max(overviewInfoRows.length, borealisAgentRows.length), {
      minHeight: BASE_GRID_HEIGHTS.topLevel,
      maxHeight: 520,
      rowHeight: 36,
    });
    return Math.round(baseHeight * 1.2);
  }, [overviewInfoRows.length, borealisAgentRows.length, resolveGridHeight]);
  const storageGridHeight = useMemo(
    () =>
      resolveGridHeight(storageRows.length, {
        minHeight: BASE_GRID_HEIGHTS.storage,
        maxHeight: 420,
      }),
    [storageRows.length, resolveGridHeight]
  );
  const memoryGridHeight = useMemo(
    () =>
      resolveGridHeight(memoryRows.length, {
        minHeight: BASE_GRID_HEIGHTS.memory,
        maxHeight: 340,
      }),
    [memoryRows.length, resolveGridHeight]
  );
  const networkGridHeight = useMemo(
    () =>
      resolveGridHeight(networkRows.length, {
        minHeight: BASE_GRID_HEIGHTS.network,
        maxHeight: 340,
      }),
    [networkRows.length, resolveGridHeight]
  );

  useEffect(() => {
    if (!isSummaryGridDebugEnabled()) return;
    const topTabKey = activeWorkspaceKey || "";
    if (topTabKey !== "inventory") return;
    summaryGridDebugLog("deviceSummaryRender", {
      renderCount: pageRenderCountRef.current,
      topTabKey,
      overviewRows: overviewInfoRows.length,
      agentRows: borealisAgentRows.length,
      storageRows: storageRows.length,
      memoryRows: memoryRows.length,
      networkRows: networkRows.length,
    });
  }, [
    activeWorkspaceKey,
    overviewInfoRows.length,
    borealisAgentRows.length,
    storageRows.length,
    memoryRows.length,
    networkRows.length,
  ]);

  const renderHistory = () => (
    <ActivityHistoryTab hostname={activityHostname} refreshToken={historyRefreshToken} />
  );
  const renderWatchdogsTab = () => (
    <DeviceWatchdogsTab
      deviceId={deviceId}
      hostname={activityHostname}
      deviceGuid={meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || ""}
      siteId={meta.siteId ?? null}
      siteName={meta.siteName || ""}
    />
  );

  const status = lockedStatus || statusFromHeartbeat(agent.last_seen || device?.lastSeen);
  const statusIsOnline = String(status || "").trim().toLowerCase() === "online";
  const roleHealthSummary = useMemo(() => {
    const roles = Array.isArray(agentHealthRows) ? agentHealthRows : [];
    const unhealthyRoles = roles.filter((role) => {
      const health = String(role?.statusCode || role?.status || "").trim().toLowerCase();
      if (!health) return false;
      return !["healthy", "loaded", "ok", "online", "running", "ready", "complete", "completed", "not_applicable", "unsupported"].includes(health);
    });
    return {
      count: roles.length,
      unhealthyCount: unhealthyRoles.length,
      label:
        roles.length === 0
          ? "No Role Report"
          : unhealthyRoles.length > 0
            ? `${unhealthyRoles.length}/${roles.length} Roles Degraded`
            : `${roles.length} Roles Healthy`,
    };
  }, [agentHealthRows]);
  const dataFreshnessLabel = useMemo(() => {
    const rawLastSeen = meta.lastSeen || summary.last_seen || device?.last_seen || agent?.last_seen || 0;
    return formatDateValue(rawLastSeen, "No heartbeat").replace(" AM", "AM").replace(" PM", "PM");
  }, [agent?.last_seen, device?.last_seen, formatDateValue, meta.lastSeen, summary.last_seen]);
  const tunnelConnection = useMemo(() => {
    const tunnelStatus = String(tunnelInfo?.status || "idle").trim().toLowerCase();
    if (tunnelStatus === "up") {
      return { value: "Connected", tone: "ready", detail: "WireGuard tunnel ready for backend tools." };
    }
    if (tunnelStatus === "down") {
      return { value: "Down", tone: "danger", detail: "WireGuard tunnel unavailable." };
    }
    if (tunnelStatus === "error") {
      return { value: "Error", tone: "danger", detail: "WireGuard tunnel status endpoint returned error." };
    }
    return { value: "Idle", tone: "muted", detail: "No active WireGuard tunnel reported." };
  }, [tunnelInfo?.status]);
  const agentSocketConnection = useMemo(() => {
    if (tunnelInfo?.agent_socket === true) {
      return { value: "Connected", tone: "ready", detail: "Engine can reach agent management socket." };
    }
    if (statusIsOnline) {
      return { value: "Disconnected", tone: "warning", detail: "Agent heartbeat is present, but management socket is unavailable." };
    }
    return { value: "Unavailable", tone: "danger", detail: "Agent must heartbeat before management socket can be confirmed." };
  }, [statusIsOnline, tunnelInfo?.agent_socket]);
  const engineConnection = useMemo(() => {
    if (statusIsOnline && tunnelInfo?.agent_socket) {
      return { value: "Connected", tone: "ready" };
    }
    if (statusIsOnline) {
      return { value: "Degraded", tone: "warning" };
    }
    return { value: "Offline", tone: "danger" };
  }, [statusIsOnline, tunnelInfo?.agent_socket]);
  const readiness = useMemo(() => {
    const tunnelStatus = String(tunnelInfo?.status || "idle").trim().toLowerCase();
    const updateState = String(meta.agentUpdateState || summary.agent_update_state || "").trim().toLowerCase();
    const versionState = String(meta.agentVersionStatus || summary.agent_version_status || "").trim().toLowerCase();
    const tunnelBlocked = ["down", "error"].includes(tunnelStatus) || tunnelInfo?.agent_socket === false;
    const updateFailed = updateState === "failed" || Boolean(meta.agentUpdateError || summary.agent_update_error);
    const needsUpdate = versionState.includes("need") || versionState.includes("outdated");
    const healthBlocked = !statusIsOnline || tunnelBlocked || updateFailed || roleHealthSummary.unhealthyCount > 0;
    const storageBlocked = hardwareOverview.storageCritical > 0;
    if (healthBlocked) {
      return {
        tone: "danger",
        headline: !statusIsOnline
          ? "Agent unreachable"
          : tunnelBlocked
            ? "Remote control degraded"
            : updateFailed
              ? "Agent updater failed"
              : "Agent role degraded",
        detail: !statusIsOnline
          ? "Recover control before running backend tools."
          : tunnelBlocked
            ? "Agent management connection needs review."
            : updateFailed
              ? "Review updater state and startup flow."
              : "Role health needs review.",
      };
    }
    if (storageBlocked || needsUpdate) {
      return {
        tone: "warning",
        headline: storageBlocked ? "Device needs attention" : "Agent update recommended",
        detail: storageBlocked
          ? `${hardwareOverview.storageCritical} storage volume exceeds ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}% usage.`
          : "Installed agent build does not match desired state.",
      };
    }
    return {
      tone: "ready",
      headline: "Ready for backend tools",
      detail: "Agent, inventory, and background-control signals look usable.",
    };
  }, [
    hardwareOverview.storageCritical,
    meta.agentUpdateError,
    meta.agentUpdateState,
    meta.agentVersionStatus,
    roleHealthSummary.unhealthyCount,
    statusIsOnline,
    summary.agent_update_error,
    summary.agent_update_state,
    summary.agent_version_status,
    tunnelInfo?.agent_socket,
    tunnelInfo?.status,
  ]);
  const agentManagementDetails = useMemo(
    () => [
      {
        group: "Connection",
        label: "Agent Status",
        value: status || "Unknown",
        tone: statusIsOnline ? "ready" : "danger",
        detail: "Latest heartbeat-derived agent state.",
      },
      {
        group: "Connection",
        label: "Agent Socket",
        value: agentSocketConnection.value,
        tone: agentSocketConnection.tone,
        detail: agentSocketConnection.detail,
      },
      {
        group: "Connection",
        label: "WireGuard",
        value: tunnelConnection.value,
        tone: tunnelConnection.tone,
        detail: tunnelConnection.detail,
      },
      {
        group: "Connection",
        label: "Last Heartbeat",
        value: dataFreshnessLabel,
        tone: statusIsOnline ? "ready" : "warning",
        detail: "Most recent agent heartbeat received by Engine.",
      },
      {
        group: "Connection",
        label: "Tunnel Listener",
        value: tunnelInfo?.listener_healthy === false ? "Unhealthy" : "Healthy",
        tone: tunnelInfo?.listener_healthy === false ? "danger" : "ready",
        detail: "Engine-side tunnel listener health.",
      },
      {
        group: "Identifiers",
        label: "Virtual IP",
        value: tunnelInfo?.virtual_ip || "No tunnel IP",
        tone: tunnelInfo?.virtual_ip ? "ready" : "muted",
        detail: "WireGuard address assigned to this agent tunnel.",
      },
      {
        group: "Identifiers",
        label: "Tunnel ID",
        value: tunnelInfo?.tunnel_id || "No active tunnel",
        tone: tunnelInfo?.tunnel_id ? "ready" : "muted",
        detail: "Current Engine tunnel record identifier.",
      },
      {
        group: "Identifiers",
        label: "Agent ID",
        value: tunnelAgentId || "Unavailable",
        tone: tunnelAgentId ? "ready" : "muted",
        detail: "Identifier used for tunnel status lookup.",
      },
    ],
    [
      agentSocketConnection.detail,
      agentSocketConnection.tone,
      agentSocketConnection.value,
      dataFreshnessLabel,
      status,
      statusIsOnline,
      tunnelAgentId,
      tunnelConnection.detail,
      tunnelConnection.tone,
      tunnelConnection.value,
      tunnelInfo?.listener_healthy,
      tunnelInfo?.tunnel_id,
      tunnelInfo?.virtual_ip,
    ]
  );
  const agentManagementSummary = useMemo(() => {
    if (!statusIsOnline) return "Agent heartbeat offline.";
    if (engineConnection.value !== "Connected") return "Agent heartbeat online, management socket degraded.";
    if (tunnelConnection.value !== "Connected") return "Management socket connected, tunnel not active.";
    return "Management socket and WireGuard tunnel connected.";
  }, [engineConnection.value, statusIsOnline, tunnelConnection.value]);
  const agentManagementGroups = useMemo(
    () =>
      ["Connection", "Identifiers"]
        .map((label) => ({
          label,
          rows: agentManagementDetails.filter((entry) => entry.group === label),
        }))
        .filter((group) => group.rows.length),
    [agentManagementDetails]
  );
  const remoteToolsBadgeCount = [
    !statusIsOnline,
    tunnelInfo?.agent_socket === false,
    ["down", "error"].includes(String(tunnelInfo?.status || "").trim().toLowerCase()),
  ].filter(Boolean).length;
  const workspaceBadges = useMemo(
    () => ({
      storage: hardwareOverview.storageCritical > 0 ? String(hardwareOverview.storageCritical) : "",
      protection: "",
      history: "",
      shell: remoteToolsBadgeCount > 0 ? String(remoteToolsBadgeCount) : "",
      config: "",
    }),
    [hardwareOverview.storageCritical, remoteToolsBadgeCount]
  );

  const renderFileManagementTab = () => <RemoteFileManagementTab device={tunnelDevice} />;
  const renderMetadataTab = () => (
    <DeviceMetadataTab
      deviceId={deviceId}
      deviceGuid={meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || ""}
      hostname={activityHostname}
    />
  );

  const rawDisplayHostname = meta.hostname || summary.hostname || agent.hostname || device?.hostname || "";
  const displayHostname = formatHostnameForDisplay(rawDisplayHostname) || "Device Summary";
  const pageSubtitle = `${readiness.headline} - Agent ${status || "Unknown"} - ${readiness.detail}`;
  const deviceOperatingSystem = readFirstNonEmptyValue(
    meta.operatingSystem,
    summary.operating_system,
    summary.agent_operating_system,
    agent.agent_operating_system,
    device?.operating_system,
    device?.summary?.operating_system,
    device?.summary?.agent_operating_system
  );
  const DeviceSummaryPageIcon = useMemo(
    () =>
      function DeviceSummaryPageIcon(props) {
        return <OperatingSystemPageIcon osName={deviceOperatingSystem} {...props} />;
      },
    [deviceOperatingSystem]
  );

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "device-summary-actions",
        label: "Actions",
        icon: <MoreHorizIcon />,
        tone: "primary",
        disabled: !activityHostname,
        onClick: (event) => setMenuAnchor(event.currentTarget),
      },
    ],
    [activityHostname]
  );

  const getToneStyles = (tone) => {
    if (tone === "danger") {
      return {
        border: "rgba(248,113,113,0.42)",
        background: "linear-gradient(135deg, rgba(127,29,29,0.42), rgba(15,23,42,0.74))",
        icon: "#fca5a5",
        accent: "#fca5a5",
      };
    }
    if (tone === "warning") {
      return {
        border: "rgba(250,204,21,0.42)",
        background: "linear-gradient(135deg, rgba(113,63,18,0.42), rgba(15,23,42,0.74))",
        icon: "#fde68a",
        accent: "#fde68a",
      };
    }
    if (tone === "ready") {
      return {
        border: "rgba(52,211,153,0.38)",
        background: "linear-gradient(135deg, rgba(6,78,59,0.38), rgba(15,23,42,0.74))",
        icon: MAGIC_UI.accentC,
        accent: MAGIC_UI.accentC,
      };
    }
    return {
      border: MAGIC_UI.panelBorder,
      background: "linear-gradient(135deg, rgba(15,23,42,0.82), rgba(7,11,24,0.72))",
      icon: MAGIC_UI.accentA,
      accent: MAGIC_UI.accentA,
    };
  };

  const renderStatusPill = ({ id, label, value, tone = "muted", onClick = null, valueColor = "" }) => {
    const toneStyles = getToneStyles(tone);
    return (
      <Box
        component={onClick ? "button" : "div"}
        type={onClick ? "button" : undefined}
        key={id}
        onClick={onClick || undefined}
        sx={{
          display: "inline-flex",
          alignItems: "center",
          gap: 0.8,
          minHeight: 30,
          px: 1.15,
          borderRadius: 999,
          border: `1px solid ${toneStyles.border}`,
          background: "rgba(2,6,23,0.44)",
          minWidth: 0,
          cursor: onClick ? "pointer" : "default",
          font: "inherit",
          textDecoration: "none",
          "&:hover": onClick
            ? {
                borderColor: "rgba(125,183,255,0.52)",
                background: "rgba(125,211,252,0.07)",
              }
            : undefined,
          "&:focus-visible": onClick
            ? {
                outline: `2px solid ${BOREALIS_LINK_COLOR}`,
                outlineOffset: 2,
              }
            : undefined,
        }}
      >
        <Typography
          component="span"
          sx={{
            color: MAGIC_UI.textMuted,
            fontSize: "0.68rem",
            letterSpacing: 0.4,
            textTransform: "uppercase",
            fontWeight: 700,
            lineHeight: 1,
            whiteSpace: "nowrap",
          }}
        >
          {label}
        </Typography>
        <Typography
          component="span"
          title={String(value || "")}
          sx={{
            color: valueColor || toneStyles.accent,
            fontSize: "0.76rem",
            fontWeight: 700,
            lineHeight: 1.1,
            minWidth: 0,
            maxWidth: { xs: 180, md: 260 },
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            textDecoration: "none",
          }}
        >
          {value}
        </Typography>
      </Box>
    );
  };

  const getConnectionRowMeta = (tone) => {
    if (tone === "danger") {
      return {
        color: "#fca5a5",
        border: "rgba(248,113,113,0.28)",
        hoverBorder: "rgba(248,113,113,0.68)",
        background: "rgba(248,113,113,0.06)",
        hoverBackground: "rgba(248,113,113,0.11)",
        Icon: ErrorOutlineRoundedIcon,
      };
    }
    if (tone === "warning") {
      return {
        color: "#fde68a",
        border: "rgba(250,204,21,0.28)",
        hoverBorder: "rgba(250,204,21,0.68)",
        background: "rgba(250,204,21,0.06)",
        hoverBackground: "rgba(250,204,21,0.11)",
        Icon: WarningAmberRoundedIcon,
      };
    }
    if (tone === "ready") {
      return {
        color: MAGIC_UI.accentC,
        border: "rgba(52,211,153,0.28)",
        hoverBorder: "rgba(52,211,153,0.68)",
        background: "rgba(52,211,153,0.06)",
        hoverBackground: "rgba(52,211,153,0.11)",
        Icon: CheckCircleRoundedIcon,
      };
    }
    return {
      color: MAGIC_UI.accentA,
      border: "rgba(125,211,252,0.24)",
      hoverBorder: "rgba(125,211,252,0.6)",
      background: "rgba(125,211,252,0.05)",
      hoverBackground: "rgba(125,211,252,0.1)",
      Icon: HelpOutlineRoundedIcon,
    };
  };

  const renderConnectionTooltip = (row) => (
    <Box sx={{ maxWidth: 300 }}>
      <Typography sx={{ color: "#fff", fontSize: "0.72rem", fontWeight: 800, lineHeight: 1.25 }}>
        {row.label}: {row.value}
      </Typography>
      <Typography sx={{ color: "rgba(226,232,240,0.86)", fontSize: "0.68rem", lineHeight: 1.35, mt: 0.45 }}>
        {row.detail}
      </Typography>
    </Box>
  );

  const renderConnectionDetailRow = (row, index) => {
    const rowMeta = getConnectionRowMeta(row.tone);
    const RowIcon = rowMeta.Icon;
    return (
      <Tooltip key={`${row.group || "connection"}-${row.label}-${index}`} title={renderConnectionTooltip(row)} arrow placement="right">
        <Box sx={{ minWidth: 0, width: "100%" }}>
          <Box
            sx={{
              width: "100%",
              minHeight: 31,
              px: 0.65,
              py: 0.4,
              borderRadius: 1.3,
              border: `1px solid ${rowMeta.border}`,
              background: rowMeta.background,
              color: MAGIC_UI.textBright,
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              gap: 0.65,
              textAlign: "left",
              overflow: "hidden",
              cursor: "help",
              "&:hover": {
                borderColor: rowMeta.hoverBorder,
                background: rowMeta.hoverBackground,
              },
            }}
          >
            <RowIcon sx={{ color: rowMeta.color, fontSize: 15, flexShrink: 0 }} />
            <Box sx={{ minWidth: 0, flex: 1 }}>
              <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.66rem", fontWeight: 740, lineHeight: 1.12 }} noWrap>
                {row.label}
              </Typography>
              <Typography sx={{ mt: 0.1, color: rowMeta.color, fontSize: "0.59rem", fontWeight: 700, lineHeight: 1.1 }} noWrap>
                {row.value}
              </Typography>
            </Box>
          </Box>
        </Box>
      </Tooltip>
    );
  };

  const readinessPills = [
    {
      id: "engine-connection",
      label: "Agent Management",
      value: engineConnection.value,
      tone: engineConnection.tone,
      valueColor: BOREALIS_LINK_COLOR,
      onClick: (event) => setAgentManagementAnchor(event.currentTarget),
    },
    {
      id: "roles",
      label: "Roles",
      value: roleHealthSummary.label,
      tone: roleHealthSummary.unhealthyCount > 0 ? "danger" : roleHealthSummary.count > 0 ? "ready" : "muted",
      valueColor: BOREALIS_LINK_COLOR,
      onClick: (event) => setRoleHealthAnchor(event.currentTarget),
    },
  ];

  const renderReadinessHeader = () => (
    <Box
      sx={{
        borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
        background:
          "linear-gradient(135deg, rgba(7,11,24,0.92), rgba(15,23,42,0.78)), " +
          "radial-gradient(120% 120% at 0% 0%, rgba(125,211,252,0.12), transparent 55%)",
        px: { xs: 1.5, md: 2 },
        py: 1.15,
        display: "flex",
        alignItems: "center",
        minWidth: 0,
      }}
    >
      <Stack
        direction="row"
        spacing={0.75}
        useFlexGap
        flexWrap="wrap"
        justifyContent="flex-start"
        sx={{ minWidth: 0 }}
      >
        {readinessPills.map(renderStatusPill)}
      </Stack>
    </Box>
  );

  const renderDeviceNavBadge = (badge) =>
    badge ? (
      <Box
        component="span"
        sx={{
          ml: "auto",
          minWidth: 18,
          height: 18,
          px: 0.5,
          borderRadius: 999,
          background: BOREALIS_PRIMARY_GRADIENT,
          color: "#06101d",
          border: "1px solid transparent",
          boxShadow: "0 8px 18px rgba(125, 211, 252, 0.16)",
          fontSize: "0.68rem",
          fontWeight: 800,
          lineHeight: "16px",
          textAlign: "center",
        }}
      >
        {badge}
      </Box>
    ) : null;

  const SidebarSection = ({ sectionId, title, children }) => (
    <Accordion
      expanded={expandedDeviceNavSections[sectionId] ?? true}
      onChange={(_, expanded) => {
        setExpandedDeviceNavSections((previous) => ({ ...previous, [sectionId]: expanded }));
      }}
      square
      disableGutters
      sx={DEVICE_NAV_ACCORDION_SX}
    >
      <AccordionSummary expandIcon={<ExpandMoreIcon sx={{ color: NAV_TAB_COLORS.iconActive }} />} sx={DEVICE_NAV_ACCORDION_SUMMARY_SX}>
        <Typography
          sx={{
            fontSize: "0.85rem",
            color: NAV_TAB_COLORS.iconActive,
            fontWeight: 700,
            minWidth: 0,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {title}
        </Typography>
      </AccordionSummary>
      <AccordionDetails sx={DEVICE_NAV_ACCORDION_DETAILS_SX}>{children}</AccordionDetails>
    </Accordion>
  );

  const SidebarNavRow = ({
    icon,
    label,
    active = false,
    disabled = false,
    onClick,
    badge = "",
    indent = 0,
    summarySectionKey = "",
  }) => (
    <ListItemButton
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      selected={active}
      aria-label={label}
      data-summary-nav-section={summarySectionKey || undefined}
      sx={deviceSidebarNavRowSx(active, disabled, indent)}
    >
      <Box sx={{ display: "flex", alignItems: "center", minWidth: 0, flex: "1 1 auto" }}>
        <Box className="device-summary-nav-icon" sx={deviceSidebarNavIconSx(active, disabled)}>{icon}</Box>
        <ListItemText
          disableTypography
          primary={
            <Typography
              className="device-summary-nav-label"
              component="span"
              sx={{
                display: "block",
                color: "inherit",
                fontSize: "0.8rem",
                fontWeight: active ? 600 : 400,
                lineHeight: 1.45,
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {label}
            </Typography>
          }
        />
        {renderDeviceNavBadge(badge)}
      </Box>
    </ListItemButton>
  );

  const renderDeviceNavigationSidebar = () => {
    const openRemoteDesktop = () => {
      if (!deviceId) return;
      navigate(APP_PATHS.deviceRemoteDesktop(deviceId), {
        state: { initialDevice: tunnelDevice },
      });
    };
    const activeView = (workspaceKey, viewKey = "") =>
      activeWorkspaceKey === workspaceKey &&
      (!viewKey || normalizeWorkspaceView(workspaceKey, activeWorkspaceView) === viewKey);
    return (
      <Box sx={DEVICE_NAV_SIDEBAR_SX}>
        <Box sx={{ flex: 1, overflowY: "auto", p: 0.25 }}>
          <SidebarSection sectionId="inventory" title="Inventory">
            <SidebarNavRow
              icon={<InfoOutlinedIcon fontSize="small" />}
              label="Overview"
              onClick={() => requestSummarySectionScroll("top-level")}
              summarySectionKey="top-level"
            />
            <SidebarNavRow
              icon={<StorageRoundedIcon fontSize="small" />}
              label="Storage"
              onClick={() => requestSummarySectionScroll("storage")}
              badge={workspaceBadges.storage}
              summarySectionKey="storage"
            />
            <SidebarNavRow
              icon={<MemoryRoundedIcon fontSize="small" />}
              label="Memory"
              onClick={() => requestSummarySectionScroll("memory")}
              summarySectionKey="memory"
            />
            <SidebarNavRow
              icon={<LanRoundedIcon fontSize="small" />}
              label="Network"
              onClick={() => requestSummarySectionScroll("network")}
              summarySectionKey="network"
            />
            <SidebarNavRow
              icon={<AppsRoundedIcon fontSize="small" />}
              label="Installed Software"
              active={activeView("inventory", "software")}
              onClick={() => setActiveWorkspace("inventory", "software")}
            />
          </SidebarSection>

          <SidebarSection sectionId="backend" title="Backend Tools">
            <SidebarNavRow
              icon={<DesktopWindowsRoundedIcon fontSize="small" />}
              label="Remote Desktop"
              disabled={!deviceId}
              onClick={openRemoteDesktop}
            />
            <SidebarNavRow
              icon={<TerminalRoundedIcon fontSize="small" />}
              label="Shell"
              active={activeView("remote_ops", "shell")}
              onClick={() => setActiveWorkspace("remote_ops", "shell")}
              badge={workspaceBadges.shell}
            />
            <SidebarNavRow
              icon={<FolderRoundedIcon fontSize="small" />}
              label="Files"
              active={activeView("remote_ops", "files")}
              onClick={() => setActiveWorkspace("remote_ops", "files")}
            />
            <SidebarNavRow
              icon={<AccountTreeRoundedIcon fontSize="small" />}
              label="Processes"
              active={activeView("remote_ops", "processes")}
              onClick={() => setActiveWorkspace("remote_ops", "processes")}
            />
            <SidebarNavRow
              icon={<SettingsRoundedIcon fontSize="small" />}
              label="Services"
              active={activeView("remote_ops", "services")}
              onClick={() => setActiveWorkspace("remote_ops", "services")}
            />
          </SidebarSection>

          <SidebarSection sectionId="protection" title="Protection">
            <SidebarNavRow
              icon={<PolicyIcon fontSize="small" />}
              label="Watchdogs"
              active={activeView("protection")}
              onClick={() => setActiveWorkspace("protection")}
            />
          </SidebarSection>

          <SidebarSection sectionId="history" title="History">
            <SidebarNavRow
              icon={<ListAltRoundedIcon fontSize="small" />}
              label="Activity History"
              active={activeView("history")}
              onClick={() => setActiveWorkspace("history")}
            />
          </SidebarSection>

          <SidebarSection sectionId="metadata" title="Metadata">
            <SidebarNavRow
              icon={<LabelRoundedIcon fontSize="small" />}
              label="Metadata"
              active={activeView("config", "metadata")}
              onClick={() => setActiveWorkspace("config", "metadata")}
            />
          </SidebarSection>
        </Box>
        <Box sx={{ px: 1, pb: 1, pt: 0.5 }}>
          <Box
            component="button"
            type="button"
            onClick={() => navigate(APP_PATHS.devices)}
            sx={{
              width: "100%",
              height: 28,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              gap: 0.75,
              px: 1,
              background: "rgba(255,255,255,0.04)",
              border: "1px solid rgba(125,183,255,0.14)",
              borderRadius: 6,
              color: NAV_TAB_COLORS.iconActive,
              cursor: "pointer",
              transition: "background 160ms ease, transform 120ms ease",
              "&:hover": {
                background: "rgba(255,255,255,0.08)",
              },
              "&:active": {
                transform: "translateY(1px)",
              },
            }}
          >
            <ChevronLeftIcon fontSize="small" />
            <Typography
              sx={{
                color: NAV_TAB_COLORS.iconActive,
                fontSize: "0.8rem",
                fontWeight: 600,
                lineHeight: 1.1,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              Devices List
            </Typography>
          </Box>
        </Box>
      </Box>
    );
  };

  const renderRemoteOpsWorkspace = () => {
    const view = normalizeWorkspaceView("remote_ops", activeWorkspaceView);
    return (
      <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0, minWidth: 0 }}>
        <Box sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          {view === "files"
            ? renderFileManagementTab()
            : view === "processes"
              ? renderProcessManagementTab()
              : view === "services"
                ? renderServicesTab()
                : renderRemoteShellTab()}
        </Box>
      </Box>
    );
  };

  const renderInventoryWorkspace = () => {
    const view = normalizeWorkspaceView("inventory", activeWorkspaceView);
    return (
      <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0, minWidth: 0 }}>
        <Box sx={{ flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          {view === "software" ? renderSoftware() : renderDeviceSummaryTab()}
        </Box>
      </Box>
    );
  };

  const renderWorkspaceContent = () => {
    if (activeWorkspaceKey === "remote_ops") return renderRemoteOpsWorkspace();
    if (activeWorkspaceKey === "inventory") return renderInventoryWorkspace();
    if (activeWorkspaceKey === "protection") return renderWatchdogsTab();
    if (activeWorkspaceKey === "history") return renderHistory();
    if (activeWorkspaceKey === "config") return renderMetadataTab();
    return renderInventoryWorkspace();
  };
  const workspaceContent = renderWorkspaceContent();
  const deviceNavigationSidebar = useMemo(
    () => renderDeviceNavigationSidebar(),
    [
      activeWorkspaceKey,
      activeWorkspaceView,
      deviceId,
      expandedDeviceNavSections,
      navigate,
      requestSummarySectionScroll,
      setActiveWorkspace,
      tunnelDevice,
      workspaceBadges,
    ]
  );

  useRoutePageChrome({
    title: displayHostname,
    subtitle: pageSubtitle,
    Icon: DeviceSummaryPageIcon,
    actions: pageHeaderActions,
    navigationSidebar: deviceNavigationSidebar,
  });

  return (
    <Box
      sx={{
        m: 0,
        p: { xs: 2, md: 3 },
        borderRadius: 0,
        background: "transparent",
        boxShadow: "none",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        width: "100%",
        maxWidth: "100%",
        minWidth: 0,
        height: "100%",
        overflowX: "hidden",
      }}
    >
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={() => setMenuAnchor(null)}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
          },
        }}
      >
        <MenuItem
          disabled={!canLaunchQuickJob}
          onClick={() => {
            setMenuAnchor(null);
            if (!canLaunchQuickJob) return;
            openQuickJobDialog();
          }}
        >
          Quick Job
        </MenuItem>
        <MenuItem
          disabled={!activityHostname}
          onClick={() => {
            setMenuAnchor(null);
            if (!activityHostname) return;
            navigate(APP_PATHS.watchdogNew, {
              state: {
                watchdogDraft: {
                  name: `${activityHostname} Watchdog`,
                  description: `Device-scoped watchdog for ${activityHostname}.`,
                  site_mode: meta.siteId ? "specific_sites" : "global",
                  site_ids: meta.siteId ? [Number(meta.siteId)] : [],
                  targets: [
                    {
                      kind: "device",
                      device_guid:
                        meta.agentGuid || summary.agent_guid || device?.agent_guid || agent?.agent_guid || "",
                      hostname: activityHostname,
                      site_id: meta.siteId ?? null,
                      site_name: meta.siteName || "",
                    },
                  ],
                },
              },
            });
          }}
        >
          New Watchdog
        </MenuItem>
        <MenuItem
          disabled={!activityHostname || updateAgentBusy}
          onClick={() => {
            setMenuAnchor(null);
            requestAgentUpdate();
          }}
        >
          Update Agent
        </MenuItem>
        <MenuItem
          onClick={() => {
            setMenuAnchor(null);
            setClearDialogOpen(true);
          }}
        >
          Clear Device Activity
        </MenuItem>
      </Menu>
      <Menu
        anchorEl={agentManagementAnchor}
        open={Boolean(agentManagementAnchor)}
        onClose={() => setAgentManagementAnchor(null)}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.98)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            borderRadius: 2,
            boxShadow: "0 24px 70px rgba(2,6,23,0.68)",
            p: 0.8,
            mt: 0.7,
            overflow: "visible",
          },
        }}
      >
        <Box
          sx={{
            width: 430,
            maxWidth: "calc(100vw - 40px)",
            border: "none",
            boxShadow: "none",
            background: "transparent",
            p: 1.1,
          }}
        >
          <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.78rem", fontWeight: 760, lineHeight: 1.2 }}>
            Agent management connection
          </Typography>
          <Typography sx={{ mt: 0.2, color: engineConnection.tone === "ready" ? MAGIC_UI.accentA : MAGIC_UI.textMuted, fontSize: "0.67rem", lineHeight: 1.25 }}>
            {agentManagementSummary}
          </Typography>
          <Box sx={{ mt: 0.9, display: "flex", flexDirection: "column", gap: 0.85 }}>
            {agentManagementGroups.map((group, groupIndex) => (
              <Box key={group.label} sx={{ minWidth: 0, mt: groupIndex > 0 ? 0.7 : 0 }}>
                <Typography
                  sx={{
                    mb: 0.35,
                    color: MAGIC_UI.textMuted,
                    fontSize: "0.58rem",
                    fontWeight: 800,
                    letterSpacing: "0.08em",
                    textTransform: "uppercase",
                  }}
                >
                  {group.label}
                </Typography>
                <Box sx={{ display: "flex", flexDirection: "column", gap: 0.35 }}>
                  {group.rows.map(renderConnectionDetailRow)}
                </Box>
              </Box>
            ))}
          </Box>
        </Box>
      </Menu>
      <Menu
        anchorEl={roleHealthAnchor}
        open={Boolean(roleHealthAnchor)}
        onClose={() => setRoleHealthAnchor(null)}
        anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.98)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            borderRadius: 2,
            boxShadow: "0 24px 70px rgba(2,6,23,0.68)",
            p: 0.8,
            mt: 0.7,
            overflow: "visible",
          },
        }}
      >
        <RuntimeRoleHealthBreakdown
          runtimeRows={agentHealthRows}
          sx={{
            width: 430,
            maxWidth: "calc(100vw - 40px)",
            border: "none",
            boxShadow: "none",
            background: "transparent",
          }}
        />
      </Menu>
      <Menu
        anchorReference="anchorPosition"
        anchorPosition={releaseChannelMenuPosition || undefined}
        open={Boolean(releaseChannelMenuPosition)}
        onClose={closeReleaseChannelMenu}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
          },
        }}
      >
        <MenuItem
          selected={!String(meta.agentReleaseChannelOverride || summary.agent_release_channel_override || "").trim()}
          disabled={releaseChannelSaving}
          onClick={() => handleReleaseChannelSelection("")}
        >
          Use Default
        </MenuItem>
        <MenuItem
          selected={String(meta.agentReleaseChannelOverride || summary.agent_release_channel_override || "").trim().toLowerCase() === "stable"}
          disabled={releaseChannelSaving}
          onClick={() => handleReleaseChannelSelection("stable")}
        >
          Stable
        </MenuItem>
        <MenuItem
          selected={
            normalizeAgentReleaseChannel(
              meta.agentReleaseChannel ||
                summary.agent_release_channel ||
                meta.agentReleaseChannelOverride ||
                summary.agent_release_channel_override ||
                ""
            ) === "unstable" ||
            String(meta.agentReleaseChannelOverride || summary.agent_release_channel_override || "").trim().toLowerCase() === "unstable"
          }
          disabled={releaseChannelSaving}
          onClick={() => handleReleaseChannelSelection("unstable")}
        >
          Unstable
        </MenuItem>
      </Menu>
      <Menu
        anchorReference="anchorPosition"
        anchorPosition={agentBranchMenuPosition || undefined}
        open={Boolean(agentBranchMenuPosition)}
        onClose={closeAgentBranchMenu}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            p: 1.25,
          },
        }}
      >
        <Box sx={{ width: 340, maxWidth: "calc(100vw - 48px)" }}>
          <TextField
            autoFocus
            fullWidth
            size="small"
            label="Unstable Branch"
            value={agentBranchDraft}
            disabled={agentBranchSaving}
            onChange={(event) => setAgentBranchDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                applyAgentBranchOverride();
              }
            }}
            sx={{
              "& .MuiInputBase-root": {
                color: MAGIC_UI.textBright,
              },
              "& .MuiInputLabel-root": {
                color: MAGIC_UI.textMuted,
              },
              "& .MuiOutlinedInput-notchedOutline": {
                borderColor: MAGIC_UI.panelBorder,
              },
            }}
          />
          <Stack direction="row" spacing={1} justifyContent="flex-end" sx={{ mt: 1.25 }}>
            <Button size="small" onClick={closeAgentBranchMenu} disabled={agentBranchSaving}>
              Cancel
            </Button>
            <Button
              size="small"
              variant="contained"
              onClick={applyAgentBranchOverride}
              disabled={agentBranchSaving || !String(agentBranchDraft || "").trim()}
            >
              Apply
            </Button>
          </Stack>
        </Box>
      </Menu>
      <QuickJobDialog
        open={quickJobOpen}
        onClose={() => setQuickJobOpen(false)}
        hostnames={quickJobTargets}
        targetRecords={quickJobTargetRecords}
        deviceLabel={activityHostname || displayHostname}
        notifyOperator={notifyOperator}
      />
      <AgentBranchChannelDialog
        open={agentBranchChannelDialogOpen}
        title="Switch Agent Branch/Channel"
        subtitle={activityHostname || displayHostname}
        rows={agentBranchRows}
        loading={agentBranchesLoading}
        error={agentBranchLoadError}
        channel={draftAgentChannel}
        branch={draftAgentBranch}
        busy={agentBranchChannelSaving}
        onChannelChange={setDraftAgentChannel}
        onBranchChange={setDraftAgentBranch}
        onRefresh={() => void fetchAgentBranches()}
        onCancel={closeAgentBranchChannelDialog}
        onApply={() => void applyAgentBranchChannel()}
        gridTheme={DEVICE_DETAILS_GRID_THEME}
      />
      {loadError ? (
        <Alert severity="error" sx={{ mb: 2 }}>
          {loadError}
        </Alert>
      ) : null}
      <Box
        sx={{
          flexGrow: 1,
          minHeight: 0,
          minWidth: 0,
          width: "100%",
          display: "flex",
          flexDirection: "column",
          borderRadius: 3,
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            flexGrow: 1,
            minHeight: 0,
            minWidth: 0,
            overflowX: "hidden",
            display: "flex",
            flexDirection: "column",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            borderRadius: 3,
            background:
              "linear-gradient(165deg, rgba(2,6,23,0.9), rgba(8,12,32,0.84)), " +
              "radial-gradient(120% 120% at 100% 0%, rgba(192,132,252,0.08), transparent 60%)",
            boxShadow: MAGIC_UI.glow,
          }}
        >
          {renderReadinessHeader()}
          <Box
            id="device-summary-workspace-scrollhost"
            sx={{
              flexGrow: 1,
              p: { xs: 1.5, md: 2 },
              minHeight: 0,
              minWidth: 0,
              overflowX: "hidden",
              overflowY: "auto",
              display: "flex",
              flexDirection: "column",
            }}
          >
            {workspaceContent}
          </Box>
        </Box>
      </Box>

      <ClearDeviceActivityDialog
        open={clearDialogOpen}
        onCancel={() => setClearDialogOpen(false)}
        onConfirm={() => {
          clearHistory();
          setClearDialogOpen(false);
        }}
      />

    </Box>
  );
}
