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
    DialogActions
} from "@mui/material";

import KeyboardArrowDownIcon from "@mui/icons-material/KeyboardArrowDown";
import ExpandMoreIcon from "@mui/icons-material/ExpandMore";

import ReactFlow, {
    Background,
    addEdge,
    applyNodeChanges,
    applyEdgeChanges,
    ReactFlowProvider,
    useReactFlow,
    Handle,
    Position
} from "reactflow";
import "reactflow/dist/style.css";
import "./Borealis.css";

// ✅ Custom styled node with handles
const CustomNode = ({ data }) => {
    return (
        <div style={{
            background: "#2c2c2c",
            border: "1px solid #3a3a3a",
            borderRadius: "6px",
            color: "#ccc",
            fontSize: "12px",
            minWidth: "160px",
            maxWidth: "260px",
            boxShadow: "0 0 10px rgba(0,0,0,0.2)",
            position: "relative"
        }}>
            <Handle
                type="target"
                position={Position.Left}
                style={{ background: "#58a6ff", width: 10, height: 10 }}
            />
            <Handle
                type="source"
                position={Position.Right}
                style={{ background: "#58a6ff", width: 10, height: 10 }}
            />
            <div style={{
                background: "#232323",
                padding: "6px 10px",
                borderTopLeftRadius: "6px",
                borderTopRightRadius: "6px",
                fontWeight: "bold",
                fontSize: "13px"
            }}>
                {data.label || "Custom Node"}
            </div>
            <div style={{ padding: "10px" }}>
                {data.content || "Placeholder"}
            </div>
        </div>
    );
};

// FlowEditor with drag-and-drop and updated behavior
function FlowEditor({ nodes, edges, setNodes, setEdges }) {
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
            const newNode = {
                id,
                type: "custom",
                position,
                data: {
                    label: "Custom Node",
                    content: "Placeholder"
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
                nodeTypes={{ custom: CustomNode }}
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

    const handleAboutMenuOpen = (event) => setAboutAnchorEl(event.currentTarget);
    const handleAboutMenuClose = () => setAboutAnchorEl(null);

    const handleOpenCloseDialog = () => setConfirmCloseOpen(true);
    const handleCloseDialog = () => setConfirmCloseOpen(false);
    const handleConfirmCloseWorkflow = () => {
        setNodes([]);
        setEdges([]);
        setConfirmCloseOpen(false);
    };

    const handleAddTestNode = () => {
        const id = `test-node-${Date.now()}`;
        const newNode = {
            id,
            type: "custom",
            data: {
                label: "Custom Node",
                content: "Placeholder"
            },
            position: {
                x: 250 + Math.random() * 300,
                y: 150 + Math.random() * 200
            }
        };
        setNodes((nds) => [...nds, newNode]);
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
                    >
                        About
                    </Button>
                    <Menu
                        anchorEl={aboutAnchorEl}
                        open={Boolean(aboutAnchorEl)}
                        onClose={handleAboutMenuClose}
                    >
                        <MenuItem onClick={handleAboutMenuClose}>Gitea Project</MenuItem>
                        <MenuItem onClick={handleAboutMenuClose}>Credits</MenuItem>
                    </Menu>
                </Toolbar>
            </AppBar>

            <Box display="flex" flexDirection="column" height="calc(100vh - 64px)">
                <Box display="flex" flexGrow={1} minHeight={0}>
                    <Box
                        sx={{
                            width: 240,
                            bgcolor: "#121212",
                            borderRight: "1px solid #333",
                            overflowY: "auto"
                        }}
                    >
                        <Accordion defaultExpanded square disableGutters sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}>
                            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={accordionHeaderStyle}>
                                <Typography align="left" sx={{ fontSize: "0.9rem", color: "#0475c2" }}><b>Workflows</b></Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0 }}>
                                <Button fullWidth sx={sidebarBtnStyle}>SAVE WORKFLOW</Button>
                                <Button fullWidth sx={sidebarBtnStyle}>OPEN WORKFLOW</Button>
                                <Button
                                    fullWidth
                                    sx={sidebarBtnStyle}
                                    onClick={handleOpenCloseDialog}
                                >
                                    CLOSE WORKFLOW
                                </Button>
                            </AccordionDetails>
                        </Accordion>

                        <Accordion square disableGutters sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}>
                            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={accordionHeaderStyle}>
                                <Typography align="left" sx={{ fontSize: "0.9rem", color: "#0475c2" }}><b>Nodes</b></Typography>
                            </AccordionSummary>
                            <AccordionDetails sx={{ p: 0 }}>
                                <Button
                                    fullWidth
                                    sx={sidebarBtnStyle}
                                    draggable
                                    onDragStart={(event) => {
                                        event.dataTransfer.setData("application/reactflow", "testNode");
                                        event.dataTransfer.effectAllowed = "move";
                                    }}
                                >
                                    TEST NODE
                                </Button>
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
                            />
                        </ReactFlowProvider>
                    </Box>
                </Box>

                <Box component="footer" sx={{
                    bgcolor: "#1e1e1e",
                    color: "white",
                    px: 2,
                    py: 1,
                    textAlign: "left"
                }}>
                    <b>Nodes</b>: <span id="nodeCount">0</span> | <b>Update Rate</b>: 500ms
                </Box>
            </Box>

            {/* Confirmation Dialog */}
            <Dialog
                open={confirmCloseOpen}
                onClose={handleCloseDialog}
                PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff" } }}
            >
                <DialogTitle>Close Workflow?</DialogTitle>
                <DialogContent>
                    <DialogContentText sx={{ color: "#ccc" }}>
                        Are you sure you want to reset the workflow? All nodes will be removed.
                    </DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button onClick={handleCloseDialog} sx={{ color: "#58a6ff" }}>
                        Cancel
                    </Button>
                    <Button onClick={handleConfirmCloseWorkflow} sx={{ color: "#ff4f4f" }}>
                        Close Workflow
                    </Button>
                </DialogActions>
            </Dialog>
        </ThemeProvider>
    );
}

const sidebarBtnStyle = {
    color: "#ccc",
    backgroundColor: "#232323",
    justifyContent: "flex-start",
    pl: 2,
    fontSize: "0.9rem"
};

const accordionHeaderStyle = {
    backgroundColor: "#2c2c2c",
    minHeight: "36px",
    "& .MuiAccordionSummary-content": { margin: 0 }
};
