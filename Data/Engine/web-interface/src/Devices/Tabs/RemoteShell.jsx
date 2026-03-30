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
} from "@mui/material";
import {
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  ContentCopy as CopyIcon,
  RefreshRounded as RefreshIcon,
} from "@mui/icons-material";
import { io } from "socket.io-client";

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
const OUTPUT_LIMIT = 40000;
const ANSI_OSC_PATTERN = /\u001B\][^\u0007]*(?:\u0007|\u001B\\)/g;
const ANSI_ST_PATTERN = /\u001B[P^_][\s\S]*?\u001B\\/g;
const ANSI_CSI_PATTERN = /\u001B\[[0-?]*[ -/]*[@-~]/g;
const ANSI_ESC_PATTERN = /\u001B[@-Z\\-_]/g;
const ANSI_C1_CSI_PATTERN = /\u009B[0-?]*[ -/]*[@-~]/g;

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

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function clampOutput(value) {
  return value.length > OUTPUT_LIMIT ? value.slice(value.length - OUTPUT_LIMIT) : value;
}

function inferShellKind(device) {
  const fingerprint = [
    device?.operating_system,
    device?.agent_operating_system,
    device?.os,
    device?.summary?.operating_system,
    device?.summary?.agent_operating_system,
    device?.summary?.os,
  ]
    .map(normalizeText)
    .join(" ")
    .toLowerCase();

  if (!fingerprint) return "powershell";
  if (fingerprint.includes("windows")) return "powershell";
  if (
    fingerprint.includes("linux") ||
    fingerprint.includes("ubuntu") ||
    fingerprint.includes("debian") ||
    fingerprint.includes("rocky") ||
    fingerprint.includes("centos") ||
    fingerprint.includes("red hat") ||
    fingerprint.includes("rhel") ||
    fingerprint.includes("fedora") ||
    fingerprint.includes("suse") ||
    fingerprint.includes("alpine")
  ) {
    return "bash";
  }
  return "powershell";
}

function cleanShellOutput(value) {
  if (value == null) return "";
  try {
    return String(value)
      .replace(/\r\n/g, "\n")
      .replace(/\r(?!\n)/g, "")
      .replace(ANSI_OSC_PATTERN, "")
      .replace(ANSI_ST_PATTERN, "")
      .replace(ANSI_CSI_PATTERN, "")
      .replace(ANSI_C1_CSI_PATTERN, "")
      .replace(ANSI_ESC_PATTERN, "")
      .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g, "");
  } catch {
    return "";
  }
}

export default function ReverseTunnelRemoteShell({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [shellState, setShellState] = useState("idle");
  const [tunnel, setTunnel] = useState(null);
  const [statusMessage, setStatusMessage] = useState("");
  const [output, setOutput] = useState("");
  const [input, setInput] = useState("");
  const [copyFlash, setCopyFlash] = useState(false);
  const [loading, setLoading] = useState(false);
  const socketRef = useRef(null);
  const localSocketRef = useRef(false);
  const terminalRef = useRef(null);
  const agentIdRef = useRef("");
  const activeSessionIdRef = useRef("");
  const activeAgentIdRef = useRef("");
  const previousAgentIdRef = useRef("");
  const connectAttemptRef = useRef(0);
  const shellKind = useMemo(() => inferShellKind(device), [device]);
  const promptLabel = shellKind === "bash" ? "#" : "PS>";
  const displayOutput = useMemo(() => cleanShellOutput(output), [output]);

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

  const cancelPendingConnect = useCallback(() => {
    connectAttemptRef.current += 1;
  }, []);

  useEffect(() => {
    agentIdRef.current = agentId;
  }, [agentId]);


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
    cancelPendingConnect();
    await notifyAgentOnboarding();
    activeSessionIdRef.current = "";
    activeAgentIdRef.current = "";
    setLoading(false);
    setSessionState("idle");
    setShellState("idle");
    setTunnel(null);
  }, [cancelPendingConnect, notifyAgentOnboarding]);

  const appendOutput = useCallback((text) => {
    if (!text) return;
    setOutput((prev) => {
      return clampOutput(`${prev}${text}`);
    });
  }, []);

  const appendCommandEcho = useCallback((text) => {
    if (!text) return;
    setOutput((prev) => {
      const separator = prev && !prev.endsWith("\n") ? "\n" : "";
      return clampOutput(`${prev}${separator}${promptLabel} ${text}\n`);
    });
  }, [promptLabel]);

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

  const disconnectShell = useCallback(async (reason = "operator_disconnect", targetAgentId = "") => {
    const currentAgentId = String(targetAgentId || activeAgentIdRef.current || agentIdRef.current || "").trim();
    if (!currentAgentId) return;
    try {
      await fetch("/api/shell/disconnect", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: currentAgentId, reason }),
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
    cancelPendingConnect();
    const connectedAgentId = activeAgentIdRef.current;
    setLoading(true);
    try {
      await closeShell();
      await disconnectShell("operator_disconnect", connectedAgentId);
    } finally {
      activeSessionIdRef.current = "";
      activeAgentIdRef.current = "";
      setTunnel(null);
      setShellState("closed");
      setSessionState("idle");
      setLoading(false);
    }
  }, [cancelPendingConnect, closeShell, disconnectShell]);

  useEffect(() => {
    const previousAgentId = previousAgentIdRef.current;
    previousAgentIdRef.current = agentId;
    if (!previousAgentId || previousAgentId === agentId) {
      return;
    }
    cancelPendingConnect();
    const connectedAgentId = activeAgentIdRef.current || previousAgentId;
    activeSessionIdRef.current = "";
    activeAgentIdRef.current = "";
    setLoading(false);
    setSessionState("idle");
    setShellState("idle");
    setTunnel(null);
    setStatusMessage("");
    closeShell();
    disconnectShell("component_unmount", connectedAgentId);
  }, [agentId, cancelPendingConnect, closeShell, disconnectShell]);

  useEffect(() => {
    const socket = ensureSocket();
    const handleDisconnectEvent = () => {
      cancelPendingConnect();
      activeSessionIdRef.current = "";
      activeAgentIdRef.current = "";
      setLoading(false);
      setShellState("closed");
      setSessionState("idle");
      setStatusMessage("Socket disconnected.");
    };
    const handleOutput = (payload) => {
      const currentSessionId = activeSessionIdRef.current;
      if (currentSessionId && payload?.session_id !== currentSessionId) {
        return;
      }
      const currentAgentId = activeAgentIdRef.current || agentIdRef.current;
      if (currentAgentId && payload?.agent_id && payload.agent_id !== currentAgentId) {
        return;
      }
      appendOutput(payload?.data || "");
    };
    const handleClosed = (payload) => {
      const currentSessionId = activeSessionIdRef.current;
      if (currentSessionId && payload?.session_id !== currentSessionId) {
        return;
      }
      const currentAgentId = activeAgentIdRef.current || agentIdRef.current;
      if (currentAgentId && payload?.agent_id && payload.agent_id !== currentAgentId) {
        return;
      }
      cancelPendingConnect();
      activeSessionIdRef.current = "";
      activeAgentIdRef.current = "";
      setLoading(false);
      setShellState("closed");
      setSessionState("idle");
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
  }, [appendOutput, cancelPendingConnect, ensureSocket]);

  useEffect(() => {
    return () => {
      cancelPendingConnect();
      const connectedAgentId = activeAgentIdRef.current;
      closeShell();
      disconnectShell("component_unmount", connectedAgentId);
    };
  }, [cancelPendingConnect, closeShell, disconnectShell]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      setStatusMessage("Agent ID is required to establish.");
      return;
    }
    const requestedAgentId = agentId;
    const connectAttempt = connectAttemptRef.current + 1;
    connectAttemptRef.current = connectAttempt;
    activeSessionIdRef.current = "";
    activeAgentIdRef.current = requestedAgentId;
    setLoading(true);
    setStatusMessage("");
    try {
      setSessionState("connecting");
      setShellState("opening");
      const resp = await fetch("/api/shell/establish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: requestedAgentId }),
      });
      if (connectAttemptRef.current !== connectAttempt) {
        return;
      }
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        if (data?.error === "agent_socket_missing") {
          await handleAgentOnboarding();
          return;
        }
        const detail = data?.detail ? `: ${data.detail}` : "";
        throw new Error(`${data?.error || `HTTP ${resp.status}`}${detail}`);
      }
      if (data?.agent_socket === false) {
        setStatusMessage("Agent socket is offline; attempting direct shell tunnel...");
      }
      const resolvedAgentId = normalizeText(data?.agent_id) || requestedAgentId;
      activeAgentIdRef.current = resolvedAgentId;
      setTunnel(data);

      const socket = ensureSocket();
      setStatusMessage("Waiting for shell session...");
      const opened = await emitAsync(socket, "vpn_shell_open", { agent_id: resolvedAgentId }, 35000);
      if (!opened || opened.cancelled) {
        return;
      }
      if (connectAttemptRef.current !== connectAttempt) {
        return;
      }
      if (opened.error === "agent_socket_missing") {
        await handleAgentOnboarding();
        return;
      }
      if (opened.error) {
        if (opened.error === "shell_connect_failed") {
          throw new Error("Agent shell service did not accept a WireGuard connection within 30 seconds.");
        }
        if (opened.error === "timeout") {
          throw new Error("Shell open request timed out while waiting for the agent tunnel.");
        }
        throw new Error(String(opened.error));
      }
      activeSessionIdRef.current = String(opened?.session_id || "").trim();
      setSessionState("connected");
      setShellState("connected");
      setStatusMessage("");
    } catch (err) {
      if (connectAttemptRef.current !== connectAttempt) {
        return;
      }
      activeSessionIdRef.current = "";
      activeAgentIdRef.current = "";
      setSessionState("error");
      setShellState("closed");
      setStatusMessage(String(err?.message || err || "shell_connect_failed"));
    } finally {
      if (connectAttemptRef.current === connectAttempt) {
        setLoading(false);
      }
    }
  }, [agentId, ensureSocket, handleAgentOnboarding]);

  const handleSend = useCallback(
    async (text) => {
      const socket = ensureSocket();
      if (!socket || sessionState !== "connected") return;
      const lineEnding = shellKind === "bash" ? "\n" : "\r\n";
      const payload = text.endsWith("\n") ? text : `${text}${lineEnding}`;
      appendCommandEcho(text);
      setInput("");
      const resp = await emitAsync(socket, "vpn_shell_send", { data: payload });
      if (resp?.error) {
        setStatusMessage(String(resp.error));
      }
    },
    [appendCommandEcho, ensureSocket, sessionState, shellKind]
  );

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(displayOutput || "");
      setCopyFlash(true);
      setTimeout(() => setCopyFlash(false), 1200);
    } catch {
      setCopyFlash(false);
    }
  };

  const isConnected = sessionState === "connected";
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
          {isConnected ? "Disconnect" : "Open Shell"}
        </Button>
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
          <Box
            component="pre"
            sx={{
              margin: 0,
              minHeight: "100%",
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              background: "transparent",
              color: "#e6edf3",
              fontFamily: fontFamilyMono,
              fontSize: 13,
              lineHeight: 1.5,
              pr: 4,
            }}
          >
            {displayOutput}
          </Box>
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
                ? `Type a ${shellKind === "bash" ? "shell" : "PowerShell"} command and press \`Enter\``
                : "Click the `Open Shell` button to start sending commands."
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

      <Stack spacing={0.3} sx={{ mt: 1 }}>
        <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
          Shell: {shellState === "connected" ? "Connection Established" : shellState}
        </Typography>
        {statusMessage ? (
          <Typography variant="body2" sx={{ color: shellState === "connected" ? MAGIC_UI.textMuted : "#ffb4c2" }}>
            {statusMessage}
          </Typography>
        ) : null}
      </Stack>
    </Box>
  );
}
