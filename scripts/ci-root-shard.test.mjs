import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { execFileSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { assignPackages, packageBatches, packageResults, verifyRootReports, runRootShard } from "./ci-root-shard.mjs";
import { digest } from "./ci-go-shard-model.mjs";

const inventory = Array.from({ length: 40 }, (_, i) => `example.test/root/pkg${i}`).sort();
const identity = { sourceSHA: "a".repeat(40), runId: "42", platform: "win32", arch: "x64", goVersion: "go version go1.26 windows/amd64" };
const expected = { sourceSHA: identity.sourceSHA, runId: "42", platform: "win32", count: 2, currentAttempt: 3 };
function reports() {
  return [1, 2].map(index => {
    const assigned = assignPackages(inventory, index, 2);
    return { schemaVersion: 1, status: "success", buildCompleted: true, ...identity, index, count: 2, attempt: index === 1 ? "3" : "1",
      inventory, inventoryDigest: digest(inventory), assigned, completed: assigned.map((p, i) => ({ package: p, result: i ? "pass" : "skip" })) };
  });
}
test("dynamic package partitions are stable, disjoint, exhaustive and include new packages", () => {
  const a = assignPackages(inventory, 1, 2), b = assignPackages(inventory, 2, 2);
  assert.deepEqual([...a, ...b].sort(), inventory);
  assert.equal(new Set([...a, ...b]).size, inventory.length);
  const next = [...inventory, "example.test/root/new"].sort();
  for (const index of [1, 2]) assert.deepEqual(assignPackages(next, index, 2).filter(p => p !== "example.test/root/new"), assignPackages(inventory, index, 2));
  assert.deepEqual(assignPackages(inventory, 1, 1), inventory);
  for (const args of [[[], 1, 2], [inventory, 0, 2], [inventory, 1, 3], [["x", "x"], 1, 2]]) assert.throws(() => assignPackages(...args));
});
test("root gate accepts previous successful attempts and normal no-test skips", () => {
  assert.deepEqual(verifyRootReports(reports(), expected), { packages: 40, attempts: ["3", "1"] });
});
test("long package arguments are bounded without losing or duplicating packages", () => {
  const packages = Array.from({ length: 500 }, (_, i) => `example.test/${"deep/".repeat(20)}p${i}`);
  const groups = packageBatches(packages);
  assert.ok(groups.length > 1);
  assert.deepEqual(groups.flat(), packages);
  assert.ok(groups.every(group => group.join(" ").length + 100 <= 12_000));
  assert.throws(() => packageBatches(["p".repeat(12_000)]));
});
test("root gate rejects incomplete, forged, overlapping and failed evidence", () => {
  const mutations = [
    r => r.pop(), r => r.push(r[0]), r => { r[1].index = 1; }, r => { r[1].sourceSHA = "b".repeat(40); },
    r => { r[1].schemaVersion = 2; }, r => { r[1].goVersion = "different"; }, r => { r[1].attempt = "4"; },
    r => { r[0].assigned.pop(); }, r => { r[0].completed.pop(); }, r => { r[0].completed[0].result = "fail"; },
    r => { r[0].status = "failure"; }, r => { r[0].buildCompleted = false; }, r => { r[0].inventoryDigest = "wrong"; },
    r => { r[1].inventory = r[1].inventory.slice(1); r[1].inventoryDigest = digest(r[1].inventory); },
  ];
  for (const mutate of mutations) { const r = reports(); mutate(r); assert.throws(() => verifyRootReports(r, expected)); }
});
test("package terminal verification rejects missing, duplicate, failed and helper test rows", () => {
  const events = [{ Package: "a", Action: "pass" }, { Package: "b", Action: "skip" }, { Package: "a", Test: "TestHelper", Action: "pass" }];
  assert.equal(packageResults(events, ["a", "b"]).length, 2);
  assert.throws(() => packageResults(events.slice(1), ["a", "b"]));
  assert.throws(() => packageResults([...events, events[0]], ["a", "b"]));
  assert.throws(() => packageResults([{ Package: "a", Action: "fail" }], ["a"]));
});
test("real Go runner preserves complete package builds, test execution, no-test packages and failures", async t => {
  const root = mkdtempSync(path.join(os.tmpdir(), "root partition fixture "));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(path.join(root, "go.mod"), "module example.test/root\n\ngo 1.26\n");
  writeFileSync(path.join(root, "main.go"), "package main\nfunc main() {}\n");
  for (const name of ["a", "b", "c", "d"]) {
    mkdirSync(path.join(root, name));
    writeFileSync(path.join(root, name, "value.go"), "package value\nfunc Value() int { return 42 }\n");
    writeFileSync(path.join(root, name, "value_test.go"), 'package value\nimport "testing"\nfunc TestValue(t *testing.T) { if Value()!=42 { t.Fatal("wrong value") } }\n');
  }
  execFileSync("git", ["init", "-q"], { cwd: root });
  execFileSync("git", ["-c", "user.name=CI Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture", "--allow-empty"], { cwd: root });
  const results = [];
  for (const index of [1, 2]) results.push(await runRootShard({ directory: root, output: path.join(root, `output-${index}`), index, count: 2 }));
  const e = { sourceSHA: results[0].sourceSHA, runId: results[0].runId, count: 2, currentAttempt: results[0].attempt };
  assert.equal(verifyRootReports(results, e).packages, 5);
  assert.ok(results.flatMap(r => r.completed).some(r => r.package === "example.test/root" && r.result === "skip"));
  writeFileSync(path.join(root, "a", "value.go"), "package value\nfunc Value() int { return 0 }\n");
  await assert.rejects(runRootShard({ directory: root, output: path.join(root, "failure"), index: 1, count: 1 }));
  assert.equal(JSON.parse(readFileSync(path.join(root, "failure", "report.json"))).status, "failure");
});
