import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useSessionSummary } from "../app-runtime/useSessionSummary";
import { installDesktopHostStub } from "./desktopHostStub";
import type { TabMeta } from "../lib/types";
import type { SessionRef } from "../lib/sessionRef";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);
const requests: SessionRef[] = [];
const pending = new Map<string, (value: { turns: number }) => void>();
installDesktopHostStub({ GetSessionSummary: (ref: SessionRef) => {
  requests.push(ref);
  return new Promise(resolve => pending.set(ref.sessionId, resolve));
} });
let result: ReturnType<typeof useSessionSummary>;
function Probe({ id, hostId = "local", revision = 0 }: { id?: string; hostId?: string; revision?: number }) {
  result = useSessionSummary({ activeTab: id ? { id: "tab", topicId: "shared", session: {hostId, sessionId:id} } as TabMeta : undefined, revision });
  return null;
}
try {
  await act(async () => root.render(<Probe />));
  assert.equal(requests.length, 0);
  await act(async () => root.render(<Probe id="a" />));
  await act(async () => root.render(<Probe id="b" />));
  await act(async () => pending.get("a")!({turns:99}));
  assert.equal(result!.activeTopicTurns, undefined, "a delayed sibling sharing the topic cannot overwrite b");
  await act(async () => pending.get("b")!({turns:2}));
  assert.equal(result!.activeTopicTurns, 2);
  assert.deepEqual(requests, [{hostId:"local",sessionId:"a"},{hostId:"local",sessionId:"b"}]);
  await act(async () => root.render(<Probe id="b" revision={1} />));
  assert.equal(requests.length, 3, "a revision refreshes the exact session");
  await act(async () => pending.get("b")!({turns:3}));
  assert.equal(result!.activeTopicTurns, 3);
  await act(async () => root.render(<Probe id="b" hostId="remote" />));
  assert.equal(requests.length, 3, "remote sessions are never sent to the local resolver");
  assert.equal(result!.activeTopicTurns, undefined);
} finally { await act(async () => root.unmount()); dom.window.close(); }
console.log("PASS session summary identity, sibling isolation and revision refresh");
