////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/web-interface/src/App.jsx

//Shared Imports
import React, { useState, useEffect, useCallback, useRef } from "react";
import { ReactFlowProvider } from "reactflow";
import "reactflow/dist/style.css";
import {
  CloseAllDialog, RenameTabDialog, TabContextMenu, NotAuthorizedDialog
} from "./Dialogs";
import NavigationSidebar from "./Navigation_Sidebar";

// Styling Imports
import {
      AppBar, Toolbar, Typography, Box, Menu, MenuItem, Button,
      CssBaseline, ThemeProvider, createTheme, Breadcrumbs
    } from "@mui/material";
    import {
      KeyboardArrowDown as KeyboardArrowDownIcon,
      Logout as LogoutIcon,
      NavigateNext as NavigateNextIcon
    } from "@mui/icons-material";
    import ClickAwayListener from "@mui/material/ClickAwayListener";
    import SearchIcon from "@mui/icons-material/Search";
    import ArrowDropDownIcon from "@mui/icons-material/ArrowDropDown";
    import ArrowDropUpIcon from "@mui/icons-material/ArrowDropUp";

// Workflow Editor Imports
import FlowTabs from "./Flow_Editor/Flow_Tabs";
import FlowEditor from "./Flow_Editor/Flow_Editor";
import NodeSidebar from "./Flow_Editor/Node_Sidebar";
import StatusBar from "./Flow_Editor/Status_Bar.jsx";

// Borealis Page Imports
import Login from "./Login.jsx";
import SiteList from "./Sites/Site_List";
import DeviceList from "./Devices/Device_List";
import DeviceDetails from "./Devices/Device_Details";
import AgentDevices from "./Devices/Agent_Devices.jsx";
import SSHDevices from "./Devices/SSH_Devices.jsx";
import WinRMDevices from "./Devices/WinRM_Devices.jsx";
import AssemblyList from "./Assemblies/Assembly_List";
import AssemblyEditor from "./Assemblies/Assembly_Editor";
import ScheduledJobsList from "./Scheduling/Scheduled_Jobs_List";
import CreateJob from "./Scheduling/Create_Job.jsx";
import CredentialList from "./Access_Management/Credential_List.jsx";
import UserManagement from "./Access_Management/Users.jsx";
import GithubAPIToken from "./Access_Management/Github_API_Token.jsx";
import ServerInfo from "./Admin/Server_Info.jsx";
import PageTemplate from "./Admin/Page_Template.jsx";
import LogManagement from "./Admin/Log_Management.jsx";
import EnrollmentCodes from "./Devices/Enrollment_Codes.jsx";
import DeviceApprovals from "./Devices/Device_Approvals.jsx";

// Networking Imports
import { io } from "socket.io-client";
if (!window.BorealisSocket) {
  window.BorealisSocket = io(window.location.origin, { transports: ["websocket"] });
}
if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 200;
}

///////////////////////////////////////////////////////////////////////////////////////////////////

// Load node modules dynamically
const modules = import.meta.glob('./Nodes/**/*.jsx', { eager: true });
const nodeTypes = {};
const categorizedNodes = {};
Object.entries(modules).forEach(([path, mod]) => {
  const comp = mod.default;
  if (!comp) return;
  const { type, component } = comp;
  if (!type || !component) return;
  const parts = path.replace('./Nodes/', '').split('/');
  const category = parts[0];
  if (!categorizedNodes[category]) categorizedNodes[category] = [];
  categorizedNodes[category].push(comp);
  nodeTypes[type] = component;
});

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

const LOCAL_STORAGE_KEY = "borealis_persistent_state";

  export default function App() {
  const [tabs, setTabs] = useState([{ id: "flow_1", tab_name: "Flow 1", nodes: [], edges: [] }]);
  const [activeTabId, setActiveTabId] = useState("flow_1");
  const [currentPage, setCurrentPageState] = useState("devices");
  const [selectedDevice, setSelectedDevice] = useState(null);

  const [userMenuAnchorEl, setUserMenuAnchorEl] = useState(null);
  const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameTabId, setRenameTabId] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
  const [tabMenuTabId, setTabMenuTabId] = useState(null);
  const fileInputRef = useRef(null);
  const [user, setUser] = useState(null);
  const [userRole, setUserRole] = useState(null);
  const [userDisplayName, setUserDisplayName] = useState(null);
  const [editingJob, setEditingJob] = useState(null);
  const [jobsRefreshToken, setJobsRefreshToken] = useState(0);
  const [quickJobDraft, setQuickJobDraft] = useState(null);
  const [assemblyEditorState, setAssemblyEditorState] = useState(null); // { mode: 'script'|'ansible', row, nonce }
  const [sessionResolved, setSessionResolved] = useState(false);
  const initialPathRef = useRef(window.location.pathname + window.location.search);
  const pendingPathRef = useRef(null);
  const quickJobSeedRef = useRef(0);
      const [notAuthorizedOpen, setNotAuthorizedOpen] = useState(false);

      // Top-bar search state
      const SEARCH_CATEGORIES = [
        { key: "hostname", label: "Hostname", scope: "device", placeholder: "Search Hostname" },
        { key: "internal_ip", label: "Internal IP", scope: "device", placeholder: "Search Internal IP" },
        { key: "external_ip", label: "External IP", scope: "device", placeholder: "Search External IP" },
        { key: "description", label: "Description", scope: "device", placeholder: "Search Description" },
        { key: "last_user", label: "Last User", scope: "device", placeholder: "Search Last User" },
        { key: "serial_number", label: "Serial Number (Soon)", scope: "device", placeholder: "Search Serial Number" },
        { key: "site_name", label: "Site Name", scope: "site", placeholder: "Search Site Name" },
        { key: "site_description", label: "Site Description", scope: "site", placeholder: "Search Site Description" },
      ];
      const [searchCategory, setSearchCategory] = useState("hostname");
      const [searchOpen, setSearchOpen] = useState(false);
      const [searchQuery, setSearchQuery] = useState("");
      const [searchMenuEl, setSearchMenuEl] = useState(null);
      const [suggestions, setSuggestions] = useState({ devices: [], sites: [], q: "", field: "" });
      const searchAnchorRef = useRef(null);
      const searchDebounceRef = useRef(null);

      // Gentle highlight helper for matched substrings
      const highlightText = useCallback((text, query) => {
        const t = String(text ?? "");
        const q = String(query ?? "").trim();
        if (!q) return t;
        try {
          const esc = q.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
          const re = new RegExp(`(${esc})`, "ig");
          const parts = t.split(re);
          return parts.map((part, i) =>
            part.toLowerCase() === q.toLowerCase()
              ? (
                  <span key={i} style={{ backgroundColor: '#243a52', color: '#a7d0ff', borderRadius: 2, padding: '0 1px' }}>{part}</span>
                )
              : <span key={i}>{part}</span>
          );
        } catch {
          return t;
        }
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
        case "device_details": {
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
            return `/device/${encodeURIComponent(deviceId)}`;
          }
          return "/devices";
        }
        case "jobs":
          return "/scheduling";
        case "create_job":
          return "/scheduling/create_job";
        case "workflows":
          return "/workflows";
        case "workflow-editor":
          return "/workflows/editor";
        case "assemblies":
          return "/assemblies";
        case "scripts":
        case "ansible_editor": {
          const mode = page === "ansible_editor" ? "ansible" : "scripts";
          const params = new URLSearchParams();
          if (mode === "ansible") {
            params.set("mode", "ansible");
          }
          const state = options.assemblyState || assemblyEditorState;
          if (state?.path) {
            params.set("path", state.path);
          }
          const query = params.toString();
          return query ? `/assemblies/editor?${query}` : "/assemblies/editor";
        }
        case "access_credentials":
          return "/access_management/credentials";
      case "access_github_token":
        return "/access_management/github_token";
      case "access_users":
        return "/access_management/users";
      case "server_info":
        return "/admin/server_info";
      case "page_template":
        return "/admin/page_template";        
      case "admin_enrollment_codes":
        return "/admin/enrollment-codes";
      case "admin_device_approvals":
        return "/admin/device-approvals";
      default:
        return "/devices";
      }
    },
    [assemblyEditorState, selectedDevice]
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
      if (path === "/" || path === "") return { page: "devices", options: {} };
      if (path === "/devices") return { page: "devices", options: {} };
      if (path === "/devices/agent") return { page: "agent_devices", options: {} };
      if (path === "/devices/ssh") return { page: "ssh_devices", options: {} };
      if (path === "/devices/winrm") return { page: "winrm_devices", options: {} };
      if (segments[0] === "device" && segments[1]) {
        const id = decodeURIComponent(segments[1]);
        return {
          page: "device_details",
          options: { device: { agent_guid: id, hostname: id } }
        };
      }
      if (path === "/sites") return { page: "sites", options: {} };
      if (path === "/scheduling") return { page: "jobs", options: {} };
      if (path === "/scheduling/create_job") return { page: "create_job", options: {} };
      if (path === "/workflows") return { page: "workflows", options: {} };
      if (path === "/workflows/editor") return { page: "workflow-editor", options: {} };
      if (path === "/assemblies") return { page: "assemblies", options: {} };
      if (path === "/assemblies/editor") {
        const mode = params.get("mode");
        const relPath = params.get("path") || "";
        const state = relPath
          ? { path: relPath, mode: mode === "ansible" ? "ansible" : "scripts", nonce: Date.now() }
          : null;
        return {
          page: mode === "ansible" ? "ansible_editor" : "scripts",
          options: state ? { assemblyState: state } : {}
        };
      }
      if (path === "/access_management/users") return { page: "access_users", options: {} };
      if (path === "/access_management/github_token") return { page: "access_github_token", options: {} };
      if (path === "/access_management/credentials") return { page: "access_credentials", options: {} };
      if (path === "/admin/server_info") return { page: "server_info", options: {} };
      if (path === "/admin/page_template") return { page: "page_template", options: {} };
      if (path === "/admin/enrollment-codes") return { page: "admin_enrollment_codes", options: {} };
      if (path === "/admin/device-approvals") return { page: "admin_device_approvals", options: {} };
      return { page: "devices", options: {} };
    } catch {
      return { page: "devices", options: {} };
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
    },
    [setAssemblyEditorState, setCurrentPageState, setSelectedDevice]
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
        items.push({ label: editingJob ? "Edit Job" : "Create Job", page: "create_job" });
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
      case "access_github_token":
        items.push({ label: "Access Management", page: "access_credentials" });
        items.push({ label: "GitHub API Token", page: "access_github_token" });
        break;
      case "access_users":
        items.push({ label: "Access Management", page: "access_credentials" });
        items.push({ label: "Users", page: "access_users" });
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
      case "admin_enrollment_codes":
        items.push({ label: "Admin Settings", page: "server_info" });
        items.push({ label: "Installer Codes", page: "admin_enrollment_codes" });
        break;
      case "admin_device_approvals":
        items.push({ label: "Admin Settings", page: "server_info" });
        items.push({ label: "Device Approvals", page: "admin_device_approvals" });
        break;
      case "filters":
        items.push({ label: "Filters & Groups", page: "filters" });
        items.push({ label: "Filters", page: "filters" });
        break;
      case "groups":
        items.push({ label: "Filters & Groups", page: "filters" });
        items.push({ label: "Groups", page: "groups" });
        break;
      default:
        // Fallback to a neutral crumb if unknown
        if (currentPage) items.push({ label: String(currentPage) });
    }
    return items;
  }, [currentPage, selectedDevice, editingJob]);

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
          }
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
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
  }, []);

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
          ? "/devices"
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

      // Suggest fetcher with debounce
      const fetchSuggestions = useCallback((field, q) => {
        const query = String(q || "").trim();
        if (query.length < 3) {
          setSuggestions({ devices: [], sites: [], q: query, field });
          return;
        }
        const params = new URLSearchParams({ field, q: query, limit: "5" });
        fetch(`/api/search/suggest?${params.toString()}`)
          .then((r) => (r.ok ? r.json() : { devices: [], sites: [], q: query, field }))
          .then((data) => setSuggestions(data))
          .catch(() => setSuggestions({ devices: [], sites: [], q: query, field }));
      }, []);

      useEffect(() => {
        if (!searchOpen) return;
        if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
        searchDebounceRef.current = setTimeout(() => {
          fetchSuggestions(searchCategory, searchQuery);
        }, 220);
        return () => { if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current); };
      }, [searchOpen, searchCategory, searchQuery, fetchSuggestions]);

      const execSearch = useCallback(async (field, q, navigateImmediate = true) => {
        const cat = SEARCH_CATEGORIES.find((c) => c.key === field) || SEARCH_CATEGORIES[0];
        if (cat.scope === "site") {
          try {
            localStorage.setItem('site_list_initial_filters', JSON.stringify(
              field === 'site_name' ? { name: q } : { description: q }
            ));
          } catch {}
          if (navigateImmediate) navigateTo("sites");
        } else {
          // device field
          // Map API field -> Device_List filter key
          const fieldMap = {
            hostname: 'hostname',
            description: 'description',
            last_user: 'lastUser',
            internal_ip: 'internalIp',
            external_ip: 'externalIp',
            serial_number: 'serialNumber', // placeholder (ignored by Device_List for now)
          };
          const k = fieldMap[field] || 'hostname';
          const qLc = String(q || '').toLowerCase();
          const exact = (suggestions.devices || []).find((d) => String(d.hostname || d.value || '').toLowerCase() === qLc);
          if (exact && (exact.hostname || '').trim()) {
            const device = { hostname: exact.hostname.trim() };
            if (navigateImmediate) {
              navigateTo('device_details', { device });
            } else {
              setSelectedDevice(device);
            }
          } else if (field === 'hostname') {
            // Probe device existence and open directly if found
            try {
              const resp = await fetch(`/api/device/details/${encodeURIComponent(q)}`);
              if (resp.ok) {
                const data = await resp.json();
                if (data && (data.summary?.hostname || Object.keys(data).length > 0)) {
                  const device = { hostname: q };
                  if (navigateImmediate) {
                    navigateTo('device_details', { device });
                  } else {
                    setSelectedDevice(device);
                  }
                } else {
                  try { localStorage.setItem('device_list_initial_filters', JSON.stringify({ [k]: q })); } catch {}
                  if (navigateImmediate) navigateTo('devices');
                }
              } else {
                try { localStorage.setItem('device_list_initial_filters', JSON.stringify({ [k]: q })); } catch {}
                if (navigateImmediate) navigateTo('devices');
              }
            } catch {
              try { localStorage.setItem('device_list_initial_filters', JSON.stringify({ [k]: q })); } catch {}
              if (navigateImmediate) navigateTo('devices');
            }
          } else {
            try {
              const payload = (k === 'serialNumber') ? {} : { [k]: q };
              localStorage.setItem('device_list_initial_filters', JSON.stringify(payload));
            } catch {}
            if (navigateImmediate) navigateTo("devices");
          }
        }
        setSearchOpen(false);
      }, [SEARCH_CATEGORIES, navigateTo, suggestions.devices]);

  const handleLoginSuccess = ({ username, role }) => {
    setUser(username);
    setUserRole(role || null);
    setUserDisplayName(username);
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
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
        }
      } catch {}
    })();
    if (pendingPathRef.current) {
      navigateByPath(pendingPathRef.current, { replace: true, allowUnauthenticated: true });
      pendingPathRef.current = null;
    } else {
      navigateTo('devices', { replace: true, allowUnauthenticated: true });
    }
  };

  useEffect(() => {
    const saved = localStorage.getItem(LOCAL_STORAGE_KEY);
    if (saved) {
      try {
        const parsed = JSON.parse(saved);
        if (Array.isArray(parsed.tabs) && parsed.activeTabId) {
          setTabs(parsed.tabs);
          setActiveTabId(parsed.activeTabId);
        }
      } catch (err) {
        console.warn("Failed to parse saved state:", err);
      }
    }
  }, []);

  useEffect(() => {
    const timeout = setTimeout(() => {
      const data = JSON.stringify({ tabs, activeTabId });
      localStorage.setItem(LOCAL_STORAGE_KEY, data);
    }, 1000);
    return () => clearTimeout(timeout);
  }, [tabs, activeTabId]);

  const handleSetNodes = useCallback((callbackOrArray, tId) => {
    const targetId = tId || activeTabId;
    setTabs((old) =>
      old.map((tab) =>
        tab.id === targetId
          ? { ...tab, nodes: typeof callbackOrArray === "function" ? callbackOrArray(tab.nodes) : callbackOrArray }
          : tab
      )
    );
  }, [activeTabId]);

  const handleSetEdges = useCallback((callbackOrArray, tId) => {
    const targetId = tId || activeTabId;
    setTabs((old) =>
      old.map((tab) =>
        tab.id === targetId
          ? { ...tab, edges: typeof callbackOrArray === "function" ? callbackOrArray(tab.edges) : callbackOrArray }
          : tab
      )
    );
  }, [activeTabId]);

  const handleUserMenuOpen = (event) => setUserMenuAnchorEl(event.currentTarget);
  const handleUserMenuClose = () => setUserMenuAnchorEl(null);
  const handleLogout = async () => {
    try {
      await fetch('/api/auth/logout', { method: 'POST', credentials: 'include' });
    } catch {}
    try { localStorage.removeItem('borealis_session'); } catch {}
    setUser(null);
    setUserRole(null);
    setUserDisplayName(null);
    navigateTo('login', { replace: true, allowUnauthenticated: true, suppressPending: true });
  };

  const handleTabRightClick = (evt, tabId) => {
    evt.preventDefault();
    setTabMenuAnchor({ x: evt.clientX, y: evt.clientY });
    setTabMenuTabId(tabId);
  };

  const handleCloseTab = () => {
    setTabs((prev) => {
      const filtered = prev.filter((t) => t.id !== tabMenuTabId);
      if (filtered.length === 0) {
        const newTab = { id: "flow_1", tab_name: "Flow 1", nodes: [], edges: [] };
        setActiveTabId(newTab.id);
        return [newTab];
      }
      if (activeTabId === tabMenuTabId) {
        setActiveTabId(filtered[0].id);
      }
      return filtered;
    });
    setTabMenuAnchor(null);
  };

  const handleRenameTab = () => {
    const tab = tabs.find((t) => t.id === tabMenuTabId);
    if (tab) {
      setRenameTabId(tabMenuTabId);
      setRenameValue(tab.tab_name);
      setRenameDialogOpen(true);
    }
    setTabMenuAnchor(null);
  };

  const handleSaveRename = () => {
    setTabs((prev) =>
      prev.map((t) => (t.id === renameTabId ? { ...t, tab_name: renameValue } : t))
    );
    setRenameDialogOpen(false);
  };

  const handleExportFlow = useCallback(() => {
    const tab = tabs.find((t) => t.id === activeTabId);
    if (!tab) return;
    const payload = {
      tab_name: tab.tab_name,
      nodes: tab.nodes,
      edges: tab.edges
    };
    const fileName = `${tab.tab_name || "workflow"}.json`;
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = fileName;
    a.click();
    URL.revokeObjectURL(url);
  }, [tabs, activeTabId]);

  const handleImportFlow = useCallback(() => {
    if (fileInputRef.current) {
      fileInputRef.current.value = null;
      fileInputRef.current.click();
    }
  }, []);

  const onFileInputChange = useCallback(
    (e) => {
      const file = e.target.files && e.target.files[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = () => {
        try {
          const data = JSON.parse(reader.result);
          const newId = "flow_" + Date.now();
          setTabs((prev) => [
            ...prev,
            {
              id: newId,
              tab_name:
                data.tab_name || data.name || file.name.replace(/\.json$/i, ""),
              nodes: data.nodes || [],
              edges: data.edges || []
            }
          ]);
          setActiveTabId(newId);
          navigateTo("workflow-editor");
        } catch (err) {
          console.error("Failed to import workflow:", err);
        }
      };
      reader.readAsText(file);
      e.target.value = "";
    },
    [navigateTo, setTabs]
  );

  const handleSaveFlow = useCallback(
    async (name) => {
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab || !name) return;
      const document = {
        tab_name: name,
        name,
        display_name: name,
        nodes: tab.nodes,
        edges: tab.edges,
      };
      try {
        const resp = await fetch("/api/assemblies/import", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            document,
            domain: tab.domain || "user",
            assembly_guid: tab.assemblyGuid || undefined,
          }),
        });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
        setTabs((prev) =>
          prev.map((t) =>
            t.id === activeTabId
              ? {
                  ...t,
                  tab_name: name,
                  assemblyGuid: data?.assembly_guid || t.assemblyGuid || null,
                  domain: (data?.source || data?.domain || t.domain || "user").toLowerCase(),
                }
              : t
          )
        );
      } catch (err) {
        console.error("Failed to save workflow:", err);
      }
    },
    [tabs, activeTabId]
  );

  const openScriptFromList = useCallback(
    (row) => {
      if (!row) return;
      const normalizedRow = {
        ...row,
        domain: (row?.domain || "user").toLowerCase(),
      };
      const mode = normalizedRow.typeKey === "ansible" || normalizedRow.mode === "ansible" ? "ansible" : "script";
      const nonce = Date.now();
      const state = {
        mode,
        row: normalizedRow,
        nonce,
      };
      setAssemblyEditorState(state);
      navigateTo(mode === "ansible" ? "ansible_editor" : "scripts", { assemblyState: state });
    },
    [navigateTo, setAssemblyEditorState]
  );

  const openWorkflowFromList = useCallback(
    async (row) => {
      const newId = "flow_" + Date.now();
      const rawDomain = (row?.domain || "user").toLowerCase();
      const sourcePath = row?.sourcePath || row?.metadata?.source_path || "";
      const folderPath = sourcePath ? sourcePath.split("/").slice(0, -1).join("/") : "";
      if (row?.assemblyGuid) {
        try {
          const resp = await fetch(`/api/assemblies/${encodeURIComponent(row.assemblyGuid)}/export`);
          if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
          const data = await resp.json();
          let payload = data?.payload;
          if (typeof payload === "string") {
            try {
              payload = JSON.parse(payload);
            } catch {
              payload = {};
            }
          }
          const nodes = Array.isArray(payload?.nodes) ? payload.nodes : [];
          const edges = Array.isArray(payload?.edges) ? payload.edges : [];
          const tabName = payload?.tab_name || data?.display_name || row?.name || "Workflow";
          const domain = (data?.domain || rawDomain).toLowerCase();
          setTabs([
            {
              id: newId,
              tab_name: tabName,
              nodes,
              edges,
              folderPath,
              assemblyGuid: data?.assembly_guid || row?.assemblyGuid || null,
              domain,
              sourceRow: row,
              exportMetadata: data,
            },
          ]);
        } catch (err) {
          console.error("Failed to load workflow:", err);
          setTabs([
            {
              id: newId,
              tab_name: row?.name || "Workflow",
              nodes: [],
              edges: [],
              folderPath,
              assemblyGuid: row?.assemblyGuid || null,
              domain: rawDomain,
              sourceRow: row,
            },
          ]);
        }
      } else {
        setTabs([
          {
            id: newId,
            tab_name: row?.name || "Workflow",
            nodes: [],
            edges: [],
            folderPath,
            assemblyGuid: null,
            domain: rawDomain,
            sourceRow: row,
          },
        ]);
      }
      setActiveTabId(newId);
      navigateTo("workflow-editor");
    },
    [navigateTo, setTabs, setActiveTabId]
  );

  const isAdmin = (String(userRole || '').toLowerCase() === 'admin');

  useEffect(() => {
    const requiresAdmin = currentPage === 'server_info'
      || currentPage === 'admin_enrollment_codes'
      || currentPage === 'admin_device_approvals'
      || currentPage === 'access_credentials'
      || currentPage === 'access_github_token'
      || currentPage === 'access_users'
      || currentPage === 'log_management'
      || currentPage === 'ssh_devices'
      || currentPage === 'winrm_devices'
      || currentPage === 'agent_devices'
      || currentPage === 'page_template';
    if (!isAdmin && requiresAdmin) {
      setNotAuthorizedOpen(true);
      navigateTo('devices', { replace: true, suppressPending: true });
    }
  }, [currentPage, isAdmin, navigateTo]);

  const renderMainContent = () => {
    switch (currentPage) {
      case "sites":
        return (
          <SiteList
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
            onSelectDevice={(d) => {
              navigateTo("device_details", { device: d });
            }}
            onQuickJobLaunch={handleQuickJobLaunch}
          />
        );
      case "agent_devices":
        return (
          <AgentDevices
            onSelectDevice={(d) => {
              navigateTo("device_details", { device: d });
            }}
            onQuickJobLaunch={handleQuickJobLaunch}
          />
        );
      case "ssh_devices":
        return <SSHDevices onQuickJobLaunch={handleQuickJobLaunch} />;
      case "winrm_devices":
        return <WinRMDevices onQuickJobLaunch={handleQuickJobLaunch} />;

      case "device_details":
        return (
          <DeviceDetails
            device={selectedDevice}
            onQuickJobLaunch={handleQuickJobLaunch}
            onBack={() => {
              navigateTo("devices");
              setSelectedDevice(null);
            }}
          />
        );

      case "jobs":
        return (
          <ScheduledJobsList
            onCreateJob={() => { setEditingJob(null); navigateTo("create_job"); }}
            onEditJob={(job) => { setEditingJob(job); navigateTo("create_job"); }}
            refreshToken={jobsRefreshToken}
          />
        );

      case "create_job":
        return (
          <CreateJob
            initialJob={editingJob}
            quickJobDraft={quickJobDraft}
            onConsumeQuickJobDraft={handleConsumeQuickJobDraft}
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
            onOpenWorkflow={openWorkflowFromList}
            onOpenScript={openScriptFromList}
            userRole={userRole || 'User'}
          />
        );

      case "assemblies":
        return (
          <AssemblyList
            onOpenWorkflow={openWorkflowFromList}
            onOpenScript={openScriptFromList}
            userRole={userRole || 'User'}
          />
        );

      case "scripts":
        return (
          <AssemblyEditor
            mode="script"
            initialAssembly={assemblyEditorState && assemblyEditorState.mode === 'script' ? assemblyEditorState : null}
            onConsumeInitialData={() => {
              setAssemblyEditorState((prev) => (prev && prev.mode === 'script' ? null : prev));
            }}
            onSaved={() => navigateTo('assemblies')}
            userRole={userRole || 'User'}
          />
        );

      case "ansible_editor":
        return (
          <AssemblyEditor
            mode="ansible"
            initialAssembly={assemblyEditorState && assemblyEditorState.mode === 'ansible' ? assemblyEditorState : null}
            onConsumeInitialData={() => {
              setAssemblyEditorState((prev) => (prev && prev.mode === 'ansible' ? null : prev));
            }}
            onSaved={() => navigateTo('assemblies')}
            userRole={userRole || 'User'}
          />
        );

      case "access_credentials":
        return <CredentialList isAdmin={isAdmin} />;

      case "access_github_token":
        return <GithubAPIToken isAdmin={isAdmin} />;

      case "access_users":
        return <UserManagement isAdmin={isAdmin} />;

      case "server_info":
        return <ServerInfo isAdmin={isAdmin} />;
      case "log_management":
        return <LogManagement isAdmin={isAdmin} />;

      case "page_template":
        return <PageTemplate isAdmin={isAdmin} />;

      case "admin_enrollment_codes":
        return <EnrollmentCodes />;

      case "admin_device_approvals":
        return <DeviceApprovals />;

      case "workflow-editor":
        return (
          <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden" }}>
            <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
              <NodeSidebar
                categorizedNodes={categorizedNodes}
                handleExportFlow={handleExportFlow}
                handleImportFlow={handleImportFlow}
                handleSaveFlow={handleSaveFlow}
                handleOpenCloseAllDialog={() => setConfirmCloseOpen(true)}
                fileInputRef={fileInputRef}
                onFileInputChange={onFileInputChange}
                currentTabName={tabs.find((t) => t.id === activeTabId)?.tab_name}
              />
              <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden" }}>
                <FlowTabs
                  tabs={tabs}
                  activeTabId={activeTabId}
                  onTabChange={setActiveTabId}
                  onAddTab={() => {}}
                  onTabRightClick={handleTabRightClick}
                />
                <Box sx={{ flexGrow: 1, position: "relative" }}>
                  {tabs.map((tab) => (
                    <Box
                      key={tab.id}
                      sx={{
                        position: "absolute", top: 0, bottom: 0, left: 0, right: 0,
                        display: tab.id === activeTabId ? "block" : "none"
                      }}
                    >
                      <ReactFlowProvider id={tab.id}>
                        <FlowEditor
                          flowId={tab.id}
                          nodes={tab.nodes}
                          edges={tab.edges}
                          setNodes={(val) => handleSetNodes(val, tab.id)}
                          setEdges={(val) => handleSetEdges(val, tab.id)}
                          nodeTypes={nodeTypes}
                          categorizedNodes={categorizedNodes}
                        />
                      </ReactFlowProvider>
                    </Box>
                  ))}
                </Box>
              </Box>
            </Box>
            <StatusBar />
          </Box>
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

  return (
    <ThemeProvider theme={darkTheme}>
      <CssBaseline />
      <Box sx={{ width: "100vw", height: "100vh", display: "flex", flexDirection: "column", overflow: "hidden" }}>
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

              {/* Search (about 20% wider) */}
              <ClickAwayListener onClickAway={() => setSearchOpen(false)}>
                <Box sx={{ display: "flex", alignItems: "center", gap: 0.5, ml: 2 }}>
                  {/* Category button unchanged... */}

                  <Box
                    ref={searchAnchorRef}
                    sx={{
                      position: "relative",
                      display: "flex",
                      alignItems: "center",
                      border: "1px solid #3a3f44",
                      borderRadius: 1,
                      height: 34,
                      minWidth: 384,             // was 320 → ~20% wider
                      bgcolor: "#1e2328"
                    }}
                  >
                    <input
                      value={searchQuery}
                      onChange={(e) => { setSearchQuery(e.target.value); setSearchOpen(true); }}
                      onFocus={() => setSearchOpen(true)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") execSearch(searchCategory, searchQuery);
                        else if (e.key === "Escape") setSearchOpen(false);
                      }}
                      placeholder={(SEARCH_CATEGORIES.find(c => c.key === searchCategory) || {}).placeholder || "Search"}
                      style={{
                        outline: "none",
                        border: "none",
                        background: "transparent",
                        color: "#e8eaed",
                        paddingLeft: 10,
                        paddingRight: 28,
                        width: 360,               // keep input width comfortable inside the wider box
                        height: "100%"
                      }}
                    />
                    <SearchIcon sx={{ position: "absolute", right: 6, color: "#8aa0b4", fontSize: 18 }} />

                    {/* suggestions popover remains as-is */}
                    { /* ...existing suggestions code... */ }
                  </Box>
                </Box>
              </ClickAwayListener>

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

              {/* User Menu (unchanged) */}
              <Button
                color="inherit"
                onClick={handleUserMenuOpen}
                endIcon={<KeyboardArrowDownIcon />}
                sx={{ height: 36 }}
              >
                {userDisplayName || user || "User"}
              </Button>
              <Menu anchorEl={userMenuAnchorEl} open={Boolean(userMenuAnchorEl)} onClose={handleUserMenuClose}>
                <MenuItem onClick={() => { handleUserMenuClose(); handleLogout(); }}>
                  <LogoutIcon sx={{ fontSize: 18, color: "#ff6b6b", mr: 1 }} /> Logout
                </MenuItem>
              </Menu>
            </Toolbar>
          </AppBar>

        <Box sx={{ display: "flex", flexGrow: 1, overflow: "auto", minHeight: 0 }}>
          <NavigationSidebar currentPage={currentPage} onNavigate={navigateTo} isAdmin={isAdmin} />
          <Box
            sx={{
              flexGrow: 1,
              display: 'flex',
              flexDirection: 'column',
              overflow: 'auto',
              minHeight: 0,
              // Ensure primary page container (usually a Paper with m:2) fills to the bottom
              '& > *': {
                alignSelf: 'stretch',
                minHeight: 'calc(100% - 32px)' // account for typical m:2 top+bottom margins
              }
            }}
          >
            {renderMainContent()}
          </Box>
        </Box>
      </Box>
      <CloseAllDialog open={confirmCloseOpen} onClose={() => setConfirmCloseOpen(false)} onConfirm={() => {}} />
      <RenameTabDialog
        open={renameDialogOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameDialogOpen(false)}
        onSave={handleSaveRename}
      />
      <TabContextMenu
        anchor={tabMenuAnchor}
        onClose={() => setTabMenuAnchor(null)}
        onRename={handleRenameTab}
        onCloseTab={handleCloseTab}
      />
      <NotAuthorizedDialog open={notAuthorizedOpen} onClose={() => setNotAuthorizedOpen(false)} />
    </ThemeProvider>
  );
}
