////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Analysis & Manipulation/Node_JSON_Display.jsx

import React, { useEffect, useState, useRef, useCallback } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
// For syntax highlighting, ensure prismjs is installed: npm install prismjs
import Prism from "prismjs";
import "prismjs/components/prism-json";
import "prismjs/themes/prism-okaidia.css";

const JSONPrettyDisplayNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const containerRef = useRef(null);
  const resizingRef = useRef(false);
  const startPosRef = useRef({ x: 0, y: 0 });
  const startDimRef = useRef({ width: 0, height: 0 });

  const [jsonData, setJsonData] = useState(data?.jsonData || {});
  const initW = parseInt(data?.width || "300", 10);
  const initH = parseInt(data?.height || "150", 10);
  const [dimensions, setDimensions] = useState({ width: initW, height: initH });
  const jsonRef = useRef(jsonData);

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

  const onResizeMouseDown = (e) => {
    e.stopPropagation();
    resizingRef.current = true;
    startPosRef.current = { x: e.clientX, y: e.clientY };
    startDimRef.current = { ...dimensions };
  };

  useEffect(() => {
    let rate = window.BorealisUpdateRate;
    const tick = () => {
      const edge = edges.find((e) => e.target === id);
      if (edge && edge.source) {
        const upstream = window.BorealisValueBus[edge.source];
        if (typeof upstream === "object") {
          if (JSON.stringify(upstream) !== JSON.stringify(jsonRef.current)) {
            jsonRef.current = upstream;
            setJsonData(upstream);
            window.BorealisValueBus[id] = upstream;
            setNodes((nds) =>
              nds.map((n) =>
                n.id === id ? { ...n, data: { ...n.data, jsonData: upstream } } : n
              )
            );
          }
        }
      } else {
        window.BorealisValueBus[id] = jsonRef.current;
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

  // Generate highlighted HTML
  const pretty = JSON.stringify(jsonData, null, 2);
  const highlighted = Prism.highlight(pretty, Prism.languages.json, "json");

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
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

      <div className="borealis-node-header">Display JSON Data</div>
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
          Display prettified JSON from upstream.
        </div>
        <div
          style={{
            flex: 1,
            width: "100%",
            background: "#1e1e1e",
            border: "1px solid #444",
            borderRadius: "2px",
            padding: "4px",
            overflowY: "auto",
            fontFamily: "monospace",
            fontSize: "9px"
          }}
        >
          <pre
            dangerouslySetInnerHTML={{ __html: highlighted }}
            style={{ margin: 0 }}
          />
        </div>
      </div>

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

export default {
  type: "Node_JSON_Pretty_Display",
  label: "Display JSON Data",
  description: "Display upstream JSON object as prettified JSON with syntax highlighting.",
  content: "Display prettified multi-line JSON from upstream node.",
  component: JSONPrettyDisplayNode
};
