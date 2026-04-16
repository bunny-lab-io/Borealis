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

const VNC_STAGE_BACKGROUND = "#0b1325";
const VNC_CANVAS_BOX_SHADOW =
  "0 0 0 1px rgba(125, 183, 255, 0.18), 0 18px 42px rgba(2, 6, 23, 0.4)";

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
  "&, & .MuiButton-startIcon": {
    color: "#08111f",
  },
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

function coerceInteger(value, defaultValue = 0) {
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : defaultValue;
}

function normalizeDisplayTopology(value) {
  if (!Array.isArray(value)) return [];
  return value
    .filter((item) => item && typeof item === "object")
    .map((item, index) => {
      const left = coerceInteger(item.left, 0);
      const top = coerceInteger(item.top, 0);
      const right = coerceInteger(item.right, left + coerceInteger(item.width, 0));
      const bottom = coerceInteger(item.bottom, top + coerceInteger(item.height, 0));
      const width = Math.max(0, coerceInteger(item.width, right - left));
      const height = Math.max(0, coerceInteger(item.height, bottom - top));
      const displayIndex = Math.max(
        1,
        coerceInteger(item.display_index ?? item.id ?? item.label, index + 1)
      );
      return {
        id: normalizeText(item.id || displayIndex) || String(displayIndex),
        displayIndex,
        label: normalizeText(item.label || displayIndex) || String(displayIndex),
        deviceName: normalizeText(item.device_name),
        left,
        top,
        right: right > left ? right : left + width,
        bottom: bottom > top ? bottom : top + height,
        width,
        height,
        primary: capabilityFlag(item.primary),
      };
    })
    .filter((item) => item.width > 0 && item.height > 0)
    .sort((left, right) => {
      if (left.displayIndex !== right.displayIndex) return left.displayIndex - right.displayIndex;
      if (left.primary !== right.primary) return left.primary ? -1 : 1;
      if (left.top !== right.top) return left.top - right.top;
      return left.left - right.left;
    });
}

function displayTopologyBounds(topology) {
  if (!Array.isArray(topology) || !topology.length) return null;
  const left = Math.min(...topology.map((item) => item.left));
  const top = Math.min(...topology.map((item) => item.top));
  const right = Math.max(...topology.map((item) => item.right));
  const bottom = Math.max(...topology.map((item) => item.bottom));
  return {
    left,
    top,
    right,
    bottom,
    width: Math.max(0, right - left),
    height: Math.max(0, bottom - top),
  };
}

function topologyMatchesFramebuffer(topologyBounds, framebufferSize) {
  if (!topologyBounds || !framebufferSize?.width || !framebufferSize?.height) return true;
  const widthTolerance = Math.max(96, Math.round(framebufferSize.width * 0.18));
  const heightTolerance = Math.max(96, Math.round(framebufferSize.height * 0.18));
  return (
    Math.abs(topologyBounds.width - framebufferSize.width) <= widthTolerance &&
    Math.abs(topologyBounds.height - framebufferSize.height) <= heightTolerance
  );
}

function monitorSelectionId(monitor) {
  return normalizeText(monitor?.id || monitor?.displayIndex || "");
}

function preferredMonitorSelection(topology) {
  if (!Array.isArray(topology) || !topology.length) return [];
  const primary = topology.find((item) => item.primary) || topology[0];
  const monitorId = monitorSelectionId(primary);
  return monitorId ? [monitorId] : [];
}

function normalizeMonitorSelection(selectionIds, topology) {
  const availableIds = new Set(
    (Array.isArray(topology) ? topology : [])
      .map((item) => monitorSelectionId(item))
      .filter(Boolean)
  );
  if (!availableIds.size) return [];
  const normalized = Array.isArray(selectionIds)
    ? selectionIds.map((item) => normalizeText(item)).filter((item) => availableIds.has(item))
    : [];
  if (normalized.length) {
    return Array.from(new Set(normalized));
  }
  return preferredMonitorSelection(topology);
}

function equalStringArrays(left, right) {
  if (!Array.isArray(left) || !Array.isArray(right)) return false;
  if (left.length !== right.length) return false;
  return left.every((item, index) => item === right[index]);
}

function selectedDisplayBounds(topology, selectionIds) {
  if (!Array.isArray(topology) || !topology.length) return null;
  const normalizedSelection = new Set(
    (Array.isArray(selectionIds) ? selectionIds : []).map((item) => normalizeText(item)).filter(Boolean)
  );
  const selected = normalizedSelection.size
    ? topology.filter((item) => normalizedSelection.has(monitorSelectionId(item)))
    : [];
  if (!selected.length) return null;
  return displayTopologyBounds(selected);
}

function buildDisplayLayoutFrames(
  topology,
  { frameWidth = 256, frameHeight = 126, padding = 10 } = {}
) {
  const bounds = displayTopologyBounds(topology);
  if (!bounds || bounds.width <= 0 || bounds.height <= 0) {
    return [];
  }
  const innerWidth = Math.max(1, frameWidth - padding * 2);
  const innerHeight = Math.max(1, frameHeight - padding * 2);
  const scale = Math.min(innerWidth / bounds.width, innerHeight / bounds.height);
  const layoutWidth = bounds.width * scale;
  const layoutHeight = bounds.height * scale;
  const offsetX = padding + (innerWidth - layoutWidth) / 2;
  const offsetY = padding + (innerHeight - layoutHeight) / 2;
  return topology.map((item) => ({
    ...item,
    x: offsetX + (item.left - bounds.left) * scale,
    y: offsetY + (item.top - bounds.top) * scale,
    widthPx: Math.max(28, item.width * scale),
    heightPx: Math.max(24, item.height * scale),
  }));
}

function aspectRatioMatches(leftSize, rightSize, tolerance = 0.12) {
  if (!leftSize?.width || !leftSize?.height || !rightSize?.width || !rightSize?.height) {
    return false;
  }
  const leftRatio = Number(leftSize.width) / Number(leftSize.height);
  const rightRatio = Number(rightSize.width) / Number(rightSize.height);
  if (!Number.isFinite(leftRatio) || !Number.isFinite(rightRatio) || rightRatio <= 0) {
    return false;
  }
  return Math.abs(leftRatio - rightRatio) / rightRatio <= tolerance;
}

function displayDiagramTopology(topology, framebufferSize, renderedCanvasSize) {
  const preferredSingleDisplaySize =
    renderedCanvasSize?.width && renderedCanvasSize?.height
      ? renderedCanvasSize
      : framebufferSize;

  if (Array.isArray(topology) && topology.length > 1) {
    return topology;
  }
  if (Array.isArray(topology) && topology.length === 1) {
    const [display] = topology;
    if (preferredSingleDisplaySize?.width && preferredSingleDisplaySize?.height) {
      const bounds = displayTopologyBounds(topology);
      const shouldUsePreferredSize =
        !topologyMatchesFramebuffer(bounds, framebufferSize) ||
        !aspectRatioMatches(display, preferredSingleDisplaySize);
      if (shouldUsePreferredSize) {
        return [
          {
            ...display,
            left: 0,
            top: 0,
            right: preferredSingleDisplaySize.width,
            bottom: preferredSingleDisplaySize.height,
            width: preferredSingleDisplaySize.width,
            height: preferredSingleDisplaySize.height,
            primary: true,
            synthetic: true,
          },
        ];
      }
    }
    return topology;
  }
  const fallbackSize =
    renderedCanvasSize?.width && renderedCanvasSize?.height
      ? renderedCanvasSize
      : framebufferSize;
  if (!fallbackSize?.width || !fallbackSize?.height) {
    return [];
  }
  return [
    {
      id: "1",
      displayIndex: 1,
      label: "1",
      deviceName: "Framebuffer",
      left: 0,
      top: 0,
      right: fallbackSize.width,
      bottom: fallbackSize.height,
      width: fallbackSize.width,
      height: fallbackSize.height,
      primary: true,
      synthetic: true,
    },
  ];
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
  const performanceLevel = 2;
  const [displayMode, setDisplayMode] = useState("fit");
  const [resizeSession, setResizeSession] = useState(true);
  const [capabilities, setCapabilities] = useState({});
  const [selectedMonitorIds, setSelectedMonitorIds] = useState([]);
  const [viewportHint, setViewportHint] = useState("");
  const [displayTopology, setDisplayTopology] = useState([]);
  const [framebufferSize, setFramebufferSize] = useState({ width: 0, height: 0 });
  const [renderedCanvasSize, setRenderedCanvasSize] = useState({ width: 0, height: 0 });

  const containerRef = useRef(null);
  const displayRef = useRef(null);
  const rfbRef = useRef(null);
  const agentIdRef = useRef("");
  const sessionIdRef = useRef("");
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const connectAttemptRef = useRef(0);
  const manualDisconnectRef = useRef(false);
  const viewportSignatureRef = useRef("");

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
  const scaleViewport = displayMode === "fit";
  const scaledViewport = displayMode === "scaled";
  const normalizedDisplayTopology = useMemo(
    () => normalizeDisplayTopology(displayTopology),
    [displayTopology]
  );
  const topologyBounds = useMemo(
    () => displayTopologyBounds(normalizedDisplayTopology),
    [normalizedDisplayTopology]
  );
  const topologyTrusted = useMemo(() => {
    if (!normalizedDisplayTopology.length) return false;
    if (normalizedDisplayTopology.length <= 1) return false;
    return topologyMatchesFramebuffer(topologyBounds, framebufferSize);
  }, [framebufferSize, normalizedDisplayTopology, topologyBounds]);
  const diagramTopology = useMemo(
    () => displayDiagramTopology(normalizedDisplayTopology, framebufferSize, renderedCanvasSize),
    [framebufferSize, normalizedDisplayTopology, renderedCanvasSize]
  );
  const displayLayoutFrames = useMemo(
    () => buildDisplayLayoutFrames(diagramTopology),
    [diagramTopology]
  );
  const effectiveSelectedMonitorIds = useMemo(
    () => normalizeMonitorSelection(selectedMonitorIds, normalizedDisplayTopology),
    [normalizedDisplayTopology, selectedMonitorIds]
  );

  const applySessionBootstrap = useCallback((data) => {
    const nextSession = data?.session && typeof data.session === "object" ? data.session : null;
    const nextSessionId = normalizeText(data?.session_id || nextSession?.session_id);
    const nextParticipantId = normalizeText(data?.participant_id || nextSession?.current_participant_id);
    const nextDisplayTopology = normalizeDisplayTopology(
      data?.display_topology || nextSession?.display_topology
    );
    setSessionId(nextSessionId);
    setParticipantId(nextParticipantId);
    setDisplayTopology(nextDisplayTopology);
    setSelectedMonitorIds((previous) => {
      const nextSelection = normalizeMonitorSelection(previous, nextDisplayTopology);
      return equalStringArrays(previous, nextSelection) ? previous : nextSelection;
    });
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

  const resetDisconnectedViewState = useCallback(() => {
    viewportSignatureRef.current = "";
    setDisplayTopology([]);
    setSelectedMonitorIds([]);
    setViewportHint("");
    setCapabilities({});
  }, []);

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
    viewportSignatureRef.current = "";
    setFramebufferSize({ width: 0, height: 0 });
    setRenderedCanvasSize({ width: 0, height: 0 });
  }, []);

  const configureDisplaySurface = useCallback((client, mode) => {
    const fitViewport = mode === "fit";
    const scaledMode = mode === "scaled";
    const host = displayRef.current;
    const screen = client?._screen || host?.firstElementChild || null;
    const canvas = client?._canvas || null;
    if (screen?.style) {
      screen.style.display = fitViewport ? "flex" : "block";
      screen.style.width = "100%";
      screen.style.height = "100%";
      screen.style.alignItems = fitViewport ? "center" : "flex-start";
      screen.style.justifyContent = fitViewport ? "center" : "flex-start";
      screen.style.overflowX = fitViewport ? "hidden" : "auto";
      screen.style.overflowY = fitViewport || scaledMode ? "hidden" : "auto";
    }
    if (canvas?.style) {
      canvas.style.display = "block";
      canvas.style.flex = fitViewport ? "0 0 auto" : "none";
      canvas.style.maxWidth = "none";
      canvas.style.maxHeight = "none";
      canvas.style.margin = fitViewport ? "auto" : "0";
      canvas.style.boxShadow = VNC_CANVAS_BOX_SHADOW;
    }
  }, []);

  const syncFramebufferSize = useCallback((client) => {
    const display = client?._display;
    const width = Number(display?._fb_width || display?._fbWidth || client?._fbWidth || 0);
    const height = Number(display?._fb_height || display?._fbHeight || client?._fbHeight || 0);
    setFramebufferSize((previous) => {
      if (previous.width === width && previous.height === height) {
        return previous;
      }
      return { width, height };
    });
    return { width, height };
  }, []);

  const syncRenderedCanvasSize = useCallback((client) => {
    const canvas = client?._canvas || null;
    const screen = client?._screen || null;
    const canvasRect =
      typeof canvas?.getBoundingClientRect === "function" ? canvas.getBoundingClientRect() : null;
    const screenRect =
      typeof screen?.getBoundingClientRect === "function" ? screen.getBoundingClientRect() : null;
    const width = Math.max(
      0,
      Math.round(
        Number(canvasRect?.width || 0) ||
          Number(canvas?.clientWidth || 0) ||
          Number(screenRect?.width || 0) ||
          0
      )
    );
    const height = Math.max(
      0,
      Math.round(
        Number(canvasRect?.height || 0) ||
          Number(canvas?.clientHeight || 0) ||
          Number(screenRect?.height || 0) ||
          0
      )
    );
    setRenderedCanvasSize((previous) => {
      if (previous.width === width && previous.height === height) {
        return previous;
      }
      return { width, height };
    });
    return { width, height };
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
      resetDisconnectedViewState();
      clipboardLastRef.current = "";
      setLoading(false);
    }
  }, [cancelPendingConnect, disconnectVnc, resetDisconnectedViewState, teardownDisplay]);

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
    rfb.dragViewport = dragViewport;
    rfb.resizeSession = resizeSession;
    rfb.qualityLevel = qualityLevel;
    rfb.compressionLevel = compressionLevel;
  }, [
    effectiveViewOnly,
    dragViewport,
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
      rfb.background = VNC_STAGE_BACKGROUND;
      rfb.resizeSession = resizeSession;
      rfb.dragViewport = dragViewport;
      rfb.viewOnly = effectiveViewOnly;
      rfb.qualityLevel = qualityLevel;
      rfb.compressionLevel = compressionLevel;

      configureDisplaySurface(rfb, displayMode);

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
          syncFramebufferSize(rfb);
          if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
            window.requestAnimationFrame(() => {
              syncRenderedCanvasSize(rfb);
            });
          } else {
            syncRenderedCanvasSize(rfb);
          }
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
          setSessionId("");
          setParticipantId("");
          resetDisconnectedViewState();
          clipboardLastRef.current = "";
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
      compressionLevel,
      configureDisplaySurface,
      displayMode,
      dragViewport,
      qualityLevel,
      resetDisconnectedViewState,
      resizeSession,
      effectiveViewOnly,
      syncFramebufferSize,
      syncRenderedCanvasSize,
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
        setSessionId("");
        setParticipantId("");
        resetDisconnectedViewState();
        return;
      }
      const nextDisplayTopology = normalizeDisplayTopology(nextSession.display_topology);
      if (nextDisplayTopology.length) {
        setDisplayTopology(nextDisplayTopology);
        setSelectedMonitorIds((previous) => {
          const nextSelection = normalizeMonitorSelection(previous, nextDisplayTopology);
          return equalStringArrays(previous, nextSelection) ? previous : nextSelection;
        });
      }
      setParticipantId((previous) => normalizeText(nextSession.current_participant_id) || previous);
    } catch {
      // ignore background session refresh failures
    }
  }, [resetDisconnectedViewState, teardownDisplay]);

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

  const syncViewportSelection = useCallback((selectionIds, options = {}) => {
    const normalizedSelection = normalizeMonitorSelection(selectionIds, normalizedDisplayTopology);
    if (options.updateState !== false) {
      setSelectedMonitorIds(normalizedSelection);
    }
    const rfb = rfbRef.current;
    const display = rfb?._display;
    const host = displayRef.current;
    const screen = rfb?._screen || host?.firstElementChild || null;
    if (!display) {
      setViewportHint("Viewport controls unavailable.");
      return;
    }
    const fbWidth = display?._fb_width || display?._fbWidth || rfb?._fbWidth || 0;
    const fbHeight = display?._fb_height || display?._fbHeight || rfb?._fbHeight || 0;
    syncFramebufferSize(rfb);
    const trustedTopology = topologyTrusted ? topologyBounds : null;
    if (!fbWidth || !fbHeight) {
      setViewportHint("Framebuffer size unavailable.");
      return;
    }
    configureDisplaySurface(rfb, displayMode);

    const applyViewportScale = (targetWidth, targetHeight) => {
      if (scaleViewport && host && typeof display.autoscale === "function") {
        display.autoscale(host.clientWidth, host.clientHeight);
        return;
      }
      if (scaledViewport && typeof display.scale !== "undefined") {
        const nextScale =
          host && targetHeight > 0 ? Number(host.clientHeight || 0) / Number(targetHeight) : 1.0;
        display.scale = Number.isFinite(nextScale) && nextScale > 0 ? nextScale : 1.0;
        return;
      }
      if (typeof display.scale !== "undefined") {
        display.scale = 1.0;
      }
    };

    const applyDisplayViewport = (target) => {
      try {
        display.clipViewport = true;
        display.viewportChangeSize(target.w, target.h);
        const viewportLoc = display?._viewportLoc || { x: 0, y: 0 };
        if (typeof display.viewportChangePos === "function") {
          display.viewportChangePos(target.x - viewportLoc.x, target.y - viewportLoc.y);
        } else if (typeof display.viewportChange === "function") {
          display.viewportChange(target.x - viewportLoc.x, target.y - viewportLoc.y, target.w, target.h);
        }
        applyViewportScale(target.w, target.h);
        rfb.dragViewport = false;
        setViewportHint("");
        if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
          window.requestAnimationFrame(() => {
            syncRenderedCanvasSize(rfb);
          });
        } else {
          syncRenderedCanvasSize(rfb);
        }
      } catch {
        setViewportHint("Viewport controls not available.");
      }
    };

    const applyFullFramebuffer = () => {
      try {
        if (host) {
          host.scrollLeft = 0;
          host.scrollTop = 0;
        }
        if (screen) {
          screen.scrollLeft = 0;
          screen.scrollTop = 0;
        }
        const viewportLoc = display?._viewportLoc || { x: 0, y: 0 };
        if (typeof display.viewportChangePos === "function" && (viewportLoc.x || viewportLoc.y)) {
          display.viewportChangePos(-viewportLoc.x, -viewportLoc.y);
        }
        if (typeof display.viewportChangeSize === "function") {
          display.viewportChangeSize(fbWidth, fbHeight);
        }
        display.clipViewport = false;
        applyViewportScale(fbWidth, fbHeight);
        rfb.dragViewport = false;
        setViewportHint("");
        if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
          window.requestAnimationFrame(() => {
            syncRenderedCanvasSize(rfb);
          });
        } else {
          syncRenderedCanvasSize(rfb);
        }
      } catch {
        setViewportHint("Viewport controls not available.");
      }
    };

    const selectionBounds =
      trustedTopology ? selectedDisplayBounds(normalizedDisplayTopology, normalizedSelection) : null;
    const hostWidth = Math.round(Number(host?.clientWidth || 0));
    const hostHeight = Math.round(Number(host?.clientHeight || 0));
    const modeSizeSignature =
      displayMode === "fit"
        ? `|host=${hostWidth}x${hostHeight}`
        : displayMode === "scaled"
          ? `|hostH=${hostHeight}`
          : "";
    const targetSignature = selectionBounds && trustedTopology
      ? `selection:${selectionBounds.left},${selectionBounds.top},${selectionBounds.width},${selectionBounds.height}`
      : `full:${fbWidth}x${fbHeight}`;
    const viewportSignature = `${displayMode}|${targetSignature}${modeSizeSignature}`;
    const shouldResetViewport =
      options.forceReset === true ||
      displayMode === "fit" ||
      viewportSignatureRef.current !== viewportSignature;

    if (selectionBounds && trustedTopology) {
      if (!shouldResetViewport) {
        setViewportHint("");
        return;
      }
      const applyMonitorViewport = () => {
        applyDisplayViewport({
          x: selectionBounds.left - trustedTopology.left,
          y: selectionBounds.top - trustedTopology.top,
          w: selectionBounds.width,
          h: selectionBounds.height,
        });
        viewportSignatureRef.current = viewportSignature;
      };
      if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
        window.requestAnimationFrame(applyMonitorViewport);
      } else {
        applyMonitorViewport();
      }
      return;
    }

    if (!shouldResetViewport) {
      setViewportHint("");
      return;
    }

    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(() => {
        applyFullFramebuffer();
        viewportSignatureRef.current = viewportSignature;
      });
    } else {
      applyFullFramebuffer();
      viewportSignatureRef.current = viewportSignature;
    }
  }, [
    configureDisplaySurface,
    displayMode,
    normalizedDisplayTopology,
    scaleViewport,
    scaledViewport,
    syncFramebufferSize,
    syncRenderedCanvasSize,
    topologyBounds,
    topologyTrusted,
  ]);

  const toggleMonitorSelection = useCallback((monitorId) => {
    const normalizedId = normalizeText(monitorId);
    if (!normalizedId) return;
    setSelectedMonitorIds((previous) => {
      const current = normalizeMonitorSelection(previous, normalizedDisplayTopology);
      const alreadySelected = current.includes(normalizedId);
      if (alreadySelected) {
        if (current.length === 1) {
          return current;
        }
        return current.filter((item) => item !== normalizedId);
      }
      return [...current, normalizedId];
    });
  }, [normalizedDisplayTopology]);

  const isConnected = sessionState === "connected";

  useEffect(() => {
    if (!isConnected) return;
    syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false, forceReset: true });
  }, [displayMode, effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);

  useEffect(() => {
    const normalized = normalizeMonitorSelection(selectedMonitorIds, normalizedDisplayTopology);
    const same =
      normalized.length === selectedMonitorIds.length &&
      normalized.every((item, index) => item === selectedMonitorIds[index]);
    if (same) return;
    setSelectedMonitorIds(normalized);
  }, [normalizedDisplayTopology, selectedMonitorIds]);

  useEffect(() => {
    if (!isConnected) return undefined;
    const timers = [
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 250),
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 1200),
    ];
    return () => {
      timers.forEach((timerId) => window.clearTimeout(timerId));
    };
  }, [effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (!isConnected) return undefined;
    if (typeof ResizeObserver === "undefined") return undefined;
    const host = containerRef.current;
    if (!host) return undefined;
    let frameId = 0;
    const observer = new ResizeObserver(() => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      frameId = window.requestAnimationFrame(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      });
    });
    observer.observe(host);
    return () => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      observer.disconnect();
    };
  }, [effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);
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

  const showClipboardActions = isConnected;
  const showPowerButtons = isConnected;
  const showLaunchButton = !loading;
  const showConnectingStatus =
    !isConnected &&
    (loading ||
      vncStage === "requesting_tunnel" ||
      vncStage === "connecting_ws" ||
      vncStage === "handshaking" ||
      vncStage === "retrying");

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
                flexGrow: 1,
                width: "100%",
                height: "100%",
                position: "relative",
                backgroundColor: "rgba(2,6,20,0.9)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                overflow: "hidden",
              }}
            >
              <Box
                ref={displayRef}
                sx={{
                  width: "100%",
                  height: "100%",
                  display: "block",
                  position: "relative",
                  overflow: "hidden",
                  "& > div": {
                    width: "100%",
                    height: "100%",
                  },
                  "& canvas": {
                    display: "block",
                    flex: "0 0 auto",
                    maxWidth: "none",
                    maxHeight: "none",
                    boxShadow: VNC_CANVAS_BOX_SHADOW,
                  },
                }}
              />
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
                    Ready to Connect
                  </Typography>
                  {showConnectingStatus ? (
                    <Typography
                      variant="body2"
                      sx={{
                        color: MAGIC_UI.accentA,
                        fontWeight: 600,
                        maxWidth: 320,
                      }}
                    >
                      Connecting...
                    </Typography>
                  ) : null}
                  {!showConnectingStatus && statusMessage ? (
                    <Typography
                      variant="body2"
                      sx={{
                        color: vncStageInfo.detailTone || MAGIC_UI.textMuted,
                        maxWidth: 320,
                      }}
                    >
                      {statusMessage}
                    </Typography>
                  ) : null}
                  {showLaunchButton ? (
                    <Button
                      size="medium"
                      startIcon={<PlayIcon />}
                      variant="outlined"
                      sx={primaryHeroActionSx}
                      disabled={!agentId}
                      onClick={handleConnect}
                    >
                      {sessionId ? "Reconnect Remote Desktop" : "Launch Remote Desktop"}
                    </Button>
                  ) : null}
                </Stack>
              ) : null}
            </Box>
          </Box>

        </Box>

        <Box sx={sidebarSx}>
          <Box sx={compactSidebarCardSx}>
            <Box sx={sectionHeaderSx}>
              <DisplayIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Display & Focus</span>
            </Box>
            <Stack spacing={1}>
              <ToggleButtonGroup
                exclusive
                size="small"
                value={displayMode}
                onChange={(_, value) => {
                  if (!value) return;
                  setDisplayMode(value);
                }}
                sx={splitToggleSx}
              >
                <ToggleButton value="fit">Fit</ToggleButton>
                <ToggleButton value="actual">Actual</ToggleButton>
                <ToggleButton value="scaled">Scaled</ToggleButton>
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
              {displayLayoutFrames.length ? (
                <Box
                  sx={{
                    position: "relative",
                    height: 126,
                    borderRadius: 2,
                    border: `1px solid ${SIDEBAR_THEME.border}`,
                    background: "rgba(7,12,23,0.82)",
                    overflow: "hidden",
                  }}
                >
                  {displayLayoutFrames.map((item) => {
                    const monitorId = monitorSelectionId(item);
                    const selected = effectiveSelectedMonitorIds.includes(monitorId);
                    const selectable = normalizedDisplayTopology.length > 1;
                    return (
                      <Box
                        key={item.id}
                        onClick={selectable ? () => toggleMonitorSelection(monitorId) : undefined}
                        sx={{
                          position: "absolute",
                          left: item.x,
                          top: item.y,
                          width: item.widthPx,
                          height: item.heightPx,
                          borderRadius: 1.5,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                          fontSize: "1rem",
                          fontWeight: 700,
                          cursor: selectable ? "pointer" : "default",
                          color: selected ? "#08111f" : SIDEBAR_THEME.text,
                          border: selected
                            ? "1px solid transparent"
                            : `1px solid ${SIDEBAR_THEME.border}`,
                          background: selected
                            ? "linear-gradient(135deg,#7fc9ff 0%,#b195ff 100%)"
                            : item.primary
                              ? "rgba(125,183,255,0.18)"
                              : "rgba(148,163,184,0.14)",
                          boxShadow: selected
                            ? "0 10px 28px rgba(91,126,255,0.25)"
                            : "none",
                          transition: "all 140ms ease",
                          "&:hover": {
                            borderColor: selectable
                              ? "rgba(125,183,255,0.58)"
                              : undefined,
                            transform: selectable ? "translateY(-1px)" : undefined,
                          },
                        }}
                      >
                        {item.label}
                      </Box>
                    );
                  })}
                </Box>
              ) : null}
              {normalizedDisplayTopology.length > 1 ? (
                <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                  Click one or more displays to focus the viewport on that layout.
                </Typography>
              ) : null}
              {!topologyTrusted && normalizedDisplayTopology.length > 1 ? (
                <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                  Monitor layout shown from agent telemetry. Targeted monitor selection will unlock once it matches the live desktop geometry.
                </Typography>
              ) : null}
              {!normalizedDisplayTopology.length && framebufferSize.width > 0 && framebufferSize.height > 0 ? (
                <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                  Showing the live framebuffer as a single display map.
                </Typography>
              ) : null}
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
