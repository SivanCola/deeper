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
  assert.equal(reports.length, 9, "require three complete paired samples");
  const first = reports[0];
  assert.match(first.benchmark.buildCacheKey, /^go-root-[a-f0-9]{16}-build-Windows-X64-[1-9]\d*-[1-9]\d*$/);
  for (const r of reports) {
    assert.deepEqual(r.inventory, first.inventory, "workload changed");
    for (const key of ["sourceSHA", "runId", "goVersion", "platform", "arch"]) assert.equal(r[key], first[key]);
    assert.equal(r.benchmark.buildCacheKey, first.benchmark.buildCacheKey, "cache changed");
    assert.equal(r.benchmark.moduleCache, "fresh-download");
    assert.equal(r.parallelism, r.count === 1 ? "auto" : 2, "baseline must retain the original default package parallelism");
    assert.ok([1, 2, 3].includes(r.benchmark.sample));
    for (const key of ["buildMs", "testMs"]) assert.ok(Number.isFinite(r.timings[key]) && r.timings[key] >= 0);
  }
  const pairs = [1, 2, 3].map(sample => {
    const group = reports.filter(r => r.benchmark.sample === sample);
    const full = group.filter(r => r.count === 1), split = group.filter(r => r.count === 2);
    const expected = { sourceSHA: first.sourceSHA, runId: first.runId, platform: "win32", arch: "x64",
      currentAttempt: Math.max(...group.map(r => Number(r.attempt))) };
    verifyRootReports(full, { ...expected, count: 1 });
    verifyRootReports(split, { ...expected, count: 2 });
    const states = rows => rows.flatMap(r => r.completed).sort((a, b) => a.package.localeCompare(b.package));
    assert.deepEqual(states(full), states(split), "package pass/skip states differ");
    const tests = rows => rows.flatMap(r => {
      assert.ok(Array.isArray(r.testOutcomes), "missing top-level evidence");
      return r.testOutcomes;
    }).sort(([a], [b]) => a.localeCompare(b));
    const fullTests = tests(full), splitTests = tests(split);
    assert.ok(fullTests.length > 0, "empty top-level evidence");
    assert.equal(new Set(splitTests.map(([name]) => name)).size, splitTests.length, "overlapping test outcomes");
    assert.deepEqual(fullTests, splitTests, "top-level test pass/skip states differ");
    const elapsed = r => r.timings.buildMs + r.timings.testMs;
    return { sample, fullMs: elapsed(full[0]), splitMs: Math.max(...split.map(elapsed)),
      partitionMs: split.sort((a, b) => a.index - b.index).map(elapsed) };
  });
  const stats = values => ({ median: [...values].sort((a, b) => a - b)[1], min: Math.min(...values), max: Math.max(...values) });
  const full = stats(pairs.map(p => p.fullMs)), split = stats(pairs.map(p => p.splitMs));
  return { schemaVersion: 1, sourceSHA: first.sourceSHA, buildCacheKey: first.benchmark.buildCacheKey,
    baselineParallelism: "auto", optimizedParallelism: 2,
    moduleCache: "fresh-download", packages: first.inventory.length, pairs, full, split,
    reduction: 1 - split.median / full.median,
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
