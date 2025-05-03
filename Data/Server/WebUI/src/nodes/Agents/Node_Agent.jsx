////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx

import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

const BorealisAgentNode = ({ id, data }) => {
  const { getNodes, setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [agents, setAgents] = useState({});
  const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
  const [isConnected, setIsConnected] = useState(false);
  const prevRolesRef = useRef([]);

  // ---------------- Agent List & Sorting ----------------
  const agentList = useMemo(() => {
    if (!agents || typeof agents !== "object") return [];
    return Object.entries(agents)
      .map(([aid, info]) => ({
        id: aid,
        status: info?.status || "offline",
        last_seen: info?.last_seen || 0
      }))
      .filter(({ status }) => status !== "offline")
      .sort((a, b) => b.last_seen - a.last_seen);
  }, [agents]);

  // ---------------- Periodic Agent Fetching ----------------
  useEffect(() => {
    const fetchAgents = () => {
      fetch("/api/agents")
        .then((res) => res.json())
        .then(setAgents)
        .catch(() => {});
    };
    fetchAgents();
    const interval = setInterval(fetchAgents, 4000);
    return () => clearInterval(interval);
  }, []);

  // ---------------- Node Data Sync ----------------
  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, agent_id: selectedAgent } } : n
      )
    );
    setIsConnected(false);
  }, [selectedAgent]);

  // ---------------- Attached Role Collection ----------------
  const attachedRoleIds = useMemo(
    () =>
      edges
        .filter((e) => e.source === id && e.sourceHandle === "provisioner")
        .map((e) => e.target),
    [edges, id]
  );

  const getAttachedRoles = useCallback(() => {
    const allNodes = getNodes();
    return attachedRoleIds
      .map((nid) => {
        const fn = window.__BorealisInstructionNodes?.[nid];
        return typeof fn === "function" ? fn() : null;
      })
      .filter((r) => r);
  }, [attachedRoleIds, getNodes]);

  // ---------------- Provision Role Logic ----------------
  const provisionRoles = useCallback((roles) => {
    if (!selectedAgent) return; // Allow empty roles but require agent
    fetch("/api/agent/provision", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: selectedAgent, roles })
    })
      .then(() => {
        setIsConnected(true);
        prevRolesRef.current = roles;
      })
      .catch(() => {});
  }, [selectedAgent]);

  const handleConnect = useCallback(() => {
    const roles = getAttachedRoles();
    provisionRoles(roles); // Always call even with empty roles
  }, [getAttachedRoles, provisionRoles]);

  const handleDisconnect = useCallback(() => {
    if (!selectedAgent) return;
    fetch("/api/agent/provision", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: selectedAgent, roles: [] })
    })
      .then(() => {
        setIsConnected(false);
        prevRolesRef.current = [];
      })
      .catch(() => {});
  }, [selectedAgent]);

  // ---------------- Auto-Provision When Roles Change ----------------
  useEffect(() => {
    const newRoles = getAttachedRoles();
    const prevSerialized = JSON.stringify(prevRolesRef.current || []);
    const newSerialized = JSON.stringify(newRoles);
    if (isConnected && newSerialized !== prevSerialized) {
      provisionRoles(newRoles);
    }
  }, [attachedRoleIds, isConnected, getAttachedRoles, provisionRoles]);

  // ---------------- Status Label ----------------
  const selectedAgentStatus = useMemo(() => {
    if (!selectedAgent) return "Unassigned";
    const agent = agents[selectedAgent];
    if (!agent) return "Reconnecting...";
    return agent.status === "provisioned" ? "Connected" : "Available";
  }, [agents, selectedAgent]);

  // ---------------- Render ----------------
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
          {agentList.map(({ id: aid, status }) => (
            <option key={aid} value={aid}>
              {aid} ({status})
            </option>
          ))}
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
          Status: <strong>{selectedAgentStatus}</strong>
          <br />
          Attach <strong>Agent Role Nodes</strong> to define live behavior.
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
