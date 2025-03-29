import React, { useState, useEffect, useCallback } from "react";
import ReactFlow, {
  addEdge,
  Controls,
  Background,
} from "reactflow";
import "reactflow/dist/style.css";
import "./FlowEditor.css";

const fetchNodes = async () => {
  const response = await fetch("/api/workflow");
  return response.json();
};

const saveWorkflow = async (workflow) => {
  await fetch("/api/workflow", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(workflow),
  });
};

export default function FlowEditor() {
  const [elements, setElements] = useState([]);

  useEffect(() => {
    fetchNodes().then((data) => {
      // Data should contain nodes and edges arrays
      const newElements = [...data.nodes, ...data.edges];
      setElements(newElements);
    });
  }, []);

  const onConnect = useCallback(
    (params) => {
      const newEdge = { id: `e${params.source}-${params.target}`, ...params };
      setElements((els) => [...els, newEdge]);

      // Separate nodes/edges for saving:
      const nodes = elements.filter((el) => el.type);
      const edges = elements.filter((el) => !el.type);

      saveWorkflow({
        nodes,
        edges: [...edges, newEdge],
      });
    },
    [elements]
  );

  return (
    <div className="flow-editor-container">
      <ReactFlow
        proOptions={{ hideAttribution: true }}  // Remove the React Flow watermark
        elements={elements}
        onConnect={onConnect}
      >
        <Controls />
        <Background
          variant="lines"
          gap={100}
          size={1}
          color="rgba(255, 255, 255, 0.2)" // White grid lines at 20% opacity
        />
      </ReactFlow>
    </div>
  );
}
