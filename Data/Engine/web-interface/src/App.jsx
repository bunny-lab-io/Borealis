////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/App.jsx

//Shared Imports
import React, { useState, useEffect, useCallback, useRef } from "react";
import { NotAuthorizedDialog } from "./Dialogs";
import NavigationSidebar from "./Navigation_Sidebar";
import { PageHeaderActionRail } from "./Page_Header_Actions.jsx";
import GlobalDeviceSearch from "./GlobalDeviceSearch.jsx";

// Styling Imports
import {
      AppBar, Toolbar, Typography, Box, Menu, MenuItem, Button,
      Dialog, DialogActions, DialogContent, DialogTitle, TextField,
      CssBaseline, ThemeProvider, createTheme, Breadcrumbs
    } from "@mui/material";
    import {
      KeyboardArrowDown as KeyboardArrowDownIcon,
      LockReset as LockResetIcon,
      Logout as LogoutIcon,
      NavigateNext as NavigateNextIcon,
      VpnKey as VpnKeyIcon,
    } from "@mui/icons-material";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "./DialogStyles.jsx";

// Workflow Editor Imports
import FlowEditor from "./Flow_Editor/Flow_Editor";

// Borealis Page Imports
import Login from "./Login.jsx";
import SiteList from "./Sites/Site_List";
import SiteAssignment from "./Sites/Site_Assignment.jsx";
import DeviceList from "./Devices/Device_List";
import DeviceSummary from "./Devices/Tabs/Device_Summary.jsx";
import AgentDevices from "./Devices/Agent_Devices.jsx";
import SSHDevices from "./Devices/SSH_Devices.jsx";
import WinRMDevices from "./Devices/WinRM_Devices.jsx";
import DeviceFilterList from "./Devices/Filters/Filter_List.jsx";
import DeviceFilterEditor from "./Devices/Filters/Filter_Editor.jsx";
import AssemblyList from "./Assemblies/Assembly_List";
import AssemblyEditor from "./Assemblies/Assembly_Editor";
import ScheduledJobsList from "./Scheduling/Scheduled_Jobs_List";
import CreateJob from "./Scheduling/Create_Job.jsx";
import CredentialList from "./Access_Management/Credential_List.jsx";
import AegisCipherDialog from "./Access_Management/Aegis_Cipher_Dialog.jsx";
import UserManagement from "./Access_Management/Users.jsx";
import ServerInfo from "./Admin/Server_Info.jsx";
import PageTemplate from "./Admin/Page_Template.jsx";
import LogManagement from "./Admin/Log_Management.jsx";
import DeviceApprovals from "./Devices/Device_Approvals.jsx";
import Notifications from "./Notifications.jsx";

// Networking Imports
import { io } from "socket.io-client";
if (!window.BorealisSocket) {
  window.BorealisSocket = io(window.location.origin, { transports: ["websocket"] });
}
if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 200;
}

///////////////////////////////////////////////////////////////////////////////////////////////////

const darkTheme = createTheme({
  palette: {
    mode: "dark",
    background: { default: "#121212", paper: "#1e1e1e" },
    text: { primary: "#ffffff" }
  },
  components: {
    MuiTooltip: {
      styleOverrides: {
        tooltip: { backgroundColor: "#2a2a2a", color: "#ccc", fontSize: "0.75rem", border: "1px solid #444" },
        arrow: { color: "#2a2a2a" }
      }
    }
  }
});

const APP_AURORA_BACKGROUND =
  "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
  "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), " +
  "linear-gradient(180deg, #040711 0%, #050816 45%, #050816 100%)";

const EMPTY_PAGE_HEADER = {
  title: "",
  subtitle: "",
  Icon: null,
  actions: [],
  controls: [],
};
const EMPTY_AEGIS_STATUS = {
  configured: false,
  locked: false,
  unlock_scope: "engine_global",
  secret_scope: ["credentials", "github_token"],
  updated_at: 0,
};
const OPERATOR_PRESENCE_PAGE_LABELS = Object.freeze({
  login: "Login",
  sites: "Sites",
  devices: "Devices",
  agent_devices: "Agent Devices",
  ssh_devices: "SSH Devices",
  winrm_devices: "WinRM Devices",
  device_details: "Device Details",
  filters: "Filters",
  filter_editor: "Filter Editor",
  jobs: "Scheduled Jobs",
  create_job: "Create Job",
  workflows: "Workflows",
  "workflow-editor": "Flow Editor",
  assemblies: "Assemblies",
  scripts: "Scripts",
  ansible_editor: "Ansible Editor",
  access_credentials: "Credentials",
  access_users: "Users",
  site_assignment: "Site Assignment",
  server_info: "Server Info",
  log_management: "Log Management",
  admin_device_approvals: "Device Approvals",
  page_template: "Page Template",
});

function formatOperatorPresencePage(pageKey, pageTitle) {
  const title = String(pageTitle || "").trim();
  if (title) return title;
  const mapped = OPERATOR_PRESENCE_PAGE_LABELS[String(pageKey || "").trim()];
  if (mapped) return mapped;
  const raw = String(pageKey || "").trim();
  if (!raw) return "";
  return raw.replace(/[_-]+/g, " ").replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function normalizeAegisStatus(payload) {
  return {
    configured: Boolean(payload?.configured),
    locked: Boolean(payload?.locked),
    unlock_scope: payload?.unlock_scope || EMPTY_AEGIS_STATUS.unlock_scope,
    secret_scope: Array.isArray(payload?.secret_scope) ? payload.secret_scope : EMPTY_AEGIS_STATUS.secret_scope,
    updated_at: Number(payload?.updated_at) || 0,
  };
}

async function sha512(text) {
  try {
    if (window.crypto && window.crypto.subtle && window.isSecureContext) {
      const encoder = new TextEncoder();
      const data = encoder.encode(text || "");
      const hashBuffer = await window.crypto.subtle.digest("SHA-512", data);
      const hashArray = Array.from(new Uint8Array(hashBuffer));
      return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
    }
  } catch (_) {
    /* fall through to plaintext fallback */
  }
  return null;
}

function inferInitialPageFromPath(rawPath) {
  try {
    const url = new URL(rawPath || "/", window.location.origin);
    let path = url.pathname || "/";
    if (path.length > 1 && path.endsWith("/")) {
      path = path.slice(0, -1);
    }
    const segments = path.split("/").filter(Boolean);
    const params = url.searchParams;

    if (path === "/login") return "login";
    if (path === "/" || path === "" || path === "/sites") return "sites";
    if (path === "/devices") return "devices";
    if (path === "/devices/agent") return "agent_devices";
    if (path === "/devices/ssh") return "ssh_devices";
    if (path === "/devices/winrm") return "winrm_devices";
    if (path === "/devices/filters") return "filters";
    if (path === "/devices/filters/editor") return "filter_editor";
    if (segments[0] === "device" && segments[1]) return "device_details";
    if (path === "/scheduling") return "jobs";
    if (path === "/scheduling/create_job") return "create_job";
    if (segments[0] === "scheduling" && segments[1] === "job" && segments[2]) return "create_job";
    if (path === "/workflows") return "workflows";
    if (path === "/workflows/editor") return "workflow-editor";
    if (segments[0] === "workflows" && segments[1] === "editor" && segments[2]) return "workflow-editor";
    if (segments[0] === "workflows" && segments[1] === "runs" && segments[2]) return "workflow-editor";
    if (path === "/assemblies") return "assemblies";
    if (path === "/assemblies/editor") {
      return params.get("mode") === "ansible" ? "ansible_editor" : "scripts";
    }
    if (path === "/access_management/users") return "access_users";
    if (path === "/access_management/users/site_assignment") return "site_assignment";
    if (path === "/access_management/github_token" || path === "/access_management/credentials") {
      return "access_credentials";
    }
    if (path === "/admin/server_info") return "server_info";
    if (path === "/admin/log_management" || path === "/admin/log-management") return "log_management";
    if (path === "/admin/page_template") return "page_template";
    if (path === "/admin/device-approvals") return "admin_device_approvals";
    return "sites";
  } catch {
    return "sites";
  }
}

export default function App() {
  const [currentPage, setCurrentPageState] = useState(() =>
    inferInitialPageFromPath(window.location.pathname + window.location.search)
  );
  const [selectedDevice, setSelectedDevice] = useState(null);

  const [userMenuAnchorEl, setUserMenuAnchorEl] = useState(null);
  const [user, setUser] = useState(null);
  const [userRole, setUserRole] = useState(null);
  const [userDisplayName, setUserDisplayName] = useState(null);
  const [userMfaEnabled, setUserMfaEnabled] = useState(false);
  const [editingJob, setEditingJob] = useState(null);
  const [jobsRefreshToken, setJobsRefreshToken] = useState(0);
  const [quickJobDraft, setQuickJobDraft] = useState(null);
  const [assemblyEditorState, setAssemblyEditorState] = useState(null); // { mode: 'script'|'ansible', row, nonce }
  const [filterEditorState, setFilterEditorState] = useState(null);
  const [siteAssignmentState, setSiteAssignmentState] = useState(null);
  const [workflowRouteState, setWorkflowRouteState] = useState({
    assemblyGuid: null,
    runId: null,
  });
  const [filtersRefreshToken, setFiltersRefreshToken] = useState(0);
  const [sessionResolved, setSessionResolved] = useState(false);
  const initialPathRef = useRef(window.location.pathname + window.location.search);
  const pendingPathRef = useRef(null);
  const quickJobSeedRef = useRef(0);
      const [notAuthorizedOpen, setNotAuthorizedOpen] = useState(false);
  const [pageHeader, setPageHeader] = useState(EMPTY_PAGE_HEADER);
  const [aegisStatus, setAegisStatus] = useState(EMPTY_AEGIS_STATUS);
  const [aegisDialog, setAegisDialog] = useState(null);
  const [aegisPromptDismissed, setAegisPromptDismissed] = useState(false);
  const [resetOwnPasswordOpen, setResetOwnPasswordOpen] = useState(false);
  const [resetOwnPasswordBusy, setResetOwnPasswordBusy] = useState(false);
  const [resetOwnPasswordCurrent, setResetOwnPasswordCurrent] = useState("");
  const [resetOwnPasswordNext, setResetOwnPasswordNext] = useState("");
  const [resetOwnPasswordConfirm, setResetOwnPasswordConfirm] = useState("");
  const [resetOwnPasswordError, setResetOwnPasswordError] = useState("");
  const [resetOwnMfaOpen, setResetOwnMfaOpen] = useState(false);
  const [resetOwnMfaBusy, setResetOwnMfaBusy] = useState(false);

  const clearResetOwnPasswordState = useCallback(() => {
    setResetOwnPasswordOpen(false);
    setResetOwnPasswordBusy(false);
    setResetOwnPasswordCurrent("");
    setResetOwnPasswordNext("");
    setResetOwnPasswordConfirm("");
    setResetOwnPasswordError("");
  }, []);

  const clearClientSession = useCallback(() => {
    try { localStorage.removeItem("borealis_session"); } catch {}
    setUser(null);
    setUserRole(null);
    setUserDisplayName(null);
    setUserMfaEnabled(false);
    setAegisStatus(EMPTY_AEGIS_STATUS);
    setAegisDialog(null);
    setAegisPromptDismissed(false);
    clearResetOwnPasswordState();
    setResetOwnMfaOpen(false);
    setResetOwnMfaBusy(false);
    setUserMenuAnchorEl(null);
  }, [clearResetOwnPasswordState]);

  const sendNotification = useCallback(async ({ title, message, icon = "notification", variant = "info" }) => {
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title,
          message,
          icon,
          variant,
        }),
      });
    } catch {
      /* notifications are best-effort */
    }
  }, []);

  const clearOperatorPresence = useCallback(() => {
    try {
      window.BorealisSocket?.emit?.("operator_presence_clear");
    } catch {
      /* operator presence is best-effort */
    }
  }, []);

  const fetchAegisStatus = useCallback(async () => {
    try {
      const resp = await fetch("/api/aegis/status", { credentials: "include" });
      if (!resp.ok) {
        if (resp.status === 401 || resp.status === 403) {
          setAegisStatus(EMPTY_AEGIS_STATUS);
        }
        return EMPTY_AEGIS_STATUS;
      }
      const data = await resp.json();
      const normalized = normalizeAegisStatus(data);
      setAegisStatus(normalized);
      return normalized;
    } catch {
      return EMPTY_AEGIS_STATUS;
    }
  }, []);

  const notifyAegisDeferred = useCallback(async () => {
    await sendNotification({
      title: "Aegis Cipher",
      message:
        "Aegis Cipher not entered. Credential-backed jobs and other protected-secret workflows remain disabled until it is entered.",
      icon: "pendingactions",
      variant: "warning",
    });
  }, [sendNotification]);

  const openAegisDialog = useCallback((mode, source = "credentials") => {
    setAegisDialog({ mode, source });
  }, []);

  const handleAegisDialogClose = useCallback((reason) => {
    const shouldWarn = reason === "cancel" && aegisDialog?.source === "login";
    setAegisDialog(null);
    if (shouldWarn) {
      setAegisPromptDismissed(true);
      notifyAegisDeferred();
    }
  }, [aegisDialog, notifyAegisDeferred]);

  const handleAegisDialogCompleted = useCallback((payload) => {
    setAegisDialog(null);
    setAegisPromptDismissed(false);
    setAegisStatus(normalizeAegisStatus(payload));
  }, []);

  const handlePageMetaChange = useCallback((meta) => {
    if (!meta) {
      setPageHeader(EMPTY_PAGE_HEADER);
      return;
    }
    const titleValue = typeof meta.page_title === "string" ? meta.page_title : meta.title;
    const subtitleValue = typeof meta.page_subtitle === "string" ? meta.page_subtitle : meta.subtitle;
    const iconValue = meta.page_icon || meta.Icon || null;
    const actionsValue = Array.isArray(meta.page_header_actions) ? meta.page_header_actions.filter(Boolean) : [];
    const controlsValue = Array.isArray(meta.page_header_controls) ? meta.page_header_controls.filter(Boolean) : [];
    setPageHeader({
      title: typeof titleValue === "string" ? titleValue : "",
      subtitle: typeof subtitleValue === "string" ? subtitleValue : "",
      Icon: iconValue || null,
      actions: actionsValue,
      controls: controlsValue,
    });
  }, []);

  const consumeScriptInitialData = useCallback(() => {
    setAssemblyEditorState((prev) => {
      if (!prev) return prev;
      return prev.mode === "script" || prev.mode === "scripts" ? null : prev;
    });
  }, []);

  const consumeAnsibleInitialData = useCallback(() => {
    setAssemblyEditorState((prev) => {
      if (!prev) return prev;
      return prev.mode === "ansible" ? null : prev;
    });
  }, []);

  const pageToPath = useCallback(
    (page, options = {}) => {
      switch (page) {
        case "login":
          return "/login";
        case "sites":
          return "/sites";
        case "devices":
          return "/devices";
        case "agent_devices":
          return "/devices/agent";
        case "ssh_devices":
          return "/devices/ssh";
        case "winrm_devices":
          return "/devices/winrm";
        case "filters":
          return "/devices/filters";
        case "filter_editor": {
          const params = new URLSearchParams();
          const filterId =
            options.filterId ||
            filterEditorState?.id ||
            filterEditorState?.filter_id ||
            filterEditorState?.raw?.id ||
            filterEditorState?.raw?.filter_id ||
            null;
          if (filterId) params.set("id", filterId);
          const query = params.toString();
          return query ? `/devices/filters/editor?${query}` : "/devices/filters/editor";
        }
        case "device_details": {
          const params = new URLSearchParams();
          const tabKey = typeof options.tab === "string" ? options.tab.trim() : "";
          if (tabKey) {
            params.set("tab", tabKey);
          }
          const device =
            options.device ||
            selectedDevice ||
            (options.deviceId
              ? { agent_guid: options.deviceId, hostname: options.deviceName || options.deviceId }
              : null);
          const deviceId =
            device?.agent_guid ||
            device?.guid ||
            device?.summary?.agent_guid ||
            device?.hostname ||
            device?.id;
          if (deviceId) {
            const query = params.toString();
            const base = `/device/${encodeURIComponent(deviceId)}`;
            return query ? `${base}?${query}` : base;
          }
          return "/devices";
        }
        case "jobs":
          return "/scheduling";
        case "create_job": {
          const params = new URLSearchParams();
          const tabKey = typeof options.tab === "string" ? options.tab.trim() : "";
          if (tabKey) {
            params.set("tab", tabKey);
          }
          const jobId = options.jobId || options.job?.id || editingJob?.id || null;
          const query = params.toString();
          if (jobId != null && String(jobId).trim() !== "") {
            const base = `/scheduling/job/${encodeURIComponent(String(jobId).trim())}`;
            return query ? `${base}?${query}` : base;
          }
          return query ? `/scheduling/create_job?${query}` : "/scheduling/create_job";
        }
        case "workflows":
          return "/workflows";
        case "workflow-editor": {
          const runId = options.runId || null;
          if (runId != null && String(runId).trim() !== "") {
            return `/workflows/runs/${encodeURIComponent(String(runId).trim())}`;
          }
          const assemblyGuid = options.assemblyGuid || "";
          if (assemblyGuid) {
            return `/workflows/editor/${encodeURIComponent(String(assemblyGuid).trim())}`;
          }
          return "/workflows/editor";
        }
        case "assemblies":
          return "/assemblies";
        case "scripts":
        case "ansible_editor": {
          const mode = page === "ansible_editor" ? "ansible" : "script";
          const params = new URLSearchParams();
          if (mode === "ansible") {
            params.set("mode", "ansible");
          }
          const state = options.assemblyState || assemblyEditorState;
          const assemblyGuid = state?.assemblyGuid || state?.row?.assemblyGuid || "";
          const assemblyPath = state?.path || state?.row?.sourcePath || state?.row?.relPath || state?.row?.path || "";
          if (assemblyGuid) {
            params.set("guid", assemblyGuid);
          }
          if (assemblyPath) {
            params.set("path", assemblyPath);
          }
          const query = params.toString();
          return query ? `/assemblies/editor?${query}` : "/assemblies/editor";
        }
        case "access_credentials":
          return "/access_management/credentials";
      case "access_users":
        return "/access_management/users";
      case "site_assignment": {
        const params = new URLSearchParams();
        const usernames = Array.isArray(options.usernames)
          ? options.usernames
          : Array.isArray(siteAssignmentState?.usernames)
          ? siteAssignmentState.usernames
          : [];
        usernames
          .map((value) => String(value || "").trim())
          .filter(Boolean)
          .forEach((username) => params.append("user", username));
        const query = params.toString();
        return query
          ? `/access_management/users/site_assignment?${query}`
          : "/access_management/users/site_assignment";
      }
      case "server_info":
        return "/admin/server_info";
      case "log_management":
        return "/admin/log_management";
      case "page_template":
        return "/admin/page_template";        
      case "admin_device_approvals":
        return "/admin/device-approvals";
      default:
        return "/sites";
      }
    },
    [assemblyEditorState, editingJob?.id, selectedDevice, siteAssignmentState]
  );

  const interpretPath = useCallback((rawPath) => {
    try {
      const url = new URL(rawPath || "/", window.location.origin);
      let path = url.pathname || "/";
      if (path.length > 1 && path.endsWith("/")) {
        path = path.slice(0, -1);
      }
      const segments = path.split("/").filter(Boolean);
      const params = url.searchParams;

      if (path === "/login") return { page: "login", options: {} };
      if (path === "/" || path === "") return { page: "sites", options: {} };
      if (path === "/devices") return { page: "devices", options: {} };
      if (path === "/devices/agent") return { page: "agent_devices", options: {} };
      if (path === "/devices/ssh") return { page: "ssh_devices", options: {} };
      if (path === "/devices/winrm") return { page: "winrm_devices", options: {} };
      if (path === "/devices/filters") return { page: "filters", options: {} };
      if (path === "/devices/filters/editor") {
        const filterId = params.get("id");
        return { page: "filter_editor", options: filterId ? { filterId } : {} };
      }
      if (segments[0] === "device" && segments[1]) {
        const id = decodeURIComponent(segments[1]);
        const tab = (params.get("tab") || "").trim().toLowerCase();
        return {
          page: "device_details",
          options: {
            device: { agent_guid: id, hostname: id },
            ...(tab ? { tab } : {}),
          }
        };
      }
      if (path === "/sites") return { page: "sites", options: {} };
      if (path === "/scheduling") return { page: "jobs", options: {} };
      if (path === "/scheduling/create_job") {
        const tab = (params.get("tab") || "").trim().toLowerCase();
        const rawJobId = (params.get("id") || "").trim();
        const parsedJobId = Number(rawJobId);
        const jobId = Number.isInteger(parsedJobId) && parsedJobId > 0 ? parsedJobId : null;
        return {
          page: "create_job",
          options: {
            ...(jobId ? { jobId } : {}),
            ...(tab ? { tab } : {}),
          },
        };
      }
      if (segments[0] === "scheduling" && segments[1] === "job" && segments[2]) {
        const decodedId = decodeURIComponent(segments[2]).trim();
        const parsedId = Number(decodedId);
        const jobId = Number.isInteger(parsedId) && parsedId > 0 ? parsedId : null;
        const tab = (params.get("tab") || "").trim().toLowerCase();
        return {
          page: "create_job",
          options: {
            ...(jobId ? { jobId } : {}),
            ...(tab ? { tab } : {}),
          },
        };
      }
      if (path === "/workflows") return { page: "workflows", options: {} };
      if (path === "/workflows/editor") return { page: "workflow-editor", options: {} };
      if (segments[0] === "workflows" && segments[1] === "editor" && segments[2]) {
        return {
          page: "workflow-editor",
          options: {
            assemblyGuid: decodeURIComponent(segments[2]).trim() || null,
          },
        };
      }
      if (segments[0] === "workflows" && segments[1] === "runs" && segments[2]) {
        const parsedRunId = Number(decodeURIComponent(segments[2]).trim());
        return {
          page: "workflow-editor",
          options: Number.isInteger(parsedRunId) && parsedRunId > 0 ? { runId: parsedRunId } : {},
        };
      }
      if (path === "/assemblies") return { page: "assemblies", options: {} };
      if (path === "/assemblies/editor") {
        const mode = params.get("mode");
        const assemblyGuid = (params.get("guid") || "").trim();
        const relPath = params.get("path") || "";
        const normalizedMode = mode === "ansible" ? "ansible" : "script";
        const state = assemblyGuid || relPath
          ? {
              assemblyGuid: assemblyGuid || null,
              path: relPath,
              mode: normalizedMode,
              nonce: Date.now(),
            }
          : null;
        return {
          page: normalizedMode === "ansible" ? "ansible_editor" : "scripts",
          options: state ? { assemblyState: state } : {}
        };
      }
      if (path === "/access_management/users") return { page: "access_users", options: {} };
      if (path === "/access_management/users/site_assignment") {
        const usernames = params
          .getAll("user")
          .map((value) => String(value || "").trim())
          .filter(Boolean);
        return { page: "site_assignment", options: usernames.length ? { usernames } : {} };
      }
      if (path === "/access_management/github_token") return { page: "access_credentials", options: {} };
      if (path === "/access_management/credentials") return { page: "access_credentials", options: {} };
      if (path === "/admin/server_info") return { page: "server_info", options: {} };
      if (path === "/admin/log_management" || path === "/admin/log-management") {
        return { page: "log_management", options: {} };
      }
      if (path === "/admin/page_template") return { page: "page_template", options: {} };
      if (path === "/admin/device-approvals") return { page: "admin_device_approvals", options: {} };
      return { page: "sites", options: {} };
    } catch {
      return { page: "sites", options: {} };
    }
  }, []);

  const updateStateForPage = useCallback(
    (page, options = {}) => {
      setCurrentPageState(page);
      if (page === "device_details") {
        if (options.device) {
          setSelectedDevice(options.device);
        } else if (options.deviceId) {
          const fallbackId = options.deviceId;
          const fallbackName = options.deviceName || options.deviceId;
          setSelectedDevice((prev) => {
            const prevId = prev?.agent_guid || prev?.guid || prev?.hostname || "";
            if (prevId === fallbackId || prevId === fallbackName) {
              return prev;
            }
            return { agent_guid: fallbackId, hostname: fallbackName };
          });
        }
      } else if (!options.preserveDevice) {
        setSelectedDevice(null);
      }

      if ((page === "scripts" || page === "ansible_editor") && options.assemblyState) {
        setAssemblyEditorState(options.assemblyState);
      }
      if (page === "filter_editor") {
        if (options.filter) {
          setFilterEditorState(options.filter);
        } else if (options.filterId && !filterEditorState) {
          setFilterEditorState({ id: options.filterId });
        }
      } else if (!options.preserveFilter) {
        setFilterEditorState(null);
      }

      if (page === "site_assignment") {
        const usernames = Array.isArray(options.usernames)
          ? options.usernames.map((value) => String(value || "").trim()).filter(Boolean)
          : [];
        setSiteAssignmentState({ usernames });
      } else if (!options.preserveSiteAssignment) {
        setSiteAssignmentState(null);
      }

      if (page === "workflow-editor") {
        setWorkflowRouteState({
          assemblyGuid: options.assemblyGuid || null,
          runId: options.runId || null,
        });
      } else if (!options.preserveWorkflowView) {
        setWorkflowRouteState({
          assemblyGuid: null,
          runId: null,
        });
      }

      if (page === "create_job") {
        if (options.job && typeof options.job === "object") {
          setEditingJob(options.job);
        } else if (options.jobId) {
          const parsedId = Number(options.jobId);
          if (Number.isInteger(parsedId) && parsedId > 0) {
            setEditingJob((prev) => {
              if (Number(prev?.id) === parsedId) {
                return prev;
              }
              return { id: parsedId };
            });
          } else {
            setEditingJob(null);
          }
        } else if (!options.preserveJob) {
          setEditingJob(null);
        }
      } else if (!options.preserveJob) {
        setEditingJob(null);
      }
    },
    [
      filterEditorState,
      setAssemblyEditorState,
      setCurrentPageState,
      setEditingJob,
      setFilterEditorState,
      setSiteAssignmentState,
      setSelectedDevice,
      setWorkflowRouteState,
    ]
  );

  const navigateTo = useCallback(
    (page, options = {}) => {
      const { replace = false, allowUnauthenticated = false, suppressPending = false } = options;
      const targetPath = pageToPath(page, options);

      if (!allowUnauthenticated && !user && page !== "login") {
        if (!suppressPending && targetPath) {
          pendingPathRef.current = targetPath;
        }
        updateStateForPage("login", {});
        const loginPath = "/login";
        const method = replace ? "replaceState" : "pushState";
        const current = window.location.pathname + window.location.search;
        if (replace || current !== loginPath) {
          window.history[method]({}, "", loginPath);
        }
        return;
      }

      if (page === "login") {
        updateStateForPage("login", {});
        const loginPath = "/login";
        const method = replace ? "replaceState" : "pushState";
        const current = window.location.pathname + window.location.search;
        if (replace || current !== loginPath) {
          window.history[method]({}, "", loginPath);
        }
        return;
      }

      pendingPathRef.current = null;
      updateStateForPage(page, options);

      if (targetPath) {
        const method = replace ? "replaceState" : "pushState";
        const current = window.location.pathname + window.location.search;
        if (replace || current !== targetPath) {
          window.history[method]({}, "", targetPath);
        }
      }
    },
    [pageToPath, updateStateForPage, user]
  );

  const navigateByPath = useCallback(
    (path, { replace = false, allowUnauthenticated = false } = {}) => {
      const { page, options } = interpretPath(path);
      navigateTo(page, { ...(options || {}), replace, allowUnauthenticated });
    },
    [interpretPath, navigateTo]
  );

  const handleAssemblyEditorSaved = useCallback(() => {
    navigateTo("assemblies");
  }, [navigateTo]);

  const handleOpenUserSiteAssignment = useCallback(
    (users) => {
      navigateTo("site_assignment", {
        usernames: Array.isArray(users)
          ? users.map((user) => String(user?.username || "").trim()).filter(Boolean)
          : [],
      });
    },
    [navigateTo]
  );

  const handleBackFromSiteAssignment = useCallback(() => {
    navigateTo("access_users");
  }, [navigateTo]);

  const navigateToRef = useRef(navigateTo);
  const navigateByPathRef = useRef(navigateByPath);

  useEffect(() => {
    navigateToRef.current = navigateTo;
    navigateByPathRef.current = navigateByPath;
  }, [navigateTo, navigateByPath]);

  const handleQuickJobLaunch = useCallback(
    (hostnames) => {
      const list = Array.isArray(hostnames) ? hostnames : [hostnames];
      const normalized = Array.from(
        new Set(
          list
            .map((host) => (typeof host === "string" ? host.trim() : ""))
            .filter((host) => Boolean(host))
        )
      );
      if (!normalized.length) {
        return;
      }
      quickJobSeedRef.current += 1;
      const primary = normalized[0];
      const extraCount = normalized.length - 1;
      const deviceLabel = extraCount > 0 ? `${primary} +${extraCount} more` : primary;
      setEditingJob(null);
      setQuickJobDraft({
        id: `${Date.now()}_${quickJobSeedRef.current}`,
        hostnames: normalized,
        deviceLabel,
        initialTabKey: "components",
        scheduleType: "immediately",
        placeholderAssemblyLabel: "Choose Assembly",
      });
      navigateTo("create_job");
    },
    [navigateTo]
  );

  const handleConsumeQuickJobDraft = useCallback((draftId) => {
    setQuickJobDraft((prev) => {
      if (!prev) return prev;
      if (draftId && prev.id !== draftId) return prev;
      return null;
    });
  }, []);

  useEffect(() => {
    if (currentPage !== "create_job") return;
    const parsedId = Number(editingJob?.id);
    if (!Number.isInteger(parsedId) || parsedId <= 0) return;

    let canceled = false;
    (async () => {
      try {
        const resp = await fetch(`/api/scheduled_jobs/${parsedId}`);
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) {
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
        const hydrated = data?.job;
        if (!hydrated || typeof hydrated !== "object") return;
        if (canceled) return;
        setEditingJob((prev) => {
          if (Number(prev?.id) !== parsedId) return prev;
          return hydrated;
        });
      } catch (err) {
        console.warn("Failed to hydrate scheduled job from URL", err);
      }
    })();

    return () => {
      canceled = true;
    };
  }, [currentPage, editingJob?.id]);

  // Build breadcrumb items for current view
  const breadcrumbs = React.useMemo(() => {
    const items = [];
      switch (currentPage) {
      case "sites":
        items.push({ label: "Sites", page: "sites" });
        items.push({ label: "Site List", page: "sites" });
        break;
      case "devices":
        items.push({ label: "Inventory", page: "devices" });
        items.push({ label: "Devices", page: "devices" });
        break;
      case "device_details":
        items.push({ label: "Devices", page: "devices" });
        items.push({ label: "Device List", page: "devices" });
        items.push({ label: "Device Details" });
        break;
      case "jobs":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Scheduled Jobs", page: "jobs" });
        break;
      case "create_job":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Scheduled Jobs", page: "jobs" });
        if (editingJob) {
          const trimmedName = typeof editingJob?.name === "string" ? editingJob.name.trim() : "";
          const idValue = editingJob?.id;
          if (trimmedName && idValue != null && String(idValue).trim() !== "") {
            items.push({ label: `${trimmedName} (#${idValue})` });
          } else if (idValue != null && String(idValue).trim() !== "") {
            items.push({ label: `Job #${idValue}` });
          } else {
            items.push({ label: "Edit Job" });
          }
        } else {
          items.push({ label: "Create Job" });
        }
        break;
      case "workflows":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Workflows", page: "workflows" });
        break;
      case "workflow-editor":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Workflows", page: "workflows" });
        items.push({ label: "Flow Editor" });
        break;
      case "scripts":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Scripts", page: "scripts" });
        break;
      case "ansible_editor":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Ansible Playbooks", page: "assemblies" });
        items.push({ label: "Playbook Editor" });
        break;
      case "assemblies":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Assemblies", page: "assemblies" });
        break;
      case "community":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Community Content", page: "community" });
        break;
      case "agent_devices":
        items.push({ label: "Inventory", page: "devices" });
        items.push({ label: "Devices", page: "devices" });
        items.push({ label: "Agent Devices", page: "agent_devices" });
        break;
      case "ssh_devices":
        items.push({ label: "Inventory", page: "devices" });
        items.push({ label: "Devices", page: "devices" });
        items.push({ label: "SSH Devices", page: "ssh_devices" });
        break;
      case "winrm_devices":
        items.push({ label: "Inventory", page: "devices" });
        items.push({ label: "Devices", page: "devices" });
        items.push({ label: "WinRM Devices", page: "winrm_devices" });
        break;
      case "access_credentials":
        items.push({ label: "Access Management", page: "access_credentials" });
        items.push({ label: "Credentials", page: "access_credentials" });
        break;
      case "access_users":
        items.push({ label: "Access Management", page: "access_credentials" });
        items.push({ label: "Users", page: "access_users" });
        break;
      case "site_assignment":
        items.push({ label: "Access Management", page: "access_credentials" });
        items.push({ label: "Users", page: "access_users" });
        items.push({ label: "Site Assignment" });
        break;
      case "server_info":
        items.push({ label: "Admin Settings" });
        items.push({ label: "Server Info", page: "server_info" });
        break;
      case "log_management":
        items.push({ label: "Admin Settings" });
        items.push({ label: "Log Management", page: "log_management" });
        break;
      case "page_template":
        items.push({ label: "Developer Tools" });
        items.push({ label: "Page Template", page: "page_template" });
        break;
      case "admin_device_approvals":
        items.push({ label: "Inventory", page: "devices" });
        items.push({ label: "Device Approvals", page: "admin_device_approvals" });
        break;
      case "filters":
        items.push({ label: "Filters & Groups", page: "filters" });
        items.push({ label: "Filters", page: "filters" });
        break;
      case "filter_editor":
        items.push({ label: "Filters & Groups", page: "filters" });
        items.push({ label: "Filters", page: "filters" });
        items.push({ label: filterEditorState?.name ? `Edit ${filterEditorState.name}` : "Filter Editor" });
        break;
      default:
        // Fallback to a neutral crumb if unknown
        if (currentPage) items.push({ label: String(currentPage) });
    }
    return items;
  }, [currentPage, selectedDevice, editingJob, filterEditorState]);

  useEffect(() => {
    let canceled = false;
    const hydrateSession = async () => {
      const session = localStorage.getItem("borealis_session");
      if (session) {
        try {
          const data = JSON.parse(session);
          if (Date.now() - data.timestamp < 3600 * 1000) {
            if (!canceled) {
              setUser(data.username);
              setUserRole(data.role || null);
              setUserDisplayName(data.display_name || data.username);
            }
          } else {
            localStorage.removeItem("borealis_session");
          }
        } catch {
          localStorage.removeItem("borealis_session");
        }
      }

      try {
        const resp = await fetch('/api/auth/me', { credentials: 'include' });
        if (resp.ok) {
          const me = await resp.json();
          if (!canceled) {
            setUser(me.username);
            setUserRole(me.role || null);
            setUserDisplayName(me.display_name || me.username);
            setUserMfaEnabled(Boolean(me.mfa_enabled));
          }
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
        } else if (resp.status === 401 || resp.status === 403) {
          if (!canceled) {
            clearClientSession();
          }
        }
      } catch {}

      if (!canceled) {
        setSessionResolved(true);
      }
    };

    hydrateSession();
    return () => {
      canceled = true;
    };
  }, [clearClientSession]);

  useEffect(() => {
    if (!sessionResolved || !user) {
      setAegisStatus(EMPTY_AEGIS_STATUS);
      return;
    }
    fetchAegisStatus();
  }, [fetchAegisStatus, sessionResolved, user]);

  useEffect(() => {
    if (!sessionResolved || !user) return;

    let canceled = false;
    const validateSession = async () => {
      try {
        const resp = await fetch('/api/auth/me', { credentials: 'include' });
        if ((resp.status === 401 || resp.status === 403) && !canceled) {
          clearClientSession();
          setAegisStatus(EMPTY_AEGIS_STATUS);
        } else if (resp.ok && !canceled) {
          const me = await resp.json();
          setUser(me.username);
          setUserRole(me.role || null);
          setUserDisplayName(me.display_name || me.username);
          setUserMfaEnabled(Boolean(me.mfa_enabled));
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
          fetchAegisStatus();
        }
      } catch {}
    };

    const intervalId = window.setInterval(validateSession, 30 * 1000);
    const handleVisibility = () => {
      if (document.visibilityState === "visible") {
        validateSession();
      }
    };

    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      canceled = true;
      window.clearInterval(intervalId);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, [clearClientSession, fetchAegisStatus, sessionResolved, user]);

  const syncOperatorPresence = useCallback(() => {
    if (!sessionResolved || !user) return;
    try {
      window.BorealisSocket?.emit?.("operator_presence_sync", {
        current_page: formatOperatorPresencePage(currentPage, pageHeader.title),
        page_key: currentPage,
      });
    } catch {
      /* operator presence is best-effort */
    }
  }, [currentPage, pageHeader.title, sessionResolved, user]);

  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket || typeof socket.on !== "function") return undefined;
    if (!sessionResolved) return undefined;

    if (!user) {
      clearOperatorPresence();
      return undefined;
    }

    syncOperatorPresence();
    const intervalId = window.setInterval(syncOperatorPresence, 30 * 1000);
    socket.on("connect", syncOperatorPresence);
    return () => {
      window.clearInterval(intervalId);
      try {
        socket.off("connect", syncOperatorPresence);
      } catch {
        /* noop */
      }
    };
  }, [clearOperatorPresence, sessionResolved, syncOperatorPresence, user]);

  useEffect(() => {
    if (!sessionResolved) return;

    const navTo = navigateToRef.current;
    const navByPath = navigateByPathRef.current;

    if (user) {
      const stored = initialPathRef.current;
      const currentLocation = window.location.pathname + window.location.search;
      const targetPath =
        stored && stored !== "/login"
          ? stored
          : currentLocation === "/login" || currentLocation === ""
          ? "/sites"
          : currentLocation;
      navByPath(targetPath, { replace: true, allowUnauthenticated: true });
      initialPathRef.current = null;
      pendingPathRef.current = null;
    } else {
      const stored = initialPathRef.current;
      const currentLocation = window.location.pathname + window.location.search;
      const rememberPath =
        stored && !stored.startsWith("/login")
          ? stored
          : !currentLocation.startsWith("/login")
          ? currentLocation
          : null;
      if (rememberPath) {
        pendingPathRef.current = rememberPath;
      }
      navTo("login", { replace: true, allowUnauthenticated: true, suppressPending: true });
    }
  }, [sessionResolved, user]);

  useEffect(() => {
    if (!sessionResolved) return;

    const handlePopState = () => {
      const path = window.location.pathname + window.location.search;
      if (!user) {
        if (!path.startsWith("/login")) {
          pendingPathRef.current = path;
        }
        navigateToRef.current("login", { replace: true, allowUnauthenticated: true, suppressPending: true });
        return;
      }
      navigateByPathRef.current(path, { replace: true, allowUnauthenticated: true });
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [sessionResolved, user]);

  const handleLoginSuccess = ({ username, role }) => {
    setUser(username);
    setUserRole(role || null);
    setUserDisplayName(username);
    setUserMfaEnabled(false);
    localStorage.setItem(
      "borealis_session",
      JSON.stringify({ username, display_name: username, role: role || null, timestamp: Date.now() })
    );
    // Refresh full profile (to get display_name) in background
    (async () => {
      try {
        const resp = await fetch('/api/auth/me', { credentials: 'include' });
        if (resp.ok) {
          const me = await resp.json();
          setUserDisplayName(me.display_name || me.username);
          setUserMfaEnabled(Boolean(me.mfa_enabled));
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
        }
      } catch {}
      try {
        await fetchAegisStatus();
      } catch {}
    })();
    if (pendingPathRef.current) {
      navigateByPath(pendingPathRef.current, { replace: true, allowUnauthenticated: true });
      pendingPathRef.current = null;
    } else {
      navigateTo('sites', { replace: true, allowUnauthenticated: true });
    }
  };

  useEffect(() => {
    const isAdminUser = String(userRole || "").toLowerCase() === "admin";
    if (!sessionResolved || !user || !isAdminUser) return;
    if (!aegisStatus.configured || !aegisStatus.locked || aegisPromptDismissed) return;
    setAegisDialog((prev) => prev || { mode: "unlock", source: "login" });
  }, [aegisPromptDismissed, aegisStatus, sessionResolved, user, userRole]);

  const handleUserMenuOpen = (event) => setUserMenuAnchorEl(event.currentTarget);
  const handleUserMenuClose = () => setUserMenuAnchorEl(null);
  const handleOpenResetOwnPasswordDialog = () => {
    handleUserMenuClose();
    if (resetOwnPasswordBusy) return;
    setResetOwnPasswordError("");
    setResetOwnPasswordCurrent("");
    setResetOwnPasswordNext("");
    setResetOwnPasswordConfirm("");
    setResetOwnPasswordOpen(true);
  };
  const handleCloseResetOwnPasswordDialog = () => {
    if (resetOwnPasswordBusy) return;
    clearResetOwnPasswordState();
  };
  const handleResetOwnPassword = async () => {
    if (resetOwnPasswordBusy) return;

    const currentPassword = String(resetOwnPasswordCurrent || "");
    const nextPassword = String(resetOwnPasswordNext || "");
    const confirmPassword = String(resetOwnPasswordConfirm || "");

    if (!currentPassword || !nextPassword) {
      setResetOwnPasswordError("Enter your current password and a new password.");
      return;
    }
    if (nextPassword !== confirmPassword) {
      setResetOwnPasswordError("The new password and confirmation do not match.");
      return;
    }
    if (currentPassword === nextPassword) {
      setResetOwnPasswordError("Choose a new password that differs from your current password.");
      return;
    }

    setResetOwnPasswordBusy(true);
    setResetOwnPasswordError("");
    try {
      const currentPasswordHash = await sha512(currentPassword);
      const nextPasswordHash = await sha512(nextPassword);
      const payload =
        currentPasswordHash && nextPasswordHash
          ? {
              current_password_sha512: currentPasswordHash,
              new_password_sha512: nextPasswordHash,
            }
          : {
              current_password: currentPassword,
              new_password: nextPassword,
            };

      const resp = await fetch("/api/auth/password/reset", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(payload),
      });

      if (resp.status === 401 || resp.status === 403) {
        const unauthorizedPayload = await resp.json().catch(() => ({}));
        if ((unauthorizedPayload?.error || "") === "invalid current password") {
          setResetOwnPasswordError("Your current password is incorrect.");
          return;
        }
        clearClientSession();
        navigateTo("login", { replace: true, allowUnauthenticated: true, suppressPending: true });
        return;
      }

      const responsePayload = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const errorMessageMap = {
          "invalid current password": "Your current password is incorrect.",
          "new password must differ from the current password": "Choose a new password that differs from your current password.",
          "invalid current password hash": "Enter your current password.",
          "invalid new password hash": "Enter a valid new password.",
        };
        throw new Error(errorMessageMap[responsePayload?.error] || responsePayload?.error || "Failed to reset password.");
      }

      clearResetOwnPasswordState();
      await sendNotification({
        title: "Reset Password",
        message: "Your Borealis password was updated.",
        icon: "user",
        variant: "info",
      });
    } catch (error) {
      setResetOwnPasswordError(
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not update your password."
      );
    } finally {
      setResetOwnPasswordBusy(false);
    }
  };
  const handleOpenResetOwnMfaDialog = () => {
    handleUserMenuClose();
    if (!userMfaEnabled || resetOwnMfaBusy) return;
    setResetOwnMfaOpen(true);
  };
  const handleCloseResetOwnMfaDialog = () => {
    if (resetOwnMfaBusy) return;
    setResetOwnMfaOpen(false);
  };
  const handleResetOwnMfa = async () => {
    if (resetOwnMfaBusy) return;
    setResetOwnMfaBusy(true);
    try {
      const resp = await fetch("/api/auth/mfa/reset", {
        method: "POST",
        credentials: "include",
      });

      if (resp.status === 401 || resp.status === 403) {
        clearClientSession();
        navigateTo("login", { replace: true, allowUnauthenticated: true, suppressPending: true });
        return;
      }

      const payload = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(payload.error || "Failed to reset MFA.");
      }

      setUserMfaEnabled(Boolean(payload.mfa_enabled));
      setResetOwnMfaOpen(false);
      await sendNotification({
        title: "Reset MFA",
        message: payload.setup_required_on_next_login
          ? "Your MFA setup was reset. The next time you sign in, Borealis will prompt you to set up MFA again."
          : "No active MFA setup was found for your account.",
        icon: "user",
        variant: "info",
      });
    } catch (error) {
      const message =
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not reset MFA for this account.";
      await sendNotification({
        title: "Reset MFA",
        message,
        icon: "warning",
        variant: "error",
      });
    } finally {
      setResetOwnMfaBusy(false);
    }
  };
  const handleLogout = async () => {
    clearOperatorPresence();
    try {
      await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' });
    } catch {}
    clearClientSession();
    navigateTo('login', { replace: true, allowUnauthenticated: true, suppressPending: true });
  };

  const openScriptFromList = useCallback(
    (row) => {
      if (!row) return;
      const normalizedRow = {
        ...row,
        domain: (row?.domain || "user").toLowerCase(),
      };
      const rowKind = String(normalizedRow.assemblyKind || normalizedRow.assembly_type || "").toLowerCase();
      const mode = rowKind === "ansible" ? "ansible" : "script";
      const nonce = Date.now();
      const state = {
        mode,
        assemblyGuid: normalizedRow.assemblyGuid || null,
        path: normalizedRow.sourcePath || normalizedRow.relPath || normalizedRow.path || "",
        row: normalizedRow,
        nonce,
      };
      setAssemblyEditorState(state);
      navigateTo(mode === "ansible" ? "ansible_editor" : "scripts", { assemblyState: state });
    },
    [navigateTo, setAssemblyEditorState]
  );

  const openWorkflowFromList = useCallback(
    (row) => {
      navigateTo("workflow-editor", {
        assemblyGuid: row?.assemblyGuid || null,
        runId: null,
      });
    },
    [navigateTo]
  );

  const isAdmin = (String(userRole || '').toLowerCase() === 'admin');

  useEffect(() => {
    const requiresAdmin = currentPage === 'server_info'
      || currentPage === 'access_credentials'
      || currentPage === 'access_users'
      || currentPage === 'site_assignment'
      || currentPage === 'log_management'
      || currentPage === 'ssh_devices'
      || currentPage === 'winrm_devices'
      || currentPage === 'agent_devices'
      || currentPage === 'page_template';

    if (!sessionResolved) {
      return;
    }

    if (!requiresAdmin) {
      setNotAuthorizedOpen(false);
      return;
    }

    if (!user) {
      return;
    }

    if (isAdmin) {
      setNotAuthorizedOpen(false);
      return;
    }

    if (!isAdmin) {
      setNotAuthorizedOpen(true);
      navigateTo('sites', { replace: true, suppressPending: true });
    }
  }, [currentPage, isAdmin, navigateTo, sessionResolved, user]);

  const handleViewDevicesForFilter = useCallback(
    (filter) => {
      try {
        localStorage.setItem(
          "device_list_saved_filter_preview",
          JSON.stringify({ filter_id: filter?.id, name: filter?.name || "Saved Filter" })
        );
      } catch {}
      navigateTo("devices");
    },
    [navigateTo]
  );

  const handleOpenJobFromFilter = useCallback(
    (job) => {
      setEditingJob(job || null);
      navigateTo("create_job", { jobId: job?.id });
    },
    [navigateTo]
  );

  const handleCreateFilter = useCallback(() => {
    setFilterEditorState(null);
    navigateTo("filter_editor");
  }, [navigateTo]);

  const handleEditFilter = useCallback(
    (filter) => {
      setFilterEditorState(filter);
      navigateTo("filter_editor", { filterId: filter?.id });
    },
    [navigateTo]
  );

  const handleCancelFilterEdit = useCallback(() => {
    setFilterEditorState(null);
    navigateTo("filters");
  }, [navigateTo]);

  const handleFilterSaved = useCallback(() => {
    setFilterEditorState(null);
    setFiltersRefreshToken(Date.now());
    navigateTo("filters");
  }, [navigateTo]);

  const handleCreateScheduledJob = useCallback(() => {
    setEditingJob(null);
    setQuickJobDraft(null);
    navigateTo("create_job");
  }, [navigateTo]);

  const handleEditScheduledJob = useCallback(
    (job) => {
      const jobId = Number(job?.id);
      setQuickJobDraft(null);
      if (!Number.isInteger(jobId) || jobId <= 0) {
        setEditingJob(null);
        return;
      }
      setEditingJob({ id: jobId });
      navigateTo("create_job", { jobId });
    },
    [navigateTo]
  );

  const renderMainContent = () => {
    switch (currentPage) {
      case "sites":
        return (
          <SiteList
            onPageMetaChange={handlePageMetaChange}
            onOpenDevicesForSite={(siteName) => {
              try {
                localStorage.setItem('device_list_initial_site_filter', String(siteName || ''));
              } catch {}
              navigateTo("devices");
            }}
          />
        );
      case "devices":
        return (
          <DeviceList
            onPageMetaChange={handlePageMetaChange}
            onSelectDevice={(d) => {
              navigateTo("device_details", { device: d });
            }}
            onQuickJobLaunch={handleQuickJobLaunch}
          />
        );
      case "agent_devices":
        return (
          <AgentDevices
            onPageMetaChange={handlePageMetaChange}
            onSelectDevice={(d) => {
              navigateTo("device_details", { device: d });
            }}
            onQuickJobLaunch={handleQuickJobLaunch}
          />
        );
      case "ssh_devices":
        return <SSHDevices onQuickJobLaunch={handleQuickJobLaunch} onPageMetaChange={handlePageMetaChange} />;
      case "winrm_devices":
        return <WinRMDevices onQuickJobLaunch={handleQuickJobLaunch} onPageMetaChange={handlePageMetaChange} />;

      case "filters":
        return (
          <DeviceFilterList
            onPageMetaChange={handlePageMetaChange}
            refreshToken={filtersRefreshToken}
            onViewDevices={handleViewDevicesForFilter}
            onOpenJob={handleOpenJobFromFilter}
            onCreateFilter={handleCreateFilter}
            onEditFilter={handleEditFilter}
          />
        );

      case "filter_editor":
        return (
          <DeviceFilterEditor
            onPageMetaChange={handlePageMetaChange}
            initialFilter={filterEditorState}
            onCancel={handleCancelFilterEdit}
            onSaved={handleFilterSaved}
          />
        );

      case "device_details":
        return (
          <DeviceSummary
            onPageMetaChange={handlePageMetaChange}
            device={selectedDevice}
            onQuickJobLaunch={handleQuickJobLaunch}
            onNotify={sendNotification}
          />
        );

      case "jobs":
        return (
          <ScheduledJobsList
            onPageMetaChange={handlePageMetaChange}
            onCreateJob={handleCreateScheduledJob}
            onEditJob={handleEditScheduledJob}
            refreshToken={jobsRefreshToken}
          />
        );

      case "create_job":
        return (
          <CreateJob
            onPageMetaChange={handlePageMetaChange}
            initialJob={editingJob}
            quickJobDraft={quickJobDraft}
            onConsumeQuickJobDraft={handleConsumeQuickJobDraft}
            aegisStatus={aegisStatus}
            onCancel={() => {
              navigateTo("jobs");
              setEditingJob(null);
              setQuickJobDraft(null);
            }}
            onCreated={() => {
              navigateTo("jobs");
              setEditingJob(null);
              setJobsRefreshToken(Date.now());
              setQuickJobDraft(null);
            }}
          />
        );

      case "workflows":
        return (
          <AssemblyList
            onPageMetaChange={handlePageMetaChange}
            onOpenWorkflow={openWorkflowFromList}
            onOpenScript={openScriptFromList}
            userRole={userRole || 'User'}
          />
        );

      case "assemblies":
        return (
          <AssemblyList
            onPageMetaChange={handlePageMetaChange}
            onOpenWorkflow={openWorkflowFromList}
            onOpenScript={openScriptFromList}
            userRole={userRole || 'User'}
          />
        );

      case "scripts":
        return (
          <AssemblyEditor
            mode="script"
            onPageMetaChange={handlePageMetaChange}
            initialAssembly={
              assemblyEditorState &&
              (assemblyEditorState.mode === "script" || assemblyEditorState.mode === "scripts")
                ? assemblyEditorState
                : null
            }
            onConsumeInitialData={consumeScriptInitialData}
            onSaved={handleAssemblyEditorSaved}
            userRole={userRole || 'User'}
          />
        );

      case "ansible_editor":
        return (
          <AssemblyEditor
            mode="ansible"
            onPageMetaChange={handlePageMetaChange}
            initialAssembly={assemblyEditorState && assemblyEditorState.mode === 'ansible' ? assemblyEditorState : null}
            onConsumeInitialData={consumeAnsibleInitialData}
            onSaved={handleAssemblyEditorSaved}
            userRole={userRole || 'User'}
          />
        );

      case "access_credentials":
        return (
          <CredentialList
            isAdmin={isAdmin}
            onPageMetaChange={handlePageMetaChange}
            aegisStatus={aegisStatus}
            onAegisAction={openAegisDialog}
          />
        );

      case "access_users":
        return (
          <UserManagement
            isAdmin={isAdmin}
            onPageMetaChange={handlePageMetaChange}
            onOpenSiteAssignment={handleOpenUserSiteAssignment}
          />
        );

      case "site_assignment":
        return (
          <SiteAssignment
            onPageMetaChange={handlePageMetaChange}
            selectedUsernames={siteAssignmentState?.usernames || []}
            onBack={handleBackFromSiteAssignment}
          />
        );

      case "server_info":
        return <ServerInfo isAdmin={isAdmin} onPageMetaChange={handlePageMetaChange} />;
      case "log_management":
        return <LogManagement isAdmin={isAdmin} onPageMetaChange={handlePageMetaChange} />;

      case "page_template":
        return <PageTemplate isAdmin={isAdmin} onPageMetaChange={handlePageMetaChange} />;

      case "admin_device_approvals":
        return <DeviceApprovals onPageMetaChange={handlePageMetaChange} />;

      case "workflow-editor":
        return (
          <FlowEditor
            routeState={workflowRouteState}
            navigateTo={navigateTo}
            onPageMetaChange={handlePageMetaChange}
          />
        );

      default:
        return (
          <Box sx={{ p: 2 }}>
            <Typography>Select a section from navigation.</Typography>
          </Box>
        );
    }
  };
  if (!user) {
    return (
      <ThemeProvider theme={darkTheme}>
        <CssBaseline />
        <Login onLogin={handleLoginSuccess} />
      </ThemeProvider>
    );
  }

  const HeaderIcon = pageHeader.Icon;
  const hasPageHeader = Boolean(
    pageHeader.title ||
    pageHeader.subtitle ||
    pageHeader.Icon ||
    pageHeader.actions?.length ||
    pageHeader.controls?.length
  );

  return (
    <ThemeProvider theme={darkTheme}>
      <CssBaseline />
      <Notifications socket={window.BorealisSocket} currentUser={user} />
      <AegisCipherDialog
        open={Boolean(aegisDialog)}
        mode={aegisDialog?.mode || "unlock"}
        source={aegisDialog?.source || "credentials"}
        onClose={handleAegisDialogClose}
        onCompleted={handleAegisDialogCompleted}
      />
      <Box
        sx={{
          width: "100vw",
          height: "100vh",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          background: APP_AURORA_BACKGROUND,
          backgroundRepeat: "no-repeat",
          backgroundSize: "cover",
          backgroundColor: "#040711",
        }}
      >
        {/* Aurora Gradient Header (darker near the logo, brighter to the right) */}
          <AppBar
            position="static"
            sx={{
              background:
                "linear-gradient(90deg, rgba(10,14,22,0.65) 0%, rgba(10,14,22,0.20) 20%), " + // left-side dark overlay for logo contrast
                "linear-gradient(90deg, rgba(64,164,255,0.5) 0%, rgba(132, 252, 230, 0.39) 100%)",
              boxShadow: "0 0 20px rgba(125,183,255,0.12)",
              backdropFilter: "blur(10px) saturate(140%)",
              borderBottom: "1px solid rgba(125,183,255,0.20)"
            }}
          >
            <Toolbar sx={{ minHeight: 40, alignItems: "center", gap: 1 }}>
              {/* Logo only (removed the standalone 'Borealis' text) */}
              <Box
                component="img"
                src="/Borealis_Logo_Full.png"
                alt="Borealis Logo"
                sx={{ height: 50, ml: -1.8, mr: 1.5 }}
              />

              {user ? (
                <Box
                  sx={{
                    ml: 2,
                    flex: { xs: "1 1 220px", md: "0 1 460px" },
                    minWidth: 0,
                    maxWidth: 520,
                  }}
                >
                  <GlobalDeviceSearch
                    onSelectDevice={(device) => {
                      navigateTo("device_details", { device });
                    }}
                  />
                </Box>
              ) : null}

              {/* Breadcrumbs — now inline and vertically centered */}
              <Box sx={{ ml: 2, display: "flex", alignItems: "center" }}>
                <Breadcrumbs
                  separator={<NavigateNextIcon fontSize="inherit" sx={{ color: "#6b6b6b" }} />}
                  aria-label="breadcrumb"
                  sx={{
                    color: "#9aa0a6",
                    fontSize: "0.825rem",
                    "& .MuiBreadcrumbs-separator": { mx: 0.6 }
                  }}
                >
                  {breadcrumbs.map((c, idx) =>
                    c.page ? (
                      <Button
                        key={idx}
                        onClick={() => navigateTo(c.page)}
                        size="small"
                        sx={{ color: "#cde1ff", textTransform: "none", minWidth: 0, p: 0, fontSize: "0.825rem" }}
                      >
                        {c.label}
                      </Button>
                    ) : (
                      <Typography key={idx} component="span" sx={{ color: "#f5f5f5", fontSize: "0.825rem" }}>
                        {c.label}
                      </Typography>
                    )
                  )}
                </Breadcrumbs>
              </Box>

              {/* Push user menu to the right */}
              <Box sx={{ flexGrow: 1 }} />

              {/* User Menu */}
              <Button
                color="inherit"
                onClick={handleUserMenuOpen}
                endIcon={<KeyboardArrowDownIcon />}
                sx={{ height: 36 }}
              >
                {userDisplayName || user || "User"}
              </Button>
              <Menu anchorEl={userMenuAnchorEl} open={Boolean(userMenuAnchorEl)} onClose={handleUserMenuClose}>
                <MenuItem disabled={resetOwnPasswordBusy} onClick={handleOpenResetOwnPasswordDialog}>
                  <VpnKeyIcon
                    sx={{
                      fontSize: 18,
                      color: !resetOwnPasswordBusy ? "#8ecbff" : "rgba(148,163,184,0.62)",
                      mr: 1,
                    }}
                  />
                  Reset Password
                </MenuItem>
                <MenuItem disabled={!userMfaEnabled || resetOwnMfaBusy} onClick={handleOpenResetOwnMfaDialog}>
                  <LockResetIcon
                    sx={{
                      fontSize: 18,
                      color: userMfaEnabled && !resetOwnMfaBusy ? "#8ecbff" : "rgba(148,163,184,0.62)",
                      mr: 1,
                    }}
                  />
                  Reset MFA
                </MenuItem>
                <MenuItem onClick={() => { handleUserMenuClose(); handleLogout(); }}>
                  <LogoutIcon sx={{ fontSize: 18, color: "#ff6b6b", mr: 1 }} /> Logout
                </MenuItem>
              </Menu>
            </Toolbar>
          </AppBar>

        <Box
          sx={{
            display: "flex",
            flexGrow: 1,
            overflow: "auto",
            minHeight: 0,
            background: APP_AURORA_BACKGROUND,
            backgroundRepeat: "no-repeat",
            backgroundSize: "cover",
            backgroundColor: "#040711",
          }}
        >
          <NavigationSidebar currentPage={currentPage} onNavigate={navigateTo} isAdmin={isAdmin} />
          <Box
            sx={{
              flexGrow: 1,
              display: "flex",
              flexDirection: "column",
              overflow: "auto",
              minHeight: 0,
              minWidth: 0,
            }}
          >
            {hasPageHeader ? (
              <Box sx={{ px: 3, pt: 3, pb: 1.5, flexShrink: 0 }}>
                <Box
                  sx={{
                    display: "flex",
                    flexDirection: { xs: "column", xl: "row" },
                    alignItems: { xs: "stretch", xl: "flex-start" },
                    justifyContent: "space-between",
                    gap: 2,
                  }}
                >
                  <Box sx={{ minWidth: 0, flex: "1 1 auto" }}>
                    <Box sx={{ display: "flex", alignItems: "center", gap: 1.25 }}>
                      {HeaderIcon ? <HeaderIcon sx={{ fontSize: 22, color: "#7dd3fc" }} /> : null}
                      <Typography variant="h6" sx={{ color: "#e2e8f0", fontWeight: 700, letterSpacing: 0.5 }}>
                        {pageHeader.title}
                      </Typography>
                    </Box>
                    {pageHeader.subtitle ? (
                      <Typography variant="body2" sx={{ color: "#aaa", mt: 0.5 }}>
                        {pageHeader.subtitle}
                      </Typography>
                    ) : null}
                  </Box>
                  <PageHeaderActionRail
                    actions={pageHeader.actions}
                    controls={pageHeader.controls}
                  />
                </Box>
              </Box>
            ) : null}
            <Box
              sx={{
                flexGrow: 1,
                display: "flex",
                flexDirection: "column",
                overflow: "auto",
                minHeight: 0,
                "& > *": {
                  alignSelf: "stretch",
                  minHeight: 0,
                },
              }}
            >
              {renderMainContent()}
            </Box>
          </Box>
        </Box>
      </Box>
      <Dialog
        open={resetOwnPasswordOpen}
        onClose={handleCloseResetOwnPasswordDialog}
        fullWidth
        maxWidth="xs"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Reset Password"
            subtitle="Verify your current password, then set a new one for this Borealis account."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Current Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordCurrent}
            onChange={(event) => setResetOwnPasswordCurrent(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
          />
          <TextField
            fullWidth
            label="New Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordNext}
            onChange={(event) => setResetOwnPasswordNext(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
          />
          <TextField
            fullWidth
            label="Confirm New Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordConfirm}
            onChange={(event) => setResetOwnPasswordConfirm(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
          />
          {resetOwnPasswordError ? (
            <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 1.5, color: "#ffb4b4" }}>
              {resetOwnPasswordError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleCloseResetOwnPasswordDialog} sx={DIALOG_BUTTON_SX} disabled={resetOwnPasswordBusy}>
            Cancel
          </Button>
          <Button onClick={handleResetOwnPassword} sx={DIALOG_PRIMARY_BUTTON_SX} disabled={resetOwnPasswordBusy}>
            {resetOwnPasswordBusy ? "Saving..." : "Save Password"}
          </Button>
        </DialogActions>
      </Dialog>
      <Dialog
        open={resetOwnMfaOpen}
        onClose={handleCloseResetOwnMfaDialog}
        fullWidth
        maxWidth="xs"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Reset MFA"
            subtitle="Clear your current Borealis MFA secret for this account."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Borealis will keep MFA enabled for your account, but the next time you sign in you will be prompted
            to complete MFA setup again.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleCloseResetOwnMfaDialog} sx={DIALOG_BUTTON_SX} disabled={resetOwnMfaBusy}>
            Cancel
          </Button>
          <Button onClick={handleResetOwnMfa} sx={DIALOG_DANGER_BUTTON_SX} disabled={resetOwnMfaBusy}>
            {resetOwnMfaBusy ? "Resetting..." : "Reset MFA"}
          </Button>
        </DialogActions>
      </Dialog>
      <NotAuthorizedDialog open={notAuthorizedOpen} onClose={() => setNotAuthorizedOpen(false)} />
    </ThemeProvider>
  );
}
