import { act } from "react";

import type { SettingsView } from "../lib/types";

export function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

export function installCanvasMock(win: Window) {
  const canvasElement = (win as Window & typeof globalThis).HTMLCanvasElement;
  Object.defineProperty(canvasElement.prototype, "getContext", {
    configurable: true,
    value(type: string) {
      if (type !== "2d") return null;
      return {
        font: "",
        measureText: () => ({ width: 0 }),
      } as unknown as CanvasRenderingContext2D;
    },
  });
}

export async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

export function baseSettings(): SettingsView {
  return {
    modelSettingsFingerprint: "test-model-settings-revision",
    defaultModel: "",
    plannerModel: "",
    visionModel: "",
    subagentModel: "",
    subagentEffort: "",
    providers: [],
    officialProviders: [],
    providerPresets: [],
    permissions: { mode: "ask", allow: [], ask: [], deny: [] },
    sandbox: { bash: "enforce", network: false, workspaceRoot: "", allowWrite: [], effectiveWorkspaceRoot: "/work", effectiveWriteRoots: ["/work"], shell: "auto", shellCapabilities: [{ id: "bash", variant: "system", available: true, path: "/bin/bash", source: "path" }] },
    network: { proxyMode: "auto", proxyUrl: "", noProxy: "", proxy: { type: "socks5", server: "", port: 0, username: "", password: "" } },
    agent: { temperature: 0, maxSteps: 0, plannerMaxSteps: 0, maxSubagentDepth: 2, maxSubagentConcurrency: 6, maxParallelWriters: 3, systemPrompt: "", reasoningLanguage: "auto", compactRatio: 0.80 },
    desktopLanguage: "en",
    desktopTheme: "auto",
    desktopThemeStyle: "graphite",
    desktopTerminalTheme: "auto",
    closeBehavior: "background",
    sessionExperience: "standard",
    statusBarStyle: "text",
    statusBarItems: ["model", "workspace", "git_branch", "cache", "balance"],
    defaultToolApprovalMode: "auto",
    checkUpdates: true,
    updaterEnabled: true,
    updateChannel: "stable",
    telemetry: true,
    metrics: true,
    configPath: "/tmp/deepzora/config.toml",
    providerKinds: [],
    autoApproveTools: false,
  };
}
