import React from "react";
import { Navigate, useLocation } from "react-router-dom";
import PatchManagement from "../../Patches/Patch_Management.jsx";
import PatchPolicyEditor from "../../Patches/Patch_Policy_Editor.jsx";
import { APP_PATHS } from "../routes/paths.js";

function PatchPlatformRedirect({ platform }) {
  const location = useLocation();
  const params = new URLSearchParams(location.search || "");
  params.set("platform", platform);
  return <Navigate to={`${APP_PATHS.patchManagement}?${params.toString()}${location.hash || ""}`} replace />;
}

export function PatchManagementRoute() {
  return <PatchManagement />;
}

export function PatchWindowsRoute() {
  return <PatchPlatformRedirect platform="windows" />;
}

export function PatchLinuxRoute() {
  return <PatchPlatformRedirect platform="linux" />;
}

export function PatchMacOSRoute() {
  return <PatchPlatformRedirect platform="macos" />;
}

export function PatchPolicyEditorRoute() {
  return <PatchPolicyEditor />;
}
