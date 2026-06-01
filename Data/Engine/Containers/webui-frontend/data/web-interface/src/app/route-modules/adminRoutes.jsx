import React from "react";
import ServerInfo, { loadServerOverviewPageData } from "../../Admin/Server_Info.jsx";
import LogManagement from "../../Admin/Log_Management.jsx";
import DevTools from "../../DevTools/Dev_Tools.jsx";
import EngineStatus from "../../Admin/Engine_Status.jsx";
import SiteWorkers from "../../Admin/Site_Workers.jsx";
import MetadataFieldList, { loadMetadataFieldsPageData } from "../../Admin/Metadata_Field_List.jsx";

export async function ServerRouteLoader({ request }) {
  return loadServerOverviewPageData(request);
}

export function ServerRoute() {
  return <ServerInfo />;
}

export function EngineStatusRoute() {
  return <EngineStatus />;
}

export function SiteWorkersRoute() {
  return <SiteWorkers />;
}

export function LogsRoute() {
  return <LogManagement />;
}

export async function MetadataFieldsRouteLoader({ request }) {
  return loadMetadataFieldsPageData(request);
}

export function MetadataFieldsRoute() {
  return <MetadataFieldList />;
}

export function DevToolsRoute() {
  return <DevTools />;
}
