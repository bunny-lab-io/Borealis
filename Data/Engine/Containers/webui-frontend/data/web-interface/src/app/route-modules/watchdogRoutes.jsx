import React from "react";
import WatchdogList, {
  loadWatchdogListPageData,
} from "../../Automation/Watchdogs/Watchdog_List.jsx";
import WatchdogEditor, {
  loadWatchdogEditorPageData,
} from "../../Automation/Watchdogs/Watchdog_Editor.jsx";
import ActiveAlerts from "../../Alerting/Active_Alerts.jsx";

export async function WatchdogListRouteLoader({ request }) {
  return loadWatchdogListPageData(request);
}

export async function WatchdogEditorRouteLoader({ request, params }) {
  return loadWatchdogEditorPageData(request, params?.watchdogId);
}

export function WatchdogListRoute() {
  return <WatchdogList />;
}

export function WatchdogEditorRoute() {
  return <WatchdogEditor />;
}

export function ActiveAlertsRoute() {
  return <ActiveAlerts />;
}
