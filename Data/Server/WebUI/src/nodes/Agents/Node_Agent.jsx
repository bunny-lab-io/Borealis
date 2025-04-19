////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx

import React, { useEffect, useState } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const BorealisAgentNode = ({ id, data }) => {
    const { getNodes, getEdges, setNodes } = useReactFlow();
    const [agents, setAgents] = useState([]);
    const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");

    // -------------------------------
    // Load agent list from backend
    // -------------------------------
    useEffect(() => {
        fetch("/api/agents")
            .then(res => res.json())
            .then(setAgents);

        const interval = setInterval(() => {
            fetch("/api/agents")
                .then(res => res.json())
                .then(setAgents);
        }, 5000);

        return () => clearInterval(interval);
    }, []);

    // -------------------------------
    // Helper: Get all provisioner role nodes connected to bottom port
    // -------------------------------
    const getAttachedProvisioners = () => {
        const allNodes = getNodes();
        const allEdges = getEdges();
        const attached = [];

        for (const edge of allEdges) {
            if (edge.source === id && edge.sourceHandle === "provisioner") {
                const roleNode = allNodes.find(n => n.id === edge.target);
                if (roleNode && typeof window.__BorealisInstructionNodes?.[roleNode.id] === "function") {
                    attached.push(window.__BorealisInstructionNodes[roleNode.id]());
                }
            }
        }

        return attached;
    };

    // -------------------------------
    // Provision Agent with all Roles
    // -------------------------------
    const handleProvision = () => {
        if (!selectedAgent) return;

        const provisionRoles = getAttachedProvisioners();
        if (!provisionRoles.length) {
            console.warn("No provisioner nodes connected to agent.");
            return;
        }

        const configPayload = {
            agent_id: selectedAgent,
            roles: provisionRoles
        };

        fetch("/api/agent/provision", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(configPayload)
        })
            .then(res => res.json())
            .then(() => {
                console.log(`[Provision] Agent ${selectedAgent} updated with ${provisionRoles.length} roles.`);
            });
    };

    return (
        <div className="borealis-node">
            {/* This bottom port is used for bi-directional provisioning & feedback */}
            <Handle
                type="source"
                position={Position.Bottom}
                id="provisioner"
                className="borealis-handle"
                style={{ top: "100%", background: "#58a6ff" }}
            />

            <div className="borealis-node-header">Borealis Agent</div>
            <div className="borealis-node-content" style={{ fontSize: "9px" }}>
                <label>Agent:</label>
                <select
                    value={selectedAgent}
                    onChange={(e) => {
                        const newId = e.target.value;
                        setSelectedAgent(newId);
                        setNodes(nds =>
                            nds.map(n =>
                                n.id === id
                                    ? { ...n, data: { ...n.data, agent_id: newId } }
                                    : n
                            )
                        );
                    }}
                    style={{ width: "100%", marginBottom: "6px", fontSize: "9px" }}
                >
                    <option value="">-- Select --</option>
                    {Object.entries(agents).map(([id, info]) => {
                        const label = info.status === "provisioned" ? "(Provisioned)" : "(Idle)";
                        return (
                            <option key={id} value={id}>
                                {id} {label}
                            </option>
                        );
                    })}
                </select>

                <button
                    onClick={handleProvision}
                    style={{ width: "100%", fontSize: "9px", padding: "4px", marginTop: "4px" }}
                >
                    Provision Agent
                </button>

                <hr style={{ margin: "6px 0", borderColor: "#444" }} />

                <div style={{ fontSize: "8px", color: "#aaa" }}>
                    Connect <strong>Instruction Nodes</strong> below to define roles.
                    Each instruction node will send back its results (like screenshots) and act as a separate data output.
                </div>

                <div style={{ fontSize: "8px", color: "#aaa", marginTop: "4px" }}>
                    <strong>Supported Roles:</strong>
                    <ul style={{ paddingLeft: "14px", marginTop: "2px", marginBottom: "0" }}>
                        <li><code>screenshot</code>: Capture a region with interval and overlay</li>
                        {/* Future roles will be listed here */}
                    </ul>
                </div>
            </div>
        </div>
    );
};

export default {
    type: "Borealis_Agent",
    label: "Borealis Agent",
    description: `
Main Agent Node

- Selects an available agent
- Connect role nodes below to assign tasks to the agent
- Roles include screenshots, keyboard macros, etc.
`.trim(),
    content: "Select and provision a Borealis Agent with task roles",
    component: BorealisAgentNode
};
