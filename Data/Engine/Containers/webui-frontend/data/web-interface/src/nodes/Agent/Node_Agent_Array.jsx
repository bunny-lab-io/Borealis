import React from "react";
import DevicesRoundedIcon from "@mui/icons-material/DevicesRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const AgentArrayNode = ({ data }) => {
  return (
    <WorkflowRuntimeNodeCard
      data={data}
      title="List of Devices"
      icon={<DevicesRoundedIcon fontSize="small" />}
      nodeType={WORKFLOW_RUNTIME_NODE_TYPES.agentArray}
    />
  );
};

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.agentArray,
  label: "List of Devices",
  description: "Targets a manually curated list of Borealis devices.",
  content: "Target selected devices",
  component: AgentArrayNode,
  config: [
    {
      key: "device_array_picker",
      label: "Selected Devices",
      type: "agent_device_array_picker",
    },
  ],
  usage_documentation: `
### List of Devices

Builds a frozen workflow target set from explicitly selected devices.

- Search devices by hostname with a minimum of 3 characters.
- Results are scoped to the operator's visible sites.
- Selected devices are shown in an AG Grid table and remain alphabetized by hostname.
- Connect this node upstream of **Execute Assembly** to target those devices.
`.trim(),
};
