#!/usr/bin/env node

const http = require("http");

const host = process.env.BOREALIS_WEBUI_HEALTH_HOST || "127.0.0.1";
const port = Number.parseInt(process.env.BOREALIS_WEBUI_UPSTREAM_PORT || "8000", 10);

const request = http.get(
  {
    host,
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
