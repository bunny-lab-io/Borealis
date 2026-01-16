import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Chip,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  Stack,
  TextField,
  Typography,
  LinearProgress,
} from "@mui/material";
import {
  DesktopWindowsRounded as DesktopIcon,
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  LinkRounded as LinkIcon,
  LanRounded as IpIcon,
} from "@mui/icons-material";
import Guacamole from "guacamole-common-js";

const MAGIC_UI = {
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  px: 2.2,
  minWidth: 120,
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
  },
};

const PROTOCOLS = [{ value: "rdp", label: "RDP" }];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

export default function ReverseTunnelRdp({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [tunnel, setTunnel] = useState(null);
  const [protocol, setProtocol] = useState("rdp");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const containerRef = useRef(null);
  const displayRef = useRef(null);
  const clientRef = useRef(null);
  const tunnelRef = useRef(null);
  const mouseRef = useRef(null);
  const agentIdRef = useRef("");
  const tunnelIdRef = useRef("");
  const bumpTimerRef = useRef(null);

  const agentId = useMemo(() => {
    return (
      normalizeText(device?.agent_id) ||
      normalizeText(device?.agentId) ||
      normalizeText(device?.agent_guid) ||
      normalizeText(device?.agentGuid) ||
      normalizeText(device?.id) ||
      normalizeText(device?.guid) ||
      normalizeText(device?.summary?.agent_id) ||
      ""
    );
  }, [device]);

  useEffect(() => {
    agentIdRef.current = agentId;
  }, [agentId]);

  useEffect(() => {
    tunnelIdRef.current = tunnel?.tunnel_id || "";
  }, [tunnel?.tunnel_id]);

  const notifyAgentOnboarding = useCallback(async () => {
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "Agent Onboarding Underway",
          message:
            "Please wait for the agent to finish onboarding into Borealis. It takes about 1 minute to finish the process.",
          icon: "info",
          variant: "info",
        }),
      });
    } catch {
      /* ignore notification transport errors */
    }
  }, []);

  const handleAgentOnboarding = useCallback(async () => {
    await notifyAgentOnboarding();
    setStatusMessage("Agent Onboarding Underway.");
    setSessionState("idle");
    setTunnel(null);
  }, [notifyAgentOnboarding]);

  const teardownDisplay = useCallback(() => {
    try {
      const client = clientRef.current;
      if (client) {
        client.disconnect();
      }
    } catch {
      /* ignore */
    }
    clientRef.current = null;
    tunnelRef.current = null;
    mouseRef.current = null;
    const host = displayRef.current;
    if (host) {
      host.innerHTML = "";
    }
  }, []);

  const stopTunnel = useCallback(async (reason = "operator_disconnect") => {
    const currentAgentId = agentIdRef.current;
    if (!currentAgentId) return;
    const currentTunnelId = tunnelIdRef.current;
    try {
      await fetch("/api/tunnel/disconnect", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: currentAgentId, tunnel_id: currentTunnelId, reason }),
      });
    } catch {
      // best-effort
    }
  }, []);

  const clearBumpTimer = useCallback(() => {
    if (bumpTimerRef.current) {
      clearInterval(bumpTimerRef.current);
      bumpTimerRef.current = null;
    }
  }, []);

  const startBumpTimer = useCallback(
    (currentAgentId) => {
      clearBumpTimer();
      if (!currentAgentId) return;
      bumpTimerRef.current = setInterval(async () => {
        try {
          await fetch(`/api/tunnel/connect/status?agent_id=${encodeURIComponent(currentAgentId)}&bump=1`);
        } catch {
          /* ignore */
        }
      }, 60000);
    },
    [clearBumpTimer]
  );

  const handleDisconnect = useCallback(async () => {
    setLoading(true);
    setStatusMessage("");
    clearBumpTimer();
    try {
      teardownDisplay();
      await stopTunnel("operator_disconnect");
    } finally {
      setTunnel(null);
      setSessionState("idle");
      setLoading(false);
    }
  }, [clearBumpTimer, stopTunnel, teardownDisplay]);

  const scaleToFit = useCallback(() => {
    const client = clientRef.current;
    const container = containerRef.current;
    if (!client || !container) return;
    const display = client.getDisplay();
    const displayWidth = display.getWidth();
    const displayHeight = display.getHeight();
    const bounds = container.getBoundingClientRect();
    if (!displayWidth || !displayHeight || !bounds.width || !bounds.height) return;
    const scale = Math.min(bounds.width / displayWidth, bounds.height / displayHeight);
    display.scale(scale);
  }, []);

  useEffect(() => {
    const handleResize = () => scaleToFit();
    window.addEventListener("resize", handleResize);
    let observer = null;
    if (typeof ResizeObserver !== "undefined" && containerRef.current) {
      observer = new ResizeObserver(() => scaleToFit());
      observer.observe(containerRef.current);
    }
    return () => {
      window.removeEventListener("resize", handleResize);
      if (observer) observer.disconnect();
    };
  }, [scaleToFit]);

  useEffect(() => {
    return () => {
      clearBumpTimer();
      teardownDisplay();
      stopTunnel("component_unmount");
    };
  }, [clearBumpTimer, stopTunnel, teardownDisplay]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      setStatusMessage("Agent ID is required to connect.");
      return null;
    }
    setLoading(true);
    setStatusMessage("");
    try {
      try {
        const readinessResp = await fetch(`/api/tunnel/status?agent_id=${encodeURIComponent(agentId)}`);
        const readinessData = await readinessResp.json().catch(() => ({}));
        if (readinessResp.ok && readinessData?.agent_socket !== true) {
          await handleAgentOnboarding();
          return null;
        }
      } catch {
        // best-effort readiness check
      }

      setSessionState("connecting");
      const resp = await fetch("/api/tunnel/connect", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: agentId }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const detail = data?.detail ? `: ${data.detail}` : "";
        throw new Error(`${data?.error || `HTTP ${resp.status}`}${detail}`);
      }
      tunnelIdRef.current = data?.tunnel_id || "";

      const waitForTunnelReady = async () => {
        const deadline = Date.now() + 60000;
        let lastError = "";
        while (Date.now() < deadline) {
          const statusResp = await fetch(
            `/api/tunnel/connect/status?agent_id=${encodeURIComponent(agentId)}&bump=1`
          );
          const statusData = await statusResp.json().catch(() => ({}));
          if (statusData?.error === "agent_socket_missing" || (statusResp.ok && statusData?.agent_socket === false)) {
            await handleAgentOnboarding();
            await stopTunnel("agent_onboarding_pending");
            return null;
          }
          if (statusResp.ok && statusData?.status === "up") {
            const agentSocket = statusData?.agent_socket;
            const agentReady = agentSocket === undefined ? true : Boolean(agentSocket);
            if (agentReady) {
              return statusData;
            }
            setStatusMessage("Waiting for agent VPN socket to register...");
          } else if (statusData?.error) {
            lastError = statusData.error;
          }
          await sleep(2000);
        }
        throw new Error(lastError || "Tunnel not ready");
      };

      const statusData = await waitForTunnelReady();
      if (!statusData) {
        return null;
      }
      setTunnel({ ...data, ...statusData });
      startBumpTimer(agentId);
      return statusData;
    } catch (err) {
      setSessionState("error");
      setStatusMessage(String(err.message || err));
      return null;
    } finally {
      setLoading(false);
    }
  }, [agentId, handleAgentOnboarding, startBumpTimer, stopTunnel]);

  const openRdpSession = useCallback(
    async () => {
      const currentAgentId = agentIdRef.current;
      if (!currentAgentId) return;
      const payload = {
        agent_id: currentAgentId,
        protocol,
        username: username.trim(),
        password,
      };
      const resp = await fetch("/api/rdp/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const detail = data?.detail ? `: ${data.detail}` : "";
        throw new Error(`${data?.error || `HTTP ${resp.status}`}${detail}`);
      }
      const token = data?.token;
      const wsUrl = data?.ws_url;
      if (!token || !wsUrl) {
        throw new Error("RDP session unavailable.");
      }
      const tunnelUrl = `${wsUrl}?token=${encodeURIComponent(token)}`;
      const tunnel = new Guacamole.WebSocketTunnel(tunnelUrl);
      const client = new Guacamole.Client(tunnel);
      const displayHost = displayRef.current;

      tunnel.onerror = (status) => {
        setStatusMessage(status?.message || "RDP tunnel error.");
      };
      client.onerror = (status) => {
        setStatusMessage(status?.message || "RDP client error.");
      };
      client.onstatechange = (state) => {
        if (state === Guacamole.Client.State.CONNECTED) {
          setSessionState("connected");
          setStatusMessage("");
        } else if (state === Guacamole.Client.State.DISCONNECTED) {
          setSessionState("idle");
        }
      };
      client.onresize = () => {
        scaleToFit();
      };

      if (displayHost) {
        displayHost.innerHTML = "";
        displayHost.appendChild(client.getDisplay().getElement());
      }

      const mouse = new Guacamole.Mouse(client.getDisplay().getElement());
      mouse.onmousemove = (state) => client.sendMouseState(state);
      mouse.onmousedown = (state) => client.sendMouseState(state);
      mouse.onmouseup = (state) => client.sendMouseState(state);

      clientRef.current = client;
      tunnelRef.current = tunnel;
      mouseRef.current = mouse;
      client.connect();
      scaleToFit();
      setStatusMessage("Connecting to RDP...");
    },
    [password, protocol, scaleToFit, username]
  );

  const handleConnect = useCallback(async () => {
    if (sessionState === "connected") return;
    setStatusMessage("");
    setSessionState("connecting");
    try {
      const tunnelReady = await requestTunnel();
      if (!tunnelReady) {
        return;
      }
      await openRdpSession();
    } catch (err) {
      setSessionState("error");
      setStatusMessage(String(err.message || err));
    }
  }, [openRdpSession, requestTunnel, sessionState]);

  const isConnected = sessionState === "connected";
  const sessionChips = [
    tunnel?.tunnel_id
      ? {
          label: `Tunnel ${tunnel.tunnel_id.slice(0, 8)}`,
          color: MAGIC_UI.accentB,
          icon: <LinkIcon sx={{ fontSize: 18 }} />,
        }
      : null,
    tunnel?.virtual_ip
      ? {
          label: `IP ${String(tunnel.virtual_ip).split("/")[0]}`,
          color: MAGIC_UI.accentA,
          icon: <IpIcon sx={{ fontSize: 18 }} />,
        }
      : null,
  ].filter(Boolean);

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, flexGrow: 1, minHeight: 0 }}>
      <Stack direction={{ xs: "column", md: "row" }} spacing={1.5} alignItems={{ xs: "flex-start", md: "center" }}>
        <Button
          size="small"
          startIcon={isConnected ? <StopIcon /> : <PlayIcon />}
          sx={gradientButtonSx}
          disabled={loading || (!isConnected && !agentId)}
          onClick={isConnected ? handleDisconnect : handleConnect}
        >
          {isConnected ? "Disconnect" : "Connect"}
        </Button>
        <Stack direction="row" spacing={1} sx={{ flexWrap: "wrap", alignItems: "center" }}>
          <FormControl
            size="small"
            sx={{
              minWidth: 140,
              "& .MuiOutlinedInput-root": {
                backgroundColor: "rgba(12,18,35,0.9)",
                color: MAGIC_UI.textBright,
                borderRadius: 2,
                "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              },
              "& .MuiInputLabel-root": { color: MAGIC_UI.textMuted },
            }}
          >
            <InputLabel>Protocol</InputLabel>
            <Select
              label="Protocol"
              value={protocol}
              onChange={(e) => setProtocol(e.target.value)}
              disabled={isConnected}
            >
              {PROTOCOLS.map((item) => (
                <MenuItem key={item.value} value={item.value}>
                  {item.label}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <TextField
            size="small"
            label="Username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={isConnected}
            sx={{
              minWidth: 180,
              input: { color: MAGIC_UI.textBright },
              "& .MuiOutlinedInput-root": {
                backgroundColor: "rgba(12,18,35,0.9)",
                borderRadius: 2,
                "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              },
              "& .MuiInputLabel-root": { color: MAGIC_UI.textMuted },
            }}
          />
          <TextField
            size="small"
            type="password"
            label="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={isConnected}
            sx={{
              minWidth: 180,
              input: { color: MAGIC_UI.textBright },
              "& .MuiOutlinedInput-root": {
                backgroundColor: "rgba(12,18,35,0.9)",
                borderRadius: 2,
                "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              },
              "& .MuiInputLabel-root": { color: MAGIC_UI.textMuted },
            }}
          />
        </Stack>
        <Stack direction="row" spacing={1}>
          {sessionChips.map((chip) => (
            <Chip
              key={chip.label}
              icon={chip.icon}
              label={chip.label}
              sx={{
                borderRadius: 999,
                color: chip.color,
                border: `1px solid ${MAGIC_UI.panelBorder}`,
                backgroundColor: "rgba(8,12,24,0.65)",
              }}
            />
          ))}
        </Stack>
      </Stack>

      <Box
        sx={{
          flexGrow: 1,
          minHeight: 320,
          display: "flex",
          flexDirection: "column",
          borderRadius: 3,
          border: `1px solid ${MAGIC_UI.panelBorder}`,
          background:
            "linear-gradient(145deg, rgba(8,12,24,0.94), rgba(10,16,30,0.9)), radial-gradient(circle at 20% 20%, rgba(125,211,252,0.08), transparent 35%)",
          boxShadow: "0 25px 80px rgba(2,6,23,0.85)",
          overflow: "hidden",
          position: "relative",
        }}
      >
        {loading ? <LinearProgress color="info" sx={{ height: 3 }} /> : null}
        <Box
          ref={containerRef}
          sx={{
            flexGrow: 1,
            position: "relative",
            backgroundColor: "rgba(2,6,20,0.9)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            overflow: "hidden",
          }}
        >
          <Box ref={displayRef} sx={{ width: "100%", height: "100%" }} />
          {!isConnected ? (
            <Stack spacing={1} sx={{ position: "absolute", alignItems: "center" }}>
              <DesktopIcon sx={{ color: MAGIC_UI.accentA, fontSize: 40 }} />
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                Connect to start the remote desktop session.
              </Typography>
            </Stack>
          ) : null}
        </Box>
      </Box>

      <Stack spacing={0.3} sx={{ mt: 1 }}>
        <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
          Session: {isConnected ? "Active" : sessionState}
        </Typography>
        {statusMessage ? (
          <Typography variant="body2" sx={{ color: sessionState === "error" ? "#ff7b89" : MAGIC_UI.textMuted }}>
            {statusMessage}
          </Typography>
        ) : null}
      </Stack>
    </Box>
  );
}
