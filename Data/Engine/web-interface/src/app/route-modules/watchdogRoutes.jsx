import React from "react";
import WatchdogList from "../../Automation/Watchdogs/Watchdog_List.jsx";
import WatchdogEditor from "../../Automation/Watchdogs/Watchdog_Editor.jsx";
import ActiveAlerts from "../../Alerting/Active_Alerts.jsx";

export function WatchdogListRoute() {
  return <WatchdogList />;
}

export function WatchdogEditorRoute() {
  return <WatchdogEditor />;
}

export function ActiveAlertsRoute() {
  return <ActiveAlerts />;
}
