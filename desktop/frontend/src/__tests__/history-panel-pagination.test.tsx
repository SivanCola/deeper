import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import type { DeepzoraDesktopHost } from "../lib/desktopHost";
import type { SessionMeta } from "../lib/types";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Node: dom.window.Node,
  Element: dom.window.Element, HTMLElement: dom.window.HTMLElement,
  localStorage: dom.window.localStorage, IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const requests: string[] = [];
const releases: string[] = [];
const sessions: SessionMeta[] = Array.from({ length: 51 }, (_, i) => ({
  sessionId: `conversation-${i}`, path: `session-id:conversation-${i}`,
  title: `Conversation ${i}`, preview: `Question ${i}`, turns: 1,
  createdAt: Date.now() - i, lastActivityAt: Date.now() - i, current: false, open: false,
}));
window.deepzoraDesktop = {
  kind: "electron", contract: { commands: ["ListHistorySessions", "ReleaseReadSnapshot"] },
  native: { window: {} }, on: () => () => {},
  invoke: async (name: string, args: unknown[]) => {
    if (name === "ReleaseReadSnapshot") { releases.push(String(args[0])); return; }
    assert.equal(name, "ListHistorySessions", "ordinary history must not query retired recovery versions");
    const request = args[0] as { cursor: string; limit: number };
    requests.push(request.cursor);
    const offset = Number(request.cursor || 0);
    const end = Math.min(sessions.length, offset + request.limit);
    return { items: sessions.slice(offset, end), nextCursor: end < sessions.length ? String(end) : "",
      snapshotId: "history-page", revision: 1, partial: false, staleCursor: false };
  },
} as unknown as DeepzoraDesktopHost;
const { createRoot } = await import("react-dom/client");
const { HistoryPanel } = await import("../components/HistoryPanel");
const { LocaleProvider } = await import("../lib/i18n");
const root = createRoot(document.getElementById("root")!);
try {
  await act(async () => root.render(<LocaleProvider><HistoryPanel
    sessions={[]} running={false} onResume={() => {}} onPreview={async () => []}
    onDelete={() => {}} onRename={() => {}} onClose={() => {}}
  /></LocaleProvider>));
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
  assert.equal(document.querySelectorAll(".hist-item").length, 50);
  const loadMore = [...document.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent?.trim() === "Load more");
  assert.ok(loadMore, "the next history page remains reachable");
  await act(async () => loadMore.click());
  assert.deepEqual(requests, ["", "50"]);
  assert.equal(document.querySelectorAll(".hist-item").length, 51, "append preserves the first page");
  assert.ok(!document.body.textContent?.includes("Load more"), "the end of history has no continuation");
} finally {
  await act(async () => root.unmount());
  dom.window.close();
}
assert.ok(releases.includes("history-page"), "closing history releases its read snapshot");
console.log("PASS current history pagination and snapshot release");
