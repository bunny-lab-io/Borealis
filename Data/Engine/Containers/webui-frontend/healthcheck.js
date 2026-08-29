#!/usr/bin/env node

const http = require("http");
const fs = require("fs");

function checkHealth(options = {}) {
  const host = options.host || process.env.BOREALIS_WEBUI_HEALTH_HOST || "127.0.0.1";
  const port = options.port || Number.parseInt(process.env.BOREALIS_WEBUI_UPSTREAM_PORT || "8000", 10);
  const timeout = options.timeout || 3000;
  const client = options.http || http;

  return new Promise((resolve) => {
    let settled = false;
    const finish = (status) => {
      if (!settled) {
        settled = true;
        resolve(status);
      }
    };
    const request = client.get(
      {
        host,
        port,
        path: "/",
        timeout,
      },
      (response) => {
        response.resume();
        finish(response.statusCode >= 200 && response.statusCode < 500 ? 0 : 1);
      },
    );

    request.on("timeout", () => {
      request.destroy(new Error("timeout"));
    });
    request.on("error", () => {
      finish(1);
    });
  });
}

if (require.main === module) {
  const mode = String(process.argv[2] || "ready").trim().toLowerCase();
  if (mode === "ready" && fs.existsSync("/tmp/borealis-draining")) {
    process.exit(1);
  }
  checkHealth().then((status) => process.exit(status));
}

module.exports = { checkHealth };
