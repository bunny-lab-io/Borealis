const fs = require("fs");
const path = require("path");

const target = path.resolve(
  __dirname,
  "..",
  "node_modules",
  "@novnc",
  "novnc",
  "lib",
  "util",
  "browser.js"
);

if (!fs.existsSync(target)) {
  console.log("[patch-novnc] target not found; skipping");
  process.exit(0);
}

const source = fs.readFileSync(target, "utf8");
const marker =
  "exports.supportsWebCodecsH264Decode = supportsWebCodecsH264Decode = await _checkWebCodecsH264DecodeSupport();";

if (!source.includes(marker)) {
  console.log("[patch-novnc] marker not found; no changes applied");
  process.exit(0);
}

const replacement = [
  "exports.supportsWebCodecsH264Decode = supportsWebCodecsH264Decode = false;",
  "_checkWebCodecsH264DecodeSupport().then(function (value) {",
  "  exports.supportsWebCodecsH264Decode = supportsWebCodecsH264Decode = value;",
  "}).catch(function () {});",
  "",
].join("\n");

const updated = source.replace(marker, replacement);
fs.writeFileSync(target, updated, "utf8");
console.log("[patch-novnc] patched", target);
