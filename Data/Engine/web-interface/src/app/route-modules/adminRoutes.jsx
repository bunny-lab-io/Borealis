import React from "react";
import ServerInfo, { loadServerOverviewPageData } from "../../Admin/Server_Info.jsx";
import LogManagement from "../../Admin/Log_Management.jsx";
import PageTemplate from "../../Admin/Page_Template.jsx";

export async function ServerRouteLoader({ request }) {
  return loadServerOverviewPageData(request);
}

export function ServerRoute() {
  return <ServerInfo />;
}

export function LogsRoute() {
  return <LogManagement />;
}

export function PageTemplateRoute() {
  return <PageTemplate />;
}
