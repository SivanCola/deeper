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

test("notes pushes preserve required ancestor CI while code pushes cancel obsolete runs", () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "deepzora-ci-cancel-"));
  const git = (...args) => {
    const result = spawnSync("git", args, { cwd: dir, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    return result.stdout.trim();
  };
  try {
    git("init", "-q");
    git("config", "user.name", "test");
    git("config", "user.email", "test@example.invalid");
    const commit = (file, content) => {
      writeFileSync(path.join(dir, file), content);
      git("add", "."); git("commit", "-qm", "fixture");
      return git("rev-parse", "HEAD");
    };
    const old = commit("code", "old");
    const code = commit("code", "current");
    mkdirSync(path.join(dir, "release-notes"));
    const notes = commit("release-notes/record", "first");
    const head = commit("release-notes/record", "reviewed");
    const run = candidate => {
      const script = `set -euo pipefail
sleep() { :; }
gh() {
  if [ "$2" = "-X" ]; then printf '%s\\n' "$4" >> "$CANCEL_LOG";
  elif [[ "$2" == *workflows/ci.yml/runs* ]]; then printf '%s\\n' "$ACTIVE_RUNS";
  else echo completed; fi
}
${shellStep(job(workflow("supersede-ci"), "cancel-superseded"), "Cancel CI runs this push supersedes")}`;
      const log = path.join(dir, "cancel-log");
      writeFileSync(log, "");
      const result = spawnSync("bash", ["-c", script], { cwd: dir, encoding: "utf8", env: {
        ...process.env, GITHUB_REPOSITORY: "example/repo", GITHUB_SHA: candidate, CANCEL_LOG: log,
        ACTIVE_RUNS: `11 ${old}\n12 ${code}\n13 ${notes}\n14 ${head}`,
      } });
      assert.equal(result.status, 0, result.stderr);
      return readFileSync(log, "utf8").trim().split("\n");
    };
    assert.deepEqual(run(head), ["repos/example/repo/actions/runs/11/cancel"]);
    const next = commit("code", "next");
    assert.deepEqual(run(next), [11, 12, 13, 14].map(id => `repos/example/repo/actions/runs/${id}/cancel`));
    const group = ci.match(/  group: (ci-.+)/)[1];
    assert.match(group, /github.event_name == 'push' && github.sha \|\| github.ref/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("Certum signing survives skipped ancestor gates but requires successful inputs", () => {
  const body = job(release, "windows-sign");
  // A status function is required to override GitHub's implicit success(),
  // which otherwise propagates a skipped standalone/orchestrator ancestor.
  assert.match(body, /if:.*always\(\)/);
  const context = {
    needs: { resolve: { result: "success" }, "windows-build": { result: "success" }, "signing-contract": { result: "success" } },
    github: { repository: "SivanCola/deepzora" },
    inputs: { desktop_manual_only: false },
  };
  assert.equal(condition(body, context), true);
  assert.equal(condition(body, { ...context, cancelled: () => true }), false);
  for (const name of Object.keys(context.needs)) {
    for (const result of ["failure", "skipped", "cancelled"]) {
      assert.equal(condition(body, { ...context, needs: { ...context.needs, [name]: { result } } }), false);
    }
  }
  assert.equal(condition(body, { ...context, inputs: { desktop_manual_only: true } }), false);
  assert.equal(condition(body, { ...context, github: { repository: "example/fork" } }), false);
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

test("reuse skips only build work and still gates every publisher on validation", () => {
  const context = {
    inputs: { preflight_artifact_prefix: "desktop-123-1-preflight", orchestrated: true, signing_preflight_verified: true, signing_preflight: false, production_signing_smoke: false },
    needs: { resolve: { result: "success" }, "signing-contract": { result: "success" }, "mac-universal-intel": { result: "skipped" }, "windows-build": { result: "skipped" }, "windows-sign": { result: "skipped" }, "windows-runtime-acceptance": { result: "skipped" }, build: { result: "skipped" } },
  };
  assert.equal(condition(job(release, "build"), context), false);
  assert.equal(condition(job(release, "publish"), context), true);
  for (const key of ["resolve", "signing-contract", "mac-universal-intel", "windows-build", "windows-sign", "build"]) {
    for (const result of ["failure", "cancelled"]) {
      const changed = structuredClone(context);
      changed.needs[key].result = result;
      assert.equal(condition(job(release, "publish"), changed), false, `${key}=${result}`);
    }
  }
  for (const key of ["orchestrated", "signing_preflight_verified"]) {
    assert.equal(condition(job(release, "publish"), { ...context, inputs: { ...context.inputs, [key]: false } }), false);
  }
  for (const key of ["signing_preflight", "production_signing_smoke"]) {
    assert.equal(condition(job(release, "publish"), { ...context, inputs: { ...context.inputs, [key]: true } }), false);
  }
  const fresh = structuredClone(context);
  fresh.inputs.preflight_artifact_prefix = "";
  assert.equal(condition(job(release, "build"), fresh), true);
  assert.equal(condition(job(release, "publish"), fresh), false);
  fresh.needs.build.result = "success";
  fresh.needs["windows-build"].result = "success";
  fresh.needs["mac-universal-intel"].result = "success";
  assert.equal(condition(job(release, "publish"), fresh), false, "unsigned Windows bundles cannot publish");
  fresh.needs["windows-sign"].result = "success";
  assert.equal(condition(job(release, "publish"), fresh), false, "signed Windows installers must pass native runtime acceptance");
  fresh.needs["windows-runtime-acceptance"].result = "success";
  assert.equal(condition(job(release, "publish"), fresh), true);
});

test("Certum signing preserves native builds and gates publication and attestation", () => {
  const windowsBuild = job(release, "windows-build");
  const signer = job(release, "windows-sign");
  assert.match(windowsBuild, /runner: windows-latest, platform: windows\/amd64/);
  assert.match(windowsBuild, /runner: windows-11-arm, platform: windows\/arm64/);
  assert.match(windowsBuild, /Smoke-test packaged Electron startup/);
  assert.match(windowsBuild, /Upload Windows signing inputs/);
  assert.match(signer, /needs: \[resolve, windows-build, signing-contract\]/);
  assert.match(signer, /runs-on: windows-2022/);
  assert.match(signer, /ref: \$\{\{ github.workflow_sha \}\}/);
  assert.equal(signer.match(/setup-certum/g)?.length, 1, "both architectures share one Certum session");
  // Certum work stays in the one session; only the credential-free packaging
  // of the two architectures runs at the same time, between the two.
  const phases = [
    "Sign both payloads in the shared Certum session",
    "Package both architectures in parallel",
    "Seal amd64 in the shared Certum session",
    "Seal arm64 in the shared Certum session",
  ].map((name) => signer.indexOf(`name: ${name}`));
  assert.ok(phases.every((index) => index >= 0), "windows-sign names its sign, package and seal phases");
  assert.deepEqual([...phases].sort((a, b) => a - b), phases, "sign, package, then seal each architecture");
  for (const phase of ["sign", "package", "seal"]) {
    assert.match(signer, new RegExp(`FINALIZE_PHASE=${phase} bash `));
  }
  assert.ok(signer.indexOf("Seal amd64 in the shared Certum session")
    < signer.indexOf("name: ${{ needs.resolve.outputs.artifact_prefix }}-windows-amd64"));
  assert.ok(signer.indexOf("Seal arm64 in the shared Certum session")
    < signer.indexOf("name: ${{ needs.resolve.outputs.artifact_prefix }}-windows-arm64"));
  assert.ok(!release.includes("secrets.SIGNPATH_API_TOKEN"));
  const runtimeAcceptance = job(release, "windows-runtime-acceptance");
  assert.match(runtimeAcceptance, /runner: windows-latest, arch: amd64/);
  assert.match(runtimeAcceptance, /runner: windows-11-arm, arch: arm64/);
  assert.match(runtimeAcceptance, /test-windows-installer-startup\.ps1/);
  assert.match(runtimeAcceptance, /ExpectedVersion "\$\{\{ needs\.resolve\.outputs\.version \}\}"/);
  const attestation = job(release, "attest-signing-contract");
  assert.ok(!attestation.includes("gh api --method"), "GITHUB_TOKEN cannot mutate repository variables");
  assert.match(attestation, /uses: actions\/upload-artifact@[0-9a-f]{40} # v7\b/);
  assert.match(attestation, /verified-contract\.json/);
  assert.match(attestation, /gh variable set/);
  const context = { github: { repository: "SivanCola/deepzora" }, inputs: { signing_preflight: true, orchestrated: false },
    needs: { "signing-contract": { result: "success" }, build: { result: "success" }, "windows-build": { result: "success" }, "windows-sign": { result: "success" }, "windows-runtime-acceptance": { result: "success" } } };
  assert.equal(condition(attestation, context), true);
  for (const key of ["windows-build", "windows-sign", "windows-runtime-acceptance"]) {
    for (const result of ["failure", "cancelled", "skipped"]) {
      assert.equal(condition(attestation, { ...context, needs: { ...context.needs, [key]: { result } } }), false);
    }
  }
});

test("reuse never moves artifact verification past public mutation or trusts candidate scripts", () => {
  const publisher = job(release, "publish");
  assert.ok(publisher.indexOf("Verify complete signed artifact handoff") < publisher.indexOf("name: Publish GitHub release"));
  assert.ok(publisher.includes("node release-control/scripts/desktop-release-artifacts.mjs collect"));
  assert.ok(publisher.includes("ref: ${{ github.workflow_sha }}"));
  assert.ok(!publisher.includes("merge-multiple: true"));
  const stable = workflow("release-stable");
  assert.ok(job(stable, "desktop").includes("preflight_artifact_prefix: ${{ needs.signpath-preflight.outputs.artifact_prefix }}"));
  for (const name of ["desktop", "cli", "npm"]) assert.ok(job(stable, name).includes("needs: [authorize, signpath-preflight]"));
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
