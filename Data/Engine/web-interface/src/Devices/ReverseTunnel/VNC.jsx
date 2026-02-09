import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Chip,
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
  LanRounded as IpIcon,
  SecurityRounded as SecurityIcon,
  OpenInNewRounded as OpenIcon,
  DownloadRounded as DownloadIcon,
  KeyboardRounded as KeyboardIcon,
  ContentPasteRounded as ClipboardIcon,
  PowerSettingsNewRounded as PowerIcon,
  AspectRatioRounded as DisplayIcon,
  FolderRounded as FileIcon,
  UploadRounded as UploadIcon,
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

const viewportPresets = [
  { value: "all", label: "All Monitors" },
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

function buildCertHelp(wsUrl) {
  if (!wsUrl) return null;
  try {
    const parsed = new URL(wsUrl);
    const isSecure = parsed.protocol === "wss:";
    const trustScheme = isSecure ? "https:" : "http:";
    return {
      wsUrl,
      isSecure,
      host: parsed.host,
      trustUrl: `${trustScheme}//${parsed.host}/`,
      trustCheck: "unknown",
    };
  } catch {
    return null;
  }
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

async function readClipboardText() {
  if (!navigator?.clipboard?.readText) return "";
  try {
    return await navigator.clipboard.readText();
  } catch {
    return "";
  }
}

async function writeClipboardText(text) {
  if (!navigator?.clipboard?.writeText) return;
  try {
    await navigator.clipboard.writeText(text || "");
  } catch {
    // ignore
  }
}

export default function ReverseTunnelVnc({ device }) {
  const [sessionState, setSessionState] = useState("idle");
  const [statusMessage, setStatusMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [tunnel, setTunnel] = useState(null);
  const [certHelp, setCertHelp] = useState(null);
  const [viewOnly, setViewOnly] = useState(false);
  const [clipboardSync, setClipboardSync] = useState(true);
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
  const certProbeRef = useRef("");
  const clipboardSyncRef = useRef(clipboardSync);
  const clipboardLastRef = useRef("");
  const certDownloadUrl = "/api/server/certificates/root";

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

  useEffect(() => {
    agentIdRef.current = agentId;
  }, [agentId]);

  useEffect(() => {
    clipboardSyncRef.current = clipboardSync;
  }, [clipboardSync]);

  useEffect(() => {
    if (!clipboardSync) return;
    let active = true;
    const interval = setInterval(async () => {
      if (!active) return;
      const rfb = rfbRef.current;
      if (!rfb) return;
      const text = await readClipboardText();
      if (!text || text === clipboardLastRef.current) return;
      clipboardLastRef.current = text;
      try {
        rfb.clipboardPasteFrom(text);
      } catch {
        // ignore clipboard send failures
      }
    }, 1500);
    return () => {
      active = false;
      clearInterval(interval);
    };
  }, [clipboardSync]);

  const probeCertificateTrust = useCallback(async (info) => {
    if (!info || !info.isSecure || !info.trustUrl) return;
    if (certProbeRef.current === info.trustUrl) return;
    certProbeRef.current = info.trustUrl;
    try {
      await fetch(info.trustUrl, { mode: "no-cors", cache: "no-store" });
      setCertHelp((prev) => (prev ? { ...prev, trustCheck: "ok" } : prev));
    } catch {
      setCertHelp((prev) => (prev ? { ...prev, trustCheck: "blocked" } : prev));
    }
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
      setCertHelp(null);
      setSessionState("idle");
      clipboardLastRef.current = "";
      setLoading(false);
    }
  }, [disconnectVnc, teardownDisplay]);

  useEffect(() => {
    return () => {
      teardownDisplay();
      disconnectVnc("component_unmount");
    };
  }, [disconnectVnc, teardownDisplay]);

  useEffect(() => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    rfb.showDotCursor = true;
    rfb.viewOnly = viewOnly;
    rfb.clipViewport = clipViewport;
    rfb.dragViewport = dragViewport;
    rfb.scaleViewport = scaleViewport;
    rfb.resizeSession = resizeSession;
    rfb.qualityLevel = qualityLevel;
    rfb.compressionLevel = compressionLevel;
  }, [
    viewOnly,
    clipViewport,
    dragViewport,
    scaleViewport,
    resizeSession,
    qualityLevel,
    compressionLevel,
  ]);

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
        body: JSON.stringify({ agent_id: agentId, remove_wallpaper: true }),
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

  const openVncSession = useCallback(
    async (data) => {
      const wsUrl = data?.ws_url;
      const vncPassword = data?.vnc_password || "";
      if (!wsUrl) {
        throw new Error("VNC session unavailable.");
      }
      const help = buildCertHelp(wsUrl);
      setCertHelp(help);
      probeCertificateTrust(help);
      const tunnelUrl = wsUrl;
      const displayHost = displayRef.current;
      if (!displayHost) {
        throw new Error("VNC display container missing.");
      }
      displayHost.innerHTML = "";

      const rfb = new RFB(displayHost, tunnelUrl, {
        credentials: { password: vncPassword },
      });
      // Always show a local dot cursor so we never lose pointer visibility.
      rfb.showDotCursor = true;
      rfb.scaleViewport = scaleViewport;
      rfb.resizeSession = resizeSession;
      rfb.clipViewport = clipViewport;
      rfb.dragViewport = dragViewport;
      rfb.viewOnly = viewOnly;
      rfb.qualityLevel = qualityLevel;
      rfb.compressionLevel = compressionLevel;

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
      rfb.addEventListener("capabilities", (evt) => {
        const caps = evt?.detail?.capabilities || rfb.capabilities || {};
        setCapabilities(caps || {});
      });
      rfb.addEventListener("clipboard", (evt) => {
        const text = evt?.detail?.text || "";
        clipboardLastRef.current = text;
        if (clipboardSyncRef.current) {
          writeClipboardText(text);
        }
      });

      rfbRef.current = rfb;
      setStatusMessage("Establishing VNC...");
    },
    [
      clipViewport,
      compressionLevel,
      dragViewport,
      probeCertificateTrust,
      qualityLevel,
      resizeSession,
      scaleViewport,
      viewOnly,
    ]
  );

  const handleConnect = useCallback(async () => {
    if (sessionState === "connected") return;
    setStatusMessage("");
    setSessionState("connecting");
    certProbeRef.current = "";
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

  const injectClipboardKeystrokes = useCallback(async () => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    const text = await readClipboardText();
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
  }, []);

  const handleCtrlAltDel = useCallback(() => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    try {
      rfb.sendCtrlAltDel();
      setStatusMessage("Sent Ctrl+Alt+Del.");
    } catch {
      setStatusMessage("Failed to send Ctrl+Alt+Del.");
    }
  }, []);

  const handlePowerAction = useCallback(
    (action) => {
      const rfb = rfbRef.current;
      if (!rfb) return;
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
    []
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
  const showCertHelp =
    certHelp?.isSecure && (certHelp.trustCheck === "blocked" || sessionState === "error");
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

          {showCertHelp ? (
            <Box
              sx={{
                borderRadius: 2,
                border: `1px solid ${MAGIC_UI.panelBorder}`,
                backgroundColor: "rgba(10,16,30,0.75)",
                p: 2,
              }}
            >
              <Stack spacing={1}>
                <Stack direction="row" spacing={1} alignItems="center">
                  <SecurityIcon sx={{ color: MAGIC_UI.accentC, fontSize: 20 }} />
                  <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 600 }}>
                    VNC proxy certificate not trusted
                  </Typography>
                </Stack>
                <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                  Your browser blocked the secure VNC WebSocket. Trust the Borealis root CA (or use a
                  publicly trusted certificate) so the VNC proxy can connect.
                </Typography>
                <Stack direction={{ xs: "column", sm: "row" }} spacing={1}>
                  <Button
                    size="small"
                    startIcon={<OpenIcon />}
                    variant="outlined"
                    sx={simpleButtonSx}
                    onClick={() => {
                      if (certHelp?.trustUrl) {
                        window.open(certHelp.trustUrl, "_blank", "noreferrer");
                      }
                    }}
                  >
                    Open VNC Proxy
                  </Button>
                  <Button
                    size="small"
                    startIcon={<DownloadIcon />}
                    sx={{
                      ...simpleButtonSx,
                      color: SIDEBAR_THEME.text,
                    }}
                    variant="outlined"
                    component="a"
                    href={certDownloadUrl}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Download Root CA
                  </Button>
                </Stack>
                <Typography variant="caption" sx={{ color: MAGIC_UI.textMuted }}>
                  After downloading, install the root CA into the OS trusted root store and refresh the
                  page. If you are behind a corporate CA, install that CA instead.
                </Typography>
              </Stack>
            </Box>
          ) : null}

        </Box>

        <Box sx={sidebarSx}>
          <Button
            size="small"
            startIcon={isConnected ? <StopIcon /> : <PlayIcon />}
            variant="outlined"
            sx={{ ...simpleButtonSx, width: "100%", minHeight: 40, py: 1 }}
            disabled={loading || (!isConnected && !agentId)}
            onClick={isConnected ? handleDisconnect : handleConnect}
          >
            {isConnected ? "Disconnect" : "Connect"}
          </Button>
          <Stack spacing={1}>
            <Box sx={sectionHeaderSx}>
              <DisplayIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Display</span>
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
                    checked={viewOnly}
                    onChange={(event) => setViewOnly(event.target.checked)}
                    size="small"
                    color="info"
                  />
                }
                label={<Typography variant="body2">View only</Typography>}
              />

              <Divider sx={{ borderColor: "rgba(148,163,184,0.15)" }} />

              <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
                Monitor focus
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
          </Stack>

          <Stack spacing={1}>
            <Box sx={sectionHeaderSx}>
              <ClipboardIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Clipboard</span>
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
                label={<Typography variant="body2">Sync clipboard</Typography>}
              />
              <Button
                size="small"
                variant="outlined"
                startIcon={<KeyboardIcon />}
                sx={simpleButtonSx}
                onClick={injectClipboardKeystrokes}
              >
                Inject Keystrokes
              </Button>
            </Stack>
          </Stack>

          <Stack spacing={1}>
            <Box sx={sectionHeaderSx}>
              <FileIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>File Transfer</span>
            </Box>
            <Stack spacing={1} direction={{ xs: "column", sm: "row" }}>
              <Button
                size="small"
                variant="outlined"
                startIcon={<UploadIcon />}
                sx={{ ...simpleButtonSx, flex: 1 }}
                disabled
              >
                Upload
              </Button>
              <Button
                size="small"
                variant="outlined"
                startIcon={<DownloadIcon />}
                sx={{ ...simpleButtonSx, flex: 1 }}
                disabled
              >
                Download
              </Button>
            </Stack>
            <Typography variant="caption" sx={{ color: SIDEBAR_THEME.muted }}>
              File transfer support is coming soon.
            </Typography>
          </Stack>

          <Stack spacing={1}>
            <Box sx={sectionHeaderSx}>
              <KeyboardIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Keyboard Shortcuts</span>
            </Box>
            <Stack spacing={1}>
              <Button
                size="small"
                variant="outlined"
                startIcon={<KeyboardIcon />}
                sx={simpleButtonSx}
                onClick={handleCtrlAltDel}
              >
                Send Ctrl+Alt+Del
              </Button>
            </Stack>
          </Stack>

          <Stack spacing={1}>
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
                  onClick={() => handlePowerAction("shutdown")}
                >
                  Shutdown
                </Button>
                <Button
                  size="small"
                  variant="outlined"
                  sx={deleteButtonSx}
                  onClick={() => handlePowerAction("reboot")}
                >
                  Restart
                </Button>
              </Stack>
            </Stack>
          </Stack>

          <Stack spacing={1}>
            <Box sx={sectionHeaderSx}>
              <LinkIcon sx={{ fontSize: 18, color: SIDEBAR_THEME.accent }} />
              <span>Tunnel Information</span>
            </Box>
            <Stack spacing={1}>
              {sessionChips.length ? (
                <Stack direction="row" spacing={1} flexWrap="wrap">
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
              ) : (
                <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                  No active tunnel.
                </Typography>
              )}
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
                Session: {isConnected ? "Active" : sessionState}
              </Typography>
              {statusMessage ? (
                <Typography
                  variant="body2"
                  sx={{ color: sessionState === "error" ? "#ff7b89" : MAGIC_UI.textMuted }}
                >
                  {statusMessage}
                </Typography>
              ) : null}
            </Stack>
          </Stack>

        </Box>
      </Box>
    </Box>
  );
}
