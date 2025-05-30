////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent_Role_Screenshot.jsx
import React, { useEffect, useRef, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";
import ShareIcon from "@mui/icons-material/Share";
import IconButton from "@mui/material/IconButton";

/*
  Agent Role: Screenshot Node (Modern, Sidebar Config Enabled)

  - Defines a screenshot region to be captured by a remote Borealis Agent.
  - Pushes live base64 PNG data to downstream nodes.
  - Region coordinates (x, y, w, h), visibility, overlay label, and interval are all persisted and synchronized.
  - All configuration is moved to the right sidebar (Node Properties).
  - Maintains full bi-directional write-back of coordinates and overlay settings.
*/

if (!window.BorealisValueBus) window.BorealisValueBus = {};
if (!window.BorealisUpdateRate) window.BorealisUpdateRate = 100;

const AgentScreenshotNode = ({ id, data }) => {
  const { setNodes, getNodes } = useReactFlow();
  const edges = useStore(state => state.edges);

  // Core config values pulled from sidebar config (with defaults)
  const interval = parseInt(data?.interval || 1000, 10) || 1000;
  const region = {
    x: parseInt(data?.x ?? 250, 10),
    y: parseInt(data?.y ?? 100, 10),
    w: parseInt(data?.w ?? 300, 10),
    h: parseInt(data?.h ?? 200, 10)
  };
  const visible = (data?.visible ?? "true") === "true";
  const alias = data?.alias || "";
  const [imageBase64, setImageBase64] = useState(data?.value || "");

  // Always push current imageBase64 into BorealisValueBus at the global update rate
  useEffect(() => {
    const intervalId = setInterval(() => {
      if (imageBase64) {
        window.BorealisValueBus[id] = imageBase64;
        setNodes(nds =>
          nds.map(n =>
            n.id === id ? { ...n, data: { ...n.data, value: imageBase64 } } : n
          )
        );
      }
    }, window.BorealisUpdateRate || 100);
    return () => clearInterval(intervalId);
  }, [id, imageBase64, setNodes]);

  // Listen for agent screenshot and overlay region updates
  useEffect(() => {
    const socket = window.BorealisSocket;
    if (!socket) return;

    const handleScreenshot = (payload) => {
      if (payload?.node_id !== id) return;

      if (payload.image_base64) {
        setImageBase64(payload.image_base64);
        window.BorealisValueBus[id] = payload.image_base64;
      }
      const { x, y, w, h } = payload;
      if (
        x !== undefined &&
        y !== undefined &&
        w !== undefined &&
        h !== undefined
      ) {
        setNodes(nds =>
          nds.map(n =>
            n.id === id ? { ...n, data: { ...n.data, x, y, w, h } } : n
          )
        );
      }
    };

    socket.on("agent_screenshot_task", handleScreenshot);
    return () => socket.off("agent_screenshot_task", handleScreenshot);
  }, [id, setNodes]);

  // Register this node for the agent provisioning sync
  window.__BorealisInstructionNodes = window.__BorealisInstructionNodes || {};
  window.__BorealisInstructionNodes[id] = () => ({
    node_id: id,
    role: "screenshot",
    interval,
    visible,
    alias,
    ...region
  });

  // Manual live view copy button
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

  // Node card UI - config handled in sidebar
  return (
    <div className="borealis-node" style={{ position: "relative" }}>
      <Handle type="target" position={Position.Left} className="borealis-handle" />
      <Handle type="source" position={Position.Right} className="borealis-handle" />

      <div className="borealis-node-header">
        {data?.label || "Agent Role: Screenshot"}
      </div>
      <div className="borealis-node-content" style={{ fontSize: "9px" }}>
        <div>
          <b>Region:</b> X:{region.x} Y:{region.y} W:{region.w} H:{region.h}
        </div>
        <div>
          <b>Interval:</b> {interval} ms
        </div>
        <div>
          <b>Overlay:</b> {visible ? "Yes" : "No"}
        </div>
        <div>
          <b>Label:</b> {alias || <span style={{ color: "#666" }}>none</span>}
        </div>
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

// Node registration for Borealis catalog (sidebar config enabled)
export default {
  type: "Agent_Role_Screenshot",
  label: "Agent Role: Screenshot",
  description: `
Capture a live screenshot of a defined region from a remote Borealis Agent.

- Define region (X, Y, Width, Height)
- Select update interval (ms)
- Optionally show a visual overlay with a label
- Pushes base64 PNG stream to downstream nodes
- Use copy button to share live view URL
`.trim(),
  content: "Capture screenshot region via agent",
  component: AgentScreenshotNode,
  config: [
    {
      key: "interval",
      label: "Update Interval (ms)",
      type: "text",
      defaultValue: "1000"
    },
    {
      key: "x",
      label: "Region X",
      type: "text",
      defaultValue: "250"
    },
    {
      key: "y",
      label: "Region Y",
      type: "text",
      defaultValue: "100"
    },
    {
      key: "w",
      label: "Region Width",
      type: "text",
      defaultValue: "300"
    },
    {
      key: "h",
      label: "Region Height",
      type: "text",
      defaultValue: "200"
    },
    {
      key: "visible",
      label: "Show Overlay on Agent",
      type: "select",
      options: ["true", "false"],
      defaultValue: "true"
    },
    {
      key: "alias",
      label: "Overlay Label",
      type: "text",
      defaultValue: ""
    }
  ],
  usage_documentation: `
### Agent Role: Screenshot Node

This node defines a screenshot-capture role for a Borealis Agent.

**How It Works**
- The region (X, Y, W, H) is sent to the Agent for real-time screenshot capture.
- The interval determines how often the Agent captures and pushes new images.
- Optionally, an overlay with a label can be displayed on the Agent's screen for visual feedback.
- The captured screenshot (as a base64 PNG) is available to downstream nodes.
- Use the share button to copy a live viewing URL for the screenshot stream.

**Configuration**
- All fields are edited via the right sidebar.
- Coordinates update live if region is changed from the Agent.

**Warning**
- Changing region from the Agent UI will update this node's coordinates.
- Do not remove the bi-directional region write-back: if the region moves, this node updates immediately.

**Example Use Cases**
- Automated visual QA (comparing regions of apps)
- OCR on live application windows
- Remote monitoring dashboards

  `.trim()
};
