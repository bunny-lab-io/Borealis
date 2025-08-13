////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/App.jsx

import React, { useState, useEffect, useCallback, useRef } from "react";
import {
  AppBar, Toolbar, Typography, Box, Menu, MenuItem, Button,
  CssBaseline, ThemeProvider, createTheme
} from "@mui/material";
import {
  KeyboardArrowDown as KeyboardArrowDownIcon,
  InfoOutlined as InfoOutlinedIcon,
  MergeType as MergeTypeIcon,
  People as PeopleIcon
} from "@mui/icons-material";
import { ReactFlowProvider } from "reactflow";
import "reactflow/dist/style.css";

import FlowTabs from "./Flow_Tabs";
import FlowEditor from "./Flow_Editor";
import NodeSidebar from "./Node_Sidebar";
import {
  CloseAllDialog, CreditsDialog, RenameTabDialog, TabContextMenu
} from "./Dialogs";
import StatusBar from "./Status_Bar";

// New imports for split pages
import NavigationSidebar from "./Navigation_Sidebar";
import WorkflowList from "./Workflow_List";
import DeviceList from "./Device_List";
import ScriptList from "./Script_List";
import ScheduledJobsList from "./Scheduled_Jobs_List";
import Login from "./Login.jsx";
import DeviceDetails from "./Device_Details";

import { io } from "socket.io-client";

if (!window.BorealisSocket) {
  window.BorealisSocket = io(window.location.origin, { transports: ["websocket"] });
}
if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 200;
}

// Load node modules dynamically
const modules = import.meta.glob('./nodes/**/*.jsx', { eager: true });
const nodeTypes = {};
const categorizedNodes = {};
Object.entries(modules).forEach(([path, mod]) => {
  const comp = mod.default;
  if (!comp) return;
  const { type, component } = comp;
  if (!type || !component) return;
  const parts = path.replace('./nodes/', '').split('/');
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

  const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
  const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
  const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameTabId, setRenameTabId] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
  const [tabMenuTabId, setTabMenuTabId] = useState(null);
  const fileInputRef = useRef(null);
  const [user, setUser] = useState(null);

  useEffect(() => {
    const session = localStorage.getItem("borealis_session");
    if (session) {
      try {
        const data = JSON.parse(session);
        if (Date.now() - data.timestamp < 3600 * 1000) {
          setUser(data.username);
        } else {
          localStorage.removeItem("borealis_session");
        }
      } catch {
        localStorage.removeItem("borealis_session");
      }
    }
  }, []);

  const handleLoginSuccess = (username) => {
    setUser(username);
    localStorage.setItem(
      "borealis_session",
      JSON.stringify({ username, timestamp: Date.now() })
    );
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

  const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
  const handleAboutMenuClose = () => setAboutAnchorEl(null);
  const openCreditsDialog = () => { handleAboutMenuClose(); setCreditsDialogOpen(true); };

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

  const renderMainContent = () => {
    switch (currentPage) {
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
        return <ScheduledJobsList />;

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
        return <ScriptList />;

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
          <Toolbar sx={{ minHeight: "36px" }}>
            <Box component="img" src="/Borealis_Logo_Full.png" alt="Borealis Logo" sx={{ height: "52px", marginRight: "8px" }} />
            <Typography variant="h6" sx={{ flexGrow: 1, fontSize: "1rem" }}></Typography>
            <Button
              color="inherit"
              onClick={handleAboutMenuOpen}
              endIcon={<KeyboardArrowDownIcon />}
              startIcon={<InfoOutlinedIcon />}
              sx={{ height: "36px" }}
            >
              About
            </Button>
            <Menu anchorEl={aboutAnchorEl} open={Boolean(aboutAnchorEl)} onClose={handleAboutMenuClose}>
              <MenuItem onClick={() => { handleAboutMenuClose(); window.open("https://git.bunny-lab.io/bunny-lab/Borealis", "_blank"); }}>
                <MergeTypeIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} /> Gitea Project
              </MenuItem>
              <MenuItem onClick={openCreditsDialog}>
                <PeopleIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} /> Credits
              </MenuItem>
            </Menu>
          </Toolbar>
        </AppBar>
        <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
          <NavigationSidebar currentPage={currentPage} onNavigate={setCurrentPage} />
          <Box sx={{ flexGrow: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
            {renderMainContent()}
          </Box>
        </Box>
      </Box>
      <CloseAllDialog open={confirmCloseOpen} onClose={() => setConfirmCloseOpen(false)} onConfirm={() => {}} />
      <CreditsDialog open={creditsDialogOpen} onClose={() => setCreditsDialogOpen(false)} />
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
    </ThemeProvider>
  );
}
