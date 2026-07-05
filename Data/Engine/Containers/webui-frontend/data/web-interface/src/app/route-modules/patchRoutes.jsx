import React from "react";
import PatchManagementWindows from "../../Patches/Patch_Management_Windows.jsx";
import PatchManagementLinux from "../../Patches/Patch_Management_Linux.jsx";
import PatchManagementMacOS from "../../Patches/Patch_Management_MacOS.jsx";
import PatchPolicyEditor from "../../Patches/Patch_Policy_Editor.jsx";

export function PatchManagementRoute() {
  return <PatchManagementWindows />;
}

export function PatchLinuxRoute() {
  return <PatchManagementLinux />;
}

export function PatchMacOSRoute() {
  return <PatchManagementMacOS />;
}

export function PatchPolicyEditorRoute() {
  return <PatchPolicyEditor />;
}
