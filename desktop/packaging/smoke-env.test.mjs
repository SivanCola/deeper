import assert from "node:assert/strict";
import { join } from "node:path";
import { test } from "node:test";
import { packagedSmokeEnv } from "./smoke-env.mjs";

test("packaged startup uses an isolated home without inherited development or service overrides", () => {
  const parent = {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    DEEPZORA_DEV: "1", Deepzora_Dev: "1", DEEPZORA_CHANNEL: "dev", DEEPZORA_COMMIT: "old",
    DEEPZORA_DESKTOP_SERVICE: "old-service", DEEPZORA_ELECTRON_DEV_URL: "http://localhost:5173",
    DEEPZORA_HOME: "real-user-data", DEEPZORA_STATE_HOME: "real-state", DEEPZORA_CACHE_HOME: "real-cache",
    NODE_OPTIONS: "--require development-hook", ELECTRON_RUN_AS_NODE: "1",
  };
  assert.deepEqual(packagedSmokeEnv(parent, "fixture"), {
    PATH: "system-path", DISPLAY: ":99", HOME: "system-home",
    DEEPZORA_HOME: "fixture", DEEPZORA_STATE_HOME: "fixture", DEEPZORA_CACHE_HOME: join("fixture", "cache"),
  });
  assert.equal(parent.DEEPZORA_DEV, "1", "the caller environment is not mutated");
});
