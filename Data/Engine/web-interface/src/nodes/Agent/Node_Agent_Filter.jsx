import React from "react";
import FilterAltRoundedIcon from "@mui/icons-material/FilterAltRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const AgentFilterNode = ({ data }) => {
  return (
    <WorkflowRuntimeNodeCard
      data={data}
      title="Device Filter"
      icon={<FilterAltRoundedIcon fontSize="small" />}
      nodeType={WORKFLOW_RUNTIME_NODE_TYPES.agentFilter}
    />
  );
};

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.agentFilter,
  label: "Device Filter",
  description: "Resolves Borealis target devices from one saved device filter.",
  content: "Target devices from a saved filter",
  component: AgentFilterNode,
  config: [
    {
      key: "filter_picker",
      label: "Device Filter",
      type: "agent_filter_picker",
    },
  ],
  usage_documentation: `
### Device Filter

Resolves one saved Borealis device filter into a frozen device target set.

- Search filters by name with a minimum of 3 characters.
- Results are scoped to the operator's visible sites.
- Connect this node upstream of **Execute Assembly** to target the resolved devices.
`.trim(),
};
