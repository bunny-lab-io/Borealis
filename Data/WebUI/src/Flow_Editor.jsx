////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Flow_Editor.jsx

import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactFlow, {
  Background,
  addEdge,
  applyNodeChanges,
  applyEdgeChanges,
  useReactFlow
} from "reactflow";
import { Menu, MenuItem } from "@mui/material";
import {
  Polyline as PolylineIcon,
  DeleteForever as DeleteForeverIcon
} from "@mui/icons-material";

import "reactflow/dist/style.css";
import "./Borealis.css";

/**
 * Single flow editor component.
 * 
 * Props:
 * - nodes
 * - edges
 * - setNodes
 * - setEdges
 * - nodeTypes
 * - categorizedNodes (used to find node meta info on drop)
 */
export default function FlowEditor({
  nodes,
  edges,
  setNodes,
  setEdges,
  nodeTypes,
  categorizedNodes
}) {
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

      // Find node definition in the categorizedNodes
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
    [project, setNodes, categorizedNodes]
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
      setNodes((nds) => nds.filter((n) => n.id !== contextMenu.nodeId));
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
