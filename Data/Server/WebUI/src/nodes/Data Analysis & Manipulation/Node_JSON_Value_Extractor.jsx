////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Data Analysis & Manipulation/Node_JSON_Value_Extractor.jsx

import React, { useState, useEffect } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

const JSONValueExtractorNode = ({ id, data }) => {
  const { setNodes, getEdges } = useReactFlow();
  const [keyName, setKeyName] = useState(data?.keyName || "");
  const [value, setValue] = useState(data?.result || "");

  const handleKeyChange = (e) => {
    const newKey = e.target.value;
    setKeyName(newKey);
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? { ...n, data: { ...n.data, keyName: newKey } }
          : n
      )
    );
  };

  useEffect(() => {
    let currentRate = window.BorealisUpdateRate;
    let intervalId;

    const runNodeLogic = () => {
      const edges = getEdges();
      const incoming = edges.filter((e) => e.target === id);
      const sourceId = incoming[0]?.source;
      let newValue = "Key Not Found";

      if (sourceId && window.BorealisValueBus[sourceId] !== undefined) {
        let upstream = window.BorealisValueBus[sourceId];
        if (upstream && typeof upstream === "object" && keyName) {
          const pathSegments = keyName.split(".");
          let nodeVal = upstream;
          for (let segment of pathSegments) {
            if (
              nodeVal != null &&
              (typeof nodeVal === "object" || Array.isArray(nodeVal)) &&
              segment in nodeVal
            ) {
              nodeVal = nodeVal[segment];
            } else {
              nodeVal = undefined;
              break;
            }
          }
          if (nodeVal !== undefined) {
            newValue = String(nodeVal);
          }
        }
      }

      if (newValue !== value) {
        setValue(newValue);
        window.BorealisValueBus[id] = newValue;
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id
              ? { ...n, data: { ...n.data, result: newValue } }
              : n
          )
        );
      }
    };

    runNodeLogic();
    intervalId = setInterval(runNodeLogic, currentRate);

    const monitor = setInterval(() => {
      const newRate = window.BorealisUpdateRate;
      if (newRate !== currentRate) {
        clearInterval(intervalId);
        currentRate = newRate;
        intervalId = setInterval(runNodeLogic, currentRate);
      }
    }, 250);

    return () => {
      clearInterval(intervalId);
      clearInterval(monitor);
    };
  }, [keyName, id, setNodes, getEdges, value]);

  return (
    <div className="borealis-node">
      <div className="borealis-node-header">JSON Value Extractor</div>
      <div className="borealis-node-content">
        <label style={{ fontSize: "9px", display: "block", marginBottom: "4px" }}>
          Key:
        </label>
        <input
          type="text"
          value={keyName}
          onChange={handleKeyChange}
          placeholder="e.g. name.en"
          style={{
            fontSize: "9px", padding: "4px", width: "100%",
            background: "#1e1e1e", color: "#ccc",
            border: "1px solid #444", borderRadius: "2px"
          }}
        />
        <label style={{ fontSize: "9px", display: "block", margin: "8px 0 4px" }}>
          Value:
        </label>
        <textarea
          readOnly
          value={value}
          rows={2}
          style={{
            fontSize: "9px", padding: "4px", width: "100%",
            background: "#1e1e1e", color: "#ccc",
            border: "1px solid #444", borderRadius: "2px",
            resize: "none"
          }}
        />
      </div>
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />
    </div>
  );
};

export default {
  type: "JSON_Value_Extractor",
  label: "JSON Value Extractor",
  description: "Extract a nested value by dot-delimited path from upstream JSON data.",
  content: "Provide a dot-separated key path (e.g. 'name.en'); outputs the extracted string or 'Key Not Found'.",
  component: JSONValueExtractorNode
};
