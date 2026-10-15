import assert from "node:assert/strict";
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { verifyRootReports } from "./ci-root-shard.mjs";

export function rootTestOutcomes(jsonl, assigned) {
  const started = new Set(), completed = new Map();
  for (const line of jsonl.split(/\r?\n/).filter(Boolean)) {
    const event = JSON.parse(line);
    if (!event.Test || event.Test.includes("/")) continue;
    assert.ok(assigned.includes(event.Package), "test outside assigned packages");
    const key = `${event.Package}\0${event.Test}`;
    if (event.Action === "run") {
      assert.ok(!started.has(key), "duplicate top-level test");
      started.add(key);
    }
    if (["pass", "fail", "skip"].includes(event.Action)) {
      assert.ok(started.has(key) && !completed.has(key), "invalid test terminal event");
      assert.notEqual(event.Action, "fail", "failed top-level test");
      completed.set(key, event.Action);
    }
  }
  assert.equal(completed.size, started.size, "unfinished top-level test");
  return [...completed].sort(([a], [b]) => a.localeCompare(b));
}

export function compareRootReports(reports) {
  assert.equal(reports.length, 6, "require three complete paired samples");
  const first = reports[0];
  assert.match(first.benchmark.buildCacheKey, /^go-root-[a-f0-9]{16}-build-Windows-X64-[1-9]\d*-[1-9]\d*$/);
  for (const r of reports) {
    assert.deepEqual(r.inventory, first.inventory, "workload changed");
    for (const key of ["sourceSHA", "runId", "goVersion", "platform", "arch"]) assert.equal(r[key], first[key]);
    assert.equal(r.benchmark.buildCacheKey, first.benchmark.buildCacheKey, "cache changed");
    assert.equal(r.benchmark.moduleCache, "fresh-download");
    assert.ok(["baseline", "optimized"].includes(r.benchmark.mode), "invalid benchmark mode");
    assert.equal(r.count, 1, "each sample must execute the complete module");
    assert.equal(r.parallelism, r.benchmark.mode === "baseline" ? "auto" : 2, "incorrect package parallelism");
    assert.equal(r.testResultCache, r.benchmark.mode === "baseline" ? "enabled" : "disabled", "incorrect test-result cache mode");
    assert.ok([1, 2, 3].includes(r.benchmark.sample));
    for (const key of ["buildMs", "testMs"]) assert.ok(Number.isFinite(r.timings[key]) && r.timings[key] >= 0);
  }
  const pairs = [1, 2, 3].map(sample => {
    const group = reports.filter(r => r.benchmark.sample === sample);
    const baseline = group.filter(r => r.benchmark.mode === "baseline"), optimized = group.filter(r => r.benchmark.mode === "optimized");
    const expected = { sourceSHA: first.sourceSHA, runId: first.runId, platform: "win32", arch: "x64",
      currentAttempt: Math.max(...group.map(r => Number(r.attempt))) };
    verifyRootReports(baseline, { ...expected, count: 1 });
    verifyRootReports(optimized, { ...expected, count: 1 });
    const states = rows => rows.flatMap(r => r.completed).sort((a, b) => a.package.localeCompare(b.package));
    assert.deepEqual(states(baseline), states(optimized), "package pass/skip states differ");
    const tests = rows => rows.flatMap(r => {
      assert.ok(Array.isArray(r.testOutcomes), "missing top-level evidence");
      return r.testOutcomes;
    }).sort(([a], [b]) => a.localeCompare(b));
    const baselineTests = tests(baseline), optimizedTests = tests(optimized);
    assert.ok(baselineTests.length > 0, "empty top-level evidence");
    for (const outcomes of [baselineTests, optimizedTests]) {
      assert.equal(new Set(outcomes.map(([name]) => name)).size, outcomes.length, "duplicate test outcomes");
    }
    assert.deepEqual(baselineTests, optimizedTests, "top-level test pass/skip states differ");
    const elapsed = r => r.timings.buildMs + r.timings.testMs;
    return { sample, baselineMs: elapsed(baseline[0]), optimizedMs: elapsed(optimized[0]), tests: baselineTests.length };
  });
  const stats = values => ({ median: [...values].sort((a, b) => a - b)[1], min: Math.min(...values), max: Math.max(...values) });
  const baseline = stats(pairs.map(p => p.baselineMs)), optimized = stats(pairs.map(p => p.optimizedMs));
  return { schemaVersion: 2, sourceSHA: first.sourceSHA, buildCacheKey: first.benchmark.buildCacheKey,
    baselineParallelism: "auto", optimizedParallelism: 2,
    baselineTestResultCache: "enabled", optimizedTestResultCache: "disabled",
    moduleCache: "fresh-download", packages: first.inventory.length, pairs, baseline, optimized,
    reduction: 1 - optimized.median / baseline.median,
    note: "Build+test wall time only. Complete runner cost and required-gate time must come from Actions job timestamps." };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const directory = process.argv[2];
  const reports = readdirSync(directory, { withFileTypes: true }).filter(e => e.isDirectory())
    .map(e => {
      const folder = path.join(directory, e.name);
      const report = JSON.parse(readFileSync(path.join(folder, "report.json"), "utf8"));
      report.testOutcomes = rootTestOutcomes(readFileSync(path.join(folder, "tests.jsonl"), "utf8"), report.assigned);
      return report;
    });
  const result = compareRootReports(reports);
  writeFileSync(path.join(directory, "comparison.json"), JSON.stringify(result, null, 2) + "\n");
  console.log(JSON.stringify(result, null, 2));
}
