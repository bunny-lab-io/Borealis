import React from "react";
import DeviceFilterList, {
  loadDeviceFilterListPageData,
} from "../../Devices/Filters/Filter_List.jsx";
import DeviceFilterEditor, {
  loadDeviceFilterEditorPageData,
} from "../../Devices/Filters/Filter_Editor.jsx";

export async function FilterListRouteLoader({ request }) {
  return loadDeviceFilterListPageData(request);
}

export async function FilterEditorRouteLoader({ request, params }) {
  return loadDeviceFilterEditorPageData(request, params?.filterId);
}

export function FilterListRoute() {
  return <DeviceFilterList />;
}

export function FilterEditorRoute() {
  return <DeviceFilterEditor />;
}
