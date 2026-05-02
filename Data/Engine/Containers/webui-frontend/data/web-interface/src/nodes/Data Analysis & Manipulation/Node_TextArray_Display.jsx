////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/nodes/Data Analysis/Node_TextArray_Display.jsx

/**
 * Display Multi-Line Array Node
 * --------------------------------------------------
 * A node to display upstream multi-line text arrays.
 * Has one input edge on left and passthrough output on right.
 * Custom drag-resize handle for width & height adjustments.
 * Inner textarea scrolls vertically; container overflow visible.
 */
import React, { useEffect, useState, useRef, useCallback } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const TextArrayDisplayNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const containerRef = useRef(null);
  const resizingRef = useRef(false);
  const startPosRef = useRef({ x: 0, y: 0 });
  const startDimRef = useRef({ width: 0, height: 0 });

  // Initialize lines and dimensions
  const [lines, setLines] = useState(data?.lines || []);
  const linesRef = useRef(lines);
  const initW = parseInt(data?.width || "300", 10);
  const initH = parseInt(data?.height || "150", 10);
  const [dimensions, setDimensions] = useState({ width: initW, height: initH });

  // Persist dimensions to node data
  const persistDimensions = useCallback(() => {
    const w = `${Math.round(dimensions.width)}px`;
    const h = `${Math.round(dimensions.height)}px`;
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? { ...n, data: { ...n.data, width: w, height: h } }
          : n
      )
    );
  }, [dimensions, id, setNodes]);

  // Mouse handlers for custom resize
  useEffect(() => {
    const onMouseMove = (e) => {
      if (!resizingRef.current) return;
      const dx = e.clientX - startPosRef.current.x;
      const dy = e.clientY - startPosRef.current.y;
      setDimensions({
        width: Math.max(100, startDimRef.current.width + dx),
        height: Math.max(60, startDimRef.current.height + dy)
      });
    };
    const onMouseUp = () => {
      if (resizingRef.current) {
        resizingRef.current = false;
        persistDimensions();
      }
    };
    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
    return () => {
      window.removeEventListener("mousemove", onMouseMove);
      window.removeEventListener("mouseup", onMouseUp);
    };
  }, [persistDimensions]);

  // Start drag
  const onResizeMouseDown = (e) => {
    e.stopPropagation();
    resizingRef.current = true;
    startPosRef.current = { x: e.clientX, y: e.clientY };
    startDimRef.current = { ...dimensions };
  };

  // Polling for upstream data
  useEffect(() => {
    let rate = window.BorealisUpdateRate;
    const tick = () => {
      const edge = edges.find((e) => e.target === id);
      if (edge && edge.source) {
        const arr = window.BorealisValueBus[edge.source] || [];
        if (JSON.stringify(arr) !== JSON.stringify(linesRef.current)) {
          linesRef.current = arr;
          setLines(arr);
          window.BorealisValueBus[id] = arr;
          setNodes((nds) =>
            nds.map((n) =>
              n.id === id ? { ...n, data: { ...n.data, lines: arr } } : n
            )
          );
        }
      } else {
        window.BorealisValueBus[id] = linesRef.current;
      }
    };
    const iv = setInterval(tick, rate);
    const monitor = setInterval(() => {
      if (window.BorealisUpdateRate !== rate) {
        clearInterval(iv);
        clearInterval(monitor);
      }
    }, 200);
    return () => { clearInterval(iv); clearInterval(monitor); };
  }, [id, edges, setNodes]);

  return (
    <div
      ref={containerRef}
      className="borealis-node"
      style={{
        display: "flex",
        flexDirection: "column",
        width: dimensions.width,
        height: dimensions.height,
        overflow: "visible",
        position: "relative",
        boxSizing: "border-box"
      }}
    >
      {/* Connectors */}
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

      {/* Header */}
      <div className="borealis-node-header">
        {data?.label || "Display Multi-Line Array"}
      </div>

      {/* Content */}
      <div
        className="borealis-node-content"
        style={{
          flex: 1,
          padding: "4px",
          fontSize: "9px",
          color: "#ccc",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden"
        }}
      >
        <div style={{ marginBottom: "4px" }}>
          {data?.content || "Display upstream multi-line text arrays."}
        </div>
        <label style={{ marginBottom: "4px" }}>Upstream Text Data:</label>
        <textarea
          value={lines.join("\n")}
          readOnly
          style={{
            flex: 1,
            width: "100%",
            fontSize: "9px",
            background: "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            padding: "4px",
            resize: "none",
            overflowY: "auto",
            boxSizing: "border-box"
          }}
        />
      </div>

      {/* Invisible drag-resize handle */}
      <div
        onMouseDown={onResizeMouseDown}
        style={{
          position: "absolute",
          width: "20px",
          height: "20px",
          right: "-4px",
          bottom: "-4px",
          cursor: "nwse-resize",
          background: "transparent",
          zIndex: 10
        }}
      />
    </div>
  );
};

// Export node metadata
export default {
  type: "Node_TextArray_Display",
  label: "Display Multi-Line Array",
  description: "Display upstream multi-line text arrays.",
  content: "Display upstream multi-line text arrays.",
  component: TextArrayDisplayNode
};
