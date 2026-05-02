import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { EMPTY_AEGIS_STATUS, normalizeAegisStatus } from "../utils/aegis.js";
import { EMPTY_BOOTSTRAP_STATE, normalizeBootstrapState } from "../utils/bootstrap.js";
import { sha512 } from "../utils/crypto.js";
import { postAppNotification } from "../utils/notifications.js";
import {
  deletePasskey as deletePasskeyCeremony,
  listPasskeys as listPasskeysCeremony,
  registerPasskey as registerPasskeyCeremony,
  updatePasskeyLabel as updatePasskeyLabelCeremony,
} from "../utils/passkeys.js";
import { getBorealisSocket } from "../runtime/bootstrapClientRuntime.js";

const SESSION_CACHE_KEY = "borealis_session";
const SESSION_CACHE_TTL_MS = 3600 * 1000;

function clearPersistedSession() {
  try {
    localStorage.removeItem(SESSION_CACHE_KEY);
  } catch {
    /* noop */
  }
}

function persistSession(payload) {
  if (!payload?.username) return;
  try {
    localStorage.setItem(
      SESSION_CACHE_KEY,
      JSON.stringify({
        username: payload.username,
        display_name: payload.display_name || payload.username,
        role: payload.role || null,
        timestamp: Date.now(),
      })
    );
  } catch {
    /* noop */
  }
}

function readPersistedSession() {
  try {
    const raw = localStorage.getItem(SESSION_CACHE_KEY);
    if (!raw) return null;
    const data = JSON.parse(raw);
    if (Date.now() - Number(data?.timestamp || 0) >= SESSION_CACHE_TTL_MS) {
      clearPersistedSession();
      return null;
    }
    return data;
  } catch {
    clearPersistedSession();
    return null;
  }
}

export const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [user, setUser] = useState(null);
  const [role, setRole] = useState(null);
  const [displayName, setDisplayName] = useState(null);
  const [mfaEnabled, setMfaEnabled] = useState(false);
  const [passkeyCount, setPasskeyCount] = useState(0);
  const [ready, setReady] = useState(false);
  const [bootstrapState, setBootstrapState] = useState(EMPTY_BOOTSTRAP_STATE);
  const [aegisStatus, setAegisStatus] = useState(EMPTY_AEGIS_STATUS);
  const [aegisDialog, setAegisDialog] = useState(null);

  const clearClientSession = useCallback(() => {
    clearPersistedSession();
    setUser(null);
    setRole(null);
    setDisplayName(null);
    setMfaEnabled(false);
    setPasskeyCount(0);
    setAegisStatus(EMPTY_AEGIS_STATUS);
    setAegisDialog(null);
  }, []);

  const clearOperatorPresence = useCallback(() => {
    try {
      getBorealisSocket()?.emit?.("operator_presence_clear");
    } catch {
      /* operator presence is best-effort */
    }
  }, []);

  const refreshBootstrapState = useCallback(async () => {
    try {
      const response = await fetch("/api/bootstrap/state", { credentials: "include" });
      if (!response.ok) {
        const fallback = { ...EMPTY_BOOTSTRAP_STATE, phase: "aegis_setup_required" };
        setBootstrapState(fallback);
        return fallback;
      }
      const payload = await response.json();
      const normalized = normalizeBootstrapState(payload);
      setBootstrapState(normalized);
      return normalized;
    } catch {
      const fallback = { ...EMPTY_BOOTSTRAP_STATE, phase: "aegis_setup_required" };
      setBootstrapState(fallback);
      return fallback;
    }
  }, []);

  const fetchAegisStatus = useCallback(async () => {
    if (String(bootstrapState?.phase || "") !== "login_required") {
      setAegisStatus(EMPTY_AEGIS_STATUS);
      return EMPTY_AEGIS_STATUS;
    }
    try {
      const response = await fetch("/api/aegis/status", { credentials: "include" });
      if (!response.ok) {
        if (response.status === 401 || response.status === 403) {
          setAegisStatus(EMPTY_AEGIS_STATUS);
        }
        return EMPTY_AEGIS_STATUS;
      }
      const payload = await response.json();
      const normalized = normalizeAegisStatus(payload);
      setAegisStatus(normalized);
      return normalized;
    } catch {
      return EMPTY_AEGIS_STATUS;
    }
  }, [bootstrapState]);

  const refreshSession = useCallback(async () => {
    if (String(bootstrapState?.phase || "") !== "login_required") {
      clearClientSession();
      return null;
    }
    try {
      const response = await fetch("/api/auth/me", { credentials: "include" });
      if (response.ok) {
        const me = await response.json();
        setUser(me.username);
        setRole(me.role || null);
        setDisplayName(me.display_name || me.username);
        setMfaEnabled(Boolean(me.mfa_enabled));
        setPasskeyCount(Number(me.passkey_count || 0));
        persistSession(me);
        return me;
      }
      if (response.status === 401 || response.status === 403) {
        clearClientSession();
      }
    } catch {
      /* noop */
    }
    return null;
  }, [bootstrapState, clearClientSession]);

  useEffect(() => {
    let cancelled = false;

    const hydrateSession = async () => {
      const bootstrap = await refreshBootstrapState();
      if (cancelled) return;

      if (String(bootstrap?.phase || "") !== "login_required") {
        clearClientSession();
        setReady(true);
        return;
      }

      const cached = readPersistedSession();
      if (cached && !cancelled) {
        setUser(cached.username);
        setRole(cached.role || null);
        setDisplayName(cached.display_name || cached.username);
      }

      try {
        const response = await fetch("/api/auth/me", { credentials: "include" });
        if (response.ok) {
          const me = await response.json();
          if (!cancelled) {
            setUser(me.username);
            setRole(me.role || null);
            setDisplayName(me.display_name || me.username);
            setMfaEnabled(Boolean(me.mfa_enabled));
            setPasskeyCount(Number(me.passkey_count || 0));
          }
          persistSession(me);
        } else if (!cancelled) {
          clearClientSession();
        }
      } catch {
        /* noop */
      }

      if (!cancelled) {
        setReady(true);
      }
    };

    hydrateSession();
    return () => {
      cancelled = true;
    };
  }, [clearClientSession, refreshBootstrapState]);

  useEffect(() => {
    if (!ready || !user || String(bootstrapState?.phase || "") !== "login_required") {
      setAegisStatus(EMPTY_AEGIS_STATUS);
      return;
    }
    fetchAegisStatus();
  }, [bootstrapState, fetchAegisStatus, ready, user]);

  useEffect(() => {
    if (!ready || !user || String(bootstrapState?.phase || "") !== "login_required") return undefined;

    let cancelled = false;
    const validateSession = async () => {
      try {
        const nextBootstrap = await refreshBootstrapState();
        if (String(nextBootstrap?.phase || "") !== "login_required") {
          if (!cancelled) {
            clearClientSession();
            setAegisStatus(EMPTY_AEGIS_STATUS);
          }
          return;
        }
        const response = await fetch("/api/auth/me", { credentials: "include" });
        if (!response.ok && !cancelled) {
          clearClientSession();
          setAegisStatus(EMPTY_AEGIS_STATUS);
          return;
        }
        if (response.ok && !cancelled) {
          const me = await response.json();
          setUser(me.username);
          setRole(me.role || null);
          setDisplayName(me.display_name || me.username);
          setMfaEnabled(Boolean(me.mfa_enabled));
          setPasskeyCount(Number(me.passkey_count || 0));
          persistSession(me);
          fetchAegisStatus();
        }
      } catch {
        /* noop */
      }
    };

    const intervalId = window.setInterval(validateSession, 30 * 1000);
    const handleVisibility = () => {
      if (document.visibilityState === "visible") {
        validateSession();
      }
    };

    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      cancelled = true;
      window.clearInterval(intervalId);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, [bootstrapState, clearClientSession, fetchAegisStatus, ready, refreshBootstrapState, user]);

  const login = useCallback(
    async ({ username, role: nextRole }) => {
      await refreshBootstrapState();
      setUser(username);
      setRole(nextRole || null);
      setDisplayName(username);
      setMfaEnabled(false);
      setPasskeyCount(0);
      persistSession({
        username,
        display_name: username,
        role: nextRole || null,
      });

      (async () => {
        try {
          const response = await fetch("/api/auth/me", { credentials: "include" });
          if (response.ok) {
            const me = await response.json();
            setUser(me.username);
            setRole(me.role || null);
            setDisplayName(me.display_name || me.username);
            setMfaEnabled(Boolean(me.mfa_enabled));
            setPasskeyCount(Number(me.passkey_count || 0));
            persistSession(me);
          }
        } catch {
          /* noop */
        }
        try {
          await fetchAegisStatus();
        } catch {
          /* noop */
        }
      })();
    },
    [fetchAegisStatus, refreshBootstrapState]
  );

  const logout = useCallback(async () => {
    clearOperatorPresence();
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "include",
      });
    } catch {
      /* noop */
    }
    clearClientSession();
    await refreshBootstrapState();
  }, [clearClientSession, clearOperatorPresence, refreshBootstrapState]);

  const openAegisDialog = useCallback((mode, source = "credentials") => {
    setAegisDialog({ mode, source });
  }, []);

  const closeAegisDialog = useCallback(() => {
    setAegisDialog(null);
  }, []);

  const completeAegisDialog = useCallback(
    async (payload) => {
      setAegisDialog(null);
      setAegisStatus(normalizeAegisStatus(payload));
      if (payload?.force_reset) {
        clearOperatorPresence();
        clearClientSession();
        await refreshBootstrapState();
      }
    },
    [clearClientSession, clearOperatorPresence, refreshBootstrapState]
  );

  const resetPassword = useCallback(
    async ({ currentPassword, nextPassword, confirmPassword }) => {
      const current = String(currentPassword || "");
      const next = String(nextPassword || "");
      const confirm = String(confirmPassword || "");

      if (!current || !next) {
        throw new Error("Enter your current password and a new password.");
      }
      if (next !== confirm) {
        throw new Error("The new password and confirmation do not match.");
      }
      if (current === next) {
        throw new Error("Choose a new password that differs from your current password.");
      }

      const currentPasswordHash = await sha512(current);
      const nextPasswordHash = await sha512(next);
      const payload =
        currentPasswordHash && nextPasswordHash
          ? {
              current_password_sha512: currentPasswordHash,
              new_password_sha512: nextPasswordHash,
            }
          : {
              current_password: current,
              new_password: next,
            };

      const response = await fetch("/api/auth/password/reset", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(payload),
      });

      if (response.status === 401 || response.status === 403) {
        const unauthorizedPayload = await response.json().catch(() => ({}));
        if ((unauthorizedPayload?.error || "") === "invalid current password") {
          throw new Error("Your current password is incorrect.");
        }
        clearClientSession();
        throw new Error("Your session expired. Please sign in again.");
      }

      const responsePayload = await response.json().catch(() => ({}));
      if (!response.ok) {
        const errorMessageMap = {
          "invalid current password": "Your current password is incorrect.",
          "new password must differ from the current password":
            "Choose a new password that differs from your current password.",
          "invalid current password hash": "Enter your current password.",
          "invalid new password hash": "Enter a valid new password.",
          auth_reset_required:
            "This account needs administrator recovery before it can change its password again.",
          bootstrap_required:
            "Borealis must finish Aegis bootstrap before account changes are available.",
        };
        throw new Error(
          errorMessageMap[responsePayload?.error] ||
            responsePayload?.error ||
            "Failed to reset password."
        );
      }

      await postAppNotification({
        title: "Reset Password",
        message: "Your Borealis password was updated.",
        icon: "user",
        variant: "info",
      });
      return responsePayload;
    },
    [clearClientSession]
  );

  const resetMfa = useCallback(async () => {
    const response = await fetch("/api/auth/mfa/reset", {
      method: "POST",
      credentials: "include",
    });

    if (response.status === 401 || response.status === 403) {
      clearClientSession();
      throw new Error("Your session expired. Please sign in again.");
    }

    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const message = payload?.error || "Failed to reset MFA.";
      await postAppNotification({
        title: "Reset MFA",
        message,
        icon: "warning",
        variant: "error",
      });
      throw new Error(message);
    }

    setMfaEnabled(Boolean(payload.mfa_enabled));
    await postAppNotification({
      title: "Reset MFA",
      message: payload.setup_required_on_next_login
        ? "Your MFA setup was reset. Borealis cleared your authenticator app secret, and the next time you use password sign-in it will prompt you to set up MFA again."
        : "No active MFA setup was found for your account.",
      icon: "user",
      variant: "info",
    });
    return payload;
  }, [clearClientSession]);

  const registerPasskey = useCallback(
    async ({ label = "" } = {}) => {
      try {
        const payload = await registerPasskeyCeremony({ label });
        const refreshed = await refreshSession();
        await postAppNotification({
          title: "Passkey Added",
          message:
            Number(refreshed?.passkey_count || payload?.passkey_count || 0) > 1
              ? "Your new passkey is ready to use for Borealis sign-in."
              : "Your first Borealis passkey is ready to use for sign-in.",
          icon: "key",
          variant: "info",
        });
        return payload;
      } catch (error) {
        const message =
          error instanceof Error && error.message
            ? error.message
            : "Borealis could not add a passkey right now.";
        await postAppNotification({
          title: "Add Passkey",
          message,
          icon: "warning",
          variant: "error",
        });
        throw error;
      }
    },
    [refreshSession]
  );

  const listPasskeys = useCallback(async () => {
    const payload = await listPasskeysCeremony();
    return Array.isArray(payload?.passkeys) ? payload.passkeys : [];
  }, []);

  const updatePasskeyLabel = useCallback(async ({ passkeyId, label = "" }) => {
    const payload = await updatePasskeyLabelCeremony({ passkeyId, label });
    await postAppNotification({
      title: "Passkey Updated",
      message: "Your Borealis passkey label was updated.",
      icon: "key",
      variant: "info",
    });
    return payload;
  }, []);

  const deletePasskey = useCallback(
    async ({ passkeyId }) => {
      const payload = await deletePasskeyCeremony({ passkeyId });
      await refreshSession();
      await postAppNotification({
        title: "Passkey Removed",
        message: "The selected passkey was removed from your Borealis account.",
        icon: "key",
        variant: "info",
      });
      return payload;
    },
    [refreshSession]
  );

  const value = useMemo(
    () => ({
      ready,
      bootstrapState,
      user,
      role,
      displayName,
      mfaEnabled,
      passkeyCount,
      aegisStatus,
      aegisDialog,
      isAuthenticated: Boolean(user),
      isAdmin: String(role || "").toLowerCase() === "admin",
      login,
      logout,
      refreshSession,
      resetPassword,
      resetMfa,
      registerPasskey,
      listPasskeys,
      updatePasskeyLabel,
      deletePasskey,
      refreshBootstrapState,
      fetchAegisStatus,
      openAegisDialog,
      closeAegisDialog,
      completeAegisDialog,
      clearOperatorPresence,
    }),
    [
      aegisDialog,
      aegisStatus,
      bootstrapState,
      clearOperatorPresence,
      closeAegisDialog,
      completeAegisDialog,
      displayName,
      fetchAegisStatus,
      deletePasskey,
      login,
      listPasskeys,
      logout,
      mfaEnabled,
      passkeyCount,
      openAegisDialog,
      ready,
      refreshBootstrapState,
      registerPasskey,
      refreshSession,
      resetMfa,
      resetPassword,
      role,
      updatePasskeyLabel,
      user,
    ]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used inside AuthProvider.");
  }
  return context;
}
