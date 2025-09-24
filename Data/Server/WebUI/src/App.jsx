////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/App.jsx

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
    import SearchIcon from "@mui/icons-material/Search";
    import ArrowDropDownIcon from "@mui/icons-material/ArrowDropDown";
    import ArrowDropUpIcon from "@mui/icons-material/ArrowDropUp";

// Workflow Editor Imports
import FlowTabs from "./Flow_Editor/Flow_Tabs";
import FlowEditor from "./Flow_Editor/Flow_Editor";
import NodeSidebar from "./Flow_Editor/Node_Sidebar";
import StatusBar from "./Status_Bar";

// Borealis Page Imports
import Login from "./Login.jsx";
import SiteList from "./Sites/Site_List";
import DeviceList from "./Devices/Device_List";
import DeviceDetails from "./Devices/Device_Details";
import WorkflowList from "./Workflows/Workflow_List";
import ScriptEditor from "./Scripting/Script_Editor";
import ScheduledJobsList from "./Scheduling/Scheduled_Jobs_List";
import CreateJob from "./Scheduling/Create_Job.jsx";
import UserManagement from "./Admin/User_Management.jsx";
import ServerInfo from "./Admin/Server_Info.jsx";

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
  const [currentPage, setCurrentPage] = useState("devices");
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

  // Build breadcrumb items for current view
  const breadcrumbs = React.useMemo(() => {
    const items = [];
    switch (currentPage) {
      case "sites":
        items.push({ label: "Sites", page: "sites" });
        items.push({ label: "Site List", page: "sites" });
        break;
      case "devices":
        items.push({ label: "Devices", page: "devices" });
        items.push({ label: "Device List", page: "devices" });
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
      case "community":
        items.push({ label: "Automation", page: "jobs" });
        items.push({ label: "Community Content", page: "community" });
        break;
      case "admin_users":
        items.push({ label: "Admin Settings", page: "admin_users" });
        items.push({ label: "User Management", page: "admin_users" });
        break;
      case "server_info":
        items.push({ label: "Admin Settings", page: "admin_users" });
        items.push({ label: "Server Info", page: "server_info" });
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
        const session = localStorage.getItem("borealis_session");
    if (session) {
      try {
        const data = JSON.parse(session);
        if (Date.now() - data.timestamp < 3600 * 1000) {
          setUser(data.username);
          setUserRole(data.role || null);
          setUserDisplayName(data.display_name || data.username);
        } else {
          localStorage.removeItem("borealis_session");
        }
      } catch {
        localStorage.removeItem("borealis_session");
      }
    }
    (async () => {
      try {
        const resp = await fetch('/api/auth/me', { credentials: 'include' });
        if (resp.ok) {
          const me = await resp.json();
          setUser(me.username);
          setUserRole(me.role || null);
          setUserDisplayName(me.display_name || me.username);
          localStorage.setItem(
            "borealis_session",
            JSON.stringify({ username: me.username, display_name: me.display_name || me.username, role: me.role, timestamp: Date.now() })
          );
        }
      } catch {}
    })();
      }, []);

      // Suggest fetcher with debounce
      const fetchSuggestions = useCallback((field, q) => {
        const params = new URLSearchParams({ field, q, limit: "5" });
        fetch(`/api/search/suggest?${params.toString()}`)
          .then((r) => (r.ok ? r.json() : { devices: [], sites: [] }))
          .then((data) => setSuggestions(data))
          .catch(() => setSuggestions({ devices: [], sites: [], q, field }));
      }, []);

      useEffect(() => {
        if (!searchOpen) return;
        if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
        searchDebounceRef.current = setTimeout(() => {
          fetchSuggestions(searchCategory, searchQuery);
        }, 220);
        return () => { if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current); };
      }, [searchOpen, searchCategory, searchQuery, fetchSuggestions]);

      const execSearch = useCallback((field, q, navigateImmediate = true) => {
        const cat = SEARCH_CATEGORIES.find((c) => c.key === field) || SEARCH_CATEGORIES[0];
        if (cat.scope === "site") {
          try {
            localStorage.setItem('site_list_initial_filters', JSON.stringify(
              field === 'site_name' ? { name: q } : { description: q }
            ));
          } catch {}
          if (navigateImmediate) setCurrentPage("sites");
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
          try {
            const payload = (k === 'serialNumber') ? {} : { [k]: q };
            localStorage.setItem('device_list_initial_filters', JSON.stringify(payload));
          } catch {}
          if (navigateImmediate) setCurrentPage("devices");
        }
        setSearchOpen(false);
      }, [SEARCH_CATEGORIES, setCurrentPage]);

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
          setCurrentPage("workflow-editor");
        } catch (err) {
          console.error("Failed to import workflow:", err);
        }
      };
      reader.readAsText(file);
      e.target.value = "";
    },
    [setTabs]
  );

  const handleSaveFlow = useCallback(
    async (name) => {
      const tab = tabs.find((t) => t.id === activeTabId);
      if (!tab || !name) return;
      const payload = {
        path: tab.folderPath ? `${tab.folderPath}/${name}` : name,
        workflow: {
          tab_name: tab.tab_name,
          nodes: tab.nodes,
          edges: tab.edges
        }
      };
      try {
        await fetch("/api/storage/save_workflow", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        });
        setTabs((prev) =>
          prev.map((t) => (t.id === activeTabId ? { ...t, tab_name: name } : t))
        );
      } catch (err) {
        console.error("Failed to save workflow:", err);
      }
    },
    [tabs, activeTabId]
  );

  const isAdmin = (String(userRole || '').toLowerCase() === 'admin');

  useEffect(() => {
    if (!isAdmin && (currentPage === 'admin_users' || currentPage === 'server_info')) {
      setNotAuthorizedOpen(true);
      setCurrentPage('devices');
    }
  }, [currentPage, isAdmin]);

  const renderMainContent = () => {
    switch (currentPage) {
      case "sites":
        return (
          <SiteList
            onOpenDevicesForSite={(siteName) => {
              try {
                localStorage.setItem('device_list_initial_site_filter', String(siteName || ''));
              } catch {}
              setCurrentPage("devices");
            }}
          />
        );
      case "devices":
        return (
          <DeviceList
            onSelectDevice={(d) => {
              setSelectedDevice(d);
              setCurrentPage("device_details");
            }}
          />
        );

      case "device_details":
        return (
          <DeviceDetails
            device={selectedDevice}
            onBack={() => {
              setCurrentPage("devices");
              setSelectedDevice(null);
            }}
          />
        );

      case "jobs":
        return (
          <ScheduledJobsList
            onCreateJob={() => { setEditingJob(null); setCurrentPage("create_job"); }}
            onEditJob={(job) => { setEditingJob(job); setCurrentPage("create_job"); }}
            refreshToken={jobsRefreshToken}
          />
        );

      case "create_job":
        return (
          <CreateJob
            initialJob={editingJob}
            onCancel={() => { setCurrentPage("jobs"); setEditingJob(null); }}
            onCreated={() => { setCurrentPage("jobs"); setEditingJob(null); setJobsRefreshToken(Date.now()); }}
          />
        );

      case "workflows":
        return (
          <WorkflowList
            onOpenWorkflow={async (workflow, folderPath, name) => {
              const newId = "flow_" + Date.now();
              if (workflow && workflow.rel_path) {
                const folder = workflow.rel_path
                  .split("/")
                  .slice(0, -1)
                  .join("/");
                try {
                  const resp = await fetch(
                    `/api/storage/load_workflow?path=${encodeURIComponent(
                      workflow.rel_path
                    )}`
                  );
                  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
                  const data = await resp.json();
                  setTabs([
                    {
                      id: newId,
                      tab_name:
                        data.tab_name || workflow.name || workflow.file_name || "Workflow",
                      nodes: data.nodes || [],
                      edges: data.edges || [],
                      folderPath: folder
                    }
                  ]);
                } catch (err) {
                  console.error("Failed to load workflow:", err);
                  setTabs([
                    {
                      id: newId,
                      tab_name: workflow?.name || "Workflow",
                      nodes: [],
                      edges: [],
                      folderPath: folder
                    }
                  ]);
                }
              } else {
                setTabs([
                  {
                    id: newId,
                    tab_name: name || "Flow",
                    nodes: [],
                    edges: [],
                    folderPath: folderPath || ""
                  }
                ]);
              }
              setActiveTabId(newId);
              setCurrentPage("workflow-editor");
            }}
          />
        );

      case "scripts":
        return <ScriptEditor />;

      case "admin_users":
        return <UserManagement isAdmin={isAdmin} />;

      case "server_info":
        return <ServerInfo isAdmin={isAdmin} />;

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
          <AppBar position="static" sx={{ bgcolor: "#16191d" }}>
              <Toolbar sx={{ minHeight: "36px", position: 'relative' }}>
                <Box component="img" src="/Borealis_Logo_Full.png" alt="Borealis Logo" sx={{ height: "52px", marginRight: "8px" }} />
            {/* Breadcrumbs inline in top bar (transparent), aligned to content area */}
            <Box
              sx={{
                position: 'absolute',
                left: 'calc(260px + 550px)', // fine-tuned to align with black content edge
                bottom: 6,
                display: 'flex',
                alignItems: 'flex-end',
                pointerEvents: 'none' // avoid interfering with About menu positioning
              }}
            >
              <Breadcrumbs
                separator={<NavigateNextIcon fontSize="inherit" sx={{ color: "#6b6b6b" }} />}
                aria-label="breadcrumb"
                sx={{
                  color: "#9aa0a6",
                  fontSize: "0.825rem", // 50% larger than previous
                  '& .MuiBreadcrumbs-separator': { mx: 0.6 },
                  pointerEvents: 'auto'
                }}
              >
                {breadcrumbs.map((c, idx) => {
                  if (c.page) {
                    return (
                      <Button
                        key={idx}
                        onClick={() => setCurrentPage(c.page)}
                        size="small"
                        sx={{
                          color: "#7db7ff",
                          textTransform: "none",
                          minWidth: 0,
                          p: 0,
                          fontSize: "0.825rem"
                        }}
                      >
                        {c.label}
                      </Button>
                    );
                  }
                  return (
                    <Typography key={idx} component="span" sx={{ color: "#e0e0e0", fontSize: "0.825rem" }}>
                      {c.label}
                    </Typography>
                  );
                })}
              </Breadcrumbs>
                </Box>
                {/* Top search: category + input */}
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, ml: 2 }}>
                  <Button
                    variant="outlined"
                    size="small"
                    onClick={(e) => setSearchMenuEl(e.currentTarget)}
                    endIcon={searchMenuEl ? <ArrowDropUpIcon /> : <ArrowDropDownIcon />}
                    sx={{
                      height: 32,
                      color: '#ddd',
                      left: -11,
                      bottom: -6,
                      borderColor: '#3a3f44',
                      textTransform: 'none',
                      bgcolor: '#1e2328',
                      '&:hover': { borderColor: '#4b5158', bgcolor: '#22272e' },
                      minWidth: 160,
                      justifyContent: 'space-between'
                    }}
                  >
                    {(SEARCH_CATEGORIES.find(c => c.key === searchCategory) || {}).label || 'Hostname'}
                  </Button>
                  <Menu
                    anchorEl={searchMenuEl}
                    open={Boolean(searchMenuEl)}
                    onClose={() => setSearchMenuEl(null)}
                    PaperProps={{ sx: { bgcolor: '#1e1e1e', color: '#fff', minWidth: 240 } }}
                  >
                    {SEARCH_CATEGORIES.map((c) => (
                      <MenuItem key={c.key} onClick={() => { setSearchCategory(c.key); setSearchMenuEl(null); setSearchQuery(''); setSuggestions({ devices: [], sites: [], q: '', field: '' }); }}>
                        {c.label}
                      </MenuItem>
                    ))}
                  </Menu>
                  <Box
                    ref={searchAnchorRef}
                    sx={{ position: 'relative', left: -2, bottom: -6, display: 'flex', alignItems: 'center', border: '1px solid #3a3f44', borderRadius: 1, height: 32, minWidth: 320, bgcolor: '#1e2328' }}
                  >
                    <input
                      value={searchQuery}
                      onChange={(e) => { setSearchQuery(e.target.value); setSearchOpen(true); }}
                      onFocus={() => setSearchOpen(true)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          execSearch(searchCategory, searchQuery);
                        } else if (e.key === 'Escape') {
                          setSearchOpen(false);
                        }
                      }}
                      placeholder={(SEARCH_CATEGORIES.find(c => c.key === searchCategory) || {}).placeholder || 'Search'}
                      style={{
                        outline: 'none', border: 'none', background: 'transparent', color: '#e8eaed', paddingLeft: 10, paddingRight: 28, width: 360, height: '100%'
                      }}
                    />
                    <SearchIcon sx={{ position: 'absolute', right: 6, color: '#8aa0b4', fontSize: 18 }} />
                    {searchOpen && (
                      <Box
                        sx={{ position: 'absolute', top: '100%', left: 0, right: 0, bgcolor: '#121417', border: '1px solid #2b2f34', borderTop: 'none', zIndex: 1400, borderRadius: '0 0 6px 6px', maxHeight: 320, overflowY: 'auto' }}
                      >
                        {/* Devices group */}
                        {((suggestions.devices || []).length > 0 || (SEARCH_CATEGORIES.find(c=>c.key===searchCategory)?.scope==='device')) && (
                          <Box sx={{ borderBottom: '1px solid #2b2f34' }}>
                            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', px: 1.2, py: 0.8, color: '#9aa0a6', fontSize: 12 }}>
                              <span>Devices</span>
                              <Button size="small" onClick={() => execSearch(searchCategory, searchQuery)} sx={{ textTransform: 'none', color: '#80bfff' }}>View Results</Button>
                            </Box>
                            {suggestions.devices && suggestions.devices.length > 0 ? (
                              suggestions.devices.map((d, idx) => (
                                <Box key={idx} onClick={() => execSearch(searchCategory, d.value)} sx={{ px: 1.2, py: 0.6, '&:hover': { bgcolor: '#1c2127' }, cursor: 'pointer' }}>
                                  <Typography variant="body2" sx={{ color: '#e8eaed' }}>{d.hostname || d.value}</Typography>
                                  <Typography variant="caption" sx={{ color: '#9aa0a6' }}>{[d.site_name, d.internal_ip || d.external_ip || d.description || d.last_user].filter(Boolean).join(' • ')}</Typography>
                                </Box>
                              ))
                            ) : (
                              <Box sx={{ px: 1.2, py: 1, color: '#6b737c', fontSize: 12 }}>
                                {searchCategory === 'serial_number' ? 'Serial numbers are not tracked yet.' : 'No matches'}
                              </Box>
                            )}
                          </Box>
                        )}
                        {/* Sites group */}
                        {((suggestions.sites || []).length > 0 || (SEARCH_CATEGORIES.find(c=>c.key===searchCategory)?.scope==='site')) && (
                          <Box>
                            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', px: 1.2, py: 0.8, color: '#9aa0a6', fontSize: 12 }}>
                              <span>Sites</span>
                              <Button size="small" onClick={() => execSearch(searchCategory, searchQuery)} sx={{ textTransform: 'none', color: '#80bfff' }}>View Results</Button>
                            </Box>
                            {suggestions.sites && suggestions.sites.length > 0 ? (
                              suggestions.sites.map((s, idx) => (
                                <Box key={idx} onClick={() => execSearch(searchCategory, s.value)} sx={{ px: 1.2, py: 0.6, '&:hover': { bgcolor: '#1c2127' }, cursor: 'pointer' }}>
                                  <Typography variant="body2" sx={{ color: '#e8eaed' }}>{s.site_name}</Typography>
                                  <Typography variant="caption" sx={{ color: '#9aa0a6' }}>{s.site_description || ''}</Typography>
                                </Box>
                              ))
                            ) : (
                              <Box sx={{ px: 1.2, py: 1, color: '#6b737c', fontSize: 12 }}>No matches</Box>
                            )}
                          </Box>
                        )}
                      </Box>
                    )}
                  </Box>
                </Box>
                {/* Spacer to keep user menu aligned right */}
                <Box sx={{ flexGrow: 1 }} />
                <Button
                  color="inherit"
                  onClick={handleUserMenuOpen}
                  endIcon={<KeyboardArrowDownIcon />}
                  sx={{ height: "36px" }}
            >
              {userDisplayName || user || 'User'}
            </Button>
            <Menu anchorEl={userMenuAnchorEl} open={Boolean(userMenuAnchorEl)} onClose={handleUserMenuClose}>
              <MenuItem onClick={() => { handleUserMenuClose(); handleLogout(); }}>
                <LogoutIcon sx={{ fontSize: 18, color: "#ff6b6b", mr: 1 }} /> Logout
              </MenuItem>
            </Menu>
          </Toolbar>
        </AppBar>
        <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
          <NavigationSidebar currentPage={currentPage} onNavigate={setCurrentPage} isAdmin={isAdmin} />
          <Box sx={{ flexGrow: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
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
