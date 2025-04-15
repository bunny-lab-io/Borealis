////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/App.jsx

// Core React Imports
import React, {
    useState,
    useEffect,
    useCallback,
    useRef
  } from "react";
  
  // Material UI - Components
  import {
    AppBar,
    Toolbar,
    Typography,
    Box,
    Menu,
    MenuItem,
    Button,
    CssBaseline,
    ThemeProvider,
    createTheme
  } from "@mui/material";
  
  // Material UI - Icons
  import {
    KeyboardArrowDown as KeyboardArrowDownIcon,
    InfoOutlined as InfoOutlinedIcon,
    MergeType as MergeTypeIcon,
    People as PeopleIcon
  } from "@mui/icons-material";
  
  // React Flow
  import { ReactFlowProvider } from "reactflow";
  
  // Styles
  import "reactflow/dist/style.css";
  
  // Import Borealis Modules
  import FlowTabs from "./Flow_Tabs";
  import FlowEditor from "./Flow_Editor";
  import NodeSidebar from "./Node_Sidebar";
  import {
    CloseAllDialog,
    CreditsDialog,
    RenameTabDialog,
    TabContextMenu
  } from "./Dialogs";
  import StatusBar from "./Status_Bar";
  
  // Global Node Update Timer Variable
  if (!window.BorealisUpdateRate) {
    window.BorealisUpdateRate = 200;
  }
  
  // Dynamically load all node components
  const nodeContext = require.context("./nodes", true, /\.jsx$/);
  const nodeTypes = {};
  const categorizedNodes = {};
  
  nodeContext.keys().forEach((path) => {
    const mod = nodeContext(path);
    if (!mod.default) return;
    const { type, label, component } = mod.default;
    if (!type || !component) return;
  
    const pathParts = path.replace("./", "").split("/");
    if (pathParts.length < 2) return;
    const category = pathParts[0];
  
    if (!categorizedNodes[category]) {
      categorizedNodes[category] = [];
    }
    categorizedNodes[category].push(mod.default);
    nodeTypes[type] = component;
  });
  
  const darkTheme = createTheme({
    palette: {
      mode: "dark",
      background: {
        default: "#121212",
        paper: "#1e1e1e"
      },
      text: {
        primary: "#ffffff"
      }
    }
  });
  
  export default function App() {
    const [tabs, setTabs] = useState([
      {
        id: "flow_1",
        tab_name: "Flow 1",
        nodes: [],
        edges: []
      }
    ]);
    const [activeTabId, setActiveTabId] = useState("flow_1");
  
    const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
    const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
    const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
    const [renameDialogOpen, setRenameDialogOpen] = useState(false);
    const [renameTabId, setRenameTabId] = useState(null);
    const [renameValue, setRenameValue] = useState("");
    const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
    const [tabMenuTabId, setTabMenuTabId] = useState(null);
    const fileInputRef = useRef(null);
  
    const handleSetNodes = useCallback(
      (callbackOrArray, tId) => {
        const targetId = tId || activeTabId;
        setTabs((old) =>
          old.map((tab) => {
            if (tab.id !== targetId) return tab;
            const newNodes =
              typeof callbackOrArray === "function"
                ? callbackOrArray(tab.nodes)
                : callbackOrArray;
            return { ...tab, nodes: newNodes };
          })
        );
      },
      [activeTabId]
    );
  
    const handleSetEdges = useCallback(
      (callbackOrArray, tId) => {
        const targetId = tId || activeTabId;
        setTabs((old) =>
          old.map((tab) => {
            if (tab.id !== targetId) return tab;
            const newEdges =
              typeof callbackOrArray === "function"
                ? callbackOrArray(tab.edges)
                : callbackOrArray;
            return { ...tab, edges: newEdges };
          })
        );
      },
      [activeTabId]
    );
  
    const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
    const handleAboutMenuClose = () => setAboutAnchorEl(null);
    const openCreditsDialog = () => {
      handleAboutMenuClose();
      setCreditsDialogOpen(true);
    };
  
    const handleOpenCloseAllDialog = () => setConfirmCloseOpen(true);
    const handleCloseDialog = () => setConfirmCloseOpen(false);
    const handleConfirmCloseAll = () => {
      setTabs([
        {
          id: "flow_1",
          tab_name: "Flow 1",
          nodes: [],
          edges: []
        }
      ]);
      setActiveTabId("flow_1");
      setConfirmCloseOpen(false);
    };
  
    const createNewTab = () => {
      const nextIndex = tabs.length + 1;
      const newId = "flow_" + nextIndex;
      setTabs((old) => [
        ...old,
        {
          id: newId,
          tab_name: "Flow " + nextIndex,
          nodes: [],
          edges: []
        }
      ]);
      setActiveTabId(newId);
    };
  
    const handleTabChange = (newActiveTabId) => {
      setActiveTabId(newActiveTabId);
    };
  
    const handleTabRightClick = (evt, tabId) => {
      evt.preventDefault();
      setTabMenuAnchor({ x: evt.clientX, y: evt.clientY });
      setTabMenuTabId(tabId);
    };
  
    const handleCloseTabMenu = () => {
      setTabMenuAnchor(null);
      setTabMenuTabId(null);
    };
  
    const handleRenameTab = () => {
      setRenameDialogOpen(true);
      setRenameTabId(tabMenuTabId);
      const t = tabs.find((x) => x.id === tabMenuTabId);
      setRenameValue(t ? t.tab_name : "");
      handleCloseTabMenu();
    };
  
    const handleCloseTab = () => {
      setTabs((old) => {
        const idx = old.findIndex((t) => t.id === tabMenuTabId);
        if (idx === -1) return old;
  
        const newList = [...old];
        newList.splice(idx, 1);
  
        if (tabMenuTabId === activeTabId && newList.length > 0) {
          setActiveTabId(newList[0].id);
        } else if (newList.length === 0) {
          newList.push({
            id: "flow_1",
            tab_name: "Flow 1",
            nodes: [],
            edges: []
          });
          setActiveTabId("flow_1");
        }
        return newList;
      });
      handleCloseTabMenu();
    };
  
    const handleRenameDialogSave = () => {
      if (!renameTabId) {
        setRenameDialogOpen(false);
        return;
      }
      setTabs((old) =>
        old.map((tab) =>
          tab.id === renameTabId
            ? { ...tab, tab_name: renameValue }
            : tab
        )
      );
      setRenameDialogOpen(false);
    };
  
    const handleExportFlow = async () => {
      const activeTab = tabs.find((x) => x.id === activeTabId);
      if (!activeTab) return;
  
      const data = JSON.stringify(
        {
          nodes: activeTab.nodes,
          edges: activeTab.edges,
          tab_name: activeTab.tab_name
        },
        null,
        2
      );
      const blob = new Blob([data], { type: "application/json" });
      const sanitizedTabName = activeTab.tab_name.replace(/\s+/g, "_").toLowerCase();
      const suggestedFilename = sanitizedTabName + "_workflow.json";
  
      if (window.showSaveFilePicker) {
        try {
          const fileHandle = await window.showSaveFilePicker({
            suggestedName: suggestedFilename,
            types: [
              {
                description: "Workflow JSON File",
                accept: { "application/json": [".json"] }
              }
            ]
          });
  
          const writable = await fileHandle.createWritable();
          await writable.write(blob);
          await writable.close();
        } catch (err) {
          console.error("Save cancelled or failed:", err);
        }
      } else {
        const a = document.createElement("a");
        a.href = URL.createObjectURL(blob);
        a.download = suggestedFilename;
        a.style.display = "none";
        document.body.appendChild(a);
        a.click();
        URL.revokeObjectURL(a.href);
        document.body.removeChild(a);
      }
    };
  
    const handleImportFlow = async () => {
      if (window.showOpenFilePicker) {
        try {
          const [fileHandle] = await window.showOpenFilePicker({
            types: [
              {
                description: "Workflow JSON File",
                accept: { "application/json": [".json"] }
              }
            ]
          });
          const file = await fileHandle.getFile();
          const text = await file.text();
          const json = JSON.parse(text);
  
          const newId = "flow_" + (tabs.length + 1);
          setTabs((prev) => [
            ...prev,
            {
              id: newId,
              tab_name: json.tab_name || "Imported Flow " + (tabs.length + 1),
              nodes: json.nodes || [],
              edges: json.edges || []
            }
          ]);
          setActiveTabId(newId);
        } catch (err) {
          console.error("Import cancelled or failed:", err);
        }
      } else {
        fileInputRef.current?.click();
      }
    };
  
    const handleFileInputChange = async (e) => {
      const file = e.target.files[0];
      if (!file) return;
      try {
        const text = await file.text();
        const json = JSON.parse(text);
  
        const newId = "flow_" + (tabs.length + 1);
        setTabs((prev) => [
          ...prev,
          {
            id: newId,
            tab_name: json.tab_name || "Imported Flow " + (tabs.length + 1),
            nodes: json.nodes || [],
            edges: json.edges || []
          }
        ]);
        setActiveTabId(newId);
      } catch (err) {
        console.error("Failed to read file:", err);
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
                <MenuItem onClick={() => { handleAboutMenuClose(); window.open("https://git.bunny-lab.io/Borealis", "_blank"); }}>
                  <MergeTypeIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} /> Gitea Project
                </MenuItem>
                <MenuItem onClick={openCreditsDialog}>
                  <PeopleIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} /> Credits
                </MenuItem>
              </Menu>
            </Toolbar>
          </AppBar>
  
          <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
            <NodeSidebar
              categorizedNodes={categorizedNodes}
              handleExportFlow={handleExportFlow}
              handleImportFlow={handleImportFlow}
              handleOpenCloseAllDialog={handleOpenCloseAllDialog}
              fileInputRef={fileInputRef}
              onFileInputChange={handleFileInputChange}
            />
  
            <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, overflow: "hidden" }}>
              <FlowTabs
                tabs={tabs}
                activeTabId={activeTabId}
                onTabChange={handleTabChange}
                onAddTab={createNewTab}
                onTabRightClick={handleTabRightClick}
              />
  
              <Box sx={{ flexGrow: 1, position: "relative" }}>
                {tabs.map((tab) => (
                  <Box
                    key={tab.id}
                    sx={{
                      position: "absolute",
                      top: 0,
                      bottom: 0,
                      left: 0,
                      right: 0,
                      display: tab.id === activeTabId ? "block" : "none"
                    }}
                  >
                    <ReactFlowProvider>
                      <FlowEditor
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
  
        <CloseAllDialog
          open={confirmCloseOpen}
          onClose={handleCloseDialog}
          onConfirm={handleConfirmCloseAll}
        />
        <CreditsDialog open={creditsDialogOpen} onClose={() => setCreditsDialogOpen(false)} />
        <RenameTabDialog
          open={renameDialogOpen}
          value={renameValue}
          onChange={setRenameValue}
          onCancel={() => setRenameDialogOpen(false)}
          onSave={handleRenameDialogSave}
        />
        <TabContextMenu
          anchor={tabMenuAnchor}
          onClose={handleCloseTabMenu}
          onRename={handleRenameTab}
          onCloseTab={handleCloseTab}
        />
      </ThemeProvider>
    );
  }
  