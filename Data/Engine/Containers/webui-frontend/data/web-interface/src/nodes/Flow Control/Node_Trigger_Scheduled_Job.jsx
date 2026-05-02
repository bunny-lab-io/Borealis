import React from "react";
import ScheduleRoundedIcon from "@mui/icons-material/ScheduleRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const ScheduledTriggerNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Trigger - Scheduled Job"
    icon={<ScheduleRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.triggerScheduledJob,
  label: "Trigger - Scheduled Job",
  description: "Starts a workflow when a Borealis scheduled job fires.",
  content: "Scheduled workflow trigger",
  component: ScheduledTriggerNode,
  config: [],
  usage_documentation: `
### Trigger - Scheduled Job

Starts this workflow when a Borealis scheduled job runs it.

- Exactly one scheduled-job trigger node is required for scheduled launches.
- Scheduler-level targets are not used for workflow jobs in Runtime v1.
- The workflow runtime reports its final status back to the scheduled job history.
`.trim(),
};
