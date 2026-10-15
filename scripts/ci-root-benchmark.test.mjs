import assert from "node:assert/strict";
import test from "node:test";
import { compareRootReports } from "./ci-root-benchmark.mjs";
import { assignPackages } from "./ci-root-shard.mjs";
import { digest } from "./ci-go-shard-model.mjs";

function cohort() {
  const inventory = Array.from({ length: 20 }, (_, i) => `example/p${i}`).sort();
  return [1, 2, 3].flatMap(sample => [[1, 1], [1, 2], [2, 2]].map(([index, count]) => {
    const assigned = assignPackages(inventory, index, count);
    return { schemaVersion: 1, sourceSHA: "a".repeat(40), runId: "42", attempt: "1", platform: "win32", arch: "x64",
      goVersion: "go1.26", index, count, inventory, buildInventory: inventory, inventoryDigest: digest(inventory), assigned,
      completed: assigned.map(p => ({ package: p, result: "pass" })), status: "success", buildCompleted: true,
      timings: { buildMs: 20, testMs: count === 1 ? sample * 100 : sample * 40 },
      benchmark: { sample, buildCacheKey: "go-root-1234567890abcdef-build-Windows-X64-123-1", moduleCache: "fresh-download" } };
  }));
}
test("three paired root samples retain ranges and verify all package outcomes", () => {
  const r = compareRootReports(cohort());
  assert.deepEqual(r.full, { median: 220, min: 120, max: 320 });
  assert.deepEqual(r.split, { median: 100, min: 60, max: 140 });
  assert.equal(r.packages, 20);
});
test("incomplete, failed, mismatched or mixed-cache cohorts cannot qualify", () => {
  for (const change of [r => r.pop(), r => { r[0].status = "failure"; }, r => { r[2].benchmark.sample = 2; },
    r => { r[1].benchmark.buildCacheKey += "x"; }, r => { r[0].timings.testMs = NaN; },
    r => { r[1].sourceSHA = "b".repeat(40); }, r => { r[1].completed[0].result = "skip"; }]) {
    const reports = cohort(); change(reports); assert.throws(() => compareRootReports(reports));
  }
});
