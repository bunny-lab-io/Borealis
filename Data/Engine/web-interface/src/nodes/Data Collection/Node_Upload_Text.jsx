////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data/Node_Upload_Text.jsx

/**
 * Upload Text File Node
 * --------------------------------------------------
 * A node to upload a text file (TXT/LOG/INI/ETC) and store it as a multi-line text array.
 * No input edges. Outputs an array of text lines via the shared value bus.
 */
import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const UploadTextFileNode = ({ id, data }) => {
  const { setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);

  // Initialize lines from persisted data or empty
  const initialLines = data?.lines || [];
  const [lines, setLines] = useState(initialLines);
  const linesRef = useRef(initialLines);

  const fileInputRef = useRef(null);

  // Handle file selection and reading
  const handleFileChange = (e) => {
    const file = e.target.files && e.target.files[0];
    if (!file) return;
    file.text().then((text) => {
      const arr = text.split(/\r\n|\n/);
      linesRef.current = arr;
      setLines(arr);
      // Broadcast to shared bus
      window.BorealisValueBus[id] = arr;
      // Persist data for workflow serialization
      setNodes((nds) =>
        nds.map((n) =>
          n.id === id
            ? { ...n, data: { ...n.data, lines: arr } }
            : n
        )
      );
    });
  };

  // Trigger file input click
  const handleUploadClick = () => {
    fileInputRef.current?.click();
  };

  // Periodically broadcast current lines to bus
  useEffect(() => {
    let currentRate = window.BorealisUpdateRate;
    const intervalId = setInterval(() => {
      window.BorealisValueBus[id] = linesRef.current;
    }, currentRate);

    // Monitor for rate changes
    const monitorId = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        clearInterval(monitorId);
      }
    }, 250);

    return () => {
      clearInterval(intervalId);
      clearInterval(monitorId);
    };
  }, [id]);

  return (
    <div className="borealis-node">
      {/* No input handle for this node */}
      <div className="borealis-node-header">
        {data?.label || "Upload Text File"}
      </div>
      <div className="borealis-node-content">
        <div style={{ marginBottom: "8px", fontSize: "9px", color: "#ccc" }}>
          {data?.content ||
            "Upload a text-based file, output a multi-line string array."}
        </div>
        <button
          onClick={handleUploadClick}
          style={{
            width: "100%",
            padding: "6px",
            fontSize: "9px",
            background: "#1e1e1e",
            color: "#ccc",
            border: "1px solid #444",
            borderRadius: "2px",
            cursor: "pointer"
          }}
        >
          Select File...
        </button>
        <input
          type="file"
          accept=".txt,.log,.ini,text/*"
          style={{ display: "none" }}
          ref={fileInputRef}
          onChange={handleFileChange}
        />
      </div>

      {/* Output connector on right */}
      <Handle
        type="source"
        position={Position.Right}
        className="borealis-handle"
      />
    </div>
  );
};

// Export node metadata for Borealis
export default {
  type: "Upload_Text_File",
  label: "Upload Text File",
  description: "A node to upload a text file (TXT/LOG/INI/ETC) and store it as a multi-line text array.",
  content: "Upload a text-based file, output a multi-line string array.",
  component: UploadTextFileNode
};
