////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx

import React, { useEffect, useState, useCallback, useMemo } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const BorealisAgentNode = ({ id, data }) => {
  const { getNodes, setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [agents, setAgents] = useState({});
  const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
  const [isConnected, setIsConnected] = useState(false);

  // Build a normalized list [{id, status}, ...]
  const agentList = useMemo(() => {
    if (Array.isArray(agents)) {
      return agents.map((a) => ({ id: a.id, status: a.status }));
    } else if (agents && typeof agents === "object") {
      return Object.entries(agents).map(([aid, info]) => ({ id: aid, status: info.status }));
    }
    return [];
  }, [agents]);

  // Fetch agents from backend
  useEffect(() => {
    const fetchAgents = () => {
      fetch("/api/agents")
        .then((res) => res.json())
        .then(setAgents)
        .catch(() => {});
    };
    fetchAgents();
    const interval = setInterval(fetchAgents, 5000);
    return () => clearInterval(interval);
  }, []);

  // Persist selectedAgent and reset connection on change
  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, agent_id: selectedAgent } } : n
      )
    );
    setIsConnected(false);
  }, [selectedAgent]);

  // Compute attached role node IDs for this agent node
  const attachedRoleIds = useMemo(
    () =>
      edges
        .filter((e) => e.source === id && e.sourceHandle === "provisioner")
        .map((e) => e.target),
    [edges, id]
  );

  // Build role payloads using the instruction registry
  const getAttachedRoles = useCallback(() => {
    const allNodes = getNodes();
    return attachedRoleIds
      .map((nid) => {
        const fn = window.__BorealisInstructionNodes?.[nid];
        return typeof fn === "function" ? fn() : null;
      })
      .filter((r) => r);
  }, [attachedRoleIds, getNodes]);

  // Connect: send roles to server
  const handleConnect = useCallback(() => {
    if (!selectedAgent) return;
    const roles = getAttachedRoles();
    fetch("/api/agent/provision", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: selectedAgent, roles }),
    })
      .then(() => setIsConnected(true))
      .catch(() => {});
  }, [selectedAgent, getAttachedRoles]);

  // Disconnect: clear roles on server
  const handleDisconnect = useCallback(() => {
    if (!selectedAgent) return;
    fetch("/api/agent/provision", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: selectedAgent, roles: [] }),
    })
      .then(() => setIsConnected(false))
      .catch(() => {});
  }, [selectedAgent]);

  // Hot-update roles when attachedRoleIds change
  useEffect(() => {
    if (isConnected) {
      const roles = getAttachedRoles();
      fetch("/api/agent/provision", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: selectedAgent, roles }),
      }).catch(() => {});
    }
  }, [attachedRoleIds]);

  return (
    <div className="borealis-node">
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
          onChange={(e) => setSelectedAgent(e.target.value)}
          style={{ width: "100%", marginBottom: "6px", fontSize: "9px" }}
        >
          <option value="">-- Select --</option>
          {agentList.map(({ id: aid, status }) => {
            const labelText = status === "provisioned" ? "(Connected)" : "(Ready to Connect)";
            return (
              <option key={aid} value={aid}>
                {aid} {labelText}
              </option>
            );
          })}
        </select>

        {isConnected ? (
          <button
            onClick={handleDisconnect}
            style={{ width: "100%", fontSize: "9px", padding: "4px", marginTop: "4px" }}
          >
            Disconnect from Agent
          </button>
        ) : (
          <button
            onClick={handleConnect}
            style={{ width: "100%", fontSize: "9px", padding: "4px", marginTop: "4px" }}
            disabled={!selectedAgent}
          >
            Connect to Agent
          </button>
        )}

        <hr style={{ margin: "6px 0", borderColor: "#444" }} />

        <div style={{ fontSize: "8px", color: "#aaa" }}>
          Attach <strong>Agent Role Nodes</strong> to define roles for this agent. Roles will be provisioned automatically.
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
- Connect/disconnects via button
- Auto-updates roles when attached roles change
`.trim(),
  content: "Select and manage an Agent with dynamic roles",
  component: BorealisAgentNode,
};
