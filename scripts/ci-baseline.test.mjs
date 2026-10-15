import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import test from "node:test";

const ci = readFileSync(new URL("../.github/workflows/ci.yml", import.meta.url), "utf8");
const job = name => {
  const body = ci.match(new RegExp(`\\n  ${name}:\\n([\\s\\S]*?)(?=\\n  [a-z][a-z0-9-]*:|$)`))?.[1];
  assert.ok(body, `missing job ${name}`);
  return body;
};

test("renderer motion and transcript contracts agree with the CI execution graph", () => {
  const result = spawnSync(process.execPath, ["desktop/frontend/scripts/check-motion-ci-contract.mjs"], {
    cwd: new URL("..", import.meta.url), encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stdout + result.stderr);
});

test("required contexts retain real checks on each platform", () => {
  for (const name of ["lint", "root", "desktop", "race", "test"]) assert.ok(job(name));
  for (const name of ["test", "desktop-go"]) {
    assert.match(job(name), /os: \[ubuntu-latest, macos-latest, windows-latest\]/);
    assert.match(job(name), /fail-fast: false/);
  }
  assert.match(job("test"), /go test -timeout=15m \.\/\.\.\./);
  assert.match(job("desktop-go"), /DESKTOP_TEST_TIMEOUT: \$\{\{ runner\.os == 'Windows' && '35m' \|\| '15m' \}\}/);
  assert.match(job("desktop-go"), /timeout-minutes: \$\{\{ matrix\.os == 'windows-latest' && 65 \|\| 35 \}\}/);
  assert.match(job("desktop-go"), /go test -timeout="\$DESKTOP_TEST_TIMEOUT" \.\/\.\.\./);
  assert.match(job("test"), /RUN_STEPS:[^\n]+needs\.changes\.result != 'success'/);
  assert.match(job("race"), /go test -race/);
  assert.match(job("lint"), /go run \.\/tools\/repolint/);
  assert.match(job("lint"), /golangci-lint-action/);
});

test("desktop aggregation rejects failed, cancelled, or missing platform evidence", () => {
  const body = job("desktop");
  assert.match(body, /needs: \[changes, desktop-frontend, desktop-tests, desktop-browser, desktop-go\]/);
  assert.match(body, /if: \$\{\{ always\(\) \}\}/);
  assert.match(body, /CI_NEEDS: \$\{\{ toJSON\(needs\) \}\}/);
  assert.match(body, /run: node scripts\/ci-plan\.mjs/);
  const run = (changes, required, frontend, native) => spawnSync(process.execPath, ["scripts/ci-plan.mjs"], {
    env: { DESKTOP_PLAN: JSON.stringify({ schemaVersion: 1, build: required === "true", tests: required === "true", browser: required === "true", native: required === "true" }),
      CI_NEEDS: JSON.stringify({ changes: { result: changes }, "desktop-frontend": { result: frontend }, "desktop-tests": { result: frontend }, "desktop-browser": { result: frontend }, "desktop-go": { result: native } }) },
  }).status;
  assert.equal(run("success", "true", "success", "success"), 0);
  assert.equal(run("success", "false", "skipped", "skipped"), 0);
  for (const status of ["failure", "cancelled", "skipped", ""]) {
    assert.notEqual(run("success", "true", status, "success"), 0);
    assert.notEqual(run("success", "true", "success", status), 0);
  }
  assert.notEqual(run("failure", "false", "skipped", "skipped"), 0);
});

test("frontend build, tests and Go run independently; only the browser consumes the artifact", () => {
  const frontend = job("desktop-frontend"), native = job("desktop-go");
  assert.equal((ci.match(/run: pnpm --dir frontend build:electron/g) ?? []).length, 1);
  assert.match(job("desktop-tests"), /node frontend\/scripts\/run-ci-tests\.mjs/);
  assert.doesNotMatch(frontend, /run-ci-tests|test:motion-browser|test:transcript-browser/);
  assert.match(frontend, /node-version-file: \.node-version/);
  assert.match(frontend, /version: 10\.34\.5/);
  assert.match(frontend, /artifact-identity\.mjs create/);
  assert.match(frontend, /retention-days: 7/);
  assert.match(job("desktop-browser"), /artifact-identity\.mjs verify/);
  assert.match(job("desktop-browser"), /needs\.desktop-frontend\.outputs\.producer_attempt/);
  assert.doesNotMatch(native, /artifact-identity|upload-artifact|download-artifact|producer_attempt/);
  assert.doesNotMatch(native, /setup-node|pnpm|needs\.desktop-frontend/);
  for (const body of [frontend, native]) {
    assert.match(body, /needs: changes/);
    assert.match(body, /needs\.changes\.result != 'success'/);
    assert.match(body, /fromJSON\(needs\.changes\.outputs\.desktop_plan\)/);
  }
});

test("release packaging and legacy acceptance do not run in ordinary CI", () => {
  assert.doesNotMatch(ci, /packaging\/package\.mjs|test-windows-installer|credential ACL identity|TestTaggedHistory|windows-isolated|desktop-browser-group/);
  assert.match(job("root"), /node --test scripts\/\*\.test\.mjs/);
  assert.match(job("root"), /bash scripts\/release-workflows\.test\.sh/);
  assert.match(job("desktop-browser"), /test:motion-browser/);
  assert.match(job("desktop-browser"), /test:transcript-browser/);
});
