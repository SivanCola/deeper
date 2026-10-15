#!/usr/bin/env node
// Read-only Actions evidence collector. Logs are parsed as data, never executed.
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync, appendFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { timingReport, timingMarkdown, goTestTimings } from "./ci-timings.mjs";

const options = Object.fromEntries(process.argv.slice(2).reduce((pairs, value, i, args) => {
  if (i % 2 === 0) pairs.push([value.replace(/^--/, ""), args[i + 1]]);
  return pairs;
}, []));
const repository = options.repository ?? process.env.GITHUB_REPOSITORY;
if (!/^[\w.-]+\/[\w.-]+$/.test(repository ?? "") || !/^\d+$/.test(options.run ?? "")) throw new Error("--repository OWNER/REPO and --run ID are required");
const output = path.resolve(options.output ?? "ci-evidence");
mkdirSync(output, { recursive: true });
const api = (endpoint, extra = []) => execFileSync("gh", ["api", `repos/${repository}/${endpoint}`, ...extra], { encoding: "utf8", maxBuffer: 256 * 1024 * 1024 });
const run = JSON.parse(api(`actions/runs/${options.run}`));
const pages = JSON.parse(api(`actions/runs/${options.run}/jobs?filter=latest&per_page=100`, ["--paginate", "--slurp"]));
const jobs = { jobs: pages.flatMap(page => page.jobs) };
const history = run.run_attempt > 1
  ? { jobs: JSON.parse(api(`actions/runs/${options.run}/jobs?filter=all&per_page=100`, ["--paginate", "--slurp"])).flatMap(page => page.jobs) }
  : jobs;
writeFileSync(path.join(output, "run.json"), JSON.stringify(run, null, 2));
writeFileSync(path.join(output, "jobs.json"), JSON.stringify(jobs, null, 2));
writeFileSync(path.join(output, "jobs-all-attempts.json"), JSON.stringify(history, null, 2));
// Parse only the small needs subset of our workflow; no YAML expressions execute.
const workflow = options.workflow ? readFileSync(options.workflow, "utf8")
  : api(`contents/.github/workflows/ci.yml?ref=${run.head_sha}`, ["-H", "Accept: application/vnd.github.raw+json"]);
writeFileSync(path.join(output, "ci.yml"), workflow);
const dependencies = {};
for (const [, name, body] of workflow.matchAll(/^  ([\w-]+):\n([\s\S]*?)(?=^  [\w-]+:|$(?![\s\S]))/gm)) {
  const needs = body.match(/^    needs:\s*(\[[^\]]*\]|[^\n]+)/m)?.[1] ?? "";
  dependencies[name] = needs.replace(/[\[\]]/g, "").split(",").map(value => value.trim()).filter(Boolean);
  const displayName = body.match(/^    name: ([^\n]+)$/m)?.[1];
  if (displayName && !displayName.includes("${{")) dependencies[displayName] = dependencies[name];
}
const report = timingReport(run, jobs, { dependencies, historyJobs: history.jobs });
let markdown = timingMarkdown(report);
const warnings = [];
const logFlags = execFileSync("gh", ["api", "--help"], { encoding: "utf8" }).includes("--allow-escape-sequences") ? ["--allow-escape-sequences"] : [];
for (const job of jobs.jobs.filter(job => /^(desktop-go|windows-root)/.test(job.name) && job.completed_at && job.conclusion !== "skipped")) {
  try {
    const log = api(`actions/jobs/${job.id}/logs`, logFlags);
    writeFileSync(path.join(output, `${job.id}.log`), log);
    const timings = goTestTimings(log);
    writeFileSync(path.join(output, `${job.id}-tests.json`), JSON.stringify(timings, null, 2));
    markdown += `\n### ${job.name}: slowest top-level tests\n\n| Seconds | Result | Test |\n| ---: | --- | --- |\n`;
    for (const row of timings.topLevel.slice(0, 30)) markdown += `| ${row.seconds ?? "unknown"} | ${row.result} | ${row.name} |\n`;
    markdown += "\n| Package | Event duration | Test process | After successful output |\n| --- | ---: | ---: | ---: |\n";
    for (const row of timings.packages) markdown += `| ${row.package} | ${row.seconds ?? "unknown"} | ${row.testProcessSeconds ?? "unknown"} | ${row.postResultSeconds ?? "unknown"} |\n`;
  } catch (error) { warnings.push(`${job.name}: ${error.message}`); }
}
if (jobs.jobs.some(job => /^windows-root(?: \(|$)/.test(job.name) && job.conclusion !== "skipped")) {
  try {
    const directory = path.join(output, "root-partitions");
    execFileSync("gh", ["run", "download", String(run.id), "--repo", repository,
      "--pattern", `windows-root-partition-*-${run.id}`, "--dir", directory], { encoding: "utf8" });
    for (const file of readdirSync(directory, { recursive: true }).filter(file => file.endsWith(`${path.sep}tests.jsonl`))) {
      const timings = goTestTimings(readFileSync(path.join(directory, file), "utf8"));
      writeFileSync(path.join(directory, `${file}.timings.json`), JSON.stringify(timings, null, 2));
      markdown += `\n### ${file}: root package execution (seconds)\n\n| Package | Event duration | Test process | After successful output | Result |\n| --- | ---: | ---: | ---: | --- |\n`;
      for (const row of timings.packages.slice(0, 30)) markdown += `| ${row.package} | ${row.seconds ?? "unknown"} | ${row.testProcessSeconds ?? "unknown"} | ${row.postResultSeconds ?? "unknown"} | ${row.result} |\n`;
    }
  } catch (error) { warnings.push(`Root partition timings: ${error.message}`); }
}
if (jobs.jobs.some(job => job.name.includes("windows-latest, shard"))) {
  try {
    const directory = path.join(output, "windows-shards");
    execFileSync("gh", ["run", "download", String(run.id), "--repo", repository,
      "--pattern", `windows-desktop-shard-*-${run.id}`, "--dir", directory], { encoding: "utf8" });
    const reports = readdirSync(directory, { recursive: true }).filter(file => file.endsWith(`${path.sep}report.json`))
      .map(file => JSON.parse(readFileSync(path.join(directory, file), "utf8")));
    markdown += "\n### Windows shards: slowest top-level tests\n\n| Seconds | Result | Test |\n| ---: | --- | --- |\n";
    for (const row of reports.flatMap(report => report.completed).sort((a, b) => b.seconds - a.seconds).slice(0, 30)) {
      markdown += `| ${row.seconds} | ${row.result} | ${row.name} |\n`;
    }
    const durations = reports.map(report => report.timings.testMs).filter(Number.isFinite);
    if (durations.length === 2) {
      const imbalance = Math.max(...durations) / Math.min(...durations) - 1;
      markdown += `\nShard duration imbalance: ${(imbalance * 100).toFixed(1)}%${imbalance > 0.25 ? " (investigate test contributions; do not automatically add runners)" : ""}.\n`;
    }
  } catch (error) { warnings.push(`Windows shard timings: ${error.message}`); }
}
writeFileSync(path.join(output, "timings.json"), JSON.stringify({ ...report, warnings }, null, 2));
writeFileSync(path.join(output, "summary.md"), markdown);
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, markdown);
process.stdout.write(markdown);
for (const warning of warnings) console.error(warning);
