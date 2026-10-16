import assert from "node:assert/strict";
import test from "node:test";
import { installDesktopHostStub } from "./desktopHostStub";

Object.defineProperty(globalThis, "window", { configurable: true, value: {} });
const commands: Record<string, unknown> = {};
installDesktopHostStub(commands);
const { readBoundHistoryWindow, readBoundHistoryContent, searchBoundHistory, releaseHistoryRead } = await import("../lib/historyReadBinding");
const { TranscriptStore } = await import("../lib/transcriptStore");
const { FakeBackend } = await import("./helpers/transcriptFakeBackend");
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
const handle = (id: string) => ({ id, sessionGeneration: 1 });
const page = { status: "ready", messages: [], snapshotSequence: 1, coverageSequence: 1, generation: "g", totalTurns: 0, hasOlder: false, hasNewer: false };

test("late Begin is released without touching replacement navigation", async () => {
  const first = deferred<ReturnType<typeof handle>>();
  const began = deferred<void>();
  const released: string[] = [];
  let begins = 0;
  commands.BeginSessionHistoryReadForTab = () => {
    begins++;
    if (begins === 1) { began.resolve(); return first.promise; }
    return handle("new");
  };
  commands.ReleaseSessionHistoryRead = (id: string) => { released.push(id); };
  commands.ReadSessionHistoryWindow = () => page;
  const old = readBoundHistoryWindow("A", { anchor: "newest", limit: 32 });
  await began.promise;
  releaseHistoryRead("A");
  assert.equal((await readBoundHistoryWindow("A", { anchor: "newest", limit: 32 }))?.status, "ready");
  first.resolve(handle("old"));
  assert.equal((await old)?.status, "stale_cursor");
  assert.deepEqual(released, ["old"]);
  releaseHistoryRead("A");
  await Promise.resolve();
  assert.deepEqual(released, ["old", "new"]);
});

test("late page cannot publish into A after A to B to A", async () => {
  const reply = deferred<typeof page>();
  const entered = deferred<void>();
  let reads = 0, begins = 0;
  commands.BeginSessionHistoryReadForTab = () => handle(String(++begins));
  commands.ReadSessionHistoryWindow = () => { if (++reads === 1) { entered.resolve(); return reply.promise; } return page; };
  const old = readBoundHistoryWindow("A", { anchor: "newest", limit: 32 });
  await entered.promise;
  releaseHistoryRead("A");
  await readBoundHistoryWindow("B", { anchor: "newest", limit: 32 });
  releaseHistoryRead("B");
  assert.equal((await readBoundHistoryWindow("A", { anchor: "newest", limit: 32 }))?.status, "ready");
  reply.resolve(page);
  assert.equal((await old)?.status, "stale_cursor");
  releaseHistoryRead("A");
});

test("a replaced source retires the stale preparation before a fresh window", async () => {
  let begins = 0;
  const released: string[] = [];
  commands.BeginSessionHistoryReadForTab = () => handle(String(++begins));
  commands.ReleaseSessionHistoryRead = (id: string) => { released.push(id); };
  commands.ReadSessionHistoryWindow = (id: string) => ({ ...page, status: id === "1" ? "stale_cursor" : "ready" });
  assert.equal((await readBoundHistoryWindow("changed", { anchor: "newest" }))?.status, "stale_cursor");
  assert.equal((await readBoundHistoryWindow("changed", { anchor: "newest" }))?.status, "ready");
  assert.equal(begins, 2, "the new read cannot reuse a failed source generation forever");
  assert.deepEqual(released, ["1"]);
  releaseHistoryRead("changed");
});

test("current anchors retain the outline cut on the bound reader", async () => {
  const requests: Record<string, unknown>[] = [];
  commands.BeginSessionHistoryReadForTab = () => handle("current");
  commands.ReadSessionHistoryWindow = (_id: string, req: Record<string, unknown>) => {
    requests.push(req);
    return { ...page, hasNewer: true, totalTurns: 200, snapshotSequence: 9, generation: "bound-cut" };
  };
  const result = await readBoundHistoryWindow("native", { anchor: "turn", turn: 17, generation: "bound-cut", snapshotSequence: 9, limit: 32 });
  assert.equal(result?.status, "ready");
  assert.equal(requests[requests.length - 1]?.anchor, "turn");
  assert.equal(requests[requests.length - 1]?.turn, 17);
  assert.equal(requests[requests.length - 1]?.generation, "bound-cut");
  assert.equal(requests[requests.length - 1]?.snapshotSequence, 9);
  await readBoundHistoryWindow("native", { anchor: "message", messageId: "sone:r9:m32:o0", generation: "bound-cut" });
  assert.equal(requests[requests.length - 1]?.messageId, "sone:r9:m32:o0");
  releaseHistoryRead("native");
});

test("bound current search fences a late reply after navigation", async () => {
  commands.BeginSessionHistoryReadForTab = () => handle("search");
  const result = { hits: [{ messageId: "old", preview: "old", position: 1, role: "user", eventSequence: 1 }],
    status: "ready", snapshotSequence: 1, coverageSequence: 1, hasMore: false };
  const reply = deferred<typeof result>(), entered = deferred<void>();
  commands.SearchSessionHistoryRead = () => { entered.resolve(); return reply.promise; };
  const old = searchBoundHistory("search", "needle");
  await entered.promise;
  releaseHistoryRead("search");
  commands.SearchSessionHistoryRead = () => ({ ...result, hits: [], status: "preparing", coverageSequence: 0 });
  assert.equal((await searchBoundHistory("search", "needle"))?.status, "preparing");
  reply.resolve(result);
  const late = await old;
  assert.equal(late?.status, "stale_cursor");
  assert.deepEqual(late?.hits, []);
  commands.SearchSessionHistoryRead = () => ({ ...result, hits: [], status: "stale_cursor" });
  assert.equal((await searchBoundHistory("search", "needle"))?.status, "stale_cursor");
  releaseHistoryRead("search");
});

test("a delayed content chunk cannot publish after its reader is replaced", async () => {
  let begins = 0;
  commands.BeginSessionHistoryReadForTab = () => handle(`content-${++begins}`);
  const result = { data: "b2s=", nextOffset: 2, done: true };
  const reply = deferred<typeof result>(), entered = deferred<void>();
  commands.ReadSessionHistoryContent = (id: string) => { assert.equal(id, "content-1"); entered.resolve(); return reply.promise; };
  const ref = { digest: "proof", bytes: 2 } as Parameters<typeof readBoundHistoryContent>[1];
  const old = readBoundHistoryContent("content", ref, 0);
  const rejected = assert.rejects(old, /expired/);
  await entered.promise;
  releaseHistoryRead("content");
  commands.ReadSessionHistoryContent = (id: string) => { assert.equal(id, "content-2"); return result; };
  assert.deepEqual(await readBoundHistoryContent("content", ref, 0), result);
  reply.resolve(result);
  await rejected;
  releaseHistoryRead("content");
});

test("cache eviction releases its pending read without releasing a rebound tab", async () => {
  const released: string[] = [];
  const first = deferred<ReturnType<typeof handle>>(), began = deferred<void>();
  let begins = 0;
  commands.BeginSessionHistoryReadForTab = () => {
    if (++begins === 1) { began.resolve(); return first.promise; }
    return handle(`read-${begins}`);
  };
  commands.ReleaseSessionHistoryRead = (id: string) => { released.push(id); };
  commands.ReadSessionHistoryWindow = () => page;
  const backend = new FakeBackend([{ role: "user", content: "history" }]);
  const store = new TranscriptStore(backend, { maxResidentSessions: 1 });
  store.noteSessionBinding("evicted", "/old", "s\0old");
  await store.loadLatest("evicted", "/old");
  const oldRead = readBoundHistoryWindow("evicted", { anchor: "newest" });
  await began.promise;
  store.noteSessionBinding("other", "/other", "s\0other");
  await store.loadLatest("other", "/other");
  assert.equal(store.isResident("evicted", "/old"), false);
  first.resolve(handle("late-evicted"));
  assert.equal((await oldRead)?.status, "stale_cursor");
  assert.deepEqual(released, ["late-evicted"], "eviction releases even a late Begin response");

  store.noteSessionBinding("other", "/replacement", "s\0replacement");
  assert.equal((await readBoundHistoryWindow("other", { anchor: "newest" }))?.status, "ready");
  await store.loadLatest("other", "/replacement");
  assert.equal(store.isResident("other", "/other"), false);
  assert.equal((await readBoundHistoryWindow("other", { anchor: "newest" }))?.status, "ready");
  assert.equal(begins, 2, "evicting the old cached source must retain the replacement binding");
  assert.deepEqual(released, ["late-evicted"]);
  releaseHistoryRead("other");
  await Promise.resolve();
  assert.deepEqual(released, ["late-evicted", "read-2"]);
});
