import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import type { AgentPreset, AgentPresetsView } from "../lib/agentPresets";

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Node: dom.window.Node, HTMLElement: dom.window.HTMLElement,
  HTMLInputElement: dom.window.HTMLInputElement, HTMLTextAreaElement: dom.window.HTMLTextAreaElement, Event: dom.window.Event, MouseEvent: dom.window.MouseEvent,
  requestAnimationFrame: dom.window.requestAnimationFrame.bind(dom.window), cancelAnimationFrame: dom.window.cancelAnimationFrame.bind(dom.window),
  localStorage: dom.window.localStorage, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
window.matchMedia = () => ({ matches: true, addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList;
const { createRoot } = await import("react-dom/client");
const { AgentPresetsSettingsPage } = await import("../components/AgentPresetsSettingsPage");
const { AgentPresetChoice } = await import("../components/AgentPresetChoice");
const { ComposerChoice } = await import("../components/ComposerChoice");
const { LocaleProvider } = await import("../lib/i18n");
const { installDesktopHostStub } = await import("./desktopHostStub");
const builtin: AgentPreset = { id: "default", name: "Default", description: "", body: "", keepCoding: true, tools: null, scope: "builtin", path: "" };
const review: AgentPreset = { ...builtin, id: "review", name: "Review", scope: "user" };
let view: AgentPresetsView = { presets: [builtin, review], current: "default", locked: false, problems: ["broken.md: unknown field tool"] };
const saves: { tabId: string; scope: string; id: string; content: string }[] = [];
const deletes: string[] = [];
let failList = false, failSave = false;
const stub = installDesktopHostStub({
  ListAgentPresets: async () => { if (failList) throw new Error("This server does not support agent presets"); return structuredClone(view); },
  SaveAgentPreset: async (tabId: string, scope: string, id: string, content: string) => {
    if (failSave) throw new Error("Preset directory is read-only");
    saves.push({ tabId, scope, id, content });
    return { ...review, id };
  },
  DeleteAgentPreset: async (_tabId: string, _scope: string, id: string) => { deletes.push(id); view = { ...view, presets: view.presets.filter(preset => preset.id !== id) }; },
});
const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);
const flush = () => new Promise<void>(resolve => setTimeout(resolve, 0));
async function settle() { await act(async () => { await flush(); }); }
async function click(button: Element) { await act(async () => { (button as HTMLElement).click(); await flush(); }); }
const button = (text: string, within: ParentNode = rootEl) => {
  const found = [...within.querySelectorAll("button")].find(item => item.textContent === text || item.getAttribute("aria-label") === text);
  assert.ok(found, `Missing button ${text}`); return found;
};
async function fill(label: string, value: string) {
  const node = [...rootEl.querySelectorAll("label")].find(item => item.textContent === label);
  assert.ok(node, `Missing label ${label}`);
  const field = document.getElementById(node.htmlFor) as HTMLInputElement | HTMLTextAreaElement;
  await act(async () => {
    const prototype = field.tagName === "TEXTAREA" ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
await act(async () => { root.render(<LocaleProvider><AgentPresetsSettingsPage tabId="tab-a" workspaceKey="a" /></LocaleProvider>); await flush(); });
await settle();
const builtInRow = rootEl.querySelector('article[aria-label="Default"]')!;
assert.ok(builtInRow);
assert.equal([...builtInRow.querySelectorAll("button")].some(item => item.textContent === "Edit" || item.textContent === "Delete"), false, "built-in has copy only");
assert.match(rootEl.textContent!, /1 preset files could not be loaded/u, "malformed files are surfaced");
await click(button("Copy", builtInRow));
assert.equal(rootEl.querySelector<HTMLInputElement>('input[id$="-id"]')!.value, "default-copy");
assert.equal(document.activeElement, rootEl.querySelector('input[id$="-name"]'), "editor focuses its name");
await click(rootEl.querySelector('input[type="checkbox"]')!);
assert.match(rootEl.textContent!, /No tools are allowed/u);
await click(button("Save"));
assert.match(saves[0].content, /^tools: \[\]$/mu, "restriction with no tools persists []");
assert.equal(saves[0].tabId, "tab-a");
assert.equal(saves[0].scope, "user");
assert.equal(saves[0].id, "default-copy");

await click(button("New preset"));
await fill("Name", "Persona");
await fill("ID", "default");
assert.equal(button("Save").disabled, true, "reserved ID is rejected");
await fill("ID", "persona");
await click(button("Replace"));
assert.equal(button("Save").disabled, true, "replacement needs a body");
await fill("Persona body", "Explain concepts clearly.");
failSave = true;
await click(button("Save"));
assert.match(rootEl.querySelector('[role="alert"]')!.textContent!, /read-only/u);
assert.equal(rootEl.querySelector<HTMLTextAreaElement>('textarea[id$="-body"]')!.value, "Explain concepts clearly.", "failed save retains draft");
failSave = false;
await click(button("Save"));
assert.match(saves[1].content, /keep-coding-instructions: false/u);
assert.doesNotMatch(saves[1].content, /^tools:/mu, "unrestricted tools remain null");
const reviewRow = rootEl.querySelector('article[aria-label="Review"]')!;
await click(button("Delete", reviewRow));
assert.deepEqual(deletes, [], "deletion requires confirmation");
await click(button("Delete Review?", reviewRow));
assert.deepEqual(deletes, ["review"]);

const pending: boolean[] = [];
view = { ...view, presets: [builtin, review], problems: [] };
let releaseChoice: (() => void) | undefined;
const selections: string[][] = [];
stub.commands.SetAgentPresetForTab = async (tabId: string, id: string) => {
  selections.push([tabId, id]);
  await new Promise<void>(resolve => { releaseChoice = resolve; });
  view = { ...view, current: id };
};
window.HTMLElement.prototype.getBoundingClientRect = () => ({ x: 20, y: 40, top: 40, left: 20, right: 220, bottom: 80, width: 200, height: 40, toJSON() {} }) as DOMRect;
const pendingChanged = (value: boolean) => pending.push(value);
await act(async () => { root.render(<LocaleProvider><AgentPresetChoice tabId="tab-a" running={false} onPendingChange={pendingChanged} /></LocaleProvider>); await flush(); });
await click(rootEl.querySelector('button[aria-label="Agent preset: Default"]')!);
await click(document.querySelector('[role="menuitemradio"][data-value="review"]')!);
assert.deepEqual(selections, [["tab-a", "review"]], "picker invokes the real tab-scoped bridge contract");
assert.equal(pending.at(-1), true, "preset mutation blocks submission until settled");
assert.equal(rootEl.querySelector<HTMLButtonElement>('.composer-choice')!.disabled, true);
await act(async () => { releaseChoice!(); await flush(); });
await settle();
assert.equal(pending.at(-1), false);
assert.ok(rootEl.querySelector('button[aria-label="Agent preset: Review"]'), "readback displays the saved preset");
view = { ...view, current: "review", presets: [builtin, review], locked: true, problems: [] };
await act(async () => { root.render(<LocaleProvider><AgentPresetChoice tabId="tab-a" running={false} locked onPendingChange={value => pending.push(value)} /></LocaleProvider>); await flush(); });
await settle();
const lockedButton = rootEl.querySelector<HTMLButtonElement>('button[aria-label="Agent preset: Review · Locked"]');
assert.ok(lockedButton?.disabled, "existing session locks the selected preset");
assert.match(rootEl.textContent!, /Start a new session/u, "lock reason is exposed to assistive technology");
failList = true;
await act(async () => { root.render(<LocaleProvider><AgentPresetChoice key="unsupported" tabId="remote-tab" running={false} onPendingChange={value => pending.push(value)} /></LocaleProvider>); await flush(); });
await settle();
assert.match(rootEl.textContent!, /Agent presets unavailable/u);
assert.ok(button("Retry"), "unsupported server isolates failure and offers retry");

// Shared composer menus must be operable without pointer input.
window.HTMLElement.prototype.getBoundingClientRect = () => ({ x: 20, y: 40, top: 40, left: 20, right: 220, bottom: 80, width: 200, height: 40, toJSON() {} }) as DOMRect;
const choices: string[] = [];
await act(async () => { root.render(<ComposerChoice label="Agent preset" value="default" options={[{ value: "default", label: "Default" }, { value: "review", label: "Review" }]} onPick={value => choices.push(value)} />); });
const trigger = button("Agent preset");
await act(async () => { trigger.focus(); trigger.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })); await new Promise(resolve => requestAnimationFrame(resolve)); });
assert.equal(document.activeElement?.textContent, "Default", "opening by keyboard focuses selected option");
await act(async () => { document.activeElement!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })); });
assert.equal(document.activeElement?.textContent, "Review", "arrow key moves to the next preset");
await click(document.activeElement!);
assert.deepEqual(choices, ["review"]);
assert.equal(document.activeElement, trigger, "selection restores trigger focus");
await click(trigger);
await act(async () => { await new Promise(resolve => requestAnimationFrame(resolve)); document.activeElement!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
assert.equal(document.activeElement, trigger, "Escape closes the menu and restores focus");
assert.equal(trigger.getAttribute("aria-expanded"), "false");
const { useAgentPresets } = await import("../lib/useAgentPresets");
let fetched: ReturnType<typeof useAgentPresets> | undefined;
let resolveA: ((value: AgentPresetsView) => void) | undefined;
let resolveB: ((value: AgentPresetsView) => void) | undefined;
stub.commands.ListAgentPresets = (id: string) => new Promise<AgentPresetsView>(resolve => { if (id === "a") resolveA = resolve; else resolveB = resolve; });
function FetchProbe({ id }: { id: string }) { fetched = useAgentPresets(id); return null; }
await act(async () => root.render(<FetchProbe id="a" />));
await act(async () => root.render(<FetchProbe id="b" />));
assert.equal(fetched?.view, null, "changing tabs hides the old tab's preset view immediately");
await act(async () => { resolveA!({ ...view, current: "old-tab" }); await flush(); });
assert.equal(fetched?.view, null, "late response from the previous tab is ignored");
await act(async () => { resolveB!({ ...view, current: "new-tab" }); await flush(); });
assert.equal(fetched?.view?.current, "new-tab");
await act(async () => root.unmount());
stub.uninstall();
dom.window.close();
console.log("Agent preset UI: builtin copy, explicit empty tools, validation, save errors, confirmed deletion, locked sessions and unsupported servers passed.");
