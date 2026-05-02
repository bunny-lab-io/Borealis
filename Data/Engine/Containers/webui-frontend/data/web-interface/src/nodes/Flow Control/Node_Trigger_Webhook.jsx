import React from "react";
import WebhookRoundedIcon from "@mui/icons-material/WebhookRounded";
import WorkflowRuntimeNodeCard from "../../Flow_Editor/WorkflowRuntimeNodeCard.jsx";
import { WORKFLOW_RUNTIME_NODE_TYPES } from "../../Flow_Editor/runtimeV1.js";

const WebhookTriggerNode = ({ data }) => (
  <WorkflowRuntimeNodeCard
    data={data}
    title="Trigger - Webhook"
    icon={<WebhookRoundedIcon fontSize="small" />}
    nodeType={WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook}
  />
);

export default {
  type: WORKFLOW_RUNTIME_NODE_TYPES.triggerWebhook,
  label: "Trigger - Webhook",
  description: "Starts a workflow from a workflow-specific opaque webhook URL.",
  content: "Webhook workflow trigger",
  component: WebhookTriggerNode,
  config: [],
  usage_documentation: `
### Trigger - Webhook

Starts this workflow when one of its configured opaque webhook URLs is called.

- Exactly one webhook trigger node is required for webhook launches.
- Manage workflow webhooks from **Webhook Management** in the workflow sidebar.
- The webhook itself is the shared secret for Runtime v1.
`.trim(),
};
