////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/Devices/Tabs/Device_Summary.jsx

import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";
import { useLoaderData, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Box,
  Stack,
  Tabs,
  Tab,
  Tooltip,
  Typography,
  Button,
  Menu,
  MenuItem,
  TextField,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
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
import SpeedRoundedIcon from "@mui/icons-material/SpeedRounded";
import DeveloperBoardRoundedIcon from "@mui/icons-material/DeveloperBoardRounded";
import FolderRoundedIcon from "@mui/icons-material/FolderRounded";
import MoreHorizIcon from "@mui/icons-material/MoreHoriz";
import { ClearDeviceActivityDialog } from "../../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { AgGridReact } from "ag-grid-react";
import ActivityHistoryTab from "./Activity_History.jsx";
import InstalledSoftwareTab from "./Installed_Software.jsx";
import DeviceWatchdogsTab from "./Device_Watchdogs.jsx";
import RemoteShellTab from "./Remote_Shell.jsx";
import RemoteFileManagementTab from "./Remote_File_Management.jsx";
import ProcessManagementTab from "./Process_Management.jsx";
import { DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI, gridFontFamily } from "./Shared.jsx";
import ServiceList from "./Service_List.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../../app/providers/AuthContext.jsx";
import { useUrlTabState } from "../../app/hooks/useUrlTabState.js";
import { APP_PATHS } from "../../app/routes/paths.js";
import {
  createRouteRequestPlan,
  fetchRouteJson,
  getRouteErrorMessage,
  requireAuthenticatedRequest,
  rethrowIfRouteRedirect,
} from "../../app/routes/routeData.js";
import { createQuickJobDraft, normalizeQuickJobTargets } from "../../app/utils/quickJob.js";

const TUNNEL_STATUS_POLL_INTERVAL_MS = 15000;
const DEVICE_DETAILS_POLL_INTERVAL_MS = 60000;
const ROLE_HEALTH_LAST_CHECKED_COLOR = "rgba(123, 137, 161, 0.9)";
const ROLE_HEALTH_STATUS_COLOR_BY_CODE = Object.freeze({
  healthy: "#00d18c",
  recovering: "#ffb347",
  unhealthy: "#ff7b89",
  pending: "#8fbfff",
  loaded: "#e2e8f0",
  unsupported: "#94a3b8",
  unknown: "#b0b8c8",
});

const PAGE_ICON = DeveloperBoardRoundedIcon;

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
const SUMMARY_SECTION_ACTIVE_BG =
  "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)";
const BOREALIS_LINK_COLOR = "#7db7ff";
const BOREALIS_LINK_HOVER_COLOR = "#a8d4ff";

const BASE_GRID_HEIGHTS = {
  topLevel: 300,
  agentRolesHealth: 240,
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

const TOP_TABS = [
  { key: "summary", label: "Device Summary", icon: InfoOutlinedIcon },
  { key: "file_management", label: "File Management", icon: FolderRoundedIcon },
  { key: "software", label: "Installed Software", icon: AppsRoundedIcon },
  { key: "services", label: "Services", icon: SettingsRoundedIcon },
  { key: "process_management", label: "Processes", icon: AccountTreeRoundedIcon },
  { key: "watchdogs", label: "Watchdogs", icon: PolicyIcon },
  { key: "activity", label: "Activity History", icon: ListAltRoundedIcon },
  { key: "shell", label: "Remote Shell", icon: TerminalRoundedIcon },
];
const DEVICE_DETAILS_TAB_URL_BY_KEY = Object.freeze({
  summary: "device_summary",
  file_management: "file_management",
  software: "installed_software",
  services: "services",
  process_management: "process_management",
  watchdogs: "watchdogs",
  activity: "activity_history",
  shell: "remote_shell",
});
const DEVICE_DETAILS_TAB_KEY_BY_URL = Object.freeze({
  device_summary: "summary",
  summary: "summary",
  file_management: "file_management",
  installed_software: "software",
  software: "software",
  services: "services",
  process_management: "process_management",
  processes: "process_management",
  watchdogs: "watchdogs",
  activity_history: "activity",
  activity: "activity",
  remote_shell: "shell",
  shell: "shell",
});

const resolveDeviceId = (device) =>
  device?.agent_guid ||
  device?.guid ||
  device?.summary?.agent_guid ||
  device?.hostname ||
  device?.id ||
  null;

const SUMMARY_SECTIONS = [
  { key: "top-level", label: "Top-Level", icon: InfoOutlinedIcon },
  { key: "agent-health", label: "Agent Health", icon: DeveloperBoardRoundedIcon },
  { key: "storage", label: "Storage", icon: StorageRoundedIcon },
  { key: "memory", label: "Memory", icon: MemoryRoundedIcon },
  { key: "network", label: "Network", icon: LanRoundedIcon },
];

function formatRoleHealthContext(context) {
  const normalized = String(context || "").trim().toLowerCase();
  if (!normalized) return "";
  if (normalized === "system") return "System";
  if (normalized === "currentuser" || normalized === "current_user" || normalized === "interactive") {
    return "Current User";
  }
  return normalized.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

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

function normalizeRoleHealthStatusText(value) {
  const text = String(value || "").trim();
  if (!text) return "Unknown";
  if (/^[a-z0-9_-]+$/i.test(text)) {
    return text.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
  }
  return text;
}

function getRoleHealthStatusColor(statusCode) {
  const normalized = String(statusCode || "").trim().toLowerCase();
  return ROLE_HEALTH_STATUS_COLOR_BY_CODE[normalized] || SUMMARY_DEFAULT_TEXT_COLOR;
}

const AGENT_HEALTH_KIND = Object.freeze({
  role: "role",
  service: "service",
});

const AGENT_HEALTH_PRESENTATION_BY_KEY = Object.freeze({
  deviceaudit: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  deviceauditor: { label: "Device Auditor", kind: AGENT_HEALTH_KIND.role },
  contextsystem: { label: "System Context", kind: AGENT_HEALTH_KIND.role },
  contextcurrentuser: { label: "Current User Context", kind: AGENT_HEALTH_KIND.role },
  remoteshell: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  remoteshellservice: { label: "Remote Shell", kind: AGENT_HEALTH_KIND.role },
  servicecontrol: { label: "Service Control", kind: AGENT_HEALTH_KIND.role },
  servicemanagement: { label: "Service Management", kind: AGENT_HEALTH_KIND.role },
  softwaremanagement: { label: "Software Management", kind: AGENT_HEALTH_KIND.role },
  scriptexeccurrentuser: { label: "Script Execution - CURRENTUSER", kind: AGENT_HEALTH_KIND.role },
  scriptexecsystem: { label: "Script Execution - SYSTEM", kind: AGENT_HEALTH_KIND.role },
  nodescreenshot: { label: "Node Screenshot", kind: AGENT_HEALTH_KIND.role },
  macros: { label: "Macro Automation", kind: AGENT_HEALTH_KIND.role },
  vnc: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravnc: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  ultravncservice: { label: "UltraVNC", kind: AGENT_HEALTH_KIND.service },
  wireguard: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardtunnel: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardservice: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
  wireguardvpn: { label: "WireGuard VPN", kind: AGENT_HEALTH_KIND.service },
});

const LEGACY_AGENT_HEALTH_KEYS = Object.freeze(
  new Set(["macro", "macroautomation", "macros", "screenshot", "screenshotcapture", "nodescreenshot"])
);

function compactAgentHealthKey(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "");
}

function resolveAgentHealthPresentation(item, index = 0) {
  const rawRoleName = String(item?.role_name || item?.role || "").trim();
  const rawRoleLabel = String(item?.role_label || "").trim();
  const presentation =
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleName)] ||
    AGENT_HEALTH_PRESENTATION_BY_KEY[compactAgentHealthKey(rawRoleLabel)] ||
    null;
  return {
    label: presentation?.label || rawRoleLabel || rawRoleName || `Role ${index + 1}`,
    kind: presentation?.kind || AGENT_HEALTH_KIND.role,
  };
}

function parseAgentHealthHostPort(detailText) {
  const text = String(detailText || "").trim();
  const match = text.match(/\b(?:Listening on\s+)?([^\s:]+):(\d{1,5})\b/i);
  if (!match) return { host: "", port: "" };
  return { host: String(match[1] || "").trim(), port: String(match[2] || "").trim() };
}

function parseAgentHealthTunnelId(detailText) {
  const text = String(detailText || "").trim();
  const match = text.match(/\btunnel_id=([^\s]+)/i);
  return match ? String(match[1] || "").trim() : "";
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

function formatAgentHealthDialogValue(value, fallback = "Unavailable") {
  const text = String(value || "").trim();
  return text || fallback;
}

function buildAgentHealthDialogContent(entry, tunnelInfo) {
  if (!entry) return "";
  const details = entry.detailsMap && typeof entry.detailsMap === "object" ? entry.detailsMap : {};
  const lines = [];
  const appendLine = (label, value, fallback = "Unavailable") => {
    lines.push(`${label}: ${formatAgentHealthDialogValue(value, fallback)}`);
  };
  const appendMultilineSection = (label, value) => {
    const text = String(value || "").trim();
    if (!text) return;
    lines.push("");
    lines.push(`${label}:`);
    for (const line of text.split(/\r?\n/)) {
      const trimmed = String(line || "").trim();
      if (trimmed) {
        lines.push(trimmed);
      }
    }
  };

  const presentationKey = String(entry.presentationKey || "").trim().toLowerCase();
  const parsedHostPort = parseAgentHealthHostPort(entry.detail);
  const fallbackWireGuardPeerIp = tunnelInfo?.virtual_ip ? String(tunnelInfo.virtual_ip).split("/")[0] : "";
  const fallbackTunnelId = String(tunnelInfo?.tunnel_id || "").trim();

  appendLine("Running Status", details.running_status || entry.status, "Unknown");

  switch (presentationKey) {
    case "deviceaudit":
    case "deviceauditor":
      appendLine("Reporter Task", details.reporter_task, "Unknown");
      appendLine("Report Interval", details.report_interval, "Unknown");
      break;
    case "macro":
    case "macros":
      appendLine("Configured Tasks", details.configured_tasks, "0");
      appendLine("Active Tasks", details.active_tasks, "0");
      break;
    case "remoteshell":
    case "remoteshellservice":
      appendLine("IP", details.listener_ip || parsedHostPort.host, "Unavailable");
      appendLine("Port", details.listener_port || parsedHostPort.port, "Unavailable");
      appendLine("Shell Binary", details.shell_binary, "Unavailable");
      break;
    case "screenshot":
    case "nodescreenshot":
      appendLine("Configured Regions", details.configured_regions, "0");
      appendLine("Active Tasks", details.active_tasks, "0");
      appendLine("Visible Overlays", details.visible_overlays, "0");
      break;
    case "scriptexeccurrentuser":
    case "contextcurrentuser":
      appendLine("Execution Context", details.execution_context, "CURRENTUSER");
      appendLine("Listener State", details.listener_state, "Unknown");
      appendMultilineSection("Loaded Helper Sessions", details.loaded_helper_sessions);
      appendMultilineSection("Pending Helper Sessions", details.pending_helper_sessions);
      break;
    case "scriptexecsystem":
    case "contextsystem":
      appendLine("Execution Context", details.execution_context, "SYSTEM");
      appendLine("Listener State", details.listener_state, "Unknown");
      appendLine("Queued Lanes", details.queued_lanes, "Unavailable");
      appendLine("Active Lanes", details.active_lanes, "Unavailable");
      break;
    case "servicemanagement":
      appendLine("Service Count", details.service_count, "0");
      appendLine("Last Refresh", details.last_refresh_at, "Unavailable");
      break;
    case "softwaremanagement":
      appendLine("Software Count", details.software_count, "0");
      appendLine("Icon Payloads", details.icon_payload_count, "0");
      appendLine("Last Refresh", details.last_refresh_at, "Unavailable");
      break;
    case "vnc":
    case "ultravnc":
    case "ultravncservice":
      appendLine("IP", details.listener_ip, "Unavailable");
      appendLine("Port", details.listener_port, "Unavailable");
      appendLine("Service Name", details.service_name, "Unavailable");
      break;
    case "wireguardtunnel":
    case "wireguard":
    case "wireguardservice":
    case "wireguardvpn":
      appendLine("WireGuard Peer IP", details.wireguard_peer_ip || fallbackWireGuardPeerIp, "Inactive");
      appendLine("Tunnel ID", details.tunnel_id || parseAgentHealthTunnelId(entry.detail) || fallbackTunnelId, "Inactive");
      appendLine("Endpoint", details.endpoint, "Unavailable");
      break;
    default:
      break;
  }

  appendLine("Last Checked", entry.lastCheckedText, "Unknown");

  if (String(entry.detail || "").trim()) {
    lines.push("");
    lines.push(`Details: ${String(entry.detail).trim()}`);
  }

  return lines.join("\n");
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

function buildAgentHealthMeta(rows, emptyText, fallbackText) {
  if (!rows.length) return emptyText;
  const counts = rows.reduce((acc, row) => {
    const key = String(row.statusCode || "unknown").trim().toLowerCase();
    acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
  const parts = [];
  if (counts.healthy) parts.push(`${counts.healthy} healthy`);
  if (counts.recovering) parts.push(`${counts.recovering} recovering`);
  if (counts.unhealthy) parts.push(`${counts.unhealthy} unhealthy`);
  if (counts.pending) parts.push(`${counts.pending} pending`);
  if (!parts.length) parts.push(fallbackText);
  return parts.join(" • ");
}

function createAgentHealthColumnDefs(nameHeader, onOpen) {
  return [
    {
      field: "name",
      headerName: nameHeader,
      width: 220,
      flex: 1.1,
      sortable: false,
      filter: false,
      cellRenderer: AgentHealthLinkCell,
      cellRendererParams: { onOpen },
      cellStyle: { color: SUMMARY_FIELD_TEXT_COLOR },
      tooltipValueGetter: (params) => params?.data?.detail || params?.value || "",
    },
    {
      field: "status",
      headerName: "Status",
      width: 170,
      flex: 0.7,
      sortable: false,
      filter: false,
      cellStyle: (params) => ({
        color: getRoleHealthStatusColor(params?.data?.statusCode),
        fontWeight: 600,
      }),
      tooltipValueGetter: (params) => params?.data?.detail || params?.value || "",
    },
    {
      field: "lastCheckedText",
      headerName: "Last Checked",
      width: 220,
      flex: 0.85,
      sortable: false,
      filter: false,
      cellStyle: { color: ROLE_HEALTH_LAST_CHECKED_COLOR },
    },
  ];
}

const AgentHealthLinkCell = React.memo(function AgentHealthLinkCell(props) {
  const row = props?.data || null;
  const onOpen = props?.onOpen;
  const value = String(props?.value || row?.name || "").trim();
  if (!value) return null;
  return (
    <Button
      size="small"
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onOpen?.(row);
      }}
      sx={{
        minWidth: 0,
        px: 0,
        py: 0,
        justifyContent: "flex-start",
        textTransform: "none",
        color: SUMMARY_FIELD_TEXT_COLOR,
        fontWeight: 400,
        textDecoration: "none",
        "&:hover": {
          background: "transparent",
          color: "#7dd3fc",
          textDecoration: "none",
        },
      }}
    >
      {value}
    </Button>
  );
});

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
          paddingTop: 0,
          paddingBottom: 0,
        },
        "& .ag-cell-wrapper": {
          display: "flex",
          alignItems: "center",
          width: "100%",
          minHeight: "100%",
        },
        "& .ag-cell-value": {
          display: "flex",
          alignItems: "center",
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

const SummarySectionsNav = React.memo(function SummarySectionsNav({ onSelectSection }) {
  const [activeSectionKey, setActiveSectionKey] = useState(SUMMARY_SECTIONS[0]?.key || "top-level");
  const observerEventCountRef = useRef(0);

  const handleSelect = useCallback(
    (sectionKey) => {
      summaryGridDebugLog("navClick", { sectionKey });
      setActiveSectionKey(sectionKey);
      onSelectSection?.(sectionKey);
    },
    [onSelectSection]
  );

  useEffect(() => {
    if (typeof window === "undefined" || typeof IntersectionObserver === "undefined") return undefined;
    const sectionElements = SUMMARY_SECTIONS
      .map((section) => document.getElementById(`device-summary-${section.key}`))
      .filter(Boolean);
    if (!sectionElements.length) return undefined;

    summaryGridDebugLog("navObserverStart", {
      sections: sectionElements.map((el) => el.id),
    });

    const observer = new IntersectionObserver(
      (entries) => {
        observerEventCountRef.current += 1;
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio);
        if (!visible.length) return;
        const nextKey = String(visible[0].target.id || "").replace("device-summary-", "");
        if (!nextKey) return;
        setActiveSectionKey((prev) => {
          if (prev === nextKey) return prev;
          summaryGridDebugLog("navObserverSectionChange", {
            from: prev,
            to: nextKey,
            eventCount: observerEventCountRef.current,
            ratio: Number(visible[0].intersectionRatio || 0).toFixed(3),
          });
          return nextKey;
        });
      },
      {
        root: null,
        rootMargin: "-120px 0px -46% 0px",
        threshold: [0.15, 0.35, 0.6],
      }
    );

    sectionElements.forEach((el) => observer.observe(el));
    return () => {
      summaryGridDebugLog("navObserverStop");
      observer.disconnect();
    };
  }, []);

  return (
    <Box
      id="device-summary-sections-nav"
      sx={{
        position: { xs: "static", lg: "sticky" },
        top: { lg: 0 },
        mt: { lg: 0 },
        alignSelf: "start",
        borderRadius: 3,
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background:
          "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), rgba(15,20,28,0.92)",
        backdropFilter: "blur(8px) saturate(130%)",
        overflow: "hidden",
        width: 220,
        minWidth: 220,
        flexShrink: 0,
      }}
    >
      <Box sx={{ px: 1.4, py: 1 }}>
        <Typography
          sx={{
            fontSize: "0.72rem",
            color: "#7db7ff",
            fontWeight: 700,
            letterSpacing: 0.35,
            textTransform: "uppercase",
          }}
        >
          Summary Sections
        </Typography>
      </Box>
      <Box sx={{ py: 0.25 }}>
        {SUMMARY_SECTIONS.map((section) => {
          const active = activeSectionKey === section.key;
          const SectionIcon = section.icon;
          return (
            <Button
              key={section.key}
              onClick={() => handleSelect(section.key)}
              startIcon={<SectionIcon sx={{ fontSize: 18 }} />}
              sx={{
                width: "100%",
                justifyContent: "flex-start",
                textTransform: "none",
                minHeight: NAV_TAB_HEIGHT,
                height: NAV_TAB_HEIGHT,
                py: 0.35,
                px: 1.6,
                borderRadius: 0,
                fontFamily: "inherit",
                fontSize: "0.8rem",
                fontWeight: active ? 600 : 400,
                color: active ? NAV_TAB_COLORS.textActive : NAV_TAB_COLORS.text,
                position: "relative",
                background: active ? SUMMARY_SECTION_ACTIVE_BG : "transparent",
                transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
                "& .MuiButton-startIcon": {
                  color: active ? NAV_TAB_COLORS.iconActive : NAV_TAB_COLORS.icon,
                  mr: 0.9,
                  transition: "color 160ms ease",
                },
                "&:hover": {
                  background: active ? SUMMARY_SECTION_ACTIVE_BG : NAV_TAB_COLORS.hover,
                },
                "&:active": {
                  transform: "translateY(0.5px)",
                },
              }}
            >
              {section.label}
            </Button>
          );
        })}
      </Box>
    </Box>
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
  const { activeKey: activeTabKey, setActiveKey: setActiveTabKey } = useUrlTabState({
    param: "tab",
    defaultKey: TOP_TABS[0]?.key || "summary",
    allowedKeys: TOP_TABS.map((tabDef) => tabDef.key),
    keyByUrl: DEVICE_DETAILS_TAB_KEY_BY_URL,
    urlByKey: DEVICE_DETAILS_TAB_URL_BY_KEY,
  });
  const tab = useMemo(() => {
    const matchIndex = TOP_TABS.findIndex((tabDef) => tabDef.key === activeTabKey);
    return matchIndex >= 0 ? matchIndex : 0;
  }, [activeTabKey]);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const legacyTab = String(params.get("tab") || "").trim().toLowerCase();
    if (!deviceId || (legacyTab !== "remote_desktop" && legacyTab !== "vnc")) {
      return;
    }
    params.delete("tab");
    navigate(
      {
        pathname: APP_PATHS.deviceRemoteDesktop(deviceId),
        search: params.toString() ? `?${params.toString()}` : "",
      },
      { replace: true, state: location.state }
    );
  }, [deviceId, location.search, location.state, navigate]);

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
  const [summaryScrollOffset, setSummaryScrollOffset] = useState(0);
  const [summaryBottomSpacer, setSummaryBottomSpacer] = useState(0);
  const [tunnelInfo, setTunnelInfo] = useState(TUNNEL_INFO_IDLE);
  const [agentHealthDialogEntry, setAgentHealthDialogEntry] = useState(null);
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [releaseChannelMenuPosition, setReleaseChannelMenuPosition] = useState(null);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [updateAgentBusy, setUpdateAgentBusy] = useState(false);
  const [releaseChannelSaving, setReleaseChannelSaving] = useState(false);
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
  const shouldPollTunnelStatus = activeTabKey === "shell" || activeTabKey === "vnc";
  const openAgentHealthDialog = useCallback((entry) => {
    if (!entry) return;
    setAgentHealthDialogEntry(entry);
  }, []);
  const closeAgentHealthDialog = useCallback(() => {
    setAgentHealthDialogEntry(null);
  }, []);
  const agentHealthDialogTitle = useMemo(() => {
    if (!agentHealthDialogEntry?.name) return "Agent Health Details";
    return `Agent Health Details  - ${agentHealthDialogEntry.name}`;
  }, [agentHealthDialogEntry]);
  const agentHealthDialogContent = useMemo(
    () => buildAgentHealthDialogContent(agentHealthDialogEntry, tunnelInfo),
    [agentHealthDialogEntry, tunnelInfo]
  );

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
      [4000, 10000, 20000].forEach((delayMs) => {
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
    const targetHost = activityHostname;
    if (!targetHost || updateAgentBusy) return;
    setUpdateAgentBusy(true);
    try {
      const resp = await fetch(`/api/device/update-agent/${encodeURIComponent(targetHost)}`, {
        method: "POST",
        credentials: "include",
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        let message = String(data?.message || data?.error || `HTTP ${resp.status}`);
        if (String(data?.error || "").trim() === "agent_unavailable") {
          message = "The agent SYSTEM socket is not connected, so Borealis could not start the local AutoUpdater.";
        }
        throw new Error(message);
      }
      await notifyOperator({
        title: "AutoUpdater Requested",
        message: `Asked ${targetHost} to start its local AutoUpdater task immediately.`,
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
  }, [activityHostname, notifyOperator, updateAgentBusy]);

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
          String(data?.agent_release_channel_effective || normalizedChannel || "stable")
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

  const scrollToSummarySection = useCallback((sectionKey) => {
    if (typeof document === "undefined" || typeof window === "undefined") return;
    const target = document.getElementById(`device-summary-${sectionKey}`);
    if (!target) return;
    const nav = document.getElementById("device-summary-sections-nav");
    if (!nav) return;

    const docScroller = document.scrollingElement || document.documentElement;
    let scrollHost = docScroller;
    let node = target.parentElement;
    while (node && node !== document.body) {
      const styles = window.getComputedStyle(node);
      const overflowY = String(styles.overflowY || "").toLowerCase();
      const overflow = String(styles.overflow || "").toLowerCase();
      const canScroll = node.scrollHeight > node.clientHeight + 1;
      const allowsScroll =
        /(auto|scroll|overlay)/.test(overflowY) ||
        /(auto|scroll|overlay)/.test(overflow) ||
        overflowY === "hidden";
      if (canScroll && allowsScroll) {
        scrollHost = node;
        break;
      }
      node = node.parentElement;
    }

    const navRect = nav.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const hostRect =
      scrollHost === docScroller
        ? { top: 0, height: window.innerHeight || document.documentElement.clientHeight || 0 }
        : scrollHost.getBoundingClientRect();
    const navTopInHost = Math.max(0, Math.round(navRect.top - hostRect.top));
    const hostViewportHeight =
      scrollHost === docScroller
        ? window.innerHeight || document.documentElement.clientHeight || 0
        : scrollHost.clientHeight || hostRect.height || 0;
    const spacer = Math.max(0, Math.round(hostViewportHeight - navTopInHost + 28));
    const delta = targetRect.top - navRect.top;

    summaryGridDebugLog("scrollToSection", {
      sectionKey,
      scrollHostTag: scrollHost?.tagName || "document",
      navTopInHost,
      spacer,
      delta: Math.round(delta),
    });

    setSummaryScrollOffset((prev) => (prev === navTopInHost ? prev : navTopInHost));
    setSummaryBottomSpacer((prev) => (prev === spacer ? prev : spacer));

    if (scrollHost === docScroller) {
      const current = window.scrollY || docScroller.scrollTop || 0;
      window.scrollTo({ top: Math.max(0, Math.round(current + delta)), behavior: "smooth" });
      return;
    }
    const current = scrollHost.scrollTop || 0;
    scrollHost.scrollTo({ top: Math.max(0, Math.round(current + delta)), behavior: "smooth" });
  }, []);

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

const MetricCard = ({ icon, title, main, sub, compact = false, sx }) => (
    <Box
      sx={{
        px: compact ? 1.5 : 2.4,
        py: compact ? 1.4 : 2,
        borderRadius: compact ? 2 : 3,
        border: 'none',
        background: 'transparent',
        boxShadow: 'none',
        minWidth: compact ? 0 : 220,
        width: compact ? "100%" : "auto",
        minHeight: compact ? 110 : 140,
        display: "flex",
        flexDirection: "column",
        gap: 0.75,
        ...(sx || {}),
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
        <Box
          sx={{
            width: compact ? 22 : 36,
            height: compact ? 22 : 36,
            borderRadius: 2,
            background: "transparent",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: NAV_TAB_COLORS.icon,
          }}
        >
          {icon}
        </Box>
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : "0.75rem",
            letterSpacing: compact ? 0 : 0.6,
            textTransform: compact ? "none" : "uppercase",
            color: compact ? NAV_TAB_COLORS.text : "rgba(255,255,255,0.72)",
            fontWeight: compact ? 400 : 600,
          }}
        >
          {title}
        </Typography>
      </Box>
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : { xs: "1.4rem", md: "1.6rem" },
            fontWeight: compact ? 600 : 700,
            color: compact ? NAV_TAB_COLORS.textActive : "#f8fafc",
            lineHeight: compact ? 1.15 : 1.1,
            ...(compact
              ? {
                  whiteSpace: "normal",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                }
              : {}),
          }}
        >
          {main}
        </Typography>
      {sub ? (
        <Typography
          sx={{
            fontSize: compact ? "0.8rem" : "0.9rem",
            color: compact ? NAV_TAB_COLORS.text : "rgba(226,232,240,0.78)",
            fontWeight: compact ? 400 : 400,
            ...(compact
              ? {
                  whiteSpace: "normal",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                }
              : {}),
          }}
        >
          {sub}
        </Typography>
      ) : null}
    </Box>
  );

  const Island = ({ title, icon = null, meta = "", children, sx }) => (
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
            <Box
              sx={{
                display: "grid",
                gridTemplateColumns: { xs: "1fr", lg: "220px minmax(0,1fr)" },
                gap: 1.5,
                alignItems: "start",
                minWidth: 0,
              }}
            >
              <SummarySectionsNav onSelectSection={scrollToSummarySection} />

              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, minWidth: 0, width: "100%" }}>
                <Box id="device-summary-top-level" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Top-Level Information"
                    icon={<InfoOutlinedIcon sx={{ fontSize: 18 }} />}
                    meta="Identity and lifecycle"
                    sx={{ mb: 0 }}
                  >
                    <Box
                      sx={{
                        display: "grid",
                        gridTemplateColumns: { xs: "1fr", xl: "minmax(260px,0.72fr) minmax(0,1.6fr)" },
                        alignItems: { xs: "start", xl: "end" },
                        gap: { xs: 1.1, md: 1.5 },
                        mb: 1.9,
                        minWidth: 0,
                      }}
                    >
                      <Box sx={{ display: "flex", flexDirection: "column", gap: 1.1, minWidth: 0 }}>
                        <Box sx={{ width: "100%", maxWidth: { xs: "100%", xl: 440 } }}>
                          <TextField
                            size="small"
                            label="Description"
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                            onBlur={saveDescription}
                            placeholder="Add a friendly label"
                            sx={{
                              width: "100%",
                              input: { color: "#fff" },
                              "& .MuiOutlinedInput-root": {
                                backgroundColor: "rgba(4,7,17,0.65)",
                                borderRadius: 3,
                                "& fieldset": {
                                  borderColor: "rgba(148,163,184,0.45)",
                                  borderRadius: 3,
                                },
                                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
                              },
                              label: { color: MAGIC_UI.textMuted },
                            }}
                          />
                        </Box>
                        {connectionType === "ssh" && (
                          <Box sx={{ display: "flex", flexWrap: "wrap", gap: 1.1, alignItems: "center" }}>
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
                      </Box>

                      <Box
                        sx={{
                          display: "grid",
                          minWidth: 0,
                          gridTemplateColumns: {
                            xs: "1fr",
                            sm: "repeat(2, minmax(0,1fr))",
                            xl: "minmax(0,1.5fr) repeat(3, minmax(0,1fr))",
                          },
                          gap: 1.1,
                          width: "100%",
                          mt: { xs: 0.4, xl: 0 },
                          pt: { xs: 0, xl: 0 },
                          alignSelf: { xs: "start", xl: "end" },
                          "& > *": {
                            background: "transparent !important",
                            border: "none !important",
                            boxShadow: "none !important",
                            borderRadius: 0,
                          },
                        }}
                      >
                        <MetricCard
                          compact
                          icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Processor"
                          main={deviceMetricData.cpuMain}
                          sub={deviceMetricData.cpuSub}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<MemoryRoundedIcon sx={{ fontSize: 18 }} />}
                          title="RAM"
                          main={deviceMetricData.memVal}
                          sub={deviceMetricData.memSpeed || " "}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<StorageRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Storage"
                          main={deviceMetricData.storageMain}
                          sub={deviceMetricData.storageSub || " "}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                        <MetricCard
                          compact
                          icon={<SpeedRoundedIcon sx={{ fontSize: 18 }} />}
                          title="Network"
                          main={deviceMetricData.netVal}
                          sub={deviceMetricData.nicLabel}
                          sx={{ minWidth: 0, width: "100%" }}
                        />
                      </Box>
                    </Box>

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
                          Overview
                        </Typography>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="top-level-overview"
                            rowData={overviewInfoRows}
                            columnDefs={topLevelSplitColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={topLevelSplitGridHeight}
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

                <Box id="device-summary-agent-health" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Agent Health"
                    icon={<DeveloperBoardRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={agentHealthMeta}
                    sx={{ mb: 0 }}
                  >
                    <Stack direction={{ xs: "column", xl: "row" }} spacing={1.6} sx={{ minWidth: 0 }}>
                      <Box sx={{ flex: 1, minWidth: 0 }}>
                        <Box sx={{ mb: 1.2 }}>
                          <Typography
                            sx={{
                              color: SUMMARY_FIELD_TEXT_COLOR,
                              fontSize: "0.88rem",
                              fontWeight: 700,
                              letterSpacing: 0.3,
                              textTransform: "none",
                              display: "block",
                            }}
                          >
                            Roles
                          </Typography>
                          <Typography
                            variant="caption"
                            sx={{ color: MAGIC_UI.textMuted, letterSpacing: 0.16, display: "block", mt: 0.25 }}
                          >
                            {agentRoleHealthMeta}
                          </Typography>
                        </Box>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="agent-health-roles"
                            rowData={agentRoleHealthRows}
                            columnDefs={agentRoleHealthColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={agentHealthGridHeight}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={agentHealthGridHeight} />
                        )}
                      </Box>
                      <Box sx={{ flex: 1, minWidth: 0 }}>
                        <Box sx={{ mb: 1.2 }}>
                          <Typography
                            sx={{
                              color: SUMMARY_FIELD_TEXT_COLOR,
                              fontSize: "0.88rem",
                              fontWeight: 700,
                              letterSpacing: 0.3,
                              textTransform: "none",
                              display: "block",
                            }}
                          >
                            Services
                          </Typography>
                          <Typography
                            variant="caption"
                            sx={{ color: MAGIC_UI.textMuted, letterSpacing: 0.16, display: "block", mt: 0.25 }}
                          >
                            {agentServiceHealthMeta}
                          </Typography>
                        </Box>
                        {summaryDataReady ? (
                          <SummarySectionGrid
                            sectionKey="agent-health-services"
                            rowData={agentServiceHealthRows}
                            columnDefs={agentServiceHealthColumnDefs}
                            defaultColDef={defaultGridColDef}
                            height={agentHealthGridHeight}
                          />
                        ) : (
                          <SummaryGridPlaceholder height={agentHealthGridHeight} />
                        )}
                      </Box>
                    </Stack>
                  </Island>
                </Box>

                <Box id="device-summary-storage" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Storage"
                    icon={<StorageRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={
                      hardwareOverview.storageCount
                        ? `${hardwareOverview.storageCount} volumes${
                            hardwareOverview.storageCritical > 0
                              ? ` • ${hardwareOverview.storageCritical} exceeding ${STORAGE_USAGE_ALERT_THRESHOLD_PCT}% usage`
                              : ""
                          }`
                        : "No storage telemetry"
                    }
                    sx={{ mb: 0 }}
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

                <Box id="device-summary-memory" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Memory"
                    icon={<MemoryRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={
                      hardwareOverview.memoryCount
                        ? `${hardwareOverview.installedMemory}/${hardwareOverview.memoryCount} populated slots`
                        : "No memory telemetry"
                    }
                    sx={{ mb: 0 }}
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

                <Box id="device-summary-network" sx={{ scrollMarginTop: `${summaryScrollOffset}px` }}>
                  <Island
                    title="Network"
                    icon={<LanRoundedIcon sx={{ fontSize: 18 }} />}
                    meta={`Internal ${hardwareOverview.internalIp} • External ${hardwareOverview.externalIp}`}
                    sx={{ mb: 0 }}
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
                <Box sx={{ width: "100%", height: `${summaryBottomSpacer}px`, flexShrink: 0 }} />
              </Box>
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
      const effectiveChannel =
        String(
          meta.agentReleaseChannelEffective ||
            summary.agent_release_channel_effective ||
            meta.agentReleaseChannelOverride ||
            summary.agent_release_channel_override ||
            "stable"
        )
          .trim()
          .toLowerCase() || "stable";
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
          id: "agent-channel",
          label: "Release Channel",
          value: effectiveChannel,
          changeActionVisible: Boolean(isAdmin),
          changeActionText: releaseChannelSaving ? "[saving...]" : "[change]",
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
      meta.agentReleaseChannelEffective,
      meta.agentReleaseChannelOverride,
      meta.agentTargetBuildId,
      meta.agentTargetPublishedAt,
      meta.agentUpdateState,
      meta.lastEnrollmentAt,
      meta.lastEnrollmentAtIso,
      meta.created,
      isAdmin,
      releaseChannelSaving,
      summary.agent_guid,
      summary.agent_version_status,
      summary.agent_release_channel_effective,
      summary.agent_release_channel_override,
      summary.agent_target_build_id,
      summary.agent_target_published_at,
      summary.agent_update_state,
      summary.last_enrollment_at,
      summary.created,
      device?.agent_guid,
      device?.last_enrollment_at,
      device?.created,
      device?.created_at,
      agent?.agent_guid,
      formatDateValue,
    ]
  );

  const agentHealthRows = useMemo(() => {
    const payload =
      meta.agentRoleHealth && typeof meta.agentRoleHealth === "object"
        ? meta.agentRoleHealth
        : summary.agent_role_health && typeof summary.agent_role_health === "object"
          ? summary.agent_role_health
          : {};
    const items = Array.isArray(payload?.roles) ? payload.roles : [];
    const normalizedItems = items.map((item, index) => {
      const rawRoleName = String(item?.role_name || item?.role || "").trim();
      const rawRoleLabel = String(item?.role_label || item?.label || "").trim();
      const presentation = resolveAgentHealthPresentation(item, index);
      return {
        id: item?.role_id || `${presentation.kind}-${presentation.label}-${index}`,
        baseLabel: presentation.label,
        presentationKey: compactAgentHealthKey(rawRoleName || rawRoleLabel || presentation.label),
        healthKind: presentation.kind,
        sourceRoleName: rawRoleName,
        sourceRoleLabel: rawRoleLabel,
        contextLabel: formatRoleHealthContext(item?.context),
        status: normalizeRoleHealthStatusText(item?.status || item?.status_code || "Unknown"),
        statusCode: String(item?.status_code || item?.status || "unknown").trim().toLowerCase(),
        lastCheckedAt: item?.last_checked_at ?? null,
        lastCheckedText: formatTimestamp(item?.last_checked_at),
        detail: String(item?.detail || "").trim(),
        detailsMap: item?.details && typeof item.details === "object" ? item.details : {},
      };
    });
    const visibleItems = normalizedItems.filter(
      (item) => !LEGACY_AGENT_HEALTH_KEYS.has(String(item.presentationKey || "").trim().toLowerCase())
    );
    const labelCounts = visibleItems.reduce((acc, item) => {
      const key = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
      if (key !== `${item.healthKind}:`) {
        acc[key] = (acc[key] || 0) + 1;
      }
      return acc;
    }, {});
    return visibleItems
      .map((item) => {
        const labelKey = `${item.healthKind}:${String(item.baseLabel || "").trim().toLowerCase()}`;
        const name =
          item.contextLabel && labelCounts[labelKey] > 1 ? `${item.baseLabel} (${item.contextLabel})` : item.baseLabel;
        return {
          ...item,
          name,
        };
      })
      .sort((left, right) => String(left.name || "").localeCompare(String(right.name || "")));
  }, [meta.agentRoleHealth, summary.agent_role_health, formatTimestamp]);

  const agentRoleHealthRows = useMemo(
    () => agentHealthRows.filter((row) => row.healthKind === AGENT_HEALTH_KIND.role),
    [agentHealthRows]
  );

  const agentServiceHealthRows = useMemo(
    () => agentHealthRows.filter((row) => row.healthKind === AGENT_HEALTH_KIND.service),
    [agentHealthRows]
  );

  const agentHealthMeta = useMemo(() => {
    if (!agentHealthRows.length) {
      return "Awaiting agent health telemetry";
    }
    const parts = [];
    if (agentRoleHealthRows.length) {
      parts.push(`${agentRoleHealthRows.length} ${agentRoleHealthRows.length === 1 ? "role" : "roles"}`);
    }
    if (agentServiceHealthRows.length) {
      parts.push(`${agentServiceHealthRows.length} ${agentServiceHealthRows.length === 1 ? "service" : "services"}`);
    }
    const statusMeta = buildAgentHealthMeta(agentHealthRows, "", `${agentHealthRows.length} items reporting`);
    if (statusMeta) parts.push(statusMeta);
    return parts.filter(Boolean).join(" • ");
  }, [agentHealthRows, agentRoleHealthRows.length, agentServiceHealthRows.length]);

  const agentRoleHealthMeta = useMemo(
    () =>
      buildAgentHealthMeta(
        agentRoleHealthRows,
        "No role telemetry reported yet",
        `${agentRoleHealthRows.length} roles reporting`
      ),
    [agentRoleHealthRows]
  );

  const agentServiceHealthMeta = useMemo(
    () =>
      buildAgentHealthMeta(
        agentServiceHealthRows,
        "No service telemetry reported yet",
        `${agentServiceHealthRows.length} services reporting`
      ),
    [agentServiceHealthRows]
  );

  const agentRoleHealthColumnDefs = useMemo(
    () => createAgentHealthColumnDefs("Role", openAgentHealthDialog),
    [openAgentHealthDialog]
  );
  const agentServiceHealthColumnDefs = useMemo(
    () => createAgentHealthColumnDefs("Service", openAgentHealthDialog),
    [openAgentHealthDialog]
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
      if (row?.id === "agent-channel") {
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
                disabled={releaseChannelSaving}
                onClick={openReleaseChannelMenu}
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  p: 0,
                  m: 0,
                  border: 0,
                  background: "transparent",
                  color: BOREALIS_LINK_COLOR,
                  cursor: releaseChannelSaving ? "default" : "pointer",
                  font: "inherit",
                  fontSize: "0.82rem",
                  lineHeight: 1.45,
                  textDecoration: "none",
                  transition: "color 160ms ease, opacity 160ms ease",
                  "&:hover": releaseChannelSaving
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
            alignItems: "center",
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
        </Box>
      );
    },
    [openReleaseChannelMenu, releaseChannelSaving]
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
      maxHeight: 420,
    });
    return Math.round(baseHeight * 1.2);
  }, [overviewInfoRows.length, borealisAgentRows.length, resolveGridHeight]);
  const agentHealthGridHeight = useMemo(
    () =>
      Math.round(
        resolveGridHeight(Math.max(agentRoleHealthRows.length, agentServiceHealthRows.length, 1), {
          minHeight: BASE_GRID_HEIGHTS.agentRolesHealth,
        }) * 1.2
      ),
    [agentRoleHealthRows.length, agentServiceHealthRows.length, resolveGridHeight]
  );
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
    const topTabKey = TOP_TABS[tab]?.key || "";
    if (topTabKey !== "summary") return;
    summaryGridDebugLog("deviceSummaryRender", {
      renderCount: pageRenderCountRef.current,
      topTabKey,
      summaryScrollOffset,
      summaryBottomSpacer,
      overviewRows: overviewInfoRows.length,
      agentRows: borealisAgentRows.length,
      storageRows: storageRows.length,
      memoryRows: memoryRows.length,
      networkRows: networkRows.length,
    });
  }, [
    tab,
    summaryScrollOffset,
    summaryBottomSpacer,
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

  const renderFileManagementTab = () => <RemoteFileManagementTab device={tunnelDevice} />;

  const rawDisplayHostname = meta.hostname || summary.hostname || agent.hostname || device?.hostname || "";
  const displayHostname = formatHostnameForDisplay(rawDisplayHostname) || "Device Summary";
  const pageSubtitle = status ? `Status: ${status}` : "";

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "device-summary-remote-desktop",
        label: "Remote Desktop",
        icon: <DesktopWindowsRoundedIcon />,
        tone: "secondary",
        disabled: !deviceId,
        onClick: () => {
          if (!deviceId) return;
          navigate(APP_PATHS.deviceRemoteDesktop(deviceId), {
            state: { initialDevice: tunnelDevice },
          });
        },
      },
      {
        id: "device-summary-actions",
        label: "Actions",
        icon: <MoreHorizIcon />,
        tone: "primary",
        disabled: !activityHostname,
        onClick: (event) => setMenuAnchor(event.currentTarget),
      },
    ],
    [activityHostname, deviceId, navigate, tunnelDevice]
  );

  useRoutePageChrome({
    title: displayHostname,
    subtitle: pageSubtitle,
    Icon: PAGE_ICON,
    actions: pageHeaderActions,
  });

  const topTabRenderers = [
    renderDeviceSummaryTab,
    renderFileManagementTab,
    renderSoftware,
    renderServicesTab,
    renderProcessManagementTab,
    renderWatchdogsTab,
    renderHistory,
    renderRemoteShellTab,
  ];
  const tabContent = (topTabRenderers[tab] || renderDeviceSummaryTab)();

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
            const quickJobDraft = createQuickJobDraft(quickJobTargets);
            if (!quickJobDraft) return;
            navigate(APP_PATHS.jobNew, { state: { quickJobDraft } });
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
          selected={String(meta.agentReleaseChannelOverride || summary.agent_release_channel_override || "").trim().toLowerCase() === "unstable"}
          disabled={releaseChannelSaving}
          onClick={() => handleReleaseChannelSelection("unstable")}
        >
          Unstable
        </MenuItem>
      </Menu>
      {loadError ? (
        <Alert severity="error" sx={{ mb: 2 }}>
          {loadError}
        </Alert>
      ) : null}
      <Tabs
        value={tab}
        onChange={(e, v) => setActiveTabKey(TOP_TABS[v]?.key || TOP_TABS[0]?.key || "summary")}
        variant="scrollable"
        scrollButtons="auto"
        TabIndicatorProps={{
          style: {
            height: 3,
            borderRadius: 3,
            background: NAV_TAB_COLORS.iconActive,
          },
        }}
        sx={{
          borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
          mb: 2,
          minHeight: NAV_TAB_HEIGHT,
          height: NAV_TAB_HEIGHT,
          minWidth: 0,
          maxWidth: "100%",
          "& .MuiTabs-flexContainer": {
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            alignItems: "stretch",
          },
          "& .MuiTab-root": {
            color: NAV_TAB_COLORS.text,
            textTransform: "none",
            fontWeight: 400,
            fontFamily: "inherit",
            fontSize: "0.8rem",
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            opacity: 1,
            borderRadius: 1,
            py: 0.35,
            transition:
              "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
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
        }}
      >
        {TOP_TABS.map((tabDef) => (
          <Tab
            key={tabDef.key || tabDef.label}
            label={tabDef.label}
            icon={<tabDef.icon sx={{ fontSize: 18 }} />}
            iconPosition="start"
          />
        ))}
      </Tabs>
      <Box
        sx={{
          mt: 1,
          flexGrow: 1,
          minHeight: 0,
          minWidth: 0,
          width: "100%",
          overflowX: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        {tabContent}
      </Box>

      <Dialog
        open={Boolean(agentHealthDialogEntry)}
        onClose={closeAgentHealthDialog}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={agentHealthDialogTitle} subtitle="Current health telemetry and runtime details." />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            fullWidth
            multiline
            minRows={12}
            value={agentHealthDialogContent}
            InputProps={{ readOnly: true }}
            sx={{
              "& .MuiOutlinedInput-root": {
                alignItems: "flex-start",
                bgcolor: "rgba(4,7,17,0.72)",
                borderRadius: 2,
                fontFamily:
                  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                fontSize: 12.5,
                color: "#e6edf3",
                "& textarea": {
                  lineHeight: 1.55,
                  whiteSpace: "pre-wrap",
                },
              },
            }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={closeAgentHealthDialog} sx={DIALOG_BUTTON_SX}>
            Close
          </Button>
        </DialogActions>
      </Dialog>

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
