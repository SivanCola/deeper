import assert from "node:assert/strict";
import { test } from "node:test";
import { markSessionRead, mergeReadStores, seedSessionReads, type ReadStore } from "../lib/sessionReadActivity";
import { projectTreeReadActivityKey } from "../lib/projectTreeTopic";
import type { ProjectNode } from "../lib/types";
const node: ProjectNode = { key: "b", kind: "topic", label: "B", session: { hostId: "local", sessionId: "b" }, turnsState: "ready", resultSequence: 20 };
const key = projectTreeReadActivityKey(node)!;
const empty = (): ReadStore => ({ version: 3, baselineAt: 1, records: {} });
test("current read positions cannot be lowered by stale observations", () => {
  const seeded = seedSessionReads(empty(), [{ ...node, resultSequence: 10 }]);
  const read = markSessionRead(seeded, node);
  assert.equal(read.records[key].value, 20);
  assert.equal(markSessionRead(read, { ...node, resultSequence: 10 }), read);
  assert.equal(seedSessionReads(read, [{ ...node, resultSequence: 30 }]), read);
});
test("cross-window read merges preserve the highest position in either order", () => {
  const seeded = seedSessionReads(empty(), [{ ...node, resultSequence: 10 }]);
  const first = markSessionRead(seeded, node);
  const second = markSessionRead(seeded, { ...node, resultSequence: 30 });
  assert.equal(mergeReadStores(first, second).records[key].value, 30);
  assert.equal(mergeReadStores(second, first), second);
});
test("a ready zero result sequence is a valid baseline", () => {
  const seeded = seedSessionReads(empty(), [{ ...node, resultSequence: 0 }]);
  assert.equal(seeded.records[key].value, 0);
  assert.equal(seedSessionReads(empty(), [{ ...node, turnsState: "pending" }]).records[key], undefined);
});
