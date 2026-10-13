import { selectSettingsValue } from "./settingsSelectTestUtils";
// Run: tsx src/__tests__/settings-refresh-snapshot.test.tsx

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import {
  SettingsPanel,
} from "../components/SettingsPanel";
import { LocaleProvider } from "../lib/i18n";
import type { AppBindings } from "../lib/bridge";
import type { ProviderModelCapabilityView, ProviderView, SettingsView } from "../lib/types";
import {
  applyTypographyPreferences,
  createDefaultTypographyPreferences,
  getTypographyPreferences,
} from "../lib/typographyPreferences";
import {
  baseSettings,
  flushPromises,
  installCanvasMock,
  waitFor,
} from "../test-support/settingsTestFixtures";
import { installDesktopHostStub } from "./desktopHostStub";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    ok(true, label);
  } else {
    ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

console.log("\nsettings refresh snapshot");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
// React's legacy input-event fallback expects these IE hooks when JSDOM does
// not expose native input event support. The custom threshold editor focuses
// its input on open, so keep that production behavior testable without noise.
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
installCanvasMock(dom.window as unknown as Window);
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.sessionStorage = dom.window.sessionStorage;
window.matchMedia = (() => ({matches: true, addEventListener(){}, removeEventListener(){}})) as any;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
window.scrollTo = () => {};
localStorage.clear();

const regionalTypography = createDefaultTypographyPreferences();
regionalTypography.code = {
  followGlobal: false,
  fontFamily: "jetbrains",
  customFontName: "",
  fontSize: 15,
};
applyTypographyPreferences(regionalTypography);
const regionalCodeFont = document.documentElement.style.getPropertyValue("--typography-code-font");

const settingsSnapshots = [baseSettings("standard")];
let settingsCalls = 0;
let setDisplayModeCalls = 0;
let setSessionExperienceCalls = 0;

const desktopStub = installDesktopHostStub(({
  main: {
    App: {
      Settings: async () => settingsSnapshots[Math.min(settingsCalls++, settingsSnapshots.length - 1)],
      SetDisplayMode: async () => {
        setDisplayModeCalls += 1;
      },
      SetSessionExperience: async () => {
        setSessionExperienceCalls += 1;
      },
    } as Partial<AppBindings> as AppBindings,
  }}).main.App);

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="general"
        desktopPlatform="linux"
        onClose={() => {}}
        onChanged={() => {}}
      />
    </LocaleProvider>,
  );
  await flushPromises();
});

const generalFieldLabels = Array.from(rootEl.querySelectorAll(".settings-section__body > .settings-field .settings-field__label"))
  .map((label) => label.textContent?.trim());
eq(generalFieldLabels[0], "Language", "general settings place language first");
eq(document.querySelectorAll(".step-limit-control").length, 0, "general settings hide executor and planner step-limit controls");
ok(!rootEl.textContent?.includes("Session experience"), "general settings remove the retired session experience field");
ok(!rootEl.textContent?.includes("Conversation density"), "general settings do not render the retired density field");
ok(!rootEl.textContent?.includes("Thinking content"), "general settings do not render the retired reasoning field");
ok(!rootEl.textContent?.includes("After the turn"), "general settings do not render the retired fold field");
ok(!document.body.textContent?.includes("step limit"), "general settings keep automatic progress free of step-limit copy");
ok(!document.body.textContent?.includes("Automatic plan mode"), "general settings omit the retired automatic Plan Mode control");
ok(!document.body.textContent?.includes("planning defaults"), "general settings omit retired automatic Plan Mode copy");

eq(setSessionExperienceCalls, 0, "removed session experience cannot invoke its legacy mutation");
eq(setDisplayModeCalls, 0, "legacy display mode mutation is not invoked");
eq(settingsCalls, 1, "settings panel reads Settings once for its initial snapshot");

await act(async () => {
  root.unmount();
});

// Models > Agent runtime: the compaction preference is directly visible, shows
// the effective token threshold, and reloads the persisted Settings snapshot.
const compactRootEl = document.createElement("div");
document.body.appendChild(compactRootEl);
const compactRoot = createRoot(compactRootEl);
let compactSettings = baseSettings("standard");
delete compactSettings.agent.compactRatio; // Old backends omit the additive field.
compactSettings.agent.effectiveCompactRatio = 0.75;
compactSettings.agent.compactRatioOverridden = true;
compactSettings.defaultModel = "context-provider/context-model";
compactSettings.providers = [{
  name: "context-provider",
  builtIn: false,
  added: true,
  kind: "openai",
  baseUrl: "https://context.example.com/v1",
  models: ["context-model"],
  visionModels: [],

  modelsUrl: "",
  default: "context-model",
  apiKeyEnv: "",
  keySet: false,
  requiresKey: false,
  configured: true,
  balanceUrl: "",
  contextWindow: 100_000,
  reasoningProtocol: "",
  thinking: "",
  supportedEfforts: [],
  defaultEffort: "",
  modelOverrides: [],
}];
let compactRatioCalls: number[] = [];
desktopStub.replaceCommands(({
  main: {
    App: {
      Settings: async () => compactSettings,
      FetchAllProviderModelCatalogs: async () => ({}),
      SetCompactRatio: async (ratio: number) => {
        compactRatioCalls.push(ratio);
        compactSettings = { ...compactSettings, agent: { ...compactSettings.agent, compactRatio: ratio } };
      },
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

await act(async () => {
  compactRoot.render(
    <LocaleProvider>
      <SettingsPanel initialTab="models" desktopPlatform="linux" onClose={() => {}} onChanged={() => {}} />
    </LocaleProvider>,
  );
  await flushPromises();
});
ok(compactRootEl.textContent?.includes("Advanced context management") === false, "compaction preference has no redundant advanced disclosure");
ok(compactRootEl.textContent?.includes("Automatic compaction threshold") === true, "compaction preference is visible without expanding a disclosure");
ok(compactRootEl.textContent?.includes("80,000 tokens") === false, "compact ratio avoids a redundant token estimate under the selected row");
ok(compactRootEl.textContent?.includes("Balance continuity and cache reuse") === true, "compact ratio explains the recommended preset consequence");
ok(compactRootEl.textContent?.includes("effective threshold is 75%") === true, "project override shows the active effective threshold");
const recommendedCompactButton = compactRootEl.querySelector('input[type="radio"][aria-label="80% · Recommended"]') as HTMLInputElement | null;
if (!recommendedCompactButton) throw new Error("recommended compaction preset did not render");
ok(recommendedCompactButton.checked, "saved compact ratio starts selected");
const customCompactButton = compactRootEl.querySelector('input[type="radio"][aria-label="Custom threshold…"]') as HTMLInputElement | null;
if (!customCompactButton) throw new Error("custom compaction threshold option did not render");
ok(customCompactButton.closest(".compact-ratio-choice-list") !== null, "custom compaction is the fourth choice in the shared radio group");
ok(!customCompactButton.checked, "custom choice does not replace the saved preset before editing");
const customCompactInput = compactRootEl.querySelector('input[aria-label="Custom compaction threshold percentage"]') as HTMLInputElement | null;
if (!customCompactInput) throw new Error("inline custom compaction threshold input did not render");
eq(customCompactInput.value, "", "preset selection leaves the inline custom input empty");
eq(customCompactInput.placeholder, "Enter percentage", "inline custom input carries the requested percentage prompt");
ok(customCompactInput.closest(".compact-ratio-choice") !== null, "custom input stays inside the fourth choice row");
ok(customCompactInput.closest(".compact-ratio-choice")?.querySelectorAll("button").length === 0, "custom row has no secondary apply or cancel actions");
const inputValueSetter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
const setCustomCompactInput = (input: HTMLInputElement, value: string) => {
  const previous = input.value;
  inputValueSetter?.call(input, value);
  (input as HTMLInputElement & { _valueTracker?: { setValue: (next: string) => void } })._valueTracker?.setValue(previous);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
};
await act(async () => {
  customCompactInput.focus();
  await flushPromises();
});
ok(recommendedCompactButton.checked, "focusing an empty custom input preserves the saved preset");
ok(!customCompactButton.checked, "an empty custom draft is not announced as the saved selection");
await act(async () => {
  customCompactInput.focus();
  setCustomCompactInput(customCompactInput, "29");
  customCompactInput.blur();
  await flushPromises();
});
eq(compactRatioCalls.length, 0, "out-of-range inline compact ratio is not saved");
eq(customCompactInput.value, "29", "invalid inline value stays available for correction");
eq(customCompactInput.getAttribute("aria-invalid"), "true", "invalid inline value is exposed to assistive technology");
await act(async () => {
  customCompactInput.focus();
  setCustomCompactInput(customCompactInput, "75");
  customCompactInput.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  await flushPromises();
});
eq(compactRatioCalls.length, 1, "Enter saves the inline custom compact ratio once");
eq(compactRatioCalls[0], 0.75, "custom compact ratio converts percentage to fraction");
eq(customCompactInput.value, "75", "saved custom compact ratio stays visible in the inline input");
ok(customCompactButton.checked, "saved custom ratio selects the custom choice");
ok(customCompactButton.getAttribute("aria-label") === "Custom threshold…", "custom choice keeps a stable accessible label after saving");
await act(async () => {
  customCompactInput.focus();
  setCustomCompactInput(customCompactInput, "74");
  customCompactInput.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await flushPromises();
});
eq(compactRatioCalls.length, 1, "Escape cancels a custom compact ratio without saving");
eq(customCompactInput.value, "75", "Escape restores the saved inline custom ratio");
const activeCompactButton = compactRootEl.querySelector('input[type="radio"][aria-label="70% · Active"]') as HTMLInputElement | null;
if (!activeCompactButton) throw new Error("active compaction preset did not render");
await act(async () => {
  activeCompactButton.click();
  await flushPromises();
});
eq(compactRatioCalls.length, 2, "compact ratio preset adds one mutation");
eq(compactRatioCalls[1], 0.7, "compact ratio preset sends the expected fraction");
ok(activeCompactButton.checked, "saved compact ratio is selected after Settings reload");

// Model native mousedown -> blur -> click with a deliberately slow bridge.
let finishCompactSave: (() => void) | undefined;
(desktopStub.commands as AppBindings).SetCompactRatio = async (ratio: number) => {
  compactRatioCalls.push(ratio);
  await new Promise<void>((resolve) => { finishCompactSave = resolve; });
  compactSettings = { ...compactSettings, agent: { ...compactSettings.agent, compactRatio: ratio } };
};
await act(async () => { customCompactInput.focus(); });
await act(async () => { setCustomCompactInput(customCompactInput, "74"); });
const recommendedLabel = recommendedCompactButton.closest("label")!;
const presetDown = new dom.window.MouseEvent("mousedown", { button: 0, bubbles: true, cancelable: true });
await act(async () => {
  recommendedLabel.dispatchEvent(presetDown);
  if (!presetDown.defaultPrevented) customCompactInput.blur();
});
ok(!recommendedCompactButton.disabled, "draft editing does not disable the preset before its click");
await act(async () => { recommendedLabel.click(); });
eq(compactRatioCalls.length, 3, "preset click sends only one mutation while bridge is pending");
eq(compactRatioCalls[2], 0.8, "explicit preset wins over an unsaved custom draft");
await act(async () => { finishCompactSave?.(); await flushPromises(); });
ok(recommendedCompactButton.checked, "clicked preset remains selected after the slow save");
eq(customCompactInput.value, "", "preset click clears the replaced custom draft");
await act(async () => { customCompactInput.focus(); });
await act(async () => { setCustomCompactInput(customCompactInput, "73"); });
await act(async () => {
  const down = new dom.window.MouseEvent("mousedown", { button: 0, bubbles: true, cancelable: true });
  recommendedCompactButton.dispatchEvent(down);
  if (!down.defaultPrevented) customCompactInput.blur();
});
await act(async () => { recommendedCompactButton.click(); });
eq(compactRatioCalls.length, 3, "clicking the current preset cancels editing without saving the draft");
eq(customCompactInput.value, "", "current preset click clears the draft");
await act(async () => { customCompactInput.focus(); });
await act(async () => { setCustomCompactInput(customCompactInput, "72"); });
await act(async () => { customCompactInput.blur(); });
eq(compactRatioCalls.length, 4, "ordinary blur still saves once");
eq(compactRatioCalls[3], 0.72, "ordinary blur persists the draft");
await act(async () => { finishCompactSave?.(); await flushPromises(); });
ok(customCompactButton.checked, "ordinary blur selects the saved custom threshold");

// A rejected save retains the draft for retry while selection stays authoritative.
let rejectCompactSave = true;
(desktopStub.commands as AppBindings).SetCompactRatio = async (ratio: number) => {
  compactRatioCalls.push(ratio);
  if (rejectCompactSave) throw new Error("Compaction save rejected");
  compactSettings = { ...compactSettings, agent: { ...compactSettings.agent, compactRatio: ratio } };
};
await act(async () => { customCompactInput.focus(); });
await act(async () => { setCustomCompactInput(customCompactInput, "74"); });
await act(async () => { customCompactInput.blur(); await flushPromises(); });
eq(customCompactInput.value, "74", "failed save retains the custom draft");
eq(compactSettings.agent.compactRatio, 0.72, "failed save preserves the persisted threshold");
ok(compactRootEl.textContent?.includes("Compaction save rejected"), "failed save displays its error");
rejectCompactSave = false;
await act(async () => { customCompactInput.focus(); });
await act(async () => {
  customCompactInput.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  await flushPromises();
});
eq(compactSettings.agent.compactRatio, 0.74, "Enter retries the retained draft successfully");
eq(customCompactInput.value, "74", "successful retry keeps the saved custom value visible");
ok(!compactRootEl.textContent?.includes("Compaction save rejected"), "successful retry clears the error");
rejectCompactSave = true;
await act(async () => { customCompactInput.focus(); });
await act(async () => { setCustomCompactInput(customCompactInput, "76"); });
await act(async () => { customCompactInput.blur(); await flushPromises(); });
eq(customCompactInput.value, "76", "subsequent rejection also retains the draft");
const callsBeforeCancel = compactRatioCalls.length;
await act(async () => { customCompactInput.focus(); });
await act(async () => {
  customCompactInput.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await flushPromises();
});
eq(customCompactInput.value, "74", "Escape after failure restores the persisted value");
eq(compactRatioCalls.length, callsBeforeCancel, "Escape after failure does not write");

await act(async () => {
  compactRoot.unmount();
});

const retryRootEl = document.createElement("div");
document.body.appendChild(retryRootEl);
const retryRoot = createRoot(retryRootEl);
let failingSettingsCalls = 0;
desktopStub.replaceCommands(({
  main: {
    App: {
      Settings: async () => {
        failingSettingsCalls += 1;
        if (failingSettingsCalls === 1) throw new Error("/Users/example/.deepzora/settings.toml: permission denied");
        return baseSettings("standard");
      },
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

await act(async () => {
  retryRoot.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="general"
        desktopPlatform="linux"
        onClose={() => {}}
        onChanged={() => {}}
      />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("settings load failure", () => Boolean(document.querySelector(".banner--error")));

ok(document.body.textContent?.includes("Settings could not be loaded.") === true, "failed initial settings load shows a visible error");
ok(document.body.textContent?.includes("Loading…") === false, "failed initial settings load stops showing the loading state");

const retryButton = Array.from(document.querySelectorAll("button")).find((button) => button.textContent?.trim() === "Retry") as HTMLButtonElement | undefined;
if (!retryButton) throw new Error("settings retry button did not render");

await act(async () => {
  retryButton.click();
  await flushPromises();
});
await waitFor("settings retry success", () => document.body.textContent?.includes("Desktop & language") === true);

eq(failingSettingsCalls, 2, "settings retry calls Settings again");
ok(document.body.textContent?.includes("Settings could not be loaded.") === false, "settings retry clears the load error");

await act(async () => {
  retryRoot.unmount();
});

const windowsSandboxRootEl = document.createElement("div");
document.body.appendChild(windowsSandboxRootEl);
const windowsSandboxRoot = createRoot(windowsSandboxRootEl);
let windowsSetSandboxCalls = 0;
desktopStub.replaceCommands(({
  main: {
    App: {
      // Deliberately return a stale enforce value: the Windows UI must still
      // render the effective immutable off state.
      Settings: async () => baseSettings("standard"),
      SetSandbox: async () => {
        windowsSetSandboxCalls += 1;
      },
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

await act(async () => {
  windowsSandboxRoot.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="sandbox"
        desktopPlatform="windows"
        onClose={() => {}}
        onChanged={() => {}}
      />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("Windows permission boundary settings", () => document.body.textContent?.includes("Currently allowed directories") === true);

ok(windowsSandboxRootEl.textContent?.includes("/work") === true, "Windows shows the effective workspace write boundary");
ok(!windowsSandboxRootEl.textContent?.includes("This setting is fixed to off."), "Windows removes the legacy Bash sandbox mode control");
eq(windowsSetSandboxCalls, 0, "rendering Windows permission boundaries does not mutate sandbox settings");

await act(async () => {
  windowsSandboxRoot.unmount();
});

const zoomRootEl = document.createElement("div");
document.body.appendChild(zoomRootEl);
const zoomRoot = createRoot(zoomRootEl);
let persistedZoom = 0.5;
const savedZoomFactors: number[] = [];
desktopStub.replaceCommands(({
  main: {
    App: {
      Settings: async () => baseSettings("standard"),
      GetDesktopZoomFactor: async () => persistedZoom,
      SetDesktopZoomFactor: async (factor: number) => {
        persistedZoom = factor;
        savedZoomFactors.push(factor);
      },
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

localStorage.setItem("deepzora-zoom-restart", "1");
await act(async () => {
  zoomRoot.render(
    <LocaleProvider>
      <SettingsPanel
        initialTab="appearance"
        desktopPlatform="windows"
        onClose={() => {}}
        onChanged={() => {}}
      />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("persisted display zoom sync", () => document.querySelector(".zoom-slider__value")?.textContent?.trim() === "50%");

const monoFontSelect = zoomRootEl.querySelector("button.settings-select[aria-labelledby='appearance-mono-font-family-label']") as HTMLButtonElement | null;
if (!monoFontSelect) throw new Error("monospace font selector did not render");
await selectSettingsValue(monoFontSelect, "custom");

const preservedTypography = getTypographyPreferences();
eq(preservedTypography.code.followGlobal, false, "global monospace changes preserve an explicit code-region override");
eq(preservedTypography.code.fontFamily, "jetbrains", "global monospace changes preserve the regional code font choice");
eq(
  document.documentElement.style.getPropertyValue("--typography-code-font"),
  regionalCodeFont,
  "global monospace changes keep the regional code font CSS variable",
);

const resetZoomButton = document.querySelector("button[aria-label='Reset display zoom to 100%']") as HTMLButtonElement | null;
if (!resetZoomButton) throw new Error("display zoom reset button did not render");
await act(async () => {
  resetZoomButton.click();
  await flushPromises();
});
await waitFor("display zoom reset", () => document.querySelector(".zoom-slider__value")?.textContent?.trim() === "100%");

eq(savedZoomFactors.at(-1), 1, "display zoom reset writes the default zoom factor");
eq(localStorage.getItem("deepzora-zoom-restart"), "1", "display zoom reset updates the local restart zoom cache");

await act(async () => {
  zoomRoot.unmount();
});

// Models tab: switching away invalidates an in-flight background discovery so
// its older completion cannot attempt a stale catalog write.
sessionStorage.clear();
const providerRaceRootEl = document.createElement("div");
document.body.appendChild(providerRaceRootEl);
const providerRaceRoot = createRoot(providerRaceRootEl);
const providerRaceSettings = baseSettings("standard");
providerRaceSettings.defaultModel = "race-provider/old-model";
providerRaceSettings.providers = [{
  name: "race-provider",
  builtIn: false,
  added: true,
  kind: "openai",
  baseUrl: "https://old.example.com/v1",
  models: ["old-model"],
  visionModels: [],

  modelsUrl: "",
  default: "missing-default",
  apiKeyEnv: "RACE_PROVIDER_API_KEY",
  headers: { "X-Gateway-Token": "private-gateway-secret" },
  extraBody: {},
  authHeader: false,
  keySet: true,
  requiresKey: true,
  configured: true,
  keySource: "global",
  keySourcePath: "",
  balanceUrl: "",
  contextWindow: 128_000,
  reasoningProtocol: "",
  thinking: "",
  supportedEfforts: [],
  defaultEffort: "",
  modelOverrides: [],
  modelCatalogFingerprint: "old-fingerprint",
}];
let resolveProviderBatch: ((models: Record<string, ProviderModelCapabilityView[]>) => void) | undefined;
const providerBatch = new Promise<Record<string, ProviderModelCapabilityView[]>>((resolve) => {
  resolveProviderBatch = resolve;
});
let providerBatchCalls = 0;
let providerCatalogSaveCalls = 0;
desktopStub.replaceCommands(({
  main: {
    App: {
      Settings: async () => providerRaceSettings,
      FetchAllProviderModelCatalogs: async () => {
        providerBatchCalls += 1;
        return providerBatch;
      },
      SaveProviderModelCatalogs: async () => {
        providerCatalogSaveCalls += 1;
        return ["race-provider"];
      },
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

await act(async () => {
  providerRaceRoot.render(
    <LocaleProvider>
      <SettingsPanel initialTab="models" desktopPlatform="linux" onClose={() => {}} onChanged={() => {}} />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("provider background discovery", () => providerBatchCalls === 1);
const providerRefreshStorageKeys = Array.from({ length: sessionStorage.length }, (_, index) => sessionStorage.key(index) ?? "");
ok(providerRefreshStorageKeys.some((key) => key.includes("old-fingerprint")), "provider auto-refresh cooldown uses the opaque catalog fingerprint");
ok(providerRefreshStorageKeys.every((key) => !key.includes("private-gateway-secret")), "provider auto-refresh cooldown does not persist header secrets");
const accessModelsButton = Array.from(providerRaceRootEl.querySelectorAll(".settings-center__navitem")).find(
  (button) => button.textContent?.trim() === "Model services",
) as HTMLButtonElement | undefined;
if (!accessModelsButton) throw new Error("provider Access subtab did not render");
await act(async () => {
  accessModelsButton.click();
  await flushPromises();
});
await act(async () => {
  resolveProviderBatch?.({ "race-provider": ["old-model", "stale-fetched-model"].map((model) => ({ model, inputModalities: [], state: "unknown", source: "adapter" })) });
  await flushPromises();
});
await waitFor("stale provider discovery completion", () => providerBatchCalls === 1);
eq(providerCatalogSaveCalls, 0, "leaving the models usage tab suppresses the stale background catalog write");

await act(async () => {
  providerRaceRoot.unmount();
});

// Cancelling a freshly fetched model catalog must also clear the success copy
// that asks the user to confirm and save that now-hidden draft.
const providerRefreshCancelRootEl = document.createElement("div");
document.body.appendChild(providerRefreshCancelRootEl);
const providerRefreshCancelRoot = createRoot(providerRefreshCancelRootEl);
const providerRefreshCancelSettings = baseSettings("standard");
providerRefreshCancelSettings.defaultModel = "deepseek/deepseek-v4-flash";
providerRefreshCancelSettings.providers = [{
  name: "deepseek",
  builtIn: true,
  added: true,
  kind: "anthropic",
  baseUrl: "https://api.deepseek.com/anthropic",
  models: ["deepseek-v4-flash"],
  visionModels: [],

  visionCapability: "unsupported",
  modelsUrl: "https://api.deepseek.com/models",
  default: "deepseek-v4-flash",
  apiKeyEnv: "DEEPSEEK_API_KEY",
  keySet: true,
  requiresKey: true,
  configured: true,
  balanceUrl: "https://api.deepseek.com/user/balance",
  contextWindow: 1_000_000,
  reasoningProtocol: "",
  thinking: "enabled",
  webSearch: true,
  serverWebSearchCapability: true,
  supportedEfforts: [],
  defaultEffort: "",
}];
desktopStub.replaceCommands(({
  main: {
    App: {
      Settings: async () => providerRefreshCancelSettings,
      FetchAllProviderModelCatalogs: async () => ({}),
      FetchProviderModelCatalog: async () => ["deepseek-v4-flash", "deepseek-v4-pro"].map((model) => ({ model, inputModalities: ["text"], state: "unsupported", source: "adapter" })),
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

await act(async () => {
  providerRefreshCancelRoot.render(
    <LocaleProvider>
      <SettingsPanel initialTab="models" desktopPlatform="linux" onClose={() => {}} onChanged={() => {}} />
    </LocaleProvider>,
  );
  await flushPromises();
});
const providerRefreshCancelAccessButton = Array.from(providerRefreshCancelRootEl.querySelectorAll(".settings-center__navitem")).find(
  (button) => button.textContent?.trim() === "Model services",
) as HTMLButtonElement | undefined;
if (!providerRefreshCancelAccessButton) throw new Error("provider refresh cancel Access subtab did not render");
await act(async () => {
  providerRefreshCancelAccessButton.click();
  await flushPromises();
});
const providerRefreshCancelButton = Array.from(providerRefreshCancelRootEl.querySelectorAll("button")).find(
  (button) => button.getAttribute("aria-label") === "Refresh models",
) as HTMLButtonElement | undefined;
if (!providerRefreshCancelButton) throw new Error("provider refresh action did not render");
await act(async () => {
  providerRefreshCancelButton.click();
  await flushPromises();
});
await waitFor("provider model discovery", () => providerRefreshCancelRootEl.textContent?.includes("deepseek-v4-pro") === true);
const providerModelDraftCancelButton = providerRefreshCancelRootEl.querySelector<HTMLButtonElement>('.provider-editor-footer button');
if (!providerModelDraftCancelButton) throw new Error("provider draft cancel action did not render");
await act(async () => { providerModelDraftCancelButton.click(); await flushPromises(); });
ok(!providerRefreshCancelRootEl.textContent?.includes("deepseek-v4-pro"), "cancelling discovery discards fetched candidates");
ok(providerRefreshCancelSettings.providers[0].models.length === 1, "cancelling discovery preserves configured models");
await act(async () => {
  providerRefreshCancelRoot.unmount();
});

dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
