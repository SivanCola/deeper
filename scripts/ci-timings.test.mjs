import assert from "node:assert/strict";
import test from "node:test";
import { timingMarkdown, timingReport, goTestTimings } from "./ci-timings.mjs";

const stamp = seconds => new Date(Date.UTC(2026, 0, 1, 0, 0, seconds)).toISOString();

test("timing report separates queue, execution, wall time and selected stages", () => {
  const run = { created_at: stamp(0) };
  const jobs = { jobs: [
    { name: "desktop-frontend", created_at: stamp(1), started_at: stamp(6), completed_at: stamp(26), conclusion: "success", steps: [
      { name: "Install frontend dependencies", started_at: stamp(7), completed_at: stamp(12), conclusion: "success" },
      { name: "Build frontend once", started_at: stamp(12), completed_at: stamp(20), conclusion: "success" },
    ] },
    { name: "desktop-frontend browsers", created_at: stamp(2), started_at: stamp(12), completed_at: stamp(32), conclusion: "success", steps: [
      { name: "Check transcript and approval behavior in Chromium", started_at: stamp(20), completed_at: stamp(30), conclusion: "success" },
    ] },
    { name: "desktop-go (windows-latest)", created_at: stamp(3), started_at: stamp(4), completed_at: stamp(24), conclusion: "success", steps: [
      { name: "Test desktop module", started_at: stamp(8), completed_at: stamp(23), conclusion: "success" },
    ] },
    { name: "sign Windows candidate", created_at: stamp(3), started_at: stamp(4), completed_at: stamp(24), conclusion: "success", steps: [
      { name: "Finalize amd64 in the shared Certum session", started_at: stamp(8), completed_at: stamp(23), conclusion: "success" },
    ] },
    { name: "skipped", created_at: stamp(1), conclusion: "skipped", steps: [] },
  ] };
  const report = timingReport(run, jobs, { dependencies: Object.fromEntries(jobs.jobs.map(job => [job.name, []])) });
  assert.equal(report.workflowElapsed, 32_000);
  assert.equal(report.runnerSum, 80_000);
  assert.equal(report.queueSum, 17_000);
  assert.deepEqual(report.stages.map(stage => stage.kind), ["frontend build", "dependency install", "browser group", "Go test", "Windows signing"]);
  const markdown = timingMarkdown(report, "Measured CI timing");
  assert.match(markdown, /Total workflow wait: \*\*0m 32s\*\*/);
  assert.match(markdown, /Sum of runner execution: \*\*1m 20s\*\*/);
  assert.match(markdown, /Step durations exclude job queue time/);
});

test("unknown queue is not zero and upstream waiting is not queue time", () => {
  const jobs = { jobs: [
    { name: "build", created_at: stamp(0), started_at: stamp(5), completed_at: stamp(20), conclusion: "success" },
    { name: "consumer", created_at: stamp(0), started_at: stamp(23), completed_at: stamp(30), conclusion: "success" },
  ] };
  const unknown = timingReport({ created_at: stamp(0) }, jobs);
  assert.equal(unknown.queueSum, null);
  assert.equal(unknown.unknownQueues, 2);
  const report = timingReport({ created_at: stamp(0) }, jobs, { dependencies: { build: [], consumer: ["build"] } });
  assert.equal(report.rows[1].dependencyWait, 20_000);
  assert.equal(report.rows[1].queue, 3_000);
  assert.equal(report.queueSum, 8_000);
});

test("test timing does not count child duration again as top-level work", () => {
  const log = [
    { Action: "pass", Package: "example", Test: "TestParent", Elapsed: 5 },
    { Action: "pass", Package: "example", Test: "TestParent/child", Elapsed: 4 },
    { Action: "pass", Package: "example", Elapsed: 6 },
  ].map(event => JSON.stringify(event)).join("\n");
  const report = goTestTimings(log);
  assert.equal(report.topLevel.length, 1);
  assert.equal(report.subtests.length, 1);
  assert.equal(report.packages.length, 1);
  assert.equal(report.topLevel[0].seconds, 5);
});

test("running jobs use the observation time for wall time", () => {
  const report = timingReport({ created_at: stamp(0) }, { jobs: [
    { name: "app-memory", created_at: stamp(1), started_at: stamp(2), completed_at: null, conclusion: null, steps: [] },
  ] }, { now: new Date(stamp(45)) });
  assert.equal(report.workflowElapsed, 45_000);
  assert.equal(report.runnerSum, 0);
});

test("partial reruns distinguish reused results from current and cumulative execution", () => {
  const build = { id: 1, name: "build", run_attempt: 2, started_at: stamp(1), completed_at: stamp(20), conclusion: "success" };
  const oldConsumer = { id: 2, name: "consumer", started_at: stamp(25), completed_at: stamp(30), conclusion: "success" };
  const consumer = { id: 3, name: "consumer", started_at: stamp(45), completed_at: stamp(50), conclusion: "success" };
  const neverStarted = { id: 4, name: "cancelled", runner_id: 0, started_at: stamp(1), completed_at: stamp(40), conclusion: "cancelled" };
  const report = timingReport({ created_at: stamp(0), run_started_at: stamp(40), run_attempt: 2 }, { jobs: [build, consumer] }, {
    dependencies: { build: [], consumer: ["build"] }, historyJobs: [build, oldConsumer, consumer, { ...build, id: 5 }, neverStarted],
  });
  assert.equal(report.workflowElapsed, 50_000);
  assert.equal(report.attemptElapsed, 10_000);
  assert.equal(report.runnerSum, 5_000);
  assert.equal(report.totalRunnerSum, 29_000);
  assert.equal(report.reusedJobs, 1);
  assert.equal(report.rows[0].queue, null);
  assert.equal(report.rows[1].queue, 5_000);
  assert.equal(report.rows[1].dependencyWait, 0);
  assert.equal(report.unknownQueues, 0);
});

test("rerun gate wall time retains workflow creation and separately records the attempt", () => {
  const names = ["lint", "root", "desktop", "race", "test (ubuntu-latest)", "test (macos-latest)", "test (windows-latest)"];
  const jobs = names.map(name => ({ name, started_at: stamp(name === "desktop" ? 45 : 10), completed_at: stamp(name === "desktop" ? 50 : 20), conclusion: "success" }));
  const report = timingReport({ created_at: stamp(0), run_started_at: stamp(40), run_attempt: 2 }, { jobs });
  assert.equal(report.requiredElapsed, 50_000);
  assert.equal(report.requiredAttemptElapsed, 10_000);
  assert.equal(report.totalRunnerSum, null, "missing historical jobs cannot imply a zero cumulative cost");
  assert.equal(report.reusedJobs, 6);
});
