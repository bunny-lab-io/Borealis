////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Flow_Editor.jsx
// Import Node Configuration Sidebar and new Context Menu Sidebar
import NodeConfigurationSidebar from "./Node_Configuration_Sidebar";
import ContextMenuSidebar from "./Context_Menu_Sidebar";
import {
  decorateWorkflowEdge,
  getWorkflowEdgePortMetadata,
  getWorkflowRuntimeDisplayLabel,
  WORKFLOW_RUNTIME_EDGE_ROUTES,
} from "./runtimeV1";

import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactFlow, {
  Background,
  addEdge,
  applyNodeChanges,
  applyEdgeChanges,
  useReactFlow
} from "reactflow";

import { Menu, MenuItem, Box, ListItemText } from "@mui/material";
import {
  Polyline as PolylineIcon,
  DeleteForever as DeleteForeverIcon,
  Edit as EditIcon,
  NavigateNext as NavigateNextIcon,
} from "@mui/icons-material";

import "reactflow/dist/style.css";

export default function FlowEditor({
  flowId,
  nodes,
  edges,
  setNodes,
  setEdges,
  nodeTypes,
  categorizedNodes,
  readOnly = false,
  nodeRunLookup = {},
  onSelectedNodeChange = null,
}) {
  // Node Configuration Sidebar State
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selectedNodeId, setSelectedNodeId] = useState(null);

  // Edge Properties Sidebar State
  const [edgeSidebarOpen, setEdgeSidebarOpen] = useState(false);
  const [edgeSidebarEdgeId, setEdgeSidebarEdgeId] = useState(null);

  // Context Menus
  const [nodeContextMenu, setNodeContextMenu] = useState(null); // { mouseX, mouseY, nodeId }
  const [edgeContextMenu, setEdgeContextMenu] = useState(null); // { mouseX, mouseY, edgeId }
  const [edgeRouteMenu, setEdgeRouteMenu] = useState(null); // { anchorEl, edgeId }

  // Drag/snap helpers (untouched)
  const wrapperRef = useRef(null);
  const { project } = useReactFlow();
  const [guides, setGuides] = useState([]);
  const [activeGuides, setActiveGuides] = useState([]);
  const movingFlowSize = useRef({ width: 0, height: 0 });

  // ----- Node/Edge Definitions -----
  const selectedNode = nodes.find((n) => n.id === selectedNodeId);
  const selectedEdge = edges.find((e) => e.id === edgeSidebarEdgeId);
  const selectedNodeRun = selectedNodeId ? nodeRunLookup?.[selectedNodeId] || null : null;
  const selectedNodeTitle = selectedNode
    ? getWorkflowRuntimeDisplayLabel(selectedNode.type, selectedNode.data?.label || selectedNode.id)
    : "";
  const nodesById = React.useMemo(
    () =>
      (Array.isArray(nodes) ? nodes : []).reduce((acc, node) => {
        if (node?.id) acc[String(node.id)] = node;
        return acc;
      }, {}),
    [nodes]
  );
  const contextMenuEdge = edgeContextMenu?.edgeId
    ? edges.find((edge) => edge.id === edgeContextMenu.edgeId) || null
    : null;
  const contextMenuEdgePorts = contextMenuEdge
    ? getWorkflowEdgePortMetadata(contextMenuEdge, nodesById)
    : {};

  // --------- Context Menu Handlers ----------
  const handleRightClick = (e, node) => {
    e.preventDefault();
    setNodeContextMenu({ mouseX: e.clientX + 2, mouseY: e.clientY - 6, nodeId: node.id });
  };

  const handleEdgeRightClick = (e, edge) => {
    e.preventDefault();
    setEdgeContextMenu({ mouseX: e.clientX + 2, mouseY: e.clientY - 6, edgeId: edge.id });
  };

  // --------- Node Context Menu Actions ---------
  const handleDisconnectAllEdges = (nodeId) => {
    if (readOnly) return;
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setNodeContextMenu(null);
  };

  const handleRemoveNode = (nodeId) => {
    if (readOnly) return;
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setNodeContextMenu(null);
  };

  const handleEditNodeProps = (nodeId) => {
    setSelectedNodeId(nodeId);
    setDrawerOpen(true);
    setNodeContextMenu(null);
  };

  // --------- Edge Context Menu Actions ---------
  const handleUnlinkEdge = (edgeId) => {
    if (readOnly) return;
    setEdges((eds) => eds.filter((e) => e.id !== edgeId));
    setEdgeContextMenu(null);
  };

  const handleEditEdgeProps = (edgeId) => {
    setEdgeSidebarEdgeId(edgeId);
    setEdgeSidebarOpen(true);
    setEdgeContextMenu(null);
  };

  const handleOpenEdgeRouteMenu = (event, edgeId) => {
    if (readOnly) return;
    event.stopPropagation();
    setEdgeRouteMenu({ anchorEl: event.currentTarget, edgeId });
  };

  const handleCloseEdgeRouteMenu = () => {
    setEdgeRouteMenu(null);
  };

  const handleQuickSetEdgeRoute = (edgeId, route) => {
    if (readOnly || !edgeId) return;
    setEdges((eds) =>
      eds.map((edge) =>
        edge.id === edgeId
          ? decorateWorkflowEdge(
              {
                ...edge,
                data: {
                  ...(edge.data || {}),
                  route_on: route,
                },
              },
              { nodesById }
            )
          : edge
      )
    );
    setEdgeRouteMenu(null);
    setEdgeContextMenu(null);
  };

  // ----- Sidebar Closing -----
  const handleCloseNodeSidebar = () => {
    setDrawerOpen(false);
    setSelectedNodeId(null);
  };

  const handleCloseEdgeSidebar = () => {
    setEdgeSidebarOpen(false);
    setEdgeSidebarEdgeId(null);
  };

  // ----- Update Edge Callback for Sidebar -----
  const updateEdge = (updatedEdgeObj) => {
    setEdges((eds) =>
      eds.map((e) =>
        e.id === updatedEdgeObj.id
          ? decorateWorkflowEdge({ ...e, ...updatedEdgeObj }, { nodesById })
          : e
      )
    );
  };

  // ----- Drag/Drop, Guides, Node Snap Logic (unchanged) -----
  const computeGuides = useCallback((dragNode) => {
    if (!wrapperRef.current) return;
    const parentRect = wrapperRef.current.getBoundingClientRect();
    const dragEl = wrapperRef.current.querySelector(
      `.react-flow__node[data-id="${dragNode.id}"]`
    );
    if (dragEl) {
      const dr = dragEl.getBoundingClientRect();
      const relLeft   = dr.left   - parentRect.left;
      const relTop    = dr.top    - parentRect.top;
      const relRight  = relLeft   + dr.width;
      const relBottom = relTop    + dr.height;
      const pTL = project({ x: relLeft,    y: relTop    });
      const pTR = project({ x: relRight,   y: relTop    });
      const pBL = project({ x: relLeft,    y: relBottom });
      movingFlowSize.current = { width:  pTR.x - pTL.x, height: pBL.y - pTL.y };
    }
    const lines = [];
    nodes.forEach((n) => {
      if (n.id === dragNode.id) return;
      const el = wrapperRef.current.querySelector(
        `.react-flow__node[data-id="${n.id}"]`
      );
      if (!el) return;
      const r = el.getBoundingClientRect();
      const relLeft   = r.left   - parentRect.left;
      const relTop    = r.top    - parentRect.top;
      const relRight  = relLeft + r.width;
      const relBottom = relTop  + r.height;
      const pTL = project({ x: relLeft,  y: relTop    });
      const pTR = project({ x: relRight, y: relTop    });
      const pBL = project({ x: relLeft,  y: relBottom });
      lines.push({ xFlow: pTL.x, xPx: relLeft });
      lines.push({ xFlow: pTR.x, xPx: relRight });
      lines.push({ yFlow: pTL.y, yPx: relTop });
      lines.push({ yFlow: pBL.y, yPx: relBottom });
    });
    setGuides(lines);
  }, [nodes, project]);

  const onNodeDrag = useCallback((_, node) => {
    const threshold = 5;
    let snapX = null, snapY = null;
    const show = [];
    const { width: fw, height: fh } = movingFlowSize.current;
    guides.forEach((ln) => {
      if (ln.xFlow != null) {
        if (Math.abs(node.position.x - ln.xFlow) < threshold) { snapX = ln.xFlow; show.push({ xPx: ln.xPx }); }
        else if (Math.abs(node.position.x + fw - ln.xFlow) < threshold) { snapX = ln.xFlow - fw; show.push({ xPx: ln.xPx }); }
      }
      if (ln.yFlow != null) {
        if (Math.abs(node.position.y - ln.yFlow) < threshold) { snapY = ln.yFlow; show.push({ yPx: ln.yPx }); }
        else if (Math.abs(node.position.y + fh - ln.yFlow) < threshold) { snapY = ln.yFlow - fh; show.push({ yPx: ln.yPx }); }
      }
    });
    if (snapX !== null || snapY !== null) {
      setNodes((nds) =>
        applyNodeChanges(
          [{
            id: node.id,
            type: "position",
            position: {
              x: snapX !== null ? snapX : node.position.x,
              y: snapY !== null ? snapY : node.position.y
            }
          }],
          nds
        )
      );
      setActiveGuides(show);
    } else {
      setActiveGuides([]);
    }
  }, [guides, setNodes]);

  const onDrop = useCallback((event) => {
    if (readOnly) return;
    event.preventDefault();
    const type = event.dataTransfer.getData("application/reactflow");
    if (!type) return;
    const bounds = wrapperRef.current.getBoundingClientRect();
    const position = project({
      x: event.clientX - bounds.left,
      y: event.clientY - bounds.top
    });
    const id = "node-" + Date.now();
    const nodeMeta = Object.values(categorizedNodes).flat().find((n) => n.type === type);
    // Seed config defaults:
    const configDefaults = {};
    (nodeMeta?.config || []).forEach(cfg => {
      if (cfg.defaultValue !== undefined) {
        configDefaults[cfg.key] = cfg.defaultValue;
      }
    });
    const newNode = {
      id,
      type,
      position,
      data: {
        label: nodeMeta?.label || type,
        content: nodeMeta?.content,
        ...configDefaults
      },
      dragHandle: ".borealis-node-header"
    };
    setNodes((nds) => [...nds, newNode]);

  }, [project, setNodes, categorizedNodes, readOnly]);

  const onDragOver = useCallback((event) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const onConnect = useCallback((params) => {
    if (readOnly) return;
    setEdges((eds) =>
      addEdge(
        decorateWorkflowEdge(
          {
            ...params,
            type: "bezier",
            data: { route_on: "always" },
          },
          { nodesById }
        ),
        eds
      )
    );
  }, [setEdges, readOnly, nodesById]);

  const onNodesChange = useCallback((changes) => {
    if (readOnly) return;
    setNodes((nds) => applyNodeChanges(changes, nds));
  }, [setNodes, readOnly]);

  const onEdgesChange = useCallback((changes) => {
    if (readOnly) return;
    setEdges((eds) => applyEdgeChanges(changes, eds));
  }, [setEdges, readOnly]);

  useEffect(() => {
    const nodeCountEl = document.getElementById("nodeCount");
    if (nodeCountEl) nodeCountEl.innerText = nodes.length;
  }, [nodes]);

  const nodeDef = selectedNode
    ? Object.values(categorizedNodes).flat().find((def) => def.type === selectedNode.type)
    : null;

  useEffect(() => {
    if (typeof onSelectedNodeChange === "function") {
      onSelectedNodeChange(selectedNodeId, selectedNodeRun);
    }
  }, [onSelectedNodeChange, selectedNodeId, selectedNodeRun]);

  // --------- MAIN RENDER ----------
  return (
    <div
      className="flow-editor-container"
      ref={wrapperRef}
      style={{ position: "relative" }}
    >
      {/* Node Config Sidebar */}
      <NodeConfigurationSidebar
        drawerOpen={drawerOpen}
        setDrawerOpen={setDrawerOpen}
        title={selectedNodeTitle}
        nodeData={
          selectedNode && nodeDef
            ? {
                config: nodeDef.config,
                usage_documentation: nodeDef.usage_documentation,
                ...selectedNode.data,
                nodeId: selectedNode.id
              }
            : null
        }
        setNodes={setNodes}
        selectedNode={selectedNode}
        readOnly={readOnly}
        runNodeRecord={selectedNodeRun}
      />

      {/* Edge Properties Sidebar */}
      <ContextMenuSidebar
        open={edgeSidebarOpen}
        onClose={handleCloseEdgeSidebar}
        edge={selectedEdge ? { ...selectedEdge } : null}
        edgePortMetadata={selectedEdge ? getWorkflowEdgePortMetadata(selectedEdge, nodesById) : null}
        updateEdge={edge => {
          // Provide id if missing
          if (!edge.id && edgeSidebarEdgeId) edge.id = edgeSidebarEdgeId;
          updateEdge(edge);
        }}
      />

      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={readOnly ? undefined : onNodesChange}
        onEdgesChange={readOnly ? undefined : onEdgesChange}
        onConnect={readOnly ? undefined : onConnect}
        onDrop={readOnly ? undefined : onDrop}
        onDragOver={onDragOver}
        onNodeContextMenu={handleRightClick}
        onEdgeContextMenu={handleEdgeRightClick}
        onNodeClick={(_, node) => {
          setSelectedNodeId(node.id);
          setDrawerOpen(true);
        }}
        defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
        edgeOptions={{ type: "bezier", animated: true, style: { strokeDasharray: "6 3", stroke: "#58a6ff" } }}
        onNodeDragStart={readOnly ? undefined : (_, node) => computeGuides(node)}
        onNodeDrag={readOnly ? undefined : onNodeDrag}
        onNodeDragStop={readOnly ? undefined : () => { setGuides([]); setActiveGuides([]); }}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        elementsSelectable
        zoomOnDoubleClick={!readOnly}
        panOnDrag
        deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
      >
        <Background id={flowId} variant="lines" gap={65} size={1} color="rgba(255,255,255,0.2)" />
      </ReactFlow>

      {/* Helper lines for snapping */}
      {activeGuides.map((ln, i) =>
        ln.xPx != null ? (
          <div
            key={i}
            className="helper-line helper-line-vertical"
            style={{ left: ln.xPx + "px", top: 0 }}
          />
        ) : (
          <div
            key={i}
            className="helper-line helper-line-horizontal"
            style={{ top: ln.yPx + "px", left: 0 }}
          />
        )
      )}

      {/* Node Context Menu */}
      <Menu
        open={Boolean(nodeContextMenu)}
        onClose={() => setNodeContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={nodeContextMenu ? { top: nodeContextMenu.mouseY, left: nodeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={() => handleEditNodeProps(nodeContextMenu.nodeId)}>
          <EditIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
          {readOnly ? "View Details" : "Edit Properties"}
        </MenuItem>
        {!readOnly ? (
          <MenuItem onClick={() => handleDisconnectAllEdges(nodeContextMenu.nodeId)}>
            <PolylineIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
            Disconnect All Edges
          </MenuItem>
        ) : null}
        {!readOnly ? (
          <MenuItem onClick={() => handleRemoveNode(nodeContextMenu.nodeId)}>
            <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
            Remove Node
          </MenuItem>
        ) : null}
      </Menu>

      {/* Edge Context Menu */}
      <Menu
        open={Boolean(edgeContextMenu)}
        onClose={() => {
          setEdgeContextMenu(null);
          setEdgeRouteMenu(null);
        }}
        anchorReference="anchorPosition"
        anchorPosition={edgeContextMenu ? { top: edgeContextMenu.mouseY, left: edgeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        {!readOnly ? (
          <MenuItem onClick={() => handleEditEdgeProps(edgeContextMenu.edgeId)}>
            <EditIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
            Edit Properties
          </MenuItem>
        ) : null}
        {!readOnly && contextMenuEdgePorts?.supportsRouteSelection ? (
          <MenuItem onClick={(event) => handleOpenEdgeRouteMenu(event, edgeContextMenu.edgeId)}>
            <ListItemText primary="Flow Control" />
            <NavigateNextIcon sx={{ fontSize: 18, color: "#7dd3fc", ml: 1 }} />
          </MenuItem>
        ) : null}
        {!readOnly ? (
          <MenuItem onClick={() => handleUnlinkEdge(edgeContextMenu.edgeId)}>
            <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
            Unlink Edge
          </MenuItem>
        ) : null}
      </Menu>

      <Menu
        open={Boolean(edgeRouteMenu)}
        anchorEl={edgeRouteMenu?.anchorEl || null}
        onClose={handleCloseEdgeRouteMenu}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
        anchorOrigin={{ vertical: "top", horizontal: "right" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
      >
        {WORKFLOW_RUNTIME_EDGE_ROUTES.map((route) => (
          <MenuItem
            key={route.value}
            onClick={() => handleQuickSetEdgeRoute(edgeRouteMenu?.edgeId, route.value)}
          >
            <Box
              sx={{
                width: 10,
                height: 10,
                borderRadius: "50%",
                bgcolor: route.color,
                mr: 1.15,
                boxShadow: `0 0 0 1px ${route.color}55`,
              }}
            />
            {route.label}
          </MenuItem>
        ))}
      </Menu>
    </div>
  );
}
