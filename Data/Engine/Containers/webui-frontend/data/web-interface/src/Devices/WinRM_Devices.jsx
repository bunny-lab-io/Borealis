import React from "react";
import SSHDevices from "./SSH_Devices.jsx";

export default function WinRMDevices(props) {
  return <SSHDevices {...props} type="winrm" />;
}
