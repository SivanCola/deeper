import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";
import { packagedSmokeEnv } from "./smoke-env.mjs";

test("packaged startup uses an isolated home without inherited development or service overrides", () => {
  const parent = {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    DEEPER_DEV: "1", Deeper_Dev: "1", DEEPER_CHANNEL: "dev", DEEPER_COMMIT: "old",
    DEEPER_DESKTOP_SERVICE: "old-service", DEEPER_ELECTRON_DEV_URL: "http://localhost:5173",
    DEEPER_HOME: "real-user-data", DEEPER_STATE_HOME: "real-state", DEEPER_CACHE_HOME: "real-cache",
    NODE_OPTIONS: "--require development-hook", ELECTRON_RUN_AS_NODE: "1",
  };
  assert.deepEqual(packagedSmokeEnv(parent, "fixture"), {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    DEEPER_HOME: "fixture", DEEPER_STATE_HOME: "fixture", DEEPER_CACHE_HOME: join("fixture", "cache"),
  });
  assert.equal(parent.DEEPER_DEV, "1", "the caller environment is not mutated");
});
