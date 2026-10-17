import assert from "node:assert/strict";
import { TranscriptStore } from "../lib/transcriptStore";
import type { HistoryMessage } from "../lib/types";
import { FakeBackend } from "./helpers/transcriptFakeBackend";

const eq: typeof assert.equal = assert.equal;
const ok: typeof assert.ok = assert.ok;

// Missing IDs never acquire an adjacent result, including across page boundaries.
{
  const messages: HistoryMessage[] = [
    { role: "assistant", content: "", toolCalls: [{ id: "", name: "grep", arguments: "first" }, { id: "", name: "grep", arguments: "second" }] },
    { role: "tool", toolName: "grep", content: "unidentified result" },
    { role: "assistant", content: "", toolCalls: [{ id: "identified", name: "grep", arguments: "current" }] },
    { role: "tool", toolCallId: "identified", toolName: "grep", content: "current result" },
  ];
  const store = new TranscriptStore(new FakeBackend(messages), { windowMaxPages: 4 });
  await store.loadLatest("identity", "/identity", { entries: 1 });
  for (let page = 0; page < 4; page++) await store.loadOlder("identity", "/identity", { entries: 1 });
  const items = store.peek("identity", "/identity")?.items.filter(item => item.kind === "tool") ?? [];
  eq(items.length, 4, "two unidentified calls and their unmatched result stay separate");
  for (const args of ["first", "second"]) {
    const call = items.find(item => item.kind === "tool" && item.args === args);
    eq(call?.kind === "tool" && call.status, "unknown", `${args} remains unknown after paging`);
    eq(call?.kind === "tool" && call.output, "", `${args} cannot borrow positional output`);
  }
  const current = items.find(item => item.id === "identified");
  eq(current?.kind === "tool" && current.output, "current result", "explicit identity still associates across pages");
  eq(current?.kind === "tool" && current.status, "done", "identified result still settles its call");
  ok(items.some(item => item.kind === "tool" && item.output === "unidentified result"), "unmatched output remains visible");
}

for (const field of ["toolArguments", "toolDiff", "canonicalMessage"] as const) {
  const backend = new FakeBackend([{ role: "assistant", content: "", toolCalls: [{ id: "", name: "grep", arguments: "preview", argumentsArchived: true }] }]);
  const slice = backend.slice(0, 1);
  slice.entries[0].refs = [{ entryId: slice.entries[0].entryId, toolCallId: "", field, size: 4, chunks: 1, revision: 1, digest: "args" }];
  backend.readHistorySlice = async () => slice;
  const store = new TranscriptStore(backend);
  const view = await store.loadLatest("missing-identity", "/missing-identity");
  const item = view?.items.find(item => item.kind === "tool");
  if (!item || item.kind !== "tool") throw new Error("unidentified preview missing");
  let rejected = false;
  try { await store.requestToolContent("missing-identity", item, {}); }
  catch (error) { rejected = error instanceof Error && error.message === "Tool call identity is missing"; }
  ok(rejected, `even one unidentified call cannot fetch ${field} by position`);
  eq(backend.contentCalls.length, 0, `missing identity is rejected before a ${field} read`);
}

console.log("PASS transcript tool identity, cross-page association and missing-ID content rejection");
