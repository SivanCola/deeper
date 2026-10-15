#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync, openSync, writeSync, closeSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { digest, inventoryFromList, assignedTests, batches, completedTests } from "./ci-go-shard-model.mjs";

// Timeout/cancellation applies to the process tree, including helper executables.
export function execute(command, args, { cwd, deadline, log, json = false, env = process.env } = {}) {
  return new Promise((resolve, reject) => {
    const remaining = deadline - Date.now();
    if (remaining <= 0) return reject(new Error("shared test deadline exceeded"));
    const child = spawn(command, args, { cwd, env, shell: false, detached: process.platform !== "win32", stdio: ["ignore", "pipe", "pipe"] });
    const events = [];
    let partial = "", output = "", aborted, settled = false;
    const terminate = reason => {
      if (settled || aborted) return;
      aborted = new Error(reason);
      if (process.platform === "win32") {
        if (child.pid) spawn("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore", shell: false });
      } else if (child.pid) {
        try { process.kill(-child.pid, "SIGKILL"); } catch { child.kill("SIGKILL"); }
      }
    };
    const onSignal = () => terminate("test execution cancelled");
    process.on("SIGTERM", onSignal); process.on("SIGINT", onSignal);
    const timer = setTimeout(() => terminate("shared test deadline exceeded"), remaining);
    const progress = setInterval(() => console.log(`test process active (${events.filter(event => event.Action === "pass" && event.Test && !event.Test.includes("/")).length} top-level tests completed)`), 30_000);
    function consume(line) {
      if (!json) return;
      let event;
      try { event = JSON.parse(line); } catch { return; }
      // Output events can contain helper subprocess text; only the parent
      // test2json framing produces authoritative completion events.
      if (event.Action !== "output") events.push(event);
      if (event.Action === "fail") console.error(`FAIL ${event.Package ?? ""} ${event.Test ?? ""}`);
    }
    child.stdout.setEncoding("utf8"); child.stderr.setEncoding("utf8");
    child.stdout.on("data", chunk => {
      if (log !== undefined) writeSync(log, chunk);
      if (!json) output += chunk;
      else {
        partial += chunk;
        const lines = partial.split("\n"); partial = lines.pop();
        for (const line of lines) consume(line);
      }
    });
    child.stderr.on("data", chunk => { if (log !== undefined) writeSync(log, chunk); if (!json) output += chunk; });
    function finish(error, code) {
      if (settled) return;
      settled = true; clearTimeout(timer); clearInterval(progress);
      process.off("SIGTERM", onSignal); process.off("SIGINT", onSignal);
      if (partial) consume(partial);
      if (error || aborted || code !== 0) {
        const failure = error ?? aborted ?? new Error(`${command} exited ${code}; see raw test log`);
        failure.events = events;
        reject(failure);
      } else resolve({ output, events });
    }
    child.on("error", error => finish(error));
    child.on("close", code => finish(null, code));
  });
}

export async function runShard({ directory, output, index = 1, count = 2, timeoutMs = 35 * 60_000, identity = {} }) {
  const cwd = path.resolve(directory), destination = path.resolve(output);
  assert.ok(Number.isInteger(timeoutMs) && timeoutMs > 0 && timeoutMs <= 35 * 60_000, "invalid test budget");
  // Validate index/count before any compilation.
  assignedTests("validation", [], index, count);
  mkdirSync(destination, { recursive: true });
  const temporary = mkdtempSync(path.join(os.tmpdir(), "deepzora-shard-"));
  const log = openSync(path.join(destination, "tests.jsonl"), "w");
  const report = { schemaVersion: 1, sample: process.env.CI_BENCHMARK_SAMPLE, cacheMode: process.env.CI_BENCHMARK_CACHE_MODE ?? "restored", cacheSnapshot: process.env.CI_BENCHMARK_CACHE_SNAPSHOT ?? "", ...identity, index, count, status: "failure", completed: [], otherPackages: [], timings: {} };
  const events = [];
  let compileStartedAt, testsStartedAt;
  const query = args => execFileSync(args[0], args.slice(1), { cwd, encoding: "utf8", timeout: 60_000 }).trim();
  try {
    report.sourceSHA ??= query(["git", "rev-parse", "HEAD"]);
    report.runId ??= process.env.GITHUB_RUN_ID ?? "0";
    report.attempt ??= process.env.GITHUB_RUN_ATTEMPT ?? "1";
    report.platform = process.platform;
    report.arch = process.arch;
    report.goVersion = query(["go", "version"]);
    report.package = query(["go", "list", "."]);
    report.packages = query(["go", "list", "./..."]).split(/\r?\n/).sort();
    report.packagesDigest = digest(report.packages);
    const binary = path.join(temporary, process.platform === "win32" ? "desktop.test.exe" : "desktop.test");
    let start = Date.now();
    compileStartedAt = start;
    await execute("go", ["test", "-c", "-o", binary, "."], { cwd, log, deadline: start + 25 * 60_000 });
    report.timings.compileMs = Date.now() - start;
    start = Date.now();
    testsStartedAt = start;
    const deadline = start + timeoutMs;
    const listed = await execute(binary, ["-test.list=."], { cwd, log, deadline });
    report.inventory = inventoryFromList(listed.output);
    report.inventoryDigest = digest(report.inventory);
    report.assigned = assignedTests(report.package, report.inventory, index, count);
    assert.ok(report.assigned.length, "empty shard assignment");
    for (const group of count === 1 ? [null] : batches(report.assigned)) {
      const remaining = deadline - Date.now();
      assert.ok(remaining > 0, "shared test deadline exceeded");
      const result = await execute("go", ["tool", "test2json", "-t", "-p", report.package, binary,
        "-test.v=test2json", group ? `-test.run=^(${group.join("|")})$` : "-test.run=.", `-test.timeout=${remaining}ms`], { cwd, log, deadline, json: true });
      events.push(...result.events);
    }
    report.completed = completedTests(events, report.package, report.assigned);
    if (index === 1) {
      const others = report.packages.filter(pkg => pkg !== report.package);
      if (others.length) {
        const remaining = deadline - Date.now();
        assert.ok(remaining > 0, "shared test deadline exceeded");
        const result = await execute("go", ["test", "-json", "-count=1", `-timeout=${remaining}ms`, ...others], { cwd, log, deadline, json: true });
        report.otherPackages = result.events.filter(event => !event.Test && ["pass", "skip", "fail"].includes(event.Action))
          .map(event => ({ package: event.Package, result: event.Action })).sort((a, b) => a.package.localeCompare(b.package));
        assert.deepEqual(report.otherPackages.map(row => row.package), others, "missing other package results");
        assert.ok(report.otherPackages.every(row => row.result !== "fail"), "other package failed");
      }
    }
    report.timings.testMs = Date.now() - start;
    report.status = "success";
  } catch (error) {
    events.push(...(error.events ?? []));
    // Failed and interrupted runs retain completed outcomes for diagnosis.
    // They remain failed reports; this does not relax the coverage gate.
    report.completed = events.filter(event => event.Package === report.package && event.Test && !event.Test.includes("/") && ["pass", "fail", "skip"].includes(event.Action))
      .map(event => ({ name: event.Test, result: event.Action, seconds: event.Elapsed ?? 0 }));
    report.otherPackages = events.filter(event => event.Package !== report.package && !event.Test && ["pass", "fail", "skip"].includes(event.Action))
      .map(event => ({ package: event.Package, result: event.Action }));
    report.error = error.message;
    throw error;
  } finally {
    const start = Date.now();
    if (compileStartedAt !== undefined) report.timings.compileMs ??= (testsStartedAt ?? start) - compileStartedAt;
    if (testsStartedAt !== undefined) report.timings.testMs ??= start - testsStartedAt;
    closeSync(log);
    try { rmSync(temporary, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); }
    catch (error) { report.status = "failure"; report.error = `cleanup failed: ${error.message}`; }
    report.timings.cleanupMs = Date.now() - start;
    writeFileSync(path.join(destination, "report.json"), JSON.stringify(report, null, 2) + "\n");
  }
  assert.equal(report.status, "success", report.error);
  console.log(`shard ${index}/${count}: ${report.completed.length} top-level tests, ${report.otherPackages.length} other packages`);
  return report;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const args = Object.fromEntries(process.argv.slice(2).reduce((pairs, value, i, argv) => {
    if (i % 2 === 0) pairs.push([value.replace(/^--/, ""), argv[i + 1]]);
    return pairs;
  }, []));
  try {
    assert.ok(args.output, "--output is required");
    await runShard({ directory: args.directory ?? "desktop", output: args.output,
      index: Number(args.index ?? 1), count: Number(args.count ?? 2), timeoutMs: Number(args["timeout-ms"] ?? 35 * 60_000) });
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
