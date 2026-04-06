import React from "react";
import { Navigate, Outlet, useLocation } from "react-router-dom";
import { Box, CircularProgress } from "@mui/material";
import { useAuth } from "../providers/AuthContext.jsx";
import { APP_PATHS } from "./paths.js";

function FullScreenPending() {
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

export function RequireAuth() {
  const location = useLocation();
  const { ready, isAuthenticated } = useAuth();

  if (!ready) {
    return <FullScreenPending />;
  }
  if (!isAuthenticated) {
    return <Navigate to={APP_PATHS.login} replace state={{ from: location }} />;
  }
  return <Outlet />;
}

export function RequireAdmin() {
  const location = useLocation();
  const { ready, isAuthenticated, isAdmin } = useAuth();

  if (!ready) {
    return <FullScreenPending />;
  }
  if (!isAuthenticated) {
    return <Navigate to={APP_PATHS.login} replace state={{ from: location }} />;
  }
  if (!isAdmin) {
    return <Navigate to={APP_PATHS.sites} replace state={{ showNotAuthorizedDialog: true }} />;
  }
  return <Outlet />;
}
