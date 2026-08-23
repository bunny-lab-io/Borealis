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
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
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
import SystemUpdateAltRoundedIcon from "@mui/icons-material/SystemUpdateAltRounded";
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
import PatchManagementTab from "./Patch_Management.jsx";
import DeviceWatchdogsTab from "./Device_Watchdogs.jsx";
import RemoteShellTab from "./Remote_Shell.jsx";
import RemoteFileManagementTab from "./Remote_File_Management.jsx";
import RemoteRegistryEditorTab from "./Remote_Registry_Editor.jsx";
import ProcessManagementTab from "./Process_Management.jsx";
import { buildAgentHealthRows } from "./Agent_Health.jsx";
import {
  CopyHealthButton,
  CopyableHealthTooltip,
  RuntimeRoleHealthSidebarRows,
  buildHealthCopyText,
  buildRuntimeHealthTooltipRows,
  getRuntimeHealthTooltipDetail,
} from "./Agent_Startup_Flow.jsx";
import DeviceMetadataTab from "./Device_Metadata.jsx";
import { DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI, gridFontFamily } from "./Shared.jsx";
import ServiceList from "./Service_List.jsx";
import AgentUpdates, { agentUpdateIsActive, openAgentUpdateJob, useAgentUpdateHistory } from "./Agent_Updates.jsx";
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
import {
  STORAGE_USAGE_ALERT_COLOR,
  STORAGE_USAGE_ALERT_LABEL,
  STORAGE_USAGE_ALERT_THRESHOLD_PCT,
  isStorageUsageAlert,
} from "./storageAlerts.js";
import {
  LEGACY_TAB_TO_WORKSPACE,
  WORKSPACE_KEYS,
  createDeviceWorkspaceSearch,
  normalizeWorkspaceKey,
  normalizeWorkspaceView,
  pruneDeviceWorkspaceContextParams,
} from "./deviceWorkspaceUrlState.js";

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
    backgroundColor: "transparent",
    backgroundImage: active ? NAV_TAB_COLORS.activeBg : "none",
    backgroundRepeat: "no-repeat",
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    justifyContent: "space-between",
    transition: "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "&:hover": {
      backgroundColor: active ? undefined : NAV_TAB_COLORS.hover,
      backgroundImage: active ? NAV_TAB_COLORS.activeBg : "none",
    },
    '&.Mui-selected, &[data-summary-active="true"]': {
      color: NAV_TAB_COLORS.textActive,
      backgroundImage: NAV_TAB_COLORS.activeBg,
      backgroundRepeat: "no-repeat",
    },
    '&.Mui-selected:hover, &[data-summary-active="true"]:hover': {
      backgroundImage: NAV_TAB_COLORS.activeBg,
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
  if (normalized === "checking") return "Checking for updates";
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

const DescriptionCellEditor = React.memo(function DescriptionCellEditor({
  value = "",
  onDraftChange = null,
  onSave = null,
}) {
  const [draft, setDraft] = useState(() => String(value || ""));
  const focusedRef = useRef(false);

  useEffect(() => {
    if (!focusedRef.current) {
      setDraft(String(value || ""));
    }
  }, [value]);

  const handleChange = useCallback(
    (event) => {
      const nextValue = event.target.value;
      setDraft(nextValue);
      onDraftChange?.(nextValue);
    },
    [onDraftChange]
  );

  const handleBlur = useCallback(() => {
    focusedRef.current = false;
    onSave?.(draft);
  }, [draft, onSave]);

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
        value={draft}
        onFocus={() => {
          focusedRef.current = true;
        }}
        onChange={handleChange}
        onBlur={handleBlur}
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
});

function DeviceSidebarHealthGroup({ id, label, summary, icon, expanded, onChange, copyText = "", children }) {
  return (
    <Accordion
      expanded={expanded}
      onChange={(_, nextExpanded) => onChange(id, nextExpanded)}
      square
      disableGutters
      sx={{
        mx: 0.45,
        my: "0 !important",
        bgcolor: "transparent",
        background: "transparent !important",
        backgroundColor: "transparent !important",
        backgroundImage: "none !important",
        border: 0,
        boxShadow: "none",
        position: "relative",
        "&:before": { display: "none" },
      }}
    >
      <Tooltip
        title={<CopyableHealthTooltip title={label} status={summary || ""} />}
        arrow
        placement="right"
      >
        <AccordionSummary
          expandIcon={<ExpandMoreIcon sx={{ color: NAV_TAB_COLORS.iconActive, fontSize: 18 }} />}
          sx={{
            minHeight: 40,
            pl: 1.55,
            pr: 0.85,
            py: 0,
            borderRadius: 0,
            color: NAV_TAB_COLORS.text,
            background: "transparent",
            backgroundColor: "transparent",
            backgroundImage: "none",
            transition: "background 160ms ease, color 160ms ease, transform 120ms ease",
            "&:hover": { background: NAV_TAB_COLORS.hover, backgroundColor: NAV_TAB_COLORS.hover },
            "&.Mui-expanded": { minHeight: 40, background: "transparent", backgroundColor: "transparent" },
            "&.Mui-expanded:hover": { background: NAV_TAB_COLORS.hover, backgroundColor: NAV_TAB_COLORS.hover },
            "& .MuiAccordionSummary-content": { my: 0, minWidth: 0, alignItems: "center" },
            "& .MuiAccordionSummary-content.Mui-expanded": { my: 0 },
          }}
        >
          <Box sx={{ color: NAV_TAB_COLORS.icon, display: "flex", alignItems: "center", mr: 1, flexShrink: 0 }}>
            {icon}
          </Box>
          <Typography sx={{ color: "inherit", fontSize: "0.8rem", fontWeight: 400, lineHeight: 1.45 }} noWrap>
            {label}
          </Typography>
        </AccordionSummary>
      </Tooltip>
      {copyText ? (
        <CopyHealthButton
          copyText={copyText}
          label={`Copy all ${label} health details`}
          sx={{
            position: "absolute",
            top: 8,
            right: 31,
            zIndex: 2,
            color: NAV_TAB_COLORS.iconActive,
            "&:hover": { backgroundColor: NAV_TAB_COLORS.hover },
          }}
        />
      ) : null}
      <AccordionDetails
        sx={{
          p: 0,
          mx: 0.35,
          mb: 0.45,
          borderLeft: "1px solid rgba(125,183,255,0.13)",
          borderRadius: 1,
          backgroundColor: "rgba(2,6,23,0.38)",
          overflow: "hidden",
        }}
      >
        {children}
      </AccordionDetails>
    </Accordion>
  );
}

function getDeviceReadinessConnectionRowMeta(tone) {
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
}

const HEALTHY_AGENT_ROLE_STATUS_CODES = new Set([
  "healthy",
  "loaded",
  "ok",
  "online",
  "running",
  "ready",
  "complete",
  "completed",
]);
const WARNING_AGENT_ROLE_STATUS_CODES = new Set(["pending", "recovering", "reconnecting", "starting", "stale"]);
const MUTED_AGENT_ROLE_STATUS_CODES = new Set(["", "unknown", "missing", "not_applicable", "unsupported"]);

function getAgentRoleHealthTone(roleHealth) {
  if (!roleHealth) return "muted";
  const statusCode = String(roleHealth?.statusCode || roleHealth?.status || "").trim().toLowerCase();
  if (HEALTHY_AGENT_ROLE_STATUS_CODES.has(statusCode)) return "ready";
  if (WARNING_AGENT_ROLE_STATUS_CODES.has(statusCode)) return "warning";
  if (MUTED_AGENT_ROLE_STATUS_CODES.has(statusCode)) return "muted";
  return "danger";
}

function renderDeviceReadinessConnectionTooltip(row) {
  return <CopyableHealthTooltip title={row.label} status={row.value} rows={row.tooltipRows} detail={row.detail} />;
}

function DeviceSidebarHealthConnectionRow({ row, index }) {
  const rowMeta = getDeviceReadinessConnectionRowMeta(row.tone);
  const RowIcon = rowMeta.Icon;
  return (
    <Tooltip key={`${row.group || "connection"}-${row.label}-${index}`} title={renderDeviceReadinessConnectionTooltip(row)} arrow placement="right">
      <Box
        aria-label={`${row.label}: ${row.value}`}
        sx={{
          minHeight: 34,
          px: 0.8,
          py: 0.45,
          borderRadius: 1,
          color: MAGIC_UI.textBright,
          display: "flex",
          alignItems: "center",
          gap: 0.7,
          minWidth: 0,
          cursor: "help",
          transition: "background 140ms ease",
          "&:hover": { background: "rgba(125,183,255,0.08)" },
        }}
      >
        <RowIcon sx={{ color: rowMeta.color, fontSize: 15, flexShrink: 0 }} />
        <Box sx={{ minWidth: 0, flex: 1 }}>
          <Typography sx={{ color: NAV_TAB_COLORS.text, fontSize: "0.67rem", fontWeight: 500, lineHeight: 1.15 }} noWrap>
            {row.label}
          </Typography>
        </Box>
      </Box>
    </Tooltip>
  );
}

const DeviceSidebarHealth = React.memo(function DeviceSidebarHealth({
  roleHealthSummary,
  agentManagementSummary,
  agentManagementGroups,
  agentHealthRows,
}) {
  const [expandedGroups, setExpandedGroups] = useState({ management: false, roles: false });
  const handleGroupChange = useCallback((groupId, expanded) => {
    setExpandedGroups((previous) => ({ ...previous, [groupId]: expanded }));
  }, []);
  const agentManagementRows = useMemo(
    () => agentManagementGroups.flatMap((group) => group.rows || []),
    [agentManagementGroups]
  );
  const agentConnectionCopyText = useMemo(
    () =>
      [
        "Agent Connection",
        ...agentManagementRows.map((row) =>
          buildHealthCopyText({
            title: row.label,
            status: row.value,
            rows: row.tooltipRows,
            detail: row.detail,
          })
        ),
      ].join("\n\n"),
    [agentManagementRows]
  );
  const roleHealthCopyText = useMemo(
    () =>
      [
        `Roles - ${roleHealthSummary.label}`,
        ...agentHealthRows.map((entry) => {
          const rows = buildRuntimeHealthTooltipRows(entry);
          return buildHealthCopyText({
            title: entry?.name || "Runtime health",
            status: entry?.status || "Unknown",
            rows,
            detail: getRuntimeHealthTooltipDetail(entry, rows),
          });
        }),
      ].join("\n\n"),
    [agentHealthRows, roleHealthSummary.label]
  );

  return (
    <Box sx={{ px: 0.15, pb: 0.35, minWidth: 0 }}>
      <DeviceSidebarHealthGroup
        id="management"
        label="Agent Connection"
        summary={agentManagementSummary}
        icon={<LanRoundedIcon fontSize="small" />}
        expanded={expandedGroups.management}
        onChange={handleGroupChange}
        copyText={agentConnectionCopyText}
      >
        <Box sx={{ display: "flex", flexDirection: "column", gap: 0.2, p: 0.35 }}>
          {agentManagementRows.map((row, index) => (
            <DeviceSidebarHealthConnectionRow
              key={`${row.group || "connection"}-${row.label}-${index}`}
              row={row}
              index={index}
            />
          ))}
        </Box>
      </DeviceSidebarHealthGroup>
      <DeviceSidebarHealthGroup
        id="roles"
        label="Roles"
        summary={roleHealthSummary.label}
        icon={<DeveloperBoardRoundedIcon fontSize="small" />}
        expanded={expandedGroups.roles}
        onChange={handleGroupChange}
        copyText={roleHealthCopyText}
      >
        <RuntimeRoleHealthSidebarRows runtimeRows={agentHealthRows} />
      </DeviceSidebarHealthGroup>
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
    const originalSearch = params.toString();
    const legacyTab = String(params.get("tab") || "").trim().toLowerCase();
    if (!deviceId) {
      return;
    }
    if (legacyTab === "remote_desktop" || legacyTab === "vnc") {
      params.delete("tab");
      pruneDeviceWorkspaceContextParams(params, "", "");
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
      const workspace = normalizeWorkspaceKey(legacyTab, "inventory");
      const view = normalizeWorkspaceView(workspace, String(params.get("view") || "").trim().toLowerCase());
      pruneDeviceWorkspaceContextParams(params, workspace, view);
      if (params.toString() !== originalSearch) {
        navigate(
          {
            pathname: location.pathname,
            search: params.toString() ? `?${params.toString()}` : "",
          },
          { replace: true, state: location.state }
        );
      }
      return;
    }
    const target = LEGACY_TAB_TO_WORKSPACE[legacyTab];
    if (!target?.workspace) {
      pruneDeviceWorkspaceContextParams(params, "", "");
      if (params.toString() !== originalSearch) {
        navigate(
          {
            pathname: location.pathname,
            search: params.toString() ? `?${params.toString()}` : "",
          },
          { replace: true, state: location.state }
        );
      }
      return;
    }
    params.set("tab", target.workspace);
    if (target.view && !String(params.get("view") || "").trim()) {
      params.set("view", target.view);
    }
    pruneDeviceWorkspaceContextParams(params, target.workspace, params.get("view"));
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
  const [expandedDeviceNavSections, setExpandedDeviceNavSections] = useState({
    health: true,
    inventory: true,
    backend: true,
    protection: true,
    history: true,
    metadata: true,
  });
  const [clearDialogOpen, setClearDialogOpen] = useState(false);
  const [updateConfirmOpen, setUpdateConfirmOpen] = useState(false);
  const [updateAgentBusy, setUpdateAgentBusy] = useState(false);
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
  const summaryNavScrollLockRef = useRef("");
  const summaryNavScrollLockTimerRef = useRef(null);
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
  const agentUpdateDeviceGuid = useMemo(
    () => meta.agentGuid || summary.agent_guid || device?.agent_guid || device?.guid || agent?.agent_guid || agent?.guid || "",
    [agent?.agent_guid, agent?.guid, device?.agent_guid, device?.guid, meta.agentGuid, summary.agent_guid]
  );
  const agentUpdateHistory = useAgentUpdateHistory(agentUpdateDeviceGuid);
  const activeAgentUpdate = agentUpdateHistory.active_operation;
  const selectedAgentUpdateOperationId = String(deviceSearchParams.get("operation_id") || "").trim();
  const openAgentUpdateStatus = useCallback((operationId = "") => {
    const params = new URLSearchParams(location.search || "");
    params.set("tab", "remote_ops");
    params.set("view", "agent_updates");
    if (operationId) params.set("operation_id", operationId);
    else params.delete("operation_id");
    pruneDeviceWorkspaceContextParams(params, "remote_ops", "agent_updates");
    navigate({ pathname: location.pathname, search: `?${params.toString()}` }, { state: location.state });
  }, [location.pathname, location.search, location.state, navigate]);
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
      if (
        payloadChange &&
        payloadChange !== "software_updated" &&
        payloadChange !== "patches_updated" &&
        payloadChange !== "updated"
      ) return;
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
    const targetGuid = agentUpdateDeviceGuid;
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
        title: "Agent Update Requested",
        message: `Queued install-equivalent Agent update for ${targetHost}.`,
        icon: "update",
        variant: "info",
      });
      const operationID = String(data?.queued?.[0]?.operation_id || "").trim();
      await agentUpdateHistory.refresh({ silent: true });
      openAgentUpdateStatus(operationID);
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
  }, [activityHostname, agentUpdateDeviceGuid, agentUpdateHistory.refresh, notifyOperator, openAgentUpdateStatus, updateAgentBusy]);

  const handleDescriptionDraftChange = useCallback((nextDescription) => {
    descriptionDraftRef.current = String(nextDescription ?? "");
  }, []);

  const saveDescription = useCallback(async (nextDescription = descriptionDraftRef.current) => {
    const targetHost = meta.hostname || details.summary?.hostname;
    const draftDescription = String(nextDescription ?? "");
    descriptionDraftRef.current = draftDescription;
    setDescription(draftDescription);
    if (!targetHost) return;
    try {
      const response = await fetch(`/api/device/description/${targetHost}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ description: draftDescription })
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      loadedDescriptionRef.current = draftDescription;
      setDetails((d) => ({
        ...d,
        summary: { ...(d.summary || {}), description: draftDescription }
      }));
      setMeta((m) => ({ ...(m || {}), hostname: targetHost }));
    } catch (e) {
      console.warn("Failed to save description", e);
    }
  }, [details.summary?.hostname, meta.hostname]);

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

  const clearSummaryNavScrollLock = useCallback(() => {
    summaryNavScrollLockRef.current = "";
    if (typeof window === "undefined" || !summaryNavScrollLockTimerRef.current) return;
    window.clearTimeout(summaryNavScrollLockTimerRef.current);
    summaryNavScrollLockTimerRef.current = null;
  }, []);

  const scheduleSummaryNavScrollLockRelease = useCallback((delayMs = 220) => {
    if (typeof window === "undefined") {
      summaryNavScrollLockRef.current = "";
      return;
    }
    if (summaryNavScrollLockTimerRef.current) {
      window.clearTimeout(summaryNavScrollLockTimerRef.current);
    }
    summaryNavScrollLockTimerRef.current = window.setTimeout(() => {
      summaryNavScrollLockRef.current = "";
      summaryNavScrollLockTimerRef.current = null;
    }, delayMs);
  }, []);

  const lockSummaryNavSection = useCallback(
    (sectionKey) => {
      const normalizedSectionKey = normalizeSummarySectionKey(sectionKey);
      summaryNavScrollLockRef.current = normalizedSectionKey;
      markActiveSummaryNavSection(normalizedSectionKey);
      scheduleSummaryNavScrollLockRelease(1800);
    },
    [markActiveSummaryNavSection, scheduleSummaryNavScrollLockRelease]
  );

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
      lockSummaryNavSection(normalizedSectionKey);
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
      lockSummaryNavSection,
      scrollHardwareSummaryToTop,
      scrollToSummarySection,
      setActiveWorkspace,
    ]
  );

  useEffect(() => {
    if (activeWorkspaceKey !== "inventory" || activeWorkspaceView !== "summary") {
      clearSummaryNavScrollLock();
      markActiveSummaryNavSection("");
      return;
    }
    markActiveSummaryNavSection(activeSummaryNavSectionRef.current || DEFAULT_SUMMARY_SECTION_KEY);
  }, [activeWorkspaceKey, activeWorkspaceView, clearSummaryNavScrollLock, markActiveSummaryNavSection]);

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
    if (typeof window === "undefined") return undefined;
    if (activeWorkspaceKey !== "inventory" || activeWorkspaceView !== "summary") return undefined;
    const releaseLockAfterScroll = () => {
      if (!summaryNavScrollLockRef.current) return;
      scheduleSummaryNavScrollLockRelease(220);
    };
    const scrollHost = document.getElementById("device-summary-workspace-scrollhost");
    window.addEventListener("scroll", releaseLockAfterScroll, { passive: true });
    scrollHost?.addEventListener?.("scroll", releaseLockAfterScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", releaseLockAfterScroll);
      scrollHost?.removeEventListener?.("scroll", releaseLockAfterScroll);
    };
  }, [activeWorkspaceKey, activeWorkspaceView, scheduleSummaryNavScrollLockRelease]);

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
        if (summaryNavScrollLockRef.current) return;
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

  const renderPatches = () => (
    <PatchManagementTab hostname={activityHostname} />
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
        disk_type: d.disk_type || d.type || "Fixed Disk",
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
          if (!isStorageUsageAlert(params?.data)) return "";
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
      (row) => isStorageUsageAlert(row)
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
      meta.agentUpdateState,
      meta.lastEnrollmentAt,
      meta.lastEnrollmentAtIso,
      meta.created,
      summary.agent_guid,
      summary.agent_version_status,
      summary.last_seen,
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
  const engineSocketRoleHealth = useMemo(
    () => agentHealthRows.find((entry) => entry?.presentationKey === "enginesocket") || null,
    [agentHealthRows]
  );
  const wireGuardRoleHealth = useMemo(
    () => agentHealthRows.find((entry) => entry?.presentationKey === "wireguardtunnel") || null,
    [agentHealthRows]
  );
  const sidebarAgentHealthRows = useMemo(
    () =>
      agentHealthRows.filter(
        (entry) => !["enginesocket", "wireguardtunnel"].includes(String(entry?.presentationKey || ""))
      ),
    [agentHealthRows]
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
          <DescriptionCellEditor
            value={params?.value ?? row?.value ?? ""}
            onDraftChange={handleDescriptionDraftChange}
            onSave={saveDescription}
          />
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
    [handleDescriptionDraftChange, saveDescription]
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
    const roles = Array.isArray(sidebarAgentHealthRows) ? sidebarAgentHealthRows : [];
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
  }, [sidebarAgentHealthRows]);
  const dataFreshnessLabel = useMemo(() => {
    const rawLastSeen = meta.lastSeen || summary.last_seen || device?.last_seen || agent?.last_seen || 0;
    return formatDateValue(rawLastSeen, "No heartbeat");
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
      return { value: "Reconnecting", tone: "warning", detail: "Agent heartbeat is present while the management socket reconnects." };
    }
    return { value: "Unavailable", tone: "danger", detail: "Agent must heartbeat before management socket can be confirmed." };
  }, [statusIsOnline, tunnelInfo?.agent_socket]);
  const websocketConnection = useMemo(() => {
    const roleTone = getAgentRoleHealthTone(engineSocketRoleHealth);
    let value = agentSocketConnection.value;
    let tone = agentSocketConnection.tone;
    if (roleTone === "danger") {
      value = value === "Unavailable" ? value : "Degraded";
      tone = "danger";
    } else if (roleTone === "warning" && tone === "ready") {
      value = "Degraded";
      tone = "warning";
    }
    return {
      value,
      tone,
      tooltipRows: [
        { label: "Engine registry", value: agentSocketConnection.value },
        { label: "Agent report", value: engineSocketRoleHealth?.status || "No role report" },
        ...buildRuntimeHealthTooltipRows(engineSocketRoleHealth),
      ],
      detail:
        tone === "ready"
          ? ""
          : [agentSocketConnection.detail, engineSocketRoleHealth?.detail].filter(Boolean).join("\n"),
    };
  }, [agentSocketConnection, engineSocketRoleHealth]);
  const secureTunnelConnection = useMemo(() => {
    const roleTone = getAgentRoleHealthTone(wireGuardRoleHealth);
    const transportReady = tunnelInfo?.listener_healthy !== false;
    let value = tunnelConnection.value;
    let tone = tunnelConnection.tone;
    if (tunnelConnection.tone === "ready" && !transportReady) {
      value = "Degraded";
      tone = "danger";
    } else if (roleTone === "danger") {
      value = value === "Down" || value === "Error" ? value : "Degraded";
      tone = "danger";
    } else if (roleTone === "warning" && tone === "ready") {
      value = "Degraded";
      tone = "warning";
    }
    return {
      value,
      tone,
      tooltipRows: [
        { label: "Engine transport", value: tunnelConnection.value },
        { label: "Transport readiness", value: transportReady ? "Ready" : "Not ready" },
        { label: "Agent report", value: wireGuardRoleHealth?.status || "No role report" },
        ...buildRuntimeHealthTooltipRows(wireGuardRoleHealth),
        { label: "Virtual IP", value: tunnelInfo?.virtual_ip || "Not assigned" },
        { label: "Tunnel ID", value: tunnelInfo?.tunnel_id || "No active tunnel" },
        { label: "Agent ID", value: tunnelAgentId || "Unavailable" },
      ],
      detail:
        tone === "ready"
          ? ""
          : [tunnelConnection.detail, wireGuardRoleHealth?.detail].filter(Boolean).join("\n"),
    };
  }, [tunnelAgentId, tunnelConnection, tunnelInfo?.listener_healthy, tunnelInfo?.tunnel_id, tunnelInfo?.virtual_ip, wireGuardRoleHealth]);
  const readiness = useMemo(() => {
    const updateState = String(meta.agentUpdateState || summary.agent_update_state || "").trim().toLowerCase();
    const versionState = String(meta.agentVersionStatus || summary.agent_version_status || "").trim().toLowerCase();
    const managementBlocked =
      ["warning", "danger"].includes(websocketConnection.tone) ||
      ["warning", "danger"].includes(secureTunnelConnection.tone);
    const updateFailed = updateState === "failed" || Boolean(meta.agentUpdateError || summary.agent_update_error);
    const needsUpdate = versionState.includes("need") || versionState.includes("outdated");
    const healthBlocked = !statusIsOnline || managementBlocked || updateFailed || roleHealthSummary.unhealthyCount > 0;
    const storageBlocked = hardwareOverview.storageCritical > 0;
    if (healthBlocked) {
      return {
        tone: "danger",
        headline: !statusIsOnline
          ? "Agent unreachable"
          : managementBlocked
            ? "Remote control degraded"
            : updateFailed
              ? "Agent updater failed"
              : "Agent role degraded",
        detail: !statusIsOnline
          ? "Recover control before running backend tools."
          : managementBlocked
            ? "Agent heartbeat is online while remote-management health recovers."
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
    secureTunnelConnection.tone,
    statusIsOnline,
    summary.agent_update_error,
    summary.agent_update_state,
    summary.agent_version_status,
    websocketConnection.tone,
  ]);
  const agentManagementDetails = useMemo(
    () => [
      {
        group: "Health",
        label: "Heartbeat",
        value: status || "Unknown",
        tone: statusIsOnline ? "ready" : "danger",
        tooltipRows: [{ label: "Last heartbeat", value: dataFreshnessLabel }],
        detail: "",
      },
      {
        group: "Health",
        label: "API Websocket Connection",
        value: websocketConnection.value,
        tone: websocketConnection.tone,
        tooltipRows: websocketConnection.tooltipRows,
        detail: websocketConnection.detail,
      },
      {
        group: "Health",
        label: "Secure Tunnel",
        value: secureTunnelConnection.value,
        tone: secureTunnelConnection.tone,
        tooltipRows: secureTunnelConnection.tooltipRows,
        detail: secureTunnelConnection.detail,
      },
    ].sort((left, right) => String(left.label || "").localeCompare(String(right.label || ""))),
    [
      dataFreshnessLabel,
      secureTunnelConnection.detail,
      secureTunnelConnection.tone,
      secureTunnelConnection.tooltipRows,
      secureTunnelConnection.value,
      status,
      statusIsOnline,
      websocketConnection.detail,
      websocketConnection.tone,
      websocketConnection.tooltipRows,
      websocketConnection.value,
    ]
  );
  const agentManagementSummary = useMemo(() => {
    if (!statusIsOnline) return "Agent heartbeat offline.";
    if (websocketConnection.value !== "Connected") {
      return `Heartbeat online; API websocket ${websocketConnection.value.toLowerCase()}.`;
    }
    if (secureTunnelConnection.value !== "Connected") {
      return `API websocket connected; secure tunnel ${secureTunnelConnection.value.toLowerCase()}.`;
    }
    return "API websocket, heartbeat, and secure tunnel healthy.";
  }, [secureTunnelConnection.value, statusIsOnline, websocketConnection.value]);
  const agentManagementGroups = useMemo(
    () =>
      ["Health"]
        .map((label) => ({
          label,
          rows: agentManagementDetails.filter((entry) => entry.group === label),
        }))
        .filter((group) => group.rows.length),
    [agentManagementDetails]
  );
  const remoteToolsBlocked = [
    !statusIsOnline,
    tunnelInfo?.agent_socket === false,
    ["down", "error"].includes(String(tunnelInfo?.status || "").trim().toLowerCase()),
  ].some(Boolean);
  const workspaceBadges = useMemo(
    () => ({
      storage: hardwareOverview.storageCritical > 0 ? String(hardwareOverview.storageCritical) : "",
      protection: "",
      history: "",
      shell: remoteToolsBlocked ? "1" : "",
      config: "",
    }),
    [hardwareOverview.storageCritical, remoteToolsBlocked]
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
  const agentUpdatesWorkspaceActive =
    activeWorkspaceKey === "remote_ops" && normalizeWorkspaceView("remote_ops", activeWorkspaceView) === "agent_updates";
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

  const pageHeaderActions = useMemo(() => {
    const actions = [];
    if (agentUpdatesWorkspaceActive) {
      actions.push({
        id: "agent-update-now",
        label: "Update Now",
        icon: <SystemUpdateAltRoundedIcon />,
        tone: "primary",
        disabled: !agentUpdateDeviceGuid || updateAgentBusy || agentUpdateIsActive(activeAgentUpdate),
        loading: updateAgentBusy,
        onClick: () => setUpdateConfirmOpen(true),
      });
      return actions;
    }
    actions.push({
      id: "device-summary-actions",
      label: "Actions",
      icon: <MoreHorizIcon />,
      tone: "primary",
      disabled: !activityHostname,
      onClick: (event) => setMenuAnchor(event.currentTarget),
    });
    return actions;
  }, [activeAgentUpdate, activityHostname, agentUpdateDeviceGuid, agentUpdatesWorkspaceActive, updateAgentBusy]);

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
          boxShadow: "none",
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
          <SidebarSection sectionId="health" title="Agent Health">
            <DeviceSidebarHealth
              roleHealthSummary={roleHealthSummary}
              agentManagementSummary={agentManagementSummary}
              agentManagementGroups={agentManagementGroups}
              agentHealthRows={sidebarAgentHealthRows}
            />
          </SidebarSection>
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
            <SidebarNavRow
              icon={<SystemUpdateAltRoundedIcon fontSize="small" />}
              label="Patch Management"
              active={activeView("inventory", "patches")}
              onClick={() => setActiveWorkspace("inventory", "patches")}
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
              label="Registry"
              active={activeView("remote_ops", "registry")}
              onClick={() => setActiveWorkspace("remote_ops", "registry")}
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
            <SidebarNavRow
              icon={<SystemUpdateAltRoundedIcon fontSize="small" />}
              label="Agent Updates"
              active={activeView("remote_ops", "agent_updates")}
              onClick={() => setActiveWorkspace("remote_ops", "agent_updates")}
              badge={agentUpdateIsActive(activeAgentUpdate) ? "1" : ""}
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
            : view === "agent_updates"
              ? (
                <AgentUpdates
                  history={agentUpdateHistory}
                  selectedOperationId={selectedAgentUpdateOperationId}
                  onSelectOperation={(operationID) => openAgentUpdateStatus(operationID)}
                  onOpenJob={(jobID) => openAgentUpdateJob(navigate, jobID)}
                />
              )
            : view === "registry"
              ? (
                <RemoteRegistryEditorTab device={tunnelDevice || device} />
              )
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
          {view === "patches"
            ? renderPatches()
            : view === "software"
              ? renderSoftware()
              : renderDeviceSummaryTab()}
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
      activeAgentUpdate,
      activeWorkspaceKey,
      activeWorkspaceView,
      agentManagementGroups,
      agentManagementSummary,
      deviceId,
      expandedDeviceNavSections,
      navigate,
      requestSummarySectionScroll,
      roleHealthSummary,
      setActiveWorkspace,
      sidebarAgentHealthRows,
      tunnelDevice,
      workspaceBadges,
    ]
  );

  useRoutePageChrome({
    title: agentUpdatesWorkspaceActive ? "Agent Updates" : displayHostname,
    subtitle: agentUpdatesWorkspaceActive
      ? "Remote trigger agent updates on-demand and see historical agent update history."
      : pageSubtitle,
    Icon: agentUpdatesWorkspaceActive ? SystemUpdateAltRoundedIcon : DeviceSummaryPageIcon,
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
          disabled={!agentUpdateDeviceGuid || updateAgentBusy || agentUpdateIsActive(activeAgentUpdate)}
          onClick={() => {
            setMenuAnchor(null);
            setUpdateConfirmOpen(true);
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
      <QuickJobDialog
        open={quickJobOpen}
        onClose={() => setQuickJobOpen(false)}
        hostnames={quickJobTargets}
        targetRecords={quickJobTargetRecords}
        deviceLabel={activityHostname || displayHostname}
        notifyOperator={notifyOperator}
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
          borderRadius: agentUpdatesWorkspaceActive ? 0 : 3,
          overflow: agentUpdatesWorkspaceActive ? "visible" : "hidden",
        }}
      >
        <Box
          sx={{
            flexGrow: 1,
            minHeight: 0,
            minWidth: 0,
            overflowX: agentUpdatesWorkspaceActive ? "visible" : "hidden",
            display: "flex",
            flexDirection: "column",
            border: agentUpdatesWorkspaceActive ? "none" : `1px solid ${MAGIC_UI.panelBorder}`,
            borderRadius: agentUpdatesWorkspaceActive ? 0 : 3,
            background: agentUpdatesWorkspaceActive
              ? "transparent"
              : "linear-gradient(165deg, rgba(2,6,23,0.9), rgba(8,12,32,0.84)), " +
                "radial-gradient(120% 120% at 100% 0%, rgba(192,132,252,0.08), transparent 60%)",
            boxShadow: agentUpdatesWorkspaceActive ? "none" : MAGIC_UI.glow,
          }}
        >
          <Box
            id="device-summary-workspace-scrollhost"
            sx={{
              flexGrow: 1,
              p: agentUpdatesWorkspaceActive ? 0 : { xs: 1.5, md: 2 },
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

      <Dialog
        open={updateConfirmOpen}
        onClose={() => setUpdateConfirmOpen(false)}
        PaperProps={{ sx: { bgcolor: "#08101f", color: "#e2e8f0", border: `1px solid ${MAGIC_UI.panelBorder}` } }}
      >
        <DialogTitle>Update Agent now?</DialogTitle>
        <DialogContent>
          <Typography sx={{ color: MAGIC_UI.textMuted, maxWidth: 560 }}>
            Update may restart Borealis Agent, UltraVNC, and WireGuard managed services. Native Windows RDP restarts only when unhealthy. Agent identity, enrollment, trust, and Engine association remain preserved.
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setUpdateConfirmOpen(false)} color="inherit">Cancel</Button>
          <Button
            variant="contained"
            disabled={updateAgentBusy}
            onClick={() => {
              setUpdateConfirmOpen(false);
              void requestAgentUpdate();
            }}
            sx={{ background: BOREALIS_PRIMARY_GRADIENT, color: "#06101d", fontWeight: 800 }}
          >
            Update Now
          </Button>
        </DialogActions>
      </Dialog>

    </Box>
  );
}
