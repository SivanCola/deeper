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
  assert.match(body, /needs: \[changes, desktop-frontend, desktop-go\]/);
  assert.match(body, /if: \$\{\{ !cancelled\(\) \}\}/);
  const script = body.split("        run: |\n")[1].replace(/^          /gm, "");
  const run = (changes, required, frontend, native) => spawnSync("bash", ["-c", script], {
    env: { CHANGES_RESULT: changes, DESKTOP_REQUIRED: required, FRONTEND_RESULT: frontend, GO_RESULT: native },
  }).status;
  assert.equal(run("success", "true", "success", "success"), 0);
  assert.equal(run("success", "false", "skipped", "skipped"), 0);
  for (const status of ["failure", "cancelled", "skipped", ""]) {
    assert.notEqual(run("success", "true", status, "success"), 0);
    assert.notEqual(run("success", "true", "success", status), 0);
  }
  assert.notEqual(run("failure", "false", "skipped", "skipped"), 0);
});

test("frontend and Go service checks run independently without unused artifacts", () => {
  const frontend = job("desktop-frontend"), native = job("desktop-go");
  assert.equal((ci.match(/run: pnpm --dir frontend build:electron/g) ?? []).length, 1);
  assert.match(frontend, /node frontend\/scripts\/run-ci-tests\.mjs/);
  assert.match(frontend, /node-version-file: \.node-version/);
  assert.match(frontend, /version: 10\.34\.5/);
  assert.doesNotMatch(frontend + native, /artifact-identity|upload-artifact|download-artifact|producer_attempt/);
  assert.doesNotMatch(native, /setup-node|pnpm|needs\.desktop-frontend/);
  for (const body of [frontend, native]) {
    assert.match(body, /needs: changes/);
    assert.match(body, /needs\.changes\.result != 'success' \|\| needs\.changes\.outputs\.desktop != 'false'/);
  }
});

test("release packaging and legacy acceptance do not run in ordinary CI", () => {
  assert.doesNotMatch(ci, /packaging\/package\.mjs|test-windows-installer|credential ACL identity|TestTaggedHistory|windows-isolated|desktop-browser-group/);
  assert.match(job("root"), /node --test scripts\/\*\.test\.mjs/);
  assert.match(job("root"), /bash scripts\/release-workflows\.test\.sh/);
  assert.match(job("desktop-frontend"), /test:motion-browser/);
  assert.match(job("desktop-frontend"), /test:transcript-browser/);
});
