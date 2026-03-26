import React from "react";
import PlayArrowRoundedIcon from "@mui/icons-material/PlayArrowRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const ManualTriggerNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Trigger - Manual"
    icon={<PlayArrowRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.triggerManual}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.triggerManual,
  label: "Trigger - Manual",
  description: "Starts a workflow when an operator manually triggers it from the workflow editor.",
  content: "Manual workflow trigger",
  component: ManualTriggerNode,
  config: [],
  usage_documentation: `
### Trigger - Manual

Starts this workflow when an operator clicks **Trigger Workflow** from the flow editor.

- Exactly one manual trigger node is required for manual launches.
- This node does not accept upstream input.
- Downstream execution begins after the workflow snapshot is saved and launched server-side.
`.trim(),
};
