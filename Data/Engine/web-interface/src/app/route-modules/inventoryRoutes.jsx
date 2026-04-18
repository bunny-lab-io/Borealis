import React from "react";
import DeviceList, { loadDeviceListPageData } from "../../Devices/Device_List";
import AgentDevices from "../../Devices/Agent_Devices.jsx";
import SSHDevices from "../../Devices/SSH_Devices.jsx";
import WinRMDevices from "../../Devices/WinRM_Devices.jsx";
import DeviceSummary, { loadDeviceSummaryPageData } from "../../Devices/Tabs/Device_Summary.jsx";
import RemoteDesktop from "../../Devices/Tabs/Remote_Desktop.jsx";
import DeviceApprovals from "../../Devices/Device_Approvals.jsx";

export async function DeviceListRouteLoader({ request }) {
  return loadDeviceListPageData(request);
}

export async function AgentDevicesRouteLoader({ request }) {
  return loadDeviceListPageData(request);
}

export function DeviceListRoute() {
  return <DeviceList />;
}

export function AgentDevicesRoute() {
  return <AgentDevices />;
}

export function SSHDevicesRoute() {
  return <SSHDevices />;
}

export function WinRMDevicesRoute() {
  return <WinRMDevices />;
}

export async function DeviceSummaryRouteLoader({ request, params }) {
  return loadDeviceSummaryPageData(request, params?.deviceId);
}

export function DeviceSummaryRouteShouldRevalidate({ currentParams, nextParams }) {
  return String(currentParams?.deviceId || "") !== String(nextParams?.deviceId || "");
}

export function DeviceSummaryRoute() {
  return <DeviceSummary />;
}

export function RemoteDesktopRoute() {
  return <RemoteDesktop />;
}

export function DeviceApprovalsRoute() {
  return <DeviceApprovals />;
}
