import { app } from "../lib/bridge";
import { normalizeLangPref } from "../lib/i18n";
import { normalizeThemePreference, normalizeThemeStyle } from "../lib/theme";
import { applyConfiguredBaseAppearance, applyThemePack, clearThemePack } from "../lib/themePack";
import { applyTerminalThemePreference } from "../lib/terminalTheme";
import { applyConversationWidth } from "../lib/conversationWidth";
import { hydrateSessionExperience } from "../lib/sessionExperience";
import type { CommandAuthority } from "../lib/commandOutcome";
import type { DesktopStartupSettingsView, SettingsView } from "../lib/types";

export type DesktopPreferencesSnapshot = DesktopStartupSettingsView | SettingsView;
export function applyPreferencesAppearance(settings: DesktopPreferencesSnapshot) {
  const theme = normalizeThemePreference(settings.desktopTheme);
  applyConfiguredBaseAppearance(theme, normalizeThemeStyle(settings.desktopThemeStyle));
  applyTerminalThemePreference(settings.desktopTerminalTheme);
  applyConversationWidth(settings.conversationWidth);
  hydrateSessionExperience(settings.sessionExperience);
  return normalizeLangPref(settings.desktopLanguage);
}
type Input = {
  provided?: DesktopPreferencesSnapshot | null;
  loadTheme: boolean;
  publish: (settings: DesktopPreferencesSnapshot) => void;
};

/** Every async boundary is fenced before publishing preferences or theme DOM. */
export async function synchronizeDesktopPreferences(input: Input, authority: CommandAuthority) {
  authority.checkpoint();
  const settings = input.provided ?? await app.DesktopStartupSettings();
  authority.checkpoint();
  input.publish(settings);
  if (!input.loadTheme) return;
  try {
    const { loadThemeExperience, applyExperienceToDOM } = await import("../lib/themeExperience");
    authority.checkpoint();
    const experience = await loadThemeExperience();
    authority.checkpoint();
    applyExperienceToDOM(experience);
  } catch {
    authority.checkpoint();
    try {
      const active = await app.GetActiveThemePack();
      authority.checkpoint();
      if (active?.pack) applyThemePack(active.pack);
      else clearThemePack();
    } catch {
      authority.checkpoint();
      clearThemePack();
    }
  }
}
