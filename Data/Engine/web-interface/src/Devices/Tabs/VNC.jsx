import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Stack,
  Typography,
  LinearProgress,
  Divider,
  Switch,
  FormControlLabel,
  ToggleButton,
  ToggleButtonGroup,
} from "@mui/material";
import {
  DesktopWindowsRounded as DesktopIcon,
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  LinkRounded as LinkIcon,
  KeyboardRounded as KeyboardIcon,
  ContentPasteRounded as ClipboardIcon,
  PowerSettingsNewRounded as PowerIcon,
  AspectRatioRounded as DisplayIcon,
} from "@mui/icons-material";
import RFB from "@novnc/novnc/lib/rfb";

const MAGIC_UI = {
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  accentC: "#34d399",
  accentD: "#f472b6",
};

const SIDEBAR_THEME = {
  panel:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.95) 60%, rgba(15, 6, 26, 0.97) 100%)",
  border: "rgba(148, 163, 184, 0.28)",
  text: "#e2e8f0",
  muted: "#94a3b8",
  accent: "#7db7ff",
};

const simpleButtonSx = {
  textTransform: "none",
  fontWeight: 600,
  borderRadius: 1,
  borderColor: SIDEBAR_THEME.border,
  color: SIDEBAR_THEME.text,
  backgroundColor: "rgba(8,15,32,0.85)",
  "&:hover": {
    borderColor: "rgba(125,183,255,0.5)",
    backgroundColor: "rgba(12,18,36,0.9)",
  },
};

const deleteButtonSx = {
  textTransform: "none",
  fontWeight: 600,
  borderRadius: 1,
  color: "#f97316",
  borderColor: "rgba(249,115,22,0.6)",
  "&:hover": {
    borderColor: "rgba(249,115,22,0.8)",
    backgroundColor: "rgba(249,115,22,0.08)",
  },
};

const purgeButtonSx = {
  textTransform: "none",
  fontWeight: 600,
  borderRadius: 1,
  color: "#f43f5e",
  borderColor: "rgba(244,63,94,0.6)",
  "&:hover": {
    borderColor: "rgba(244,63,94,0.8)",
    backgroundColor: "rgba(244,63,94,0.08)",
  },
};

const sidebarSx = {
  minWidth: 280,
  maxWidth: 360,
  width: { xs: "100%", lg: 320 },
  borderRadius: 0,
  border: "none",
  background: "transparent",
  boxShadow: "none",
  p: 0,
  display: "flex",
  flexDirection: "column",
  gap: 1.5,
  overflow: "auto",
};

const sectionHeaderSx = {
  display: "flex",
  alignItems: "center",
  gap: 0.75,
  color: SIDEBAR_THEME.muted,
  fontWeight: 700,
  fontSize: "0.8rem",
  letterSpacing: "0.04em",
};

const splitToggleSx = {
  background: "rgba(9,14,25,0.9)",
  borderRadius: 1,
  border: `1px solid ${SIDEBAR_THEME.border}`,
  overflow: "hidden",
  width: "100%",
  "& .MuiToggleButton-root": {
    borderRadius: 0,
    border: "none",
    color: SIDEBAR_THEME.text,
    textTransform: "none",
    px: 1.6,
    py: 0.8,
    fontWeight: 600,
    backgroundColor: "#0f1627",
    flex: 1,
    minWidth: 0,
    "&:hover": { backgroundColor: "rgba(148,163,184,0.14)" },
  },
  "& .MuiToggleButton-root.Mui-selected": {
    color: "#0c1224 !important",
    backgroundImage: "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)",
  },
  "& .MuiToggleButton-root.Mui-selected:hover": {
    backgroundImage: "linear-gradient(135deg,#8bd8ff 0%,#c0a8ff 100%)",
  },
};

const glassCardSx = {
  borderRadius: 3,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background:
    "linear-gradient(145deg, rgba(8,12,24,0.94), rgba(10,16,30,0.9)), radial-gradient(circle at 20% 20%, rgba(125,211,252,0.08), transparent 35%)",
  boxShadow: "0 25px 80px rgba(2,6,23,0.6)",
};

const pillActionSx = {
  ...simpleButtonSx,
  minHeight: 38,
  px: 1.8,
  borderRadius: 999,
  backgroundColor: "rgba(9,14,25,0.88)",
  backdropFilter: "blur(12px)",
};

const primaryHeroActionSx = {
  ...pillActionSx,
  color: "#08111f",
  borderColor: "transparent",
  backgroundImage: "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)",
  boxShadow: "0 10px 30px rgba(91,126,255,0.25)",
  "&:hover": {
    borderColor: "transparent",
    backgroundImage: "linear-gradient(135deg,#95dcff 0%,#c1a9ff 100%)",
    backgroundColor: "transparent",
  },
};

const sidebarCardSx = {
  borderRadius: 2.5,
  border: `1px solid ${SIDEBAR_THEME.border}`,
  background:
    "linear-gradient(160deg, rgba(15,22,41,0.92) 0%, rgba(10,15,29,0.96) 100%)",
  boxShadow: "0 16px 40px rgba(2,6,23,0.38)",
  p: 1.4,
  display: "flex",
  flexDirection: "column",
  gap: 1.1,
};

const compactSidebarCardSx = {
  ...sidebarCardSx,
  gap: 0.85,
};

const stageBadgeSx = (accent) => ({
  display: "inline-flex",
  alignItems: "center",
  gap: 0.55,
  borderRadius: 999,
  px: 1,
  py: 0.45,
  border: `1px solid ${accent}`,
  color: accent,
  backgroundColor: "rgba(8,14,26,0.55)",
  fontSize: "0.75rem",
  fontWeight: 700,
  lineHeight: 1,
});

const viewportPresets = [
  { value: "all", label: "All" },
  { value: "left", label: "Left" },
  { value: "right", label: "Right" },
];

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function summarizeStatus(message, limit = 120) {
  if (!message) return "";
  const text = String(message).trim();
  if (text.length <= limit) return text;
  const sliceLimit = Math.max(0, limit - 3);
  return `${text.slice(0, sliceLimit)}...`;
}

function buildWsUrl(wsPath, token) {
  const pathText = normalizeText(wsPath);
  if (!pathText || typeof window === "undefined") return "";
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const parsed = new URL(pathText, `${protocol}//${window.location.host}`);
  if (token && !parsed.searchParams.get("token")) {
    parsed.searchParams.set("token", token);
  }
  return parsed.toString();
}

function keysymFromChar(char) {
  if (!char) return null;
  if (char === "\n" || char === "\r") return 0xff0d;
  if (char === "\t") return 0xff09;
  if (char === "\b") return 0xff08;
  if (char === "\x1b") return 0xff1b;
  const code = char.codePointAt(0);
  if (!code) return null;
  return code;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function buildRetryableError(message, retryable = true) {
  const error = message instanceof Error ? message : new Error(String(message || "Request failed."));
  error.retryable = retryable;
  return error;
}

const VNC_AUTO_RETRY_ATTEMPTS = 3;
const VNC_AUTO_RETRY_DELAY_MS = 1500;
const VNC_OPEN_TIMEOUT_MS = 12000;

async function writeClipboardText(text) {
  if (!navigator?.clipboard?.writeText) return;
  try {
    await navigator.clipboard.writeText(text || "");
  } catch {
    // ignore
  }
}

function capabilityFlag(value) {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  const text = normalizeText(value).toLowerCase();
  if (!text) return false;
  return ["1", "true", "yes", "on", "supported", "available"].includes(text);
}

function supportsPowerAction(capabilities, action) {
  const power = capabilities?.power;
  if (capabilityFlag(power)) return true;
  if (!power || typeof power !== "object") return false;
  const aliases = {
    shutdown: ["shutdown", "machineShutdown"],
    reboot: ["reboot", "restart", "machineReboot"],
    reset: ["reset", "machineReset"],
  };
  return (aliases[action] || []).some((key) => capabilityFlag(power?.[key]));
}

export default function ReverseTunnelVnc({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [vncStage, setVncStage] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [viewOnly, setViewOnly] = useState(false);
  const [clipboardSync, setClipboardSync] = useState(false);
  const [sessionId, setSessionId] = useState("");
  const [, setParticipantId] = useState("");
  const [sessionDetails, setSessionDetails] = useState(null);
  const performanceLevel = 2;
  const [scaleViewport, setScaleViewport] = useState(true);
  const [clipViewport, setClipViewport] = useState(true);
  const [resizeSession, setResizeSession] = useState(true);
  const [capabilities, setCapabilities] = useState({});
  const [viewportPreset, setViewportPreset] = useState("all");
  const [viewportHint, setViewportHint] = useState("");

  const containerRef = useRef(null);
  const displayRef = useRef(null);
  const rfbRef = useRef(null);
  const agentIdRef = useRef("");
  const sessionIdRef = useRef("");
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const connectAttemptRef = useRef(0);
  const manualDisconnectRef = useRef(false);

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
  const qualityLevel = Math.min(8, Math.max(0, performanceLevel));
  const compressionLevel = Math.max(0, 8 - qualityLevel);
  const dragViewport = false;
  const effectiveViewOnly = viewOnly;

  const applySessionBootstrap = useCallback((data) => {
    const nextSession = data?.session && typeof data.session === "object" ? data.session : null;
    const nextSessionId = normalizeText(data?.session_id || nextSession?.session_id);
    const nextParticipantId = normalizeText(data?.participant_id || nextSession?.current_participant_id);
    setSessionId(nextSessionId);
    setParticipantId(nextParticipantId);
    setSessionDetails(nextSession);
  }, []);

  useEffect(() => {
    agentIdRef.current = agentId;
  }, [agentId]);

  useEffect(() => {
    clipboardSyncRef.current = clipboardSync;
  }, [clipboardSync]);

  useEffect(() => {
    sessionIdRef.current = sessionId;
  }, [sessionId]);

  const cancelPendingConnect = useCallback(() => {
    connectAttemptRef.current += 1;
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
    await notifyAgentOnboarding();
    setStatusMessage("Agent Onboarding Underway.");
    setSessionState("idle");
    setVncStage("agent_onboarding");
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
        body: JSON.stringify({
          agent_id: currentAgentId,
          session_id: sessionIdRef.current || undefined,
          reason,
        }),
      });
    } catch {
      // best-effort
    }
  }, []);

  const handleDisconnect = useCallback(async () => {
    cancelPendingConnect();
    manualDisconnectRef.current = true;
    setVncStage("disconnecting");
    setLoading(true);
    setStatusMessage("");
    try {
      teardownDisplay();
      await disconnectVnc("operator_disconnect");
    } finally {
      setSessionState("idle");
      setSessionId("");
      setParticipantId("");
      setSessionDetails(null);
      clipboardLastRef.current = "";
      setLoading(false);
    }
  }, [cancelPendingConnect, disconnectVnc, teardownDisplay]);

  useEffect(() => {
    return () => {
      cancelPendingConnect();
      teardownDisplay();
      disconnectVnc("component_unmount");
    };
  }, [cancelPendingConnect, disconnectVnc, teardownDisplay]);

  useEffect(() => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    rfb.showDotCursor = true;
    rfb.viewOnly = effectiveViewOnly;
    rfb.clipViewport = clipViewport;
    rfb.dragViewport = dragViewport;
    rfb.scaleViewport = scaleViewport;
    rfb.resizeSession = resizeSession;
    rfb.qualityLevel = qualityLevel;
    rfb.compressionLevel = compressionLevel;
  }, [
    effectiveViewOnly,
    clipViewport,
    dragViewport,
    scaleViewport,
    resizeSession,
    qualityLevel,
    compressionLevel,
  ]);

  const requestTunnel = useCallback(async () => {
    if (!agentId) {
      throw buildRetryableError("Agent ID is required to establish.", false);
    }
    setStatusMessage("");
    setVncStage("requesting_tunnel");
    setSessionState("connecting");
    try {
      const resp = await fetch("/api/vnc/establish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: agentId, remove_wallpaper: true }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        if (data?.error === "agent_socket_missing") {
          await handleAgentOnboarding();
          return null;
        }
        const detail = data?.detail ? `: ${data.detail}` : "";
        const status = Number(resp.status || 0);
        throw buildRetryableError(
          `${data?.error || `HTTP ${resp.status}`}${detail}`,
          status >= 500 || status === 429 || status === 408
        );
      }
      applySessionBootstrap(data);
      return data;
    } catch (err) {
      if (err?.retryable !== false) {
        throw buildRetryableError(err?.message || err, true);
      }
      throw err;
    }
  }, [agentId, applySessionBootstrap, handleAgentOnboarding]);

  const openVncSession = useCallback(
    async (data, options = {}) => {
      const connectToken = Number(options.connectToken || 0);
      const attempt = Number(options.attempt || 1);
      const maxAttempts = Number(options.maxAttempts || VNC_AUTO_RETRY_ATTEMPTS);
      const wsUrl = data?.ws_url || buildWsUrl(data?.ws_path, data?.token);
      const vncPassword = data?.vnc_password || "";
      if (!wsUrl) {
        throw new Error("VNC session unavailable.");
      }
      const tunnelUrl = wsUrl;
      const displayHost = displayRef.current;
      if (!displayHost) {
        throw new Error("VNC display container missing.");
      }
      displayHost.innerHTML = "";

      setVncStage("connecting_ws");
      const rfb = new RFB(displayHost, tunnelUrl, {
        credentials: { password: vncPassword },
      });
      // Always show a local dot cursor so we never lose pointer visibility.
      rfb.showDotCursor = true;
      rfb.scaleViewport = scaleViewport;
      rfb.resizeSession = resizeSession;
      rfb.clipViewport = clipViewport;
      rfb.dragViewport = dragViewport;
      rfb.viewOnly = effectiveViewOnly;
      rfb.qualityLevel = qualityLevel;
      rfb.compressionLevel = compressionLevel;

      rfbRef.current = rfb;
      setStatusMessage(
        attempt > 1 ? `Establishing VNC... attempt ${attempt}/${maxAttempts}` : "Establishing VNC..."
      );

      return await new Promise((resolve, reject) => {
        let settled = false;
        let connected = false;

        const isStaleAttempt = () => connectToken && connectAttemptRef.current !== connectToken;

        const cleanupListeners = () => {
          try {
            rfb.removeEventListener("connect", handleConnectEvent);
            rfb.removeEventListener("disconnect", handleDisconnectEvent);
            rfb.removeEventListener("securityfailure", handleSecurityFailure);
            rfb.removeEventListener("credentialsrequired", handleCredentialsRequired);
            rfb.removeEventListener("capabilities", handleCapabilities);
            rfb.removeEventListener("clipboard", handleClipboard);
          } catch {
            /* ignore */
          }
        };

        const finishResolve = () => {
          if (settled) return;
          settled = true;
          clearTimeout(timeoutId);
          cleanupListeners();
          resolve();
        };

        const finishReject = (error, retryable = true) => {
          if (settled) return;
          settled = true;
          clearTimeout(timeoutId);
          cleanupListeners();
          const err = error instanceof Error ? error : new Error(String(error || "VNC session unavailable."));
          err.retryable = retryable;
          reject(err);
        };

        const handleConnectEvent = () => {
          if (isStaleAttempt()) {
            try {
              rfb.disconnect();
            } catch {
              /* ignore */
            }
            return;
          }
          connected = true;
          setSessionState("connected");
          setVncStage("connected");
          setStatusMessage("");
          finishResolve();
        };

        const handleDisconnectEvent = () => {
          rfbRef.current = null;
          if (isStaleAttempt()) return;
          if (!connected) {
            setSessionState("connecting");
            setVncStage("retrying");
            finishReject("VNC proxy disconnected before the desktop became available.", true);
            return;
          }
          const wasManual = manualDisconnectRef.current;
          manualDisconnectRef.current = false;
          setSessionState("idle");
          setVncStage((prev) =>
            prev === "auth_failed" || prev === "error" ? prev : "disconnected"
          );
          if (!wasManual && sessionIdRef.current) {
            setStatusMessage("Desktop stream closed. Reconnect or claim control when the session is ready.");
          }
        };

        const handleSecurityFailure = (evt) => {
          if (isStaleAttempt()) return;
          const detail = evt?.detail?.reason ? ` (${evt.detail.reason})` : "";
          setSessionState("error");
          setVncStage("auth_failed");
          setStatusMessage(`VNC authentication failed${detail}.`);
          finishReject(`VNC authentication failed${detail}.`, false);
        };

        const handleCredentialsRequired = () => {
          if (isStaleAttempt()) return;
          setVncStage("handshaking");
          try {
            rfb.sendCredentials({ password: vncPassword });
          } catch {
            /* ignore */
          }
        };

        const handleCapabilities = (evt) => {
          if (isStaleAttempt()) return;
          const caps = evt?.detail?.capabilities || rfb.capabilities || {};
          setCapabilities(caps || {});
        };

        const handleClipboard = (evt) => {
          if (isStaleAttempt()) return;
          const text = evt?.detail?.text || "";
          clipboardLastRef.current = text;
          if (clipboardSyncRef.current) {
            writeClipboardText(text);
          }
        };

        const timeoutId = setTimeout(() => {
          if (connected) return;
          try {
            rfb.disconnect();
          } catch {
            /* ignore */
          }
          finishReject("VNC connection timed out.", true);
        }, VNC_OPEN_TIMEOUT_MS);

        rfb.addEventListener("connect", handleConnectEvent);
        rfb.addEventListener("disconnect", handleDisconnectEvent);
        rfb.addEventListener("securityfailure", handleSecurityFailure);
        rfb.addEventListener("credentialsrequired", handleCredentialsRequired);
        rfb.addEventListener("capabilities", handleCapabilities);
        rfb.addEventListener("clipboard", handleClipboard);
      });
    },
    [
      clipViewport,
      compressionLevel,
      dragViewport,
      qualityLevel,
      resizeSession,
      scaleViewport,
      effectiveViewOnly,
    ]
  );

  const handleConnect = useCallback(async () => {
    if (sessionState === "connected" || loading) return;
    const connectToken = connectAttemptRef.current + 1;
    connectAttemptRef.current = connectToken;
    manualDisconnectRef.current = false;
    setStatusMessage("");
    setLoading(true);
    setSessionState("connecting");
    setCapabilities({});
    try {
      for (let attempt = 1; attempt <= VNC_AUTO_RETRY_ATTEMPTS; attempt += 1) {
        if (connectAttemptRef.current !== connectToken) return;
        if (attempt > 1) {
          setSessionState("connecting");
          setVncStage("retrying");
          setStatusMessage(`Retrying VNC... (${attempt}/${VNC_AUTO_RETRY_ATTEMPTS})`);
          await sleep(VNC_AUTO_RETRY_DELAY_MS);
          if (connectAttemptRef.current !== connectToken) return;
        }
        try {
          const sessionData = await requestTunnel();
          if (!sessionData || connectAttemptRef.current !== connectToken) {
            return;
          }
          await openVncSession(sessionData, {
            connectToken,
            attempt,
            maxAttempts: VNC_AUTO_RETRY_ATTEMPTS,
          });
          return;
        } catch (err) {
          teardownDisplay();
          const retryable = err?.retryable !== false;
          if (!retryable || attempt >= VNC_AUTO_RETRY_ATTEMPTS) {
            throw err;
          }
        }
      }
    } catch (err) {
      if (connectAttemptRef.current !== connectToken) return;
      setSessionState("error");
      setStatusMessage(String(err.message || err));
      setVncStage("error");
    } finally {
      if (connectAttemptRef.current === connectToken) {
        setLoading(false);
      }
    }
  }, [loading, openVncSession, requestTunnel, sessionState, teardownDisplay]);

  const refreshSessionDetails = useCallback(async () => {
    const currentSessionId = sessionIdRef.current;
    if (!currentSessionId) return;
    try {
      const resp = await fetch(`/api/vnc/sessions?session_id=${encodeURIComponent(currentSessionId)}`, {
        credentials: "include",
      });
      if (!resp.ok) return;
      const data = await resp.json().catch(() => ({}));
      const nextSession = Array.isArray(data?.sessions) ? data.sessions[0] || null : null;
      if (!nextSession) {
        teardownDisplay();
        setSessionState("idle");
        setVncStage("disconnected");
        setSessionDetails(null);
        setSessionId("");
        setParticipantId("");
        return;
      }
      setSessionDetails(nextSession);
      setParticipantId((previous) => normalizeText(nextSession.current_participant_id) || previous);
    } catch {
      // ignore background session refresh failures
    }
  }, [teardownDisplay]);

  useEffect(() => {
    if (!sessionId) return undefined;
    refreshSessionDetails();
    const intervalId = window.setInterval(() => {
      refreshSessionDetails();
    }, 4000);
    return () => {
      window.clearInterval(intervalId);
    };
  }, [refreshSessionDetails, sessionId]);

  const injectClipboardKeystrokes = useCallback(async (prefilledText = "") => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    if (effectiveViewOnly) {
      setStatusMessage("Clipboard input is disabled while view only is enabled.");
      return;
    }
    let text = prefilledText || "";
    if (!text && navigator?.clipboard?.readText) {
      try {
        text = await navigator.clipboard.readText();
      } catch {
        text = "";
      }
    }
    if (!text) {
      setStatusMessage("Clipboard is empty or unavailable.");
      return;
    }
    try {
      const current = rfbRef.current;
      for (const char of text) {
        if (rfbRef.current !== current) break;
        const keysym = keysymFromChar(char);
        if (keysym == null) continue;
        rfb.sendKey(keysym, null);
        // Slow down to improve reliability on large pastes.
        await sleep(20);
      }
      setStatusMessage("Clipboard injected as keystrokes.");
    } catch {
      setStatusMessage("Failed to inject keystrokes.");
    }
  }, [effectiveViewOnly]);

  const pasteClipboardText = useCallback(async () => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    if (effectiveViewOnly) {
      setStatusMessage("Clipboard input is disabled while view only is enabled.");
      return;
    }
    let text = "";
    if (navigator?.clipboard?.readText) {
      try {
        text = await navigator.clipboard.readText();
      } catch {
        text = "";
      }
    }
    if (!text) {
      setStatusMessage("Clipboard is empty or unavailable.");
      return;
    }
    if (typeof rfb.clipboardPasteFrom === "function") {
      try {
        rfb.clipboardPasteFrom(text);
        setStatusMessage("Clipboard pasted.");
        return;
      } catch {
        // fall back to keystroke injection below
      }
    }
    await injectClipboardKeystrokes(text);
  }, [effectiveViewOnly, injectClipboardKeystrokes]);

  const handleCtrlAltDel = useCallback(() => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    if (effectiveViewOnly) {
      setStatusMessage("Keyboard input is disabled while view only is enabled.");
      return;
    }
    try {
      rfb.sendCtrlAltDel();
      setStatusMessage("Sent Ctrl+Alt+Del.");
    } catch {
      setStatusMessage("Failed to send Ctrl+Alt+Del.");
    }
  }, [effectiveViewOnly]);

  const handlePowerAction = useCallback(
    (action) => {
      const rfb = rfbRef.current;
      if (!rfb) return;
      if (effectiveViewOnly) {
        setStatusMessage("Power controls are disabled while view only is enabled.");
        return;
      }
      const confirmed = window.confirm(`Send ${action} request to the remote machine?`);
      if (!confirmed) return;
      try {
        if (action === "shutdown") {
          rfb.machineShutdown();
        } else if (action === "reboot") {
          rfb.machineReboot();
        } else if (action === "reset") {
          rfb.machineReset();
        }
        setStatusMessage(`Power action sent: ${action}.`);
      } catch {
        setStatusMessage("Failed to send power command.");
      }
    },
    [effectiveViewOnly]
  );

  const applyViewportPreset = useCallback((value) => {
    setViewportPreset(value);
    const rfb = rfbRef.current;
    const display = rfb?._display;
    if (!display) {
      setViewportHint("Viewport controls unavailable.");
      return;
    }
    if (rfb) {
      rfb.clipViewport = true;
      rfb.dragViewport = false;
    }
    const fbWidth = display?._fb_width || display?._fbWidth || rfb?._fbWidth || 0;
    const fbHeight = display?._fb_height || display?._fbHeight || rfb?._fbHeight || 0;
    const viewportLoc = display?._viewportLoc || { x: 0, y: 0 };
    if (!fbWidth || !fbHeight) {
      setViewportHint("Framebuffer size unavailable.");
      return;
    }
    let target = { x: 0, y: 0, w: fbWidth, h: fbHeight };
    switch (value) {
      case "left":
        target = { x: 0, y: 0, w: Math.floor(fbWidth / 2), h: fbHeight };
        break;
      case "right":
        target = { x: Math.floor(fbWidth / 2), y: 0, w: Math.floor(fbWidth / 2), h: fbHeight };
        break;
      default:
        target = { x: 0, y: 0, w: fbWidth, h: fbHeight };
        break;
    }

    setClipViewport(true);

    const applyViewport = () => {
      try {
        if (typeof display.viewportChangeSize === "function") {
          display.viewportChangeSize(target.w, target.h);
        }
        if (typeof display.viewportChangePos === "function") {
          display.viewportChangePos(target.x - viewportLoc.x, target.y - viewportLoc.y);
        } else if (typeof display.viewportChange === "function") {
          display.viewportChange(target.x - viewportLoc.x, target.y - viewportLoc.y, target.w, target.h);
        } else if (displayRef.current) {
          displayRef.current.scrollLeft = target.x;
          displayRef.current.scrollTop = target.y;
        }
        setViewportHint("");
      } catch {
        setViewportHint("Viewport controls not available.");
      }
    };

    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(applyViewport);
    } else {
      applyViewport();
    }
  }, []);

  const isConnected = sessionState === "connected";
  const sessionStateLabel = normalizeText(sessionDetails?.state || "idle") || "idle";
  const controllerOperator = normalizeText(sessionDetails?.controller_operator_id) || "Unassigned";
  const shutdownSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "shutdown");
  const rebootSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "reboot");
  const resetSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "reset");
  const vncStageInfo = useMemo(() => {
    const errorDetail = summarizeStatus(statusMessage) || "VNC session encountered an error.";
    switch (vncStage) {
      case "requesting_tunnel":
        return {
          label: "Requesting Tunnel",
          detail: "Engine preparing WireGuard + VNC credentials.",
          accent: MAGIC_UI.accentA,
          detailTone: MAGIC_UI.textMuted,
        };
      case "connecting_ws":
        return {
          label: "Connecting noVNC",
          detail: "Opening WebSocket to the VNC proxy.",
          accent: MAGIC_UI.accentA,
          detailTone: MAGIC_UI.textMuted,
        };
      case "handshaking":
        return {
          label: "Handshaking",
          detail: "Authenticating with UltraVNC and negotiating display.",
          accent: MAGIC_UI.accentB,
          detailTone: MAGIC_UI.textMuted,
        };
      case "connected":
        return {
          label: "Live",
          detail: "Desktop stream active.",
          accent: MAGIC_UI.accentC,
          detailTone: MAGIC_UI.textMuted,
        };
      case "disconnecting":
        return {
          label: "Disconnecting",
          detail: "Closing the VNC session.",
          accent: MAGIC_UI.accentD,
          detailTone: MAGIC_UI.textMuted,
        };
      case "disconnected":
        return {
          label: "Disconnected",
          detail: "Session closed. Ready to reconnect.",
          accent: "rgba(148, 163, 184, 0.6)",
          detailTone: MAGIC_UI.textMuted,
        };
      case "retrying":
        return {
          label: "Retrying",
          detail: summarizeStatus(statusMessage) || "Retrying VNC after a transient tunnel or proxy failure.",
          accent: MAGIC_UI.accentB,
          detailTone: MAGIC_UI.textMuted,
        };
      case "agent_onboarding":
        return {
          label: "Agent Onboarding",
          detail: "Waiting for the agent tunnel to finish onboarding.",
          accent: MAGIC_UI.accentB,
          detailTone: MAGIC_UI.textMuted,
        };
      case "auth_failed":
        return {
          label: "Authentication Failed",
          detail: "UltraVNC rejected the credentials.",
          accent: "#ff7b89",
          detailTone: "#ff7b89",
        };
      case "error":
        return {
          label: "Error",
          detail: errorDetail,
          accent: "#ff7b89",
          detailTone: "#ff7b89",
        };
      default:
        return {
          label: "Idle",
          detail: "Waiting for operator to connect.",
          accent: "rgba(148, 163, 184, 0.6)",
          detailTone: MAGIC_UI.textMuted,
        };
    }
  }, [statusMessage, vncStage]);

  const accessLabel = isConnected ? (effectiveViewOnly ? "View only" : "Interactive") : "Disconnected";
  const showClipboardActions = isConnected;
  const showPowerButtons = isConnected;
  const showHeaderStatus =
    Boolean(statusMessage) &&
    (loading || vncStage === "retrying" || vncStage === "agent_onboarding" || vncStage === "error" || vncStage === "auth_failed");

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, flexGrow: 1, minHeight: 0 }}>
      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", lg: "minmax(0,1fr) 320px" },
          gap: 2,
          flexGrow: 1,
          minHeight: 0,
        }}
      >
        <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, minHeight: 0 }}>
          <Box
            ref={containerRef}
            sx={{
              flexGrow: 1,
              minHeight: 420,
              display: "flex",
              flexDirection: "column",
              ...glassCardSx,
              overflow: "hidden",
              position: "relative",
            }}
          >
            {loading ? <LinearProgress color="info" sx={{ height: 3 }} /> : null}
            <Box
              sx={{
                display: "flex",
                justifyContent: "flex-start",
                alignItems: "center",
                gap: 1,
                px: 1.5,
                py: 1.1,
                borderBottom: "1px solid rgba(148,163,184,0.15)",
                background:
                  "linear-gradient(180deg, rgba(12,18,33,0.94) 0%, rgba(9,14,26,0.88) 100%)",
              }}
            >
              <Stack spacing={showHeaderStatus ? 0.2 : 0}>
                <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap">
                  <Box
                    sx={{
                      width: 8,
                      height: 8,
                      borderRadius: "50%",
                      backgroundColor: vncStageInfo.accent,
                      boxShadow: `0 0 14px ${vncStageInfo.accent}`,
                    }}
                  />
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                    Remote Desktop
                  </Typography>
                  <Box sx={stageBadgeSx(vncStageInfo.accent)}>{vncStageInfo.label}</Box>
                </Stack>
                {showHeaderStatus ? (
                  <Typography variant="caption" sx={{ color: vncStageInfo.detailTone || MAGIC_UI.textMuted }}>
                    {statusMessage}
                  </Typography>
                ) : null}
              </Stack>
            </Box>
            <Box
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
                <Stack
                  spacing={1.5}
                  sx={{
                    position: "absolute",
                    alignItems: "center",
                    textAlign: "center",
                    px: 2,
                    maxWidth: 320,
                  }}
                >
                  <DesktopIcon sx={{ color: MAGIC_UI.accentA, fontSize: 40 }} />
                  <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                    Ready to connect
                  </Typography>
                  <Button
                    size="medium"
                    startIcon={<PlayIcon />}
                    variant="outlined"
                    sx={primaryHeroActionSx}
                    disabled={loading || !agentId}
                    onClick={handleConnect}
                  >
                    {loading ? "Preparing Desktop" : sessionId ? "Reconnect Remote Desktop" : "Launch Remote Desktop"}
                  </Button>
                </Stack>
              ) : null}
            </Box>
          </Box>

        </Box>

        <Box sx={sidebarSx}>
          <Box sx={compactSidebarCardSx}>
            <Box sx={sectionHeaderSx}>
              <LinkIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Session</span>
            </Box>
            <Stack spacing={0.9}>
              <Stack spacing={0.7}>
                <Typography variant="body2" sx={{ color: SIDEBAR_THEME.text }}>
                  Access: {accessLabel}
                </Typography>
                <Typography variant="body2" sx={{ color: SIDEBAR_THEME.text }}>
                  State: {sessionStateLabel}
                </Typography>
                <Typography variant="body2" sx={{ color: SIDEBAR_THEME.text }}>
                  Owner: {controllerOperator}
                </Typography>
              </Stack>
            </Stack>
          </Box>

          <Box sx={compactSidebarCardSx}>
            <Box sx={sectionHeaderSx}>
              <DisplayIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Display & Focus</span>
            </Box>
            <Stack spacing={1}>
              <ToggleButtonGroup
                exclusive
                size="small"
                value={scaleViewport ? "fit" : "actual"}
                onChange={(_, value) => {
                  if (!value) return;
                  setScaleViewport(value === "fit");
                }}
                sx={splitToggleSx}
              >
                <ToggleButton value="fit">Fit Screen</ToggleButton>
                <ToggleButton value="actual">Actual Size</ToggleButton>
              </ToggleButtonGroup>

              <FormControlLabel
                control={
                  <Switch
                    checked={effectiveViewOnly}
                    onChange={(event) => setViewOnly(event.target.checked)}
                    size="small"
                    color="info"
                  />
                }
                label={<Typography variant="body2">View only</Typography>}
              />

              <Divider sx={{ borderColor: "rgba(148,163,184,0.15)" }} />

              <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                Monitors
              </Typography>
              <ToggleButtonGroup
                exclusive
                size="small"
                value={viewportPreset}
                onChange={(_, value) => {
                  if (!value) return;
                  applyViewportPreset(value);
                }}
                sx={splitToggleSx}
              >
                {viewportPresets.map((preset) => (
                  <ToggleButton key={preset.value} value={preset.value}>
                    {preset.label}
                  </ToggleButton>
                ))}
              </ToggleButtonGroup>
              {viewportHint ? (
                <Typography variant="caption" sx={{ color: "#ffb86b" }}>
                  {viewportHint}
                </Typography>
              ) : null}
            </Stack>
          </Box>

          <Box sx={compactSidebarCardSx}>
            <Box sx={sectionHeaderSx}>
              <ClipboardIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Clipboard & Keys</span>
            </Box>
            <Stack spacing={1}>
              <FormControlLabel
                control={
                  <Switch
                    checked={clipboardSync}
                    onChange={(event) => setClipboardSync(event.target.checked)}
                    size="small"
                    color="info"
                  />
                }
                label={<Typography variant="body2">Sync remote clipboard to browser</Typography>}
              />
              <Button
                size="small"
                variant="outlined"
                startIcon={<ClipboardIcon />}
                sx={simpleButtonSx}
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={pasteClipboardText}
              >
                Paste Clipboard
              </Button>
              <Button
                size="small"
                variant="outlined"
                startIcon={<KeyboardIcon />}
                sx={simpleButtonSx}
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={() => injectClipboardKeystrokes()}
              >
                Inject Keystrokes
              </Button>
              <Button
                size="small"
                variant="outlined"
                startIcon={<KeyboardIcon />}
                sx={simpleButtonSx}
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={handleCtrlAltDel}
              >
                Send Ctrl+Alt+Del
              </Button>
            </Stack>
          </Box>

          <Box sx={compactSidebarCardSx}>
            <Box sx={sectionHeaderSx}>
              <PowerIcon sx={{ fontSize: 18, color: "#f97316" }} />
              <span>Power</span>
            </Box>
            <Stack spacing={1}>
              <Stack direction={{ xs: "column", sm: "row" }} spacing={1}>
                <Button
                  size="small"
                  variant="outlined"
                  sx={purgeButtonSx}
                  disabled={!showPowerButtons || !shutdownSupported}
                  onClick={() => handlePowerAction("shutdown")}
                >
                  Shutdown
                </Button>
                <Button
                  size="small"
                  variant="outlined"
                  sx={deleteButtonSx}
                  disabled={!showPowerButtons || !rebootSupported}
                  onClick={() => handlePowerAction("reboot")}
                >
                  Restart
                </Button>
                <Button
                  size="small"
                  variant="outlined"
                  sx={simpleButtonSx}
                  disabled={!showPowerButtons || !resetSupported}
                  onClick={() => handlePowerAction("reset")}
                >
                  Reset
                </Button>
              </Stack>
            </Stack>
          </Box>

          <Box
            sx={{
              mt: "auto",
              display: "flex",
              justifyContent: "flex-end",
            }}
          >
            <Button
              size="small"
              variant="outlined"
              startIcon={<StopIcon />}
              sx={{
                ...simpleButtonSx,
                minHeight: 36,
                px: 1.8,
                minWidth: 132,
              }}
              disabled={!isConnected}
              onClick={handleDisconnect}
            >
              Disconnect
            </Button>
          </Box>

        </Box>
      </Box>
    </Box>
  );
}
