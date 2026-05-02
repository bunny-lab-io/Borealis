import React from "react";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const ExecuteSubworkflowNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Execute Subworkflow"
    icon={<AccountTreeRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.executeSubworkflow,
  label: "Execute Subworkflow",
  description: "Executes another Borealis workflow as a child workflow run.",
  content: "Execute a saved workflow",
  component: ExecuteSubworkflowNode,
  config: [
    {
      key: "workflow_guid",
      label: "Workflow",
      type: "select",
      optionsSource: "workflows",
      placeholder: "Select a workflow",
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
### Execute Subworkflow

Runs another saved workflow as a child workflow execution.

- Returns the child workflow final status by default.
- Optionally give the node an export key so parent workflows can consume selected child data later.
- Runtime v1 prevents recursive parent/child workflow loops.
`.trim(),
};
