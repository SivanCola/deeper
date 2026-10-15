import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { spawnSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";
import test from "node:test";
import { classifyPaths } from "./ci-paths.mjs";

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
const goCache = readFileSync(new URL("../.github/actions/go-build-cache/action.yml", import.meta.url), "utf8");

test("Windows root gate requires valid detection, planned success and exact report coverage", () => {
  const gate = job(ci, "test-windows"), worker = job(ci, "windows-root");
  const script = shellStep(gate, "Require successful detection and planned execution");
  for (const changes of ["success", "failure", "cancelled", "skipped", ""]) {
    for (const code of ["true", "false", ""]) {
      for (const result of ["success", "failure", "cancelled", "skipped", ""]) {
        const run = spawnSync("bash", ["-c", `set -e\n${script}`], { env: { ...process.env, CHANGES_RESULT: changes, CODE: code, ROOT_RESULT: result } });
        assert.equal(run.status === 0, changes === "success" && (code === "true" && result === "success" || code === "false" && result === "skipped"));
      }
    }
  }
  assert.doesNotMatch(worker, /matrix|PARTITION_COUNT/);
  assert.match(worker, /ci-root-shard.mjs run 1 1/);
  assert.match(worker, /@\('pwsh', 'powershell.exe'\)/);
  assert.match(worker, /go test -count=1 -timeout=2m -run '\^TestPowerShell'/);
  assert.ok(worker.indexOf("name: Test both Windows PowerShell runtimes") < worker.indexOf("ci-root-shard.mjs run"));
  assert.match(gate, /ci-root-shard.mjs verify root-partitions 1/);
  assert.doesNotMatch(gate + worker, /continue-on-error/);
});

test("desktop disables only Windows test-result caching and retains complete suites and deadlines", () => {
  const script = shellStep(job(ci, "desktop-go"), "Test desktop module");
  for (const [RUNNER_OS, DESKTOP_TEST_TIMEOUT] of [["Windows", "35m"], ["Linux", "15m"], ["macOS", "15m"]]) {
    const result = spawnSync("bash", ["-c", `set -e\ngo() { printf '%s\\n' "$@"; }\n${script}`], {
      env: { ...process.env, RUNNER_OS, DESKTOP_TEST_TIMEOUT }, encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(result.stdout.trim().split("\n"), ["test", `-timeout=${DESKTOP_TEST_TIMEOUT}`, "-json",
      ...(RUNNER_OS === "Windows" ? ["-count=1"] : []), "./..."]);
  }
});

test("manual benchmark requires exact cache hits without changing production cache restores", () => {
  const benchmark = workflow("ci-windows-benchmark");
  assert.match(benchmark, /cache_snapshot:/);
  assert.match(benchmark, /node scripts\/ci-go-benchmark-cache\.mjs/);
  const restores = benchmark.split(/\n      - /).filter(step => step.includes("uses: actions/cache/restore@"));
  assert.equal(restores.length, 2);
  for (const step of restores) {
    assert.match(step, /fail-on-cache-miss: true/);
    assert.doesNotMatch(step, /restore-keys:/);
  }
  assert.match(benchmark, /MODULE_HIT: \$\{\{ steps\.module-cache\.outputs\.cache-hit \}\}/);
  assert.match(benchmark, /BUILD_HIT: \$\{\{ steps\.build-cache\.outputs\.cache-hit \}\}/);
  const verify = benchmark.match(/run: (test "\$MODULE_HIT"[^\n]+)/)[1];
  for (const moduleHit of ["true", "false", ""]) {
    for (const buildHit of ["true", "false", ""]) {
      const result = spawnSync("bash", ["-c", verify], { env: { ...process.env, MODULE_HIT: moduleHit, BUILD_HIT: buildHit } });
      assert.equal(result.status === 0, moduleHit === "true" && buildHit === "true");
    }
  }
  assert.doesNotMatch(benchmark, /cache\/save@/);
  assert.match(goCache, /restore-keys: \$\{\{ steps\.key\.outputs\.mod_restore \}\}/);
});

test("actual workflow conditions implement frontend-only and native execution plans", () => {
  for (const [file, frontend, native] of [
    ["README.md", false, false], ["desktop/frontend/src/components/Button.tsx", true, false],
    ["desktop/frontend/src/lib/bridge.ts", true, true], ["desktop/app.go", true, true],
    [".github/actions/go-build-cache/action.yml", true, true], ["unknown/input", true, true],
  ]) {
    const { plan } = classifyPaths([file]);
    const context = { fromJSON: JSON.parse, needs: {
      changes: { result: "success", outputs: { desktop_plan: JSON.stringify(plan) } },
      "desktop-frontend": { result: frontend ? "success" : "skipped" },
    } };
    for (const name of ["desktop-frontend", "desktop-tests", "desktop-browser"]) {
      assert.equal(Boolean(condition(job(ci, name), context)), frontend, `${file}: ${name}`);
    }
    assert.equal(Boolean(condition(job(ci, "desktop-go"), context)), native, file);
  }
  const broken = { fromJSON: JSON.parse, needs: { changes: { result: "failure", outputs: {} } } };
  for (const name of ["desktop-frontend", "desktop-tests", "desktop-go"]) {
    assert.equal(condition(job(ci, name), broken), true, name);
  }
  assert.equal(condition(job(ci, "desktop"), { cancelled: () => true }), true, "aggregate still checks upstream cancellation");
});
const cacheSteps = source => source.split(/\n    - /).filter(step => step.includes("uses: actions/cache/"));
const cacheGate = (step, context) => vm.runInNewContext(step.match(/if: ([^\n]+)/)[1], {
  always: () => true, success: () => false, ...context,
});

test("workflows pin setup-go to the retrievable Node 24 release", () => {
  const expectedPin = "924ae3a1cded613372ab5595356fb5720e22ba16";
  const workflowDirectory = new URL("../.github/workflows/", import.meta.url);
  const pins = readdirSync(workflowDirectory)
    .filter(name => name.endsWith(".yml"))
    .flatMap(name => [...workflow(name.slice(0, -".yml".length)).matchAll(/actions\/setup-go@([a-f0-9]{40})/g)]
      .map(match => match[1]));
  assert.ok(pins.length > 0, "workflows must set up Go");
  assert.deepEqual(new Set(pins), new Set([expectedPin]));
});

test("module archives share a portable version while build archives stay per-platform", () => {
  const steps = cacheSteps(goCache);
  assert.equal(steps.length, 4);
  for (const step of steps) {
    if (step.includes("outputs.mod_paths")) assert.match(step, /enableCrossOsArchive: true/);
    else assert.doesNotMatch(step, /enableCrossOsArchive: true/);
  }
  const moduleSave = steps.find(step => step.includes("cache/save@") && step.includes("outputs.mod_paths"));
  const buildSave = steps.find(step => step.includes("cache/save@") && step.includes("outputs.build_paths"));
  for (const event_name of ["push", "pull_request", "workflow_dispatch"]) {
    for (const runnerOs of ["Linux", "macOS", "Windows"]) {
      for (const save_mod_os of ["Linux", ""]) {
        const context = { inputs: { mode: "save", save_mod_os }, github: { event_name }, runner: { os: runnerOs } };
        assert.equal(cacheGate(moduleSave, context), event_name === "push" && runnerOs === save_mod_os);
        assert.equal(cacheGate(buildSave, context), event_name === "push");
        assert.equal(cacheGate(moduleSave, { ...context, inputs: { ...context.inputs, mode: "restore" } }), false);
      }
    }
  }
});

test("cache keys and Go consumers use the same module directory from nested jobs", t => {
  const root = mkdtempSync(path.join(os.tmpdir(), "deepzora-go-cache-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(path.join(root, "desktop"));
  mkdirSync(path.join(root, "sdk/go"), { recursive: true });
  mkdirSync(path.join(root, "bin"));
  writeFileSync(path.join(root, "desktop/go.sum"), "fixture dependency identity\n");
  writeFileSync(path.join(root, "sdk/go/go.mod"), "module example.com/cache-consumer\n\ngo 1.26\n");
  const nativeRoot = root.replaceAll("\\", "/");
  writeFileSync(path.join(root, "bin/cygpath"), '#!/usr/bin/env bash\n[ "$1" = -am ] || exit 1\nprintf "%s\\n" "$2" >> "$CYGPATH_LOG"\nprintf "%s\\n" "${CYGPATH_RESULT:-$2}"\n', { mode: 0o755 });
  const script = goCache.match(/      run: \|\n([\s\S]*?)(?=\n    - )/)[1].replace(/^        /gm, "");
  let identity;
  for (const runnerOs of ["Linux", "macOS", "Windows"]) {
    for (const directory of ["desktop", "sdk/go"]) {
      const output = path.join(root, `output-${runnerOs}-${directory.replaceAll("/", "-")}`);
      const environment = `${output}-env`;
      const values = { "inputs.dependency": "desktop/go.sum", "inputs.module": "desktop", "inputs.build_module": "desktop-contract", "github.run_id": "123", "github.run_attempt": "1", "runner.os": runnerOs, "runner.arch": "X64" };
      const run = () => spawnSync("bash", ["-c", script.replace(/\$\{\{ ([^}]+) \}\}/g, (_, key) => {
        assert.ok(key in values, key);
        return values[key];
      })], { cwd: path.join(root, directory), encoding: "utf8", env: {
        ...process.env, PATH: `${path.join(root, "bin")}${path.delimiter}${process.env.PATH}`,
        GITHUB_WORKSPACE: root, GITHUB_OUTPUT: output, GITHUB_ENV: environment,
        GOCACHE: path.join(root, "build"), CYGPATH_LOG: `${output}-cygpath`,
      } });
      for (const mode of ["restore", "save"]) {
        const result = run();
        assert.equal(result.status, 0, `${runnerOs} ${directory} ${mode}: ${result.stderr}`);
      }
      const outputs = Object.fromEntries(readFileSync(output, "utf8").trim().split("\n").map(line => line.split("=")));
      assert.equal(outputs.mod_paths, ".go-mod-cache");
      assert.equal(outputs.build_paths, `${nativeRoot}/build`);
      assert.match(outputs.build_key, new RegExp(`^go-desktop-contract-[a-f0-9]{16}-build-${runnerOs}-X64-123-1$`));
      identity ??= [outputs.mod_key, outputs.mod_restore, outputs.mod_paths];
      assert.deepEqual([outputs.mod_key, outputs.mod_restore, outputs.mod_paths], identity);
      const gomodcache = readFileSync(environment, "utf8").trim().split("\n").at(-1).slice("GOMODCACHE=".length);
      assert.equal(gomodcache, `${nativeRoot}/.go-mod-cache`);
      if (runnerOs === "Windows") assert.deepEqual(readFileSync(`${output}-cygpath`, "utf8").trim().split("\n"), [gomodcache, gomodcache]);
      const consumed = spawnSync("go", ["env", "GOMODCACHE"], { cwd: path.join(root, directory), encoding: "utf8", env: { ...process.env, GOMODCACHE: gomodcache } });
      assert.equal(consumed.status, 0, consumed.stderr);
      assert.equal(consumed.stdout.trim().replaceAll("\\", "/"), gomodcache);
    }
  }
  const windowsWorkspace = "D:\\a\\deepzora\\deepzora";
  const windowsCache = "D:/a/deepzora/deepzora/.go-mod-cache";
  const windowsOutput = path.join(root, "windows-output");
  const windowsScript = script.replace(/\$\{\{ ([^}]+) \}\}/g, (_, key) => ({
    "inputs.dependency": "desktop/go.sum", "inputs.module": "desktop", "inputs.build_module": "desktop-contract",
    "github.run_id": "123", "github.run_attempt": "1", "runner.os": "Windows", "runner.arch": "X64",
  })[key]).replace('cd "$GITHUB_WORKSPACE"', 'cd "$FIXTURE_WORKSPACE"');
  const windowsRun = spawnSync("bash", ["-c", windowsScript], { cwd: root, encoding: "utf8", env: {
    ...process.env, PATH: `${path.join(root, "bin")}${path.delimiter}${process.env.PATH}`, FIXTURE_WORKSPACE: root,
    GITHUB_WORKSPACE: windowsWorkspace, GITHUB_OUTPUT: windowsOutput, GITHUB_ENV: `${windowsOutput}-env`,
    GOCACHE: path.join(root, "build"), CYGPATH_LOG: `${windowsOutput}-cygpath`, CYGPATH_RESULT: windowsCache,
  } });
  assert.equal(windowsRun.status, 0, windowsRun.stderr);
  assert.equal(readFileSync(`${windowsOutput}-env`, "utf8").trim(), `GOMODCACHE=${windowsCache}`);
  assert.equal(readFileSync(`${windowsOutput}-cygpath`, "utf8").trim(), windowsCache);
  assert.match(readFileSync(windowsOutput, "utf8"), /^mod_paths=\.go-mod-cache$/m);
  const ignored = spawnSync("git", ["check-ignore", ".go-mod-cache/example/go.mod"], {
    cwd: new URL("..", import.meta.url), encoding: "utf8",
  });
  assert.equal(ignored.status, 0, ignored.stderr);
  const download = spawnSync("go", ["mod", "download"], { cwd: path.join(root, "sdk/go"), encoding: "utf8", env: {
    ...process.env, GOMODCACHE: `${nativeRoot}/.go-mod-cache`, GOPROXY: "off", GOSUMDB: "off",
  } });
  assert.equal(download.status, 0, download.stderr);
});

test("each CI module namespace has one writer and skipped checks never save", () => {
  const moduleSave = cacheSteps(goCache).find(step => step.includes("cache/save@") && step.includes("outputs.mod_paths"));
  const owners = new Map();
  for (const name of ["test", "root", "race", "desktop-frontend", "desktop-go", "lint"]) {
    const body = job(ci, name);
    const steps = body.split(/\n      - /).filter(step => step.includes("uses: ./.github/actions/go-build-cache"));
    assert.equal(steps.length, 2, name);
    assert.ok(body.indexOf("uses: actions/setup-go@") < body.indexOf("uses: ./.github/actions/go-build-cache"), name);
    const parsed = steps.map(step => Object.fromEntries([...step.matchAll(/^          ([a-z_]+): (.+)$/gm)].map(([, key, value]) => [key, value.replace(/^['"]|['"]$/g, "")])));
    assert.equal(parsed[0].module, parsed[1].module, name);
    assert.equal(parsed[0].dependency, parsed[1].dependency, name);
    assert.equal(parsed[0].build_module ?? parsed[0].module, parsed[1].build_module ?? parsed[1].module, name);
    for (const runnerOs of name === "test" || name === "desktop-go" ? ["Linux", "macOS", "Windows"] : ["Linux"]) {
      const inputs = { save_mod_os: "Linux", ...parsed[1] };
      if (cacheGate(moduleSave, { inputs, github: { event_name: "push" }, runner: { os: runnerOs } })) {
        const namespace = `${inputs.module}:${inputs.dependency}`;
        assert.equal(owners.has(namespace), false, `${namespace} has more than one writer`);
        owners.set(namespace, name);
      }
    }
    if (["test", "root", "lint"].includes(name)) {
      const saveCondition = steps[1].match(/^if: (.+)$/m)[1];
      assert.equal(vm.runInNewContext(saveCondition, { always: () => true, env: { RUN_STEPS: "false" } }), false, name);
      assert.equal(vm.runInNewContext(saveCondition, { always: () => true, env: { RUN_STEPS: "true" } }), true, name);
    } else assert.match(steps[1], /^if: always\(\)/);
    if (["test", "race", "desktop-frontend", "desktop-go"].includes(name)) assert.match(body, /run: go mod download/);
  }
  assert.deepEqual(Object.fromEntries(owners), {
    "root:go.sum": "test", "root-race:go.sum": "race", "desktop:desktop/go.sum": "desktop-go", "root-tools:go.sum": "lint",
  });
});

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
test("frontend workflows share one exact Node runtime", () => {
  const version = readFileSync(new URL("../.node-version", import.meta.url), "utf8").trim();
  assert.match(version, /^\d+\.\d+\.\d+$/);
  for (const [name, source] of [["ci", ci], ["app-memory", appMemory], ["release-desktop", release]]) {
    assert.doesNotMatch(source, /node-version: ["']?24(?:["']|\s)/, name);
    assert.match(source, /node-version-file: \.node-version/, name);
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
  const required = ["test", "test-windows", "race", "lint", "desktop", "root"];
  const jobs = ci.slice(ci.indexOf("\njobs:\n")); // `on:` also nests two-space keys
  const names = [...jobs.matchAll(/^ {2}([a-z][a-z0-9-]*):$/gm)].map(match => match[1]);
  assert.equal(names.length, 13, "twelve validation jobs plus non-gating timing evidence");
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
    if (name === "timings") {
      assert.match(job(ci, name), /continue-on-error: true/);
      assert.match(job(ci, name), /actions: read/);
      continue;
    }
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
