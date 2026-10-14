import assert from "node:assert/strict";
import React, { act } from "react";
import { JSDOM } from "jsdom";
import { installDesktopHostStub } from "./desktopHostStub";
import { baseSettings } from "../test-support/settingsTestFixtures";
import type { ModelSettingsChange, ProviderView } from "../lib/types";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  Node: dom.window.Node, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
const { createRoot } = await import("react-dom/client");
const { ProvidersSection } = await import("../components/SettingsPanel");
const { LocaleProvider } = await import("../lib/i18n");
const root = createRoot(document.getElementById("root")!);
const mutations: ModelSettingsChange[] = [];
installDesktopHostStub({ ApplyModelSettings: async (change: ModelSettingsChange) => {
  mutations.push(change); return { persisted: true, issues: [] };
} } as never);
const provider = (name: string): ProviderView => ({ name, displayName: name, added: true, builtIn: false,
  kind: "openai", baseUrl: "http://localhost/v1", models: [`${name}-first`, `${name}-preferred`],
  default: `${name}-preferred`, keySet: true, apiKeyEnv: "TEST_KEY", visionModels: [], supportedEfforts: [], modelCapabilities: [] } as unknown as ProviderView);
const settings = { ...baseSettings(), providers: [provider("A"), provider("B")] };
let failApply = false;
let starts = 0;
let later = 0;
let finish!: () => void;
const started = new Promise<void>(resolve => { finish = resolve; });
const paint = () => act(async () => root.render(<LocaleProvider><ProvidersSection s={settings} busy={false}
  onboarding onOnboardingComplete={() => { later++; }} onOnboardingStart={async () => { starts++; await started; }}
  apply={async operation => { if (failApply) return false; await operation(); return true; }} /></LocaleProvider>));
const detail = () => document.querySelector<HTMLElement>('.provider-connections__detail > div:not([hidden])')!;
const primary = () => detail().querySelector<HTMLButtonElement>('.provider-onboarding-footer .btn--primary')!;
try {
  await paint();
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>(".provider-connections__item")].find(button => button.querySelector("strong")?.textContent === "B")!.click());
  assert.ok(detail().textContent?.includes("B-preferred"));
  assert.ok(!detail().textContent?.includes("A-preferred"), "completion copy names the visible connection, not the first configured one");
  await act(async () => detail().querySelector<HTMLButtonElement>(".provider-onboarding-footer__later")!.click());
  assert.equal(later, 1);
  assert.equal(mutations.length, 0, "start later does not set defaults or create a session");
  const key = detail().querySelector<HTMLInputElement>('input[type="password"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(key, "replacement-test-key");
    key.dispatchEvent(new window.Event("input", { bubbles: true }));
  });
  assert.equal(detail().querySelector(".provider-onboarding-footer"), null, "an unsaved key cannot enter the completed state");
  assert.equal(detail().querySelector(".provider-key-saved"), null, "a replacement key is not announced as saved");
  await act(async () => detail().querySelector<HTMLButtonElement>(".provider-editor-footer .btn--primary")!.click());
  assert.equal(mutations[0].kind, "provider_save");
  assert.equal((mutations[0] as { provider: ProviderView }).provider.default, "B-preferred", "saving a key preserves the selected provider's default model");
  assert.ok(primary());
  settings.providers = settings.providers.map(provider => ({ ...provider }));
  await paint();
  assert.ok(detail().textContent?.includes("B-preferred"), "authoritative save readback cannot reveal the first connection over a manually selected one");
  failApply = true;
  await act(async () => primary().click());
  assert.equal(starts, 0, "a failed default-model write cannot finish onboarding");
  assert.equal(primary().disabled, false, "the user can retry after persistence failure");
  failApply = false;
  await act(async () => primary().click());
  assert.equal(starts, 1);
  assert.equal(primary().disabled, true, "the completion action stays disabled through session creation");
  await act(async () => primary().click());
  assert.equal(starts, 1, "double clicks cannot create another first conversation");
  const preference = mutations.find(change => change.kind === "preference") as { ref: string };
  assert.equal(preference.ref, "B/B-preferred");
  await act(async () => { finish(); await started; });
  assert.equal(primary().disabled, false);
  console.log("PASS selected connection/model, unsaved key guard, save failure, start later and duplicate-click protection");
} finally { await act(async () => root.unmount()); dom.window.close(); }
