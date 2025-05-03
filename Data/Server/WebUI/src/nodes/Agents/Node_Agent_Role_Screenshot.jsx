////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent_Role_Screenshot.jsx

import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import ShareIcon from "@mui/icons-material/Share";
import IconButton from "@mui/material/IconButton";

if (!window.BorealisValueBus) {
  window.BorealisValueBus = {};
}

if (!window.BorealisUpdateRate) {
  window.BorealisUpdateRate = 100;
}

const ScreenshotInstructionNode = ({ id, data }) => {
  const { setNodes, getNodes } = useReactFlow();
  const edges = useStore(state => state.edges);

  const [interval, setInterval] = useState(data?.interval || 1000);
  const [region, setRegion] = useState({
    x: data?.x ?? 250,
    y: data?.y ?? 100,
    w: data?.w ?? 300,
    h: data?.h ?? 200,
  });
  const [visible, setVisible] = useState(data?.visible ?? true);
  const [alias, setAlias] = useState(data?.alias || "");
  const [imageBase64, setImageBase64] = useState("");

  const base64Ref = useRef("");
  const regionRef = useRef(region);

  // Push current state into BorealisValueBus at intervals
  useEffect(() => {
    const intervalId = setInterval(() => {
      const val = base64Ref.current;
      if (val) {
        window.BorealisValueBus[id] = val;
        setNodes(nds =>
          nds.map(n =>
            n.id === id ? { ...n, data: { ...n.data, value: val } } : n
          )
        );
      }
    }, window.BorealisUpdateRate || 100);
    return () => clearInterval(intervalId);
  }, [id, setNodes]);

  // Listen for agent screenshot + overlay updates
  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket) return;

    const handleScreenshot = (payload) => {
      if (payload?.node_id !== id || !payload.image_base64) return;

      base64Ref.current = payload.image_base64;
      setImageBase64(payload.image_base64);
      window.BorealisValueBus[id] = payload.image_base64;

      // If geometry changed from agent side, sync into UI
      const { x, y, w, h } = payload;
      if (x !== undefined && y !== undefined && w !== undefined && h !== undefined) {
        const newRegion = { x, y, w, h };
        const prev = regionRef.current;
        const changed = Object.entries(newRegion).some(([k, v]) => prev[k] !== v);

        if (changed) {
          regionRef.current = newRegion;
          setRegion(newRegion);
          setNodes(nds =>
            nds.map(n =>
              n.id === id ? { ...n, data: { ...n.data, ...newRegion } } : n
            )
          );
        }
      }
    };

    socket.on("agent_screenshot_task", handleScreenshot);
    return () => socket.off("agent_screenshot_task", handleScreenshot);
  }, [id, setNodes]);

  // Bi-directional instruction export
  window.__BorealisInstructionNodes = window.__BorealisInstructionNodes || {};
  window.__BorealisInstructionNodes[id] = () => ({
    node_id: id,
    role: "screenshot",
    interval,
    visible,
    alias,
    ...regionRef.current
  });

  // Manual live view copy
  const handleCopyLiveViewLink = () => {
    const agentEdge = edges.find(e => e.target === id && e.sourceHandle === "provisioner");
    const agentNode = getNodes().find(n => n.id === agentEdge?.source);
    const selectedAgentId = agentNode?.data?.agent_id;

    if (!selectedAgentId) {
      alert("No valid agent connection found.");
      return;
    }

    const liveUrl = `${window.location.origin}/api/agent/${selectedAgentId}/node/${id}/screenshot/live`;
    navigator.clipboard.writeText(liveUrl)
      .then(() => console.log(`[Clipboard] Live View URL copied: ${liveUrl}`))
      .catch(err => console.error("Clipboard copy failed:", err));
  };

  return (
    <div className="borealis-node" style={{ position: "relative" }}>
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

      <div className="borealis-node-header">Agent Role: Screenshot</div>
      <div className="borealis-node-content" style={{ fontSize: "9px" }}>
        <label>Update Interval (ms):</label>
        <input
          type="number"
          min="100"
          step="100"
          value={interval}
          onChange={(e) => setInterval(Number(e.target.value))}
          style={{ width: "100%", marginBottom: "4px" }}
        />

        <label>Region X / Y / W / H:</label>
        <div style={{ display: "flex", gap: "4px", marginBottom: "4px" }}>
          <input type="number" value={region.x} onChange={(e) => {
            const x = Number(e.target.value);
            const updated = { ...region, x }; setRegion(updated); regionRef.current = updated;
          }} style={{ width: "25%" }} />
          <input type="number" value={region.y} onChange={(e) => {
            const y = Number(e.target.value);
            const updated = { ...region, y }; setRegion(updated); regionRef.current = updated;
          }} style={{ width: "25%" }} />
          <input type="number" value={region.w} onChange={(e) => {
            const w = Number(e.target.value);
            const updated = { ...region, w }; setRegion(updated); regionRef.current = updated;
          }} style={{ width: "25%" }} />
          <input type="number" value={region.h} onChange={(e) => {
            const h = Number(e.target.value);
            const updated = { ...region, h }; setRegion(updated); regionRef.current = updated;
          }} style={{ width: "25%" }} />
        </div>

        <div style={{ marginBottom: "4px" }}>
          <label>
            <input
              type="checkbox"
              checked={visible}
              onChange={() => setVisible(!visible)}
              style={{ marginRight: "4px" }}
            />
            Show Overlay on Agent
          </label>
        </div>

        <label>Overlay Label:</label>
        <input
          type="text"
          value={alias}
          onChange={(e) => setAlias(e.target.value)}
          placeholder="Label (optional)"
          style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
        />

        <div style={{ textAlign: "center", fontSize: "8px", color: "#aaa" }}>
          {imageBase64
            ? `Last image: ${Math.round(imageBase64.length / 1024)} KB`
            : "Awaiting Screenshot Data..."}
        </div>
      </div>

      <div style={{ position: "absolute", top: 4, right: 4 }}>
        <IconButton size="small" onClick={handleCopyLiveViewLink}>
          <ShareIcon style={{ fontSize: 14 }} />
        </IconButton>
      </div>
    </div>
  );
};

export default {
  type: "Agent_Role_Screenshot",
  label: "Agent Role: Screenshot",
  description: `
Agent Role Node: Screenshot Region

- Defines a single region capture role
- Allows custom update interval and overlay
- Emits captured base64 PNG data from agent
`.trim(),
  content: "Capture screenshot region via agent",
  component: ScreenshotInstructionNode
};
