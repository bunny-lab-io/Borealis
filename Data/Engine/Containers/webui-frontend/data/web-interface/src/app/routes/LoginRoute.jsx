import React, { useCallback, useEffect } from "react";
import { Box, CircularProgress } from "@mui/material";
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

export default function LoginRoute() {
  const navigate = useNavigate();
  const location = useLocation();
  const { ready, isAuthenticated, login, bootstrapState, refreshBootstrapState } = useAuth();

  useEffect(() => {
    if (ready && bootstrapState?.phase === "login_required" && isAuthenticated) {
      navigate(resolvePostLoginTarget(location), { replace: true });
    }
  }, [bootstrapState, isAuthenticated, location, navigate, ready]);

  const handleLogin = useCallback(
    async (payload) => {
      await login(payload);
      navigate(resolvePostLoginTarget(location), { replace: true });
    },
    [location, login, navigate]
  );

  if (!ready) {
    return (
      <Box
        sx={{
          width: "100vw",
          height: "100vh",
          display: "grid",
          placeItems: "center",
          background: "#040711",
        }}
      >
        <CircularProgress />
      </Box>
    );
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
