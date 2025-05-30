////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx
import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Modern Node: Borealis Agent (Sidebar Config Enabled)
const BorealisAgentNode = ({ id, data }) => {
  const { getNodes, setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [agents, setAgents] = useState({});
  const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
  const [isConnected, setIsConnected] = useState(false);
  const prevRolesRef = useRef([]);

  // Agent List Sorted (Online First)
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

  // Fetch Agents Periodically
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

  // Sync node data with sidebar changes
  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id ? { ...n, data: { ...n.data, agent_id: selectedAgent } } : n
      )
    );
    setIsConnected(false);
  }, [selectedAgent, setNodes, id]);

  // Attached Roles logic
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

  // Provision Roles to Agent
  const provisionRoles = useCallback((roles) => {
    if (!selectedAgent) return;
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
    provisionRoles(roles);
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

  // Auto-provision on role change
  useEffect(() => {
    const newRoles = getAttachedRoles();
    const prevSerialized = JSON.stringify(prevRolesRef.current || []);
    const newSerialized = JSON.stringify(newRoles);
    if (isConnected && newSerialized !== prevSerialized) {
      provisionRoles(newRoles);
    }
  }, [attachedRoleIds, isConnected, getAttachedRoles, provisionRoles]);

  // Status Label
  const selectedAgentStatus = useMemo(() => {
    if (!selectedAgent) return "Unassigned";
    const agent = agents[selectedAgent];
    if (!agent) return "Reconnecting...";
    return agent.status === "provisioned" ? "Connected" : "Available";
  }, [agents, selectedAgent]);

  // Render (Sidebar handles config)
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

// Node Registration Object with sidebar config and docs
export default {
  type: "Borealis_Agent",
  label: "Borealis Agent",
  description: `
Select and connect to a remote Borealis Agent.
- Assign roles to agent dynamically by connecting "Agent Role" nodes.
- Auto-provisions agent as role assignments change.
- See live agent status and re-connect/disconnect easily.
`.trim(),
  content: "Select and manage an Agent with dynamic roles",
  component: BorealisAgentNode,
  config: [
    {
      key: "agent_id",
      label: "Agent",
      type: "text", // NOTE: UI populates via agent fetch, but config drives default for sidebar.
      defaultValue: ""
    }
  ],
  usage_documentation: `
### Borealis Agent Node

This node represents an available Borealis Agent (Python client) you can control from your workflow.

#### Features
- **Select** an agent from the list of online agents.
- **Connect/Disconnect** from the agent at any time.
- **Attach roles** (by connecting "Agent Role" nodes to this node's output handle) to assign behaviors dynamically.
- **Live status** shows if the agent is available, connected, or offline.

#### How to Use
1. **Drag in a Borealis Agent node.**
2. **Pick an agent** from the dropdown list (auto-populates from backend).
3. **Click "Connect to Agent"** to provision it for the workflow.
4. **Attach Agent Role Nodes** (e.g., Screenshot, Macro Keypress) to the "provisioner" output handle to define what the agent should do.
5. Agent will automatically update its roles as you change connected Role Nodes.

#### Output Handle
- "provisioner" (bottom): Connect Agent Role nodes here.

#### Good to Know
- If an agent disconnects or goes offline, its status will show "Reconnecting..." until it returns.
- Node config can be edited in the right sidebar.
- **Roles update LIVE**: Any time you change attached roles, the agent gets updated instantly.

`.trim()
};
