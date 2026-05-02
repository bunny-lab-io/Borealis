import React from "react";
import { RouterProvider } from "react-router-dom";
import { AppProviders } from "./providers/AppProviders.jsx";
import { bootstrapClientRuntime } from "./runtime/bootstrapClientRuntime.js";
import { createAppRouter } from "./routes/router.jsx";
import { FullScreenPending } from "./routes/guards.jsx";

bootstrapClientRuntime();

const appRouter = createAppRouter();

export default function AppRoot() {
  return (
    <AppProviders>
      <RouterProvider router={appRouter} fallbackElement={<FullScreenPending />} />
    </AppProviders>
  );
}
