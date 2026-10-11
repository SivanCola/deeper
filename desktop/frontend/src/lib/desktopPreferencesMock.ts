import type { SettingsView } from "./types";
import { normalizeStatusBarItems } from "./statusBarItems";
import { applyMockSessionExperience } from "./sessionExperienceMock";

/** Browser fixtures share one preference snapshot . */
export function createDesktopPreferencesMock(settings: SettingsView) {
  return {
    async SetCloseBehavior(mode: string) {
      settings.closeBehavior = mode === "quit" ? "quit" : "background";
    },
    async SetStatusBarStyle(style: string) {
      settings.statusBarStyle = style === "text" ? "text" : "icon";
    },
    async SetStatusBarItems(items: string[]) {
      settings.statusBarItems = normalizeStatusBarItems(items);
    },
    async SetDesktopLanguage(lang: string) {
      settings.desktopLanguage = lang === "en" || lang === "zh" ? lang : "";
    },
    async SetDesktopCurrency(currency: string) {
      settings.desktopCurrency = currency === "CNY" || currency === "USD" ? currency : "";
    },
    async SetDesktopCheckUpdates(enabled: boolean) {
      settings.checkUpdates = enabled;
    },
    async SetDesktopTelemetry(enabled: boolean) {
      settings.telemetry = enabled;
    },
    async SetDesktopMetrics(enabled: boolean) {
      settings.metrics = enabled;
    },
    async SetDesktopConversationWidth(width: string) { settings.conversationWidth = width; },
    async SetSessionExperience(mode: "standard" | "deep") { applyMockSessionExperience(settings, mode); },
  };
}
