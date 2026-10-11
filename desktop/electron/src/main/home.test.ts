import assert from "node:assert/strict";
import { sep } from "node:path";
import { test } from "node:test";
import { deepzoraHome } from "./home.js";

const unix = (env: NodeJS.ProcessEnv, platform: NodeJS.Platform = "darwin") =>
  deepzoraHome({ env, platform, homedir: () => "/home/fallback", cwd: () => "/work" });

test("Reasonix environment cannot select Deepzora's home", () => {
  assert.equal(unix({ HOME: "/home/u", REASONIX_HOME: "/home/u/.reasonix" }), "/home/u/.deepzora");
});

test("DEEPZORA_HOME wins, with ~ and ${VAR} expansion and relative paths made absolute", () => {
  assert.equal(unix({ DEEPZORA_HOME: "/data/rx", HOME: "/home/u" }), "/data/rx");
  assert.equal(unix({ DEEPZORA_HOME: "  /data/rx/  ", HOME: "/home/u" }), "/data/rx");
  assert.equal(unix({ DEEPZORA_HOME: "~/rx", HOME: "/home/u" }), "/home/u/rx");
  assert.equal(unix({ DEEPZORA_HOME: "~", HOME: "/home/u" }), "/home/u");
  assert.equal(unix({ DEEPZORA_HOME: "${BASE}/rx", BASE: "/srv", HOME: "/home/u" }), "/srv/rx");
  assert.equal(unix({ DEEPZORA_HOME: "${MISSING:-/opt}/rx", HOME: "/home/u" }), "/opt/rx");
  assert.equal(unix({ DEEPZORA_HOME: "rel/rx", HOME: "/home/u" }), "/work/rel/rx");
});

test("macOS and Linux default to ~/.deepzora from $HOME, then the passwd home", () => {
  assert.equal(unix({ HOME: "/home/u" }), "/home/u/.deepzora");
  assert.equal(unix({ HOME: "/home/u" }, "linux"), "/home/u/.deepzora");
  assert.equal(unix({}), "/home/fallback/.deepzora");
});

test("Windows uses %APPDATA%\\deepzora with the Roaming fallback", () => {
  const win = (env: NodeJS.ProcessEnv) => deepzoraHome({ env, platform: "win32", homedir: () => "", cwd: () => "C:\\work" });
  assert.equal(win({ APPDATA: "C:\\Users\\u\\AppData\\Roaming" }), ["C:\\Users\\u\\AppData\\Roaming", "deepzora"].join(sep));
  assert.equal(win({ USERPROFILE: "C:\\Users\\u" }), ["C:\\Users\\u", "AppData", "Roaming", "deepzora"].join(sep));
});
