import assert from "node:assert/strict";
import { createHash } from "node:crypto";

export const digest = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
export const testName = value => /^(?:Test|Example|Fuzz)[\p{L}\p{N}_]*$/u.test(value);
export function inventoryFromList(output) {
  const names = output.split(/\r?\n/).filter(testName);
  assert.ok(names.length, "test discovery returned an empty inventory");
  assert.equal(new Set(names).size, names.length, "duplicate discovered tests");
  return names.sort();
}

export function assignedTests(pkg, inventory, index, count) {
  assert.ok(Number.isInteger(count) && count >= 1 && count <= 2, "invalid shard count");
  assert.ok(Number.isInteger(index) && index >= 1 && index <= count, "invalid shard index");
  return inventory.filter(name => createHash("sha256").update(`${pkg}\0${name}`).digest().readUInt32BE(0) % count === index - 1);
}

export function batches(names, limit = 12_000) {
  const groups = [];
  let group = [], size = 4;
  for (const name of names) {
    assert.ok(testName(name), `invalid test name: ${name}`);
    assert.ok(name.length + 4 <= limit, "test name exceeds argument budget");
    if (size + name.length + (group.length ? 1 : 0) > limit) {
      groups.push(group); group = []; size = 4;
    }
    size += name.length + (group.length ? 1 : 0); group.push(name);
  }
  if (group.length) groups.push(group);
  return groups;
}

export function completedTests(events, pkg, selected) {
  const starts = new Set(), completed = new Map();
  for (const event of events) {
    if (event.Package !== pkg || !testName(event.Test ?? "")) continue;
    assert.ok(selected.includes(event.Test), `unexpected top-level test ${event.Test}`);
    if (event.Action === "run") {
      assert.ok(!starts.has(event.Test), `duplicate top-level test ${event.Test}`);
      starts.add(event.Test);
    }
    if (["pass", "fail", "skip"].includes(event.Action)) {
      assert.ok(!completed.has(event.Test), `duplicate result ${event.Test}`);
      completed.set(event.Test, { name: event.Test, result: event.Action, seconds: event.Elapsed ?? 0 });
    }
  }
  assert.deepEqual([...completed.keys()].sort(), [...selected].sort(), "missing or unfinished top-level tests");
  assert.deepEqual([...starts].sort(), [...selected].sort(), "test results without run events");
  assert.ok([...completed.values()].every(row => row.result !== "fail"), "failed top-level tests");
  return [...completed.values()].sort((a, b) => a.name.localeCompare(b.name));
}

export function verifyShards(reports, expected) {
  assert.equal(reports.length, 2, "exactly two Windows reports required");
  const sorted = [...reports].sort((a, b) => a.index - b.index);
  const first = sorted[0];
  for (const [i, report] of sorted.entries()) {
    assert.equal(report.schemaVersion, 1, "unsupported shard report");
    assert.equal(report.index, i + 1, "missing or duplicate shard index");
    assert.equal(report.count, 2, "invalid shard count");
    assert.equal(report.status, "success", "incomplete or failed shard");
    for (const [key, value] of Object.entries(expected)) if (key !== "currentAttempt") assert.equal(report[key], value, `${key} mismatch`);
    assert.match(String(report.attempt), /^[1-9]\d*$/, "invalid producer attempt");
    if (expected.currentAttempt !== undefined) assert.ok(Number(report.attempt) <= Number(expected.currentAttempt), "future producer attempt");
    for (const key of ["sourceSHA", "runId", "platform", "arch", "goVersion", "package", "inventoryDigest", "packagesDigest"]) {
      assert.equal(report[key], first[key], `${key} differs between shards`);
    }
    assert.match(report.sourceSHA, /^[0-9a-f]{40}$/);
    assert.match(report.runId, /^\d+$/);
    assert.ok(report.goVersion && report.package && report.platform && report.arch, "missing runtime identity");
    assert.ok(report.inventory.length && report.inventory.every(testName), "invalid discovery inventory");
    assert.deepEqual(report.inventory, [...new Set(report.inventory)].sort(), "inventory must be sorted and unique");
    assert.equal(report.inventoryDigest, digest(report.inventory), "inventory digest mismatch");
    assert.equal(report.packagesDigest, digest(report.packages), "package inventory digest mismatch");
    assert.ok(report.packages.includes(report.package), "main package missing from package inventory");
    assert.deepEqual(report.packages, [...new Set(report.packages)].sort(), "package inventory must be sorted and unique");
    const assigned = assignedTests(report.package, report.inventory, report.index, 2);
    assert.deepEqual(report.assigned, assigned, "incorrect shard assignment");
    assert.deepEqual(report.completed.map(row => row.name).sort(), [...assigned].sort(), "incomplete shard coverage");
    assert.ok(report.completed.every(row => ["pass", "skip"].includes(row.result) && Number.isFinite(row.seconds) && row.seconds >= 0), "invalid test outcome");
    if (report.index === 1) {
      const others = report.packages.filter(pkg => pkg !== report.package);
      assert.deepEqual(report.otherPackages.map(row => row.package).sort(), others, "other package coverage missing");
      assert.ok(report.otherPackages.every(row => ["pass", "skip"].includes(row.result)), "other package failed");
    } else assert.deepEqual(report.otherPackages, [], "other packages must run only in shard one");
  }
  const combined = sorted.flatMap(report => report.assigned).sort();
  assert.deepEqual(combined, first.inventory, "shards overlap or omit tests");
  return { tests: combined.length, packages: first.packages.length, attempts: sorted.map(report => report.attempt) };
}
