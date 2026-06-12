import React, { useCallback, useEffect } from "react";
import { Box, CircularProgress, Typography } from "@mui/material";
import { useLocation, useNavigate } from "react-router-dom";
import Login from "../../Login.jsx";
import BootstrapEntry from "./BootstrapEntry.jsx";
import { useAuth } from "../providers/AuthContext.jsx";
import { APP_PATHS, normalizeAppRedirectTarget } from "./paths.js";

function resolvePostLoginTarget(location) {
  const searchParams = new URLSearchParams(location?.search || "");
  const nextTarget = normalizeAppRedirectTarget(searchParams.get("next"));
  if (nextTarget) {
    return nextTarget;
  }

  const from = location?.state?.from;
  if (typeof from?.pathname === "string" && from.pathname && from.pathname !== APP_PATHS.login) {
    const search = typeof from?.search === "string" ? from.search : "";
    return `${from.pathname}${search}`;
  }

  return APP_PATHS.sites;
}

function EngineLoadingScreen() {
  return (
    <Box
      role="status"
      sx={{
        width: "100vw",
        height: "100vh",
        display: "grid",
        placeItems: "center",
        background: "#040711",
      }}
    >
      <Box
        sx={{
          display: "grid",
          justifyItems: "center",
          gap: 1.5,
        }}
      >
        <CircularProgress />
        <Typography
          variant="body2"
          sx={{
            color: "var(--text-dim)",
            fontWeight: 700,
            letterSpacing: 0,
          }}
        >
          Engine Loading...
        </Typography>
      </Box>
    </Box>
  );
}

export default function LoginRoute() {
  const navigate = useNavigate();
  const location = useLocation();
  const { ready, isAuthenticated, login, bootstrapState, refreshBootstrapState } = useAuth();

  useEffect(() => {
    if (ready && bootstrapState?.phase === "login_required" && isAuthenticated) {
      navigate(resolvePostLoginTarget(location), { replace: true });
    }
  }, [bootstrapState, isAuthenticated, location, navigate, ready]);

  useEffect(() => {
    if (!ready || String(bootstrapState?.phase || "") !== "loading") return undefined;

    let cancelled = false;
    const intervalId = window.setInterval(async () => {
      if (!cancelled) {
        await refreshBootstrapState();
      }
    }, 2500);

    return () => {
      cancelled = true;
      window.clearInterval(intervalId);
    };
  }, [bootstrapState, ready, refreshBootstrapState]);

  const handleLogin = useCallback(
    async (payload) => {
      await login(payload);
      navigate(resolvePostLoginTarget(location), { replace: true });
    },
    [location, login, navigate]
  );

  if (!ready || String(bootstrapState?.phase || "") === "loading") {
    return <EngineLoadingScreen />;
  }

  if (bootstrapState?.phase && bootstrapState.phase !== "login_required") {
    return (
      <BootstrapEntry
        bootstrapState={bootstrapState}
        refreshBootstrapState={refreshBootstrapState}
        onAuthenticated={handleLogin}
      />
    );
  }

  return <Login onLogin={handleLogin} />;
}
