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
import "prismjs/themes/prism-okaidia.css";
import {
  clampOutput,
  cleanShellOutput,
  highlightShellOutput,
  inferShellKind,
  normalizeText,
  shellLanguageForKind,
} from "./remoteShellFormatting.js";

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

const waitForSocketConnect = (socket, timeoutMs = 10000) =>
  new Promise((resolve) => {
    if (!socket) {
      resolve({ error: "socket_missing" });
      return;
    }
    if (socket.connected) {
      resolve({ status: "ok" });
      return;
    }

    let settled = false;
    const cleanup = () => {
      socket.off("connect", handleConnect);
      socket.off("connect_error", handleConnectError);
      socket.off("disconnect", handleDisconnect);
      clearTimeout(timer);
    };
    const finish = (payload) => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve(payload || {});
    };
    const handleConnect = () => finish({ status: "ok" });
    const handleConnectError = (err) =>
      finish({ error: "worker_socket_connect_failed", detail: normalizeText(err?.message || err) });
    const handleDisconnect = () => finish({ error: "worker_socket_disconnected" });
    const timer = setTimeout(() => finish({ error: "worker_socket_connect_timeout" }), timeoutMs);

    socket.on("connect", handleConnect);
    socket.on("connect_error", handleConnectError);
    socket.on("disconnect", handleDisconnect);
  });

function shellSocketConfig(remoteOpsSession) {
  const routePrefix = normalizeText(remoteOpsSession?.worker?.route_path_prefix);
  if (routePrefix) {
    const normalizedPrefix = routePrefix.startsWith("/") ? routePrefix : `/${routePrefix}`;
    return {
      origin: window.location.origin,
      path: `${normalizedPrefix.replace(/\/$/, "")}/socket.io/`,
    };
  }

  const socketUrl = normalizeText(remoteOpsSession?.worker?.urls?.socket_io);
  if (!socketUrl) return null;
  try {
    const parsed = new URL(socketUrl, window.location.origin);
    return {
      origin: window.location.origin,
      path: parsed.pathname,
    };
  } catch {
    return null;
  }
}

function shellTunnelPayload(data) {
  const payload = { ...(data || {}) };
  delete payload.remote_ops_session;
  delete payload.agent_socket;
  delete payload.status;
  return payload;
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
  const socketCleanupRef = useRef(null);
  const terminalRef = useRef(null);
  const agentIdRef = useRef("");
  const activeSessionIdRef = useRef("");
  const activeAgentIdRef = useRef("");
  const previousAgentIdRef = useRef("");
  const connectAttemptRef = useRef(0);
  const shellKind = useMemo(() => inferShellKind(device), [device]);
  const shellLanguage = useMemo(() => shellLanguageForKind(shellKind), [shellKind]);
  const promptLabel = shellKind === "bash" ? "#" : "PS>";
  const displayOutput = useMemo(() => cleanShellOutput(output), [output]);
  const highlightedOutput = useMemo(
    () => highlightShellOutput(displayOutput, shellKind),
    [displayOutput, shellKind]
  );

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

  const bindShellSocketHandlers = useCallback(
    (socket) => {
      if (!socket) return () => {};
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
      };
    },
    [appendOutput, cancelPendingConnect]
  );

  const disposeShellSocket = useCallback(() => {
    if (socketCleanupRef.current) {
      socketCleanupRef.current();
      socketCleanupRef.current = null;
    }
    if (localSocketRef.current && socketRef.current) {
      socketRef.current.disconnect();
    }
    socketRef.current = null;
    localSocketRef.current = false;
  }, []);

  const replaceShellSocket = useCallback(
    (remoteOpsSession) => {
      const config = shellSocketConfig(remoteOpsSession);
      if (!config) {
        throw new Error("site_worker_shell_route_unavailable");
      }
      disposeShellSocket();
      const socket = io(config.origin, {
        path: config.path,
        transports: ["polling", "websocket"],
        tryAllTransports: true,
        forceNew: true,
      });
      socketRef.current = socket;
      localSocketRef.current = true;
      socketCleanupRef.current = bindShellSocketHandlers(socket);
      return socket;
    },
    [bindShellSocketHandlers, disposeShellSocket]
  );

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
    const socket = socketRef.current;
    if (!socket) return;
    await emitAsync(socket, "vpn_shell_close", {});
  }, []);

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
      disposeShellSocket();
    }
  }, [cancelPendingConnect, closeShell, disconnectShell, disposeShellSocket]);

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
    disposeShellSocket();
  }, [agentId, cancelPendingConnect, closeShell, disconnectShell, disposeShellSocket]);

  useEffect(() => {
    return () => {
      cancelPendingConnect();
      const connectedAgentId = activeAgentIdRef.current;
      closeShell();
      disconnectShell("component_unmount", connectedAgentId);
      disposeShellSocket();
    };
  }, [cancelPendingConnect, closeShell, disconnectShell, disposeShellSocket]);

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

      const remoteOpsSession = data?.remote_ops_session;
      if (!remoteOpsSession?.token) {
        throw new Error("Site-worker shell session token was not returned.");
      }
      const socket = replaceShellSocket(remoteOpsSession);
      setStatusMessage("Connecting to shell worker...");
      const connected = await waitForSocketConnect(socket, 10000);
      if (connected?.error) {
        const detail = connected.detail ? `: ${connected.detail}` : "";
        throw new Error(`${connected.error}${detail}`);
      }
      setStatusMessage("Waiting for shell session...");
      const opened = await emitAsync(
        socket,
        "vpn_shell_open",
        {
          agent_id: resolvedAgentId,
          operation_token: remoteOpsSession.token,
          shell_port: data?.shell_port,
          tunnel: shellTunnelPayload(data),
        },
        35000
      );
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
          throw new Error("Agent shell service did not accept a WireGuard connection.");
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
  }, [agentId, handleAgentOnboarding, replaceShellSocket]);

  const handleSend = useCallback(
    async (text) => {
      const socket = socketRef.current;
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
    [appendCommandEcho, sessionState, shellKind]
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
            className={`language-${shellLanguage}`}
            sx={{
              margin: 0,
              minHeight: "100%",
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              background: "transparent !important",
              backgroundColor: "transparent !important",
              color: "#e6edf3",
              fontFamily: fontFamilyMono,
              fontSize: 13,
              lineHeight: 1.5,
              pr: 4,
              "& code": {
                display: "block",
                fontFamily: "inherit",
                fontSize: "inherit",
                lineHeight: "inherit",
                color: "inherit",
                whiteSpace: "inherit",
                background: "transparent !important",
                backgroundColor: "transparent !important",
              },
              "&[class*='language-']": {
                background: "transparent !important",
                backgroundColor: "transparent !important",
              },
              "& code[class*='language-']": {
                background: "transparent !important",
                backgroundColor: "transparent !important",
              },
            }}
          >
            <Box
              component="code"
              className={`language-${shellLanguage}`}
              dangerouslySetInnerHTML={{ __html: highlightedOutput }}
            />
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
