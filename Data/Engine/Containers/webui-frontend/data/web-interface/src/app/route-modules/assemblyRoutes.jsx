import React from "react";
import AssemblyList, { loadAssemblyListPageData } from "../../Assemblies/Assembly_List.jsx";
import AssemblyEditor from "../../Assemblies/Assembly_Editor.jsx";
import FlowEditor from "../../Flow_Editor/Flow_Editor.jsx";

export async function AssembliesRouteLoader({ request }) {
  return loadAssemblyListPageData(request);
}

export function AssembliesRoute() {
  return <AssemblyList />;
}

export function ScriptAssemblyRoute() {
  return <AssemblyEditor />;
}

export function AnsibleAssemblyRoute() {
  return <AssemblyEditor />;
}

export function WorkflowEditorRoute() {
  return <FlowEditor />;
}
