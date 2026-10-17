import assert from "node:assert/strict";
import test from "node:test";
import { existsSync } from "node:fs";
import { discoveredTests, testPlan } from "./ci-test-plan.mjs";

test("every discovered suite runs exactly once with asset hooks", () => {
  const discovered = discoveredTests();
  const plan = testPlan({}, discovered);
  assert.equal(new Set(plan.map(entry => entry.key)).size, plan.length);
  for (const file of discovered) {
    const entry = plan.find(entry => entry.key === `src/__tests__/${file}`);
    assert.ok(entry, file);
    assert.ok(entry.args.includes("./scripts/svg-stub-register.mjs"));
  }
  for (const entry of plan) if (entry.key.startsWith("src/")) assert.ok(existsSync(entry.key), entry.key);
  assert.ok(plan.find(entry => entry.key.endsWith("history-performance-benchmark.tsx"))?.serial);
});

test("groups remain selectable and unknown groups fail closed", () => {
  assert.ok(testPlan({}, [], "motion").some(entry => entry.key.endsWith("native-motion.test.tsx")));
  assert.ok(testPlan({}, [], "transcript").some(entry => entry.key.endsWith("transcript-tool-identity.test.ts")));
  assert.throws(() => testPlan({}, [], "missing"), /unknown test group/);
});
