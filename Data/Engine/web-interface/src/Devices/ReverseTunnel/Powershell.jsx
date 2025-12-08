import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Typography,
  Button,
  Stack,
  TextField,
  MenuItem,
  IconButton,
  Tooltip,
  LinearProgress,
} from "@mui/material";
import {
  TerminalRounded as TerminalIcon,
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  ContentCopy as CopyIcon,
  RefreshRounded as RefreshIcon,
  LanRounded as PortIcon,
  LinkRounded as LinkIcon,
} from "@mui/icons-material";
import { io } from "socket.io-client";
import Prism from "prismjs";
import "prismjs/components/prism-powershell";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";

// Console diagnostics for troubleshooting the connect/disconnect flow.
const debugLog = (...args) => {
  try {
    // eslint-disable-next-line no-console
    console.error("[ReverseTunnel][PS]", ...args);
  } catch {
    // ignore
  }
};

const MAGIC_UI = {
  panelBg: "rgba(7,11,24,0.92)",
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
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  px: 2.2,
  minWidth: 120,
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
  },
};

const FRAME_HEADER_BYTES = 12; // version, msg_type, flags, reserved, channel_id(u32), length(u32)
const MSG_CLOSE = 0x08;
const CLOSE_AGENT_SHUTDOWN = 6;

const fontFamilyMono =
  'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace';

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function base64FromBytes(bytes) {
  let binary = "";
  bytes.forEach((b) => {
    binary += String.fromCharCode(b);
  });
  return btoa(binary);
}

function buildCloseFrame(channelId = 1, code = CLOSE_AGENT_SHUTDOWN, reason = "operator_close") {
  const payload = new TextEncoder().encode(JSON.stringify({ code, reason }));
  const buffer = new ArrayBuffer(FRAME_HEADER_BYTES + payload.length);
  const view = new DataView(buffer);
  view.setUint8(0, 1); // version
  view.setUint8(1, MSG_CLOSE);
  view.setUint8(2, 0); // flags
  view.setUint8(3, 0); // reserved
  view.setUint32(4, channelId >>> 0, true);
  view.setUint32(8, payload.length >>> 0, true);
  new Uint8Array(buffer, FRAME_HEADER_BYTES).set(payload);
  return base64FromBytes(new Uint8Array(buffer));
}

function highlightPs(code) {
  try {
    return Prism.highlight(code || "", Prism.languages.powershell, "powershell");
  } catch {
    return code || "";
  }
}

const INITIAL_MILESTONES = {
  tunnelReady: false,
  operatorAttached: false,
  shellEstablished: false,
};
const INITIAL_STATUS_CHAIN = ["Offline"];

export default function ReverseTunnelPowershell({ device }) {
  const [connectionType, setConnectionType] = useState("ps");
  const [tunnel, setTunnel] = useState(null);
  const [sessionState, setSessionState] = useState("idle");
  const [, setStatusMessage] = useState("");
  const [, setStatusSeverity] = useState("info");
  const [output, setOutput] = useState("");
  const [input, setInput] = useState("");
  const [copyFlash, setCopyFlash] = useState(false);
  const [, setPolling] = useState(false);
  const [psStatus, setPsStatus] = useState({});
  const [milestones, setMilestones] = useState(() => ({ ...INITIAL_MILESTONES }));
  const [tunnelSteps, setTunnelSteps] = useState(() => [...INITIAL_STATUS_CHAIN]);
  const [websocketSteps, setWebsocketSteps] = useState(() => [...INITIAL_STATUS_CHAIN]);
  const [shellSteps, setShellSteps] = useState(() => [...INITIAL_STATUS_CHAIN]);
  const socketRef = useRef(null);
  const pollTimerRef = useRef(null);
  const resizeTimerRef = useRef(null);
  const terminalRef = useRef(null);
  const joinRetryRef = useRef(null);
  const joinAttemptsRef = useRef(0);
  const tunnelRef = useRef(null);
  const DOMAIN_REMOTE_SHELL = "remote-interactive-shell";

  useEffect(() => {
    debugLog("component mount", { hostname: device?.hostname, agentId });
    return () => debugLog("component unmount");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const hostname = useMemo(() => {
    return (
      normalizeText(device?.hostname) ||
      normalizeText(device?.summary?.hostname) ||
      normalizeText(device?.agent_hostname) ||
      ""
    );
  }, [device]);

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

  const appendStatus = useCallback((setter, label) => {
    if (!label) return;
    setter((prev) => {
      const next = [...prev, label];
      const cap = 6;
      return next.length > cap ? next.slice(next.length - cap) : next;
    });
  }, []);

  const resetState = useCallback(() => {
    debugLog("resetState invoked");
    setTunnel(null);
    setSessionState("idle");
    setStatusMessage("");
    setStatusSeverity("info");
    setOutput("");
    setInput("");
    setPsStatus({});
    setMilestones({ ...INITIAL_MILESTONES });
    setTunnelSteps([...INITIAL_STATUS_CHAIN]);
    setWebsocketSteps([...INITIAL_STATUS_CHAIN]);
    setShellSteps([...INITIAL_STATUS_CHAIN]);
  }, []);

  useEffect(() => {
    tunnelRef.current = tunnel?.tunnel_id || null;
  }, [tunnel?.tunnel_id]);

  const disconnectSocket = useCallback(() => {
    const socket = socketRef.current;
    if (socket) {
      socket.off();
      socket.disconnect();
    }
    socketRef.current = null;
  }, []);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
    setPolling(false);
  }, []);

  const stopTunnel = useCallback(async (reason = "operator_disconnect", tunnelIdOverride = null) => {
    const tunnelId = tunnelIdOverride || tunnelRef.current;
    if (!tunnelId) return;
    try {
      await fetch(`/api/tunnel/${tunnelId}`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reason }),
      });
    } catch (err) {
      // best-effort; socket close frame acts as fallback
    }
  }, []);

  useEffect(() => {
    return () => {
      debugLog("cleanup on unmount", { tunnelId: tunnelRef.current });
      stopPolling();
      disconnectSocket();
      if (joinRetryRef.current) {
        clearTimeout(joinRetryRef.current);
        joinRetryRef.current = null;
      }
      stopTunnel("component_unmount", tunnelRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(output || "");
      setCopyFlash(true);
      setTimeout(() => setCopyFlash(false), 1200);
    } catch {
      setCopyFlash(false);
    }
  };

  const measureTerminal = useCallback(() => {
    const el = terminalRef.current;
    if (!el) return { cols: 120, rows: 32 };
    const width = el.clientWidth || 960;
    const height = el.clientHeight || 460;
    const charWidth = 8.2;
    const charHeight = 18;
    const cols = Math.max(20, Math.min(Math.floor(width / charWidth), 300));
    const rows = Math.max(10, Math.min(Math.floor(height / charHeight), 200));
    return { cols, rows };
  }, []);

  const emitAsync = useCallback((socket, event, payload, timeoutMs = 4000) => {
    return new Promise((resolve) => {
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
  }, []);

  const pollLoop = useCallback(
    (socket, tunnelId) => {
      if (!socket || !tunnelId) return;
      debugLog("pollLoop tick", { tunnelId });
      setPolling(true);
      pollTimerRef.current = setTimeout(async () => {
        const resp = await emitAsync(socket, "ps_poll", {});
        if (resp?.error) {
          debugLog("pollLoop error", resp);
          stopPolling();
          disconnectSocket();
          setPsStatus({});
          setTunnel(null);
          setSessionState("error");
          return;
        }
        if (Array.isArray(resp?.output) && resp.output.length) {
          appendOutput(resp.output.join(""));
        }
        if (resp?.status) {
          setPsStatus(resp.status);
          debugLog("pollLoop status", resp.status);
          if (resp.status.closed) {
            setSessionState("closed");
            setTunnel(null);
            setMilestones({ ...INITIAL_MILESTONES });
            appendStatus(setShellSteps, "Shell closed");
            appendStatus(setTunnelSteps, "Stopped");
            appendStatus(setWebsocketSteps, "Relay stopped");
            stopPolling();
            return;
          }
          if (resp.status.open_sent) {
            appendStatus(setShellSteps, "Opening remote shell");
          }
          if (resp.status.ack) {
            setSessionState("connected");
            setMilestones((prev) => ({ ...prev, shellEstablished: true }));
            appendStatus(setShellSteps, "Remote shell established");
          }
        }
        pollLoop(socket, tunnelId);
      }, 520);
    },
    [appendOutput, emitAsync, stopPolling, disconnectSocket, appendStatus]
  );

  const handleDisconnect = useCallback(
    async (reason = "operator_disconnect") => {
    debugLog("handleDisconnect begin", { reason, tunnelId: tunnel?.tunnel_id, psStatus, sessionState });
    setPsStatus({});
    const socket = socketRef.current;
    const tunnelId = tunnel?.tunnel_id;
    if (joinRetryRef.current) {
      clearTimeout(joinRetryRef.current);
      joinRetryRef.current = null;
    }
    joinAttemptsRef.current = 0;
    if (socket && tunnelId) {
      const frame = buildCloseFrame(1, CLOSE_AGENT_SHUTDOWN, "operator_close");
      debugLog("emit CLOSE", { tunnelId });
      socket.emit("send", { frame });
    }
    await stopTunnel(reason);
    debugLog("stopTunnel issued", { tunnelId });
    stopPolling();
    disconnectSocket();
    setTunnel(null);
    setSessionState("closed");
    setMilestones({ ...INITIAL_MILESTONES });
    appendStatus(setTunnelSteps, "Stopped");
    appendStatus(setWebsocketSteps, "Relay closed");
    appendStatus(setShellSteps, "Shell closed");
    debugLog("handleDisconnect finished", { tunnelId });
  },
    [appendStatus, disconnectSocket, stopPolling, stopTunnel, tunnel?.tunnel_id]
  );

  const handleResize = useCallback(() => {
    if (!socketRef.current || sessionState === "idle") return;
    const dims = measureTerminal();
    socketRef.current.emit("ps_resize", dims);
  }, [measureTerminal, sessionState]);

  useEffect(() => {
    const observer =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(() => {
            if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current);
            resizeTimerRef.current = setTimeout(() => handleResize(), 200);
          })
        : null;
    const el = terminalRef.current;
    if (observer && el) observer.observe(el);
    const onWinResize = () => handleResize();
    window.addEventListener("resize", onWinResize);
    return () => {
      window.removeEventListener("resize", onWinResize);
      if (observer && el) observer.unobserve(el);
    };
  }, [handleResize]);

  const connectSocket = useCallback(
    (lease, { isRetry = false } = {}) => {
      if (!lease?.tunnel_id) return;
      if (joinRetryRef.current) {
        clearTimeout(joinRetryRef.current);
        joinRetryRef.current = null;
      }
      if (!isRetry) {
        joinAttemptsRef.current = 0;
      }
      disconnectSocket();
      stopPolling();
      setSessionState("waiting");
      const socket = io(`${window.location.origin}/tunnel`, { transports: ["websocket", "polling"] });
      socketRef.current = socket;

      socket.on("connect_error", () => {
        debugLog("socket connect_error");
        setStatusSeverity("warning");
        setStatusMessage("Tunnel namespace unavailable.");
        setTunnel(null);
        setSessionState("error");
        appendStatus(setWebsocketSteps, "Relay connect error");
      });

      socket.on("disconnect", () => {
        debugLog("socket disconnect", { tunnelId: tunnel?.tunnel_id });
        stopPolling();
        if (sessionState !== "closed") {
          setSessionState("disconnected");
          setStatusSeverity("warning");
          setStatusMessage("Socket disconnected.");
          setTunnel(null);
          appendStatus(setWebsocketSteps, "Relay disconnected");
        }
      });

      socket.on("connect", async () => {
        debugLog("socket connect", { tunnelId: lease.tunnel_id });
        setMilestones((prev) => ({ ...prev, operatorAttached: true }));
        setStatusSeverity("info");
        setStatusMessage("Joining tunnel...");
        appendStatus(setWebsocketSteps, "Relay connected");
        const joinResp = await emitAsync(socket, "join", { tunnel_id: lease.tunnel_id }, 5000);
        if (joinResp?.error) {
          const attempt = (joinAttemptsRef.current += 1);
          const isTimeout = joinResp.error === "timeout";
          if (joinResp.error === "unknown_tunnel") {
            setSessionState("waiting_agent");
            setStatusSeverity("info");
            setStatusMessage("Waiting for agent to establish tunnel...");
            appendStatus(setWebsocketSteps, "Waiting for agent");
          } else if (isTimeout || joinResp.error === "attach_failed") {
            setSessionState("waiting_agent");
            setStatusSeverity("warning");
            setStatusMessage("Tunnel join timed out. Retrying...");
            appendStatus(setWebsocketSteps, `Join retry ${attempt}`);
          } else {
            debugLog("join error", joinResp);
            setSessionState("error");
            setStatusSeverity("error");
            setStatusMessage(joinResp.error);
            appendStatus(setWebsocketSteps, `Join failed: ${joinResp.error}`);
            return;
          }
          if (attempt <= 5) {
            joinRetryRef.current = setTimeout(() => connectSocket(lease, { isRetry: true }), 800);
          } else {
            setSessionState("error");
            setTunnel(null);
            setStatusSeverity("warning");
            setStatusMessage("Operator could not attach to tunnel. Try Connect again.");
            appendStatus(setWebsocketSteps, "Join failed after retries");
          }
          return;
        }
        appendStatus(setWebsocketSteps, "Relay joined");
        const dims = measureTerminal();
        debugLog("ps_open emit", { tunnelId: lease.tunnel_id, dims });
        const openResp = await emitAsync(socket, "ps_open", dims, 5000);
        if (openResp?.error && openResp.error === "ps_unsupported") {
          // Suppress warming message; channel will settle once agent attaches.
        }
        appendStatus(setShellSteps, "Opening remote shell");
        appendOutput("");
        setSessionState("waiting_agent");
        pollLoop(socket, lease.tunnel_id);
        handleResize();
      });
    },
    [appendOutput, appendStatus, disconnectSocket, emitAsync, handleResize, measureTerminal, pollLoop, sessionState, stopPolling]
  );

  const requestTunnel = useCallback(async () => {
    if (tunnel && sessionState !== "closed" && sessionState !== "idle") {
        setStatusSeverity("info");
        setStatusMessage("");
      connectSocket(tunnel);
      return;
    }
    debugLog("requestTunnel", { agentId, connectionType });
    if (!agentId) {
      setStatusSeverity("warning");
      setStatusMessage("Agent ID is required to request a tunnel.");
      return;
    }
    if (connectionType !== "ps") {
      setStatusSeverity("warning");
      setStatusMessage("Only PowerShell is supported right now.");
      return;
    }
    resetState();
    setSessionState("requesting");
      setStatusSeverity("info");
      setStatusMessage("");
    appendStatus(setTunnelSteps, "Requesting lease");
    try {
      const resp = await fetch("/api/tunnel/request", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: agentId, protocol: "ps", domain: DOMAIN_REMOTE_SHELL }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        const err = data?.error || `HTTP ${resp.status}`;
        setSessionState("error");
        setStatusSeverity(err === "domain_limit" ? "warning" : "error");
        setStatusMessage("");
        return;
      }
      setMilestones((prev) => ({ ...prev, tunnelReady: true }));
      setTunnel(data);
      setStatusMessage("");
      setSessionState("lease_issued");
      appendStatus(
        setTunnelSteps,
        data?.tunnel_id
          ? `Lease issued (${data.tunnel_id.slice(0, 8)} @ Port ${data.port || "-"})`
          : "Lease issued"
      );
      connectSocket(data);
    } catch (e) {
      setSessionState("error");
      setStatusSeverity("error");
      setStatusMessage("");
      appendStatus(setTunnelSteps, "Lease request failed");
    }
  }, [DOMAIN_REMOTE_SHELL, agentId, appendStatus, connectSocket, connectionType, resetState]);

  const handleSend = useCallback(
    async (text) => {
      const socket = socketRef.current;
      if (!socket) return;
      const payload = `${text}${text.endsWith("\n") ? "" : "\r\n"}`;
      appendOutput(`\nPS> ${text}\n`);
      setInput("");
      const resp = await emitAsync(socket, "ps_send", { data: payload });
      if (resp?.error) {
        setStatusSeverity("warning");
        setStatusMessage("");
      }
    },
    [appendOutput, emitAsync]
  );

  const isConnected = sessionState === "connected" || (psStatus?.ack && !psStatus?.closed);
  const isClosed = sessionState === "closed" || psStatus?.closed;
  const isBusy =
    sessionState === "requesting" ||
    sessionState === "waiting" ||
    sessionState === "waiting_agent" ||
    sessionState === "lease_issued";
  const canStart = Boolean(agentId) && !isBusy;

  useEffect(() => {
    const handleUnload = () => {
      stopTunnel("window_unload");
    };
    if (tunnel?.tunnel_id) {
      window.addEventListener("beforeunload", handleUnload);
      return () => window.removeEventListener("beforeunload", handleUnload);
    }
    return undefined;
  }, [stopTunnel, tunnel?.tunnel_id]);

  const sessionChips = [
    tunnel?.tunnel_id
      ? {
          label: `Tunnel ${tunnel.tunnel_id.slice(0, 8)}`,
          color: MAGIC_UI.accentB,
          icon: <LinkIcon sx={{ fontSize: 18 }} />,
        }
      : null,
    tunnel?.port
      ? {
          label: `Port ${tunnel.port}`,
          color: MAGIC_UI.accentA,
          icon: <PortIcon sx={{ fontSize: 18 }} />,
        }
      : null,
  ].filter(Boolean);

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, flexGrow: 1, minHeight: 0 }}>
      <Box>
        <Stack
          direction={{ xs: "column", md: "row" }}
          spacing={1.5}
          alignItems={{ xs: "flex-start", md: "center" }}
          justifyContent="space-between"
        >
          <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
            <TerminalIcon sx={{ fontSize: 22, color: MAGIC_UI.accentA }} />
            <Typography variant="h6" sx={{ fontWeight: 700, letterSpacing: 0.3 }}>
              Remote Shell
            </Typography>
          </Stack>

          <Stack direction={{ xs: "column", sm: "row" }} spacing={1} alignItems="center">
            <TextField
              select
              label="Connection Type"
              size="small"
              value={connectionType}
              onChange={(e) => setConnectionType(e.target.value)}
              sx={{
                minWidth: 180,
                "& .MuiInputBase-root": {
                  backgroundColor: "rgba(12,18,35,0.85)",
                  color: MAGIC_UI.textBright,
                  borderRadius: 1.5,
                },
                "& fieldset": { borderColor: MAGIC_UI.panelBorder },
                "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
              }}
            >
              <MenuItem value="ps">PowerShell</MenuItem>
            </TextField>
            <Tooltip title={isConnected ? "Disconnect session" : "Connect to agent"}>
              <span>
                <Button
                  size="small"
                  startIcon={isConnected ? <StopIcon /> : <PlayIcon />}
                  sx={gradientButtonSx}
                  disabled={!isConnected && !canStart}
                  onClick={isConnected ? handleDisconnect : requestTunnel}
                >
                  {isConnected ? "Disconnect" : "Connect"}
                </Button>
              </span>
            </Tooltip>
          </Stack>
        </Stack>

        <Stack spacing={0.3} sx={{ mt: 1.25 }}>
          <Typography
            variant="body2"
            sx={{
              color: milestones.tunnelReady ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
              fontWeight: 700,
            }}
          >
            Tunnel:{" "}
            <Typography component="span" variant="body2" sx={{ color: MAGIC_UI.textMuted, fontWeight: 500 }}>
              {tunnelSteps.join(" > ")}
            </Typography>
          </Typography>
          <Typography
            variant="body2"
            sx={{
              color: milestones.operatorAttached ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
              fontWeight: 700,
            }}
          >
            Websocket:{" "}
            <Typography component="span" variant="body2" sx={{ color: MAGIC_UI.textMuted, fontWeight: 500 }}>
              {websocketSteps.join(" > ")}
            </Typography>
          </Typography>
          <Typography
            variant="body2"
            sx={{
              color: milestones.shellEstablished ? MAGIC_UI.accentC : MAGIC_UI.textMuted,
              fontWeight: 700,
            }}
          >
            Remote Shell:{" "}
            <Typography component="span" variant="body2" sx={{ color: MAGIC_UI.textMuted, fontWeight: 500 }}>
              {shellSteps.join(" > ")}
            </Typography>
          </Typography>
        </Stack>
      </Box>

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
        {isBusy ? <LinearProgress color="info" sx={{ height: 3 }} /> : null}
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
            placeholder={
              isConnected
                ? "Enter PowerShell command and press Enter"
                : "Connect to start sending commands"
            }
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
    </Box>
  );
}
