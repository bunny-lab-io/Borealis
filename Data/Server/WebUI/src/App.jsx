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

  const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
  const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
  const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameTabId, setRenameTabId] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
  const [tabMenuTabId, setTabMenuTabId] = useState(null);
  const fileInputRef = useRef(null);

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

  const renderMainContent = () => {
    switch (currentPage) {
      case "devices":
        return <DeviceList />;

      case "jobs":
        return <ScheduledJobsList />;

      case "workflows":
        return (
          <WorkflowList
            onOpenWorkflow={(workflow) => {
              // If workflow name exists in tabs, just switch to it
              if (workflow?.name) {
                const existing = tabs.find(
                  (t) => t.tab_name.toLowerCase() === workflow.name.toLowerCase()
                );
                if (existing) {
                  setActiveTabId(existing.id);
                  setCurrentPage("workflow-editor");
                  return;
                }
              }
              // Otherwise, create a new workflow tab
              const newId = "flow_" + (tabs.length + 1);
              setTabs((prev) => [
                ...prev,
                {
                  id: newId,
                  tab_name: workflow?.name || `Flow ${tabs.length + 1}`,
                  nodes: [],
                  edges: []
                }
              ]);
              setActiveTabId(newId);
              setCurrentPage("workflow-editor");
            }}
          />
        );

      case "scripts":
        return <ScriptList />;

      case "workflow-editor":
        return (
          <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
            <NodeSidebar
              categorizedNodes={categorizedNodes}
              handleExportFlow={() => {}}
              handleImportFlow={() => {}}
              handleOpenCloseAllDialog={() => {}}
              fileInputRef={fileInputRef}
              onFileInputChange={() => {}}
            />
            <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden" }}>
              <FlowTabs
                tabs={tabs}
                activeTabId={activeTabId}
                onTabChange={setActiveTabId}
                onAddTab={() => {}}
                onTabRightClick={() => {}}
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
        );

      default:
        return (
          <Box sx={{ p: 2 }}>
            <Typography>Select a section from navigation.</Typography>
          </Box>
        );
    }
  };

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
        <StatusBar />
      </Box>
      <CloseAllDialog open={confirmCloseOpen} onClose={() => setConfirmCloseOpen(false)} onConfirm={() => {}} />
      <CreditsDialog open={creditsDialogOpen} onClose={() => setCreditsDialogOpen(false)} />
      <RenameTabDialog
        open={renameDialogOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameDialogOpen(false)}
        onSave={() => {}}
      />
      <TabContextMenu
        anchor={tabMenuAnchor}
        onClose={() => setTabMenuAnchor(null)}
        onRename={() => {}}
        onCloseTab={() => {}}
      />
    </ThemeProvider>
  );
}
