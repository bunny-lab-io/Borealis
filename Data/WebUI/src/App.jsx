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
    createTheme,
    Dialog,
    DialogTitle,
    DialogContent,
    DialogContentText,
    DialogActions,
    Divider,
    TextField
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
  
  // Import our new components
  import FlowTabs from "./Flow_Tabs";
  import FlowEditor from "./Flow_Editor";
  import NodeSidebar from "./Node_Sidebar";
  
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
  
    // About menu
    const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
    const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
  
    // Close all flows
    const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
  
    // Rename tab
    const [renameDialogOpen, setRenameDialogOpen] = useState(false);
    const [renameTabId, setRenameTabId] = useState(null);
    const [renameValue, setRenameValue] = useState("");
  
    // Right-click tab menu
    const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
    const [tabMenuTabId, setTabMenuTabId] = useState(null);
  
    // File input ref (for imports on older browsers)
    const fileInputRef = useRef(null);
  
    // Setup callbacks to update nodes/edges in the currently active tab
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
  
    // About menu
    const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
    const handleAboutMenuClose = () => setAboutAnchorEl(null);
  
    // Credits
    const openCreditsDialog = () => {
      handleAboutMenuClose();
      setCreditsDialogOpen(true);
    };
  
    // Close all dialog
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
  
    // Create new tab
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
  
    // Handle user clicking on a tab
    const handleTabChange = (newActiveTabId) => {
      setActiveTabId(newActiveTabId);
    };
  
    // Right-click tab menu
    const handleTabRightClick = (evt, tabId) => {
      evt.preventDefault();
      setTabMenuAnchor({ x: evt.clientX, y: evt.clientY });
      setTabMenuTabId(tabId);
    };
    const handleCloseTabMenu = () => {
      setTabMenuAnchor(null);
      setTabMenuTabId(null);
    };
  
    // Rename / close tab
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
  
        // If we closed the current tab, pick a new active tab
        if (tabMenuTabId === activeTabId && newList.length > 0) {
          setActiveTabId(newList[0].id);
        } else if (newList.length === 0) {
          // If we closed the only tab, create a fresh one
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
  
    // Export current tab
    const handleExportFlow = async () => {
      const activeTab = tabs.find((x) => x.id === activeTabId);
      if (!activeTab) return;
  
      // Build JSON data from the active tab
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
  
      // Suggested filename based on the tab name
      // e.g. "Nicole Work Flow" => "nicole_work_flow_workflow.json"
      const sanitizedTabName = activeTab.tab_name
        .replace(/\s+/g, "_")
        .toLowerCase();
      const suggestedFilename = sanitizedTabName + "_workflow.json";
  
      // Check if showSaveFilePicker is available (Chrome/Edge)
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
        // Fallback for browsers like Firefox
        // (Relies on browser settings to ask user where to save)
        const a = document.createElement("a");
        a.href = URL.createObjectURL(blob);
        a.download = suggestedFilename; // e.g. nicole_work_flow_workflow.json
        a.style.display = "none";
        document.body.appendChild(a);
        a.click();
        // Cleanup
        URL.revokeObjectURL(a.href);
        document.body.removeChild(a);
      }
    };
  
    // Import flow -> new tab
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
        // Fallback for older browsers
        fileInputRef.current?.click();
      }
    };
  
    // Fallback <input> import
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
  
        <Box
          sx={{
            width: "100vw",
            height: "100vh",
            display: "flex",
            flexDirection: "column",
            overflow: "hidden"
          }}
        >
          <AppBar position="static" sx={{ bgcolor: "#16191d" }}>
            <Toolbar sx={{ minHeight: "36px" }}>
              {/* Logo */}
              <Box
                component="img"
                src="/Borealis_Logo_Full.png"
                alt="Borealis Logo"
                sx={{
                  height: "52px",
                  marginRight: "8px"
                }}
              />
  
              <Typography
                variant="h6"
                sx={{ flexGrow: 1, fontSize: "1rem" }}
              >
                {/* Additional Title/Info if desired */}
              </Typography>
  
              <Button
                color="inherit"
                onClick={handleAboutMenuOpen}
                endIcon={<KeyboardArrowDownIcon />}
                startIcon={<InfoOutlinedIcon />}
                sx={{ height: "36px" }}
              >
                About
              </Button>
  
              <Menu
                anchorEl={aboutAnchorEl}
                open={Boolean(aboutAnchorEl)}
                onClose={handleAboutMenuClose}
              >
                <MenuItem
                  onClick={() => {
                    handleAboutMenuClose();
                    window.open("https://git.bunny-lab.io/Borealis", "_blank");
                  }}
                >
                  <MergeTypeIcon
                    sx={{
                      fontSize: 18,
                      color: "#58a6ff",
                      mr: 1
                    }}
                  />
                  Gitea Project
                </MenuItem>
                <MenuItem onClick={openCreditsDialog}>
                  <PeopleIcon
                    sx={{
                      fontSize: 18,
                      color: "#58a6ff",
                      mr: 1
                    }}
                  />
                  Credits
                </MenuItem>
              </Menu>
            </Toolbar>
          </AppBar>
  
          <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
            {/* Sidebar */}
            <NodeSidebar
              categorizedNodes={categorizedNodes}
              handleExportFlow={handleExportFlow}
              handleImportFlow={handleImportFlow}
              handleOpenCloseAllDialog={handleOpenCloseAllDialog}
              fileInputRef={fileInputRef}
              onFileInputChange={handleFileInputChange}
            />
  
            {/* Right content: tab bar + flow editors */}
            <Box
              sx={{
                display: "flex",
                flexDirection: "column",
                flexGrow: 1,
                overflow: "hidden"
              }}
            >
              {/* Tab bar */}
              <FlowTabs
                tabs={tabs}
                activeTabId={activeTabId}
                onTabChange={handleTabChange}
                onAddTab={createNewTab}
                onTabRightClick={handleTabRightClick}
              />
  
              {/* The flow editors themselves */}
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
  
          {/* Bottom status bar */}
          <Box
            component="footer"
            sx={{
              bgcolor: "#1e1e1e",
              color: "white",
              px: 2,
              py: 1,
              display: "flex",
              alignItems: "center",
              gap: 2
            }}
          >
            <b>Nodes</b>: <span id="nodeCount">0</span>
            <Divider
              orientation="vertical"
              flexItem
              sx={{ borderColor: "#444" }}
            />
            <b>Update Rate (ms):</b>
            <input
              id="updateRateInput"
              type="number"
              min="50"
              step="50"
              defaultValue={window.BorealisUpdateRate}
              style={{
                width: "80px",
                background: "#121212",
                color: "#fff",
                border: "1px solid #444",
                borderRadius: "3px",
                padding: "3px",
                fontSize: "0.8rem"
              }}
            />
            <Button
              variant="outlined"
              size="small"
              onClick={() => {
                const val = parseInt(
                  document.getElementById("updateRateInput")?.value
                );
                if (!isNaN(val) && val >= 50) {
                  window.BorealisUpdateRate = val;
                  console.log("Global update rate set to", val + "ms");
                } else {
                  alert("Please enter a valid number (min 50).");
                }
              }}
              sx={{
                color: "#58a6ff",
                borderColor: "#58a6ff",
                fontSize: "0.75rem",
                textTransform: "none",
                px: 1.5
              }}
            >
              Apply Rate
            </Button>
          </Box>
        </Box>
  
        {/* Close All Dialog */}
        <Dialog
          open={confirmCloseOpen}
          onClose={handleCloseDialog}
          PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
        >
          <DialogTitle>Close All Flow Tabs?</DialogTitle>
          <DialogContent>
            <DialogContentText sx={{ color: "#ccc" }}>
              This will remove all existing flow tabs and create a fresh tab named Flow 1.
            </DialogContentText>
          </DialogContent>
          <DialogActions>
            <Button
              onClick={handleCloseDialog}
              sx={{ color: "#58a6ff" }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleConfirmCloseAll}
              sx={{ color: "#ff4f4f" }}
            >
              Close All
            </Button>
          </DialogActions>
        </Dialog>
  
        {/* Credits */}
        <Dialog
          open={creditsDialogOpen}
          onClose={() => setCreditsDialogOpen(false)}
          PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
        >
          <DialogTitle>Borealis Workflow Automation Tool</DialogTitle>
          <DialogContent>
            <DialogContentText sx={{ color: "#ccc" }}>
              Designed by Nicole Rappe @ Bunny Lab
            </DialogContentText>
          </DialogContent>
          <DialogActions>
            <Button
              onClick={() => setCreditsDialogOpen(false)}
              sx={{ color: "#58a6ff" }}
            >
              Close
            </Button>
          </DialogActions>
        </Dialog>
  
        {/* Tab Context Menu */}
        <Menu
          open={Boolean(tabMenuAnchor)}
          onClose={handleCloseTabMenu}
          anchorReference="anchorPosition"
          anchorPosition={
            tabMenuAnchor
              ? { top: tabMenuAnchor.y, left: tabMenuAnchor.x }
              : undefined
          }
          PaperProps={{
            sx: {
              bgcolor: "#1e1e1e",
              color: "#fff",
              fontSize: "13px"
            }
          }}
        >
          <MenuItem onClick={handleRenameTab}>Rename</MenuItem>
          <MenuItem onClick={handleCloseTab}>Close</MenuItem>
        </Menu>
  
        {/* Rename Tab Dialog */}
        <Dialog
          open={renameDialogOpen}
          onClose={() => setRenameDialogOpen(false)}
          PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
        >
          <DialogTitle>Rename Tab</DialogTitle>
          <DialogContent>
            <TextField
              autoFocus
              margin="dense"
              label="Tab Name"
              fullWidth
              variant="outlined"
              value={renameValue}
              onChange={(e) => setRenameValue(e.target.value)}
              sx={{
                "& .MuiOutlinedInput-root": {
                  backgroundColor: "#2a2a2a",
                  color: "#ccc",
                  "& fieldset": {
                    borderColor: "#444"
                  },
                  "&:hover fieldset": {
                    borderColor: "#666"
                  }
                },
                label: { color: "#aaa" },
                mt: 1
              }}
            />
          </DialogContent>
          <DialogActions>
            <Button
              onClick={() => setRenameDialogOpen(false)}
              sx={{ color: "#58a6ff" }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleRenameDialogSave}
              sx={{ color: "#58a6ff" }}
            >
              Save
            </Button>
          </DialogActions>
        </Dialog>
      </ThemeProvider>
    );
  }
  