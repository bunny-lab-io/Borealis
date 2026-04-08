import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  AppBar,
  Box,
  Breadcrumbs,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Menu,
  MenuItem,
  TextField,
  Toolbar,
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
import { Outlet, useLocation, useMatches, useNavigate } from "react-router-dom";
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
  buildBreadcrumbItems,
  resolveActiveNavKey,
  resolveCurrentPageKey,
  resolvePageChromeDefaults,
} from "../routes/breadcrumbs.js";
import { APP_PATHS } from "../routes/paths.js";
import { getBorealisSocket } from "../runtime/bootstrapClientRuntime.js";
import { formatOperatorPresencePage } from "../utils/operatorPresence.js";
import { APP_AURORA_BACKGROUND } from "../utils/theme.js";

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
  const {
    user,
    displayName,
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

  const defaultChrome = useMemo(() => resolvePageChromeDefaults(matches), [matches]);
  const activeNavKey = useMemo(() => resolveActiveNavKey(matches), [matches]);
  const currentPageKey = useMemo(() => resolveCurrentPageKey(matches), [matches]);

  const resolvedChrome = useMemo(
    () => ({
      title: pageChrome.title || defaultChrome.title || "",
      subtitle: pageChrome.subtitle || defaultChrome.subtitle || "",
      Icon: pageChrome.Icon || defaultChrome.Icon || null,
      actions: pageChrome.actions?.length ? pageChrome.actions : defaultChrome.actions || [],
      controls: pageChrome.controls?.length ? pageChrome.controls : defaultChrome.controls || [],
      breadcrumbLabel:
        pageChrome.breadcrumbLabel || pageChrome.title || defaultChrome.title || "",
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

  const clearResetOwnPasswordState = useCallback(() => {
    setResetOwnPasswordOpen(false);
    setResetOwnPasswordBusy(false);
    setResetOwnPasswordCurrent("");
    setResetOwnPasswordNext("");
    setResetOwnPasswordConfirm("");
    setResetOwnPasswordError("");
  }, []);

  useEffect(() => {
    if (!location.state?.showNotAuthorizedDialog) {
      return;
    }
    setNotAuthorizedOpen(true);
    navigate(`${location.pathname}${location.search}`, {
      replace: true,
      state: stripTransientLocationState(location.state),
    });
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
    if (managePasskeysLoading) return;
    setManagePasskeysOpen(true);
    void loadManagedPasskeys();
  }, [handleUserMenuClose, loadManagedPasskeys, managePasskeysLoading]);

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
              <MenuItem
                disabled={managePasskeysLoading || addPasskeyBusy}
                onClick={handleOpenManagePasskeysDialog}
              >
                <FingerprintIcon
                  sx={{
                    fontSize: 18,
                    color:
                      !managePasskeysLoading && !addPasskeyBusy
                        ? "#8ecbff"
                        : "rgba(148,163,184,0.62)",
                    mr: 1,
                  }}
                />
                {managePasskeysLoading
                  ? "Loading Passkeys..."
                  : `Manage Passkeys${passkeyCount ? ` (${passkeyCount})` : ""}`}
              </MenuItem>
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
          <NavigationSidebar
            activeNavKey={activeNavKey}
            isAdmin={isAdmin}
          />
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
                        {resolvedChrome.subtitle}
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
                "& > *": {
                  alignSelf: "stretch",
                  minHeight: 0,
                },
              }}
            >
              <Outlet />
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
            subtitle="Review the passkeys this Borealis account can use for MFA, rename them, or remove the ones you no longer want."
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
            secret and passkeys will be cleared. The next time you sign in you will be prompted to
            complete MFA setup again.
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
