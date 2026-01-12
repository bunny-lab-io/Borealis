import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Typography,
  Button,
  Stack,
  TextField,
  IconButton,
  Tooltip,
  LinearProgress,
  Chip,
} from "@mui/material";
import {
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  ContentCopy as CopyIcon,
  RefreshRounded as RefreshIcon,
  LanRounded as IpIcon,
  LinkRounded as LinkIcon,
} from "@mui/icons-material";
import { io } from "socket.io-client";
import Prism from "prismjs";
import "prismjs/components/prism-powershell";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";

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

const fontFamilyMono =
  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace';

const emitAsync = (socket, event, payload, timeoutMs = 4000) =>
  new Promise((resolve) => {
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      resolve({ error: "timeout" });
    }, timeoutMs);
    socket.emit(event, payload, (resp) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(resp || {});
    });
  });

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function highlightPs(code) {
  try {
    return Prism.highlight(code || "", Prism.languages.powershell, "powershell");
  } catch {
    return code || "";
  }
}

export default function ReverseTunnelPowershell({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [shellState, setShellState] = useState("idle");
  const [tunnel, setTunnel] = useState(null);
  const [output, setOutput] = useState("");
  const [input, setInput] = useState("");
  const [statusMessage, setStatusMessage] = useState("");
  const [copyFlash, setCopyFlash] = useState(false);
  const [loading, setLoading] = useState(false);
  const socketRef = useRef(null);
  const localSocketRef = useRef(false);
  const terminalRef = useRef(null);
  const agentIdRef = useRef("");
  const tunnelIdRef = useRef("");

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

  const ensureSocket = useCallback(() => {
    if (socketRef.current) return socketRef.current;
    const existing = typeof window !== "undefined" ? window.BorealisSocket : null;
    if (existing) {
      socketRef.current = existing;
      localSocketRef.current = false;
      return existing;
    }
    const socket = io(window.location.origin, { transports: ["websocket"] });
    socketRef.current = socket;
    localSocketRef.current = true;
    return socket;
  }, []);

  const appendOutput = useCallback((text) => {
    if (!text) return;
    setOutput((prev) => {
      const next = `${prev}${text}`;
      const limit = 40000;
      return next.length > limit ? next.slice(next.length - limit) : next;
    });
  }, []);

  const scrollToBottom = useCallback(() => {
    const el = terminalRef.current;
    if (!el) return;
    requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, []);

  useEffect(() => {
    scrollToBottom();
  }, [output, scrollToBottom]);

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

  const closeShell = useCallback(async () => {
    const socket = ensureSocket();
    await emitAsync(socket, "vpn_shell_close", {});
  }, [ensureSocket]);

  const handleDisconnect = useCallback(async () => {
    setLoading(true);
    setStatusMessage("");
    try {
      await closeShell();
      await stopTunnel("operator_disconnect");
    } finally {
      setTunnel(null);
      setShellState("closed");
      setSessionState("idle");
      setLoading(false);
    }
  }, [closeShell, stopTunnel]);

  useEffect(() => {
    const socket = ensureSocket();
    const handleDisconnectEvent = () => {
      if (sessionState === "connected") {
        setShellState("closed");
        setSessionState("idle");
        setStatusMessage("Socket disconnected.");
      }
    };
    const handleOutput = (payload) => {
      appendOutput(payload?.data || "");
    };
    const handleClosed = () => {
      setShellState("closed");
      setSessionState("idle");
      setStatusMessage("Shell closed.");
    };

    socket.on("disconnect", handleDisconnectEvent);
    socket.on("vpn_shell_output", handleOutput);
    socket.on("vpn_shell_closed", handleClosed);

    return () => {
      socket.off("disconnect", handleDisconnectEvent);
      socket.off("vpn_shell_output", handleOutput);
      socket.off("vpn_shell_closed", handleClosed);
      if (localSocketRef.current) {
        socket.disconnect();
      }
    };
  }, [appendOutput, ensureSocket, sessionState]);

  useEffect(() => {
    return () => {
      closeShell();
      stopTunnel("component_unmount");
    };
  }, [closeShell, stopTunnel]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      setStatusMessage("Agent ID is required to connect.");
      return;
    }
    setLoading(true);
    setStatusMessage("");
    setSessionState("connecting");
    setShellState("opening");
    try {
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
      const waitForTunnelReady = async () => {
        const deadline = Date.now() + 60000;
        let lastError = "";
        while (Date.now() < deadline) {
          const statusResp = await fetch(
            `/api/tunnel/connect/status?agent_id=${encodeURIComponent(agentId)}&bump=1`
          );
          const statusData = await statusResp.json().catch(() => ({}));
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
      setTunnel({ ...data, ...statusData });

      const socket = ensureSocket();
      const openShellWithRetry = async () => {
        const deadline = Date.now() + 30000;
        let lastError = "";
        let attempt = 0;
        while (Date.now() < deadline) {
          attempt += 1;
          const openResp = await emitAsync(socket, "vpn_shell_open", { agent_id: agentId }, 6000);
          if (!openResp?.error) {
            return openResp;
          }
          lastError = openResp.error;
          setStatusMessage(`Waiting for PowerShell shell (${attempt})...`);
          await sleep(2000);
        }
        throw new Error(lastError || "shell_connect_failed");
      };

      await openShellWithRetry();
      setStatusMessage("");
      setSessionState("connected");
      setShellState("connected");
    } catch (err) {
      setSessionState("error");
      setShellState("closed");
      setStatusMessage(String(err.message || err));
    } finally {
      setLoading(false);
    }
  }, [agentId, ensureSocket]);

  const handleSend = useCallback(
    async (text) => {
      const socket = ensureSocket();
      if (!socket || sessionState !== "connected") return;
      const payload = `${text}${text.endsWith("\n") ? "" : "\r\n"}`;
      appendOutput(`\nPS> ${text}\n`);
      setInput("");
      const resp = await emitAsync(socket, "vpn_shell_send", { data: payload });
      if (resp?.error) {
        setStatusMessage("Send failed.");
      }
    },
    [appendOutput, ensureSocket, sessionState]
  );

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(output || "");
      setCopyFlash(true);
      setTimeout(() => setCopyFlash(false), 1200);
    } catch {
      setCopyFlash(false);
    }
  };

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
      <Stack direction={{ xs: "column", sm: "row" }} spacing={1.5} alignItems={{ xs: "flex-start", sm: "center" }}>
        <Button
          size="small"
          startIcon={isConnected ? <StopIcon /> : <PlayIcon />}
          sx={gradientButtonSx}
          disabled={loading || (!isConnected && !agentId)}
          onClick={isConnected ? handleDisconnect : requestTunnel}
        >
          {isConnected ? "Disconnect" : "Connect"}
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
        }}
      >
        {loading ? <LinearProgress color="info" sx={{ height: 3 }} /> : null}
        <Box
          ref={terminalRef}
          sx={{
            flexGrow: 1,
            minHeight: 240,
            maxHeight: "100%",
            overflow: "auto",
            position: "relative",
            p: 2,
            "& pre": {
              margin: 0,
              fontFamily: fontFamilyMono,
              fontSize: 13,
              lineHeight: 1.5,
              color: "#e6edf3",
            },
          }}
        >
          <Editor
            value={output}
            onValueChange={() => {}}
            highlight={highlightPs}
            padding={12}
            readOnly
            style={{
              minHeight: "100%",
              background: "transparent",
              color: "#e6edf3",
              fontFamily: fontFamilyMono,
              fontSize: 13,
            }}
          />
          <Box sx={{ position: "absolute", top: 8, right: 8, display: "flex", gap: 0.5 }}>
            <Tooltip title="Copy output">
              <IconButton size="small" onClick={handleCopy} sx={{ color: copyFlash ? MAGIC_UI.accentC : MAGIC_UI.textMuted }}>
                <CopyIcon fontSize="small" />
              </IconButton>
            </Tooltip>
            <Tooltip title="Clear output">
              <IconButton
                size="small"
                onClick={() => setOutput("")}
                sx={{ color: MAGIC_UI.textMuted }}
              >
                <RefreshIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          </Box>
        </Box>

        <Box
          sx={{
            borderTop: `1px solid ${MAGIC_UI.panelBorder}`,
            p: 1.5,
            background: "rgba(6,10,20,0.92)",
          }}
        >
          <TextField
            fullWidth
            size="small"
            value={input}
            disabled={!isConnected}
            placeholder={isConnected ? "Enter PowerShell command and press Enter" : "Connect to start sending commands"}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                const text = input.trim();
                if (text) handleSend(text);
              }
            }}
            InputProps={{
              sx: {
                backgroundColor: "rgba(12,18,35,0.9)",
                color: "#e2e8f0",
                borderRadius: 2,
                "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              },
            }}
          />
        </Box>
      </Box>

      <Stack spacing={0.3} sx={{ mt: 1 }}>
        <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
          Tunnel: {sessionState === "connected" ? "Active" : sessionState}
        </Typography>
        <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
          Shell: {shellState === "connected" ? "Ready" : shellState}
        </Typography>
        {statusMessage ? (
          <Typography variant="body2" sx={{ color: "#ff7b89" }}>
            {statusMessage}
          </Typography>
        ) : null}
      </Stack>
    </Box>
  );
}
