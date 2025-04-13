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
    Accordion,
    AccordionSummary,
    AccordionDetails,
    Dialog,
    DialogTitle,
    DialogContent,
    DialogContentText,
    DialogActions,
    Divider,
    Tooltip,
    Tabs,
    Tab,
    TextField
} from "@mui/material";

// Material UI - Icons
import {
    DragIndicator as DragIndicatorIcon,
    KeyboardArrowDown as KeyboardArrowDownIcon,
    ExpandMore as ExpandMoreIcon,
    Save as SaveIcon,
    FileOpen as FileOpenIcon,
    DeleteForever as DeleteForeverIcon,
    InfoOutlined as InfoOutlinedIcon,
    Polyline as PolylineIcon,
    MergeType as MergeTypeIcon,
    People as PeopleIcon,
    Add as AddIcon
} from "@mui/icons-material";

// React Flow
import ReactFlow, {
    Background,
    addEdge,
    applyNodeChanges,
    applyEdgeChanges,
    ReactFlowProvider,
    useReactFlow
} from "reactflow";

// Styles
import "reactflow/dist/style.css";
import "./Borealis.css";

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

// Single flow editor
function FlowEditor({ nodes, edges, setNodes, setEdges, nodeTypes }) {
    const wrapperRef = useRef(null);
    const { project } = useReactFlow();
    const [contextMenu, setContextMenu] = useState(null);

    const onDrop = useCallback(
        (event) => {
            event.preventDefault();
            const type = event.dataTransfer.getData("application/reactflow");
            if (!type) return;

            const bounds = wrapperRef.current.getBoundingClientRect();
            const position = project({
                x: event.clientX - bounds.left,
                y: event.clientY - bounds.top
            });

            const id = "node-" + Date.now();

            const nodeMeta = Object.values(categorizedNodes)
                .flat()
                .find((n) => n.type === type);

            const newNode = {
                id: id,
                type: type,
                position: position,
                data: {
                    label: nodeMeta?.label || type,
                    content: nodeMeta?.content
                }
            };

            setNodes((nds) => [...nds, newNode]);
        },
        [project, setNodes]
    );

    const onDragOver = useCallback((event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
    }, []);

    const onConnect = useCallback(
        (params) =>
            setEdges((eds) =>
                addEdge(
                    {
                        ...params,
                        type: "smoothstep",
                        animated: true,
                        style: {
                            strokeDasharray: "6 3",
                            stroke: "#58a6ff"
                        }
                    },
                    eds
                )
            ),
        [setEdges]
    );

    const onNodesChange = useCallback(
        (changes) => setNodes((nds) => applyNodeChanges(changes, nds)),
        [setNodes]
    );

    const onEdgesChange = useCallback(
        (changes) => setEdges((eds) => applyEdgeChanges(changes, eds)),
        [setEdges]
    );

    const handleRightClick = (e, node) => {
        e.preventDefault();
        setContextMenu({
            mouseX: e.clientX + 2,
            mouseY: e.clientY - 6,
            nodeId: node.id
        });
    };

    const handleDisconnect = () => {
        if (contextMenu?.nodeId) {
            setEdges((eds) =>
                eds.filter(
                    (e) =>
                        e.source !== contextMenu.nodeId &&
                        e.target !== contextMenu.nodeId
                )
            );
        }
        setContextMenu(null);
    };

    const handleRemoveNode = () => {
        if (contextMenu?.nodeId) {
            setNodes((nds) =>
                nds.filter((n) => n.id !== contextMenu.nodeId)
            );
            setEdges((eds) =>
                eds.filter(
                    (e) =>
                        e.source !== contextMenu.nodeId &&
                        e.target !== contextMenu.nodeId
                )
            );
        }
        setContextMenu(null);
    };

    useEffect(() => {
        const nodeCountEl = document.getElementById("nodeCount");
        if (nodeCountEl) {
            nodeCountEl.innerText = nodes.length;
        }
    }, [nodes]);

    return (
        <div className="flow-editor-container" ref={wrapperRef}>
            <ReactFlow
                nodes={nodes}
                edges={edges}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onDrop={onDrop}
                onDragOver={onDragOver}
                onNodeContextMenu={handleRightClick}
                defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
                edgeOptions={{
                    type: "smoothstep",
                    animated: true,
                    style: {
                        strokeDasharray: "6 3",
                        stroke: "#58a6ff"
                    }
                }}
                proOptions={{ hideAttribution: true }}
            >
                <Background
                    variant="lines"
                    gap={65}
                    size={1}
                    color="rgba(255, 255, 255, 0.2)"
                />
            </ReactFlow>

            {/* Right-click node menu */}
            <Menu
                open={Boolean(contextMenu)}
                onClose={() => setContextMenu(null)}
                anchorReference="anchorPosition"
                anchorPosition={
                    contextMenu
                        ? { top: contextMenu.mouseY, left: contextMenu.mouseX }
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
                <MenuItem onClick={handleDisconnect}>
                    <PolylineIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
                    Disconnect All Edges
                </MenuItem>
                <MenuItem onClick={handleRemoveNode}>
                    <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
                    Remove Node
                </MenuItem>
            </Menu>
        </div>
    );
}

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
    const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
    const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
    const fileInputRef = useRef(null);

    const [renameDialogOpen, setRenameDialogOpen] = useState(false);
    const [renameTabId, setRenameTabId] = useState(null);
    const [renameValue, setRenameValue] = useState("");

    const [tabMenuAnchor, setTabMenuAnchor] = useState(null);
    const [tabMenuTabId, setTabMenuTabId] = useState(null);

    // Update nodes/edges in a particular tab
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

    // Close all flows
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

    // Export current tab
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

        if (window.showSaveFilePicker) {
            try {
                const fileHandle = await window.showSaveFilePicker({
                    suggestedName: "workflow.json",
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
            a.download = "workflow.json";
            a.style.display = "none";
            document.body.appendChild(a);
            a.click();
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
                        tab_name:
                            json.tab_name ||
                            "Imported Flow " + (tabs.length + 1),
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
                    tab_name:
                        json.tab_name ||
                        "Imported Flow " + (tabs.length + 1),
                    nodes: json.nodes || [],
                    edges: json.edges || []
                }
            ]);
            setActiveTabId(newId);
        } catch (err) {
            console.error("Failed to read file:", err);
        }
    };

    /**
     * Tab onChange logic:
     * If user clicks the plus “tab”, newValue = “__addtab__”.
     * Otherwise, newValue is an index: setActiveTab accordingly.
     */
    const handleTabChange = (event, newValue) => {
        if (newValue === "__addtab__") {
            // Create the new tab
            createNewTab();
        } else {
            // Normal tab index
            setActiveTabId(tabs[newValue].id);
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
                                    window.open(
                                        "https://git.bunny-lab.io/Borealis",
                                        "_blank"
                                    );
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
                            <MenuItem
                                onClick={() => {
                                    handleAboutMenuClose();
                                    setCreditsDialogOpen(true);
                                }}
                            >
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


                <Box
                    sx={{
                        display: "flex",
                        flexGrow: 1,
                        overflow: "hidden"
                    }}
                >
                    {/* Sidebar */}
                    <Box
                        sx={{
                            width: 320,
                            bgcolor: "#121212",
                            borderRight: "1px solid #333",
                            overflowY: "auto"
                        }}
                    >
                        <Accordion
                            defaultExpanded
                            square
                            disableGutters
                            sx={{
                                "&:before": { display: "none" },
                                margin: 0,
                                border: 0
                            }}
                        >
                            <AccordionSummary
                                expandIcon={<ExpandMoreIcon />}
                                sx={{
                                    backgroundColor: "#2c2c2c",
                                    minHeight: "36px",
                                    "& .MuiAccordionSummary-content": {
                                        margin: 0
                                    }
                                }}
                            >
                                <Typography
                                    align="left"
                                    sx={{
                                        fontSize: "0.9rem",
                                        color: "#0475c2"
                                    }}
                                >
                                    <b>Workflows</b>
                                </Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                                <Button
                                    fullWidth
                                    startIcon={<SaveIcon />}
                                    sx={{
                                        color: "#ccc",
                                        backgroundColor: "#232323",
                                        justifyContent: "flex-start",
                                        pl: 2,
                                        fontSize: "0.9rem",
                                        textTransform: "none",
                                        "&:hover": {
                                            backgroundColor: "#2a2a2a"
                                        }
                                    }}
                                    onClick={handleExportFlow}
                                >
                                    Export Current Flow
                                </Button>
                                <Button
                                    fullWidth
                                    startIcon={<FileOpenIcon />}
                                    sx={{
                                        color: "#ccc",
                                        backgroundColor: "#232323",
                                        justifyContent: "flex-start",
                                        pl: 2,
                                        fontSize: "0.9rem",
                                        textTransform: "none",
                                        "&:hover": {
                                            backgroundColor: "#2a2a2a"
                                        }
                                    }}
                                    onClick={handleImportFlow}
                                >
                                    Import Flow
                                </Button>
                                <Button
                                    fullWidth
                                    startIcon={<DeleteForeverIcon />}
                                    sx={{
                                        color: "#ccc",
                                        backgroundColor: "#232323",
                                        justifyContent: "flex-start",
                                        pl: 2,
                                        fontSize: "0.9rem",
                                        textTransform: "none",
                                        "&:hover": {
                                            backgroundColor: "#2a2a2a"
                                        }
                                    }}
                                    onClick={handleOpenCloseAllDialog}
                                >
                                    Close All Flow Tabs
                                </Button>
                            </AccordionDetails>
                        </Accordion>

                        <Accordion
                            defaultExpanded
                            square
                            disableGutters
                            sx={{
                                "&:before": { display: "none" },
                                margin: 0,
                                border: 0
                            }}
                        >
                            <AccordionSummary
                                expandIcon={<ExpandMoreIcon />}
                                sx={{
                                    backgroundColor: "#2c2c2c",
                                    minHeight: "36px",
                                    "& .MuiAccordionSummary-content": {
                                        margin: 0
                                    }
                                }}
                            >
                                <Typography
                                    align="left"
                                    sx={{
                                        fontSize: "0.9rem",
                                        color: "#0475c2"
                                    }}
                                >
                                    <b>Nodes</b>
                                </Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0 }}>
                                {Object.entries(categorizedNodes).map(
                                    ([category, items]) => (
                                        <Box
                                            key={category}
                                            sx={{
                                                mb: 0,
                                                bgcolor: "#232323"
                                            }}
                                        >
                                            <Divider
                                                sx={{
                                                    bgcolor: "transparent",
                                                    px: 2,
                                                    py: 0.75,
                                                    display: "flex",
                                                    justifyContent: "center",
                                                    borderColor: "#333"
                                                }}
                                                variant="fullWidth"
                                            >
                                                <Typography
                                                    variant="caption"
                                                    sx={{
                                                        color: "#888",
                                                        fontSize: "0.75rem"
                                                    }}
                                                >
                                                    {category}
                                                </Typography>
                                            </Divider>
                                            {items.map((nodeDef) => (
                                                <Tooltip
                                                    key={`${category}-${nodeDef.type}`}
                                                    title={
                                                        <span
                                                            style={{
                                                                whiteSpace:
                                                                    "pre-line",
                                                                wordWrap:
                                                                    "break-word",
                                                                maxWidth: 220
                                                            }}
                                                        >
                                                            {nodeDef.description ||
                                                                "Drag & Drop into Editor"}
                                                        </span>
                                                    }
                                                    placement="right"
                                                    arrow
                                                >
                                                    <Button
                                                        fullWidth
                                                        sx={{
                                                            color: "#ccc",
                                                            backgroundColor:
                                                                "#232323",
                                                            justifyContent:
                                                                "space-between",
                                                            pl: 2,
                                                            pr: 1,
                                                            fontSize:
                                                                "0.9rem",
                                                            textTransform:
                                                                "none",
                                                            "&:hover": {
                                                                backgroundColor:
                                                                    "#2a2a2a"
                                                            }
                                                        }}
                                                        draggable
                                                        onDragStart={(
                                                            event
                                                        ) => {
                                                            event.dataTransfer.setData(
                                                                "application/reactflow",
                                                                nodeDef.type
                                                            );
                                                            event.dataTransfer.effectAllowed =
                                                                "move";
                                                        }}
                                                        startIcon={
                                                            <DragIndicatorIcon
                                                                sx={{
                                                                    color: "#666",
                                                                    fontSize: 18
                                                                }}
                                                            />
                                                        }
                                                    >
                                                        <Box
                                                            component="span"
                                                            sx={{
                                                                flexGrow: 1,
                                                                textAlign:
                                                                    "left"
                                                            }}
                                                        >
                                                            {nodeDef.label}
                                                        </Box>
                                                        <PolylineIcon
                                                            sx={{
                                                                color: "#58a6ff",
                                                                fontSize: 18,
                                                                ml: 1
                                                            }}
                                                        />
                                                    </Button>
                                                </Tooltip>
                                            ))}
                                        </Box>
                                    )
                                )}
                            </AccordionDetails>
                        </Accordion>
                    </Box>

                    {/* Right content area: tab bar plus flow editors */}
                    <Box
                        sx={{
                            display: "flex",
                            flexDirection: "column",
                            flexGrow: 1,
                            overflow: "hidden"
                        }}
                    >
                        {/* Tab bar with special 'add tab' value */}
                        <Box
                            sx={{
                                display: "flex",
                                alignItems: "center",
                                backgroundColor: "#232323",
                                borderBottom: "1px solid #333",
                                height: "36px"
                            }}
                        >
                            <Tabs
                                value={(() => {
                                    // Return the index of the active tab,
                                    // or fallback to -1 if none
                                    const idx = tabs.findIndex(
                                        (t) => t.id === activeTabId
                                    );
                                    return idx >= 0 ? idx : 0;
                                })()}
                                onChange={handleTabChange}
                                variant="scrollable"
                                scrollButtons="auto"
                                textColor="inherit"
                                TabIndicatorProps={{
                                    style: { backgroundColor: "#58a6ff" }
                                }}
                                sx={{
                                    minHeight: "36px",
                                    height: "36px",
                                    flexGrow: 1
                                }}
                            >
                                {tabs.map((tab, index) => (
                                    <Tab
                                        key={tab.id}
                                        label={tab.tab_name}
                                        value={index}
                                        onContextMenu={(evt) =>
                                            handleTabRightClick(evt, tab.id)
                                        }
                                        sx={{
                                            minHeight: "36px",
                                            height: "36px",
                                            textTransform: "none",
                                            backgroundColor:
                                                tab.id === activeTabId
                                                    ? "#2C2C2C"
                                                    : "transparent",
                                            color: "#58a6ff"
                                        }}
                                    />
                                ))}
                                {/* The 'plus' tab has a special value to detect in onChange */}
                                <Tab
                                    icon={<AddIcon />}
                                    value="__addtab__"
                                    sx={{
                                        minHeight: "36px",
                                        height: "36px",
                                        color: "#58a6ff",
                                        textTransform: "none"
                                    }}
                                />
                            </Tabs>
                        </Box>

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
                                        display:
                                            tab.id === activeTabId
                                                ? "block"
                                                : "none"
                                    }}
                                >
                                    <ReactFlowProvider>
                                        <FlowEditor
                                            nodes={tab.nodes}
                                            edges={tab.edges}
                                            setNodes={(val) =>
                                                handleSetNodes(val, tab.id)
                                            }
                                            setEdges={(val) =>
                                                handleSetEdges(val, tab.id)
                                            }
                                            nodeTypes={nodeTypes}
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
                                document.getElementById("updateRateInput")
                                    ?.value
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
                        This will remove all existing flow tabs and
                        create a fresh tab named Flow 1.
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

            {/* Hidden file input fallback */}
            <input
                type="file"
                accept=".json,application/json"
                style={{ display: "none" }}
                ref={fileInputRef}
                onChange={handleFileInputChange}
            />
        </ThemeProvider>
    );
}
