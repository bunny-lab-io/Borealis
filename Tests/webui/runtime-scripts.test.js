const assert = require("node:assert/strict");
const childProcess = require("node:child_process");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const repoRoot = path.resolve(__dirname, "../..");
const staticServerPath = path.join(
  repoRoot,
  "Data/Engine/Containers/webui-frontend/static-server.js",
);
const healthcheckPath = path.join(
  repoRoot,
  "Data/Engine/Containers/webui-frontend/healthcheck.js",
);
const entrypointPath = path.join(
  repoRoot,
  "Data/Engine/Containers/webui-frontend/entrypoint.sh",
);
const { createRequestHandler, createStaticServer } = require(staticServerPath);
const { checkHealth } = require(healthcheckPath);

function fixtureRoot() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "borealis-webui-runtime-"));
  fs.mkdirSync(path.join(root, "assets"));
  fs.mkdirSync(path.join(root, "docs"));
  fs.writeFileSync(path.join(root, "index.html"), "<html>spa</html>");
  fs.writeFileSync(path.join(root, "assets/app.js"), "console.log('asset');");
  fs.writeFileSync(path.join(root, "docs/index.html"), "<html>docs</html>");
  fs.writeFileSync(path.join(root, "data.json"), "{}\n");
  return root;
}

async function listen(server) {
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  return server.address().port;
}

async function close(server) {
  await new Promise((resolve) => server.close(resolve));
}

function request(port, requestPath, method = "GET") {
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: "127.0.0.1", port, path: requestPath, method },
      (response) => {
        const chunks = [];
        response.on("data", (chunk) => chunks.push(chunk));
        response.on("end", () =>
          resolve({
            status: response.statusCode,
            headers: response.headers,
            body: Buffer.concat(chunks).toString("utf8"),
          }),
        );
      },
    );
    req.on("error", reject);
    req.end();
  });
}

async function withStaticServer(callback) {
  const root = fixtureRoot();
  const server = createStaticServer({ root, host: "127.0.0.1" });
  const port = await listen(server);
  try {
    await callback({ root, port });
  } finally {
    await close(server);
    fs.rmSync(root, { recursive: true, force: true });
  }
}

test("static server accepts only GET and HEAD", async () => {
  await withStaticServer(async ({ port }) => {
    const response = await request(port, "/", "POST");
    assert.equal(response.status, 405);
    assert.equal(response.headers.allow, "GET, HEAD");
  });
});

test("static server rejects malformed, invalid decoding, NUL, and traversal paths", async () => {
  const handler = createRequestHandler({ root: fixtureRoot(), host: "127.0.0.1" });
  const response = {
    status: 0,
    writeHead(status) {
      this.status = status;
    },
    end() {},
  };
  handler({ method: "GET", url: "http://[invalid", headers: {} }, response);
  assert.equal(response.status, 400);
  await withStaticServer(async ({ port }) => {
    assert.equal((await request(port, "/bad%ZZ")).status, 400);
    assert.equal((await request(port, "/bad%00name")).status, 400);
    assert.equal((await request(port, "/%2e%2e%2fsecret.txt")).status, 400);
  });
});

test("static server serves directories, exact files, SPA routes, and MIME types", async () => {
  await withStaticServer(async ({ port }) => {
    assert.equal((await request(port, "/docs/")).body, "<html>docs</html>");
    assert.equal((await request(port, "/docs")).body, "<html>docs</html>");
    const exact = await request(port, "/data.json");
    assert.equal(exact.status, 200);
    assert.equal(exact.headers["content-type"], "application/json; charset=utf-8");
    assert.equal((await request(port, "/devices/example")).body, "<html>spa</html>");
    assert.equal((await request(port, "/missing.js")).status, 404);
  });
});

test("static server applies immutable asset and no-cache HTML policy", async () => {
  await withStaticServer(async ({ port }) => {
    assert.equal(
      (await request(port, "/assets/app.js")).headers["cache-control"],
      "public, max-age=31536000, immutable",
    );
    assert.equal((await request(port, "/")).headers["cache-control"], "no-cache");
  });
});

test("HEAD returns content length without body", async () => {
  await withStaticServer(async ({ port }) => {
    const response = await request(port, "/assets/app.js", "HEAD");
    assert.equal(response.status, 200);
    assert.equal(Number(response.headers["content-length"]), Buffer.byteLength("console.log('asset');"));
    assert.equal(response.body, "");
  });
});

test("missing index produces 404 instead of empty SPA fallback", async () => {
  await withStaticServer(async ({ root, port }) => {
    fs.unlinkSync(path.join(root, "index.html"));
    assert.equal((await request(port, "/route")).status, 404);
  });
});

test("healthcheck accepts 2xx-4xx and rejects 5xx", async () => {
  for (const [statusCode, expected] of [
    [204, 0],
    [404, 0],
    [500, 1],
  ]) {
    const server = http.createServer((_request, response) => response.writeHead(statusCode).end());
    const port = await listen(server);
    assert.equal(await checkHealth({ host: "127.0.0.1", port, timeout: 100 }), expected);
    await close(server);
  }
});

test("healthcheck rejects connection errors and timeout", async () => {
  const closedServer = http.createServer();
  const closedPort = await listen(closedServer);
  await close(closedServer);
  assert.equal(await checkHealth({ host: "127.0.0.1", port: closedPort, timeout: 50 }), 1);

  const hangingServer = http.createServer(() => {});
  const hangingPort = await listen(hangingServer);
  assert.equal(await checkHealth({ host: "127.0.0.1", port: hangingPort, timeout: 20 }), 1);
  await close(hangingServer);
});

function runEntrypoint(mode) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "borealis-webui-entrypoint-"));
  const bin = path.join(root, "bin");
  const capture = path.join(root, "capture.txt");
  fs.mkdirSync(bin);
  const recorder = "#!/bin/sh\nprintf '%s|%s|%s\\n' \"$*\" \"${NODE_ENV:-}\" \"${BOREALIS_DEV_UI_PROXY_ENABLED:-}\" > \"$BOREALIS_TEST_CAPTURE\"\n";
  for (const command of ["node", "npm"]) {
    const commandPath = path.join(bin, command);
    fs.writeFileSync(commandPath, recorder, { mode: 0o755 });
  }
  const result = childProcess.spawnSync("sh", [entrypointPath], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      BOREALIS_TEST_CAPTURE: capture,
      BOREALIS_WEBUI_MODE: mode,
      BOREALIS_WEBUI_WORKDIR: root,
      BOREALIS_WEBUI_STATIC_SERVER_BIN: "/fixture/static-server.js",
      BOREALIS_WEBUI_BIND_HOST: "127.0.0.2",
      BOREALIS_WEBUI_UPSTREAM_PORT: "8123",
    },
  });
  const output = fs.existsSync(capture) ? fs.readFileSync(capture, "utf8").trim() : "";
  fs.rmSync(root, { recursive: true, force: true });
  return { ...result, output };
}

test("entrypoint forwards prod and dev modes and rejects unsupported mode", () => {
  const prod = runEntrypoint("prod");
  assert.equal(prod.status, 0);
  assert.equal(prod.output, "/fixture/static-server.js|production|");

  const dev = runEntrypoint("dev");
  assert.equal(dev.status, 0);
  assert.equal(dev.output, "run dev -- --host 127.0.0.2 --port 8123 --strictPort --no-open|development|1");

  const unsupported = runEntrypoint("preview");
  assert.equal(unsupported.status, 2);
  assert.match(unsupported.stderr, /Unsupported BOREALIS_WEBUI_MODE/);
});
