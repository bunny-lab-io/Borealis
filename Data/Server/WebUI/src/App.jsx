////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/App.jsx

// Core React Imports
import React, {
  useState,
  useEffect,
  useCallback,
  useRef,
  useMemo
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
  Accordion,
  AccordionSummary,
  AccordionDetails,
  List,
  ListItemButton,
  ListItemText,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  Paper,
  Tooltip
} from "@mui/material";

// Material UI - Icons
import {
  KeyboardArrowDown as KeyboardArrowDownIcon,
  InfoOutlined as InfoOutlinedIcon,
  MergeType as MergeTypeIcon,
  People as PeopleIcon,
  ExpandMore as ExpandMoreIcon,
  PlayCircle as PlayCircleIcon,
  Devices as DevicesIcon,
  AutoAwesomeMosaic as WorkflowsIcon,
  Construction as JobsIcon,
  PeopleOutline as CommunityIcon,
  FilterAlt as FilterIcon,
  Groups as GroupsIcon
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

// Websocket Functionality
import { io } from "socket.io-client";

if (!window.BorealisSocket) {
  window.BorealisSocket = io(window.location.origin, {
    transports: ["websocket"]
  });
}

if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 200;
}

const modules = import.meta.glob("./nodes/**/*.jsx", { eager: true });
const nodeTypes = {};
const categorizedNodes = {};

Object.entries(modules).forEach(([path, mod]) => {
  const comp = mod.default;
  if (!comp) return;
  const { type, component } = comp;
  if (!type || !component) return;
  const parts = path.replace("./nodes/", "").split("/");
  const category = parts[0];
  if (!categorizedNodes[category]) {
    categorizedNodes[category] = [];
  }
  categorizedNodes[category].push(comp);
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
  },
  components: {
    MuiTooltip: {
      styleOverrides: {
        tooltip: {
          backgroundColor: "#2a2a2a",
          color: "#ccc",
          fontSize: "0.75rem",
          border: "1px solid #444"
        },
        arrow: {
          color: "#2a2a2a"
        }
      }
    }
  }
});

const LOCAL_STORAGE_KEY = "borealis_persistent_state";

// ---------- Utilities ----------
function timeSince(tsSec) {
  if (!tsSec) return "unknown";
  const now = Date.now() / 1000;
  const s = Math.max(0, Math.floor(now - tsSec));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function statusFromHeartbeat(tsSec, offlineAfter = 15) {
  if (!tsSec) return "Offline";
  const now = Date.now() / 1000;
  return now - tsSec <= offlineAfter ? "Online" : "Offline";
}

// ---------- Devices Table (sortable) ----------
function DevicesTable() {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("status");
  const [order, setOrder] = useState("desc");

  const fetchAgents = useCallback(async () => {
    try {
      const res = await fetch("/api/agents");
      const data = await res.json();
      const arr = Object.values(data || {}).map((a) => ({
        hostname: a.hostname || a.agent_id || "unknown",
        status: statusFromHeartbeat(a.last_seen),
        lastSeen: a.last_seen || 0,
        os: a.agent_operating_system || a.os || "-"
      }));
      setRows(arr);
    } catch (e) {
      console.warn("Failed to load agents:", e);
      setRows([]);
    }
  }, []);

  useEffect(() => {
    fetchAgents();
    const t = setInterval(fetchAgents, 5000);
    return () => clearInterval(t);
  }, [fetchAgents]);

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const A = a[orderBy];
      const B = b[orderBy];
      if (orderBy === "lastSeen") return (A - B) * dir;
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [rows, orderBy, order]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const statusColor = (s) => (s === "Online" ? "#00d18c" : "#ff4f4f");

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
          Devices
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          Connected agents and their recent heartbeat.
        </Typography>
      </Box>
      <Table size="small" sx={{ minWidth: 680 }}>
        <TableHead>
          <TableRow>
            <TableCell sortDirection={orderBy === "status" ? order : false}>
              <TableSortLabel
                active={orderBy === "status"}
                direction={orderBy === "status" ? order : "asc"}
                onClick={() => handleSort("status")}
              >
                Status
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "hostname" ? order : false}>
              <TableSortLabel
                active={orderBy === "hostname"}
                direction={orderBy === "hostname" ? order : "asc"}
                onClick={() => handleSort("hostname")}
              >
                Hostname
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "lastSeen" ? order : false}>
              <TableSortLabel
                active={orderBy === "lastSeen"}
                direction={orderBy === "lastSeen" ? order : "asc"}
                onClick={() => handleSort("lastSeen")}
              >
                Last Heartbeat
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "os" ? order : false}>
              <TableSortLabel
                active={orderBy === "os"}
                direction={orderBy === "os" ? order : "asc"}
                onClick={() => handleSort("os")}
              >
                OS
              </TableSortLabel>
            </TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow key={i} hover>
              <TableCell>
                <span
                  style={{
                    display: "inline-block",
                    width: 10,
                    height: 10,
                    borderRadius: 10,
                    background: statusColor(r.status),
                    marginRight: 8,
                    verticalAlign: "middle"
                  }}
                />
                {r.status}
              </TableCell>
              <TableCell>{r.hostname}</TableCell>
              <TableCell>{timeSince(r.lastSeen)}</TableCell>
              <TableCell>{r.os}</TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} sx={{ color: "#888" }}>
                No agents connected.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Paper>
  );
}

// ---------- Main App ----------
export default function App() {
  const [tabs, setTabs] = useState([
    { id: "flow_1", tab_name: "Flow 1", nodes: [], edges: [] }
  ]);
  const [activeTabId, setActiveTabId] = useState("flow_1");
  const [currentPage, setCurrentPage] = useState("jobs");

  // navigation state
  const [currentPage, setCurrentPage] = useState("devices");
  const [navCollapsed, setNavCollapsed] = useState(false);
  const [expandedNav, setExpandedNav] = useState({
    devices: true,
    filters: false,
    automation: true
  });

  // dialogs / menus
  const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
  const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
  const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameTabId, setRenameTabId] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
  const [tabMenuTabId, setTabMenuTabId] = useState(null);
  const fileInputRef = useRef(null);

  // persist tabs
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

  // app bar menu
  const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
  const handleAboutMenuClose = () => setAboutAnchorEl(null);
  const openCreditsDialog = () => {
    handleAboutMenuClose();
    setCreditsDialogOpen(true);
  };

  // flow tab helpers...
  const handleOpenCloseAllDialog = () => setConfirmCloseOpen(true);
  const handleCloseDialog = () => setConfirmCloseOpen(false);
  const handleConfirmCloseAll = () => {
    setTabs([{ id: "flow_1", tab_name: "Flow 1", nodes: [], edges: [] }]);
    setActiveTabId("flow_1");
    setConfirmCloseOpen(false);
  };

  const createNewTab = () => {
    const nextIndex = tabs.length + 1;
    const newId = "flow_" + nextIndex;
    setTabs((old) => [
      ...old,
      { id: newId, tab_name: "Flow " + nextIndex, nodes: [], edges: [] }
    ]);
    setActiveTabId(newId);
    setCurrentPage("workflow-editor");
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
        tab.id === renameTabId ? { ...tab, tab_name: renameValue } : tab
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
          types: [{ description: "Workflow JSON File", accept: { "application/json": [".json"] } }]
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
          types: [{ description: "Workflow JSON File", accept: { "application/json": [".json"] } }]
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
        setCurrentPage("workflow-editor");
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
      setCurrentPage("workflow-editor");
    } catch (err) {
      console.error("Failed to read file:", err);
    }
  };

  // ---------- Main Content ----------
  const renderMainContent = () => {
    if (currentPage === "workflow-editor") {
      return (
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
    }
    if (currentPage === "devices") {
      return <DevicesTable />;
    }
    return (
      <Box sx={{ p: 2 }}>
        <Typography>Select a section from navigation.</Typography>
      </Box>
    );
  };

  // ---------- Nav helpers ----------
  const NavItem = ({ icon, label, pageKey, indent = 0, onClick }) => {
    const active = currentPage === pageKey;
    return (
      <ListItemButton
        onClick={onClick || (() => setCurrentPage(pageKey))}
        sx={{
          pl: indent ? 4 : 2,
          py: 1,
          color: "#ccc",
          position: "relative",
          bgcolor: active ? "#2a2a2a" : "transparent",
          "&:hover": { bgcolor: "#2c2c2c" }
        }}
      >
        {/* left accent when active */}
        <Box
          sx={{
            position: "absolute",
            left: 0,
            top: 0,
            bottom: 0,
            width: active ? 3 : 0,
            bgcolor: "#58a6ff",
            transition: "width 0.15s ease"
          }}
        />
        {icon && <Box sx={{ mr: 1, display: "flex", alignItems: "center" }}>{icon}</Box>}
        <ListItemText primary={label} primaryTypographyProps={{ fontSize: "0.95rem" }} />
      </ListItemButton>
    );
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

        {/* Main area with new wider nav styled like Node Sidebar */}
        <Box sx={{ display: "flex", flexGrow: 1, overflow: "hidden" }}>
          {/* Navigation Sidebar */}
          <Box
            sx={{
              width: navCollapsed ? 48 : 300,
              bgcolor: "#121212",
              borderRight: "1px solid #333",
              display: "flex",
              flexDirection: "column",
              transition: "width 0.2s ease"
            }}
          >
            <Box sx={{ flex: 1, overflowY: "auto" }}>
              {!navCollapsed && (
                <>
                  {/* Devices */}
                  <Accordion
                    expanded={expandedNav.devices}
                    onChange={(_, e) => setExpandedNav((s) => ({ ...s, devices: e }))}
                    square
                    disableGutters
                    sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
                  >
                    <AccordionSummary
                      expandIcon={<ExpandMoreIcon />}
                      sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
                    >
                      <Typography sx={{ fontSize: "0.9rem", color: "#0475c2" }}>
                        <b>Devices</b>
                      </Typography>
                    </AccordionSummary>
                    <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                      <NavItem icon={<DevicesIcon fontSize="small" />} label="Devices" pageKey="devices" />
                    </AccordionDetails>
                  </Accordion>

                  {/* Filters & Groups */}
                  <Accordion
                    expanded={expandedNav.filters}
                    onChange={(_, e) => setExpandedNav((s) => ({ ...s, filters: e }))}
                    square
                    disableGutters
                    sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
                  >
                    <AccordionSummary
                      expandIcon={<ExpandMoreIcon />}
                      sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
                    >
                      <Typography sx={{ fontSize: "0.9rem", color: "#0475c2" }}>
                        <b>Filters & Groups</b>
                      </Typography>
                    </AccordionSummary>
                    <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                      <NavItem icon={<FilterIcon fontSize="small" />} label="Filters" pageKey="filters" />
                      <NavItem icon={<GroupsIcon fontSize="small" />} label="Groups" pageKey="groups" />
                    </AccordionDetails>
                  </Accordion>

                  {/* Automation */}
                  <Accordion
                    expanded={expandedNav.automation}
                    onChange={(_, e) => setExpandedNav((s) => ({ ...s, automation: e }))}
                    square
                    disableGutters
                    sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
                  >
                    <AccordionSummary
                      expandIcon={<ExpandMoreIcon />}
                      sx={{ backgroundColor: "#2c2c2c", minHeight: "36px", "& .MuiAccordionSummary-content": { margin: 0 } }}
                    >
                      <Typography sx={{ fontSize: "0.9rem", color: "#0475c2" }}>
                        <b>Automation</b>
                      </Typography>
                    </AccordionSummary>
                    <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                      <NavItem icon={<JobsIcon fontSize="small" />} label="Jobs" pageKey="jobs" />
                      <NavItem
                        icon={<WorkflowsIcon fontSize="small" />}
                        label="Workflows"
                        pageKey="workflow-editor"
                        onClick={() => setCurrentPage("workflow-editor")}
                      />
                      <Box sx={{ px: 2, pb: 1 }}>
                        <Tooltip title="Create a new workflow tab and open the editor" arrow>
                          <Button
                            fullWidth
                            onClick={createNewTab}
                            startIcon={<PlayCircleIcon />}
                            sx={{
                              mt: 1,
                              color: "#58a6ff",
                              borderColor: "#58a6ff",
                              textTransform: "none",
                              border: "1px solid #58a6ff",
                              backgroundColor: "#1e1e1e",
                              "&:hover": { backgroundColor: "#1b1b1b" }
                            }}
                          >
                            New Workflow
                          </Button>
                        </Tooltip>
                      </Box>
                      <NavItem icon={<CommunityIcon fontSize="small" />} label="Community Nodes" pageKey="community" />
                    </AccordionDetails>
                  </Accordion>

                  {/* Hidden file input for import */}
                  <input
                    type="file"
                    accept=".json,application/json"
                    style={{ display: "none" }}
                    ref={fileInputRef}
                    onChange={handleFileInputChange}
                  />
                </>
              )}
            </Box>

            {/* Collapse/Expand bar */}
            <Box
              onClick={() => setNavCollapsed((c) => !c)}
              sx={{
                height: "36px",
                borderTop: "1px solid #333",
                cursor: "pointer",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                color: "#888",
                backgroundColor: "#121212",
                "&:hover": { backgroundColor: "#1e1e1e" }
              }}
            >
              {navCollapsed ? ">>" : "<<"}
            </Box>
          </Box>

          {/* Content */}
          <Box sx={{ flexGrow: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
            {renderMainContent()}
          </Box>
        </Box>
        <StatusBar />
      </Box>

      {/* Dialogs / Menus */}
      <CloseAllDialog open={confirmCloseOpen} onClose={handleCloseDialog} onConfirm={handleConfirmCloseAll} />
      <CreditsDialog open={creditsDialogOpen} onClose={() => setCreditsDialogOpen(false)} />
      <RenameTabDialog
        open={renameDialogOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameDialogOpen(false)}
        onSave={handleRenameDialogSave}
      />
      <TabContextMenu anchor={tabMenuAnchor} onClose={handleCloseTabMenu} onRename={handleRenameTab} onCloseTab={handleCloseTab} />
    </ThemeProvider>
  );
}
