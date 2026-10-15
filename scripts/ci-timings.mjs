#!/usr/bin/env node

import { readFileSync, appendFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

function milliseconds(start, end) {
  if (!start || !end) return null;
  return Math.max(0, Date.parse(end) - Date.parse(start));
}

function format(ms) {
  if (ms === null || !Number.isFinite(ms)) return "unknown / incomplete";
  const seconds = Math.round(ms / 1000);
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s`;
}

function stageKind(job, step) {
  if (step === "Resolve reviewed source") return "release control preflight";
  if (step === "Build each CLI binary once and package both surfaces") return "shared CLI/npm build";
  if (step === "Build and package") return "Desktop build";
  if (/^Finalize (amd64|arm64) in the shared Certum session$/.test(step)) return "Windows signing";
  if (step === "Install and smoke-test exact signed installer") return "Windows acceptance";
  if (step === "Run universal DMG smoke on Intel hardware") return "macOS acceptance";
  if (step === "Seal candidate record") return "candidate sealing";
  if (step === "Verify payload provenance, bytes, source, and operation") return "candidate verification";
  if (step === "Create or verify all implementation tags") return "release activation";
  if (step === "Verify public artifacts") return "public verification";
  if (/Install (frontend|memory) dependencies/.test(step)) return "dependency install";
  if (step === "Install browser runtimes") return "browser setup";
  if (/Build (stable|canary|memory) frontend/.test(step)) return "frontend build";
  if (step === "Build frontend once") return "frontend build";
  if (step === "Check transcript and approval behavior in Chromium") return "browser group";
  if (step === "Test desktop module" || step === "Build and test root module") return "Go test";
  if (/Windows desktop shard|desktop test shard/.test(step)) return "Go test shard";
  if (step === "Build and test every assigned root package") return "Go root build / test";
  if (/go-build-cache|cache\/(restore|save)|Cache/.test(step)) return "dependency cache";
  if (/go mod download|pnpm install|Install .*dependencies/.test(step)) return "dependency install";
  if (/^(Vet|Build) desktop module/.test(step)) return "Go compile / vet";
  if (/native build tags|Lint/.test(step)) return "lint";
  if (/upload-artifact|Upload/.test(step)) return "artifact upload";
  if (job.startsWith("shard (") && step === "Run complete independent memory process") return "memory shard";
  return null;
}

function executionTime(job) {
  // A cancelled queue entry can have timestamps without ever owning a runner.
  if (job.conclusion === "cancelled" && job.runner_id === 0) return 0;
  return milliseconds(job.started_at, job.completed_at);
}

export function timingReport(run, jobsPayload, { now = new Date(), dependencies = {}, historyJobs } = {}) {
  const allJobs = jobsPayload.jobs ?? [];
  const jobs = allJobs.filter(job => job.started_at && job.conclusion !== "skipped" && job.name !== "timings");
  const endTimes = jobs.map(job => job.completed_at).filter(Boolean).map(Date.parse);
  const workflowEnd = endTimes.length === jobs.length && jobs.length > 0 ? Math.max(...endTimes) : now.getTime();
  const workflowStart = Date.parse(run.created_at ?? run.run_started_at);
  const attemptStart = run.run_attempt > 1 ? Date.parse(run.run_started_at) : workflowStart;
  const rows = [];
  const stages = [];
  let runnerSum = 0;
  let queueSum = 0;
  let knownQueues = 0;
  let reusedJobs = 0;
  for (const job of jobs) {
    const execution = executionTime(job);
    // The latest-jobs API can stamp reused jobs with the new run_attempt.
    // Their actual start time, not that field, identifies old execution.
    const reused = Date.parse(job.started_at) < attemptStart;
    if (reused) reusedJobs++;
    const dependencyNames = dependencies[job.name] ?? dependencies[job.name.split(" (")[0]];
    const upstream = dependencyNames?.flatMap(name => allJobs.filter(candidate => candidate.name === name || candidate.name.startsWith(`${name} (`)));
    const ready = upstream?.length && upstream.every(candidate => candidate.completed_at)
      ? Math.max(attemptStart, ...upstream.map(candidate => Date.parse(candidate.completed_at)))
      : dependencyNames?.length === 0 ? attemptStart : null;
    // created_at can include dependency waiting. Only call a value queue time
    // when eligibility is known; otherwise preserve the observable pre-start gap.
    const created = job.created_at ? Date.parse(job.created_at) : null;
    const eligible = ready !== null && created !== null ? Math.max(ready, created) : ready;
    const queue = !reused && eligible !== null ? Math.max(0, Date.parse(job.started_at) - eligible) : null;
    if (!reused && execution !== null) runnerSum += execution;
    if (queue !== null) { queueSum += queue; knownQueues++; }
    rows.push({ name: job.name, queue, execution, reused,
      dependencyWait: reused || ready === null ? null : Math.max(0, ready - attemptStart),
      preStart: milliseconds(job.created_at, job.started_at), conclusion: job.conclusion ?? "running" });
    if (reused) continue;
    for (const step of job.steps ?? []) {
      const kind = stageKind(job.name, step.name);
      if (kind) stages.push({ kind, name: `${job.name} / ${step.name}`, duration: milliseconds(step.started_at, step.completed_at), conclusion: step.conclusion ?? "running" });
    }
  }
  const history = historyJobs ?? (run.run_attempt > 1 ? undefined : allJobs);
  // GitHub also assigns new job IDs to copied successful results. Match the
  // physical execution (name, runner and timestamps), not those clone IDs.
  const uniqueHistory = history && [...new Map(history.map(job => [JSON.stringify([job.name, job.runner_id, job.started_at, job.completed_at]), job])).values()];
  const totalRunnerSum = uniqueHistory ? uniqueHistory.filter(job => job.name !== "timings" && job.conclusion !== "skipped")
    .reduce((sum, job) => sum + (executionTime(job) ?? 0), 0) : null;
  return {
    workflowElapsed: Math.max(0, workflowEnd - workflowStart),
    attemptElapsed: Math.max(0, workflowEnd - attemptStart),
    attempt: run.run_attempt ?? 1,
    runnerSum,
    totalRunnerSum,
    reusedJobs,
    queueSum: knownQueues ? queueSum : null,
    unknownQueues: jobs.length - reusedJobs - knownQueues,
    requiredElapsed: requiredElapsed(run, allJobs),
    requiredAttemptElapsed: requiredElapsed({ ...run, created_at: new Date(attemptStart).toISOString() }, allJobs),
    rows: rows.sort((a, b) => a.name.localeCompare(b.name)),
    stages: stages.sort((a, b) => a.name.localeCompare(b.name)),
  };
}

const requiredContexts = ["lint", "root", "desktop", "race", "test (ubuntu-latest)", "test (macos-latest)", "test (windows-latest)"];
function requiredElapsed(run, jobs) {
  const gates = requiredContexts.map(name => jobs.find(job => job.name === name));
  if (gates.some(job => !job?.completed_at)) return null;
  return milliseconds(run.created_at ?? run.run_started_at,
    new Date(Math.max(...gates.map(job => Date.parse(job.completed_at)))).toISOString());
}

export function goTestTimings(log) {
  const topLevel = [], subtests = [], packages = [];
  const summaries = new Map();
  for (const line of log.split("\n")) {
    const start = line.indexOf('{"Time"');
    let event;
    try { event = JSON.parse(start >= 0 ? line.slice(start) : line); } catch { continue; }
    if (event.Action === "start") summaries.delete(event.Package);
    if (event.Action === "output" && !event.Test) {
      const match = event.Output?.match(/^ok\s+(\S+)\s+(\d+(?:\.\d+)?)s(?:\s|$)/);
      if (match?.[1] === event.Package) summaries.set(event.Package, { seconds: Number(match[2]), time: Date.parse(event.Time) });
    }
    if (!["pass", "fail", "skip"].includes(event.Action)) continue;
    const row = { package: event.Package, name: event.Test ?? "", result: event.Action, seconds: event.Elapsed ?? null };
    if (!event.Test) {
      const summary = summaries.get(event.Package), terminal = Date.parse(event.Time);
      row.testProcessSeconds = summary?.seconds ?? null;
      // This is an observed post-output gap, not proof of its cause. Missing
      // timestamps and cached/no-test results must not imply zero overhead.
      row.postResultSeconds = Number.isFinite(summary?.time) && Number.isFinite(terminal) && terminal >= summary.time
        ? (terminal - summary.time) / 1000 : null;
    }
    (event.Test ? event.Test.includes("/") ? subtests : topLevel : packages).push(row);
  }
  const sort = rows => rows.sort((a, b) => (b.seconds ?? 0) - (a.seconds ?? 0));
  return { topLevel: sort(topLevel), subtests: sort(subtests), packages: sort(packages) };
}

export function timingMarkdown(report, title = "CI timing") {
  const lines = [
    `## ${title}`,
    "",
    `- Total workflow wait: **${format(report.workflowElapsed)}**`,
    `- Required checks complete: **${format(report.requiredElapsed)}**`,
    `- Sum of runner execution: **${format(report.runnerSum)}**`,
    `- Runner execution across all attempts: **${format(report.totalRunnerSum)}**`,
    `- Current attempt wall time: **${format(report.attemptElapsed)}** (attempt ${report.attempt})`,
    `- Reused job results: **${report.reusedJobs}**`,
    `- Sum of recorded job queue time: **${format(report.queueSum)}**`,
    `- Jobs with unknown queue time: **${report.unknownQueues}**`,
    "",
    "Step durations exclude job queue time. Total workflow wait is wall time from workflow creation through the latest completed job. Runner execution and stage rows describe this attempt; reused rows retain their original execution duration and have no new queue time. Cumulative runner execution deduplicates copied results by job name, runner and execution timestamps, and excludes the timing collector.",
  ];
  if (report.stages.length) {
    lines.push("", "| Stage | Step | Execution | Result |", "| --- | --- | ---: | --- |");
    for (const stage of report.stages) lines.push(`| ${stage.kind} | ${stage.name} | ${format(stage.duration)} | ${stage.conclusion} |`);
  }
  lines.push("", "<details><summary>Job timing</summary>", "", "| Job | Dependency wait | Queue | Execution | Result | Reused |", "| --- | ---: | ---: | ---: | --- | --- |");
  for (const row of report.rows) lines.push(`| ${row.name} | ${format(row.dependencyWait)} | ${format(row.queue)} | ${format(row.execution)} | ${row.conclusion} | ${row.reused ? "yes" : "no"} |`);
  lines.push("", "</details>", "");
  return lines.join("\n");
}

function args(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 2) {
    if (!argv[i]?.startsWith("--") || argv[i + 1] === undefined) throw new Error(`invalid argument ${argv[i] ?? ""}`);
    out[argv[i].slice(2)] = argv[i + 1];
  }
  return out;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    const options = args(process.argv.slice(2));
    const run = JSON.parse(readFileSync(options.run, "utf8"));
    const jobs = JSON.parse(readFileSync(options.jobs, "utf8"));
    const historyJobs = options.history ? JSON.parse(readFileSync(options.history, "utf8")).jobs : undefined;
    const report = timingReport(run, jobs, { historyJobs });
    const markdown = timingMarkdown(report, options.title);
    if (options.output) writeFileSync(options.output, `${JSON.stringify(report, null, 2)}\n`);
    if (options.summary) appendFileSync(options.summary, markdown);
    else process.stdout.write(markdown);
  } catch (error) {
    console.error(`ci-timings: ${error.message}`);
    process.exitCode = 1;
  }
}
