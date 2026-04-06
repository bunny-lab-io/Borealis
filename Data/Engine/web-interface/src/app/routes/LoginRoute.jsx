import React, { useCallback, useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import Login from "../../Login.jsx";
import { useAuth } from "../providers/AuthContext.jsx";
import { APP_PATHS } from "./paths.js";

function resolvePostLoginTarget(locationState) {
  const from = locationState?.from;
  if (typeof from?.pathname === "string" && from.pathname && from.pathname !== APP_PATHS.login) {
    const search = typeof from?.search === "string" ? from.search : "";
    return `${from.pathname}${search}`;
  }
  return APP_PATHS.sites;
}

export default function LoginRoute() {
  const navigate = useNavigate();
  const location = useLocation();
  const { ready, isAuthenticated, login } = useAuth();

  useEffect(() => {
    if (ready && isAuthenticated) {
      navigate(resolvePostLoginTarget(location.state), { replace: true });
    }
  }, [isAuthenticated, location.state, navigate, ready]);

  const handleLogin = useCallback(
    async (payload) => {
      await login(payload);
      navigate(resolvePostLoginTarget(location.state), { replace: true });
    },
    [location.state, login, navigate]
  );

  return <Login onLogin={handleLogin} />;
}
