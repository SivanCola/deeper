#!/usr/bin/env node
import assert from "node:assert/strict";
import { readdirSync, readFileSync, writeFileSync, appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { digest, verifyShards } from "./ci-go-shard-model.mjs";

const median = values => [...values].sort((a, b) => a - b)[Math.floor(values.length / 2)];
const distribution = values => ({ median: median(values), min: Math.min(...values), max: Math.max(...values) });
export function compareBenchmark(reports) {
  assert.equal(reports.length, 9, "three full runs and three shard pairs required");
  const baseline = reports[0], rows = [];
  for (const report of reports) {
    assert.equal(report.status, "success", "benchmark includes a failed run");
    for (const field of ["schemaVersion", "sourceSHA", "runId", "platform", "arch", "goVersion", "inventoryDigest", "packagesDigest", "cacheMode", "cacheSnapshot"]) {
      assert.equal(report[field], baseline[field], `benchmark ${field} differs`);
    }
    assert.equal(report.platform, "win32", "Windows measurements required");
    for (const field of ["compileMs", "testMs", "cleanupMs"]) {
      assert.ok(Number.isFinite(report.timings[field]) && report.timings[field] >= 0, `invalid ${field}`);
    }
    assert.ok(report.timings.testMs > 0, "test duration must be positive");
  }
  for (const sample of ["1", "2", "3"]) {
    const group = reports.filter(report => report.sample === sample);
    const full = group.filter(report => report.count === 1), shards = group.filter(report => report.count === 2);
    assert.equal(full.length, 1, `sample ${sample}: missing/duplicate full run`);
    verifyShards(shards, { sourceSHA: baseline.sourceSHA, runId: baseline.runId, platform: "win32" });
    assert.equal(full[0].inventoryDigest, digest(full[0].inventory), "full inventory digest mismatch");
    assert.equal(full[0].packagesDigest, digest(full[0].packages), "full package digest mismatch");
    assert.deepEqual(full[0].inventory, shards[0].inventory, "full and sharded inventories differ");
    assert.deepEqual(full[0].assigned, full[0].inventory, "full run selection is incomplete");
    assert.deepEqual(full[0].completed.map(row => row.name).sort(), full[0].inventory, "full run missed tests");
    assert.ok(full[0].completed.every(row => ["pass", "skip"].includes(row.result)), "full run failed");
    assert.equal(full[0].index, 1, "invalid full-run index");
    assert.deepEqual(full[0].otherPackages.map(row => row.package).sort(), full[0].packages.filter(pkg => pkg !== full[0].package), "full run missed other packages");
    assert.ok(full[0].otherPackages.every(row => ["pass", "skip"].includes(row.result)), "full run other package failed");
    const fullOutcomes = new Map(full[0].completed.map(row => [row.name, row.result]));
    assert.ok(shards.flatMap(report => report.completed).every(row => fullOutcomes.get(row.name) === row.result), "skip/pass outcomes differ");
    const elapsed = report => report.timings.compileMs + report.timings.testMs + report.timings.cleanupMs;
    rows.push({ sample, fullTestMs: full[0].timings.testMs, shardTestMs: Math.max(...shards.map(report => report.timings.testMs)),
      fullExecutionMs: elapsed(full[0]), shardExecutionMs: shards.reduce((sum, report) => sum + elapsed(report), 0),
      imbalance: Math.max(...shards.map(report => report.timings.testMs)) / Math.min(...shards.map(report => report.timings.testMs)) - 1 });
  }
  const full = distribution(rows.map(row => row.fullTestMs)), sharded = distribution(rows.map(row => row.shardTestMs));
  const reduction = 1 - sharded.median / full.median;
  const executionIncrease = median(rows.map(row => row.shardExecutionMs)) / median(rows.map(row => row.fullExecutionMs)) - 1;
  return { schemaVersion: 1, sourceSHA: baseline.sourceSHA, cacheMode: baseline.cacheMode ?? "unrecorded", cacheSnapshot: baseline.cacheSnapshot ?? "unrecorded", rows, full, sharded, reduction, executionIncrease,
    performanceTargetMet: reduction >= 0.35 && executionIncrease <= 0.25,
    note: "Execution costs include compile, tests and cleanup only. Use Actions job timings for complete runner costs and queue effects." };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const root = process.argv[2];
  const files = readdirSync(root, { recursive: true }).filter(file => file.endsWith(`${path.sep}report.json`));
  const report = compareBenchmark(files.map(file => JSON.parse(readFileSync(path.join(root, file), "utf8"))));
  writeFileSync(path.join(root, "comparison.json"), JSON.stringify(report, null, 2));
  const markdown = `## Windows shard comparison\n\nSource: \`${report.sourceSHA}\`\n\n` +
    `| Mode | Median test minutes | Range |\n| --- | ---: | --- |\n` +
    ["full", "sharded"].map(mode => `| ${mode} | ${(report[mode].median / 60000).toFixed(2)} | ${(report[mode].min / 60000).toFixed(2)}–${(report[mode].max / 60000).toFixed(2)} |`).join("\n") +
    `\n\nTest wall-time reduction: ${(report.reduction * 100).toFixed(1)}%. Compile/test/cleanup cost change: ${(report.executionIncrease * 100).toFixed(1)}%.\n\n${report.note}\n`;
  console.log(markdown);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown);
}
