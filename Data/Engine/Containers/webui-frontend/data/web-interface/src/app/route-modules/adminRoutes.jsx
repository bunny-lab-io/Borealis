import React from "react";
import ServerInfo, { loadServerOverviewPageData } from "../../Admin/Server_Info.jsx";
import LogManagement from "../../Admin/Log_Management.jsx";
import DevTools from "../../DevTools/Dev_Tools.jsx";
import EngineStatus from "../../Admin/Engine_Status.jsx";

export async function ServerRouteLoader({ request }) {
  return loadServerOverviewPageData(request);
}

export function ServerRoute() {
  return <ServerInfo />;
}

export function EngineStatusRoute() {
  return <EngineStatus />;
}

export function LogsRoute() {
  return <LogManagement />;
}

export function DevToolsRoute() {
  return <DevTools />;
}
