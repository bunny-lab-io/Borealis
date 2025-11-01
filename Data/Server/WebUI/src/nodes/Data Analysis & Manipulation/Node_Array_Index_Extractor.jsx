import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Ensure Borealis shared memory exists
if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const ArrayIndexExtractorNode = ({ id, data }) => {
  const edges = useStore((state) => state.edges);
  const { setNodes } = useReactFlow();
  const [result, setResult] = useState("Line Does Not Exist");
  const valueRef = useRef(result);

  // Use config field, always 1-based for UX, fallback to 1
  const lineNumber = parseInt(data?.lineNumber, 10) || 1;

  useEffect(() => {
    let intervalId = null;
    let currentRate = window.BorealisUpdateRate;

    const runNodeLogic = () => {
      const inputEdge = edges.find((e) => e.target === id);
      if (!inputEdge) {
        valueRef.current = "Line Does Not Exist";
        setResult("Line Does Not Exist");
        window.BorealisValueBus[id] = "Line Does Not Exist";
        return;
      }

      const upstreamValue = window.BorealisValueBus[inputEdge.source];
      if (!Array.isArray(upstreamValue)) {
        valueRef.current = "Line Does Not Exist";
        setResult("Line Does Not Exist");
        window.BorealisValueBus[id] = "Line Does Not Exist";
        return;
      }

      const index = Math.max(0, lineNumber - 1); // 1-based to 0-based
      const selected = upstreamValue[index] ?? "Line Does Not Exist";

      if (selected !== valueRef.current) {
        valueRef.current = selected;
        setResult(selected);
        window.BorealisValueBus[id] = selected;
      }
    };

    intervalId = setInterval(runNodeLogic, currentRate);

    // Monitor update rate live
    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        currentRate = newRate;
        intervalId = setInterval(runNodeLogic, currentRate);
      }
    }, 300);

    return () => {
      clearInterval(intervalId);
      clearInterval(monitor);
    };
  }, [id, edges, lineNumber]);

  return (
    <div className="borealis-node">
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <div className="borealis-node-header">
        {data?.label || "Array Index Extractor"}
      </div>
      <div className="borealis-node-content" style={{ fontSize: "9px" }}>
        <div style={{ marginBottom: "6px", color: "#ccc" }}>
          Output a specific line from an upstream array.
        </div>
        <div style={{ color: "#888", marginBottom: 4 }}>
          Line Number: <b>{lineNumber}</b>
        </div>
        <label style={{ display: "block", marginBottom: "2px" }}>Output:</label>
        <input
          type="text"
          value={result}
          disabled
          style={{
            width: "100%",
            fontSize: "9px",
            background: "#2a2a2a",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            padding: "3px"
          }}
        />
      </div>
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

// ---- Node Registration Object with Sidebar Config & Markdown Docs ----
export default {
  type: "ArrayIndexExtractor",
  label: "Array Index Extractor",
  description: `
Outputs a specific line from an upstream array, such as the result of OCR multi-line extraction.

- Specify the **line number** (1 = first line)
- Outputs the value at that index if present
- If index is out of bounds, outputs "Line Does Not Exist"
`.trim(),
  content: "Output a Specific Array Index's Value",
  component: ArrayIndexExtractorNode,
  config: [
    {
      key: "lineNumber",
      label: "Line Number (1 = First Line)",
      type: "text",
      defaultValue: "1"
    }
  ],
  usage_documentation: `
### Array Index Extractor Node

This node allows you to extract a specific line or item from an upstream array value.

**Typical Use:**
- Used after OCR or any node that outputs an array of lines or items.
- Set the **Line Number** (1-based, so "1" = first line).

**Behavior:**
- If the line exists, outputs the value at that position.
- If not, outputs: \`Line Does Not Exist\`.

**Input:**
- Connect an upstream node that outputs an array (such as OCR Text Extraction).

**Sidebar Config:**
- Set the desired line number from the configuration sidebar for live updates.

---
`.trim()
};
