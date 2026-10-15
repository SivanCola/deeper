import assert from "node:assert/strict";
import test from "node:test";
import { compareRootReports, rootTestOutcomes } from "./ci-root-benchmark.mjs";
import { assignPackages, testBatches } from "./ci-root-shard.mjs";
import { digest } from "./ci-go-shard-model.mjs";

function cohort() {
  const inventory = Array.from({ length: 20 }, (_, i) => `example/p${i}`).sort();
  return [1, 2, 3].flatMap(sample => ["baseline", "optimized"].map(mode => {
    const index = 1, count = 1, parallelism = mode === "baseline" ? "auto" : 2;
    const assigned = assignPackages(inventory, index, count);
    return { schemaVersion: 2, sourceSHA: "a".repeat(40), runId: "42", attempt: "1", platform: "win32", arch: "x64",
      goVersion: "go1.26", index, count, parallelism, testResultCache: mode === "baseline" ? "enabled" : "disabled", inventory, buildInventory: inventory, inventoryDigest: digest(inventory), assigned,
      testBatches: testBatches(assigned, parallelism),
      completed: assigned.map(p => ({ package: p, result: "pass" })), status: "success", buildCompleted: true,
      testOutcomes: assigned.map(p => [`${p}\0TestExample`, "pass"]),
      timings: { buildMs: 20, testMs: mode === "baseline" ? sample * 100 : sample * 40 },
      benchmark: { sample, mode, buildCacheKey: "go-root-1234567890abcdef-build-Windows-X64-123-1", moduleCache: "fresh-download" } };
  }));
}
test("three paired root samples retain ranges and verify all package outcomes", () => {
  const r = compareRootReports(cohort());
  assert.deepEqual(r.baseline, { median: 220, min: 120, max: 320 });
  assert.deepEqual(r.optimized, { median: 100, min: 60, max: 140 });
  assert.equal(r.packages, 20);
});
test("incomplete, failed, mismatched or mixed-cache cohorts cannot qualify", () => {
  for (const change of [r => r.pop(), r => { r[0].status = "failure"; }, r => { r[2].benchmark.sample = 1; },
    r => { r[0].benchmark.mode = "optimized"; }, r => { r[1].testResultCache = "enabled"; },
    r => { r[0].count = 2; },
    r => { r[1].benchmark.buildCacheKey += "x"; }, r => { r[0].timings.testMs = NaN; },
    r => { r[1].sourceSHA = "b".repeat(40); }, r => { r[1].completed[0].result = "skip"; }, r => { r[0].parallelism = 2; },
    r => { r[0].testOutcomes[0][1] = "skip"; }, r => { r[0].testOutcomes.pop(); }, r => { delete r[0].testOutcomes; }]) {
    const reports = cohort(); change(reports); assert.throws(() => compareRootReports(reports));
  }
});

test("benchmark evidence requires complete top-level events and ignores child accounting", () => {
  const event = (Action, Test = "TestParent", Package = "example/p") => ({ Action, Test, Package });
  const jsonl = rows => rows.map(r => JSON.stringify(r)).join("\n");
  const rows = [event("run"), event("run", "TestParent/child"), event("pass", "TestParent/child"), event("pass")];
  assert.deepEqual(rootTestOutcomes(jsonl(rows), ["example/p"]), [["example/p\0TestParent", "pass"]]);
  for (const invalid of [[event("run")], [event("pass")], [event("run"), event("fail")],
    [...rows, ...rows], [event("run", "TestOther", "other")]]) {
    assert.throws(() => rootTestOutcomes(jsonl(invalid), ["example/p"]));
  }
  assert.throws(() => rootTestOutcomes("not JSON", ["example/p"]));
});
