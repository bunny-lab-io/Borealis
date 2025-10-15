////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx
import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Modern Node: Borealis Agent (Sidebar Config Enabled)
const BorealisAgentNode = ({ id, data }) => {
  const { getNodes, setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [agents, setAgents] = useState({});
  const [selectedAgent, setSelectedAgent] = useState(data.agent_id || "");
  const [selectedHost, setSelectedHost] = useState(data.agent_host || "");
  const initialMode = (data.agent_mode || "currentuser").toLowerCase();
  const [selectedMode, setSelectedMode] = useState(
    initialMode === "system" ? "system" : "currentuser"
  );
  const [isConnected, setIsConnected] = useState(false);
  const prevRolesRef = useRef([]);

  // Group agents by hostname and execution context
  const agentsByHostname = useMemo(() => {
    if (!agents || typeof agents !== "object") return {};
    const grouped = {};
    Object.entries(agents).forEach(([aid, info]) => {
      if (!info || typeof info !== "object") return;
      const status = (info.status || "").toString().toLowerCase();
      if (status === "offline") return;
      const host = (info.hostname || info.agent_hostname || "").trim() || "unknown";
      const modeRaw = (info.service_mode || "").toString().toLowerCase();
      const mode = modeRaw === "system" ? "system" : "currentuser";
      if (!grouped[host]) {
        grouped[host] = { currentuser: null, system: null };
      }
      grouped[host][mode] = {
        agent_id: aid,
        status: info.status || "offline",
        last_seen: info.last_seen || 0,
        info,
      };
    });
    return grouped;
  }, [agents]);

// Locale-aware, case-insensitive, numeric-friendly sorter (e.g., "host2" < "host10")
const hostCollator = useMemo(
  () => new Intl.Collator(undefined, { sensitivity: "base", numeric: true }),
  []
);

const hostOptions = useMemo(() => {
  const entries = Object.entries(agentsByHostname)
    .map(([host, contexts]) => {
      const candidates = [contexts.currentuser, contexts.system].filter(Boolean);
      if (!candidates.length) return null;

      // Label is just the hostname (you already simplified this earlier)
      const label = host;

      // Keep latest around if you use it elsewhere, but it no longer affects ordering
      const latest = Math.max(...candidates.map((r) => r.last_seen || 0));

      return { host, label, contexts, latest };
    })
    .filter(Boolean)
    // Always alphabetical, case-insensitive, numeric-aware
    .sort((a, b) => hostCollator.compare(a.host, b.host));

  return entries;
}, [agentsByHostname, hostCollator]);

  // Fetch Agents Periodically
  useEffect(() => {
    const fetchAgents = () => {
      fetch("/api/agents")
        .then((res) => res.json())
        .then(setAgents)
        .catch(() => {});
    };
    fetchAgents();
    const interval = setInterval(fetchAgents, 10000); // Update Agent List Every 10 Seconds
    return () => clearInterval(interval);
  }, []);

  // Ensure host selection stays aligned with available agents
  useEffect(() => {
    const hostExists = hostOptions.some((opt) => opt.host === selectedHost);
    if (hostExists) return;

    if (selectedAgent && agents[selectedAgent]) {
      const info = agents[selectedAgent];
      const inferredHost = (info?.hostname || info?.agent_hostname || "").trim() || "unknown";
      if (inferredHost && inferredHost !== selectedHost) {
        setSelectedHost(inferredHost);
        return;
      }
    }

    const fallbackHost = hostOptions[0]?.host || "";
    if (fallbackHost !== selectedHost) {
      setSelectedHost(fallbackHost);
    }
    if (!fallbackHost && selectedAgent) {
      setSelectedAgent("");
    }
  }, [hostOptions, selectedHost, selectedAgent, agents]);

  // Align agent selection with host/mode choice
  useEffect(() => {
    if (!selectedHost) {
      if (selectedMode !== "currentuser") setSelectedMode("currentuser");
      if (selectedAgent) setSelectedAgent("");
      return;
    }
    const contexts = agentsByHostname[selectedHost];
    if (!contexts) {
      if (selectedMode !== "currentuser") setSelectedMode("currentuser");
      if (selectedAgent) setSelectedAgent("");
      return;
    }
    if (!contexts[selectedMode]) {
      const fallbackMode = contexts.currentuser
        ? "currentuser"
        : contexts.system
        ? "system"
        : selectedMode;
      if (fallbackMode !== selectedMode) {
        setSelectedMode(fallbackMode);
        return;
      }
    }
    const activeContext = contexts[selectedMode];
    const targetAgentId = activeContext?.agent_id || "";
    if (targetAgentId !== selectedAgent) {
      setSelectedAgent(targetAgentId);
    }
  }, [selectedHost, selectedMode, agentsByHostname, selectedAgent]);

  // Sync node data with sidebar changes
  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                agent_id: selectedAgent,
                agent_host: selectedHost,
                agent_mode: selectedMode,
              },
            }
          : n
      )
    );
    setIsConnected(false);
  }, [selectedAgent, selectedHost, selectedMode, setNodes, id]);

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
    if (!selectedHost) return "Unassigned";
    const contexts = agentsByHostname[selectedHost];
    if (!contexts) return "Offline";
    const activeContext = contexts[selectedMode];
    if (!selectedAgent || !activeContext) return "Unavailable";
    const status = (activeContext.status || "").toString().toLowerCase();
    if (status === "provisioned") return "Connected";
    if (status === "orphaned") return "Available";
    if (!status) return "Available";
    return status.charAt(0).toUpperCase() + status.slice(1);
  }, [agentsByHostname, selectedHost, selectedMode, selectedAgent]);

  const activeHostContexts = selectedHost ? agentsByHostname[selectedHost] : null;

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

      <div className="borealis-node-header">Device Agent</div>
      <div className="borealis-node-content" style={{ fontSize: "9px" }}>
        <label>Device:</label>
        <select
          value={selectedHost}
          onChange={(e) => setSelectedHost(e.target.value)}
          style={{ width: "100%", marginBottom: "6px", fontSize: "9px" }}
        >
          <option value="">-- Select --</option>
          {hostOptions.map(({ host, label }) => (
            <option key={host} value={host}>
              {label}
            </option>
          ))}
        </select>

        <label>Available Agent Context(s):</label>
        <select
          value={selectedMode}
          onChange={(e) => setSelectedMode(e.target.value)}
          style={{ width: "100%", marginBottom: "2px", fontSize: "9px" }}
          disabled={!selectedHost}
        >
          <option value="currentuser" disabled={!activeHostContexts?.currentuser}>
            CURRENTUSER (Screen Capture / Macros)
          </option>
          <option value="system" disabled={!activeHostContexts?.system}>
            SYSTEM (Scripts)
          </option>
        </select>

        <div style={{ fontSize: "6px", color: "#aaa", marginBottom: "6px" }}>
          Agent ID:{" "}
          {selectedAgent ? (
            <span style={{ color: "#666" }}>{selectedAgent}</span>
          ) : (
            <span style={{ color: "#666" }}>No Agent Selected</span>
          )}
        </div>

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
            Connect to Device
          </button>
        )}
      </div>
    </div>
  );
};

// Node Registration Object with sidebar config and docs
export default {
  type: "Borealis_Agent",
  label: "Device Agent",
  description: `
Select and connect to a remote Borealis Agent.
- Assign roles to agent dynamically by connecting "Agent Role" nodes.
- Auto-provisions agent as role assignments change.
- See live agent status and re-connect/disconnect easily.
- Choose between CURRENTUSER and SYSTEM contexts for each device.
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
- **Select** a device and agent context (CURRENTUSER vs SYSTEM).
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
