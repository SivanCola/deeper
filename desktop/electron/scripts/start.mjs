#!/usr/bin/env node
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dev = process.argv.includes("--dev");
const env = { ...process.env };

if (!env.DEEPER_DESKTOP_SERVICE) {
  env.DEEPER_DESKTOP_SERVICE = resolve(root, "../build/bin", process.platform === "win32" ? "deeper-desktop-service.exe" : "deeper-desktop-service");
}
if (!existsSync(env.DEEPER_DESKTOP_SERVICE)) {
  console.error("desktop service binary not found; check DEEPER_DESKTOP_SERVICE or build it with: cd desktop && go build -o build/bin/deeper-desktop-service .");
  process.exit(1);
}
if (dev) {
  env.DEEPER_DEV ??= "1";
  env.DEEPER_ELECTRON_DEV_URL ??= `http://127.0.0.1:${env.DEEPER_DESKTOP_VITE_PORT || "5173"}`;
}

const electron = createRequire(import.meta.url)("electron");
const child = spawn(electron, [root], { env, stdio: "inherit", windowsHide: false });
child.on("exit", (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
