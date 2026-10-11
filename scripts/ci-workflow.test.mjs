import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";
import test from "node:test";

const workflow = name => readFileSync(new URL(`../.github/workflows/${name}.yml`, import.meta.url), "utf8");
function job(source, name) {
  const body = source.match(new RegExp(`\\n  ${name}:\\n([\\s\\S]*?)(?=\\n  [a-z][a-z0-9-]*:|$)`))?.[1];
  assert.ok(body, name);
  return body;
}
function condition(body, context) {
  const expression = body.match(/^    if: (.+)$/m)[1].replace(/^\$\{\{\s*|\s*\}\}$/g, "")
    .replace(/needs\.([a-z][a-z0-9-]*)/g, 'needs["$1"]');
  return vm.runInNewContext(expression, { always: () => true, cancelled: () => false, ...context });
}
function shellStep(body, name) {
  return body.split(`      - name: ${name}\n`)[1].match(/        run: \|\n((?:          .*\n|\n)+)/)[1]
    .replace(/^          /gm, "");
}
const ci = workflow("ci");
const release = workflow("release-desktop");
const promote = workflow("release-promote");
const appMemory = workflow("app-memory");

test("App memory artifacts support rerunning only failed shards", () => {
  const prepare = job(appMemory, "prepare");
  const shard = job(appMemory, "shard");
  const aggregate = job(appMemory, "app-memory");
  const upload = body => body.match(/uses: actions\/upload-artifact@[^\n]+\n        (?:if: [^\n]+\n        )?with:\n          name: ([^\n]+)([\s\S]*?)(?=\n      - |$)/);
  const download = body => [...body.matchAll(/uses: actions\/download-artifact@[^\n]+\n        (?:if: [^\n]+\n        )?with:\n          (name|pattern): ([^\n]+)/g)];
  const render = (name, attempt, shardId = 1) => name
    .replaceAll("${{ github.run_id }}", "123")
    .replaceAll("${{ github.run_attempt }}", String(attempt))
    .replaceAll("${{ matrix.shard }}", String(shardId));
  const build = upload(prepare);
  const shardReport = upload(shard);
  const shardBuild = download(shard)[0][2];
  const [reports, aggregateBuild] = download(aggregate).map(match => match[2]);
  assert.ok(build && shardReport && shardBuild && reports && aggregateBuild);
  assert.match(build[2], /\n          overwrite: true\n/);
  assert.match(shardReport[2], /\n          overwrite: true\n/);
  assert.equal(render(build[1], 1), render(shardBuild, 2));
  assert.equal(render(build[1], 1), render(aggregateBuild, 2));
  for (const shardId of [1, 2, 3]) {
    assert.equal(render(shardReport[1], shardId === 1 ? 2 : 1, shardId),
      render(reports, 2).replaceAll("*", String(shardId)));
  }
});
test("frontend artifact workflows share one exact Node runtime", () => {
  const version = readFileSync(new URL("../.node-version", import.meta.url), "utf8").trim();
  assert.match(version, /^\d+\.\d+\.\d+$/);
  for (const [name, source] of [["ci", ci], ["app-memory", appMemory], ["release-desktop", release]]) {
    assert.doesNotMatch(source, /node-version: ["']?24(?:["']|\s)/, name);
    assert.match(source, /node-version-file: \.node-version/, name);
  }
  const names = [...ci.matchAll(/^  ([a-z][a-z0-9-]*):$/gm)].map(match => match[1]);
  const participants = names.map(name => [name, job(ci, name)])
    .filter(([, body]) => /artifact-identity\.mjs (create|verify)/.test(body));
  assert.ok(participants.length > 1, "cover both producer and consumers");
  for (const [name, body] of participants) {
    assert.match(body, /uses: actions\/setup-node@[^\n]+\n\s+with:\n\s+node-version-file: \.node-version/, name);
    assert.doesNotMatch(body, /node-version:/, `${name} must not override the shared runtime`);
  }
  for (const name of ["prepare", "shard", "app-memory"]) {
    const body = job(appMemory, name);
    assert.match(body, /node-version-file: \.node-version/, name);
    assert.doesNotMatch(body, /node-version:/, name);
  }
});

test("release tag mutation follows approval with an explicit identity and read-only default token", () => {
  assert.match(job(promote, "preflight"), /permissions:\n      actions: read\n      attestations: read\n      contents: read/);
  assert.match(job(promote, "authorize"), /environment: release[\s\S]*permissions:\n      contents: read/);
  const activation = job(promote, "activate");
  assert.match(activation, /needs: \[preflight, authorize\]/);
  assert.match(activation, /permissions:\n      contents: read/);
  assert.match(activation, /persist-credentials: false/);
  assert.match(activation, /GH_TOKEN: \$\{\{ secrets.RELEASE_TAG_TOKEN \}\}/);
  assert.match(activation, /RELEASE_TAG_ACTOR_ID: \$\{\{ needs.preflight.outputs.tag_actor_id \}\}/);
  assert.doesNotMatch(job(promote, "preflight"), /release-candidate-tags.sh activate|git push/);
});



test("manual App memory screening retains full protocol and rejects missing evidence", () => {
  assert.match(appMemory, /workflow_dispatch:/);
  assert.doesNotMatch(appMemory, /schedule:|pull_request:|push:/);
  assert.match(appMemory, /echo 'profile=full'/);
  assert.match(appMemory, /echo 'shards=\[1,2,3\]'/);
  assert.match(appMemory, /if-no-files-found: error/);
  assert.doesNotMatch(appMemory, /continue-on-error/);
});

test("macOS signing diagnostics require protected main and cannot publish", () => {
  const source = workflow("macos-signing-check");
  const verify = job(source, "verify");
  const github = { repository: "SivanCola/deepzora", ref: "refs/heads/main", ref_protected: true };
  assert.equal(condition(verify, { github }), true);
  for (const changed of [{ repository: "fork/Deepzora" }, { ref: "refs/tags/v1.0.0" }, { ref_protected: false }]) {
    assert.equal(condition(verify, { github: { ...github, ...changed } }), false);
  }
  assert.match(verify, /environment: release/);
  assert.match(verify, /ref: \$\{\{ github.sha \}\}/);
  assert.match(source, /permissions:\n  contents: read\n/);
  assert.doesNotMatch(source, /: write|secrets\.(R2_|SIGNPATH_|MINISIGN_|NPM_)/);
  assert.match(verify, /HAS_APPLE_CERT: "true"/);
  assert.match(verify, /scripts\/desktop-build.sh darwin\/universal v0.0.0-signing-check stable/);
  assert.match(verify, /path: \$\{\{ runner.temp \}\}\/apple-notarization\/\*\.json/);
  assert.match(verify, /if: always\(\)/);
});

test("every ci job is reachable from a required aggregate", () => {
  const required = ["test", "race", "lint", "desktop", "root"];
  const jobs = ci.slice(ci.indexOf("\njobs:\n")); // `on:` also nests two-space keys
  const names = [...jobs.matchAll(/^ {2}([a-z][a-z0-9-]*):$/gm)].map(match => match[1]);
  assert.equal(names.length, 8, "keep the compact CI baseline");
  // A bracketed list may wrap across lines, so consume up to its closing ].
  const edges = new Map(names.map(name => [name, (job(ci, name).match(/^ {4}needs:\s*(\[[^\]]*\]|\S.*)$/m)?.[1] ?? "")
    .replace(/[[\]]/g, "").split(",").map(entry => entry.trim()).filter(Boolean)]));
  const reachable = new Set(required);
  for (const name of required) for (const dependency of edges.get(name) ?? []) reachable.add(dependency);
  for (let size = 0; size !== reachable.size;) {
    size = reachable.size;
    for (const name of [...reachable]) for (const dependency of edges.get(name) ?? []) reachable.add(dependency);
  }
  for (const name of names) {
    if (reachable.has(name)) continue;
    assert.fail(`${name} is gated by no required check`);
  }
});




test("installer evidence excludes running payloads and cache files on every publisher", () => {
  for (const body of [job(release, "windows-runtime-acceptance")]) {
    const upload = body.match(/name: Upload (?:signed )?Windows installer acceptance evidence\n([\s\S]*?)(?=\n      - |$)/)?.[1];
    assert.ok(upload);
    for (const extension of ["json", "png", "log"])
      assert.ok(upload.includes(`deepzora-installer-acceptance/**/*.${extension}`));
    for (const excluded of ["installed/**", "**/cache/**"])
      assert.ok(upload.includes(`!\${{ runner.temp }}/deepzora-installer-acceptance/${excluded}`));
  }
});
