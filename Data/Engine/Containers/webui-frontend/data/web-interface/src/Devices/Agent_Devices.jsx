import React from "react";
import DeviceList from "./Device_List.jsx";

export default function AgentDevices(props) {
  return (
    <DeviceList
      {...props}
      filterMode="agent"
      title="Agent Devices"
      showAddButton={false}
    />
  );
}
