import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Box,
  Button,
  Divider,
  ListItemButton,
  ListItemText,
  Switch,
  LinearProgress,
  Stack,
  Typography,
} from "@mui/material";
import {
  DesktopWindowsRounded as DesktopIcon,
  PlayArrowRounded as PlayIcon,
  StopRounded as StopIcon,
  KeyboardRounded as KeyboardIcon,
  ContentPasteRounded as ClipboardIcon,
  PowerSettingsNewRounded as PowerIcon,
  ExpandMore as ExpandMoreIcon,
  FitScreenRounded as FitScreenIcon,
  CropFreeRounded as CropFreeIcon,
  SwapHorizRounded as SwapHorizIcon,
  RestartAltRounded as RestartAltIcon,
  ReplayRounded as ReplayIcon,
  KeyboardCommandKeyRounded as KeyboardCommandKeyIcon,
  ChevronLeft as ChevronLeftIcon,
  CheckCircleRounded as StageCompleteIcon,
  AutorenewRounded as StageActiveIcon,
  ErrorOutlineRounded as StageErrorIcon,
  RadioButtonUncheckedRounded as StagePendingIcon,
} from "@mui/icons-material";
import RFB from "@novnc/novnc/lib/rfb";
import { APP_PATHS } from "../../app/routes/paths.js";

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

const NAV_COLORS = {
  cyan: "#7db7ff",
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  line: "rgba(125,183,255,0.14)",
  hover: "rgba(255,255,255,0.05)",
  itemActiveBg:
    "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
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

const sidebarSx = {
  minWidth: 260,
  maxWidth: 260,
  width: { xs: "100%", lg: 260 },
  borderRadius: 2.5,
  border: `1px solid ${NAV_COLORS.line}`,
  background:
    "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), #0f141c",
  boxShadow: "none",
  p: 0.25,
  display: "flex",
  flexDirection: "column",
  gap: 0.25,
  overflow: "auto",
};

const sidebarAccordionSx = {
  "&:before": { display: "none" },
  m: 0,
  bgcolor: "transparent",
  border: 0,
  boxShadow: "none",
};

const sidebarAccordionSummarySx = {
  minHeight: 38,
  px: 1.5,
  backgroundColor: "rgba(255,255,255,0.02)",
  borderTopRightRadius: 8,
  borderBottomRightRadius: 8,
  "& .MuiAccordionSummary-content": {
    m: 0,
    py: 0.5,
    display: "flex",
  },
  "&.Mui-expanded": {
    minHeight: 38,
  },
  "& .MuiAccordionSummary-content.Mui-expanded": {
    my: 0,
  },
};

const sidebarAccordionDetailsSx = {
  p: 0,
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

function sidebarNavRowSx(active, disabled = false) {
  return {
    pl: 2,
    pr: 2,
    py: 1,
    color: active ? NAV_COLORS.textActive : NAV_COLORS.text,
    position: "relative",
    background: active ? NAV_COLORS.itemActiveBg : "transparent",
    borderTopRightRadius: 0,
    borderBottomRightRadius: 0,
    justifyContent: "space-between",
    transition:
      "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
    "&:hover": {
      background: active ? NAV_COLORS.itemActiveBg : NAV_COLORS.hover,
    },
    "&:active": {
      transform: "translateY(0.5px)",
    },
    "&.Mui-disabled": {
      color: "rgba(203,213,225,0.38)",
    },
    ...(disabled
      ? {
          cursor: "not-allowed",
        }
      : null),
  };
}

function sidebarNavIconSx(active, disabled = false) {
  return {
    mr: 1,
    display: "flex",
    alignItems: "center",
    color: disabled ? "rgba(143,191,255,0.35)" : active ? NAV_COLORS.cyan : "#8fbfff",
    transition: "color 160ms ease",
  };
}

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
const ALL_DISPLAYS_ID = "__all_displays__";
const CONNECTION_FLOW_STEPS = Object.freeze([
  {
    id: "tunnel",
    label: "Securing Tunnel",
    detail: "Requesting the WireGuard route to the remote device.",
  },
  {
    id: "service",
    label: "Checking VNC Service",
    detail: "Confirming the remote desktop service is running.",
  },
  {
    id: "socket",
    label: "Opening Desktop Socket",
    detail: "Connecting the browser to the live VNC socket.",
  },
  {
    id: "auth",
    label: "Authenticating Session",
    detail: "Negotiating credentials and display settings.",
  },
  {
    id: "ready",
    label: "Desktop Ready",
    detail: "Finalizing the live desktop stream for control.",
  },
]);

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

function clampNumber(value, min, max) {
  if (!Number.isFinite(value)) return min;
  if (max < min) return min;
  return Math.min(Math.max(value, min), max);
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

function equalDisplayTopology(left, right) {
  if (!Array.isArray(left) || !Array.isArray(right)) return false;
  if (left.length !== right.length) return false;
  return left.every((item, index) => {
    const other = right[index];
    if (!other) return false;
    return (
      item.id === other.id &&
      item.displayIndex === other.displayIndex &&
      item.label === other.label &&
      item.deviceName === other.deviceName &&
      item.left === other.left &&
      item.top === other.top &&
      item.right === other.right &&
      item.bottom === other.bottom &&
      item.width === other.width &&
      item.height === other.height &&
      item.primary === other.primary
    );
  });
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

function buildDisplayLayoutGeometry(
  topology,
  { frameWidth = 256, frameHeight = 126, padding = 6, edgeInset = 0 } = {}
) {
  const bounds = displayTopologyBounds(topology);
  if (!bounds || bounds.width <= 0 || bounds.height <= 0) {
    return {
      bounds: null,
      frameWidth,
      frameHeight,
      offsetX: padding + edgeInset,
      offsetY: padding + edgeInset,
      padding,
      edgeInset,
      scale: 1,
      frames: [],
    };
  }
  const insetPadding = padding + edgeInset;
  const innerWidth = Math.max(1, frameWidth - insetPadding * 2);
  const innerHeight = Math.max(1, frameHeight - insetPadding * 2);
  const aspectRatio = bounds.width / bounds.height;
  const horizontalUtilization =
    aspectRatio >= 3
      ? 0.78
      : aspectRatio >= 2
        ? 0.84
        : 0.9;
  const verticalUtilization = 0.9;
  const scale = Math.min(
    (innerWidth * horizontalUtilization) / bounds.width,
    (innerHeight * verticalUtilization) / bounds.height
  );
  const layoutWidth = bounds.width * scale;
  const layoutHeight = bounds.height * scale;
  const offsetX = insetPadding + (innerWidth - layoutWidth) / 2;
  const offsetY = insetPadding + (innerHeight - layoutHeight) / 2;
  const maxRight = frameWidth - insetPadding;
  const maxBottom = frameHeight - insetPadding;
  return {
    bounds,
    frameWidth,
    frameHeight,
    offsetX,
    offsetY,
    padding,
    edgeInset,
    scale,
    frames: topology.map((item) => {
      const rawX = offsetX + (item.left - bounds.left) * scale;
      const rawY = offsetY + (item.top - bounds.top) * scale;
      const x = clampNumber(rawX, insetPadding, maxRight);
      const y = clampNumber(rawY, insetPadding, maxBottom);
      return {
        ...item,
        x,
        y,
        widthPx: Math.max(2, Math.min(item.width * scale, maxRight - x)),
        heightPx: Math.max(2, Math.min(item.height * scale, maxBottom - y)),
      };
    }),
  };
}

function buildDisplayLayoutFrames(topology, options) {
  return buildDisplayLayoutGeometry(topology, options).frames;
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
  const authoritativeFramebufferSize =
    framebufferSize?.width && framebufferSize?.height
      ? framebufferSize
      : renderedCanvasSize?.width && renderedCanvasSize?.height
        ? renderedCanvasSize
        : null;

  if (Array.isArray(topology) && topology.length > 1) {
    return topology;
  }
  if (Array.isArray(topology) && topology.length === 1) {
    const [display] = topology;
    if (authoritativeFramebufferSize?.width && authoritativeFramebufferSize?.height) {
      const bounds = displayTopologyBounds(topology);
      const shouldUsePreferredSize =
        !topologyMatchesFramebuffer(bounds, framebufferSize) ||
        !aspectRatioMatches(display, authoritativeFramebufferSize);
      if (shouldUsePreferredSize) {
        return [
          {
            ...display,
            left: 0,
            top: 0,
            right: authoritativeFramebufferSize.width,
            bottom: authoritativeFramebufferSize.height,
            width: authoritativeFramebufferSize.width,
            height: authoritativeFramebufferSize.height,
            primary: true,
            synthetic: true,
          },
        ];
      }
    }
    return topology;
  }
  const fallbackSize =
    framebufferSize?.width && framebufferSize?.height
      ? framebufferSize
      : renderedCanvasSize?.width && renderedCanvasSize?.height
        ? renderedCanvasSize
        : null;
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

export default function RemoteDesktopPage({ device: providedDevice = null }) {
  const location = useLocation();
  const navigate = useNavigate();
  const { deviceId } = useParams();
  const device = useMemo(() => {
    if (providedDevice) {
      return providedDevice;
    }
    const initialDevice = location.state?.initialDevice;
    if (initialDevice && typeof initialDevice === "object") {
      return initialDevice;
    }
    return {
      agent_guid: deviceId || null,
      hostname: deviceId || null,
      id: deviceId || null,
    };
  }, [deviceId, location.state, providedDevice]);
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
  const [expandedSidebarSections, setExpandedSidebarSections] = useState({
    display: true,
    clipboard: true,
    power: true,
    session: true,
  });
  const [resizeSession, setResizeSession] = useState(true);
  const [capabilities, setCapabilities] = useState({});
  const [selectedDisplayId, setSelectedDisplayId] = useState(ALL_DISPLAYS_ID);
  const [displaySelectorExpanded, setDisplaySelectorExpanded] = useState(false);
  const [viewportHint, setViewportHint] = useState("");
  const [displayTopology, setDisplayTopology] = useState([]);
  const [framebufferSize, setFramebufferSize] = useState({ width: 0, height: 0 });
  const [renderedCanvasSize, setRenderedCanvasSize] = useState({ width: 0, height: 0 });
  const [viewfinderSize, setViewfinderSize] = useState({ width: 0, height: 126 });
  const [viewportPreview, setViewportPreview] = useState({
    left: 0,
    top: 0,
    width: 0,
    height: 0,
    targetLeft: 0,
    targetTop: 0,
    targetWidth: 0,
    targetHeight: 0,
    interactive: false,
    mode: "fit",
  });

  const containerRef = useRef(null);
  const displayScrollRef = useRef(null);
  const displayRef = useRef(null);
  const viewfinderRef = useRef(null);
  const rfbRef = useRef(null);
  const agentIdRef = useRef("");
  const sessionIdRef = useRef("");
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const connectAttemptRef = useRef(0);
  const manualDisconnectRef = useRef(false);
  const viewportSignatureRef = useRef("");
  const forcedViewportKeyRef = useRef("");
  const viewportStateRef = useRef({
    mode: "fit",
    targetBounds: null,
    targetPreviewBounds: null,
    viewportX: 0,
    viewportY: 0,
    viewportWidth: 0,
    viewportHeight: 0,
    interactive: false,
  });

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
  const deviceRouteId = useMemo(
    () =>
      normalizeText(device?.agent_guid) ||
      normalizeText(device?.agentGuid) ||
      normalizeText(device?.id) ||
      normalizeText(deviceId),
    [device, deviceId]
  );
  const deviceHostname = useMemo(
    () =>
      normalizeText(device?.hostname) ||
      normalizeText(device?.summary?.hostname) ||
      deviceRouteId ||
      "device",
    [device, deviceRouteId]
  );
  const qualityLevel = Math.min(8, Math.max(0, performanceLevel));
  const compressionLevel = Math.max(0, 8 - qualityLevel);
  const dragViewport = false;
  const effectiveViewOnly = viewOnly;
  const effectiveResizeSession = resizeSession && displayMode === "fit";
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
  const displaySelectorOptions = useMemo(
    () => [
      { id: ALL_DISPLAYS_ID, label: "All", shortLabel: "All" },
      ...normalizedDisplayTopology.map((item) => ({
        id: monitorSelectionId(item),
        label: `Display ${item.label}`,
        shortLabel: item.label,
      })),
    ],
    [normalizedDisplayTopology]
  );
  const effectiveSelectedMonitorIds = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return [];
    const availableIds = new Set(
      normalizedDisplayTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    return availableIds.has(selectedDisplayId) ? [selectedDisplayId] : [];
  }, [normalizedDisplayTopology, selectedDisplayId]);
  const displaySelectorLabel = useMemo(() => {
    const selectedOption = displaySelectorOptions.find((item) => item.id === selectedDisplayId);
    return `Display: ${selectedOption?.shortLabel || "All"}`;
  }, [displaySelectorOptions, selectedDisplayId]);
  const viewfinderTopology = useMemo(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return diagramTopology;
    const filtered = diagramTopology.filter(
      (item) => monitorSelectionId(item) === selectedDisplayId
    );
    return filtered.length ? filtered : diagramTopology;
  }, [diagramTopology, selectedDisplayId]);
  const displayLayoutGeometry = useMemo(
    () =>
      buildDisplayLayoutGeometry(viewfinderTopology, {
        frameWidth: Math.max(1, Math.round(viewfinderSize.width || 256)),
        frameHeight: Math.max(1, Math.round(viewfinderSize.height || 126)),
      }),
    [viewfinderSize.height, viewfinderSize.width, viewfinderTopology]
  );
  const displayLayoutFrames = useMemo(
    () => displayLayoutGeometry.frames,
    [displayLayoutGeometry]
  );
  const singleViewfinderFrameRect = useMemo(() => {
    if (viewfinderTopology.length !== 1) return null;
    const [display] = viewfinderTopology;
    const boundsWidth = Math.max(1, Number(display.width || 0));
    const boundsHeight = Math.max(1, Number(display.height || 0));
    const aspectRatio = boundsWidth / boundsHeight;
    const inset = 10;
    const availableWidth = Math.max(1, viewfinderSize.width - inset * 2);
    const availableHeight = Math.max(1, viewfinderSize.height - inset * 2);
    const widthUtilization =
      aspectRatio >= 3
        ? 0.82
        : aspectRatio >= 2
          ? 0.88
          : 0.92;
    const heightUtilization = 0.9;
    const widthLimit = availableWidth * widthUtilization;
    const heightLimit = availableHeight * heightUtilization;
    const widthFromHeight = heightLimit * aspectRatio;
    const resolvedWidth = Math.min(widthLimit, widthFromHeight);
    const resolvedHeight = resolvedWidth / aspectRatio;
    const x = Math.max(0, (viewfinderSize.width - resolvedWidth) / 2);
    const y = Math.max(0, (viewfinderSize.height - resolvedHeight) / 2);
    return {
      ...display,
      widthPx: resolvedWidth,
      heightPx: resolvedHeight,
      x,
      y,
    };
  }, [viewfinderSize.height, viewfinderSize.width, viewfinderTopology]);

  const applySessionBootstrap = useCallback((data) => {
    const nextSession = data?.session && typeof data.session === "object" ? data.session : null;
    const nextSessionId = normalizeText(data?.session_id || nextSession?.session_id);
    const nextParticipantId = normalizeText(data?.participant_id || nextSession?.current_participant_id);
    const nextDisplayTopology = normalizeDisplayTopology(
      data?.display_topology || nextSession?.display_topology
    );
    setSessionId(nextSessionId);
    setParticipantId(nextParticipantId);
    setDisplayTopology((previous) =>
      equalDisplayTopology(previous, nextDisplayTopology) ? previous : nextDisplayTopology
    );
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
    forcedViewportKeyRef.current = "";
    viewportStateRef.current = {
      mode: "fit",
      targetBounds: null,
      targetPreviewBounds: null,
      viewportX: 0,
      viewportY: 0,
      viewportWidth: 0,
      viewportHeight: 0,
      interactive: false,
    };
    setDisplayTopology([]);
    setSelectedDisplayId(ALL_DISPLAYS_ID);
    setDisplaySelectorExpanded(false);
    setDisplayMode("fit");
    setViewportHint("");
    setCapabilities({});
    setViewportPreview({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      targetLeft: 0,
      targetTop: 0,
      targetWidth: 0,
      targetHeight: 0,
      interactive: false,
      mode: "fit",
    });
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
    forcedViewportKeyRef.current = "";
    viewportStateRef.current = {
      mode: "fit",
      targetBounds: null,
      targetPreviewBounds: null,
      viewportX: 0,
      viewportY: 0,
      viewportWidth: 0,
      viewportHeight: 0,
      interactive: false,
    };
    setFramebufferSize({ width: 0, height: 0 });
    setRenderedCanvasSize({ width: 0, height: 0 });
    setViewportPreview({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      targetLeft: 0,
      targetTop: 0,
      targetWidth: 0,
      targetHeight: 0,
      interactive: false,
      mode: "fit",
    });
  }, []);

  const configureDisplaySurface = useCallback((client, mode) => {
    const fitViewport = mode === "fit";
    const host = displayRef.current;
    const screen = client?._screen || host?.firstElementChild || null;
    const canvas = client?._canvas || null;
    if (screen?.style) {
      screen.style.display = fitViewport ? "flex" : "block";
      screen.style.width = fitViewport ? "100%" : "max-content";
      screen.style.height = fitViewport ? "100%" : "max-content";
      screen.style.alignItems = fitViewport ? "center" : "flex-start";
      screen.style.justifyContent = fitViewport ? "center" : "flex-start";
      screen.style.overflow = "hidden";
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

  const syncViewportPreview = useCallback(
    ({
      mode,
      targetBounds,
      targetPreviewBounds,
      viewportX,
      viewportY,
      viewportWidth,
      viewportHeight,
      interactive,
    }) => {
      const nextPreview = {
        left: targetPreviewBounds.left + (viewportX - targetBounds.x),
        top: targetPreviewBounds.top + (viewportY - targetBounds.y),
        width: viewportWidth,
        height: viewportHeight,
        targetLeft: targetPreviewBounds.left,
        targetTop: targetPreviewBounds.top,
        targetWidth: targetPreviewBounds.width,
        targetHeight: targetPreviewBounds.height,
        interactive,
        mode,
      };
      viewportStateRef.current = {
        mode,
        targetBounds,
        targetPreviewBounds,
        viewportX,
        viewportY,
        viewportWidth,
        viewportHeight,
        interactive,
      };
      setViewportPreview((previous) => {
        if (
          previous.left === nextPreview.left &&
          previous.top === nextPreview.top &&
          previous.width === nextPreview.width &&
          previous.height === nextPreview.height &&
          previous.targetLeft === nextPreview.targetLeft &&
          previous.targetTop === nextPreview.targetTop &&
          previous.targetWidth === nextPreview.targetWidth &&
          previous.targetHeight === nextPreview.targetHeight &&
          previous.interactive === nextPreview.interactive &&
          previous.mode === nextPreview.mode
        ) {
          return previous;
        }
        return nextPreview;
      });
    },
    []
  );

  const applyViewportFrame = useCallback(
    (
      rfb,
      {
        mode,
        targetBounds,
        targetPreviewBounds,
        forceReset = false,
        requestedCenter = null,
      }
    ) => {
      const display = rfb?._display;
      const viewportHost = displayScrollRef.current || containerRef.current;
      if (!display || !viewportHost || !targetBounds || !targetPreviewBounds) {
        return null;
      }

      const hostWidth = Math.max(1, Math.round(Number(viewportHost.clientWidth || 0)));
      const hostHeight = Math.max(1, Math.round(Number(viewportHost.clientHeight || 0)));
      const resolveViewportLoc = () => {
        const viewportLoc = display?._viewportLoc || {};
        return {
          x: Number.isFinite(Number(viewportLoc.x)) ? Number(viewportLoc.x) : targetBounds.x,
          y: Number.isFinite(Number(viewportLoc.y)) ? Number(viewportLoc.y) : targetBounds.y,
          w: Number.isFinite(Number(viewportLoc.w)) ? Number(viewportLoc.w) : targetBounds.width,
          h: Number.isFinite(Number(viewportLoc.h)) ? Number(viewportLoc.h) : targetBounds.height,
        };
      };
      const currentViewport = resolveViewportLoc();
      const previous = viewportStateRef.current;
      const sameTarget =
        previous.mode === mode &&
        previous.targetBounds?.x === targetBounds.x &&
        previous.targetBounds?.y === targetBounds.y &&
        previous.targetBounds?.width === targetBounds.width &&
        previous.targetBounds?.height === targetBounds.height &&
        previous.targetPreviewBounds?.left === targetPreviewBounds.left &&
        previous.targetPreviewBounds?.top === targetPreviewBounds.top &&
        previous.targetPreviewBounds?.width === targetPreviewBounds.width &&
        previous.targetPreviewBounds?.height === targetPreviewBounds.height;

      if (mode === "fit") {
        const viewportLoc = display?._viewportLoc || { x: 0, y: 0 };
        if (
          typeof display.viewportChangePos === "function" &&
          (viewportLoc.x !== targetBounds.x || viewportLoc.y !== targetBounds.y)
        ) {
          display.viewportChangePos(targetBounds.x - viewportLoc.x, targetBounds.y - viewportLoc.y);
        }
        if (targetBounds.width > 0 && targetBounds.height > 0) {
          display.clipViewport = true;
          display.viewportChangeSize(targetBounds.width, targetBounds.height);
        } else {
          display.clipViewport = false;
        }
        if (typeof display.autoscale === "function") {
          display.autoscale(hostWidth, hostHeight);
        }
        const actualViewport = resolveViewportLoc();
        syncViewportPreview({
          mode,
          targetBounds,
          targetPreviewBounds,
          viewportX: actualViewport.x,
          viewportY: actualViewport.y,
          viewportWidth: actualViewport.w,
          viewportHeight: actualViewport.h,
          interactive: false,
        });
        return {
          x: actualViewport.x,
          y: actualViewport.y,
          width: actualViewport.w,
          height: actualViewport.h,
          interactive: false,
        };
      }

      let viewportWidth = targetBounds.width;
      let viewportHeight = targetBounds.height;
      let scale = 1;
      if (mode === "scaled") {
        scale = hostHeight > 0 ? hostHeight / targetBounds.height : 1;
        const safeScale = Number.isFinite(scale) && scale > 0 ? scale : 1;
        viewportWidth = Math.min(
          targetBounds.width,
          Math.max(1, Math.round(hostWidth / safeScale))
        );
        viewportHeight = targetBounds.height;
        scale = safeScale;
      } else {
        viewportWidth = Math.min(targetBounds.width, hostWidth);
        viewportHeight = Math.min(targetBounds.height, hostHeight);
      }

      let nextX = sameTarget && !forceReset ? currentViewport.x : targetBounds.x;
      let nextY = sameTarget && !forceReset ? currentViewport.y : targetBounds.y;
      if (requestedCenter) {
        nextX =
          targetBounds.x +
          (requestedCenter.x - targetPreviewBounds.left) -
          viewportWidth / 2;
        nextY =
          mode === "scaled"
            ? targetBounds.y
            : targetBounds.y +
              (requestedCenter.y - targetPreviewBounds.top) -
              viewportHeight / 2;
      }
      nextX = clampNumber(
        Math.round(nextX),
        targetBounds.x,
        targetBounds.x + Math.max(0, targetBounds.width - viewportWidth)
      );
      nextY =
        mode === "scaled"
          ? targetBounds.y
          : clampNumber(
              Math.round(nextY),
              targetBounds.y,
              targetBounds.y + Math.max(0, targetBounds.height - viewportHeight)
            );

      display.clipViewport = true;
      display.viewportChangeSize(viewportWidth, viewportHeight);
      const viewportLoc = display?._viewportLoc || { x: 0, y: 0 };
      if (typeof display.viewportChangePos === "function") {
        display.viewportChangePos(nextX - viewportLoc.x, nextY - viewportLoc.y);
      } else if (typeof display.viewportChange === "function") {
        display.viewportChange(nextX - viewportLoc.x, nextY - viewportLoc.y, viewportWidth, viewportHeight);
      }
      if (typeof display.scale !== "undefined") {
        display.scale = mode === "scaled" ? scale : 1.0;
      }
      const interactive =
        mode === "scaled"
          ? targetBounds.width > viewportWidth
          : targetBounds.width > viewportWidth || targetBounds.height > viewportHeight;
      syncViewportPreview({
        mode,
        targetBounds,
        targetPreviewBounds,
        viewportX: nextX,
        viewportY: nextY,
        viewportWidth,
        viewportHeight,
        interactive,
      });
      return {
        x: nextX,
        y: nextY,
        width: viewportWidth,
        height: viewportHeight,
        interactive,
      };
    },
    [syncViewportPreview]
  );

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

  const handleReturnToDevice = useCallback(() => {
    if (deviceRouteId) {
      navigate(APP_PATHS.device(deviceRouteId), {
        state: device ? { initialDevice: device } : undefined,
      });
      return;
    }
    if (typeof window !== "undefined" && window.history.length > 1) {
      navigate(-1);
      return;
    }
    navigate(APP_PATHS.devices);
  }, [device, deviceRouteId, navigate]);

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
    rfb.resizeSession = effectiveResizeSession;
    rfb.qualityLevel = qualityLevel;
    rfb.compressionLevel = compressionLevel;
  }, [
    effectiveViewOnly,
    dragViewport,
    effectiveResizeSession,
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
      rfb.resizeSession = effectiveResizeSession;
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
      effectiveResizeSession,
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
        setDisplayTopology((previous) =>
          equalDisplayTopology(previous, nextDisplayTopology) ? previous : nextDisplayTopology
        );
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
    const rfb = rfbRef.current;
    const display = rfb?._display;
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
    const selectionBounds =
      trustedTopology && Array.isArray(selectionIds) && selectionIds.length
        ? selectedDisplayBounds(normalizedDisplayTopology, selectionIds)
        : null;
    const targetBounds = selectionBounds && trustedTopology
      ? {
          x: selectionBounds.left - trustedTopology.left,
          y: selectionBounds.top - trustedTopology.top,
          width: selectionBounds.width,
          height: selectionBounds.height,
        }
      : {
          x: 0,
          y: 0,
          width: fbWidth,
          height: fbHeight,
        };
    const targetPreviewBounds = selectionBounds && trustedTopology
      ? selectionBounds
      : trustedTopology || {
          left: 0,
          top: 0,
          width: fbWidth,
          height: fbHeight,
        };
    const viewportHost = displayScrollRef.current || containerRef.current;
    const hostWidth = Math.round(Number(viewportHost?.clientWidth || 0));
    const hostHeight = Math.round(Number(viewportHost?.clientHeight || 0));
    const modeSizeSignature =
      displayMode === "fit"
        ? `|host=${hostWidth}x${hostHeight}`
        : displayMode === "scaled"
          ? `|hostH=${hostHeight}`
          : "";
    const targetSignature =
      selectionBounds && trustedTopology
        ? `selection:${selectionBounds.left},${selectionBounds.top},${selectionBounds.width},${selectionBounds.height}`
        : `full:${fbWidth}x${fbHeight}`;
    const viewportSignature = `${displayMode}|${targetSignature}${modeSizeSignature}`;
    const shouldResetViewport =
      options.forceReset === true ||
      displayMode === "fit" ||
      viewportSignatureRef.current !== viewportSignature;

    if (!shouldResetViewport) {
      setViewportHint("");
      return;
    }

    const applyViewport = () => {
      try {
        const result = applyViewportFrame(rfb, {
          mode: displayMode,
          targetBounds,
          targetPreviewBounds,
          forceReset: shouldResetViewport,
        });
        if (!result) {
          setViewportHint("Viewport controls unavailable.");
          return;
        }
        rfb.dragViewport = false;
        setViewportHint("");
        if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
          window.requestAnimationFrame(() => {
            syncRenderedCanvasSize(rfb);
          });
        } else {
          syncRenderedCanvasSize(rfb);
        }
        viewportSignatureRef.current = viewportSignature;
      } catch {
        setViewportHint("Viewport controls not available.");
      }
    };

    if (typeof window !== "undefined" && typeof window.requestAnimationFrame === "function") {
      window.requestAnimationFrame(() => {
        applyViewport();
      });
    } else {
      applyViewport();
    }
  }, [
    applyViewportFrame,
    configureDisplaySurface,
    displayMode,
    normalizedDisplayTopology,
    syncFramebufferSize,
    syncRenderedCanvasSize,
    topologyBounds,
    topologyTrusted,
  ]);

  const isConnected = sessionState === "connected";
  const previewNavigationEnabled = isConnected && displayMode !== "fit";

  const handlePreviewNavigate = useCallback(
    (event) => {
      if (!isConnected || displayMode === "fit") return;
      if (typeof event.preventDefault === "function") {
        event.preventDefault();
      }
      const rfb = rfbRef.current;
      const state = viewportStateRef.current;
      const geometry = displayLayoutGeometry;
      if (!rfb || !state?.interactive) return;
      const rect = event.currentTarget.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const localX = event.clientX - rect.left;
      const localY = event.clientY - rect.top;
      let previewX = 0;
      let previewY = 0;
      if (
        singleViewfinderFrameRect &&
        state.targetPreviewBounds?.width > 0 &&
        state.targetPreviewBounds?.height > 0
      ) {
        const relativeX = clampNumber(
          localX - singleViewfinderFrameRect.x,
          0,
          singleViewfinderFrameRect.widthPx
        );
        const relativeY = clampNumber(
          localY - singleViewfinderFrameRect.y,
          0,
          singleViewfinderFrameRect.heightPx
        );
        previewX =
          state.targetPreviewBounds.left +
          (relativeX / Math.max(1, singleViewfinderFrameRect.widthPx)) *
            state.targetPreviewBounds.width;
        previewY =
          state.targetPreviewBounds.top +
          (relativeY / Math.max(1, singleViewfinderFrameRect.heightPx)) *
            state.targetPreviewBounds.height;
      } else {
        if (!geometry?.bounds || !geometry.scale) return;
        previewX =
          geometry.bounds.left + (localX - geometry.offsetX) / geometry.scale;
        previewY =
          geometry.bounds.top + (localY - geometry.offsetY) / geometry.scale;
      }
      const clampedCenter = {
        x: clampNumber(
          previewX,
          state.targetPreviewBounds.left,
          state.targetPreviewBounds.left + state.targetPreviewBounds.width
        ),
        y: clampNumber(
          previewY,
          state.targetPreviewBounds.top,
          state.targetPreviewBounds.top + state.targetPreviewBounds.height
        ),
      };
      try {
        const applied = applyViewportFrame(rfb, {
          mode: displayMode,
          targetBounds: state.targetBounds,
          targetPreviewBounds: state.targetPreviewBounds,
          requestedCenter: clampedCenter,
        });
        if (!applied) return;
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
    },
    [
      applyViewportFrame,
      displayLayoutGeometry,
      displayMode,
      isConnected,
      singleViewfinderFrameRect,
      syncRenderedCanvasSize,
    ]
  );

  const forcedViewportKey = useMemo(
    () =>
      isConnected
        ? `${displayMode}|${effectiveSelectedMonitorIds.join(",") || ALL_DISPLAYS_ID}`
        : "",
    [displayMode, effectiveSelectedMonitorIds, isConnected]
  );

  useEffect(() => {
    if (!isConnected) {
      forcedViewportKeyRef.current = "";
      return;
    }
    if (forcedViewportKeyRef.current === forcedViewportKey) return;
    forcedViewportKeyRef.current = forcedViewportKey;
    syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false, forceReset: true });
  }, [effectiveSelectedMonitorIds, forcedViewportKey, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (selectedDisplayId === ALL_DISPLAYS_ID) return;
    const availableIds = new Set(
      normalizedDisplayTopology.map((item) => monitorSelectionId(item)).filter(Boolean)
    );
    if (availableIds.has(selectedDisplayId)) return;
    setSelectedDisplayId(ALL_DISPLAYS_ID);
  }, [normalizedDisplayTopology, selectedDisplayId]);

  useEffect(() => {
    if (!isConnected) return undefined;
    const timers = [
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 250),
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 1200),
      window.setTimeout(() => {
        syncViewportSelection(effectiveSelectedMonitorIds, { updateState: false });
      }, 2500),
    ];
    return () => {
      timers.forEach((timerId) => window.clearTimeout(timerId));
    };
  }, [effectiveSelectedMonitorIds, isConnected, syncViewportSelection]);

  useEffect(() => {
    if (!isConnected) return undefined;
    if (typeof ResizeObserver === "undefined") return undefined;
    const host = displayScrollRef.current || containerRef.current;
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

  useEffect(() => {
    if (typeof ResizeObserver === "undefined") return undefined;
    const host = viewfinderRef.current;
    if (!host) return undefined;
    let frameId = 0;
    const syncSize = () => {
      const nextWidth = Math.max(0, Math.round(Number(host.clientWidth || 0)));
      const nextHeight = Math.max(0, Math.round(Number(host.clientHeight || 0)));
      setViewfinderSize((previous) => {
        if (previous.width === nextWidth && previous.height === nextHeight) {
          return previous;
        }
        return { width: nextWidth, height: nextHeight || previous.height || 126 };
      });
    };
    syncSize();
    const observer = new ResizeObserver(() => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      frameId = window.requestAnimationFrame(syncSize);
    });
    observer.observe(host);
    return () => {
      if (frameId) {
        window.cancelAnimationFrame(frameId);
      }
      observer.disconnect();
    };
  }, []);

  const shutdownSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "shutdown");
  const rebootSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "reboot");
  const resetSupported = isConnected && !effectiveViewOnly && supportsPowerAction(capabilities, "reset");
  const viewfinderViewportRect = useMemo(() => {
    if (!isConnected || !displayLayoutGeometry.bounds || viewportPreview.width <= 0 || viewportPreview.height <= 0) {
      return null;
    }
    if (
      singleViewfinderFrameRect &&
      viewportPreview.targetWidth > 0 &&
      viewportPreview.targetHeight > 0
    ) {
      const x =
        singleViewfinderFrameRect.x +
        ((viewportPreview.left - viewportPreview.targetLeft) / viewportPreview.targetWidth) *
          singleViewfinderFrameRect.widthPx;
      const y =
        singleViewfinderFrameRect.y +
        ((viewportPreview.top - viewportPreview.targetTop) / viewportPreview.targetHeight) *
          singleViewfinderFrameRect.heightPx;
      const width =
        (viewportPreview.width / viewportPreview.targetWidth) * singleViewfinderFrameRect.widthPx;
      const height =
        (viewportPreview.height / viewportPreview.targetHeight) *
        singleViewfinderFrameRect.heightPx;
      const clampedX = clampNumber(
        x,
        singleViewfinderFrameRect.x,
        singleViewfinderFrameRect.x + Math.max(0, singleViewfinderFrameRect.widthPx - 2)
      );
      const clampedY = clampNumber(
        y,
        singleViewfinderFrameRect.y,
        singleViewfinderFrameRect.y + Math.max(0, singleViewfinderFrameRect.heightPx - 2)
      );
      return {
        x: clampedX,
        y: clampedY,
        width: Math.max(
          2,
          Math.min(width, singleViewfinderFrameRect.x + singleViewfinderFrameRect.widthPx - clampedX)
        ),
        height: Math.max(
          2,
          Math.min(height, singleViewfinderFrameRect.y + singleViewfinderFrameRect.heightPx - clampedY)
        ),
        interactive: viewportPreview.interactive,
      };
    }
    const rawX =
      displayLayoutGeometry.offsetX +
      (viewportPreview.left - displayLayoutGeometry.bounds.left) * displayLayoutGeometry.scale;
    const rawY =
      displayLayoutGeometry.offsetY +
      (viewportPreview.top - displayLayoutGeometry.bounds.top) * displayLayoutGeometry.scale;
    const rawTargetX =
      displayLayoutGeometry.offsetX +
      (viewportPreview.targetLeft - displayLayoutGeometry.bounds.left) * displayLayoutGeometry.scale;
    const rawTargetY =
      displayLayoutGeometry.offsetY +
      (viewportPreview.targetTop - displayLayoutGeometry.bounds.top) * displayLayoutGeometry.scale;
    const insetPadding = displayLayoutGeometry.padding + (displayLayoutGeometry.edgeInset || 0);
    const maxRight = displayLayoutGeometry.frameWidth - insetPadding;
    const maxBottom = displayLayoutGeometry.frameHeight - insetPadding;
    const targetX = clampNumber(rawTargetX, insetPadding, maxRight);
    const targetY = clampNumber(rawTargetY, insetPadding, maxBottom);
    const targetRight = Math.max(
      targetX,
      Math.min(maxRight, targetX + viewportPreview.targetWidth * displayLayoutGeometry.scale)
    );
    const targetBottom = Math.max(
      targetY,
      Math.min(maxBottom, targetY + viewportPreview.targetHeight * displayLayoutGeometry.scale)
    );
    const x = clampNumber(rawX, targetX, Math.max(targetX, targetRight - 2));
    const y = clampNumber(rawY, targetY, Math.max(targetY, targetBottom - 2));
    const width = Math.max(
      2,
      Math.min(viewportPreview.width * displayLayoutGeometry.scale, targetRight - x)
    );
    const height = Math.max(
      2,
      Math.min(viewportPreview.height * displayLayoutGeometry.scale, targetBottom - y)
    );
    return {
      x,
      y,
      width,
      height,
      interactive: viewportPreview.interactive,
    };
  }, [
    displayLayoutGeometry,
    isConnected,
    viewportPreview.height,
    viewportPreview.interactive,
    viewportPreview.left,
    viewportPreview.targetHeight,
    viewportPreview.targetLeft,
    viewportPreview.targetTop,
    viewportPreview.targetWidth,
    viewportPreview.top,
    viewportPreview.width,
    singleViewfinderFrameRect,
  ]);
  const viewfinderHelperText = previewNavigationEnabled
    ? viewportPreview.interactive
      ? "Tap anywhere on the preview to recenter the live viewport."
      : "The full desktop already fits inside the current viewport."
    : "";
  const highlightedMonitorIds =
    selectedDisplayId === ALL_DISPLAYS_ID ? [] : effectiveSelectedMonitorIds;
  const showViewportIndicator = Boolean(
    viewfinderViewportRect && previewNavigationEnabled && viewportPreview.interactive
  );
  const showViewfinderHelper = Boolean(viewfinderHelperText);
  const SidebarSection = ({ sectionId, title, children }) => (
    <Accordion
      expanded={expandedSidebarSections[sectionId]}
      onChange={(_, expanded) => {
        setExpandedSidebarSections((previous) => ({ ...previous, [sectionId]: expanded }));
      }}
      square
      disableGutters
      sx={sidebarAccordionSx}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon sx={{ color: NAV_COLORS.cyan }} />}
        sx={sidebarAccordionSummarySx}
      >
        <Typography
          sx={{
            fontSize: "0.85rem",
            color: NAV_COLORS.cyan,
            fontWeight: 700,
            letterSpacing: 0.3,
          }}
        >
          {title}
        </Typography>
      </AccordionSummary>
      <AccordionDetails sx={sidebarAccordionDetailsSx}>{children}</AccordionDetails>
    </Accordion>
  );
  const SidebarNavRow = ({
    icon,
    label,
    onClick,
    active = false,
    disabled = false,
    trailing = null,
    ariaLabel = null,
  }) => (
    <ListItemButton
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      aria-label={ariaLabel || label}
      sx={sidebarNavRowSx(active, disabled)}
    >
      <Box sx={{ display: "flex", alignItems: "center", minWidth: 0, flex: "1 1 auto" }}>
        <Box sx={sidebarNavIconSx(active, disabled)}>{icon}</Box>
        <ListItemText
          primary={label}
          primaryTypographyProps={{
            fontSize: "0.8rem",
            fontWeight: active ? 600 : 400,
            letterSpacing: 0.2,
            noWrap: true,
          }}
        />
      </Box>
      {trailing ? <Box sx={{ ml: 1, flexShrink: 0 }}>{trailing}</Box> : null}
    </ListItemButton>
  );
  const connectionFlowStages = useMemo(() => {
    const steps = CONNECTION_FLOW_STEPS.map((step) => ({ ...step, status: "pending" }));
    if (isConnected || vncStage === "connected") {
      return steps.map((step) => ({ ...step, status: "complete" }));
    }
    switch (vncStage) {
      case "requesting_tunnel":
      case "agent_onboarding":
        steps[0].status = "active";
        break;
      case "connecting_ws":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "active";
        break;
      case "retrying":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "active";
        break;
      case "handshaking":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "active";
        break;
      case "auth_failed":
        steps[0].status = "complete";
        steps[1].status = "complete";
        steps[2].status = "complete";
        steps[3].status = "failed";
        break;
      case "error":
        if (sessionId) {
          steps[0].status = "complete";
          steps[1].status = "complete";
          steps[2].status = "failed";
        } else {
          steps[0].status = "failed";
        }
        break;
      default:
        break;
    }
    return steps;
  }, [isConnected, sessionId, vncStage]);

  const showClipboardActions = isConnected;
  const showPowerButtons = isConnected;
  const displaySettingsEnabled = isConnected;
  const showLaunchButton = !loading;
  const showConnectingStatus =
    !isConnected &&
    (loading ||
      vncStage === "requesting_tunnel" ||
      vncStage === "connecting_ws" ||
      vncStage === "handshaking" ||
      vncStage === "retrying");
  const showConnectionFlow =
    !isConnected &&
    (showConnectingStatus ||
      vncStage === "agent_onboarding" ||
      vncStage === "auth_failed" ||
      vncStage === "error");

  return (
    <Box
      className="remote-desktop-shell"
      sx={{
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minHeight: 0,
        minWidth: 0,
        height: "100%",
        overflow: "hidden",
      }}
    >
      <Box
        sx={{
          display: "flex",
          flexDirection: { xs: "column", lg: "row" },
          flexGrow: 1,
          minHeight: 0,
          minWidth: 0,
          height: "100%",
          overflow: "hidden",
        }}
      >
        <Box
          sx={{
            ...sidebarSx,
            flexShrink: 0,
            width: { xs: "100%", lg: 260 },
            maxWidth: { xs: "100%", lg: 260 },
            borderRadius: { xs: 2.5, lg: 0 },
            borderTop: `1px solid ${NAV_COLORS.line}`,
            borderBottom: `1px solid ${NAV_COLORS.line}`,
            borderLeft: `1px solid ${NAV_COLORS.line}`,
            borderRight: { xs: `1px solid ${NAV_COLORS.line}`, lg: "none" },
            boxShadow: "none",
            height: { xs: "auto", lg: "100%" },
            overflow: "hidden",
          }}
        >
          <Box sx={{ flex: 1, overflowY: "auto" }}>
            <SidebarSection
              sectionId="display"
              title="Display & Focus"
            >
              <>
                <Box sx={{ px: 2, py: 1.25, display: "flex", flexDirection: "column", gap: 1 }}>
                <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                  Viewfinder
                </Typography>
                <Box
                  sx={{
                    position: "relative",
                    height: 126,
                    borderRadius: 2,
                    border: `1px solid ${SIDEBAR_THEME.border}`,
                    background: "rgba(7,12,23,0.82)",
                    overflow: "hidden",
                    opacity: displaySettingsEnabled ? 1 : 0.6,
                  }}
                >
                  <Box
                    ref={viewfinderRef}
                    onPointerDown={previewNavigationEnabled ? handlePreviewNavigate : undefined}
                    sx={{
                      position: "absolute",
                      inset: 10,
                      overflow: "hidden",
                      cursor: previewNavigationEnabled
                        ? viewportPreview.interactive
                          ? "crosshair"
                          : "default"
                        : "default",
                      touchAction: previewNavigationEnabled ? "none" : "auto",
                    }}
                  >
                    {isConnected
                      ? (singleViewfinderFrameRect
                          ? [singleViewfinderFrameRect]
                          : displayLayoutFrames
                        ).map((item) => {
                          const monitorId = monitorSelectionId(item);
                          const selected = highlightedMonitorIds.includes(monitorId);
                          return (
                            <Box
                              key={item.id}
                              sx={{
                                position: "absolute",
                                boxSizing: "border-box",
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
                                cursor: "default",
                                color: selected ? "#08111f" : SIDEBAR_THEME.text,
                                border: selected
                                  ? "1px solid rgba(125, 201, 255, 0.42)"
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
                              }}
                            >
                              {item.label}
                            </Box>
                          );
                        })
                      : null}
                    {isConnected && showViewportIndicator ? (
                      <Box
                        sx={{
                          position: "absolute",
                          boxSizing: "border-box",
                          left: viewfinderViewportRect.x,
                          top: viewfinderViewportRect.y,
                          width: viewfinderViewportRect.width,
                          height: viewfinderViewportRect.height,
                          borderRadius: 1.5,
                          border: "2px solid rgba(125, 201, 255, 0.98)",
                          background:
                            "linear-gradient(135deg, rgba(125,201,255,0.18), rgba(177,149,255,0.14))",
                          boxShadow:
                            "0 0 0 1px rgba(8,17,31,0.62), inset 0 0 0 1px rgba(255,255,255,0.08)",
                          pointerEvents: "none",
                        }}
                      />
                    ) : null}
                  </Box>
                </Box>
                {showViewfinderHelper ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    {viewfinderHelperText}
                  </Typography>
                ) : null}
                {!previewNavigationEnabled && !topologyTrusted && normalizedDisplayTopology.length > 1 ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    Monitor layout shown from agent telemetry. Targeted monitor selection will unlock once it matches the live desktop geometry.
                  </Typography>
                ) : null}
                {!normalizedDisplayTopology.length && framebufferSize.width > 0 && framebufferSize.height > 0 ? (
                  <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                    Showing the live framebuffer as a single display map.
                  </Typography>
                ) : null}
                </Box>
                <Divider sx={{ borderColor: NAV_COLORS.line, mx: 2 }} />
                <SidebarNavRow
                  icon={<FitScreenIcon fontSize="small" />}
                  label="Fit"
                  active={displaySettingsEnabled && displayMode === "fit"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("fit")}
                />
                <SidebarNavRow
                  icon={<CropFreeIcon fontSize="small" />}
                  label="Actual"
                  active={displaySettingsEnabled && displayMode === "actual"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("actual")}
                />
                <SidebarNavRow
                  icon={<SwapHorizIcon fontSize="small" />}
                  label="Scaled"
                  active={displaySettingsEnabled && displayMode === "scaled"}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplayMode("scaled")}
                />
                <SidebarNavRow
                  icon={<DesktopIcon fontSize="small" />}
                  label={displaySelectorLabel}
                  active={displaySettingsEnabled && selectedDisplayId !== ALL_DISPLAYS_ID}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setDisplaySelectorExpanded((previous) => !previous)}
                  trailing={
                    <ExpandMoreIcon
                      sx={{
                        color: displaySettingsEnabled ? NAV_COLORS.cyan : "rgba(143,191,255,0.35)",
                        transform: displaySelectorExpanded ? "rotate(180deg)" : "none",
                        transition: "transform 140ms ease",
                      }}
                    />
                  }
                  ariaLabel="Choose display"
                />
                {displaySelectorExpanded ? (
                  <Box sx={{ pb: 0.5, pl: 1.5 }}>
                    {displaySelectorOptions.map((option) => (
                      <SidebarNavRow
                        key={option.id}
                        icon={<DesktopIcon fontSize="small" />}
                        label={option.label}
                        active={displaySettingsEnabled && selectedDisplayId === option.id}
                        disabled={!displaySettingsEnabled}
                        onClick={() => setSelectedDisplayId(option.id)}
                      />
                    ))}
                  </Box>
                ) : null}
                <SidebarNavRow
                  icon={<DesktopIcon fontSize="small" />}
                  label="View only"
                  active={displaySettingsEnabled && effectiveViewOnly}
                  disabled={!displaySettingsEnabled}
                  onClick={() => setViewOnly((previous) => !previous)}
                  trailing={
                    <Switch
                      checked={effectiveViewOnly}
                      onClick={(event) => event.stopPropagation()}
                      onChange={(event) => setViewOnly(event.target.checked)}
                      disabled={!displaySettingsEnabled}
                      size="small"
                      color="info"
                    />
                  }
                />
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="clipboard"
              title="Clipboard & Keys"
            >
              <>
              <SidebarNavRow
                icon={<ClipboardIcon fontSize="small" />}
                label="Sync Clipboard"
                active={showClipboardActions && clipboardSync}
                disabled={!showClipboardActions}
                onClick={() => setClipboardSync((previous) => !previous)}
                trailing={
                  <Switch
                    checked={clipboardSync}
                    onClick={(event) => event.stopPropagation()}
                    onChange={(event) => setClipboardSync(event.target.checked)}
                    disabled={!showClipboardActions}
                    size="small"
                    color="info"
                  />
                }
              />
              <SidebarNavRow
                icon={<ClipboardIcon fontSize="small" />}
                label="Paste Clipboard"
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={pasteClipboardText}
              />
              <SidebarNavRow
                icon={<KeyboardIcon fontSize="small" />}
                label="Inject Keystrokes"
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={() => injectClipboardKeystrokes()}
              />
              <SidebarNavRow
                icon={<KeyboardCommandKeyIcon fontSize="small" />}
                label="Send Ctrl+Alt+Del"
                disabled={!showClipboardActions || effectiveViewOnly}
                onClick={handleCtrlAltDel}
              />
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="power"
              title="Power"
            >
              <>
              <SidebarNavRow
                icon={<PowerIcon fontSize="small" />}
                label="Shutdown"
                disabled={!showPowerButtons || !shutdownSupported}
                onClick={() => handlePowerAction("shutdown")}
              />
              <SidebarNavRow
                icon={<RestartAltIcon fontSize="small" />}
                label="Restart"
                disabled={!showPowerButtons || !rebootSupported}
                onClick={() => handlePowerAction("reboot")}
              />
              <SidebarNavRow
                icon={<ReplayIcon fontSize="small" />}
                label="Reset"
                disabled={!showPowerButtons || !resetSupported}
                onClick={() => handlePowerAction("reset")}
              />
              </>
            </SidebarSection>

            <SidebarSection
              sectionId="session"
              title="Session Control"
            >
              <SidebarNavRow
                icon={<StopIcon fontSize="small" />}
                label="Disconnect"
                disabled={!isConnected}
                onClick={handleDisconnect}
              />
            </SidebarSection>
          </Box>

          <Box sx={{ px: 1, pb: 1, pt: 0.5 }}>
            <Box
              component="button"
              type="button"
              onClick={handleReturnToDevice}
              title={deviceHostname}
              sx={{
                width: "100%",
                height: 28,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                gap: 0.75,
                px: 1,
                background: "rgba(255,255,255,0.04)",
                border: `1px solid ${NAV_COLORS.line}`,
                borderRadius: 6,
                color: NAV_COLORS.cyan,
                cursor: "pointer",
                transition: "background 160ms ease, transform 120ms ease",
                "&:hover": {
                  background: "rgba(255,255,255,0.08)",
                },
                "&:active": {
                  transform: "translateY(1px)",
                },
              }}
            >
              <ChevronLeftIcon fontSize="small" />
              <Typography
                sx={{
                  color: NAV_COLORS.cyan,
                  fontSize: "0.8rem",
                  fontWeight: 600,
                  lineHeight: 1.1,
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                }}
              >
                {deviceHostname}
              </Typography>
            </Box>
          </Box>

        </Box>

        <Box
          sx={{
            display: "flex",
            flexDirection: "column",
            flexGrow: 1,
            minHeight: 0,
            minWidth: 0,
            overflow: "hidden",
          }}
        >
          <Box
            ref={containerRef}
            sx={{
              flexGrow: 1,
              minHeight: { xs: 420, lg: 0 },
              display: "flex",
              flexDirection: "column",
              ...glassCardSx,
              overflow: "hidden",
              position: "relative",
              borderRadius: { xs: 3, lg: 0 },
              borderTop: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRight: `1px solid ${MAGIC_UI.panelBorder}`,
              borderBottom: `1px solid ${MAGIC_UI.panelBorder}`,
              borderLeft: { xs: `1px solid ${MAGIC_UI.panelBorder}`, lg: "none" },
              boxShadow: { xs: "0 25px 80px rgba(2,6,23,0.6)", lg: "none" },
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
                sx={{
                  width: "100%",
                  height: "100%",
                  display: "block",
                  position: "relative",
                  overflow: "hidden",
                  minWidth: 0,
                  minHeight: 0,
                }}
              >
                <Box
                  ref={displayScrollRef}
                  sx={{
                    width: "100%",
                    height: "100%",
                    position: "relative",
                    overflow: "hidden",
                    minWidth: 0,
                    minHeight: 0,
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
                      minWidth: 0,
                      minHeight: 0,
                      "& canvas": {
                        display: "block",
                        flex: "0 0 auto",
                        maxWidth: "none",
                        maxHeight: "none",
                        boxShadow: VNC_CANVAS_BOX_SHADOW,
                      },
                    }}
                  />
                </Box>
              </Box>
              {!isConnected ? (
                <Stack
                  spacing={1.5}
                  sx={{
                    position: "absolute",
                    alignItems: "center",
                    textAlign: "center",
                    px: 2,
                    width: "100%",
                    maxWidth: 380,
                    "@keyframes remoteDesktopFlowSpin": {
                      from: { transform: "rotate(0deg)" },
                      to: { transform: "rotate(360deg)" },
                    },
                    "@keyframes remoteDesktopFlowPulse": {
                      "0%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0.28)" },
                      "70%": { boxShadow: "0 0 0 10px rgba(125, 201, 255, 0)" },
                      "100%": { boxShadow: "0 0 0 0 rgba(125, 201, 255, 0)" },
                    },
                  }}
                >
                  <DesktopIcon sx={{ color: MAGIC_UI.accentA, fontSize: 40 }} />
                  <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700 }}>
                    {showConnectingStatus ? "Connecting..." : "Ready to Connect"}
                  </Typography>
                  {showConnectionFlow ? (
                    <Box
                      sx={{
                        width: "100%",
                        display: "flex",
                        justifyContent: "center",
                        px: 1,
                      }}
                    >
                      <Box
                        sx={{
                          width: "100%",
                          maxWidth: 340,
                          display: "flex",
                          flexDirection: "column",
                          alignItems: "flex-start",
                        }}
                      >
                        {connectionFlowStages.map((step, index) => {
                          const isLast = index === connectionFlowStages.length - 1;
                          const iconColor =
                            step.status === "complete"
                              ? MAGIC_UI.accentC
                              : step.status === "active"
                                ? MAGIC_UI.accentA
                                : step.status === "failed"
                                  ? "#ff7b89"
                                  : "rgba(148, 163, 184, 0.58)";
                          const labelColor =
                            step.status === "pending" ? MAGIC_UI.textMuted : MAGIC_UI.textBright;
                          const detailColor =
                            step.status === "failed"
                              ? "#ff7b89"
                              : step.status === "active"
                                ? MAGIC_UI.accentA
                                : MAGIC_UI.textMuted;
                          const connectorColor =
                            step.status === "complete"
                              ? "linear-gradient(180deg, rgba(52,211,153,0.9), rgba(52,211,153,0.16))"
                              : step.status === "active"
                                ? "linear-gradient(180deg, rgba(125,201,255,0.95), rgba(125,201,255,0.18))"
                                : step.status === "failed"
                                  ? "linear-gradient(180deg, rgba(255,123,137,0.9), rgba(255,123,137,0.18))"
                                  : "rgba(148,163,184,0.18)";
                          const showStepDetail = step.status === "active" || step.status === "failed";
                          return (
                            <Box
                              key={step.id}
                              sx={{
                                display: "flex",
                                alignItems: "stretch",
                                gap: 1.25,
                                width: "100%",
                              }}
                            >
                              <Box
                                sx={{
                                  width: 22,
                                  display: "flex",
                                  flexDirection: "column",
                                  alignItems: "center",
                                  flexShrink: 0,
                                }}
                              >
                                <Box
                                  sx={{
                                    width: 20,
                                    height: 20,
                                    display: "flex",
                                    alignItems: "center",
                                    justifyContent: "center",
                                    color: iconColor,
                                    borderRadius: "50%",
                                    ...(step.status === "active"
                                      ? {
                                          background: "rgba(125, 201, 255, 0.12)",
                                          animation: "remoteDesktopFlowPulse 1.8s ease-out infinite",
                                        }
                                      : null),
                                  }}
                                >
                                  {step.status === "complete" ? (
                                    <StageCompleteIcon sx={{ fontSize: 18 }} />
                                  ) : step.status === "active" ? (
                                    <StageActiveIcon
                                      sx={{
                                        fontSize: 18,
                                        animation: "remoteDesktopFlowSpin 1.15s linear infinite",
                                      }}
                                    />
                                  ) : step.status === "failed" ? (
                                    <StageErrorIcon sx={{ fontSize: 18 }} />
                                  ) : (
                                    <StagePendingIcon sx={{ fontSize: 18 }} />
                                  )}
                                </Box>
                                {!isLast ? (
                                  <Box
                                    sx={{
                                      mt: 0.4,
                                      mb: 0.2,
                                      width: 2,
                                      flexGrow: 1,
                                      minHeight: showStepDetail ? 22 : 14,
                                      borderRadius: 999,
                                      background: connectorColor,
                                    }}
                                  />
                                ) : null}
                              </Box>
                              <Box
                                sx={{
                                  pt: 0.05,
                                  pb: isLast ? 0 : 0.65,
                                  minWidth: 0,
                                  textAlign: "left",
                                }}
                              >
                                <Typography
                                  sx={{
                                    color: labelColor,
                                    fontSize: "0.85rem",
                                    fontWeight:
                                      step.status === "active" || step.status === "complete" ? 600 : 500,
                                    letterSpacing: 0.2,
                                  }}
                                >
                                  {step.label}
                                </Typography>
                                {showStepDetail ? (
                                  <Typography
                                    sx={{
                                      mt: 0.2,
                                      color: detailColor,
                                      fontSize: "0.74rem",
                                      lineHeight: 1.35,
                                      maxWidth: 280,
                                    }}
                                  >
                                    {step.detail}
                                  </Typography>
                                ) : null}
                              </Box>
                            </Box>
                          );
                        })}
                      </Box>
                    </Box>
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
      </Box>
    </Box>
  );
}
