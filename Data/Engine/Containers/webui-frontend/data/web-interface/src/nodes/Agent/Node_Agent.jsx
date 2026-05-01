////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/nodes/Agent/Node_Agent.jsx
import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import { Handle, Position, useReactFlow, useStore } from "reactflow";

// Modern Node: Borealis Agent (Sidebar Config Enabled)
const BorealisAgentNode = ({ id, data }) => {
  const { getNodes, setNodes } = useReactFlow();
  const edges = useStore((state) => state.edges);
  const [agents, setAgents] = useState({});
  const [sites, setSites] = useState([]);
  const [isConnected, setIsConnected] = useState(false);
  const [siteMapping, setSiteMapping] = useState({});
  const prevRolesRef = useRef([]);
  const selectionRef = useRef({ host: "", mode: "", agentId: "", siteId: "" });

  const selectedSiteId = data?.agent_site_id ? String(data.agent_site_id) : "";
  const selectedHost = data?.agent_host || "";
  const selectedMode =
    (data?.agent_mode || "currentuser").toString().toLowerCase() === "system"
      ? "system"
      : "currentuser";
  const selectedAgent = data?.agent_id || "";

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
      const helperContexts = Array.isArray(info.helper_contexts)
        ? info.helper_contexts
            .map((value) => String(value || "").trim().toLowerCase())
            .filter(Boolean)
        : [];
      if (!grouped[host]) {
        grouped[host] = { currentuser: null, system: null };
      }
      const entry = {
        agent_id: aid,
        status: info.status || "offline",
        last_seen: info.last_seen || 0,
        info,
      };
      grouped[host][mode] = entry;
      if (mode === "system" && helperContexts.includes("currentuser") && !grouped[host].currentuser) {
        grouped[host].currentuser = {
          ...entry,
          info: {
            ...info,
            helper_backed: true,
            service_mode: "currentuser",
          },
        };
      }
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

  // Fetch sites list
  useEffect(() => {
    const fetchSites = () => {
      fetch("/api/sites")
        .then((res) => res.json())
        .then((data) => {
          const siteEntries = Array.isArray(data?.sites) ? data.sites : [];
          setSites(siteEntries);
        })
        .catch(() => setSites([]));
    };
    fetchSites();
  }, []);

  // Fetch site mapping for current host options
  useEffect(() => {
    const hostnames = hostOptions.map(({ host }) => host).filter(Boolean);
    if (!hostnames.length) {
      setSiteMapping({});
      return;
    }
    const query = hostnames.map(encodeURIComponent).join(",");
    fetch(`/api/sites/device_map?hostnames=${query}`)
      .then((res) => res.json())
      .then((data) => {
        const mapping = data?.mapping && typeof data.mapping === "object" ? data.mapping : {};
        setSiteMapping(mapping);
      })
      .catch(() => setSiteMapping({}));
  }, [hostOptions]);

  const filteredHostOptions = useMemo(() => {
    if (!selectedSiteId) return hostOptions;
    return hostOptions.filter(({ host }) => {
      const mapping = siteMapping[host];
      if (!mapping || typeof mapping.site_id === "undefined" || mapping.site_id === null) {
        return false;
      }
      return String(mapping.site_id) === selectedSiteId;
    });
  }, [hostOptions, selectedSiteId, siteMapping]);

  // Align selected site with known host mapping when available
  useEffect(() => {
    if (selectedSiteId || !selectedHost) return;
    const mapping = siteMapping[selectedHost];
    if (!mapping || typeof mapping.site_id === "undefined" || mapping.site_id === null) return;
    const mappedId = String(mapping.site_id);
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                agent_site_id: mappedId,
              },
            }
          : n
      )
    );
  }, [selectedHost, selectedSiteId, siteMapping, id, setNodes]);

  // Ensure host selection stays aligned with available agents
  useEffect(() => {
    if (!selectedHost) return;

    const hostExists = filteredHostOptions.some((opt) => opt.host === selectedHost);
    if (hostExists) return;

    if (selectedAgent && agents[selectedAgent]) {
      const info = agents[selectedAgent];
      const inferredHost = (info?.hostname || info?.agent_hostname || "").trim() || "unknown";
      const allowed = filteredHostOptions.some((opt) => opt.host === inferredHost);
      if (allowed && inferredHost && inferredHost !== selectedHost) {
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id
              ? {
                  ...n,
                  data: {
                    ...n.data,
                    agent_host: inferredHost,
                  },
                }
              : n
          )
        );
        return;
      }
    }

    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                agent_host: "",
                agent_id: "",
                agent_mode: "currentuser",
              },
            }
          : n
        )
    );
  }, [filteredHostOptions, selectedHost, selectedAgent, agents, id, setNodes]);

  const siteSelectOptions = useMemo(() => {
    const entries = Array.isArray(sites) ? [...sites] : [];
    entries.sort((a, b) =>
      (a?.name || "").localeCompare(b?.name || "", undefined, { sensitivity: "base" })
    );
    const mapped = entries.map((site) => ({
      value: String(site.id),
      label: site.name || `Site ${site.id}`,
    }));
    return [{ value: "", label: "All Sites" }, ...mapped];
  }, [sites]);

  const hostSelectOptions = useMemo(() => {
    const mapped = filteredHostOptions.map(({ host, label }) => ({
      value: host,
      label,
    }));
    return [{ value: "", label: "-- Select --" }, ...mapped];
  }, [filteredHostOptions]);

  const activeHostContexts = selectedHost ? agentsByHostname[selectedHost] : null;

  const modeSelectOptions = useMemo(
    () => [
      {
        value: "currentuser",
        label: "CURRENTUSER (Interactive Helper)",
        disabled: !activeHostContexts?.currentuser,
      },
      {
        value: "system",
        label: "SYSTEM (Core Agent)",
        disabled: !activeHostContexts?.system,
      },
    ],
    [activeHostContexts]
  );

  useEffect(() => {
    setNodes((nds) =>
      nds.map((n) =>
        n.id === id
          ? {
              ...n,
              data: {
                ...n.data,
                siteOptions: siteSelectOptions,
                hostOptions: hostSelectOptions,
                modeOptions: modeSelectOptions,
              },
            }
          : n
      )
    );
  }, [id, setNodes, siteSelectOptions, hostSelectOptions, modeSelectOptions]);

  useEffect(() => {
    if (!selectedHost) {
      if (selectedAgent || selectedMode !== "currentuser") {
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id
              ? {
                  ...n,
                  data: {
                    ...n.data,
                    agent_id: "",
                    agent_mode: "currentuser",
                  },
                }
              : n
          )
        );
      }
      return;
    }

    const contexts = agentsByHostname[selectedHost];
    if (!contexts) {
      if (selectedAgent || selectedMode !== "currentuser") {
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id
              ? {
                  ...n,
                  data: {
                    ...n.data,
                    agent_id: "",
                    agent_mode: "currentuser",
                  },
                }
              : n
          )
        );
      }
      return;
    }

    if (!contexts[selectedMode]) {
      const fallbackMode = contexts.currentuser
        ? "currentuser"
        : contexts.system
        ? "system"
        : "currentuser";
      const fallbackAgentId = contexts[fallbackMode]?.agent_id || "";
      if (fallbackMode !== selectedMode || fallbackAgentId !== selectedAgent) {
        setNodes((nds) =>
          nds.map((n) =>
            n.id === id
              ? {
                  ...n,
                  data: {
                    ...n.data,
                    agent_mode: fallbackMode,
                    agent_id: fallbackAgentId,
                  },
                }
              : n
          )
        );
      }
      return;
    }

    const targetAgentId = contexts[selectedMode]?.agent_id || "";
    if (targetAgentId !== selectedAgent) {
      setNodes((nds) =>
        nds.map((n) =>
          n.id === id
            ? {
                ...n,
                data: {
                  ...n.data,
                  agent_id: targetAgentId,
                },
              }
            : n
          )
      );
    }
  }, [selectedHost, selectedMode, agentsByHostname, selectedAgent, id, setNodes]);

  useEffect(() => {
    const prev = selectionRef.current;
    const changed =
      prev.host !== selectedHost ||
      prev.mode !== selectedMode ||
      prev.agentId !== selectedAgent ||
      prev.siteId !== selectedSiteId;
    if (!changed) return;

    const selectionChangedAgent =
      prev.agentId &&
      (prev.agentId !== selectedAgent || prev.host !== selectedHost || prev.mode !== selectedMode);
    if (selectionChangedAgent) {
      setIsConnected(false);
      prevRolesRef.current = [];
    }

    selectionRef.current = {
      host: selectedHost,
      mode: selectedMode,
      agentId: selectedAgent,
      siteId: selectedSiteId,
    };
  }, [selectedHost, selectedMode, selectedAgent, selectedSiteId]);

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
      <div
        className="borealis-node-content"
        style={{
          fontSize: "9px",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          textAlign: "center",
          minHeight: "80px",
          gap: "8px",
        }}
      >
        <div style={{ fontSize: "8px", color: "#666" }}>Right-Click to Configure Agent</div>
        <button
          onClick={isConnected ? handleDisconnect : handleConnect}
          style={{
            padding: "6px 14px",
            fontSize: "10px",
            background: isConnected ? "#3a3a3a" : "#0475c2",
            color: "#fff",
            border: "1px solid #0475c2",
            borderRadius: "4px",
            cursor: selectedAgent ? "pointer" : "not-allowed",
            opacity: selectedAgent ? 1 : 0.5,
            minWidth: "150px",
          }}
          disabled={!selectedAgent}
        >
          {isConnected ? "Disconnect" : "Connect to Device"}
        </button>
        <div style={{ fontSize: "8px", color: "#777" }}>
          {selectedHost ? `${selectedHost} · ${selectedMode.toUpperCase()}` : "No device selected"}
        </div>
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
      key: "agent_site_id",
      label: "Site",
      type: "select",
      optionsKey: "siteOptions",
      defaultValue: ""
    },
    {
      key: "agent_host",
      label: "Device",
      type: "select",
      optionsKey: "hostOptions",
      defaultValue: ""
    },
    {
      key: "agent_mode",
      label: "Agent Context",
      type: "select",
      optionsKey: "modeOptions",
      defaultValue: "currentuser"
    },
    {
      key: "agent_id",
      label: "Agent ID",
      type: "text",
      readOnly: true,
      defaultValue: ""
    }
  ],
  usage_documentation: `
### Borealis Agent Node

This node allows you to establish a connection with a device running a Borealis "Agent", so you can instruct the agent to do things from your workflow.

#### Features
- **Select** a site, then a device, then finally an agent context (CURRENTUSER vs SYSTEM).
- **Connect/Disconnect** from the agent at any time.
- **Attach roles** (by connecting "Agent Role" nodes to this node's output handle) to assign behaviors dynamically.

#### How to Use
1. **Drag and drop in a Borealis Agent node.**
2. **Pick an agent** from the dropdown list (auto-populates from API backend).
3. **Click "Connect to Agent"**.
4. **Attach Agent Role Nodes** (e.g., Screenshot, Macro Keypress) to the "provisioner" output handle to define what the agent should do.
5. Agent will automatically update its roles as you change connected Role Nodes.

#### Good to Know
- If an agent disconnects or goes offline, its status will show "Reconnecting..." until it returns.
- **Roles update LIVE**: Any time you change attached roles, the agent gets updated instantly.

`.trim()
};
