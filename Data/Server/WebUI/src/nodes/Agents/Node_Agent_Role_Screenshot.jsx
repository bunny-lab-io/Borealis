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

    const handleCopyLiveViewLink = () => {
        const agentEdge = edges.find(e => e.target === id && e.sourceHandle === "provisioner");
        if (!agentEdge) {
            alert("No upstream agent connection found.");
            return;
        }

        const agentNode = getNodes().find(n => n.id === agentEdge.source);
        const selectedAgentId = agentNode?.data?.agent_id;

        if (!selectedAgentId) {
            alert("Upstream agent node does not have a selected agent.");
            return;
        }

        const liveUrl = `${window.location.origin}/api/agent/${selectedAgentId}/node/${id}/screenshot/live`;
        navigator.clipboard.writeText(liveUrl)
            .then(() => console.log(`[Clipboard] Copied Live View URL: ${liveUrl}`))
            .catch(err => console.error("Clipboard copy failed:", err));
    };

    useEffect(() => {
        const intervalId = setInterval(() => {
            const val = base64Ref.current;

            console.log(`[Screenshot Node] setInterval update. Current base64 length: ${val?.length || 0}`);

            if (!val) return;

            window.BorealisValueBus[id] = val;
            setNodes(nds =>
                nds.map(n =>
                    n.id === id
                        ? { ...n, data: { ...n.data, value: val } }
                        : n
                )
            );
        }, window.BorealisUpdateRate || 100);

        return () => clearInterval(intervalId);
    }, [id, setNodes]);

    useEffect(() => {
        const socket = window.BorealisSocket || null;
        if (!socket) {
            console.warn("[Screenshot Node] BorealisSocket not available");
            return;
        }

        console.log(`[Screenshot Node] Listening for agent_screenshot_task with node_id: ${id}`);

        const handleScreenshot = (payload) => {
            console.log("[Screenshot Node] Received payload:", payload);

            if (payload?.node_id === id && payload?.image_base64) {
                base64Ref.current = payload.image_base64;
                setImageBase64(payload.image_base64);
                window.BorealisValueBus[id] = payload.image_base64;

                console.log(`[Screenshot Node] Updated base64Ref and ValueBus for ${id}, length: ${payload.image_base64.length}`);
            } else {
                console.log(`[Screenshot Node] Ignored payload for mismatched node_id (${payload?.node_id})`);
            }
        };

        socket.on("agent_screenshot_task", handleScreenshot);
        return () => socket.off("agent_screenshot_task", handleScreenshot);
    }, [id]);

    window.__BorealisInstructionNodes = window.__BorealisInstructionNodes || {};
    window.__BorealisInstructionNodes[id] = () => ({
        node_id: id,
        role: "screenshot",
        interval,
        visible,
        alias,
        ...region
    });

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
                    <input type="number" value={region.x} onChange={(e) => setRegion({ ...region, x: Number(e.target.value) })} style={{ width: "25%" }} />
                    <input type="number" value={region.y} onChange={(e) => setRegion({ ...region, y: Number(e.target.value) })} style={{ width: "25%" }} />
                    <input type="number" value={region.w} onChange={(e) => setRegion({ ...region, w: Number(e.target.value) })} style={{ width: "25%" }} />
                    <input type="number" value={region.h} onChange={(e) => setRegion({ ...region, h: Number(e.target.value) })} style={{ width: "25%" }} />
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
