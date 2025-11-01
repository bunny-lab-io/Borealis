////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Flow_Editor.jsx
// Import Node Configuration Sidebar and new Context Menu Sidebar
import NodeConfigurationSidebar from "./Node_Configuration_Sidebar";
import ContextMenuSidebar from "./Context_Menu_Sidebar";

import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactFlow, {
  Background,
  addEdge,
  applyNodeChanges,
  applyEdgeChanges,
  useReactFlow
} from "reactflow";

import { Menu, MenuItem, Box } from "@mui/material";
import {
  Polyline as PolylineIcon,
  DeleteForever as DeleteForeverIcon,
  Edit as EditIcon
} from "@mui/icons-material";

import "reactflow/dist/style.css";

export default function FlowEditor({
  flowId,
  nodes,
  edges,
  setNodes,
  setEdges,
  nodeTypes,
  categorizedNodes
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

  // Drag/snap helpers (untouched)
  const wrapperRef = useRef(null);
  const { project } = useReactFlow();
  const [guides, setGuides] = useState([]);
  const [activeGuides, setActiveGuides] = useState([]);
  const movingFlowSize = useRef({ width: 0, height: 0 });

  // ----- Node/Edge Definitions -----
  const selectedNode = nodes.find((n) => n.id === selectedNodeId);
  const selectedEdge = edges.find((e) => e.id === edgeSidebarEdgeId);

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
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setNodeContextMenu(null);
  };

  const handleRemoveNode = (nodeId) => {
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
    setEdges((eds) => eds.filter((e) => e.id !== edgeId));
    setEdgeContextMenu(null);
  };

  const handleEditEdgeProps = (edgeId) => {
    setEdgeSidebarEdgeId(edgeId);
    setEdgeSidebarOpen(true);
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
      eds.map((e) => (e.id === updatedEdgeObj.id ? { ...e, ...updatedEdgeObj } : e))
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

  }, [project, setNodes, categorizedNodes]);

  const onDragOver = useCallback((event) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const onConnect = useCallback((params) => {
    setEdges((eds) =>
      addEdge({
        ...params,
        type: "bezier",
        animated: true,
        style: { strokeDasharray: "6 3", stroke: "#58a6ff" }
      }, eds)
    );
  }, [setEdges]);

  const onNodesChange = useCallback((changes) => {
    setNodes((nds) => applyNodeChanges(changes, nds));
  }, [setNodes]);

  const onEdgesChange = useCallback((changes) => {
    setEdges((eds) => applyEdgeChanges(changes, eds));
  }, [setEdges]);

  useEffect(() => {
    const nodeCountEl = document.getElementById("nodeCount");
    if (nodeCountEl) nodeCountEl.innerText = nodes.length;
  }, [nodes]);

  const nodeDef = selectedNode
    ? Object.values(categorizedNodes).flat().find((def) => def.type === selectedNode.type)
    : null;

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
        title={selectedNode ? selectedNode.data?.label || selectedNode.id : ""}
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
      />

      {/* Edge Properties Sidebar */}
      <ContextMenuSidebar
        open={edgeSidebarOpen}
        onClose={handleCloseEdgeSidebar}
        edge={selectedEdge ? { ...selectedEdge } : null}
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
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onDrop={onDrop}
        onDragOver={onDragOver}
        onNodeContextMenu={handleRightClick}
        onEdgeContextMenu={handleEdgeRightClick}
        defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
        edgeOptions={{ type: "bezier", animated: true, style: { strokeDasharray: "6 3", stroke: "#58a6ff" } }}
        proOptions={{ hideAttribution: true }}
        onNodeDragStart={(_, node) => computeGuides(node)}
        onNodeDrag={onNodeDrag}
        onNodeDragStop={() => { setGuides([]); setActiveGuides([]); }}
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
          Edit Properties
        </MenuItem>
        <MenuItem onClick={() => handleDisconnectAllEdges(nodeContextMenu.nodeId)}>
          <PolylineIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
          Disconnect All Edges
        </MenuItem>
        <MenuItem onClick={() => handleRemoveNode(nodeContextMenu.nodeId)}>
          <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
          Remove Node
        </MenuItem>
      </Menu>

      {/* Edge Context Menu */}
      <Menu
        open={Boolean(edgeContextMenu)}
        onClose={() => setEdgeContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={edgeContextMenu ? { top: edgeContextMenu.mouseY, left: edgeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={() => handleEditEdgeProps(edgeContextMenu.edgeId)}>
          <EditIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
          Edit Properties
        </MenuItem>
        <MenuItem onClick={() => handleUnlinkEdge(edgeContextMenu.edgeId)}>
          <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
          Unlink Edge
        </MenuItem>
      </Menu>
    </div>
  );
}
