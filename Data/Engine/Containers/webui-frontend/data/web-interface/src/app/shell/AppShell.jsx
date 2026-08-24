import React, { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import {
  Alert,
  AppBar,
  Box,
  Breadcrumbs,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  LinearProgress,
  Menu,
  MenuItem,
  TextField,
  Toolbar,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  Fingerprint as FingerprintIcon,
  KeyboardArrowDown as KeyboardArrowDownIcon,
  LockReset as LockResetIcon,
  Logout as LogoutIcon,
  NavigateNext as NavigateNextIcon,
  VpnKey as VpnKeyIcon,
} from "@mui/icons-material";
import { Outlet, useLocation, useMatches, useNavigate, useNavigation } from "react-router-dom";
import { NotAuthorizedDialog } from "../../Dialogs.jsx";
import NavigationSidebar from "../../Navigation_Sidebar.jsx";
import GlobalDeviceSearch from "../../GlobalDeviceSearch.jsx";
import Notifications from "../../Notifications.jsx";
import { PageHeaderActionRail } from "../../Page_Header_Actions.jsx";
import AegisCipherDialog from "../../Access_Management/Aegis_Cipher_Dialog.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { useAuth } from "../providers/AuthContext.jsx";
import { usePageChrome } from "../providers/PageChromeContext.jsx";
import {
  APP_DOCUMENT_TITLE,
  buildBreadcrumbItems,
  formatDocumentTitle,
  resolveActiveNavKey,
  resolveCurrentPageKey,
  resolvePageChromeDefaults,
} from "../routes/breadcrumbs.js";
import { APP_PATHS } from "../routes/paths.js";
import {
  clearRouteRequestProgress,
  getRouteRequestProgressSnapshot,
  startRouteRequestProgress,
  subscribeRouteRequestProgress,
} from "../routes/routeData.js";
import { getBorealisSocket } from "../runtime/bootstrapClientRuntime.js";
import { formatOperatorPresencePage } from "../utils/operatorPresence.js";
import { APP_AURORA_BACKGROUND } from "../utils/theme.js";
import PageSubtitleMarkdown from "./PageSubtitleMarkdown.jsx";

function resolveDeviceId(device) {
  return (
    device?.agent_guid ||
    device?.guid ||
    device?.summary?.agent_guid ||
    device?.hostname ||
    device?.id ||
    null
  );
}

function stripTransientLocationState(state) {
  if (!state || typeof state !== "object") {
    return null;
  }

  const nextState = { ...state };
  delete nextState.showNotAuthorizedDialog;
  return Object.keys(nextState).length ? nextState : null;
}

function formatPasskeyTimestamp(value) {
  const ts = Number(value || 0);
  if (!ts) return "Never";
  try {
    return new Date(ts * 1000).toLocaleString();
  } catch {
    return "Unknown";
  }
}

function formatPasskeyTransportLabel(transports) {
  if (!Array.isArray(transports) || !transports.length) {
    return "Platform or roaming authenticator";
  }
  return transports
    .map((item) => String(item || "").trim())
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(", ");
}

export default function AppShell() {
  const location = useLocation();
  const navigate = useNavigate();
  const matches = useMatches();
  const navigation = useNavigation();
  const routeRequestProgress = useSyncExternalStore(
    subscribeRouteRequestProgress,
    getRouteRequestProgressSnapshot,
    getRouteRequestProgressSnapshot
  );
  const {
    user,
    displayName,
    authSource,
    mfaEnabled,
    passkeyCount,
    logout,
    registerPasskey,
    listPasskeys,
    updatePasskeyLabel,
    deletePasskey,
    resetPassword,
    resetMfa,
    aegisDialog,
    closeAegisDialog,
    completeAegisDialog,
    ready,
    isAdmin,
  } = useAuth();
  const { pageChrome } = usePageChrome();

  const [notAuthorizedOpen, setNotAuthorizedOpen] = useState(false);
  const [userMenuAnchorEl, setUserMenuAnchorEl] = useState(null);
  const [resetOwnPasswordOpen, setResetOwnPasswordOpen] = useState(false);
  const [resetOwnPasswordBusy, setResetOwnPasswordBusy] = useState(false);
  const [resetOwnPasswordCurrent, setResetOwnPasswordCurrent] = useState("");
  const [resetOwnPasswordNext, setResetOwnPasswordNext] = useState("");
  const [resetOwnPasswordConfirm, setResetOwnPasswordConfirm] = useState("");
  const [resetOwnPasswordError, setResetOwnPasswordError] = useState("");
  const [resetOwnMfaOpen, setResetOwnMfaOpen] = useState(false);
  const [resetOwnMfaBusy, setResetOwnMfaBusy] = useState(false);
  const [addPasskeyBusy, setAddPasskeyBusy] = useState(false);
  const [managePasskeysOpen, setManagePasskeysOpen] = useState(false);
  const [managePasskeysLoading, setManagePasskeysLoading] = useState(false);
  const [managePasskeysError, setManagePasskeysError] = useState("");
  const [managePasskeyRows, setManagePasskeyRows] = useState([]);
  const [managePasskeyLabels, setManagePasskeyLabels] = useState({});
  const [managePasskeyAction, setManagePasskeyAction] = useState({ id: null, type: "" });
  const [newPasskeyLabel, setNewPasskeyLabel] = useState("");
  const [removePasskeyTarget, setRemovePasskeyTarget] = useState(null);
  const [clusterBanner, setClusterBanner] = useState(null);

  const defaultChrome = useMemo(() => resolvePageChromeDefaults(matches), [matches]);
  const activeNavKey = useMemo(() => resolveActiveNavKey(matches), [matches]);
  const currentPageKey = useMemo(() => resolveCurrentPageKey(matches), [matches]);
  const hideNavigationSidebar = currentPageKey === "device" || currentPageKey === "device-remote-desktop";
  const passkeysDisabledForDirectoryUser = String(authSource || "").toLowerCase() === "directory";
  const managePasskeysDisabled = passkeysDisabledForDirectoryUser || managePasskeysLoading || addPasskeyBusy;

  const resolvedChrome = useMemo(
    () => ({
      title: pageChrome.title || defaultChrome.title || "",
      subtitle: pageChrome.subtitle || defaultChrome.subtitle || "",
      Icon: pageChrome.Icon || defaultChrome.Icon || null,
      actions: pageChrome.actions?.length ? pageChrome.actions : defaultChrome.actions || [],
      controls: pageChrome.controls?.length ? pageChrome.controls : defaultChrome.controls || [],
      breadcrumbLabel:
        pageChrome.breadcrumbLabel || pageChrome.title || defaultChrome.title || "",
      navigationSidebar: pageChrome.navigationSidebar || null,
    }),
    [defaultChrome, pageChrome]
  );

  const breadcrumbs = useMemo(
    () => buildBreadcrumbItems(matches, resolvedChrome),
    [matches, resolvedChrome]
  );

  const hasPageHeader = Boolean(
    resolvedChrome.title ||
      resolvedChrome.subtitle ||
      resolvedChrome.Icon ||
      resolvedChrome.actions?.length ||
      resolvedChrome.controls?.length
  );

  const isBufferedRoutePending = useMemo(() => {
    if (navigation.state === "idle" || !navigation.location) {
      return false;
    }
    const nextTarget = `${navigation.location.pathname}${navigation.location.search || ""}`;
    const currentTarget = `${location.pathname}${location.search || ""}`;
    return nextTarget !== currentTarget;
  }, [location.pathname, location.search, navigation.location, navigation.state]);
  const routeRequestProgressValue = useMemo(() => {
    const total = Number(routeRequestProgress?.total || 0);
    const completed = Math.min(Number(routeRequestProgress?.completed || 0), total);
    return total > 0 ? Math.round((completed / total) * 100) : 0;
  }, [routeRequestProgress]);
  const routeRequestProgressLabel = useMemo(() => {
    const total = Number(routeRequestProgress?.total || 0);
    const completed = Math.min(Number(routeRequestProgress?.completed || 0), total);
    if (total <= 0) {
      return "Preparing requests...";
    }
    return `${completed} of ${total} Requests Complete`;
  }, [routeRequestProgress]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    document.title = formatDocumentTitle(resolvedChrome.title);
  }, [resolvedChrome.title]);

  useEffect(() => {
    if (isBufferedRoutePending && navigation.location) {
      startRouteRequestProgress(
        `${navigation.location.pathname}${navigation.location.search || ""}`
      );
      return;
    }
    clearRouteRequestProgress();
  }, [isBufferedRoutePending, navigation.location]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return undefined;
    }

    return () => {
      document.title = APP_DOCUMENT_TITLE;
    };
  }, []);

  useEffect(() => {
    let active = true;
    const refreshClusterBanner = async () => {
      try {
        const response = await fetch("/api/server/cluster/banner", { credentials: "include", cache: "no-store" });
        const payload = await response.json().catch(() => ({}));
        if (active && response.ok) setClusterBanner(payload);
      } catch {
        // Main route owns error reporting; banner polling stays silent.
      }
    };
    void refreshClusterBanner();
    const timer = window.setInterval(refreshClusterBanner, 5000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

  const clearResetOwnPasswordState = useCallback(() => {
    setResetOwnPasswordOpen(false);
    setResetOwnPasswordBusy(false);
    setResetOwnPasswordCurrent("");
    setResetOwnPasswordNext("");
    setResetOwnPasswordConfirm("");
    setResetOwnPasswordError("");
  }, []);

  useEffect(() => {
    const queryParams = new URLSearchParams(location.search);
    const hasNotAuthorizedQuery = queryParams.get("not_authorized") === "1";
    if (!location.state?.showNotAuthorizedDialog && !hasNotAuthorizedQuery) {
      return;
    }
    setNotAuthorizedOpen(true);
    if (hasNotAuthorizedQuery) {
      queryParams.delete("not_authorized");
    }
    navigate(
      {
        pathname: location.pathname,
        search: queryParams.toString() ? `?${queryParams.toString()}` : "",
      },
      {
        replace: true,
        state: stripTransientLocationState(location.state),
      }
    );
  }, [location.pathname, location.search, location.state, navigate]);

  const syncOperatorPresence = useCallback(() => {
    if (!ready || !user) return;
    try {
      getBorealisSocket()?.emit?.("operator_presence_sync", {
        current_page: formatOperatorPresencePage(currentPageKey, resolvedChrome.title),
        page_key: currentPageKey || activeNavKey,
      });
    } catch {
      /* operator presence is best-effort */
    }
  }, [activeNavKey, currentPageKey, ready, resolvedChrome.title, user]);

  useEffect(() => {
    const socket = getBorealisSocket();
    if (!socket || typeof socket.on !== "function" || !ready) {
      return undefined;
    }

    if (!user) {
      try {
        socket.emit?.("operator_presence_clear");
      } catch {
        /* noop */
      }
      return undefined;
    }

    syncOperatorPresence();
    const intervalId = window.setInterval(syncOperatorPresence, 30 * 1000);
    socket.on("connect", syncOperatorPresence);
    return () => {
      window.clearInterval(intervalId);
      try {
        socket.off("connect", syncOperatorPresence);
      } catch {
        /* noop */
      }
    };
  }, [ready, syncOperatorPresence, user]);

  const handleUserMenuOpen = useCallback((event) => {
    setUserMenuAnchorEl(event.currentTarget);
  }, []);

  const handleUserMenuClose = useCallback(() => {
    setUserMenuAnchorEl(null);
  }, []);

  const handleGlobalDeviceSelect = useCallback(
    (device) => {
      const deviceId = resolveDeviceId(device);
      if (!deviceId) return;
      navigate(APP_PATHS.device(deviceId), {
        state: device ? { initialDevice: device } : undefined,
      });
    },
    [navigate]
  );

  const handleOpenResetOwnPasswordDialog = useCallback(() => {
    handleUserMenuClose();
    if (resetOwnPasswordBusy) return;
    setResetOwnPasswordError("");
    setResetOwnPasswordCurrent("");
    setResetOwnPasswordNext("");
    setResetOwnPasswordConfirm("");
    setResetOwnPasswordOpen(true);
  }, [handleUserMenuClose, resetOwnPasswordBusy]);

  const handleCloseResetOwnPasswordDialog = useCallback(() => {
    if (resetOwnPasswordBusy) return;
    clearResetOwnPasswordState();
  }, [clearResetOwnPasswordState, resetOwnPasswordBusy]);

  const handleResetOwnPassword = useCallback(async () => {
    if (resetOwnPasswordBusy) return;
    setResetOwnPasswordBusy(true);
    setResetOwnPasswordError("");
    try {
      await resetPassword({
        currentPassword: resetOwnPasswordCurrent,
        nextPassword: resetOwnPasswordNext,
        confirmPassword: resetOwnPasswordConfirm,
      });
      clearResetOwnPasswordState();
    } catch (error) {
      setResetOwnPasswordError(
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not update your password."
      );
    } finally {
      setResetOwnPasswordBusy(false);
    }
  }, [
    clearResetOwnPasswordState,
    resetOwnPasswordBusy,
    resetOwnPasswordConfirm,
    resetOwnPasswordCurrent,
    resetOwnPasswordNext,
    resetPassword,
  ]);

  const syncManagedPasskeys = useCallback((items) => {
    const nextItems = Array.isArray(items) ? items : [];
    setManagePasskeyRows(nextItems);
    setManagePasskeyLabels(
      nextItems.reduce((acc, item) => {
        acc[item.id] = item.label || "Passkey";
        return acc;
      }, {})
    );
  }, []);

  const loadManagedPasskeys = useCallback(async () => {
    setManagePasskeysLoading(true);
    setManagePasskeysError("");
    try {
      const items = await listPasskeys();
      syncManagedPasskeys(items);
    } catch (error) {
      setManagePasskeysError(
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not load your passkeys."
      );
    } finally {
      setManagePasskeysLoading(false);
    }
  }, [listPasskeys, syncManagedPasskeys]);

  const handleOpenManagePasskeysDialog = useCallback(() => {
    handleUserMenuClose();
    if (managePasskeysLoading || passkeysDisabledForDirectoryUser) return;
    setManagePasskeysOpen(true);
    void loadManagedPasskeys();
  }, [handleUserMenuClose, loadManagedPasskeys, managePasskeysLoading, passkeysDisabledForDirectoryUser]);

  const handleCloseManagePasskeysDialog = useCallback(() => {
    if (addPasskeyBusy || managePasskeyAction.id) return;
    setManagePasskeysOpen(false);
    setManagePasskeysError("");
    setNewPasskeyLabel("");
    setRemovePasskeyTarget(null);
  }, [addPasskeyBusy, managePasskeyAction.id]);

  const handleAddPasskey = useCallback(async () => {
    if (addPasskeyBusy) return;
    setAddPasskeyBusy(true);
    setManagePasskeysError("");
    try {
      await registerPasskey({ label: newPasskeyLabel });
      setNewPasskeyLabel("");
      if (managePasskeysOpen) {
        await loadManagedPasskeys();
      }
    } catch (error) {
      setManagePasskeysError(
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not add a passkey right now."
      );
    } finally {
      setAddPasskeyBusy(false);
    }
  }, [addPasskeyBusy, loadManagedPasskeys, managePasskeysOpen, newPasskeyLabel, registerPasskey]);

  const handleManagedPasskeyLabelChange = useCallback((passkeyId, value) => {
    setManagePasskeyLabels((current) => ({
      ...current,
      [passkeyId]: value,
    }));
  }, []);

  const handleSaveManagedPasskey = useCallback(
    async (passkeyId) => {
      if (managePasskeyAction.id) return;
      setManagePasskeyAction({ id: passkeyId, type: "save" });
      setManagePasskeysError("");
      try {
        const payload = await updatePasskeyLabel({
          passkeyId,
          label: managePasskeyLabels[passkeyId] || "",
        });
        const updatedPasskey = payload?.passkey || null;
        if (updatedPasskey?.id) {
          setManagePasskeyRows((current) =>
            current.map((item) => (item.id === updatedPasskey.id ? updatedPasskey : item))
          );
          setManagePasskeyLabels((current) => ({
            ...current,
            [updatedPasskey.id]: updatedPasskey.label || "Passkey",
          }));
        }
      } catch (error) {
        setManagePasskeysError(
          error instanceof Error && error.message
            ? error.message
            : "Borealis could not update that passkey."
        );
      } finally {
        setManagePasskeyAction({ id: null, type: "" });
      }
    },
    [managePasskeyAction.id, managePasskeyLabels, updatePasskeyLabel]
  );

  const handleRequestRemoveManagedPasskey = useCallback((passkey) => {
    setRemovePasskeyTarget(passkey);
  }, []);

  const handleCancelRemoveManagedPasskey = useCallback(() => {
    if (managePasskeyAction.id) return;
    setRemovePasskeyTarget(null);
  }, [managePasskeyAction.id]);

  const handleConfirmRemoveManagedPasskey = useCallback(async () => {
    if (!removePasskeyTarget?.id || managePasskeyAction.id) return;
    setManagePasskeyAction({ id: removePasskeyTarget.id, type: "delete" });
    setManagePasskeysError("");
    try {
      await deletePasskey({ passkeyId: removePasskeyTarget.id });
      setManagePasskeyRows((current) =>
        current.filter((item) => item.id !== removePasskeyTarget.id)
      );
      setManagePasskeyLabels((current) => {
        const next = { ...current };
        delete next[removePasskeyTarget.id];
        return next;
      });
      setRemovePasskeyTarget(null);
    } catch (error) {
      setManagePasskeysError(
        error instanceof Error && error.message
          ? error.message
          : "Borealis could not remove that passkey."
      );
    } finally {
      setManagePasskeyAction({ id: null, type: "" });
    }
  }, [deletePasskey, managePasskeyAction.id, removePasskeyTarget]);

  const handleOpenResetOwnMfaDialog = useCallback(() => {
    handleUserMenuClose();
    if (!mfaEnabled || resetOwnMfaBusy) return;
    setResetOwnMfaOpen(true);
  }, [handleUserMenuClose, mfaEnabled, resetOwnMfaBusy]);

  const handleCloseResetOwnMfaDialog = useCallback(() => {
    if (resetOwnMfaBusy) return;
    setResetOwnMfaOpen(false);
  }, [resetOwnMfaBusy]);

  const handleResetOwnMfa = useCallback(async () => {
    if (resetOwnMfaBusy) return;
    setResetOwnMfaBusy(true);
    try {
      await resetMfa();
      setResetOwnMfaOpen(false);
    } catch {
      /* notifications are already emitted by the auth layer */
    } finally {
      setResetOwnMfaBusy(false);
    }
  }, [resetMfa, resetOwnMfaBusy]);

  const handleLogout = useCallback(async () => {
    await logout();
    navigate(APP_PATHS.login, { replace: true });
  }, [logout, navigate]);

  const HeaderIcon = resolvedChrome.Icon;

  return (
    <>
      <Notifications socket={getBorealisSocket()} currentUser={user} />
      <AegisCipherDialog
        open={Boolean(aegisDialog)}
        mode={aegisDialog?.mode || "unlock"}
        source={aegisDialog?.source || "credentials"}
        onClose={closeAegisDialog}
        onCompleted={completeAegisDialog}
      />
      <Box
        sx={{
          width: "100vw",
          height: "100vh",
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          background: APP_AURORA_BACKGROUND,
          backgroundRepeat: "no-repeat",
          backgroundSize: "cover",
          backgroundColor: "#040711",
        }}
      >
        <AppBar
          position="static"
          sx={{
            background:
              "linear-gradient(90deg, rgba(10,14,22,0.65) 0%, rgba(10,14,22,0.20) 20%), " +
              "linear-gradient(90deg, rgba(64,164,255,0.5) 0%, rgba(132,252,230,0.39) 100%)",
            boxShadow: "0 0 20px rgba(125,183,255,0.12)",
            backdropFilter: "blur(10px) saturate(140%)",
            borderBottom: "1px solid rgba(125,183,255,0.20)",
          }}
        >
          <Toolbar sx={{ minHeight: 40, alignItems: "center", gap: 1 }}>
            <Box
              component="img"
              src="/Borealis_Logo_Full.png"
              alt="Borealis Logo"
              sx={{ height: 50, ml: -1.8, mr: 1.5 }}
            />

            <Box
              sx={{
                ml: 2,
                flex: { xs: "1 1 220px", md: "0 1 460px" },
                minWidth: 0,
                maxWidth: 520,
              }}
            >
              <GlobalDeviceSearch onSelectDevice={handleGlobalDeviceSelect} />
            </Box>

            <Box sx={{ ml: 2, display: "flex", alignItems: "center" }}>
              <Breadcrumbs
                separator={<NavigateNextIcon fontSize="inherit" sx={{ color: "#6b6b6b" }} />}
                aria-label="breadcrumb"
                sx={{
                  color: "#9aa0a6",
                  fontSize: "0.825rem",
                  "& .MuiBreadcrumbs-separator": { mx: 0.6 },
                }}
              >
                {breadcrumbs.map((item) =>
                  item.to ? (
                    <Button
                      key={item.id}
                      onClick={() => navigate(item.to)}
                      size="small"
                      sx={{
                        color: "#cde1ff",
                        textTransform: "none",
                        minWidth: 0,
                        p: 0,
                        fontSize: "0.825rem",
                      }}
                    >
                      {item.label}
                    </Button>
                  ) : (
                    <Typography
                      key={item.id}
                      component="span"
                      sx={{ color: "#f5f5f5", fontSize: "0.825rem" }}
                    >
                      {item.label}
                    </Typography>
                  )
                )}
              </Breadcrumbs>
            </Box>

            <Box sx={{ flexGrow: 1 }} />

            <Button
              color="inherit"
              onClick={handleUserMenuOpen}
              endIcon={<KeyboardArrowDownIcon />}
              sx={{ height: 36 }}
            >
              {displayName || user || "User"}
            </Button>
            <Menu
              anchorEl={userMenuAnchorEl}
              open={Boolean(userMenuAnchorEl)}
              onClose={handleUserMenuClose}
            >
              <Tooltip
                title={passkeysDisabledForDirectoryUser ? "Passkeys are Disallowed for Directory Users" : ""}
                placement="left"
              >
                <span style={{ display: "block" }}>
                  <MenuItem
                    disabled={managePasskeysDisabled}
                    onClick={handleOpenManagePasskeysDialog}
                  >
                    <FingerprintIcon
                      sx={{
                        fontSize: 18,
                        color: !managePasskeysDisabled ? "#8ecbff" : "rgba(148,163,184,0.62)",
                        mr: 1,
                      }}
                    />
                    {managePasskeysLoading
                      ? "Loading Passkeys..."
                      : `Manage Passkeys${passkeyCount ? ` (${passkeyCount})` : ""}`}
                  </MenuItem>
                </span>
              </Tooltip>
              <MenuItem disabled={resetOwnPasswordBusy} onClick={handleOpenResetOwnPasswordDialog}>
                <VpnKeyIcon
                  sx={{
                    fontSize: 18,
                    color: !resetOwnPasswordBusy ? "#8ecbff" : "rgba(148,163,184,0.62)",
                    mr: 1,
                  }}
                />
                Reset Password
              </MenuItem>
              <MenuItem disabled={!mfaEnabled || resetOwnMfaBusy} onClick={handleOpenResetOwnMfaDialog}>
                <LockResetIcon
                  sx={{
                    fontSize: 18,
                    color: mfaEnabled && !resetOwnMfaBusy ? "#8ecbff" : "rgba(148,163,184,0.62)",
                    mr: 1,
                  }}
                />
                Reset MFA
              </MenuItem>
              <MenuItem onClick={() => void handleLogout()}>
                <LogoutIcon sx={{ fontSize: 18, color: "#ff6b6b", mr: 1 }} />
                Logout
              </MenuItem>
            </Menu>
          </Toolbar>
        </AppBar>

        <Box
          sx={{
            display: "flex",
            flexGrow: 1,
            overflow: "auto",
            minHeight: 0,
            background: APP_AURORA_BACKGROUND,
            backgroundRepeat: "no-repeat",
            backgroundSize: "cover",
            backgroundColor: "#040711",
          }}
        >
          {resolvedChrome.navigationSidebar ? (
            resolvedChrome.navigationSidebar
          ) : hideNavigationSidebar ? null : (
            <NavigationSidebar
              activeNavKey={activeNavKey}
              isAdmin={isAdmin}
            />
          )}
          <Box
            sx={{
              flexGrow: 1,
              display: "flex",
              flexDirection: "column",
              overflow: "auto",
              minHeight: 0,
              minWidth: 0,
            }}
          >
            {clusterBanner?.hmr_state && clusterBanner.hmr_state !== "inactive" ? (
              <Alert severity="warning" variant="filled" sx={{ borderRadius: 0, flexShrink: 0 }}>
                Cluster-wide non-HA HMR active. All Borealis application traffic runs on designated HMR node; standby nodes remain drained until pinned production release is restored.
              </Alert>
            ) : clusterBanner?.status === "Degraded Quorum" ? (
              <Alert severity="error" variant="filled" sx={{ borderRadius: 0, flexShrink: 0 }}>
                Cluster degraded. Failed node remains drained; Admin retry or recovery required.
              </Alert>
            ) : clusterBanner?.active_operation ? (
              <Alert severity="info" sx={{ borderRadius: 0, flexShrink: 0 }}>
                Cluster operation {clusterBanner.active_operation.kind} · {clusterBanner.active_operation.current_step} · {clusterBanner.active_operation.state}
              </Alert>
            ) : null}
            {hasPageHeader ? (
              <Box sx={{ px: 3, pt: 3, pb: 1.5, flexShrink: 0 }}>
                <Box
                  sx={{
                    display: "flex",
                    flexDirection: { xs: "column", xl: "row" },
                    alignItems: { xs: "stretch", xl: "flex-start" },
                    justifyContent: "space-between",
                    gap: 2,
                  }}
                >
                  <Box sx={{ minWidth: 0, flex: "1 1 auto" }}>
                    <Box sx={{ display: "flex", alignItems: "center", gap: 1.25 }}>
                      {HeaderIcon ? <HeaderIcon sx={{ fontSize: 22, color: "#7dd3fc" }} /> : null}
                      <Typography
                        variant="h6"
                        sx={{ color: "#e2e8f0", fontWeight: 700, letterSpacing: 0.5 }}
                      >
                        {resolvedChrome.title}
                      </Typography>
                    </Box>
                    {resolvedChrome.subtitle ? (
                      <Typography variant="body2" sx={{ color: "#aaa", mt: 0.5 }}>
                        <PageSubtitleMarkdown text={resolvedChrome.subtitle} />
                      </Typography>
                    ) : null}
                  </Box>
                  <PageHeaderActionRail
                    actions={resolvedChrome.actions}
                    controls={resolvedChrome.controls}
                  />
                </Box>
              </Box>
            ) : null}

            <Box
              sx={{
                flexGrow: 1,
                display: "flex",
                flexDirection: "column",
                overflow: "auto",
                minHeight: 0,
                position: "relative",
                "& > *": {
                  alignSelf: "stretch",
                  minHeight: 0,
                },
              }}
            >
              <Outlet />
              {isBufferedRoutePending ? (
                <Box
                  sx={{
                    position: "absolute",
                    inset: 0,
                    zIndex: 20,
                    display: "grid",
                    placeItems: "center",
                    background:
                      "linear-gradient(180deg, rgba(4,7,17,0.42), rgba(4,7,17,0.62)), rgba(4,7,17,0.36)",
                    backdropFilter: "blur(10px)",
                    pointerEvents: "auto",
                  }}
                >
                  <Box
                    sx={{
                      minWidth: 240,
                      px: 3,
                      py: 2.5,
                      borderRadius: 3,
                      border: "1px solid rgba(125,211,252,0.28)",
                      background:
                        "linear-gradient(165deg, rgba(8,12,24,0.94), rgba(10,16,31,0.9))",
                      boxShadow: "0 20px 60px rgba(2,8,23,0.5)",
                      display: "flex",
                      alignItems: "center",
                      gap: 1.5,
                    }}
                  >
                    <CircularProgress size={24} sx={{ color: "#7dd3fc" }} />
                    <Box>
                      <Typography sx={{ color: "#e2e8f0", fontWeight: 700, fontSize: "0.95rem" }}>
                        Loading Data...
                      </Typography>
                      <Typography sx={{ color: "#94a3b8", fontSize: "0.82rem", mt: 0.25 }}>
                        {routeRequestProgressLabel}
                      </Typography>
                      <LinearProgress
                        variant={routeRequestProgress?.total > 0 ? "determinate" : "indeterminate"}
                        value={routeRequestProgressValue}
                        sx={{
                          mt: 1.2,
                          height: 6,
                          borderRadius: 999,
                          backgroundColor: "rgba(148,163,184,0.16)",
                          "& .MuiLinearProgress-bar": {
                            borderRadius: 999,
                            background: "linear-gradient(90deg, #7dd3fc, #c084fc)",
                          },
                        }}
                      />
                    </Box>
                  </Box>
                </Box>
              ) : null}
            </Box>
          </Box>
        </Box>
      </Box>

      <Dialog
        open={managePasskeysOpen}
        onClose={handleCloseManagePasskeysDialog}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Manage Passkeys"
            subtitle="Review the passkeys this Borealis account can use to sign in, rename them, or remove the ones you no longer want."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Passkeys are stored per operator account. You can add another passkey for a second
            device, give each one a friendly name, and remove old passkeys whenever you retire a
            browser, phone, or laptop.
          </Typography>
          <Box
            sx={{
              display: "flex",
              gap: 1.5,
              mt: 2,
              flexDirection: { xs: "column", sm: "row" },
              alignItems: { xs: "stretch", sm: "flex-start" },
            }}
          >
            <TextField
              fullWidth
              label="New Passkey Label"
              variant="outlined"
              value={newPasskeyLabel}
              onChange={(event) => setNewPasskeyLabel(event.target.value)}
              disabled={addPasskeyBusy || managePasskeysLoading || Boolean(managePasskeyAction.id)}
              placeholder="Work laptop, YubiKey, iPhone, ..."
              sx={{ ...DIALOG_INPUT_SX, mt: 0 }}
            />
            <Button
              onClick={() => void handleAddPasskey()}
              sx={{ ...DIALOG_PRIMARY_BUTTON_SX, minWidth: { xs: "100%", sm: 168 } }}
              disabled={addPasskeyBusy || managePasskeysLoading || Boolean(managePasskeyAction.id)}
            >
              {addPasskeyBusy ? "Adding..." : "Add Passkey"}
            </Button>
          </Box>

          {managePasskeysError ? (
            <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 1.5, color: "#ffb4b4" }}>
              {managePasskeysError}
            </Typography>
          ) : null}

          {managePasskeysLoading ? (
            <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 2.2 }}>
              Loading your Borealis passkeys...
            </Typography>
          ) : managePasskeyRows.length ? (
            <Box sx={{ display: "flex", flexDirection: "column", gap: 1.5, mt: 2.2 }}>
              {managePasskeyRows.map((passkey) => {
                const rowBusy = managePasskeyAction.id === passkey.id;
                const draftLabel = managePasskeyLabels[passkey.id] ?? passkey.label ?? "Passkey";
                const normalizedDraft = String(draftLabel || "").trim() || "Passkey";
                const normalizedCurrent = String(passkey.label || "").trim() || "Passkey";
                return (
                  <Box
                    key={passkey.id}
                    sx={{
                      borderRadius: 2.5,
                      border: "1px solid rgba(125,183,255,0.18)",
                      background:
                        "linear-gradient(180deg, rgba(15,23,42,0.96), rgba(8,13,24,0.94))",
                      boxShadow: "0 14px 28px rgba(3,10,24,0.26)",
                      p: 2,
                    }}
                  >
                    <TextField
                      fullWidth
                      label="Passkey Name"
                      variant="outlined"
                      value={draftLabel}
                      onChange={(event) =>
                        handleManagedPasskeyLabelChange(passkey.id, event.target.value)
                      }
                      disabled={rowBusy || addPasskeyBusy}
                      sx={{ ...DIALOG_INPUT_SX, mt: 0 }}
                    />
                    <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 1.2 }}>
                      Created: {formatPasskeyTimestamp(passkey.created_at)}
                    </Typography>
                    <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 0.8 }}>
                      Last used: {formatPasskeyTimestamp(passkey.last_used_at)}
                    </Typography>
                    <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 0.8 }}>
                      Device type: {formatPasskeyTransportLabel(passkey.transports)}
                    </Typography>
                    <Box
                      sx={{
                        display: "flex",
                        gap: 1.25,
                        mt: 1.9,
                        flexDirection: { xs: "column", sm: "row" },
                        justifyContent: "space-between",
                      }}
                    >
                      <Button
                        onClick={() => void handleSaveManagedPasskey(passkey.id)}
                        sx={DIALOG_PRIMARY_BUTTON_SX}
                        disabled={
                          rowBusy ||
                          addPasskeyBusy ||
                          normalizedDraft === normalizedCurrent ||
                          normalizedDraft.length > 80
                        }
                      >
                        {rowBusy && managePasskeyAction.type === "save" ? "Saving..." : "Save Name"}
                      </Button>
                      <Button
                        onClick={() => handleRequestRemoveManagedPasskey(passkey)}
                        sx={DIALOG_DANGER_BUTTON_SX}
                        disabled={rowBusy || addPasskeyBusy}
                      >
                        Remove Passkey
                      </Button>
                    </Box>
                  </Box>
                );
              })}
            </Box>
          ) : (
            <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 2.2 }}>
              No passkeys are enrolled for this account yet. Add one above to start using passkeys
              for Borealis MFA.
            </Typography>
          )}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={handleCloseManagePasskeysDialog}
            sx={DIALOG_BUTTON_SX}
            disabled={addPasskeyBusy || Boolean(managePasskeyAction.id)}
          >
            Done
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={Boolean(removePasskeyTarget)}
        onClose={handleCancelRemoveManagedPasskey}
        fullWidth
        maxWidth="xs"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Remove Passkey"
            subtitle="Revoke a passkey from this Borealis account."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Remove{" "}
            <Box component="span" sx={{ color: "#f8fafc", fontWeight: 700 }}>
              {removePasskeyTarget?.label || "this passkey"}
            </Box>
            {" "}from your account? You can still sign in with your password and any remaining MFA
            methods, and Borealis will prompt you to set MFA up again later if no methods remain.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={handleCancelRemoveManagedPasskey}
            sx={DIALOG_BUTTON_SX}
            disabled={Boolean(managePasskeyAction.id)}
          >
            Cancel
          </Button>
          <Button
            onClick={() => void handleConfirmRemoveManagedPasskey()}
            sx={DIALOG_DANGER_BUTTON_SX}
            disabled={Boolean(managePasskeyAction.id)}
          >
            {managePasskeyAction.type === "delete" ? "Removing..." : "Remove"}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={resetOwnPasswordOpen}
        onClose={handleCloseResetOwnPasswordDialog}
        fullWidth
        maxWidth="xs"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Reset Password"
            subtitle="Verify your current password, then set a new one for this Borealis account."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Current Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordCurrent}
            onChange={(event) => setResetOwnPasswordCurrent(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
          />
          <TextField
            fullWidth
            label="New Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordNext}
            onChange={(event) => setResetOwnPasswordNext(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
          />
          <TextField
            fullWidth
            label="Confirm New Password"
            type="password"
            variant="outlined"
            value={resetOwnPasswordConfirm}
            onChange={(event) => setResetOwnPasswordConfirm(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
          />
          {resetOwnPasswordError ? (
            <Typography sx={{ ...DIALOG_BODY_TEXT_SX, mt: 1.5, color: "#ffb4b4" }}>
              {resetOwnPasswordError}
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={handleCloseResetOwnPasswordDialog}
            sx={DIALOG_BUTTON_SX}
            disabled={resetOwnPasswordBusy}
          >
            Cancel
          </Button>
          <Button
            onClick={() => void handleResetOwnPassword()}
            sx={DIALOG_PRIMARY_BUTTON_SX}
            disabled={resetOwnPasswordBusy}
          >
            {resetOwnPasswordBusy ? "Saving..." : "Save Password"}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={resetOwnMfaOpen}
        onClose={handleCloseResetOwnMfaDialog}
        fullWidth
        maxWidth="xs"
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Reset MFA"
            subtitle="Clear your current Borealis MFA setup for this account."
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={DIALOG_BODY_TEXT_SX}>
            Borealis will keep MFA enabled for your account, but your current authenticator app
            secret will be cleared. Your passkeys remain available for direct sign-in, and the next
            time you use password sign-in Borealis will prompt you to complete MFA setup again.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={handleCloseResetOwnMfaDialog}
            sx={DIALOG_BUTTON_SX}
            disabled={resetOwnMfaBusy}
          >
            Cancel
          </Button>
          <Button
            onClick={() => void handleResetOwnMfa()}
            sx={DIALOG_DANGER_BUTTON_SX}
            disabled={resetOwnMfaBusy}
          >
            {resetOwnMfaBusy ? "Resetting..." : "Reset MFA"}
          </Button>
        </DialogActions>
      </Dialog>

      <NotAuthorizedDialog open={notAuthorizedOpen} onClose={() => setNotAuthorizedOpen(false)} />
    </>
  );
}
