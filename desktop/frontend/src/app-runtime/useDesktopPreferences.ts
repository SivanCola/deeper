import { useEffect, useState } from "react";
import { useCommittedCommand } from "../lib/useCommittedCommand";
import { useCommittedAsyncCommand } from "../lib/useCommittedAsyncCommand";
import { useConfigLoadWarnings } from "../lib/useConfigLoadWarnings";
import { useI18n } from "../lib/i18n";
import { DEFAULT_STATUS_BAR_ITEMS, normalizeStatusBarItems } from "../lib/statusBarItems";
import { hydrateSessionExperience } from "../lib/sessionExperience";
import { app } from "../lib/bridge";
import { applyPreferencesAppearance, synchronizeDesktopPreferences, type DesktopPreferencesSnapshot } from "./desktopPreferencesAdapter";

export function useDesktopPreferences() {
  const { locale, setPref } = useI18n();
  const warnings = useConfigLoadWarnings();
  const [snapshot, setSnapshot] = useState<DesktopPreferencesSnapshot | null>(null);
  const [startupFailed, setStartupFailed] = useState(false);
  const publish = useCommittedCommand((settings: DesktopPreferencesSnapshot) => {
    setPref(applyPreferencesAppearance(settings));
    if ("configWarnings" in settings) warnings.applySnapshot(settings.configWarnings, settings.configWarningsRevision);
    setSnapshot(settings);
    setStartupFailed(false);
  });
  const synchronize = useCommittedAsyncCommand((provided?: DesktopPreferencesSnapshot | null, loadTheme: boolean = false) => ({ provided, publish, loadTheme }), synchronizeDesktopPreferences);
  const failed = useCommittedCommand((error: unknown) => {
    setStartupFailed(true);
    if (!snapshot) {
      hydrateSessionExperience("standard");
    }
    console.warn("desktop preferences sync failed", error);
  });
  const reload = useCommittedCommand(async (provided?: DesktopPreferencesSnapshot | null, loadTheme = false) => {
    const result = await synchronize(provided, loadTheme);
    if (result.status === "failed") failed(result.error);
  });
  useEffect(() => {
    void reload(undefined, true);
  }, [reload]);
  useEffect(() => { void app.SetTrayLocale(locale).catch(() => {}); }, [locale]);
  return {
    startupUpdateChecksEnabled: snapshot
      ? snapshot.updaterEnabled === true && snapshot.checkUpdates !== false
      : startupFailed ? false : null,
    statusBarStyle: snapshot?.statusBarStyle === "text" ? "text" as const : "icon" as const,
    statusBarItems: snapshot ? normalizeStatusBarItems(snapshot.statusBarItems) : DEFAULT_STATUS_BAR_ITEMS,
    configLoadWarnings: warnings.configLoadWarnings, reloadConfigWarnings: warnings.reload, dismissConfigWarnings: warnings.dismiss,
    reload,
  };
}
