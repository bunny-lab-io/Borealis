import React from "react";
import DeviceFilterList from "../../Devices/Filters/Filter_List.jsx";
import DeviceFilterEditor from "../../Devices/Filters/Filter_Editor.jsx";

export function FilterListRoute() {
  return <DeviceFilterList />;
}

export function FilterEditorRoute() {
  return <DeviceFilterEditor />;
}
