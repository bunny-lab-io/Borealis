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

function resolveRequestPath(pathname, rootPath = defaultRoot) {
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
  const relativePath = normalizedPath.replace(/^\/+/, "");
  const resolvedPath = path.resolve(rootPath, relativePath);
  if (resolvedPath !== rootPath && !resolvedPath.startsWith(`${rootPath}${path.sep}`)) {
    return null;
  }
  return resolvedPath;
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
  const indexPath = path.join(root, "index.html");

  return function handleRequest(request, response) {
    if (!["GET", "HEAD"].includes(request.method || "")) {
      response.writeHead(405, { allow: "GET, HEAD" });
      response.end();
      return;
    }

    let requestUrl;
    try {
      requestUrl = new URL(request.url || "/", `http://${request.headers.host || host}`);
    } catch {
      sendText(response, 400, "Bad Request\n");
      return;
    }
    const rawPathname = (request.url || "/").split(/[?#]/, 1)[0];
    const requestedPath = resolveRequestPath(rawPathname, root);
    if (!requestedPath) {
      sendText(response, 400, "Bad Request\n");
      return;
    }

    fs.stat(requestedPath, (statError, stats) => {
      if (!statError && stats.isDirectory()) {
        sendFile(request, response, path.join(requestedPath, "index.html"), false);
        return;
      }
      if (!statError && stats.isFile()) {
        sendFile(request, response, requestedPath, requestUrl.pathname.startsWith("/assets/"));
        return;
      }

      if (path.extname(requestUrl.pathname)) {
        sendText(response, 404, "Not Found\n");
        return;
      }
      sendFile(request, response, indexPath, false);
    });
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
  resolveRequestPath,
};
