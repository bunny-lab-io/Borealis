#!/usr/bin/env node

const fs = require("fs");
const http = require("http");
const path = require("path");
const { pipeline } = require("stream");

const defaultHost = process.env.BOREALIS_WEBUI_BIND_HOST || "127.0.0.1";
const defaultPort = Number.parseInt(process.env.BOREALIS_WEBUI_UPSTREAM_PORT || "8000", 10);
const defaultRoot = path.resolve(
  process.env.BOREALIS_WEBUI_STATIC_ROOT || "/opt/Borealis/Data/Engine/web-interface/build",
);

const mimeTypes = new Map([
  [".css", "text/css; charset=utf-8"],
  [".gif", "image/gif"],
  [".html", "text/html; charset=utf-8"],
  [".ico", "image/x-icon"],
  [".js", "text/javascript; charset=utf-8"],
  [".json", "application/json; charset=utf-8"],
  [".map", "application/json; charset=utf-8"],
  [".png", "image/png"],
  [".svg", "image/svg+xml"],
  [".txt", "text/plain; charset=utf-8"],
  [".wasm", "application/wasm"],
  [".woff", "font/woff"],
  [".woff2", "font/woff2"],
]);

function sendText(response, statusCode, body) {
  response.writeHead(statusCode, {
    "content-length": Buffer.byteLength(body),
    "content-type": "text/plain; charset=utf-8",
  });
  response.end(body);
}

function normalizeRequestPath(pathname) {
  let decodedPath = "/";
  try {
    decodedPath = decodeURIComponent(pathname || "/");
  } catch {
    return null;
  }
  if (decodedPath.includes("\0") || decodedPath.includes("\\")) {
    return null;
  }
  if (decodedPath.split("/").some((segment) => segment === "..")) {
    return null;
  }

  const normalizedPath = path.posix.normalize(decodedPath);
  return `/${normalizedPath.replace(/^\/+/, "")}`;
}

function buildFileIndex(rootPath) {
  const files = new Map();
  const pending = [{ absolutePath: rootPath, relativePath: "" }];
  while (pending.length > 0) {
    const current = pending.pop();
    let entries;
    try {
      entries = fs.readdirSync(current.absolutePath, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries) {
      const absolutePath = path.join(current.absolutePath, entry.name);
      const relativePath = current.relativePath
        ? `${current.relativePath}/${entry.name}`
        : entry.name;
      if (entry.isDirectory()) {
        pending.push({ absolutePath, relativePath });
      } else if (entry.isFile()) {
        files.set(`/${relativePath}`, absolutePath);
      }
    }
  }

  for (const [requestPath, filePath] of Array.from(files.entries())) {
    if (!requestPath.endsWith("/index.html")) {
      continue;
    }
    const directoryPath = requestPath.slice(0, -"index.html".length);
    files.set(directoryPath, filePath);
    if (directoryPath !== "/") {
      files.set(directoryPath.slice(0, -1), filePath);
    }
  }
  return files;
}

function sendFile(request, response, filePath, cacheable) {
  const contentType = mimeTypes.get(path.extname(filePath).toLowerCase()) || "application/octet-stream";
  const headers = {
    "cache-control": cacheable ? "public, max-age=31536000, immutable" : "no-cache",
    "content-type": contentType,
  };

  fs.stat(filePath, (statError, stats) => {
    if (statError || !stats.isFile()) {
      sendText(response, 404, "Not Found\n");
      return;
    }

    response.writeHead(200, {
      ...headers,
      "content-length": stats.size,
    });
    if (request.method === "HEAD") {
      response.end();
      return;
    }

    pipeline(fs.createReadStream(filePath), response, (streamError) => {
      if (streamError && !response.headersSent) {
        sendText(response, 500, "Internal Server Error\n");
      } else if (streamError) {
        response.destroy(streamError);
      }
    });
  });
}

function createRequestHandler(options = {}) {
  const host = options.host || defaultHost;
  const root = path.resolve(options.root || defaultRoot);
  const fileIndex = buildFileIndex(root);
  const indexPath = fileIndex.get("/");

  return function handleRequest(request, response) {
    if (!["GET", "HEAD"].includes(request.method || "")) {
      response.writeHead(405, { allow: "GET, HEAD" });
      response.end();
      return;
    }

    try {
      new URL(request.url || "/", `http://${request.headers.host || host}`);
    } catch {
      sendText(response, 400, "Bad Request\n");
      return;
    }
    const rawPathname = (request.url || "/").split(/[?#]/, 1)[0];
    const requestPath = normalizeRequestPath(rawPathname);
    if (!requestPath) {
      sendText(response, 400, "Bad Request\n");
      return;
    }

    const requestedFile = fileIndex.get(requestPath);
    if (requestedFile) {
      sendFile(request, response, requestedFile, requestPath.startsWith("/assets/"));
      return;
    }
    if (path.posix.extname(requestPath) || !indexPath) {
      sendText(response, 404, "Not Found\n");
      return;
    }
    sendFile(request, response, indexPath, false);
  };
}

function createStaticServer(options = {}) {
  return http.createServer(createRequestHandler(options));
}

if (require.main === module) {
  const server = createStaticServer();
  server.listen(defaultPort, defaultHost, () => {
    console.log(`Borealis WebUI static server listening on http://${defaultHost}:${defaultPort}`);
  });
}

module.exports = {
  createRequestHandler,
  createStaticServer,
  mimeTypes,
  normalizeRequestPath,
};
