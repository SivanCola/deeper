import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { digest, inventoryFromList, assignedTests, batches, completedTests, verifyShards } from "./ci-go-shard-model.mjs";
import { execute, runShard } from "./ci-go-shard.mjs";
import { compareBenchmark } from "./ci-go-benchmark.mjs";

const pkg = "example.test/shard";
const inventory = ["ExampleHello", "FuzzSeed", "TestAlpha", "TestBeta", "TestHelper", "TestNew"].sort();
const identity = { sourceSHA: "a".repeat(40), runId: "42", attempt: "1" };
function reports() {
  return [1, 2].map(index => {
    const assigned = assignedTests(pkg, inventory, index, 2);
    return { schemaVersion: 1, ...identity, platform: "win32", arch: "x64", goVersion: "go version fixture", package: pkg,
      inventory, inventoryDigest: digest(inventory), packages: [pkg, `${pkg}/other`], packagesDigest: digest([pkg, `${pkg}/other`]),
      index, count: 2, status: "success", assigned, completed: assigned.map(name => ({ name, result: "pass", seconds: 1 })),
      otherPackages: index === 1 ? [{ package: `${pkg}/other`, result: "pass" }] : [] };
  });
}

test("shards discover new tests and partition top-level tests without exclusions", () => {
  const found = inventoryFromList(inventory.join("\r\n") + "\r\nPASS\n");
  assert.deepEqual(found, inventory);
  assert.throws(() => inventoryFromList("PASS\n"));
  assert.throws(() => inventoryFromList("TestAlpha\nTestAlpha"));
  const left = assignedTests(pkg, found, 1, 2), right = assignedTests(pkg, found, 2, 2);
  assert.deepEqual([...left, ...right].sort(), inventory);
  assert.deepEqual(assignedTests(pkg, found, 1, 2), left);
  assert.throws(() => assignedTests(pkg, found, 3, 2));
  const split = batches(Array.from({ length: 1000 }, (_, i) => `TestLongName_${i}`), 200);
  assert.ok(split.every(group => `^(${group.join("|")})$`.length <= 200));
  assert.equal(split.flat().length, 1000);
  assert.throws(() => batches(["TestA|TestB"]));
  assert.throws(() => batches(["TestTooLong"], 8));
});

test("coverage rejects missing events, duplicate execution and failures but keeps native skips", () => {
  const events = [
    { Package: pkg, Action: "run", Test: "TestAlpha" },
    { Package: pkg, Action: "output", Test: "TestAlpha", Output: '{"Action":"pass","Test":"TestHelper"}' },
    { Package: pkg, Action: "pass", Test: "TestAlpha/child", Elapsed: 1 },
    { Package: pkg, Action: "skip", Test: "TestAlpha", Elapsed: 2 },
  ];
  assert.equal(completedTests(events, pkg, ["TestAlpha"])[0].result, "skip");
  assert.throws(() => completedTests(events, pkg, ["TestAlpha", "TestBeta"]));
  assert.throws(() => completedTests([...events, events[3]], pkg, ["TestAlpha"]));
  assert.throws(() => completedTests([{ ...events[3], Action: "fail" }], pkg, ["TestAlpha"]));
  assert.throws(() => completedTests(events.slice(1), pkg, ["TestAlpha"]));
});

test("report gate requires complete identity and coverage, allowing older successful attempts", () => {
  const expected = { sourceSHA: identity.sourceSHA, runId: "42", platform: "win32", currentAttempt: "2" };
  const good = reports(); good[1].attempt = "2";
  assert.equal(verifyShards(good, expected).tests, inventory.length);
  for (const mutate of [
    rows => rows.pop(), rows => rows.push(rows[0]), rows => { rows[1].index = 1; },
    rows => { rows[0].sourceSHA = "b".repeat(40); }, rows => { rows[0].runId = "43"; },
    rows => { rows[0].schemaVersion = 2; }, rows => { rows[0].attempt = "3"; },
    rows => { rows[0].inventory = rows[0].inventory.slice(1); }, rows => { rows[0].inventoryDigest = "bad"; },
    rows => { rows[0].assigned = []; }, rows => { rows[0].completed = []; },
    rows => { rows[0].completed[0].result = "fail"; }, rows => { rows[0].otherPackages = []; },
    rows => { rows[0].status = "failure"; }, rows => { rows[0].goVersion = "different"; },
  ]) {
    const changed = structuredClone(good); mutate(changed);
    assert.throws(() => verifyShards(changed, expected));
  }
});

test("process runner preserves failures, enforces a deadline and accepts spaced working paths", async t => {
  const cwd = mkdtempSync(path.join(os.tmpdir(), "ci shard runner "));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const result = await execute(process.execPath, ["-e", "process.stdout.write(process.cwd())"], { cwd, deadline: Date.now() + 5000 });
  assert.equal(result.output, realpathSync(cwd));
  await assert.rejects(execute(process.execPath, ["-e", "process.exit(7)"], { cwd, deadline: Date.now() + 5000 }), /exited 7/);
  await assert.rejects(execute(process.execPath, ["-e", "setInterval(()=>{},1000)"], { cwd, deadline: Date.now() + 100 }), /deadline/);
  await assert.rejects(execute(process.execPath, ["-e", "0"], { cwd, deadline: Date.now() - 1 }), /deadline/);
  await assert.rejects(execute("definitely-missing-ci-fixture-program", [], { cwd, deadline: Date.now() + 1000 }), /ENOENT/);
  const pending = execute(process.execPath, ["-e", "setInterval(()=>{},1000)"], { cwd, deadline: Date.now() + 5000 });
  const cancellation = setTimeout(() => process.emit("SIGTERM"), 100);
  await assert.rejects(pending, /cancelled/);
  clearTimeout(cancellation);
});

test("benchmark requires three identical candidates and records medians and cost", () => {
  const samples = [];
  for (const sample of ["1", "2", "3"]) {
    const pair = reports().map(report => ({ ...report, sample, timings: { compileMs: 10, testMs: 50, cleanupMs: 1 } }));
    samples.push(...pair, { ...pair[0], index: 1, count: 1, assigned: inventory,
      completed: inventory.map(name => ({ name, result: "pass", seconds: 1 })), timings: { compileMs: 10, testMs: 100, cleanupMs: 1 } });
  }
  const result = compareBenchmark(samples);
  assert.equal(result.reduction, 0.5);
  assert.equal(result.performanceTargetMet, true);
  assert.equal(result.full.min, 100);
  assert.throws(() => compareBenchmark(samples.slice(1)));
  const changed = structuredClone(samples); changed[0].sourceSHA = "b".repeat(40);
  assert.throws(() => compareBenchmark(changed), /sourceSHA/);
  for (const mutate of [
    full => { full.inventory = [...full.inventory, "TestUnverified"]; },
    full => { full.assigned = full.assigned.slice(1); },
    full => { full.timings.testMs = null; },
    full => { full.otherPackages = []; },
  ]) {
    const altered = structuredClone(samples);
    mutate(altered.find(report => report.count === 1));
    assert.throws(() => compareBenchmark(altered));
  }
});

test("real Go fixture preserves examples, fuzz seeds, helper re-exec and other packages", { timeout: 120_000 }, async t => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "ci shard go "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  writeFileSync(path.join(directory, "go.mod"), `module ${pkg}\n\ngo 1.24\n`);
  writeFileSync(path.join(directory, "fixture.go"), "package fixture\nfunc Hello() {}\n");
  writeFileSync(path.join(directory, "fixture_test.go"), `package fixture
import ("fmt"; "os"; "os/exec"; "testing")
func TestAlpha(t *testing.T) { t.Run("child",func(t *testing.T){}) }
func TestBeta(t *testing.T) { t.Skip("native skip") }
func TestHelper(t *testing.T) { if os.Getenv("SHARD_FIXTURE_HELPER") == "1" { fmt.Println("helper output"); os.Exit(0) } }
func TestReexec(t *testing.T) { cmd := exec.Command(os.Args[0], "-test.run=^TestHelper$"); cmd.Env=append(os.Environ(), "SHARD_FIXTURE_HELPER=1"); if out,err:=cmd.CombinedOutput(); err!=nil { t.Fatalf("%s: %v",out,err) } }
func ExampleHello() { fmt.Println("hello"); /* Output: hello */ }
func FuzzSeed(f *testing.F) { f.Add("seed"); f.Fuzz(func(t *testing.T, s string){}) }
`);
  mkdirSync(path.join(directory, "other"));
  writeFileSync(path.join(directory, "other/other.go"), "package other\n");
  const full = await runShard({ directory, output: path.join(directory, "full"), index: 1, count: 1, identity });
  const halves = [];
  for (const index of [1, 2]) halves.push(await runShard({ directory, output: path.join(directory, `shard${index}`), index, count: 2, identity }));
  assert.equal(verifyShards(halves, { sourceSHA: identity.sourceSHA, runId: "42" }).tests, full.inventory.length);
  assert.ok(full.inventory.includes("ExampleHello")); assert.ok(full.inventory.includes("FuzzSeed"));
  assert.ok(full.completed.find(row => row.name === "TestBeta" && row.result === "skip"));
  assert.ok(readFileSync(path.join(directory, "full/report.json"), "utf8").includes('"success"'));
});
