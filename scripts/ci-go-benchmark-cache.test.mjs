import assert from "node:assert/strict";
import test from "node:test";
import { benchmarkCacheIdentity } from "./ci-go-benchmark-cache.mjs";

const input = { mode: "restored", snapshot: "12345-2", dependencyHash: "abcde".repeat(8),
  workspace: "D:\\a\\workspace with spaces\\repo", buildDirectory: "C:\\Users\\runner\\AppData\\Local\\go-build" };

test("benchmark restores exact existing portable module and Windows build keys", () => {
  const actual = benchmarkCacheIdentity(input);
  assert.equal(actual.mod_key, "go-desktop-abcdeabcdeabcdea-mod-12345-2");
  assert.equal(actual.build_key, "go-desktop-abcdeabcdeabcdea-build-Windows-X64-12345-2");
  assert.equal(actual.mod_paths, ".go-mod-cache");
  assert.equal(actual.build_paths, "C:/Users/runner/AppData/Local/go-build");
  assert.equal(actual.gomodcache, "D:/a/workspace with spaces/repo/.go-mod-cache");
  assert.deepEqual(benchmarkCacheIdentity(input), actual);
});

test("benchmark refuses missing, ambiguous or injectable snapshots and mixed cold inputs", () => {
  for (const snapshot of ["", "12345", "0-1", "12345-0", "latest", "12345-1\nother", "12345-1; echo bad"]) {
    assert.throws(() => benchmarkCacheIdentity({ ...input, snapshot }), /cache_snapshot/);
  }
  assert.throws(() => benchmarkCacheIdentity({ ...input, mode: "cold" }), /cold mode/);
  assert.throws(() => benchmarkCacheIdentity({ ...input, mode: "unknown" }), /cache mode/);
  assert.throws(() => benchmarkCacheIdentity({ ...input, dependencyHash: "invalid" }), /dependency identity/);
  assert.doesNotThrow(() => benchmarkCacheIdentity({ ...input, mode: "cold", snapshot: "" }));
});
