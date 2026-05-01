#!/usr/bin/env node

const http = require("http");

const mode = String(process.env.BOREALIS_WEBUI_MODE || "prod").toLowerCase();
const port = mode === "dev" || mode === "developer" ? 5173 : 8080;

const request = http.get(
  {
    host: "127.0.0.1",
    port,
    path: "/",
    timeout: 3000,
  },
  (response) => {
    response.resume();
    process.exit(response.statusCode >= 200 && response.statusCode < 500 ? 0 : 1);
  },
);

request.on("timeout", () => {
  request.destroy(new Error("timeout"));
});

request.on("error", () => {
  process.exit(1);
});
