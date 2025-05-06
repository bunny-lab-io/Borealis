// //////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Flow_Editor.jsx

import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactFlow, {
  Background,
  addEdge,
  applyNodeChanges,
  applyEdgeChanges,
  useReactFlow
} from "reactflow";
import { Menu, MenuItem, MenuList, Slider, Box } from "@mui/material";
import {
  Polyline as PolylineIcon,
  DeleteForever as DeleteForeverIcon,
  DoubleArrow as DoubleArrowIcon,
  LinearScale as LinearScaleIcon,
  Timeline as TimelineIcon,
  FormatColorFill as FormatColorFillIcon,
  ArrowRight as ArrowRightIcon,
  Edit as EditIcon
} from "@mui/icons-material";
import { SketchPicker } from "react-color";
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
  const wrapperRef = useRef(null);
  const { project } = useReactFlow();

  const [contextMenu, setContextMenu] = useState(null);
  const [edgeContextMenu, setEdgeContextMenu] = useState(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState(null);
  const [showColorPicker, setShowColorPicker] = useState(false);
  const [colorPickerMode, setColorPickerMode] = useState(null);
  const [labelPadding, setLabelPadding] = useState([8, 4]);
  const [labelBorderRadius, setLabelBorderRadius] = useState(4);
  const [labelOpacity, setLabelOpacity] = useState(0.8);
  const [tempColor, setTempColor] = useState({ hex: "#58a6ff" });
  const [pickerPos, setPickerPos] = useState({ x: 0, y: 0 });

  // helper-line state
  // guides: array of { xFlow, xPx } or { yFlow, yPx } for stationary nodes
  const [guides, setGuides] = useState([]);
  // activeGuides: array of { xPx } or { yPx } to draw
  const [activeGuides, setActiveGuides] = useState([]);

  // store moving node flow-size on drag start
  const movingFlowSize = useRef({ width: 0, height: 0 });

  const edgeStyles = {
    step: "step",
    curved: "bezier",
    straight: "straight"
  };

  const animationStyles = {
    dashes: { animated: true, style: { strokeDasharray: "6 3" } },
    dots:   { animated: true, style: { strokeDasharray: "2 4" } },
    none:   { animated: false, style: {} }
  };

  // Compute edge-only guides and capture moving node flow-size
  const computeGuides = useCallback((dragNode) => {
    if (!wrapperRef.current) return;
    const parentRect = wrapperRef.current.getBoundingClientRect();

    // measure moving node in pixel space
    const dragEl = wrapperRef.current.querySelector(
      `.react-flow__node[data-id="${dragNode.id}"]`
    );
    if (dragEl) {
      const dr = dragEl.getBoundingClientRect();
      const relLeft   = dr.left   - parentRect.left;
      const relTop    = dr.top    - parentRect.top;
      const relRight  = relLeft   + dr.width;
      const relBottom = relTop    + dr.height;

      // project pixel corners to flow coords
      const pTL = project({ x: relLeft,    y: relTop    });
      const pTR = project({ x: relRight,   y: relTop    });
      const pBL = project({ x: relLeft,    y: relBottom });

      movingFlowSize.current = {
        width:  pTR.x - pTL.x,
        height: pBL.y - pTL.y
      };
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

      // project pixel to flow coords
      const pTL = project({ x: relLeft,  y: relTop    });
      const pTR = project({ x: relRight, y: relTop    });
      const pBL = project({ x: relLeft,  y: relBottom });

      // vertical guides: left edge, right edge
      lines.push({ xFlow: pTL.x, xPx: relLeft });
      lines.push({ xFlow: pTR.x, xPx: relRight });

      // horizontal guides: top edge, bottom edge
      lines.push({ yFlow: pTL.y, yPx: relTop });
      lines.push({ yFlow: pBL.y, yPx: relBottom });
    });
    setGuides(lines);
  }, [nodes, project]);

  // Snap & show only matching guides within threshold during drag
  const onNodeDrag = useCallback((_, node) => {
    const threshold = 5;
    let snapX = null, snapY = null;
    const show = [];
    const { width: fw, height: fh } = movingFlowSize.current;

    guides.forEach((ln) => {
      if (ln.xFlow != null) {
        // moving left edge to stationary edges
        if (Math.abs(node.position.x - ln.xFlow) < threshold) {
          snapX = ln.xFlow;
          show.push({ xPx: ln.xPx });
        }
        // moving right edge to stationary edges
        else if (Math.abs(node.position.x + fw - ln.xFlow) < threshold) {
          snapX = ln.xFlow - fw;
          show.push({ xPx: ln.xPx });
        }
      }
      if (ln.yFlow != null) {
        // moving top edge
        if (Math.abs(node.position.y - ln.yFlow) < threshold) {
          snapY = ln.yFlow;
          show.push({ yPx: ln.yPx });
        }
        // moving bottom edge
        else if (Math.abs(node.position.y + fh - ln.yFlow) < threshold) {
          snapY = ln.yFlow - fh;
          show.push({ yPx: ln.yPx });
        }
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
    const newNode = {
      id,
      type,
      position,
      data: {
        label: nodeMeta?.label || type,
        content: nodeMeta?.content
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

  const handleRightClick = (e, node) => {
    e.preventDefault();
    setContextMenu({ mouseX: e.clientX + 2, mouseY: e.clientY - 6, nodeId: node.id });
  };

  const handleEdgeRightClick = (e, edge) => {
    e.preventDefault();
    setEdgeContextMenu({ edgeId: edge.id, mouseX: e.clientX + 2, mouseY: e.clientY - 6 });
    setSelectedEdgeId(edge.id);
  };

  const changeEdgeType = (newType) => {
    setEdges((eds) => eds.map((e) =>
      e.id === selectedEdgeId ? { ...e, type: edgeStyles[newType] } : e
    ));
    setEdgeContextMenu(null);
  };

  const changeEdgeAnimation = (newAnim) => {
    setEdges((eds) => eds.map((e) => {
      if (e.id !== selectedEdgeId) return e;
      const strokeColor = e.style?.stroke || "#58a6ff";
      const anim = animationStyles[newAnim] || {};
      return {
        ...e,
        animated: anim.animated,
        style: { ...anim.style, stroke: strokeColor },
        markerEnd: e.markerEnd ? { ...e.markerEnd, color: strokeColor } : undefined
      };
    }));
    setEdgeContextMenu(null);
  };

  const handleColorChange = (color) => {
    setEdges((eds) => eds.map((e) => {
      if (e.id !== selectedEdgeId) return e;
      const updated = { ...e };
      if (colorPickerMode === "stroke") {
        updated.style = { ...e.style, stroke: color.hex };
        if (e.markerEnd) updated.markerEnd = { ...e.markerEnd, color: color.hex };
      } else if (colorPickerMode === "labelText") {
        updated.labelStyle = { ...e.labelStyle, fill: color.hex };
      } else if (colorPickerMode === "labelBg") {
        updated.labelBgStyle = { ...e.labelBgStyle, fill: color.hex, fillOpacity: labelOpacity };
      }
      return updated;
    }));
  };

  const handleAddLabel = () => {
    setEdges((eds) => eds.map((e) =>
      e.id === selectedEdgeId ? { ...e, label: "New Label" } : e
    ));
    setEdgeContextMenu(null);
  };

  const handleEditLabel = () => {
    const newText = prompt("Enter label text:");
    if (newText !== null) {
      setEdges((eds) => eds.map((e) =>
        e.id === selectedEdgeId ? { ...e, label: newText } : e
      ));
    }
    setEdgeContextMenu(null);
  };

  const handleRemoveLabel = () => {
    setEdges((eds) => eds.map((e) =>
      e.id === selectedEdgeId ? { ...e, label: undefined } : e
    ));
    setEdgeContextMenu(null);
  };

  const handlePickColor = (mode) => {
    setColorPickerMode(mode);
    setTempColor({ hex: "#58a6ff" });
    setPickerPos({ x: edgeContextMenu?.mouseX || 0, y: edgeContextMenu?.mouseY || 0 });
    setShowColorPicker(true);
  };

  const applyLabelStyleExtras = () => {
    setEdges((eds) => eds.map((e) =>
      e.id === selectedEdgeId
        ? {
            ...e,
            labelBgPadding: labelPadding,
            labelBgStyle: {
              ...e.labelBgStyle,
              fillOpacity: labelOpacity,
              rx: labelBorderRadius,
              ry: labelBorderRadius
            }
          }
        : e
    ));
    setEdgeContextMenu(null);
  };

  useEffect(() => {
    const nodeCountEl = document.getElementById("nodeCount");
    if (nodeCountEl) nodeCountEl.innerText = nodes.length;
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

      {/* helper lines */}
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

      <Menu
        open={Boolean(contextMenu)}
        onClose={() => setContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={contextMenu ? { top: contextMenu.mouseY, left: contextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={() => {
          if (contextMenu?.nodeId) {
            setEdges((eds) => eds.filter((e) =>
              e.source !== contextMenu.nodeId && e.target !== contextMenu.nodeId
            ));
          }
          setContextMenu(null);
        }}>
          <PolylineIcon sx={{ fontSize: 18, color: "#58a6ff", mr: 1 }} />
          Disconnect All Edges
        </MenuItem>
        <MenuItem onClick={() => {
          if (contextMenu?.nodeId) {
            setNodes((nds) => nds.filter((n) => n.id !== contextMenu.nodeId));
            setEdges((eds) => eds.filter((e) =>
              e.source !== contextMenu.nodeId && e.target !== contextMenu.nodeId
            ));
          }
          setContextMenu(null);
        }}>
          <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
          Remove Node
        </MenuItem>
      </Menu>

      <Menu
        open={Boolean(edgeContextMenu)}
        onClose={() => setEdgeContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={edgeContextMenu ? { top: edgeContextMenu.mouseY, left: edgeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={() => setEdges((eds) => eds.filter((e) => e.id !== selectedEdgeId))}>
          <DeleteForeverIcon sx={{ fontSize: 18, color: "#ff4f4f", mr: 1 }} />
          Unlink Edge
        </MenuItem>
        <MenuItem>
          Edge Styles
          <MenuList>
            <MenuItem onClick={() => changeEdgeType("step")}>Step</MenuItem>
            <MenuItem onClick={() => changeEdgeType("curved")}>Curved</MenuItem>
            <MenuItem onClick={() => changeEdgeType("straight")}>Straight</MenuItem>
          </MenuList>
        </MenuItem>
        <MenuItem>
          Animations
          <MenuList>
            <MenuItem onClick={() => changeEdgeAnimation("dashes")}>Dashes</MenuItem>
            <MenuItem onClick={() => changeEdgeAnimation("dots")}>Dots</MenuItem>
            <MenuItem onClick={() => changeEdgeAnimation("none")}>Solid Line</MenuItem>
          </MenuList>
        </MenuItem>
        <MenuItem>
          Label
          <MenuList>
            <MenuItem onClick={handleAddLabel}>Add</MenuItem>
            <MenuItem onClick={handleRemoveLabel}>Remove</MenuItem>
            <MenuItem onClick={handleEditLabel}>
              <EditIcon sx={{ fontSize: 16, mr: 1 }} />
              Edit
            </MenuItem>
            <MenuItem onClick={() => handlePickColor("labelText")}>Text Color</MenuItem>
            <MenuItem onClick={() => handlePickColor("labelBg")}>Background Color</MenuItem>
            <MenuItem>
              Padding:
              <input
                type="text"
                defaultValue={`${labelPadding[0]},${labelPadding[1]}`}
                style={{ width: 80, marginLeft: 8 }}
                onBlur={(e) => {
                  const parts = e.target.value.split(",").map((v) => parseInt(v.trim()));
                  if (parts.length === 2 && parts.every(Number.isFinite)) setLabelPadding(parts);
                }}
              />
            </MenuItem>
            <MenuItem>
              Radius:
              <input
                type="number"
                min="0"
                max="20"
                defaultValue={labelBorderRadius}
                style={{ width: 60, marginLeft: 8 }}
                onBlur={(e) => {
                  const val = parseInt(e.target.value);
                  if (!isNaN(val)) setLabelBorderRadius(val);
                }}
              />
            </MenuItem>
            <MenuItem>
              Opacity:
              <Box display="flex" alignItems="center" ml={1}>
                <Slider
                  value={labelOpacity}
                  onChange={(_, v) => setLabelOpacity(v)}
                  step={0.05}
                  min={0}
                  max={1}
                  style={{ width: 100 }}
                />
                <input
                  type="number"
                  step="0.05"
                  min="0"
                  max="1"
                  value={labelOpacity}
                  style={{ width: 60, marginLeft: 8 }}
                  onChange={(e) => {
                    const v = parseFloat(e.target.value);
                    if (!isNaN(v)) setLabelOpacity(v);
                  }}
                />
              </Box>
            </MenuItem>
            <MenuItem onClick={applyLabelStyleExtras}>Apply Label Style Changes</MenuItem>
          </MenuList>
        </MenuItem>
        <MenuItem onClick={() => handlePickColor("stroke")}>Color</MenuItem>
      </Menu>

      {showColorPicker && (
        <div
          style={{
            position: "absolute",
            top: pickerPos.y,
            left: pickerPos.x,
            zIndex: 9999,
            background: "#1e1e1e",
            padding: "10px",
            borderRadius: "8px"
          }}
        >
          <SketchPicker color={tempColor.hex} onChange={(c) => setTempColor(c)} />
          <div style={{ marginTop: "10px", textAlign: "center" }}>
            <button
              onClick={() => {
                handleColorChange(tempColor);
                setShowColorPicker(false);
              }}
              style={{
                backgroundColor: "#58a6ff",
                color: "#121212",
                border: "none",
                padding: "6px 12px",
                borderRadius: "4px",
                cursor: "pointer",
                fontWeight: "bold"
              }}
            >
              Set Color
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
