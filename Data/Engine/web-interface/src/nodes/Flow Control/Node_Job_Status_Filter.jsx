import React from "react";
import RuleRoundedIcon from "@mui/icons-material/RuleRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import {
  WORKFLOW_RUNTIME_JOB_STATUS_OPTIONS,
  WORKFLOW_RUNTIME_NODE_TYPES,
} from "../../Flow_Editor/runtimeV1.js";

const JobStatusFilterNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Job Status Filter"
    icon={<RuleRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.jobStatusFilter}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.jobStatusFilter,
  label: "Job Status Filter",
  description: "Filters per-device Job Output records by Success, Warning, or Failed status.",
  content: "Filter job output results",
  component: JobStatusFilterNode,
  config: [
    {
      key: "match_status",
      label: "Match Status",
      type: "select",
      options: WORKFLOW_RUNTIME_JOB_STATUS_OPTIONS,
      defaultValue: "Failed",
    },
  ],
  usage_documentation: `
### Job Status Filter

Filters one or more upstream **Job Output** streams and emits only the devices whose execution status matches the selected status.

- Accepts **Job Output** from nodes such as **Execute Assembly**.
- Emits matching devices from the **Targets** output.
- Also emits **Action** so you can continue the workflow after the filter node runs.
`.trim(),
};
