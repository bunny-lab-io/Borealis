import React from "react";
import DeviceList from "../../Devices/Device_List";
import AgentDevices from "../../Devices/Agent_Devices.jsx";
import SSHDevices from "../../Devices/SSH_Devices.jsx";
import WinRMDevices from "../../Devices/WinRM_Devices.jsx";
import DeviceSummary from "../../Devices/Tabs/Device_Summary.jsx";
import RemoteDesktop from "../../Devices/Tabs/Remote_Desktop.jsx";
import DeviceApprovals from "../../Devices/Device_Approvals.jsx";

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

export function DeviceSummaryRoute() {
  return <DeviceSummary />;
}

export function RemoteDesktopRoute() {
  return <RemoteDesktop />;
}

export function DeviceApprovalsRoute() {
  return <DeviceApprovals />;
}
