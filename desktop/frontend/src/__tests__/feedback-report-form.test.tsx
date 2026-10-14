import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  Node: dom.window.Node, HTMLElement: dom.window.HTMLElement,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window),
  cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });

const { createRoot } = await import("react-dom/client");
const { FeedbackReportForm } = await import("../components/FeedbackReportForm");
const { LocaleProvider } = await import("../lib/i18n");
const { installDesktopHostStub } = await import("./desktopHostStub");
const calls: { kind: string; detail: string }[] = [];
let resolveReport: () => void = () => {};
let rejectReport: (error: Error) => void = () => {};
const stub = installDesktopHostStub({
  ReportCrash: (kind: string, detail: string) => {
    calls.push({ kind, detail });
    return new Promise<void>((resolve, reject) => { resolveReport = resolve; rejectReport = reject; });
  },
});
const root = createRoot(document.getElementById("root")!);
const editor = () => document.querySelector("textarea")!;
const send = () => document.querySelector("button")!;
async function edit(value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")!.set!.call(editor(), value);
    editor().dispatchEvent(new window.Event("input", { bubbles: true }));
  });
}

try {
  await act(async () => root.render(<LocaleProvider><FeedbackReportForm /></LocaleProvider>));
  assert.equal(calls.length, 0, "opening feedback never uploads anything");
  assert.equal(send().disabled, true, "empty feedback cannot submit");
  const text = "  中文反馈：retry after a rejected upload  ";
  await edit(text);
  await act(async () => { send().click(); send().click(); });
  assert.equal(calls.length, 1, "rapid clicks cannot submit two concurrent events");
  assert.equal(editor().disabled, true, "submitted content stays fixed while uploading");
  assert.equal(calls[0].kind, "feedback");
  assert.equal(JSON.parse(calls[0].detail).message, text.trim());

  await act(async () => rejectReport(new Error("crash endpoint returned 503 Service Unavailable")));
  assert.ok(document.querySelector('[role="alert"]'), "rejected upload shows an error");
  assert.equal(editor().value, text, "failure preserves the user's unsent feedback");
  assert.equal(send().disabled, false, "user can retry after a failure");
  assert.equal(document.querySelector('[role="status"]'), null, "failure never reports success");

  await act(async () => send().click());
  assert.equal(calls.length, 2);
  assert.equal(JSON.parse(calls[1].detail).message, text.trim(), "retry uses the preserved content");
  await act(async () => resolveReport());
  assert.ok(document.querySelector('[role="status"]'), "success is announced");
  assert.equal(send().disabled, true, "confirmed feedback cannot be sent again accidentally");
  await edit("A new suggestion");
  assert.equal(send().disabled, false, "editing starts a new feedback submission");
  assert.equal(calls.length, 2, "editing never uploads automatically");
  console.log("PASS feedback explicit submission, single in-flight request, failed-upload retry and success state");
} finally {
  await act(async () => root.unmount());
  stub.uninstall();
  dom.window.close();
}
