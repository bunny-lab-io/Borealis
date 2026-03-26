import React from "react";
import BuildCircleRoundedIcon from "@mui/icons-material/BuildCircleRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const ExecuteAssemblyNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Execute Assembly"
    icon={<BuildCircleRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.executeAssembly,
  label: "Execute Assembly",
  description: "Executes a saved script or Ansible assembly as one workflow node.",
  content: "Execute a saved assembly",
  component: ExecuteAssemblyNode,
  config: [
    {
      key: "assembly_guid",
      label: "Assembly",
      type: "select",
      optionsSource: "assemblies_executable",
      placeholder: "Select a script or Ansible assembly",
    },
    {
      key: "execution_mode",
      label: "Execution Mode",
      type: "select",
      options: [
        { value: "system", label: "System" },
        { value: "currentuser", label: "Current User" },
        { value: "local", label: "Local Engine" },
        { value: "ssh", label: "SSH" },
        { value: "winrm", label: "WinRM" },
      ],
      defaultValue: "system",
    },
    {
      key: "timeout_seconds",
      label: "Timeout Seconds",
      type: "number",
      defaultValue: 0,
    },
    {
      key: "export_key",
      label: "Export Key",
      type: "text",
      defaultValue: "",
    },
  ],
  usage_documentation: `
### Execute Assembly

Runs a saved script or Ansible assembly inside Workflow Runtime v1.

- Connect one or more **Device Filter** or **List of Devices** nodes upstream to provide the frozen target snapshot for this node.
- Runtime v1 supports script and Ansible assemblies here, not workflow assemblies.
- Use **Execute Subworkflow** for child workflows.
`.trim(),
};
