#!/usr/bin/env node
const { spawnSync } = require("node:child_process");

const pkg = `@sivancola/deepzora-cli-${process.platform}-${process.arch}`;
const exe = `deepzora${process.platform === "win32" ? ".exe" : ""}`;

let binary;
try {
  binary = require.resolve(`${pkg}/bin/${exe}`);
} catch {
  console.error(
    `deepzora: no prebuilt binary for ${process.platform}-${process.arch}.\n` +
      `Install the matching optional package (${pkg}), or build from source:\n` +
      `  https://github.com/SivanCola/deepzora`,
  );
  process.exit(1);
}

const res = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (res.error) throw res.error;
process.exit(res.status === null ? 1 : res.status);
