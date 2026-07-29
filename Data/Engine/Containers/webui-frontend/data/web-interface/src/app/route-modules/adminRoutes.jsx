import React from "react";
import ServerInfo, { loadServerOverviewPageData } from "../../Admin/Server_Info.jsx";
import DevTools from "../../DevTools/Dev_Tools.jsx";
import MetadataFieldList, { loadMetadataFieldsPageData } from "../../Admin/Metadata_Field_List.jsx";
import BackupRestore from "../../Admin/Backup_Restore.jsx";

export async function ServerRouteLoader({ request }) {
  return loadServerOverviewPageData(request);
}

export function ServerRoute() {
  return <ServerInfo />;
}

export function BackupRestoreRoute() {
  return <BackupRestore mode="admin" />;
}

export function BootstrapBackupRestoreRoute() {
  return <BackupRestore mode="bootstrap" />;
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
