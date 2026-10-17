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
const legacyBuiltin = { id: "default", name: "Default", description: "", body: "", keepCoding: true, tools: null, scope: "builtin" as const, path: "" };
const builtin: AgentPreset = { ...legacyBuiltin, complete: false, includeRuntimeContext: true };
const review: AgentPreset = { ...builtin, id: "review", name: "Review", scope: "user" };
const complete: AgentPreset = { ...review, id: "complete", name: "Complete", body: "Use this full system prompt.", keepCoding: false, complete: true, includeRuntimeContext: false, tools: [] };
let view: AgentPresetsView = { presets: [builtin, review, complete], current: "default", locked: false, problems: ["broken.md: unknown field tool"] };
const saves: { tabId: string; scope: string; id: string; content: string }[] = [];
const deletes: string[] = [];
let failList = false, failSave = false;
const stub = installDesktopHostStub({
  ListAgentPresets: async () => {
    if (failList) throw new Error("This server does not support agent presets");
    return structuredClone({ ...view, presets: view.presets.map(preset => preset.id === "default" ? legacyBuiltin : preset) });
  },
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
const checkbox = (label: string) => {
  const node = [...rootEl.querySelectorAll("label")].find(item => item.textContent === label);
  assert.ok(node, `Missing checkbox label ${label}`);
  const field = (node.htmlFor ? document.getElementById(node.htmlFor) : node.querySelector('input[type="checkbox"]')) as HTMLInputElement | null;
  assert.equal(field?.type, "checkbox"); return field!;
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
assert.equal(checkbox("Complete system prompt").checked, false, "missing complete field defaults to false when copying an older DTO");
assert.equal(checkbox("Include runtime context").checked, true, "missing runtime context field defaults to true when copying an older DTO");
await click(checkbox("Restrict available tools"));
assert.match(rootEl.textContent!, /No tools are allowed/u);
await click(button("Save"));
assert.match(saves[0].content, /^tools: \[\]$/mu, "restriction with no tools persists []");
assert.equal(saves[0].tabId, "tab-a");
assert.equal(saves[0].scope, "user");
assert.equal(saves[0].id, "default-copy");
assert.doesNotMatch(saves[0].content, /^(?:complete|include-runtime-context):/mu, "default prompt options remain omitted after copying an older DTO");

await click(button("New preset"));
assert.equal(checkbox("Complete system prompt").checked, false, "new presets use an assembled system prompt");
assert.equal(checkbox("Include runtime context").checked, true, "new presets include runtime context");
await fill("Name", "Persona");
await fill("ID", "default");
assert.equal(button("Save").disabled, true, "reserved ID is rejected");
await fill("ID", "persona");
await click(checkbox("Complete system prompt"));
assert.equal(button("Save").disabled, true, "complete mode needs a body even when Keep is selected");
assert.equal(rootEl.querySelector<HTMLTextAreaElement>('textarea[id$="-body"]')!.required, true);
assert.equal(button("Keep").disabled, true);
assert.equal(button("Replace").disabled, true);
assert.equal(button("Keep").getAttribute("aria-pressed"), "true", "complete mode preserves the Keep choice");
assert.match(document.getElementById(checkbox("Complete system prompt").getAttribute("aria-describedby")!)!.textContent!, /core instructions, standing documents, skill instructions, and the model persona/u);
assert.match(document.getElementById(checkbox("Include runtime context").getAttribute("aria-describedby")!)!.textContent!, /Independent.*optional runtime background.*working directory.*permissions are unchanged/u);
await fill("Persona body", "   ");
assert.equal(button("Save").disabled, true, "whitespace does not satisfy complete mode");
await click(checkbox("Include runtime context"));
assert.equal(checkbox("Complete system prompt").checked, true, "runtime context is independent of complete mode");
await click(checkbox("Complete system prompt"));
assert.equal(checkbox("Include runtime context").checked, false, "changing complete mode preserves the runtime context choice");
assert.equal(button("Keep").disabled, false);
assert.equal(button("Keep").getAttribute("aria-pressed"), "true");
assert.equal(button("Save").disabled, false, "leaving complete mode permits an empty body when Keep is selected");
await click(button("Replace"));
assert.equal(button("Save").disabled, true, "replacement needs a body");
await fill("Persona body", "Explain concepts clearly.");
await click(checkbox("Complete system prompt"));
assert.equal(button("Replace").getAttribute("aria-pressed"), "true", "complete mode preserves Replace as well as Keep");
failSave = true;
await click(button("Save"));
assert.match(rootEl.querySelector('[role="alert"]')!.textContent!, /read-only/u);
assert.equal(rootEl.querySelector<HTMLTextAreaElement>('textarea[id$="-body"]')!.value, "Explain concepts clearly.", "failed save retains draft");
assert.equal(checkbox("Complete system prompt").checked, true);
assert.equal(checkbox("Include runtime context").checked, false);
failSave = false;
await click(button("Save"));
assert.match(saves[1].content, /keep-coding-instructions: false/u);
assert.match(saves[1].content, /^complete: true$/mu);
assert.match(saves[1].content, /^include-runtime-context: false$/mu);
assert.doesNotMatch(saves[1].content, /^tools:/mu, "unrestricted tools remain null");
const completeRow = rootEl.querySelector('article[aria-label="Complete"]')!;
assert.match(completeRow.querySelector(".agent-preset-row__summary")!.textContent!, /Complete system prompt/u, "row summary identifies the full system replacement");
assert.doesNotMatch(completeRow.querySelector(".agent-preset-row__summary")!.textContent!, /Coding instructions/u, "complete mode is not summarized as a coding-only replacement");
assert.match(completeRow.textContent!, /Optional runtime context omitted/u);
await click(button("Edit", completeRow));
assert.equal(checkbox("Complete system prompt").checked, true);
assert.equal(checkbox("Include runtime context").checked, false);
assert.equal(checkbox("Restrict available tools").checked, true, "edit retains an explicit empty tools list");
assert.equal(button("Replace").getAttribute("aria-pressed"), "true");
await click(checkbox("Complete system prompt"));
assert.equal(button("Replace").disabled, false);
assert.equal(button("Replace").getAttribute("aria-pressed"), "true", "leaving complete mode restores the saved coding choice");
await click(checkbox("Complete system prompt"));
await click(button("Save"));
assert.match(saves[2].content, /^complete: true$/mu);
assert.match(saves[2].content, /^include-runtime-context: false$/mu);
assert.match(saves[2].content, /^tools: \[\]$/mu);
assert.equal(saves[2].id, "complete");
await click(button("Copy", rootEl.querySelector('article[aria-label="Complete"]')!));
assert.equal(checkbox("Complete system prompt").checked, true);
assert.equal(checkbox("Include runtime context").checked, false);
assert.equal(button("Replace").getAttribute("aria-pressed"), "true");
await click(button("Save"));
assert.equal(saves[3].id, "complete-copy");
assert.match(saves[3].content, /^complete: true$/mu);
assert.match(saves[3].content, /^include-runtime-context: false$/mu);
assert.match(saves[3].content, /^keep-coding-instructions: false$/mu);
assert.match(saves[3].content, /^tools: \[\]$/mu, "copy retains explicit empty tools with prompt options");
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
console.log("Agent preset UI: legacy/new defaults, complete mode validation, independent context, edit/copy retention, empty tools, save errors, confirmed deletion and locked sessions passed.");
