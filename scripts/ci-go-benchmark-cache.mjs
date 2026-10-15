#!/usr/bin/env node
// Benchmark-only reader of existing main caches. Production cache policy stays
// in go-build-cache; a benchmark must never fall back to a newer snapshot.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function benchmarkCacheIdentity({ mode, snapshot, dependencyHash, workspace, buildDirectory }) {
  assert.ok(["restored", "cold"].includes(mode), "invalid benchmark cache mode");
  assert.match(dependencyHash, /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/, "invalid dependency identity");
  if (mode === "restored") assert.match(snapshot, /^[1-9]\d*-[1-9]\d*$/, "restored mode requires cache_snapshot=MAIN_RUN_ID-ATTEMPT");
  else assert.equal(snapshot, "", "cold mode must not specify a cache snapshot");
  const prefix = `go-desktop-${dependencyHash.slice(0, 16)}`;
  return {
    mod_key: `${prefix}-mod-${snapshot}`,
    build_key: `${prefix}-build-Windows-X64-${snapshot}`,
    mod_paths: ".go-mod-cache",
    build_paths: buildDirectory.replaceAll("\\", "/"),
    gomodcache: `${workspace.replaceAll("\\", "/").replace(/\/$/, "")}/.go-mod-cache`,
  };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  assert.equal(process.platform, "win32", "Windows benchmark runner required");
  assert.equal(process.arch, "x64", "Windows x64 cache required");
  const dependency = execFileSync("git", ["hash-object", "desktop/go.sum"], { encoding: "utf8" });
  const dependencyHash = execFileSync("git", ["hash-object", "--stdin"], { input: dependency, encoding: "utf8" }).trim();
  const identity = benchmarkCacheIdentity({ mode: process.env.CI_BENCHMARK_CACHE_MODE,
    snapshot: process.env.CI_BENCHMARK_CACHE_SNAPSHOT ?? "", dependencyHash,
    workspace: process.env.GITHUB_WORKSPACE,
    buildDirectory: execFileSync("go", ["env", "GOCACHE"], { encoding: "utf8" }).trim() });
  for (const [key, value] of Object.entries(identity)) {
    if (key !== "gomodcache") appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
  }
  // Cold mode keeps setup-go's ordinary module-cache location.
  if (process.env.CI_BENCHMARK_CACHE_MODE === "restored") appendFileSync(process.env.GITHUB_ENV, `GOMODCACHE=${identity.gomodcache}\n`);
}
