import React, { useState, useEffect, useCallback, useRef } from "react";
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
    Divider
} from "@mui/material";

import DragIndicatorIcon from "@mui/icons-material/DragIndicator";
import Tooltip from "@mui/material/Tooltip";
import KeyboardArrowDownIcon from "@mui/icons-material/KeyboardArrowDown";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

// Icons for Sidebar Workflow Buttons
import SaveIcon from "@mui/icons-material/Save";
import FileOpenIcon from "@mui/icons-material/FileOpen";
import DeleteForeverIcon from "@mui/icons-material/DeleteForever";

// Info Icon for About Menu
import InfoOutlinedIcon from "@mui/icons-material/InfoOutlined";

// Node Icon (Left-Side of Each Node in Sidebar Menu)
import PolylineIcon from "@mui/icons-material/Polyline";

// Gitea Project Icon
import MergeTypeIcon from "@mui/icons-material/MergeType";

// Credits Icon
import PeopleIcon from "@mui/icons-material/People";

import ReactFlow, {
    Background,
    addEdge,
    applyNodeChanges,
    applyEdgeChanges,
    ReactFlowProvider,
    useReactFlow
} from "reactflow";
import "reactflow/dist/style.css";
import "./Borealis.css";

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
    categorizedNodes[category].push(mod.default); // includes type, label, component, defaultContent
    nodeTypes[type] = component;
});

function FlowEditor({ nodes, edges, setNodes, setEdges, nodeTypes }) {
    const reactFlowWrapper = useRef(null);
    const { project } = useReactFlow();

    const onDrop = useCallback(
        (event) => {
            event.preventDefault();
            const type = event.dataTransfer.getData("application/reactflow");
            if (!type) return;

            const bounds = reactFlowWrapper.current.getBoundingClientRect();
            const position = project({
                x: event.clientX - bounds.left,
                y: event.clientY - bounds.top
            });

            const id = `node-${Date.now()}`;

            const nodeMeta = Object.values(categorizedNodes)
                .flat()
                .find((n) => n.type === type);

            const newNode = {
                id,
                type,
                position,
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

    useEffect(() => {
        const nodeCountEl = document.getElementById("nodeCount");
        if (nodeCountEl) {
            nodeCountEl.innerText = nodes.length;
        }
    }, [nodes]);

    return (
        <div className="flow-editor-container" ref={reactFlowWrapper}>
            <ReactFlow
                proOptions={{ hideAttribution: true }}
                nodes={nodes}
                edges={edges}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onDrop={onDrop}
                onDragOver={onDragOver}
                defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
                edgeOptions={{
                    type: "smoothstep",
                    animated: true,
                    style: {
                        strokeDasharray: "6 3",
                        stroke: "#58a6ff"
                    }
                }}
            >
                <Background
                    variant="lines"
                    gap={65}
                    size={1}
                    color="rgba(255, 255, 255, 0.2)"
                />
            </ReactFlow>
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
    const [aboutAnchorEl, setAboutAnchorEl] = useState(null);
    const [nodes, setNodes] = useState([]);
    const [edges, setEdges] = useState([]);
    const [confirmCloseOpen, setConfirmCloseOpen] = useState(false);
    const [creditsDialogOpen, setCreditsDialogOpen] = useState(false);
    const fileInputRef = useRef(null);

    const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
    const handleAboutMenuClose = () => setAboutAnchorEl(null);
    const handleOpenCloseDialog = () => setConfirmCloseOpen(true);
    const handleCloseDialog = () => setConfirmCloseOpen(false);
    const handleConfirmCloseWorkflow = () => {
        setNodes([]);
        setEdges([]);
        setConfirmCloseOpen(false);
    };

    const handleSaveWorkflow = async () => {
        const data = JSON.stringify({ nodes, edges }, null, 2);
        const blob = new Blob([data], { type: "application/json" });

        if (window.showSaveFilePicker) {
            try {
                const fileHandle = await window.showSaveFilePicker({
                    suggestedName: "workflow.json",
                    types: [{
                        description: "Workflow JSON File",
                        accept: { "application/json": [".json"] }
                    }]
                });
                const writable = await fileHandle.createWritable();
                await writable.write(blob);
                await writable.close();
            } catch (error) {
                console.error("Save cancelled or failed:", error);
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

    const handleOpenWorkflow = async () => {
        if (window.showOpenFilePicker) {
            try {
                const [fileHandle] = await window.showOpenFilePicker({
                    types: [{
                        description: "Workflow JSON File",
                        accept: { "application/json": [".json"] }
                    }]
                });
                const file = await fileHandle.getFile();
                const text = await file.text();
                const { nodes: loadedNodes, edges: loadedEdges } = JSON.parse(text);
                const confirm = window.confirm("Opening a workflow will overwrite your current one. Continue?");
                if (!confirm) return;
                setNodes(loadedNodes);
                setEdges(loadedEdges);
            } catch (error) {
                console.error("Open cancelled or failed:", error);
            }
        } else {
            fileInputRef.current?.click();
        }
    };

    return (
        <ThemeProvider theme={darkTheme}>
            <CssBaseline />
            <AppBar position="static" sx={{ bgcolor: "#092c44" }}>
                <Toolbar>
                    <Typography variant="h6" sx={{ flexGrow: 1 }}>
                        Borealis - Workflow Automation Tool
                    </Typography>
                    <Button
                        color="inherit"
                        onClick={handleAboutMenuOpen}
                        endIcon={<KeyboardArrowDownIcon />}
                        startIcon={<InfoOutlinedIcon />}
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
                            <MergeTypeIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
                            Gitea Project
                        </MenuItem>
                        <MenuItem
                            onClick={() => {
                                handleAboutMenuClose();
                                setCreditsDialogOpen(true);
                            }}
                        >
                            <PeopleIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
                            Credits
                        </MenuItem>
                    </Menu>
                </Toolbar>
            </AppBar>

            <Box display="flex" flexDirection="column" height="calc(100vh - 64px)">
                <Box display="flex" flexGrow={1} minHeight={0}>
                    <Box sx={{ width: 320, bgcolor: "#121212", borderRight: "1px solid #333", overflowY: "auto" }}>
                        <Accordion defaultExpanded square disableGutters sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}>
                            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={accordionHeaderStyle}>
                                <Typography align="left" sx={{ fontSize: "0.9rem", color: "#0475c2" }}><b>Workflows</b></Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                                <Button fullWidth startIcon={<SaveIcon />} sx={sidebarBtnStyle} onClick={handleSaveWorkflow}>
                                    SAVE WORKFLOW
                                </Button>
                                <Button fullWidth startIcon={<FileOpenIcon />} sx={sidebarBtnStyle} onClick={handleOpenWorkflow}>
                                    OPEN WORKFLOW
                                </Button>
                                <Button fullWidth startIcon={<DeleteForeverIcon />} sx={sidebarBtnStyle} onClick={handleOpenCloseDialog}>
                                    CLOSE WORKFLOW
                                </Button>
                            </AccordionDetails>
                        </Accordion>

                        <Accordion defaultExpanded square disableGutters sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}>
                            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={accordionHeaderStyle}>
                                <Typography align="left" sx={{ fontSize: "0.9rem", color: "#0475c2" }}><b>Nodes</b></Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0 }}>
                                {Object.entries(categorizedNodes).map(([category, items]) => (
                                    <Box key={category} sx={{ mb: 0, bgcolor: "#232323" }}>
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
                                        {items.map(({ type, label, description }) => (
                                            <Tooltip
                                                key={`${category}-${type}`}
                                                title={
                                                    <span style={{ whiteSpace: "pre-line", wordWrap: "break-word", maxWidth: 220 }}>
                                                        {description || "Drag & Drop into Editor"}
                                                    </span>
                                                }
                                                placement="right"
                                                arrow
                                            >
                                                <Button
                                                    fullWidth
                                                    sx={{
                                                        ...sidebarBtnStyle,
                                                        justifyContent: "space-between", // ensures spacing between left icon/label and right icon
                                                        pr: 1 // optional: tighter padding on right
                                                    }}
                                                    draggable
                                                    onDragStart={(event) => {
                                                        event.dataTransfer.setData("application/reactflow", type);
                                                        event.dataTransfer.effectAllowed = "move";
                                                    }}
                                                    startIcon={<DragIndicatorIcon sx={{ color: "#666", fontSize: 18 }} />}
                                                >
                                                    <Box component="span" sx={{ flexGrow: 1, textAlign: "left" }}>{label}</Box>
                                                    <PolylineIcon sx={{ color: "#58a6ff", fontSize: 18, ml: 1 }} />
                                                </Button>
                                            </Tooltip>
                                        ))}
                                    </Box>
                                ))}
                            </AccordionDetails>
                        </Accordion>
                    </Box>

                    <Box flexGrow={1} overflow="hidden">
                        <ReactFlowProvider>
                            <FlowEditor
                                nodes={nodes}
                                edges={edges}
                                setNodes={setNodes}
                                setEdges={setEdges}
                                nodeTypes={nodeTypes}
                            />
                        </ReactFlowProvider>
                    </Box>
                </Box>

                <Box component="footer" sx={{ bgcolor: "#1e1e1e", color: "white", px: 2, py: 1, textAlign: "left" }}>
                    <b>Nodes</b>: <span id="nodeCount">0</span> | <b>Update Rate</b>: 500ms
                </Box>
            </Box>

            <Dialog open={confirmCloseOpen} onClose={handleCloseDialog} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
                <DialogTitle>Close Workflow?</DialogTitle>
                <DialogContent>
                    <DialogContentText sx={{ color: "#ccc" }}>
                        Are you sure you want to reset the workflow? All nodes will be removed.
                    </DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button onClick={handleCloseDialog} sx={{ color: "#58a6ff" }}>Cancel</Button>
                    <Button onClick={handleConfirmCloseWorkflow} sx={{ color: "#ff4f4f" }}>Close Workflow</Button>
                </DialogActions>
            </Dialog>

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
                    <Button onClick={() => setCreditsDialogOpen(false)} sx={{ color: "#58a6ff" }}>
                        Close
                    </Button>
                </DialogActions>
            </Dialog>

            <input
                type="file"
                accept=".json,application/json"
                style={{ display: "none" }}
                ref={fileInputRef}
                onChange={async (e) => {
                    const file = e.target.files[0];
                    if (!file) return;
                    try {
                        const text = await file.text();
                        const { nodes: loadedNodes, edges: loadedEdges } = JSON.parse(text);
                        const confirm = window.confirm("Opening a workflow will overwrite your current one. Continue?");
                        if (!confirm) return;
                        setNodes(loadedNodes);
                        setEdges(loadedEdges);
                    } catch (err) {
                        console.error("Failed to read file:", err);
                    }
                }}
            />
        </ThemeProvider>
    );
}

const sidebarBtnStyle = {
    color: "#ccc",
    backgroundColor: "#232323",
    justifyContent: "flex-start",
    pl: 2,
    fontSize: "0.9rem",
    textTransform: "none",
    "&:hover": {
        backgroundColor: "#2a2a2a"
    }
};

const accordionHeaderStyle = {
    backgroundColor: "#2c2c2c",
    minHeight: "36px",
    "& .MuiAccordionSummary-content": { margin: 0 }
};
