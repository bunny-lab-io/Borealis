import React, { useEffect, useState } from "react";
import { Handle, Position, useReactFlow } from "reactflow";

const APINode = ({ id, data }) => {
    const { setNodes } = useReactFlow();
    const [agents, setAgents] = useState([]);
    const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
    const [selectedType, setSelectedType] = useState(data.data_type || "screenshot");
    const [imageData, setImageData] = useState("");
    const [intervalMs, setIntervalMs] = useState(data.interval || 1000);
    const [paused, setPaused] = useState(false);
    const [overlayVisible, setOverlayVisible] = useState(true);

    // Refresh agents every 5s
    useEffect(() => {
        const fetchAgents = () => fetch("/api/agents").then(res => res.json()).then(setAgents);
        fetchAgents();
        const interval = setInterval(fetchAgents, 5000);
        return () => clearInterval(interval);
    }, []);

    // Pull image if agent provisioned
    useEffect(() => {
        if (!selectedAgent || paused) return;
        const interval = setInterval(() => {
            fetch(`/api/agent/image?agent_id=${selectedAgent}`)
                .then(res => res.json())
                .then(json => {
                    if (json.image_base64) {
                        setImageData(json.image_base64);
                        window.BorealisValueBus = window.BorealisValueBus || {};
                        window.BorealisValueBus[id] = json.image_base64;
                    }
                })
                .catch(() => { });
        }, intervalMs);
        return () => clearInterval(interval);
    }, [selectedAgent, id, paused, intervalMs]);

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
        }).then(() => {
            setNodes(nds =>
                nds.map(n => n.id === id
                    ? {
                        ...n,
                        data: {
                            ...n.data,
                            agent_id: selectedAgent,
                            data_type: selectedType,
                            interval: intervalMs
                        }
                    }
                    : n
                )
            );
        });
    };

    const toggleOverlay = () => {
        const newVisibility = !overlayVisible;
        setOverlayVisible(newVisibility);
        if (selectedAgent) {
            fetch("/api/agent/overlay_visibility", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ agent_id: selectedAgent, visible: newVisibility })
            });
        }
    };

    return (
        <div className="borealis-node">
            <Handle type="source" position={Position.Right} className="borealis-handle" />
            <div className="borealis-node-header">API Data Collector</div>
            <div className="borealis-node-content">
                <label style={{ fontSize: "10px" }}>Agent:</label>
                <select
                    value={selectedAgent}
                    onChange={(e) => setSelectedAgent(e.target.value)}
                    style={{ width: "100%", fontSize: "9px", marginBottom: "6px" }}
                >
                    <option value="">-- Select --</option>
                    {Object.entries(agents).map(([id, info]) => (
                        <option key={id} value={id} disabled={info.status === "provisioned"}>
                            {id} {info.status === "provisioned" ? "(Adopted)" : ""}
                        </option>
                    ))}
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
    type: "API_Data_Collector",
    label: "API Data Collector",
    description: "Connects to a remote agent via API and collects data such as screenshots, OCR results, and more.",
    content: "Publishes agent-collected data into the workflow ValueBus.",
    component: APINode
};