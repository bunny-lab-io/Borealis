const modules = import.meta.glob("../nodes/**/*.jsx", { eager: true });

export const workflowNodeTypes = {};
export const workflowCategorizedNodes = {};

Object.entries(modules).forEach(([path, mod]) => {
  const comp = mod.default;
  if (!comp) return;
  const { type, component } = comp;
  if (!type || !component) return;
  const parts = path.replace("../nodes/", "").split("/");
  const category = parts[0];
  if (!workflowCategorizedNodes[category]) workflowCategorizedNodes[category] = [];
  workflowCategorizedNodes[category].push(comp);
  workflowNodeTypes[type] = component;
});

if (!Object.keys(workflowNodeTypes).length) {
  console.warn(
    "[Flow Editor] No node modules were loaded from ../nodes/**/*.jsx. " +
      "Check the node source tree and import.meta.glob path casing."
  );
}
