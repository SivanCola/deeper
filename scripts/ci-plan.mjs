#!/usr/bin/env node
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import path from "node:path";

export function desktopPlan(flags) {
  return { schemaVersion: 1, build: flags.desktop, tests: flags.desktop, browser: flags.desktop, native: flags.native };
}

export function validateDesktopPlan(plan) {
  assert.deepEqual(Object.keys(plan).sort(), ["schemaVersion", "build", "tests", "browser", "native"].sort(), "invalid desktop plan fields");
  assert.equal(plan.schemaVersion, 1, "unsupported desktop plan");
  for (const field of ["build", "tests", "browser", "native"]) assert.equal(typeof plan[field], "boolean", `invalid ${field}`);
  assert.equal(plan.build, plan.tests, "tests must cover the built frontend");
  assert.equal(plan.build, plan.browser, "browser validation must cover the built frontend");
  assert.ok(!plan.native || plan.build, "native inputs require frontend validation");
  return plan;
}

export function verifyDesktopResults(plan, needs) {
  validateDesktopPlan(plan);
  assert.equal(needs.changes?.result, "success", "path detection must succeed");
  const owners = { "desktop-frontend": "build", "desktop-tests": "tests", "desktop-browser": "browser", "desktop-go": "native" };
  for (const [job, flag] of Object.entries(owners)) {
    assert.equal(needs[job]?.result, plan[flag] ? "success" : "skipped", `${job}: missing, failed, cancelled or unexpectedly skipped check`);
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    verifyDesktopResults(JSON.parse(process.env.DESKTOP_PLAN ?? ""), JSON.parse(process.env.CI_NEEDS ?? ""));
    console.log("Desktop execution plan is complete");
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
