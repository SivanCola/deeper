#!/usr/bin/env node
import { readdirSync, readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { verifyShards } from "./ci-go-shard-model.mjs";

const root = process.argv[2];
if (!root) throw new Error("usage: node scripts/ci-go-shard-verify.mjs REPORT_DIRECTORY");
const files = readdirSync(root, { recursive: true }).filter(name => name === "report.json" || name.endsWith(`${path.sep}report.json`));
const reports = files.map(file => JSON.parse(readFileSync(path.join(root, file), "utf8")));
const result = verifyShards(reports, { sourceSHA: execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim(),
  runId: process.env.GITHUB_RUN_ID, platform: "win32", arch: "x64", package: "deepzora/desktop", currentAttempt: process.env.GITHUB_RUN_ATTEMPT });
console.log(JSON.stringify(result));
