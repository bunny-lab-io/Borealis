////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: Node_Borealis_Agent.jsx

import React, { useEffect, useState, useRef } from "react";
import { Handle, Position, useReactFlow } from "reactflow";
import { io } from "socket.io-client";

const socket = io(window.location.origin, {
    transports: ["websocket"]
});

const BorealisAgentNode = ({ id, data }) => {
    const { setNodes } = useReactFlow();
    const [agents, setAgents] = useState([]);
    const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
    const [selectedType, setSelectedType] = useState(data.data_type || "screenshot");
    const [intervalMs, setIntervalMs] = useState(data.interval || 1000);
    const [paused, setPaused] = useState(false);
    const [overlayVisible, setOverlayVisible] = useState(true);
    const [imageData, setImageData] = useState("");
    const imageRef = useRef("");

    useEffect(() => {
        fetch("/api/agents").then(res => res.json()).then(setAgents);
        const interval = setInterval(() => {
            fetch("/api/agents").then(res => res.json()).then(setAgents);
        }, 5000);
        return () => clearInterval(interval);
    }, []);

    useEffect(() => {
        socket.on('new_screenshot', (data) => {
            if (data.agent_id === selectedAgent) {
                setImageData(data.image_base64);
                imageRef.current = data.image_base64;
            }
        });

        return () => socket.off('new_screenshot');
    }, [selectedAgent]);

    useEffect(() => {
        const interval = setInterval(() => {
            if (!paused && imageRef.current) {
                window.BorealisValueBus = window.BorealisValueBus || {};
                window.BorealisValueBus[id] = imageRef.current;

                setNodes(nds => {
                    const updated = [...nds];
                    const node = updated.find(n => n.id === id);
                    if (node) {
                        node.data = {
                            ...node.data,
                            value: imageRef.current
                        };
                    }
                    return updated;
                });
            }
        }, window.BorealisUpdateRate || 100);

        return () => clearInterval(interval);
    }, [id, paused, setNodes]);

    const provisionAgent = () => {
        if (!selectedAgent) return;
        fetch("/api/agent/provision", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
                agent_id: selectedAgent,
                x: 250,
                y: 100,
                w: 300,
                h: 200,
                interval: intervalMs,
                visible: overlayVisible,
                task: selectedType
            })
        })
            .then(res => res.json())
            .then(() => {
                console.log("[DEBUG] Agent provisioned");
            });
    };

    const toggleOverlay = () => {
        const newVisibility = !overlayVisible;
        setOverlayVisible(newVisibility);
        provisionAgent();
    };

    return (
        <div className="borealis-node">
            <Handle type="source" position={Position.Right} className="borealis-handle" />
            <div className="borealis-node-header">Borealis Agent</div>
            <div className="borealis-node-content">
                <label style={{ fontSize: "10px" }}>Agent:</label>
                <select
                    value={selectedAgent}
                    onChange={(e) => setSelectedAgent(e.target.value)}
                    style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
                >
                    <option value="">-- Select --</option>
                    {Object.entries(agents).map(([id, info]) => {
                        const statusLabel = info.status === "provisioned"
                            ? "(Provisioned)"
                            : "(Not Provisioned)";
                        return (
                            <option key={id} value={id}>
                                {id} {statusLabel}
                            </option>
                        );
                    })}
                </select>

                <label style={{ fontSize: "10px" }}>Data Type:</label>
                <select
                    value={selectedType}
                    onChange={(e) => setSelectedType(e.target.value)}
                    style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
                >
                    <option value="screenshot">Screenshot Region</option>
                </select>

                <label style={{ fontSize: "10px" }}>Update Rate (ms):</label>
                <input
                    type="number"
                    min="100"
                    step="100"
                    value={intervalMs}
                    onChange={(e) => setIntervalMs(Number(e.target.value))}
                    style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
                />

                <div style={{ marginBottom: "6px" }}>
                    <label style={{ fontSize: "10px" }}>
                        <input
                            type="checkbox"
                            checked={paused}
                            onChange={() => setPaused(!paused)}
                            style={{ marginRight: "4px" }}
                        />
                        Pause Data Collection
                    </label>
                </div>

                <div style={{ display: "flex", gap: "4px", flexWrap: "wrap" }}>
                    <button
                        style={{ flex: 1, fontSize: "9px" }}
                        onClick={provisionAgent}
                    >
                        (Re)Provision
                    </button>
                    <button
                        style={{ flex: 1, fontSize: "9px" }}
                        onClick={toggleOverlay}
                    >
                        {overlayVisible ? "Hide Overlay" : "Show Overlay"}
                    </button>
                </div>
            </div>
        </div>
    );
};

export default {
    type: "Borealis_Agent",
    label: "Borealis Agent",
    description: "Connects to and controls a Borealis Agent via WebSocket in real-time.",
    content: "Provisions a Borealis Agent and streams collected data into the workflow graph.",
    component: BorealisAgentNode
};
