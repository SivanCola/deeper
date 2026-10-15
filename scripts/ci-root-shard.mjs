import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, openSync, closeSync, writeFileSync, readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execute } from "./ci-go-shard.mjs";
import { digest } from "./ci-go-shard-model.mjs";

export function assignPackages(packages, index, count) {
  assert.ok([1, 2].includes(count) && Number.isInteger(index) && index >= 1 && index <= count, "invalid partition");
  assert.ok(packages.length && packages.every(p => typeof p === "string" && /^[\w./-]+$/.test(p)), "invalid package inventory");
  assert.deepEqual(packages, [...new Set(packages)].sort(), "inventory must be sorted and unique");
  return packages.filter(p => createHash("sha256").update(p).digest().readUInt32BE(0) % count === index - 1);
}

export function packageResults(events, assigned) {
  const rows = events.filter(e => !e.Test && ["pass", "fail", "skip"].includes(e.Action))
    .map(e => ({ package: e.Package, result: e.Action })).sort((a, b) => a.package.localeCompare(b.package));
  assert.deepEqual(rows.map(r => r.package).sort(), assigned, "missing, duplicate or unexpected package result");
  assert.ok(rows.every(r => r.result !== "fail"), "package failed");
  return rows;
}

export function packageBatches(packages, limit = 12_000) {
  const groups = []; let group = [], size = 100;
  for (const pkg of packages) {
    assert.ok(pkg.length + 100 < limit, "package name exceeds command budget");
    if (size + pkg.length + 1 > limit) { groups.push(group); group = []; size = 100; }
    group.push(pkg); size += pkg.length + 1;
  }
  if (group.length) groups.push(group);
  return groups;
}

export function verifyRootReports(reports, { count, currentAttempt, ...identity }) {
  assert.ok([1, 2].includes(count), "unsupported partition count");
  assert.equal(reports.length, count, "missing or duplicate reports");
  const sorted = [...reports].sort((a, b) => a.index - b.index), first = sorted[0];
  for (const [i, r] of sorted.entries()) {
    assert.equal(r.schemaVersion, 1, "unsupported root report version");
    assert.equal(r.index, i + 1, "missing or duplicate index");
    assert.equal(r.count, count, "partition count mismatch");
    assert.equal(r.status, "success", "failed or unfinished partition");
    assert.equal(r.buildCompleted, true, "build did not complete");
    for (const [key, value] of Object.entries(identity)) assert.equal(r[key], value, `${key} mismatch`);
    for (const key of ["sourceSHA", "runId", "platform", "arch", "goVersion", "inventoryDigest"]) assert.equal(r[key], first[key], `${key} differs`);
    assert.match(r.sourceSHA, /^[0-9a-f]{40}$/);
    assert.match(String(r.runId), /^\d+$/);
    assert.match(String(r.attempt), /^[1-9]\d*$/);
    assert.ok(r.goVersion && r.platform && r.arch, "missing toolchain identity");
    assert.ok(Number(r.attempt) <= Number(currentAttempt), "future producer attempt");
    assert.equal(r.inventoryDigest, digest(r.inventory), "inventory digest mismatch");
    assert.deepEqual(r.buildInventory, first.buildInventory, "build inventory differs");
    assert.deepEqual(r.buildInventory, [...new Set(r.buildInventory)].sort(), "invalid build inventory");
    assert.ok(r.buildInventory.every(p => r.inventory.includes(p)), "unexpected build package");
    assert.deepEqual(r.assigned, assignPackages(r.inventory, r.index, count), "incorrect assignment");
    assert.ok(r.assigned.length, "empty assignment");
    assert.deepEqual(r.completed.map(x => x.package).sort(), r.assigned, "incomplete package coverage");
    assert.ok(r.completed.every(x => ["pass", "skip"].includes(x.result)), "invalid terminal state");
  }
  assert.deepEqual(sorted.flatMap(r => r.assigned).sort(), first.inventory, "overlapping or incomplete union");
  return { packages: first.inventory.length, attempts: sorted.map(r => r.attempt) };
}

export async function runRootShard({ directory = ".", output, index, count }) {
  const cwd = path.resolve(directory), destination = path.resolve(output);
  mkdirSync(destination, { recursive: true });
  const query = args => execFileSync(args[0], args.slice(1), { cwd, encoding: "utf8", timeout: 60_000 }).trim();
  const r = { schemaVersion: 1, status: "failure", index, count, buildCompleted: false, completed: [], timings: {},
    sourceSHA: query(["git", "rev-parse", "HEAD"]), runId: process.env.GITHUB_RUN_ID ?? "0", attempt: process.env.GITHUB_RUN_ATTEMPT ?? "1",
    platform: process.platform, arch: process.arch, goVersion: query(["go", "version"]),
    benchmark: process.env.CI_ROOT_BENCHMARK_SAMPLE ? { sample: Number(process.env.CI_ROOT_BENCHMARK_SAMPLE),
      buildCacheKey: process.env.CI_ROOT_BENCHMARK_BUILD_KEY, moduleCache: "fresh-download" } : null };
  const log = openSync(path.join(destination, "tests.jsonl"), "w");
  // The workflow still has its existing 35-minute limit, including setup and
  // both PowerShell checks. Every Go package retains its 15-minute test limit.
  const deadline = Date.now() + 30 * 60_000;
  try {
    const packages = query(["go", "list", "-f", "{{.ImportPath}} {{if or .GoFiles .CgoFiles}}build{{else}}test-only{{end}}", "./..."])
      .split(/\r?\n/).map(line => line.split(" "));
    r.inventory = packages.map(([name]) => name).sort();
    // go build ./... excludes packages containing only test files. Explicit
    // package arguments do not, so preserve that behavior without dropping
    // those packages from go test or the coverage report.
    r.buildInventory = packages.filter(([, kind]) => kind === "build").map(([name]) => name).sort();
    r.inventoryDigest = digest(r.inventory);
    r.assigned = assignPackages(r.inventory, index, count);
    assert.ok(r.assigned.length, "empty assignment");
    let start = Date.now();
    const groups = packageBatches(r.assigned);
    for (const group of packageBatches(r.assigned.filter(p => r.buildInventory.includes(p)))) {
      await execute("go", ["build", ...group], { cwd, log, deadline });
    }
    r.timings.buildMs = Date.now() - start;
    r.buildCompleted = true;
    start = Date.now();
    const events = [];
    for (const group of groups) {
      const result = await execute("go", ["test", "-json", "-timeout=15m", ...group], { cwd, log, deadline, json: true });
      events.push(...result.events);
    }
    r.timings.testMs = Date.now() - start;
    r.completed = packageResults(events, r.assigned);
    r.status = "success";
  } catch (error) {
    r.error = error.message;
    r.completed = (error.events ?? []).filter(e => !e.Test && ["pass", "fail", "skip"].includes(e.Action))
      .map(e => ({ package: e.Package, result: e.Action }));
    throw error;
  } finally {
    closeSync(log);
    writeFileSync(path.join(destination, "report.json"), JSON.stringify(r, null, 2) + "\n");
  }
  console.log(`root partition ${index}/${count}: ${r.completed.length} packages built and tested`);
  return r;
}

function reportsUnder(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(e => e.isDirectory() ? reportsUnder(path.join(directory, e.name))
    : e.name === "report.json" ? [JSON.parse(readFileSync(path.join(directory, e.name), "utf8"))] : []);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    const [command, ...args] = process.argv.slice(2);
    if (command === "verify") {
      console.log(JSON.stringify(verifyRootReports(reportsUnder(args[0]), { count: Number(args[1]), currentAttempt: process.env.GITHUB_RUN_ATTEMPT,
        sourceSHA: process.env.GITHUB_SHA, runId: process.env.GITHUB_RUN_ID, platform: "win32", arch: "x64" })));
    } else {
      assert.equal(command, "run", "expected run or verify");
      assert.equal(args.length, 3, "expected INDEX COUNT OUTPUT");
      await runRootShard({ index: Number(args[0]), count: Number(args[1]), output: args[2] });
    }
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
