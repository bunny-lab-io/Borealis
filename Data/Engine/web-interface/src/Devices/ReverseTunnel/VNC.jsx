import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Chip,
  Stack,
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
import RFB from "@novnc/novnc/lib/rfb";

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

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

export default function ReverseTunnelVnc({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [tunnel, setTunnel] = useState(null);
  const containerRef = useRef(null);
  const displayRef = useRef(null);
  const rfbRef = useRef(null);
  const agentIdRef = useRef("");

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
      const client = rfbRef.current;
      if (client) {
        client.disconnect();
      }
    } catch {
      /* ignore */
    }
    rfbRef.current = null;
    const host = displayRef.current;
    if (host) {
      host.innerHTML = "";
    }
  }, []);

  const disconnectVnc = useCallback(async (reason = "operator_disconnect") => {
    const currentAgentId = agentIdRef.current;
    if (!currentAgentId) return;
    try {
      await fetch("/api/vnc/disconnect", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: currentAgentId, reason }),
      });
    } catch {
      // best-effort
    }
  }, []);

  const handleDisconnect = useCallback(async () => {
    setLoading(true);
    setStatusMessage("");
    try {
      teardownDisplay();
      await disconnectVnc("operator_disconnect");
    } finally {
      setTunnel(null);
      setSessionState("idle");
      setLoading(false);
    }
  }, [disconnectVnc, teardownDisplay]);

  useEffect(() => {
    return () => {
      teardownDisplay();
      disconnectVnc("component_unmount");
    };
  }, [disconnectVnc, teardownDisplay]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      setStatusMessage("Agent ID is required to establish.");
      return null;
    }
    setLoading(true);
    setStatusMessage("");
    try {
      setSessionState("connecting");
      const resp = await fetch("/api/vnc/establish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: agentId }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        if (data?.error === "agent_socket_missing") {
          await handleAgentOnboarding();
          return null;
        }
        const detail = data?.detail ? `: ${data.detail}` : "";
        throw new Error(`${data?.error || `HTTP ${resp.status}`}${detail}`);
      }
      setTunnel({
        tunnel_id: data?.tunnel_id,
        virtual_ip: data?.virtual_ip,
      });
      return data;
    } catch (err) {
      setSessionState("error");
      setStatusMessage(String(err.message || err));
      return null;
    } finally {
      setLoading(false);
    }
  }, [agentId, handleAgentOnboarding]);

  const openVncSession = useCallback(async (data) => {
    const token = data?.token;
    const wsUrl = data?.ws_url;
    const vncPassword = data?.vnc_password || "";
    if (!token || !wsUrl) {
      throw new Error("VNC session unavailable.");
    }
    const tunnelUrl = `${wsUrl}?token=${encodeURIComponent(token)}`;
    const displayHost = displayRef.current;
    if (!displayHost) {
      throw new Error("VNC display container missing.");
    }
    displayHost.innerHTML = "";

    const rfb = new RFB(displayHost, tunnelUrl, {
      credentials: { password: vncPassword },
    });
    rfb.scaleViewport = true;
    rfb.resizeSession = true;
    rfb.clipViewport = true;

    rfb.addEventListener("connect", () => {
      setSessionState("connected");
      setStatusMessage("");
    });
    rfb.addEventListener("disconnect", () => {
      setSessionState("idle");
      rfbRef.current = null;
    });
    rfb.addEventListener("securityfailure", (evt) => {
      const detail = evt?.detail?.reason ? ` (${evt.detail.reason})` : "";
      setSessionState("error");
      setStatusMessage(`VNC authentication failed${detail}.`);
    });
    rfb.addEventListener("credentialsrequired", () => {
      try {
        rfb.sendCredentials({ password: vncPassword });
      } catch {
        // ignore
      }
    });

    rfbRef.current = rfb;
    setStatusMessage("Establishing VNC...");
  }, []);

  const handleConnect = useCallback(async () => {
    if (sessionState === "connected") return;
    setStatusMessage("");
    setSessionState("connecting");
    try {
      const sessionData = await requestTunnel();
      if (!sessionData) {
        return;
      }
      await openVncSession(sessionData);
    } catch (err) {
      setSessionState("error");
      setStatusMessage(String(err.message || err));
    }
  }, [openVncSession, requestTunnel, sessionState]);

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
          {isConnected ? "Disconnect" : "Establish"}
        </Button>
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
                Establish to start the VNC session.
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
