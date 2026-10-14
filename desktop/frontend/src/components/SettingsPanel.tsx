import { ErrorMessage } from "./ErrorMessage";
import { FeedbackReportForm } from "./FeedbackReportForm";
import { saveModelSettings, isModelSettingsResult } from "../lib/modelSettings";
import { ModelSettingHelp } from "./ModelSettingHelp";
import { desktopHost } from "../lib/desktopHost";
import { SettingsOptions } from "./SettingsOptions";
import { SettingsSelect } from "./SettingsSelect";
import { providerProtocolLabel, providerProtocolChoices } from "../lib/providerProtocol";
import { providerSupportsServerWebSearch } from "../lib/providerSearch";
export { providerSupportsServerWebSearch } from "../lib/providerSearch";
import { ManagementPageShell } from "./ManagementPageShell";
import { useProviderT as useT } from "../lib/providerSettingsLocale";
import type { ModelDetailsDraft } from "./ProviderModelDialog";
import { ConnectionTitle } from "./ConnectionTitle";
import { connectionBrand, ProviderConnections } from "./ProviderConnections";
import { providerBrandIcons } from "../lib/providerBrandIcons";
import { catalogForPreset } from "../lib/providerCatalog";
import { ProviderCatalogPicker, type CatalogChoice } from "./ProviderCatalogPicker";
import { Eye, EyeOff, Files } from "lucide-react";
import { lazy, memo, Suspense, startTransition, useCallback, useDeferredValue, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { Check, CheckCircle2, ChevronDown, ChevronUp, CircleDollarSign, ExternalLink, Languages, ListChecks, Loader2, Monitor, MoreHorizontal, PanelBottom, Play, Power, RefreshCw, SlidersHorizontal, Trash2, Volume2 } from "lucide-react";
import { asArray } from "../lib/array";
import { ShellInterpreterFields } from "./SettingsShellSupport";
import { RuleList } from "./SettingsRuleList";
import { CopyButton } from "./CopyButton";
import { app, COMPACT_RATIO_MAX_PERCENT, COMPACT_RATIO_MIN_PERCENT, onRuntimeRebuilt, openExternal } from "../lib/bridge";
import { normalizeLangPref, useI18n, type DictKey, type LangPref } from "../lib/i18n";
import { createLatestRequestGate, mergedFetchedProviderModels, mergeProviderModelContextWindows, providerApiKeyEnvForSave, providerDefaultModel, providerIsConfigured, providerModelCandidates, providerModelContextWindowDrafts, providerRequiresKey } from "../lib/providerModels";
import { cachedFetchProviderModelCatalog, invalidateProviderCacheByAPIKeyEnv, shouldSkipAutoRefresh } from "../lib/providerModelCache";
import { providerBaseURLForSave, providerEndpointMismatchDetail, providerRequestURLForCatalogFormatChange, providerRequestURLFromConfig, trimmedBaseURL } from "../lib/providerEndpoint";
import { providerModelVisionCapability, providerVisionModelsForView } from "../lib/providerVisionCapability";
import { useUpdater } from "../lib/useUpdater";
import {
  applyTheme,
  getTheme,
  getThemeStyle,
  normalizeThemePreference,
  normalizeThemeStyle,
  type Theme,
  type ThemeStyle,
} from "../lib/theme";
import {
  applyTerminalThemePreference,
  createTerminalThemeSaveQueue,
  getTerminalThemePreference,
  normalizeTerminalThemePreference,
  type TerminalThemePreference,
} from "../lib/terminalTheme";
import {
  applyConversationWidth,
  getCachedConversationWidth,
  normalizeConversationWidth,
  type ConversationWidth,
} from "../lib/conversationWidth";
import { applyTextSize, getTextSize, type TextSize } from "../lib/textSize";
import { snapZoom, zoomToPercent, saveRestartZoom, getRestartZoom, type ZoomLevel } from "../lib/dpiScale";
import {
  applyFontFamily,
  applyMonoFontFamily,
  getFontFamily,
  getMonoFontFamily,
  getCustomFontName,
  getCustomMonoFontName,
  setCustomFontName,
  setCustomMonoFontName,
  type FontFamily,
  type MonoFontFamily,
} from "../lib/fontFamily";
import { SessionExperienceSettings } from "./SessionExperienceSettings";
import { SettingsField, SettingsSection } from "./SettingsForm";
import { normalizeStatusBarItems, type StatusBarItemId } from "../lib/statusBarItems";
import { normalizeToolApprovalMode } from "../lib/types";
import {
  comboFromKeyboardEvent,
  detectShortcutPlatform,
  formatShortcutCombo,
  onShortcutsChanged,
  resetCustomShortcuts,
  resolvedShortcutCombo,
  saveCustomShortcut,
  shortcutAcceptsCombo,
  shortcutConflict,
  shortcutDefinitions,
  type ShortcutAction,
} from "../lib/keyboardShortcuts";
import type { HookConfigView, HooksSettingsView, NetworkView, ProviderModelCapabilityView, ProviderModelCatalogUpdate, ProviderPresetView, ProviderView, SettingsTab, SettingsView } from "../lib/types";
import { AppearanceOverview } from "./AppearanceOverview";
import { applyConfiguredBaseAppearance, setBaseAppearance } from "../lib/themePack";
import { InlineConfirmButton } from "./InlineConfirmButton";
import { Tooltip } from "./Tooltip";
import { AnchoredPopover } from "./AnchoredPopover";
import { getGenerativePreset, setGenerativePreset, generativeMusic, type GenerativePreset } from "../lib/generative-music";
import { SoundSelect } from "./SoundSelect";
import { getSuccessPreference, setSuccessPreference, getAttentionPreference, setAttentionPreference, getNotificationVolume, setNotificationVolume as persistNotificationVolume, playSuccessChime, playAttentionChime, type SoundWavPref } from "../lib/sound";
import { NotificationVolumeSlider } from "./NotificationVolumeSlider";
import { ShortcutComboDisplay } from "./ShortcutComboDisplay";
import { SettingsNavigation, SETTINGS_NAV_TABS } from "./SettingsNavigation";
import { StatusBarItemsEditor } from "./StatusBarItemsEditor";
import { DesktopCloseBehaviorHint } from "./DesktopCloseBehaviorHint";
export type SettingsInitialFocus =
  | { target: "model-access"; requestId?: number; onboarding?: boolean; providerName?: string; sourceTabId?: string }
  | { target: "model-stats"; requestId: number };
type DesktopPlatform = "darwin" | "windows" | "linux";

const MCPServersSettingsPage = lazy(() => import("./CapabilitiesPanel").then((module) => ({ default: module.MCPServersSettingsPage })));
const RemoteHostsPage = lazy(() => import("./RemoteHostsPage").then((module) => ({ default: module.RemoteHostsPage })));
const SkillsSettingsPage = lazy(() => import("./CapabilitiesPanel").then((module) => ({ default: module.SkillsSettingsPage })));
const PluginsSettingsPage = lazy(() => import("./CapabilitiesPanel").then((module) => ({ default: module.PluginsSettingsPage })));
const MemorySettingsPage = lazy(() => import("./MemoryPanel").then((module) => ({ default: module.MemorySettingsPage })));
const SubagentsSettingsPage = lazy(() => import("./SubagentsPanel").then((module) => ({ default: module.SubagentsSettingsPage })));
const DiagnosticsSettingsPage = lazy(() => import("./DiagnosticsSettingsPage").then((module) => ({ default: module.DiagnosticsSettingsPage })));
const StorageSettingsPage = lazy(() => import("./StorageSettingsPage").then((module) => ({ default: module.StorageSettingsPage })));
const BrowserControlSettingsPage = lazy(() => import("./BrowserControlSettingsPage").then((module) => ({ default: module.BrowserControlSettingsPage })));
const UsageStatsPanel = lazy(() => import("./UsageStatsPanel").then((module) => ({ default: module.UsageStatsPanel })));

// SettingsPanel owns a full-window settings page while the workspace stays mounted.
export function SettingsPanel({
  onClose,
  onOnboardingStart,
  onChanged,
  initialTab,
  onNavigate,
  initialFocus,
  agentRunning = false,
  desktopPlatform,
  onUseSubagent,
  activeWorkspaceKey = "",
}: {
  onClose: () => void;
  onOnboardingStart?: () => Promise<void>;
  onChanged: (settings?: SettingsView | null) => void;
  initialTab?: SettingsTab;
  onNavigate?: (tab: SettingsTab) => void;
  workspaceRef?: { readonly current: HTMLDivElement | null };
  initialFocus?: SettingsInitialFocus;
  agentRunning?: boolean;
  desktopPlatform: DesktopPlatform;
  onUseSubagent: (command: string) => void;
  activeWorkspaceKey?: string;
}) {
  const t = useT();
  const [s, setS] = useState<SettingsView | null>(null);
  const [loadingSettings, setLoadingSettings] = useState(true);
  const [settingsLoadFailed, setSettingsLoadFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);
  const [modelApplication, setModelApplication] = useState<import("../lib/types").ModelSettingsResult | null>(null);
  const modelApplicationSeq = useRef(0);
  const settingsLoadSeq = useRef(0);
  const settingsApplySeq = useRef(0);
  const [theme, setThemeState] = useState<Theme>(getTheme());
  const [themeStyle, setThemeStyleState] = useState<ThemeStyle>(() => getThemeStyle());
  const [terminalTheme, setTerminalThemeState] = useState<TerminalThemePreference>(getTerminalThemePreference());
  const [conversationWidth, setConversationWidth] = useState<ConversationWidth>(() => getCachedConversationWidth());
  const [textSize, setTextSizeState] = useState<TextSize>(getTextSize());
  const [zoomPct, setZoomPct] = useState<number>(zoomToPercent(getRestartZoom()));
  const [fontFamily, setFontFamilyState] = useState<FontFamily>(getFontFamily());
  const [monoFontFamily, setMonoFontFamilyState] = useState<MonoFontFamily>(getMonoFontFamily());
  const [customFontName, setCustomFontNameState] = useState<string>(getCustomFontName());
  const [customMonoFontName, setCustomMonoFontNameState] = useState<string>(getCustomMonoFontName());
  const [tab, setTab] = useState<SettingsTab>(initialTab ?? "general");
  const settingsContentRef = useRef<HTMLElement>(null);
  const pendingSubagentCommandRef = useRef<string | null>(null);
  const requestClose = useCallback(() => {
    const command = pendingSubagentCommandRef.current;
    pendingSubagentCommandRef.current = null;
    onClose();
    if (command) onUseSubagent(command);
  }, [onClose, onUseSubagent]);
  const zoomSaveSeq = useRef(0);
  const terminalThemeSaveSeq = useRef(0);
  const terminalThemeSavePending = useRef(false);
  const terminalThemeSaveQueue = useRef<ReturnType<typeof createTerminalThemeSaveQueue> | null>(null);
  if (!terminalThemeSaveQueue.current) {
    terminalThemeSaveQueue.current = createTerminalThemeSaveQueue((next) => app.SetDesktopTerminalTheme(next));
  }

  const reload = useCallback(async () => {
    const seq = ++settingsLoadSeq.current;
    const applicationSeq = ++modelApplicationSeq.current;
    setLoadingSettings(true);
    setSettingsLoadFailed(false);
    try {
      const [view, application] = await Promise.all([
        app.Settings(),
        Promise.resolve().then(() => app.GetModelSettingsApplication()).catch(() => null),
      ]);
      const next = normalizeSettingsView(view);
      if (seq !== settingsLoadSeq.current) return next;
      setS(next);
      if (application && applicationSeq === modelApplicationSeq.current) setModelApplication(application);
      return next;
    } catch {
      if (seq !== settingsLoadSeq.current) return null;
      setSettingsLoadFailed(true);
      return null;
    } finally {
      if (seq === settingsLoadSeq.current) setLoadingSettings(false);
    }
  }, []);
  useEffect(() => {
    void reload();
  }, [reload]);
  const refreshModelApplication = useCallback(async () => {
    const seq = ++modelApplicationSeq.current;
    try {
      const result = await app.GetModelSettingsApplication();
      if (seq === modelApplicationSeq.current) setModelApplication(result);
    } catch { /* Keep the last confirmed status until the next read. */ }
  }, []);
  useEffect(() => {
    const refresh = () => { void refreshModelApplication(); };
    const unsubscribe = onRuntimeRebuilt(refresh);
    window.addEventListener("focus", refresh);
    return () => {
      unsubscribe();
      window.removeEventListener("focus", refresh);
      modelApplicationSeq.current += 1;
    };
  }, [refreshModelApplication]);
  useEffect(() => {
    if (modelApplication?.application !== "pending" && modelApplication?.application !== "failed") return;
    const timer = window.setInterval(() => { void refreshModelApplication(); }, 2000);
    return () => window.clearInterval(timer);
  }, [modelApplication?.application, refreshModelApplication]);
  useEffect(() => {
    if (initialTab) setTab(initialTab);
  }, [initialTab]);
  useEffect(() => {
    if (initialFocus?.target === "model-access") setTab("providers");
    if (initialFocus?.target === "model-stats") setTab("model-stats");
  }, [initialFocus?.target, initialFocus?.requestId]);
  useEffect(() => {
    const content = settingsContentRef.current;
    if (!content) return;
    content.scrollTop = 0;
    content.scrollLeft = 0;
  }, [tab]);
  useEffect(() => {
    if (!s) return;
    const nextTheme = normalizeThemePreference(s.desktopTheme);
    const nextStyle = normalizeThemeStyle(s.desktopThemeStyle);
    setThemeState(nextTheme);
    setThemeStyleState(nextStyle);
    if (!terminalThemeSavePending.current) {
      setTerminalThemeState(applyTerminalThemePreference(s.desktopTerminalTheme));
    }
    setConversationWidth(applyConversationWidth(s.conversationWidth));
  }, [s?.conversationWidth, s?.desktopTheme, s?.desktopThemeStyle, s?.desktopTerminalTheme]);
  useEffect(() => {
    const host = desktopHost();
    if (host.kind === "none" && desktopPlatform !== "windows") return;
    let cancelled = false;
    void (async () => {
      try {
        const persisted = host.kind === "electron"
          ? await host.native.getAppZoom()
          : await app.GetDesktopZoomFactor();
        if (cancelled || typeof persisted !== "number" || !Number.isFinite(persisted)) return;
        const snapped = snapZoom(persisted);
        saveRestartZoom(snapped);
        setZoomPct(zoomToPercent(snapped));
      } catch {
        // Older mocks or startup races can lack the binding; keep the local fallback.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [desktopPlatform]);

  // apply runs a mutation, re-reads settings, and refreshes the topbar/model.
  const pendingSettingsApplies = useRef(0);
  const apply = useCallback(async (fn: () => Promise<unknown>) => {
    const seq = ++settingsApplySeq.current;
    pendingSettingsApplies.current += 1;
    setBusy(true);
    setErr(null);
    setWarning(null);
    try {
      const result = await fn();
      if (seq !== settingsApplySeq.current) return isModelSettingsResult(result) ? result.persisted : true;
      if (isModelSettingsResult(result)) {
        modelApplicationSeq.current += 1;
        setModelApplication(result);
      }
      const next = await reload();
      if (seq !== settingsApplySeq.current) return isModelSettingsResult(result) ? result.persisted : true;
      onChanged(next);
      window.dispatchEvent(new Event("deepzora:model-catalog-changed"));
      if (isModelSettingsResult(result)) {
        if (!result.persisted) throw new Error(result.issues.map(issue => issue.message).join("\n"));
      }
      if (typeof result === "string" && result.trim()) {
        setWarning(result.trim());
      }
      return true;
    } catch (e) {
      if (seq !== settingsApplySeq.current) return false;
      // Settings writes can be two-phase: persistence may succeed before a
      // runtime refresh reports a real boot error. Re-read the authoritative
      // state even on failure so the UI never offers an action that already
      // committed (for example, a DeepSeek protocol upgrade).
      try {
        const next = await reload();
        if (seq !== settingsApplySeq.current) return false;
        onChanged(next);
        window.dispatchEvent(new Event("deepzora:model-catalog-changed"));
      } catch {
        // Keep the original mutation error; it is the actionable failure.
      }
      if (seq !== settingsApplySeq.current) return false;
      setErr(formatSettingsError(e, t));
      return false;
    } finally {
      pendingSettingsApplies.current -= 1;
      setBusy(pendingSettingsApplies.current > 0);
    }
  }, [reload, onChanged, t]);
  const backgroundApply = useCallback(async (fn: () => Promise<void>) => {
    const seq = ++settingsApplySeq.current;
    setErr(null);
    setWarning(null);
    try {
      await fn();
      if (seq !== settingsApplySeq.current) return;
      const next = await reload();
      if (seq !== settingsApplySeq.current) return;
      onChanged(next);
      window.dispatchEvent(new Event("deepzora:model-catalog-changed"));
    } catch (e) {
      if (seq !== settingsApplySeq.current) return;
      setErr(formatSettingsError(e, t));
      try {
        const current = await reload();
        if (seq === settingsApplySeq.current) onChanged(current);
      } catch { /* Keep the original save issue if readback is unavailable. */ }
    }
  }, [reload, onChanged, t]);
  const setTerminalThemePreference = useCallback((next: TerminalThemePreference) => {
    const seq = ++terminalThemeSaveSeq.current;
    const previous = getTerminalThemePreference();
    terminalThemeSavePending.current = true;
    setErr(null);
    setWarning(null);
    applyTerminalThemePreference(next);
    setTerminalThemeState(next);

    void terminalThemeSaveQueue.current!(next)
      .then(async () => {
        if (seq !== terminalThemeSaveSeq.current) return;
        const refreshed = await reload();
        if (seq !== terminalThemeSaveSeq.current) return;
        terminalThemeSavePending.current = false;
        onChanged(refreshed);
      })
      .catch(async (error) => {
        if (seq !== terminalThemeSaveSeq.current) return;
        const refreshed = await reload();
        if (seq !== terminalThemeSaveSeq.current) return;
        const restored = normalizeTerminalThemePreference(refreshed?.desktopTerminalTheme ?? previous);
        applyTerminalThemePreference(restored);
        setTerminalThemeState(restored);
        terminalThemeSavePending.current = false;
        setErr(formatSettingsError(error, t));
        onChanged(refreshed);
      });
  }, [onChanged, reload, t]);
  const setRestartZoom = useCallback(async (zoom: ZoomLevel) => {
    const snapped = snapZoom(zoom);
    const seq = ++zoomSaveSeq.current;
    setErr(null);
    setWarning(null);
    setZoomPct(zoomToPercent(snapped));
    try {
      const host = desktopHost();
      if (host.kind === "electron") await host.native.setAppZoom(snapped);
      else await app.SetDesktopZoomFactor(snapped);
      if (seq === zoomSaveSeq.current) saveRestartZoom(snapped);
    } catch (e) {
      if (seq !== zoomSaveSeq.current) return;
      setErr(formatSettingsError(e, t));
      setZoomPct(zoomToPercent(getRestartZoom()));
    }
  }, [t]);

  const selectTab = (next: SettingsTab) => { setTab(next); onNavigate?.(next); };

  // These pages need SettingsView; capability pages load their own data.
  const needsSettings = tab === "general" || tab === "models" || tab === "providers" || tab === "model-stats" || tab === "subagents" || tab === "network" || tab === "permissions" || tab === "sandbox" || tab === "appearance" || tab === "updates";
  const lazySettingsPageFallback = <div className="empty">{t("settings.loading")}</div>;
  const settingsNavigationItems = useMemo(() => SETTINGS_NAV_TABS.map((id) => ({
    id,
    label: settingsTabLabel(id, t),
    meta: s ? settingsTabMeta(id, s, t) : "",
    searchTerms: id === "general" ? [
      "settings.language", "settings.currency",
      "settings.closeBehavior",
      "settings.defaultToolApprovalMode", "settings.sound", "settings.statusBarStyle", "settings.statusBarItems",
      "settings.hardwareAcceleration", "GPU", "白屏", "闪烁", "渲染",
    ].map((key) => t(key as DictKey)).join(" ") : "",
  })), [s, t]);

  return (
    <ManagementPageShell title={t("settings.title")} className="settings-screen" onBack={requestClose} contentRef={settingsContentRef}
      navigation={<SettingsNavigation items={settingsNavigationItems} activeTab={tab} onSelect={selectTab} />}>
      <div className="settings-page-content">
            {needsSettings && settingsLoadFailed && (
              <div className="banner banner--error settings-load-error" role="alert">
                <span>{t("settings.loadFailed")}</span>
                <button className="btn btn--small" type="button" onClick={() => void reload()}>{t("common.retry")}</button>
              </div>
            )}
            {needsSettings && err && <div className="banner banner--error"><ErrorMessage error={err} /></div>}
            {needsSettings && warning && <div className="banner banner--warning"><ErrorMessage error={warning} /></div>}
            {needsSettings && (tab === "models" || tab === "providers") && modelApplication && (modelApplication.application === "pending" || modelApplication.application === "failed") && (
              <div className="banner banner--warning" role="status">
                <span>{t(modelApplication.application === "pending" ? "settings.models.savedPending" : "settings.models.savedApplyFailed")}</span>
                {modelApplication.issues.map((issue, index) => <span key={`${issue.code}:${index}`}><ErrorMessage error={issue} /></span>)}
                {modelApplication.targets.filter(target => target.application === "failed").map(target => (
                  <button className="btn btn--small" key={target.tabId} type="button" disabled={busy} onClick={() => void apply(() => app.RetryModelSettingsApplication(target.tabId))}>{t("settings.models.applyRetry")}{target.title ? ` · ${target.title}` : ""}</button>
                ))}
              </div>
            )}
            {needsSettings && !s ? (
              loadingSettings ? <div className="empty">{t("settings.loading")}</div> : null
            ) : (
              <>
                {tab === "general" && s && <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}><GeneralSection s={s} busy={busy} apply={apply} agentRunning={agentRunning} /></SettingsPageShell>}
                {(tab === "models" || tab === "providers" || tab === "model-stats") && s && <SettingsPageShell key="model-pages" s={s} tab={tab} busy={busy} apply={apply}><ModelsSection onOpenProviders={() => selectTab("providers")} s={s} busy={busy} apply={apply} backgroundApply={backgroundApply} onboarding={initialFocus?.target === "model-access" && initialFocus.onboarding} focusProvider={initialFocus?.target === "model-access" ? initialFocus.providerName : undefined} focusRequestId={initialFocus?.target === "model-access" ? initialFocus.requestId : undefined} onOnboardingComplete={onClose} onOnboardingStart={onOnboardingStart} subtab={tab === "providers" ? "access" : tab === "model-stats" ? "stats" : "usage"} /></SettingsPageShell>}
                {tab === "mcp" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><MCPServersSettingsPage /></Suspense></SettingsPageShell>}
                {tab === "remote" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><RemoteHostsPage /></Suspense></SettingsPageShell>}
                {tab === "skills" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><SkillsSettingsPage activeWorkspaceKey={activeWorkspaceKey} /></Suspense></SettingsPageShell>}
                {tab === "subagents" && s && <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}><Suspense fallback={lazySettingsPageFallback}><SubagentsSettingsPage s={s} onUseInChat={(command) => {
                  pendingSubagentCommandRef.current = command;
                  requestClose();
                }} /></Suspense></SettingsPageShell>}
                {tab === "plugins" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><PluginsSettingsPage /></Suspense></SettingsPageShell>}
                {tab === "memory" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><MemorySettingsPage /></Suspense></SettingsPageShell>}
                {tab === "hooks" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><HooksSection onChanged={onChanged} /></SettingsPageShell>}
                {tab === "diagnostics" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><DiagnosticsSettingsPage onNavigate={selectTab} /></Suspense></SettingsPageShell>}
                {tab === "shortcuts" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><ShortcutsSection /></SettingsPageShell>}
                {tab === "permissions" && s && <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}><PermissionsSection s={s} busy={busy} apply={apply} /></SettingsPageShell>}
                {tab === "sandbox" && s && <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}><SandboxSection s={s} busy={busy} apply={apply} windows={desktopPlatform === "windows"} /></SettingsPageShell>}
                {tab === "network" && s && <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}><NetworkSection s={s} busy={busy} apply={apply} /></SettingsPageShell>}
                {tab === "appearance" && s && (
                  <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}>
                    <AppearanceOverview
                      theme={theme}
                      themeStyle={themeStyle}
                      terminalTheme={terminalTheme}
                      conversationWidth={conversationWidth}
                      textSize={textSize}
                      showDisplayZoom={desktopHost().kind !== "none" || desktopPlatform === "windows"}
                      zoomPct={zoomPct}
                      fontFamily={fontFamily}
                      monoFontFamily={monoFontFamily}
                      customFontName={customFontName}
                      customMonoFontName={customMonoFontName}
                      onTheme={(nextTheme) => {
                        applyConfiguredBaseAppearance(nextTheme, themeStyle);
                        setThemeState(nextTheme);
                        void apply(() => app.SetDesktopAppearance(nextTheme, themeStyle));
                      }}
                      onConversationWidth={(width) => {
                        applyConversationWidth(width);
                        setConversationWidth(width);
                        void apply(() => app.SetDesktopConversationWidth(width));
                      }}
                      onThemeStyle={(style) => {
                        // AppearanceOverview already persists via ActivateBaseStyle /
                        // experience APIs. Parent only mirrors React + DOM state.
                        applyTheme(getTheme(), style, { persist: false });
                        setThemeStyleState(style);
                        setBaseAppearance(getTheme(), style);
                      }}
                      onTerminalTheme={setTerminalThemePreference}
                      onTextSize={(size) => {
                        applyTextSize(size);
                        setTextSizeState(size);
                      }}
                      onRestartZoom={setRestartZoom}
                      onFontFamily={(font) => {
                        applyFontFamily(font);
                        setFontFamilyState(font);
                      }}
                      onMonoFontFamily={(font) => {
                        applyMonoFontFamily(font);
                        setMonoFontFamilyState(font);
                      }}
                      onCustomFontNameChange={(name) => {
                        setCustomFontNameState(name);
                        setCustomFontName(name);
                        applyFontFamily("custom");
                      }}
                      onCustomMonoFontNameChange={(name) => {
                        setCustomMonoFontNameState(name);
                        setCustomMonoFontName(name);
                        applyMonoFontFamily("custom");
                      }}
                    />
                  </SettingsPageShell>
                )}
                {tab === "storage" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><StorageSettingsPage /></Suspense></SettingsPageShell>}
                {tab === "browser" && <SettingsPageShell key={tab} s={s} tab={tab} busy={false} apply={apply}><Suspense fallback={lazySettingsPageFallback}><BrowserControlSettingsPage /></Suspense></SettingsPageShell>}
                {tab === "updates" && s && (
                  <SettingsPageShell key={tab} s={s} tab={tab} busy={busy} apply={apply}>
                    <AboutSection
                      configPath={s.configPath}
                      shadowedByPath={s.shadowedByPath}
                      checkUpdates={s.checkUpdates}
                      updaterEnabled={s.updaterEnabled === true}
                      telemetry={s.telemetry !== false}
                      metrics={s.metrics !== false}
                      settingsBusy={busy}
                      applySettings={apply}
                    />
                  </SettingsPageShell>
                )}
              </>
            )}
      </div>
    </ManagementPageShell>
  );
}

function SettingsPageShell({ s: _s, tab, children }: { s: SettingsView | null; tab: SettingsTab; busy: boolean; apply: (fn: () => Promise<unknown>) => Promise<boolean>; children: ReactNode }) {
  const t = useT();
  return (
    <div aria-label={settingsTabPageTitle(tab, t)} className={`settings-page settings-page--${settingsPageKind(tab)} settings-page--${tab}`} data-layout={settingsPageLayout(tab)}>
      {children}
    </div>
  );
}

export function settingsPageLayout(tab: SettingsTab): "form" | "list" | "detail" | "tool" {
  if (tab === "providers") return "detail";
  if (["model-stats", "diagnostics", "hooks", "updates"].includes(tab)) return "tool";
  if (["mcp", "remote", "skills", "subagents", "plugins", "memory", "shortcuts"].includes(tab)) return "list";
  return "form";
}

function settingsPageKind(tab: SettingsTab): "form" | "manager" {
  switch (tab) {
    case "providers":
    case "model-stats":
    case "hooks":
    case "diagnostics":
    case "mcp":
    case "remote":
    case "skills":
    case "subagents":
    case "plugins":
    case "memory":
    case "appearance":
      return "manager";
    default:
      return "form";
  }
}


function settingsTabPageTitle(id: SettingsTab, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "mcp": return t("settings.tab.mcp");
    case "skills": return t("settings.tab.skills");
    case "plugins": return t("settings.tab.plugins");
    case "memory": return t("settings.tab.memory");
    case "diagnostics": return t("settings.tab.diagnostics");
    case "shortcuts": return t("settings.tab.shortcuts");
    default: return settingsTabLabel(id, t);
  }
}

type SectionProps = {
  s: SettingsView;
  busy: boolean;
  apply: (fn: () => Promise<unknown>) => Promise<boolean>;
};

type ModelsSectionProps = SectionProps & {
  onOpenProviders?: () => void;
  onboarding?: boolean;
  focusProvider?: string;
  focusRequestId?: number;
  onOnboardingComplete?: () => void;
  onOnboardingStart?: () => Promise<void>;
  backgroundApply: (fn: () => Promise<void>) => Promise<void>;
  subtab: "usage" | "access" | "stats";
};

function settingsTabLabel(id: SettingsTab, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "model-stats": return t("settings.modelTab.stats");
    case "general":
      return t("settings.tab.general");
    case "models":
      return t("settings.models.preferences");
    case "providers":
      return t("settings.models.services");
    case "mcp":
      return t("settings.tab.mcp");
    case "remote":
      return t("settings.tab.remote");
    case "skills":
      return t("settings.tab.skills");
    case "subagents":
      return t("settings.tab.subagents");
    case "plugins":
      return t("settings.tab.plugins");
    case "memory":
      return t("settings.tab.memory");
    case "hooks":
      return t("settings.tab.hooks");
    case "diagnostics":
      return t("settings.tab.diagnostics");
    case "shortcuts":
      return t("settings.tab.shortcuts");
    case "network":
      return t("settings.tab.network");
    case "permissions":
      return t("settings.tab.permissions");
    case "sandbox":
      return t("settings.tab.sandbox");
    case "appearance": return t("settings.tab.appearance");
    case "storage": return t("settings.tab.storage");
    case "browser": return t("settings.tab.browser");
    case "updates":
      return t("settings.tab.updates");
  }
}

function settingsTabMeta(id: SettingsTab, s: SettingsView, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "model-stats": return "";
    case "models":
      return settingsModelMeta(s, t);
    case "general":
      return s.sessionExperience === "deep" ? t("settings.sessionExperience.deep") : t("settings.sessionExperience.standard");
    case "providers":
      return t("settings.providerCount", { n: s.providers.length });
    case "mcp":
      return t("caps.connectorsTab");
    case "remote":
      return t("remote.tabHint");
    case "skills":
      return t("settings.tabSub.skills");
    case "subagents":
      return t("subagents.tabHint");
    case "plugins":
      return t("settings.tabSub.plugins");
    case "memory":
      return t("settings.tabSub.memory");
    case "hooks":
      return t("settings.tabSub.hooks");
    case "diagnostics":
      return t("settings.tabSub.diagnostics");
    case "shortcuts":
      return t("settings.tabSub.shortcuts");
    case "network":
      return proxyModeLabel(normalizeProxyMode(s.network.proxyMode), t);
    case "permissions":
      return "";
    case "sandbox":
      return "";
    case "appearance": return t("settings.appearanceMeta");
    case "storage": return t("settings.storageMeta");
    case "browser": return t("settings.browserMeta");
    case "updates":
      return t("settings.updatesMeta");
  }
}

function settingsModelMeta(s: SettingsView, t: ReturnType<typeof useT>): string {
  const ref = toRef(s.defaultModel, s);
  if (!ref) return t("common.none");
  if (!ref.includes("/")) return ref;
  const [provider, ...modelParts] = ref.split("/");
  const model = modelParts.join("/") || ref;
  const providerView = s.providers.find((p) => p.name === provider);
  return `${modelProviderLabel(provider, providerView, t)} · ${model}`;
}

export function ShortcutsSection() {
  const t = useT();
  const [platform] = useState(() => detectShortcutPlatform());
  const [revision, setRevision] = useState(0);
  const [recording, setRecording] = useState<ShortcutAction | null>(null);
  const [conflict, setConflict] = useState<{ action: ShortcutAction; conflictAction: ShortcutAction } | null>(null);
  const [unsupportedAction, setUnsupportedAction] = useState<ShortcutAction | null>(null);

  useEffect(() => onShortcutsChanged(() => setRevision((value) => value + 1)), []);

  const definitions = shortcutDefinitions();
  const commitShortcut = (action: ShortcutAction, event: ReactKeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setConflict(null);
      setUnsupportedAction(null);
      setRecording(null);
      return;
    }
    const combo = comboFromKeyboardEvent(event.nativeEvent);
    if (!combo) return;
    if (!shortcutAcceptsCombo(action, combo)) {
      // Let the browser move focus before onBlur cancels recording. Updating
      // recording state synchronously here can keep focus on the re-rendered
      // button in WebKit.
      if (event.key === "Tab") {
        const recorder = event.currentTarget;
        queueMicrotask(() => {
          // Native Tab normally moves focus first. If this WebView does not,
          // release focus so the recorder cannot become a keyboard trap.
          if (document.activeElement === recorder) recorder.blur();
        });
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      setConflict(null);
      setUnsupportedAction(action);
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const conflictDefinition = shortcutConflict(action, combo, platform);
    if (conflictDefinition) {
      setUnsupportedAction(null);
      setConflict({ action, conflictAction: conflictDefinition.action });
      return;
    }
    saveCustomShortcut(action, combo);
    setConflict(null);
    setUnsupportedAction(null);
    setRecording(null);
    setRevision((value) => value + 1);
  };

  return (
    <SettingsSection
      title={t("settings.shortcutsTitle")}
      description={t("settings.shortcutsHint")}
      actions={
        <button
          className="chip chip--icon"
          type="button"
          title={t("settings.shortcutsResetAll")}
          aria-label={t("settings.shortcutsResetAll")}
          onClick={() => {
            resetCustomShortcuts();
            setConflict(null);
            setUnsupportedAction(null);
            setRecording(null);
            setRevision((value) => value + 1);
          }}
        >
          <RefreshCw size={14} />
        </button>
      }
    >
      <div className="shortcuts-settings" data-revision={revision}>
        {conflict && (
          <div className="shortcuts-settings__conflict" role="alert">
            {t("settings.shortcutsConflict", {
              action: t(definitions.find((definition) => definition.action === conflict.action)?.labelKey ?? "settings.tab.shortcuts"),
              conflict: t(definitions.find((definition) => definition.action === conflict.conflictAction)?.labelKey ?? "settings.tab.shortcuts"),
            })}
          </div>
        )}
        {unsupportedAction && (
          <div className="shortcuts-settings__conflict" role="alert">
            {t("settings.shortcutsEnterOnly", {
              action: t(definitions.find((definition) => definition.action === unsupportedAction)?.labelKey ?? "settings.tab.shortcuts"),
            })}
          </div>
        )}
        {definitions.map((definition) => {
          const resolved = resolvedShortcutCombo(definition.action, platform);
          const defaultCombo = definition.defaults[platform];
          const display = formatShortcutCombo(resolved, platform);
          const isCustom = formatShortcutCombo(resolved, platform) !== formatShortcutCombo(defaultCombo, platform);
          const isRecording = recording === definition.action;
          return (
            <div className="shortcuts-settings__row" key={definition.action}>
              <div className="shortcuts-settings__copy">
                <div className="shortcuts-settings__label">{t(definition.labelKey)}</div>
                <div className="shortcuts-settings__desc">{t(definition.descriptionKey)}</div>
              </div>
              <div className="shortcuts-settings__control">
                <button
                  className={`shortcuts-settings__key${isRecording ? " shortcuts-settings__key--recording" : ""}${definition.configurable === false ? " shortcuts-settings__key--locked" : ""}`}
                  type="button"
                  data-shortcut-action={definition.action}
                  disabled={definition.configurable === false}
                  aria-label={isRecording ? t("settings.shortcutsRecording") : display}
                  aria-pressed={isRecording}
                  onClick={(event) => {
                    setRecording(definition.action);
                    setConflict(null);
                    setUnsupportedAction(null);
                    // WebKit (the desktop WKWebView) does not focus buttons on
                    // click, and the recorder listens for keys on the button —
                    // without this the recorder never receives any keydown.
                    event.currentTarget.focus();
                  }}
                  onBlur={() => {
                    if (!isRecording) return;
                    setConflict(null);
                    setUnsupportedAction(null);
                    setRecording(null);
                  }}
                  onKeyDown={(event) => isRecording && commitShortcut(definition.action, event)}
                >
                  {isRecording ? t("settings.shortcutsRecording") : <ShortcutComboDisplay combo={resolved} platform={platform} />}
                </button>
                <button
                  className="chip"
                  type="button"
                  disabled={!isCustom}
                  onClick={() => {
                    saveCustomShortcut(definition.action, null);
                    setConflict(null);
                    setUnsupportedAction(null);
                    setRecording(null);
                    setRevision((value) => value + 1);
                  }}
                >
                  {t("settings.shortcutsReset")}
                </button>
              </div>
            </div>
          );
        })}
      </div>
    </SettingsSection>
  );
}

// allRefs flattens providers into "provider/model" refs for the model selectors.
export function allRefs(s: SettingsView): string[] {
  const out: string[] = [];
  for (const p of s.providers) {
    if (!p.added || !providerIsConfigured(p)) continue;
    for (const m of p.models) out.push(`${p.name}/${m}`);
  }
  return out;
}

// toRef normalises a stored model id (a provider name, a bare model, or a ref) to
// a "provider/model" ref so a <select> of refs can show it selected.
export function toRef(model: string, s: SettingsView): string {
  if (!model) return "";
  if (model.includes("/")) return model;
  const byName = s.providers.find((p) => p.name === model);
  if (byName) return `${byName.name}/${byName.default || byName.models[0] || ""}`;
  const byModel = s.providers.find((p) => p.models.includes(model));
  if (byModel) return `${byModel.name}/${model}`;
  return model;
}

const PROXY_MODES = ["auto", "custom", "off"] as const;

// EFFORT_PRESETS is the canonical union of /effort levels the kernel recognises.
// The settings UI uses it for subagent defaults; provider-specific levels are
// inferred by the backend or edited in TOML for rare gateways.
export const EFFORT_PRESETS: readonly string[] = ["low", "medium", "high", "xhigh", "max"];
const COMPACT_RATIO_PRESETS = [
  [0.7, "settings.compactRatioPreset.70", "settings.compactRatioPresetEffect.70"],
  [0.8, "settings.compactRatioPreset.80", "settings.compactRatioPresetEffect.80"],
  [0.85, "settings.compactRatioPreset.85", "settings.compactRatioPresetEffect.85"],
] as const;
const REASONING_PROTOCOLS: readonly string[] = ["", "deepseek", "glm", "kimi-k3", "openai", "none"];
const THINKING_MODES: readonly string[] = ["", "enabled", "disabled", "adaptive"];
const PROXY_TYPES = ["http", "https", "socks5", "socks5h"] as const;
const LANGUAGE_PREFS: LangPref[] = ["", "zh", "en"];

type ProxyMode = (typeof PROXY_MODES)[number];

function normalizeProxyMode(mode: string): ProxyMode {
  switch (mode) {
    case "custom":
      return "custom";
    case "off":
      return "off";
    default:
      return "auto";
  }
}

function normalizeNetworkView(network: NetworkView): NetworkView {
  return { ...network, proxyMode: normalizeProxyMode(network.proxyMode) };
}

function normalizeReasoningProtocol(protocol: string | undefined): string {
  return REASONING_PROTOCOLS.includes(protocol ?? "") ? protocol ?? "" : "";
}

function normalizeThinkingMode(thinking: string | undefined): string {
  const v = String(thinking ?? "").trim().toLowerCase();
  return THINKING_MODES.includes(v) ? v : "";
}

export function providerEditorEffectiveKind(isNewCustomProvider: boolean, kind: string, kinds: string[]): string {
  void isNewCustomProvider;
  const selected = kind.trim();
  return selected || kinds[0] || "openai";
}

function formatProviderHeaders(headers: Record<string, string> | null | undefined): string {
  const entries = Object.entries(headers ?? {})
    .map(([key, value]) => [key.trim(), String(value ?? "").trim()] as const)
    .filter(([key, value]) => key && value)
    .sort(([a], [b]) => a.localeCompare(b));
  return entries.map(([key, value]) => `${key}: ${value}`).join("\n");
}

function parseProviderHeaders(raw: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const colon = trimmed.indexOf(":");
    const eq = trimmed.indexOf("=");
    const cut = colon >= 0 && (eq < 0 || colon < eq) ? colon : eq;
    if (cut <= 0) continue;
    const key = trimmed.slice(0, cut).trim();
    const value = trimmed.slice(cut + 1).trim();
    if (key && value) out[key] = value;
  }
  return out;
}

function sortedJSONValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortedJSONValue);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(value as Record<string, unknown>).sort((a, b) => a.localeCompare(b))) {
      out[key] = sortedJSONValue((value as Record<string, unknown>)[key]);
    }
    return out;
  }
  return value;
}

function formatSettingsError(error: unknown, t: ReturnType<typeof useT>): string {
  const msg = String((error as Error)?.message ?? error ?? "").trim();
  const unknownModel = /^unknown model (.+)$/i.exec(msg);
  if (unknownModel) return t("settings.errorUnknownModel", { model: unknownModel[1] });
  const providerNotAdded = /^model (.+) is not available because provider (.+) is not added$/i.exec(msg);
  if (providerNotAdded) return t("settings.errorModelProviderMissing", { model: providerNotAdded[1], provider: providerNotAdded[2] });
  const providerNoKey = /^model (.+) is not available because provider (.+) has no key$/i.exec(msg);
  if (providerNoKey) return t("settings.errorModelProviderNoKey", { model: providerNoKey[1], provider: providerNoKey[2] });
  if (/^background session is still open; reopen or close it before upgrading the DeepSeek provider protocol$/i.test(msg)) {
    return t("settings.errorProviderDetached");
  }
  const removeAccessBusy = /^finish or cancel active work using (.+) before removing the provider access$/i.exec(msg);
  if (removeAccessBusy) return t("settings.errorRemoveAccessBusy", { provider: removeAccessBusy[1] });
  const removeAccessDetached = /^background session is still using (.+); reopen or close it before removing the provider access$/i.exec(msg);
  if (removeAccessDetached) return t("settings.errorProviderDetached");
  const removeAccessNoFallback = /^remove provider access: (.+) is in use and no other configured provider exists$/i.exec(msg);
  if (removeAccessNoFallback) return t("settings.errorRemoveProviderNoFallback", { provider: removeAccessNoFallback[1] });
  const deleteProviderNoFallback = /^remove provider: (.+) is in use and no other configured provider exists$/i.exec(msg);
  if (deleteProviderNoFallback) return t("settings.errorRemoveProviderNoFallback", { provider: deleteProviderNoFallback[1] });
  const deleteProviderBusy = /^finish or cancel active work using (.+) before deleting the provider$/i.exec(msg);
  if (deleteProviderBusy) return t("settings.errorDeleteProviderBusy", { provider: deleteProviderBusy[1] });
  const deleteProviderDetached = /^background session is still using (.+); reopen or close it before deleting the provider$/i.exec(msg);
  if (deleteProviderDetached) return t("settings.errorProviderDetached");
  const saveBeforeRemoveAccess = /^save current session before removing provider access: (.+)$/is.exec(msg);
  if (saveBeforeRemoveAccess) return t("settings.errorSaveBeforeRemoveAccess", { err: saveBeforeRemoveAccess[1] });
  const saveBeforeDeleteProvider = /^save current session before deleting provider: (.+)$/is.exec(msg);
  if (saveBeforeDeleteProvider) return t("settings.errorSaveBeforeDeleteProvider", { err: saveBeforeDeleteProvider[1] });
  const removeProviderUsed = /^remove provider: (.+) is used by open tabs and no other configured provider exists$/i.exec(msg);
  if (removeProviderUsed) return t("settings.errorRemoveProviderNoFallback", { provider: removeProviderUsed[1] });
  return msg || t("settings.errorUnknown");
}

function validateProviderExtraBodyValue(value: unknown, path = "extra_body", t?: ReturnType<typeof useT>): void {
  if (value === null) {
    throw new Error(t ? t("settings.providerExtraBodyNull", { path }) : `${path} cannot contain null`);
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => validateProviderExtraBodyValue(item, `${path}[${index}]`, t));
    return;
  }
  if (typeof value === "object") {
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      validateProviderExtraBodyValue(child, `${path}.${key}`, t);
    }
  }
}

export function formatProviderExtraBody(extraBody: Record<string, unknown> | null | undefined): string {
  const cleaned: Record<string, unknown> = {};
  for (const [rawKey, value] of Object.entries(extraBody ?? {})) {
    const key = rawKey.trim();
    if (!key || value === undefined) continue;
    cleaned[key] = value;
  }
  if (Object.keys(cleaned).length === 0) return "";
  return JSON.stringify(sortedJSONValue(cleaned), null, 2);
}

export function parseProviderExtraBody(raw: string, t?: ReturnType<typeof useT>): Record<string, unknown> {
  const trimmed = raw.trim();
  if (!trimmed) return {};
  const parsed = JSON.parse(trimmed) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(t ? t("settings.providerExtraBodyObjectRequired") : "extra body must be a JSON object");
  }
  validateProviderExtraBodyValue(parsed, "extra_body", t);
  const out: Record<string, unknown> = {};
  for (const [rawKey, value] of Object.entries(parsed as Record<string, unknown>)) {
    const key = rawKey.trim();
    if (key) out[key] = value;
  }
  return out;
}

export function providerExtraBodyParseError(error: unknown, t: ReturnType<typeof useT>): string {
  if (error instanceof SyntaxError) return t("settings.providerExtraBodyError");
  const message = String((error as Error)?.message ?? error ?? "").trim();
  return message || t("settings.providerExtraBodyError");
}

function providerModelFetchFallbackMessage(error: unknown, t: ReturnType<typeof useT>): string {
  const message = String((error as Error)?.message ?? error);
  if (/\bstatus\s+(401|403)\b/i.test(message)) {
    return t("settings.fetchModelsManualFallbackAuth");
  }
  if (/\bstatus\s+(404|405)\b/i.test(message)) {
    return t("settings.fetchModelsManualFallbackUnsupported");
  }
  if (/\b(status\s+5\d\d|request failed|network|timeout|timed out|connection|deadline|fetch failed)\b/i.test(message)) {
    return t("settings.fetchModelsManualFallbackNetwork");
  }
  if (/\b(decode response|invalid character|unexpected end|unexpected format)\b/i.test(message)) {
    return t("settings.fetchModelsManualFallbackDecode");
  }
  return t("settings.fetchModelsManualFallbackGeneric", { err: message });
}

function normalizeReasoningLanguage(lang: string | undefined): string {
  const v = String(lang ?? "").trim().toLowerCase();
  return v === "zh" || v === "en" ? v : "auto";
}

function normalizeStringMap(value: unknown): Record<string, string> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Record<string, string> = {};
  for (const [rawKey, rawValue] of Object.entries(value as Record<string, unknown>)) {
    const key = rawKey.trim();
    const val = String(rawValue ?? "").trim();
    if (key && val) out[key] = val;
  }
  return out;
}

function normalizeExtraBodyMap(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Record<string, unknown> = {};
  for (const [rawKey, rawValue] of Object.entries(value as Record<string, unknown>)) {
    const key = rawKey.trim();
    if (key && rawValue !== undefined) out[key] = rawValue;
  }
  return out;
}

export function normalizeProviderView(p: ProviderView): ProviderView {
  const visionModels = asArray(p.visionModels);
  const modelCapabilities = asArray(p.modelCapabilities).flatMap((item) => {
    if (!item || typeof item !== "object") return [];
    const raw = item as Partial<ProviderModelCapabilityView>;
    const model = String(raw.model ?? "").trim();
    if (!model) return [];
    return [{
      automaticState: raw.automaticState,
      automaticSource: raw.automaticSource,
      imageInputEnableAllowed: raw.imageInputEnableAllowed,
      imageInputBlockReason: raw.imageInputBlockReason,
      model,
      inputModalities: asArray(raw.inputModalities).map(String),
      state: String(raw.state ?? "unknown"),
      source: String(raw.source ?? "unknown"),
    }];
  });
  const requiresKey = providerRequiresKey(p);
  return {
    ...p,
    name: String(p.name ?? ""),
    baseUrl: String(p.baseUrl ?? ""),
    builtIn: Boolean(p.builtIn),
    added: Boolean(p.added),
    requestUrl: p.requestUrl ?? "",
    models: asArray(p.models),
    visionModels,
    modelCapabilities,

    visionCapability: p.visionCapability === "unsupported" || p.visionCapability === "configurable"
      ? p.visionCapability
      : undefined,
    modelsUrl: p.modelsUrl ?? "",
    headers: normalizeStringMap(p.headers),
    extraBody: normalizeExtraBodyMap(p.extraBody),
    authHeader: Boolean(p.authHeader),
    noProxy: Boolean(p.noProxy),
    reasoningProtocol: normalizeReasoningProtocol(p.reasoningProtocol),
    thinking: normalizeThinkingMode(p.thinking),
    webSearch: Boolean(p.webSearch),
    serverWebSearchCapability: typeof p.serverWebSearchCapability === "boolean"
      ? p.serverWebSearchCapability
      : undefined,
    supportedEfforts: asArray(p.supportedEfforts),
    modelOverrides: asArray(p.modelOverrides),
    requiresKey,
    configured: providerIsConfigured({ ...p, requiresKey }),
    keySource: p.keySource ?? "",
    keySourcePath: p.keySourcePath ?? "",
    modelCatalogFingerprint: p.modelCatalogFingerprint ?? "",
  };
}

type ProviderPresetStatus = NonNullable<ProviderPresetView["status"]> | "partial";

function normalizeProviderPresetStatus(status: ProviderPresetView["status"] | undefined, added: boolean): ProviderPresetStatus {
  if (status === "installed" || status === "installed_modified" || status === "partial" || status === "name_conflict" || status === "similar_existing") return status;
  return added ? "installed" : "available";
}

function normalizeProviderPresetView(p: ProviderPresetView): ProviderPresetView {
  const requiresKey = Boolean(p.requiresKey ?? p.keyEnv);
  const configured = Boolean(p.configured ?? (!requiresKey || p.keySet));
  const status = normalizeProviderPresetStatus(p.status, Boolean(p.added));
  return {
    ...p,
    id: String(p.id ?? "").trim(),
    label: String(p.label ?? "").trim(),
    description: String(p.description ?? "").trim(),
    keyEnv: String(p.keyEnv ?? "").trim(),
    providerNames: asArray(p.providerNames),
    models: asArray(p.models),
    added: Boolean(p.added || status === "installed" || status === "installed_modified" || status === "partial" || status === "name_conflict"),
    status,
    statusProviderNames: asArray(p.statusProviderNames),
    missingProviderNames: asArray(p.missingProviderNames),
    keySet: Boolean(p.keySet),
    requiresKey,
    configured,
    keySource: p.keySource ?? "",
    keySourcePath: p.keySourcePath ?? "",
  };
}

function normalizeSettingsView(view: SettingsView | null | undefined): SettingsView | null {
  if (!view) return null;
  const permissions = view.permissions ?? { mode: "ask", allow: [], ask: [], deny: [] };
  const sandbox = view.sandbox ?? { bash: "enforce", network: false, workspaceRoot: "", allowWrite: [], effectiveWorkspaceRoot: "", effectiveWriteRoots: [], shell: "auto", effectiveShell: "" };
  const network = view.network ?? {
    proxyMode: "auto",
    proxyUrl: "",
    noProxy: "",
    proxy: { type: "socks5", server: "", port: 0, username: "", password: "" },
  };
  const agent = view.agent ?? { temperature: 0, maxSteps: 0, plannerMaxSteps: 0, maxSubagentDepth: 2, maxSubagentConcurrency: 6, maxParallelWriters: 3, systemPrompt: "", reasoningLanguage: "auto", compactRatio: 0.80 };
  agent.plannerMaxSteps = Number.isFinite(agent.plannerMaxSteps) ? Math.max(0, Math.trunc(agent.plannerMaxSteps)) : 0;
  agent.maxSteps = Number.isFinite(agent.maxSteps) ? Math.max(0, Math.trunc(agent.maxSteps)) : 0;
  agent.maxSubagentDepth = Number.isFinite(agent.maxSubagentDepth) && agent.maxSubagentDepth <= 1 ? 1 : 2;
  agent.reasoningLanguage = normalizeReasoningLanguage(agent.reasoningLanguage);
  agent.compactRatio = Number.isFinite(agent.compactRatio) && Number(agent.compactRatio) > 0 ? Number(agent.compactRatio) : 0.80;
  agent.effectiveCompactRatio = Number.isFinite(agent.effectiveCompactRatio) && Number(agent.effectiveCompactRatio) > 0
    ? Number(agent.effectiveCompactRatio)
    : agent.compactRatio;
  agent.compactRatioOverridden = Boolean(agent.compactRatioOverridden);
  return {
    ...view,
    webSearchModel: view.webSearchModel || "auto",
    webSearchModels: asArray(view.webSearchModels).filter((ref): ref is string => typeof ref === "string"),
    providers: asArray(view.providers).map(normalizeProviderView),
    officialProviders: asArray(view.officialProviders).map(normalizeProviderView),
    providerPresets: asArray(view.providerPresets).map(normalizeProviderPresetView).filter((p) => p.id),
    providerKinds: asArray(view.providerKinds),
    permissions: {
      ...permissions,
      allow: asArray(permissions.allow),
      ask: asArray(permissions.ask),
      deny: asArray(permissions.deny),
    },
    sandbox: {
      ...sandbox,
      allowWrite: asArray(sandbox.allowWrite),
      effectiveWorkspaceRoot: String(sandbox.effectiveWorkspaceRoot ?? ""),
      effectiveWriteRoots: asArray(sandbox.effectiveWriteRoots),
      effectiveShell: String(sandbox.effectiveShell ?? sandbox.shell ?? ""),
    },
    network: {
      ...network,
      proxy: network.proxy ?? { type: "socks5", server: "", port: 0, username: "", password: "" },
    },
    agent,
    defaultToolApprovalMode: normalizeToolApprovalMode(view.defaultToolApprovalMode),
    autoApproveTools: Boolean(view.autoApproveTools),
    desktopLanguage: normalizeLangPref(view.desktopLanguage),
    desktopCurrency: normalizeDesktopCurrency(view.desktopCurrency),
    desktopTheme: normalizeThemePreference(view.desktopTheme),
    desktopThemeStyle: normalizeThemeStyle(view.desktopThemeStyle),
    desktopTerminalTheme: normalizeTerminalThemePreference(view.desktopTerminalTheme),
    closeBehavior: normalizeCloseBehavior(view.closeBehavior),
    sessionExperience: view.sessionExperience === "deep" ? "deep" : "standard",
    statusBarStyle: normalizeStatusBarStyle(view.statusBarStyle),
    statusBarItems: normalizeStatusBarItems(view.statusBarItems),
    conversationWidth: normalizeConversationWidth(view.conversationWidth),
    checkUpdates: view.checkUpdates !== false,
    updaterEnabled: view.updaterEnabled === true,
    updateChannel: "stable",
  };
}

type DesktopCurrency = "" | "CNY" | "USD";

function normalizeDesktopCurrency(currency: string | undefined): DesktopCurrency {
  return currency === "CNY" || currency === "USD" ? currency : "";
}

type CloseBehavior = "background" | "quit";

function normalizeCloseBehavior(mode: string | undefined): CloseBehavior {
  return mode === "quit" ? "quit" : "background";
}

type StatusBarStyle = "icon" | "text";
function normalizeStatusBarStyle(style: string | undefined): StatusBarStyle {
  return style === "text" ? "text" : "icon";
}

function statusBarItemLabel(id: StatusBarItemId, t: ReturnType<typeof useT>): string {
  switch (id) {
    case "workspace":
      return t("settings.statusBarItem.workspace");
    case "cache":
      return t("status.cacheLabel");
    case "cache_avg":
      return t("status.cacheAvgLabel");
    case "session_tokens":
      return t("status.sessionTokensLabel");
    case "turn_tokens":
      return t("status.turnTokensLabel");
    case "turn_tps":
      return t("status.tpsLabel");
    case "turn_output_tokens":
      return t("status.outputTokensLabel");
    case "turn_cache_tokens":
      return t("status.cacheTokensLabel");
    case "turn_cost":
      return t("status.turnCostLabel");
    case "session_turns":
      return t("status.sessionTurnsLabel");
    case "context":
      return t("status.ctxLabel");
    case "compact":
      return t("status.compactLabel");
    case "cost":
      return t("status.costLabel");
    case "balance":
      return t("status.balanceLabel");
  }
}

function closeBehaviorLabel(mode: CloseBehavior, t: ReturnType<typeof useT>): string {
  return mode === "quit" ? t("settings.closeBehavior.quit") : t("settings.closeBehavior.background");
}

function providerKindLabel(kind: string, _t: ReturnType<typeof useT>): string {
  return providerProtocolLabel(kind);
}

function providerKindHint(kind: string, t: ReturnType<typeof useT>): string {
  if (kind === "responses" || kind === "dashscope-responses") return providerProtocolLabel(kind);
  return kind === "responses" ? t("settings.catalog.responsesHint") : kind === "anthropic" ? t("settings.providerProtocolAnthropicHint") : t("settings.providerProtocolOpenAIHint");
}

function reasoningProtocolLabel(protocol: string, t: ReturnType<typeof useT>): string {
  switch (protocol) {
    case "deepseek":
      return t("settings.reasoningProtocol.deepseek");
    case "glm": return t("settings.reasoningProtocol.glm");
    case "kimi-k3": return t("settings.reasoningProtocol.kimiK3");
    case "openai":
      return t("settings.reasoningProtocol.openai");
    case "none":
      return t("settings.reasoningProtocol.none");
    default:
      return t("settings.reasoningProtocol.auto");
  }
}

function thinkingModeLabel(mode: string, t: ReturnType<typeof useT>): string {
  switch (mode) {
    case "enabled":
      return t("settings.thinkingMode.enabled");
    case "disabled":
      return t("settings.thinkingMode.disabled");
    case "adaptive":
      return t("settings.thinkingMode.adaptive");
    default:
      return t("settings.thinkingMode.auto");
  }
}
function GeneralSection({ s, busy, apply, agentRunning }: SectionProps & { agentRunning: boolean }) {
  const { setPref } = useI18n();
  const t = useT();
  const closeBehavior = normalizeCloseBehavior(s.closeBehavior);
  const soundPanelId = useId();
  const languagePref = normalizeLangPref(s.desktopLanguage);
  const desktopCurrency = normalizeDesktopCurrency(s.desktopCurrency);
  const [genMusicPreset, setGenMusicPreset] = useState<GenerativePreset>(getGenerativePreset());
  const [soundPref, setSoundPref] = useState<SoundWavPref>(getSuccessPreference());
  const [attentionPref, setAttentionPref] = useState<SoundWavPref>(getAttentionPreference());
  const [notificationVolume, setNotificationVolume] = useState(getNotificationVolume);
  const [soundExpanded, setSoundExpanded] = useState(false);
  const [graphics, setGraphics] = useState<import("../lib/desktopHost").GraphicsSettingsState | null>(null);
  const [graphicsBusy, setGraphicsBusy] = useState(false);
  const [graphicsError, setGraphicsError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    const host = desktopHost();
    if (host.kind === "electron") void host.native.graphics.get().then((value) => { if (active) setGraphics(value); }).catch(() => undefined);
    return () => { active = false; };
  }, []);
  const updateGraphics = (enabled: boolean) => {
    const host = desktopHost();
    if (host.kind !== "electron" || graphicsBusy) return;
    setGraphicsBusy(true); setGraphicsError(null);
    void host.native.graphics.setHardwareAcceleration(enabled).then((value) => { setGraphics(value); }).catch((error: unknown) => { setGraphicsError(error instanceof Error ? error.message : String(error)); }).finally(() => setGraphicsBusy(false));
  };
  const statusBarStyle = normalizeStatusBarStyle(s.statusBarStyle);
  const statusBarItems = normalizeStatusBarItems(s.statusBarItems);
  const soundStatus = summarizeSoundStatus(genMusicPreset, soundPref, attentionPref, notificationVolume);
  const applyStatusBarItems = (items: StatusBarItemId[]) => {
    const contentScrollTop = document.querySelector<HTMLElement>(".settings-center__content")?.scrollTop ?? 0;
    const navScrollTop = document.querySelector<HTMLElement>(".settings-center__nav")?.scrollTop ?? 0;
    const active = document.activeElement;
    if (active instanceof HTMLElement && active.closest(".status-bar-items-editor")) active.blur();
    void apply(() => app.SetStatusBarItems(items)).finally(() => {
      window.scrollTo(0, 0);
      requestAnimationFrame(() => {
        window.scrollTo(0, 0);
        const content = document.querySelector<HTMLElement>(".settings-center__content");
        const nav = document.querySelector<HTMLElement>(".settings-center__nav");
        if (content) content.scrollTop = Math.min(contentScrollTop, Math.max(0, content.scrollHeight - content.clientHeight));
        if (nav) nav.scrollTop = navScrollTop;
      });
    });
  };
  const setLanguage = (next: LangPref) => {
    setPref(next);
    void apply(() => app.SetDesktopLanguage(next));
  };
  return (
    <>
      <SettingsSection title={t("settings.general.sectionAppearance")} description={t("settings.general.sectionAppearanceHint")}>
      <SettingsField label={t("settings.language")} hint={t("settings.languageHint")} icon={<Languages size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {LANGUAGE_PREFS.map((pref) => (
            <button
              key={pref || "auto"}
              className={`set-seg__btn${languagePref === pref ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => setLanguage(pref)}
            >
              {pref === "" ? t("settings.langAuto") : pref === "zh" ? "中文" : "English"}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      <SettingsField label={t("settings.currency")} hint={t("settings.currencyHint")} icon={<CircleDollarSign size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {(["", "CNY", "USD"] as DesktopCurrency[]).map((currency) => (
            <button
              key={currency || "auto"}
              className={`set-seg__btn${desktopCurrency === currency ? " set-seg__btn--on" : ""}`}
              disabled={busy || agentRunning}
              onClick={() => void apply(() => app.SetDesktopCurrency(currency))}
            >
              {currency === "" ? t("settings.currencyAuto") : currency}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      </SettingsSection>

      <SessionExperienceSettings snapshot={s} busy={busy} apply={apply} />

      <SettingsSection title={t("settings.general.sectionSystem")} description={t("settings.general.sectionSystemHint")}>
      {graphics && <SettingsField label={<span className="settings-graphics-label"><span>{t("settings.hardwareAcceleration")}</span>{graphics.restartRequired && graphics.override === "none" && <span className="settings-graphics-status">{t("settings.hardwareAccelerationRestartShort")}</span>}{graphics.override !== "none" && <span className="settings-graphics-status settings-graphics-status--warning">{t("settings.hardwareAccelerationOverride")}</span>}{graphicsError && <span className="settings-graphics-status settings-graphics-status--error" role="alert"><ErrorMessage error={graphicsError} /></span>}</span>} hint={t("settings.hardwareAccelerationHint")} icon={<Monitor size={18} />}>
        <div className="settings-graphics-control">
          <ToggleSegment value={graphics.hardwareAcceleration} disabled={graphicsBusy || !graphics.writable || graphics.override !== "none"} onChange={updateGraphics} />
        </div>
      </SettingsField>}
      <SettingsField label={t("settings.closeBehavior")} hint={<DesktopCloseBehaviorHint backgroundSelected={closeBehavior === "background"} hint={t("settings.closeBehaviorHint")} unavailableHint={t("settings.closeBehaviorUnavailable")} />} icon={<Power size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {(["background", "quit"] as const).map((mode) => (
            <button
              key={mode}
              className={`set-seg__btn${closeBehavior === mode ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => void apply(() => app.SetCloseBehavior(mode))}
            >
              {closeBehaviorLabel(mode, t)}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      <SettingsField label={t("settings.sound")} hint={t("settings.soundHint")} icon={<Volume2 size={18} />} stacked>
        <div className={`settings-sound-editor${soundExpanded ? " settings-sound-editor--expanded" : ""}`}>
          <div className="settings-sound-editor__summary">
            <span className={`settings-sound-editor__status settings-sound-editor__status--${soundStatus}`}>
              {t(`settings.soundStatus.${soundStatus}`)}
            </span>
            <Tooltip label={t(soundExpanded ? "settings.soundCollapse" : "settings.soundExpand")}>
              <button
                type="button"
                className="settings-sound-editor__toggle"
                aria-expanded={soundExpanded}
                aria-controls={soundPanelId}
                aria-label={t(soundExpanded ? "settings.soundCollapse" : "settings.soundExpand")}
                onClick={() => setSoundExpanded((open) => !open)}
              >
                {soundExpanded ? <ChevronUp size={15} aria-hidden="true" /> : <ChevronDown size={15} aria-hidden="true" />}
              </button>
            </Tooltip>
          </div>
          {soundExpanded && (
            <div className="settings-sound-editor__list" id={soundPanelId}>
              <div className="settings-sound-row">
                <span className="settings-sound-row__label">{t("settings.generativeMusic")}</span>
                <GenMusicSelect
                  value={genMusicPreset}
                  onChange={(next) => {
                    setGenMusicPreset(next);
                    setGenerativePreset(next);
                    if (next === "off") {
                      generativeMusic.stop();
                    } else {
                      if (generativeMusic.isRunning) {
                        generativeMusic.setPreset(next);
                      } else if (agentRunning) {
                        generativeMusic.start(next);
                      }
                      generativeMusic.playPreview(next);
                    }
                  }}
                  onPreview={() => { if (genMusicPreset !== "off") generativeMusic.playPreview(genMusicPreset); }}
                  previewDisabled={genMusicPreset === "off"}
                />
              </div>
              <div className="settings-sound-row">
                <span className="settings-sound-row__label">{t("settings.notificationVolume")}</span>
                <NotificationVolumeSlider
                  value={notificationVolume}
                  onChange={(next) => setNotificationVolume(persistNotificationVolume(next))}
                />
              </div>
              <div className="settings-sound-row">
                <span className="settings-sound-row__label">{t("settings.notificationSoundSuccess")}</span>
                <SoundSelect
                  value={soundPref}
                  onChange={(next) => {
                    setSoundPref(next);
                    setSuccessPreference(next);
                    playSuccessChime();
                  }}
                  onPreview={playSuccessChime}
                  previewDisabled={soundPref === "off"}
                />
              </div>
              <div className="settings-sound-row">
                <span className="settings-sound-row__label">{t("settings.notificationSoundAttention")}</span>
                <SoundSelect
                  value={attentionPref}
                  onChange={(next) => {
                    setAttentionPref(next);
                    setAttentionPreference(next);
                    playAttentionChime();
                  }}
                  onPreview={playAttentionChime}
                  previewDisabled={attentionPref === "off"}
                />
              </div>
            </div>
          )}
        </div>
      </SettingsField>
      <SettingsField label={t("settings.statusBarStyle")} hint={t("settings.statusBarStyleHint")} icon={<PanelBottom size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {(["icon", "text"] as const).map((style) => (
            <button
              key={style}
              className={`set-seg__btn${statusBarStyle === style ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => void apply(() => app.SetStatusBarStyle(style))}
            >
              {t(`settings.statusBarStyle.${style}`)}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      <SettingsField label={t("settings.statusBarItems")} hint={t("settings.statusBarItemsHint")} icon={<ListChecks size={18} />} className="status-bar-items-setting" stacked>
        <StatusBarItemsEditor
          items={statusBarItems}
          busy={busy}
          onChange={applyStatusBarItems}
          itemLabel={(id) => statusBarItemLabel(id, t)}
        />
      </SettingsField>
    </SettingsSection>
    </>
  );
}

const GENRE_OPTIONS: { value: GenerativePreset; labelKey: DictKey }[] = [
  { value: "off", labelKey: "settings.generativeMusic.off" },
  { value: "ethereal", labelKey: "settings.generativeMusic.presets.ethereal" },
  { value: "classic", labelKey: "settings.generativeMusic.presets.classic" },
  { value: "digital", labelKey: "settings.generativeMusic.presets.digital" },
  { value: "retro", labelKey: "settings.generativeMusic.presets.retro" },
];

function summarizeSoundStatus(
  music: GenerativePreset,
  success: SoundWavPref,
  attention: SoundWavPref,
  notificationVolume: number,
): "allOff" | "enabled" | "custom" {
  const notificationsAudible = notificationVolume > 0;
  const enabledCount = [
    music !== "off",
    notificationsAudible && success !== "off",
    notificationsAudible && attention !== "off",
  ].filter(Boolean).length;
  if (enabledCount === 0) return "allOff";
  if (enabledCount === 1) return "enabled";
  return "custom";
}

function GenMusicSelect({
  value,
  onChange,
  onPreview,
  previewDisabled,
}: {
  value: GenerativePreset;
  onChange: (v: GenerativePreset) => void;
  onPreview: () => void;
  previewDisabled?: boolean;
}) {
  const t = useT();
  return (
    <div className="sound-select">
      <SettingsSelect value={value} onValueChange={next => onChange(next as GenerativePreset)}
        aria-label={t("settings.generativeMusic")}
        options={GENRE_OPTIONS.map(option => ({ value: option.value, label: t(option.labelKey) }))} />
      {!previewDisabled && (
        <button className="chip chip--icon" type="button" title={t("settings.generativeMusicPreview")} aria-label={t("settings.generativeMusicPreview")} onClick={onPreview}>
          <Play size={13} aria-hidden="true" />
        </button>
      )}

    </div>
  );
}

function NetworkSection({ s, busy, apply }: SectionProps) {
  const t = useT();
  const savedNetwork = normalizeNetworkView(s.network);
  const [draft, setDraft] = useState<NetworkView>(savedNetwork);
  useEffect(() => setDraft(normalizeNetworkView(s.network)), [s.network]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(savedNetwork);
  const setProxy = (next: Partial<NetworkView["proxy"]>) => {
    setDraft({ ...draft, proxy: { ...draft.proxy, ...next } });
  };

  return (
    <SettingsSection
      title={t("settings.tab.network")}

    >
      <SettingsField label={t("settings.proxyMode")}>
        <SettingsOptions layout="field" className="set-seg">
          {PROXY_MODES.map((mode) => (
            <button
              key={mode}
              className={`set-seg__btn${draft.proxyMode === mode ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => setDraft({ ...draft, proxyMode: mode })}
            >
              {proxyModeLabel(mode, t)}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>

      {draft.proxyMode === "custom" && (
        <>
          <SettingsField label={t("settings.proxyType")}>
            <SettingsOptions layout="field" className="set-seg">
              {PROXY_TYPES.map((typ) => (
                <button
                  key={typ}
                  className={`set-seg__btn${draft.proxy.type === typ ? " set-seg__btn--on" : ""}`}
                  disabled={busy}
                  onClick={() => setProxy({ type: typ })}
                >
                  {typ.toUpperCase()}
                </button>
              ))}
            </SettingsOptions>
          </SettingsField>
          <SettingsField label={t("settings.proxyServer")}>
            <div className="settings-inline-controls">
            <input
              className="mem-input set-grow"
              placeholder="127.0.0.1"
              value={draft.proxy.server}
              disabled={busy || !!draft.proxyUrl.trim()}
              onChange={(e) => setProxy({ server: e.target.value })}
            />
            <label className="set-label">{t("settings.proxyPort")}</label>
            <input
              className="mem-input set-narrow"
              placeholder="7890"
              value={draft.proxy.port ? String(draft.proxy.port) : ""}
              disabled={busy || !!draft.proxyUrl.trim()}
              inputMode="numeric"
              onChange={(e) => setProxy({ port: Number(e.target.value) || 0 })}
            />
            </div>
          </SettingsField>
          <SettingsField label={t("settings.proxyUsername")}>
            <div className="settings-inline-controls">
            <input
              className="mem-input set-grow"
              value={draft.proxy.username}
              disabled={busy || !!draft.proxyUrl.trim()}
              onChange={(e) => setProxy({ username: e.target.value })}
            />
            <label className="set-label">{t("settings.proxyPassword")}</label>
            <input
              className="mem-input set-grow"
              type="password"
              value={draft.proxy.password}
              disabled={busy || !!draft.proxyUrl.trim()}
              onChange={(e) => setProxy({ password: e.target.value })}
            />
            </div>
          </SettingsField>
          <SettingsField label={t("settings.proxyUrl")} hint={t("settings.proxyUrlHint")}>
              <input
                className="mem-input set-grow"
                placeholder="socks5://127.0.0.1:7890"
                value={draft.proxyUrl}
                disabled={busy}
                onChange={(e) => setDraft({ ...draft, proxyUrl: e.target.value })}
              />
          </SettingsField>
          <SettingsField label={t("settings.noProxy")}>
            <input
              className="mem-input set-grow"
              placeholder="localhost,127.0.0.1,.local"
              value={draft.noProxy}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, noProxy: e.target.value })}
            />
          </SettingsField>
        </>
      )}
      <div className="settings-save-bar">
        <span role="status">{t(dirty ? "settings.models.unsaved" : "settings.models.saved")}</span>
        <button className="btn btn--small" disabled={busy || !dirty} onClick={() => setDraft(savedNetwork)}>{t("common.cancel")}</button>
        <button className="btn btn--primary btn--small" disabled={busy || !dirty} onClick={() => void apply(() => app.SetNetwork(draft))}>{t("settings.saveNetwork")}</button>
      </div>
    </SettingsSection>
  );
}

function ToggleSegment({
  value,
  disabled,
  onLabel,
  offLabel,
  onChange,
}: {
  value: boolean;
  disabled: boolean;
  onLabel?: string;
  offLabel?: string;
  onChange: (value: boolean) => void;
}) {
  const t = useT();
  return (
    <SettingsOptions className="set-seg">
      <button
        type="button"
        className={`set-seg__btn${value ? " set-seg__btn--on" : ""}`}
        disabled={disabled}
        onClick={() => onChange(true)}
      >
        {onLabel ?? t("settings.toggleOn")}
      </button>
      <button
        type="button"
        className={`set-seg__btn${!value ? " set-seg__btn--on" : ""}`}
        disabled={disabled}
        onClick={() => onChange(false)}
      >
        {offLabel ?? t("settings.toggleOff")}
      </button>
    </SettingsOptions>
  );
}

export function ModelsSection({ s, busy, apply, backgroundApply, subtab, onboarding, focusProvider, focusRequestId, onOnboardingComplete, onOnboardingStart, onOpenProviders }: ModelsSectionProps) {
  const t = useT();
  const autoRefreshKeyRef = useRef("");
  const autoRefreshGenerationRef = useRef(0);
  const refs = useMemo(() => allRefs(s), [s.providers]);
  const defaultRef = toRef(s.defaultModel, s);
  const plannerRef = toRef(s.plannerModel, s);
  const subagentRef = toRef(s.subagentModel, s);
  const subagentOption = modelOptionFromRef(subagentRef, s);
  const subagentOverride = subagentOption?.providerView?.modelOverrides?.find(item => item.model === subagentOption.model);
  const subagentLevels = subagentRef ? (subagentOverride?.supportedEfforts?.length ? subagentOverride.supportedEfforts : subagentOption?.providerView?.supportedEfforts ?? []) : [];

  const visionRef = s.visionModel === "auto" ? "auto" : toRef(s.visionModel, s);
  const plannerSelectRef = plannerRef === defaultRef ? "" : plannerRef;
  const [defaultProvider] = defaultRef.split("/");
  const defaultProviderView = s.providers.find((p) => p.name === defaultProvider);
  const modelIssue = !defaultProviderView
    ? t("settings.modelUnavailable", { ref: defaultRef || t("common.none") })
    : !providerIsConfigured(defaultProviderView)
      ? t("settings.modelNeedsKey", { provider: modelProviderLabel(defaultProvider, defaultProviderView, t) })
      : "";
  const agent = s.agent ?? { temperature: 0, maxSteps: 0, plannerMaxSteps: 0, maxSubagentDepth: 2, maxSubagentConcurrency: 6, maxParallelWriters: 3, systemPrompt: "", reasoningLanguage: "auto", compactRatio: 0.80 };
  const compactRatio = agent.compactRatio ?? 0.80;
  const compactRatioPercent = Math.round(compactRatio * 1000) / 10;
  const compactRatioPreset = COMPACT_RATIO_PRESETS.find(([ratio]) => Math.abs(compactRatio - ratio) < 0.0001);
  const [compactRatioDraft, setCompactRatioDraft] = useState(() => compactRatioPreset ? "" : String(compactRatioPercent));
  const [compactRatioCustomEditing, setCompactRatioCustomEditing] = useState(false);
  const compactRatioCustomInputRef = useRef<HTMLInputElement>(null);
  const compactRatioCancelBlurRef = useRef(false);
  const compactRatioDraftPercent = Number(compactRatioDraft);
  const compactRatioDraftValid = compactRatioDraft !== ""
    && Number.isFinite(compactRatioDraftPercent)
    && compactRatioDraftPercent >= COMPACT_RATIO_MIN_PERCENT
    && compactRatioDraftPercent <= COMPACT_RATIO_MAX_PERCENT;
  const compactRatioImpact = t("settings.compactRatioImpact");
  const compactRatioOverrideHint = agent.compactRatioOverridden
    ? t("settings.compactRatioProjectOverride", { percent: Math.round((agent.effectiveCompactRatio ?? compactRatio) * 100) })
    : "";
  const subagentDepth = Number.isFinite(agent.maxSubagentDepth) && agent.maxSubagentDepth <= 1 ? 1 : 2;
  const subagentConcurrency = Number.isFinite(agent.maxSubagentConcurrency) && agent.maxSubagentConcurrency > 0
    ? Math.max(1, Math.min(32, Math.floor(agent.maxSubagentConcurrency)))
    : 6;
  const parallelWriters = Number.isFinite(agent.maxParallelWriters) && agent.maxParallelWriters > 0
    ? Math.max(1, Math.min(subagentConcurrency, Math.floor(agent.maxParallelWriters)))
    : Math.min(3, subagentConcurrency);
  const visionRefs = useMemo(() => {
    const defaultProviderName = defaultRef.split("/")[0];
    const candidates = refs.filter((ref) => {
      const [provider, ...parts] = ref.split("/");
      const model = parts.join("/");
      const view = s.providers.find((item) => item.name === provider);
      return Boolean(view && providerVisionModelsForView(view).includes(model));
    });
    return candidates.sort((a, b) => {
      const aSame = a.startsWith(`${defaultProviderName}/`);
      const bSame = b.startsWith(`${defaultProviderName}/`);
      return aSame === bSame ? a.localeCompare(b) : aSame ? -1 : 1;
    });
  }, [defaultRef, refs, s.providers]);

  useEffect(() => {
    if (!compactRatioCustomEditing) setCompactRatioDraft(compactRatioPreset ? "" : String(compactRatioPercent));
  }, [compactRatioCustomEditing, compactRatioPercent, compactRatioPreset]);

  const persistCompactRatio = async (ratio: number) => {
    return await apply(() => app.SetCompactRatio(ratio));
  };

  const focusCompactRatioCustom = () => {
    setCompactRatioCustomEditing(true);
    requestAnimationFrame(() => compactRatioCustomInputRef.current?.focus());
  };

  const selectCompactRatioPreset = async (ratio: number) => {
    // A preset click replaces the draft; do not also persist it on blur.
    if (document.activeElement === compactRatioCustomInputRef.current) {
      compactRatioCancelBlurRef.current = true;
      compactRatioCustomInputRef.current?.blur();
    }
    setCompactRatioCustomEditing(false);
    if (Math.abs(compactRatio - ratio) < 0.0001) {
      return;
    }
    await persistCompactRatio(ratio);
  };

  const commitCompactRatioDraft = async (rawValue: string) => {
    const percent = Number(rawValue);
    const valid = rawValue !== ""
      && Number.isFinite(percent)
      && percent >= COMPACT_RATIO_MIN_PERCENT
      && percent <= COMPACT_RATIO_MAX_PERCENT;
    const dirty = valid && Math.abs(percent / 100 - compactRatio) > 0.0001;
    if (!valid && rawValue !== "") return;
    if (!valid || busy) {
      setCompactRatioCustomEditing(false);
      return;
    }
    if (dirty && !await persistCompactRatio(percent / 100)) return;
    setCompactRatioCustomEditing(false);
  };

  useEffect(() => {
    const generation = ++autoRefreshGenerationRef.current;
    let cancelled = false;
    const stale = () => cancelled || autoRefreshGenerationRef.current !== generation;
    if (subtab !== "usage") return;
    const groups = providerAccessGroups(s.providers.filter((p) => p.added), t);
    const candidates = groups
      .map((group) => {
        const provider = group.providers.find((p) => providerIsConfigured(p) && p.baseUrl);
        return provider ? { group, provider } : null;
      })
      .filter((item): item is { group: ProviderAccessGroup; provider: ProviderView } => Boolean(item));
    // The backend token covers provider identity, current catalog, headers,
    // and credential revision without persisting sensitive header values in
    // sessionStorage. Older payloads without the token simply skip this
    // opportunistic background refresh; manual refresh remains available.
    if (candidates.some(({ provider }) => !provider.modelCatalogFingerprint?.trim())) return;
    const refreshKey = candidates.map(({ group, provider }) => JSON.stringify([
      group.id,
      provider.modelCatalogFingerprint!.trim(),
    ])).join("|");
    if (!refreshKey || autoRefreshKeyRef.current === refreshKey) return;

    // Session-level cooldown per provider set: reopening the panel does not
    // refetch the same providers, while a changed set refreshes immediately.
    const autoRefreshStorageKey = `settings-auto-refresh-at:${refreshKey}`;
    const lastAutoRefresh = sessionStorage.getItem(autoRefreshStorageKey);
    if (lastAutoRefresh && Date.now() - Number(lastAutoRefresh) < 30_000) return;

    // Respect slow network hints; background model-list refresh can wait.
    if (shouldSkipAutoRefresh()) return;

    autoRefreshKeyRef.current = refreshKey;
    sessionStorage.setItem(autoRefreshStorageKey, String(Date.now()));

    void backgroundApply(async () => {
      // Batch-fetch models for all candidates in one round-trip.
      const providersToFetch = candidates.map((c) => c.provider).filter((p) => p.models && p.models.length > 0);
      let batchResults: Record<string, ProviderModelCapabilityView[]> = {};
      try {
        batchResults = await app.FetchAllProviderModelCatalogs(providersToFetch);
      } catch {
        // Batch failed entirely — fall back to per-provider cached calls below.
      }
      if (stale()) return;

      const updates: ProviderModelCatalogUpdate[] = [];
      for (const { provider } of candidates) {
        if (stale()) return;
        if (!provider.models || provider.models.length === 0) continue;
        try {
          const catalog = batchResults[provider.name]
            ?? await cachedFetchProviderModelCatalog((p) => app.FetchProviderModelCatalog(p), provider);
          const fetched = catalog.map((item) => item.model);
          if (stale()) return;
          if (!fetched || fetched.length === 0) continue;
          const models = mergedFetchedProviderModels(provider.models, fetched, { preserveCurated: true });
          const currentDefault = providerDefaultModel(provider.default, models);
          if (sameStringList(provider.models, models) && provider.default === currentDefault) continue;
          const expectedFingerprint = provider.modelCatalogFingerprint?.trim() ?? "";
          if (!expectedFingerprint) continue;
          updates.push({ name: provider.name, expectedFingerprint, models, default: currentDefault, modelCapabilities: catalog.map(({model,inputModalities}) => ({model,inputModalities})) });
        } catch {
          // Background discovery is opportunistic; manual refresh shows errors.
        }
      }
      if (updates.length > 0) {
        try {
          if (stale()) return;
          // Compare and apply narrow catalog updates in one transaction.
          await saveModelSettings(s, {kind: "catalogs", catalogs: updates});
        } catch {
          // Background discovery is opportunistic; explicit edits show errors.
        }
      }
    });
    return () => {
      cancelled = true;
      if (autoRefreshGenerationRef.current === generation) autoRefreshGenerationRef.current += 1;
    };
  }, [backgroundApply, s.providers, subtab, t]);

  return (
    <>
      {subtab === "usage" ? (
        <div className="model-preferences">
          <SettingsSection className="model-assignment-section" title={t("settings.models.preferences")} description={t("settings.models.preferencesApplyHint")}>
            <div className="model-assignment-head"><span>{t("settings.modelPurpose")}</span><span>{t("settings.modelUsage")}</span><span>{t("settings.modelConnection")}</span></div>
            <SettingsField className="model-assignment-row" label={<ModelSettingHelp label={t("settings.defaultModel")} text={t("providerUI.defaultModelHelp")} />}>
              <ModelPicker
                s={s}
                refs={refs}
                value={toRef(s.defaultModel, s)}
                disabled={busy}
                ariaLabel={t("settings.defaultModel")}
                onPick={(ref) => void apply(() => saveModelSettings(s, {kind: "preference", field: "default", ref: ref}))}
              />
            <span className="model-assignment-connection">{toRef(s.defaultModel, s) && toRef(s.defaultModel, s) !== "auto" ? modelOptionMeta(modelOptionFromRef(toRef(s.defaultModel, s), s)!, t) : t("settings.connectionAutomatic")}</span>
            </SettingsField>

            <SettingsField className="model-assignment-row" label={<ModelSettingHelp label={t("settings.plannerModel")} text={t("providerUI.plannerModelHelp")} />}>
              <ModelPicker
                s={s}
                refs={refs}
                value={plannerSelectRef}
                disabled={busy}
                ariaLabel={t("settings.plannerModel")}
                includeSameDefault
                onPick={(ref) => void apply(() => saveModelSettings(s, {kind: "preference", field: "planner", ref: ref}))}
              />
            <span className="model-assignment-connection">{plannerSelectRef && plannerSelectRef !== "auto" ? modelOptionMeta(modelOptionFromRef(plannerSelectRef, s)!, t) : t("settings.connectionFollowSession")}</span>
            </SettingsField>

            <SettingsField className="model-assignment-row" label={<ModelSettingHelp label={t("settings.imageUnderstandingModel")} text={t("providerUI.visionModelHelp")} />}>
              <ModelPicker
                s={s}
                refs={visionRefs}
                value={visionRef}
                disabled={busy}
                ariaLabel={t("settings.imageUnderstandingModel")}
                emptyOptionLabel={t("common.none")}
                autoOptionLabel={t("common.auto")}
                onPick={(ref) => void apply(() => saveModelSettings(s, {kind: "preference", field: "vision", ref: ref}))}
              />
            <span className="model-assignment-connection">{visionRef && visionRef !== "auto" ? modelOptionMeta(modelOptionFromRef(visionRef, s)!, t) : (visionRef === "auto" ? t("settings.connectionAutomatic") : t("common.none"))}</span>
            </SettingsField>


            <SettingsField className="model-assignment-row" label={<ModelSettingHelp label={t("settings.webSearchModel")} text={t("providerUI.searchModelHelp")} />}>
              <div className="web-search-assignment-control">
                <ModelPicker s={s} refs={s.webSearchModels ?? []} value={s.webSearchModel || "auto"}
                  disabled={busy} ariaLabel={t("settings.webSearchModel")} autoOptionLabel={t("common.auto")}
                  onPick={(ref) => void apply(() => saveModelSettings(s, {kind: "preference", field: "search", ref: ref}))} />
                {(s.webSearchModelStatus === "invalid" || Boolean(s.webSearchModel && s.webSearchModel !== "auto" && !(s.webSearchModels ?? []).includes(s.webSearchModel))) && <p role="status" className="web-search-assignment-hint">{t("settings.webSearchModelUnavailable")} {s.webSearchModelReason}</p>}
                {s.webSearchModelOverridden && <p className="web-search-assignment-hint">{t("settings.webSearchModelOverride", { model: s.effectiveWebSearchModel || t("common.auto") })}</p>}
                {(s.webSearchModels ?? []).length === 0 && <p className="web-search-assignment-hint">{t("settings.webSearchModelEmpty")} {onOpenProviders && <button type="button" className="btn btn--small" onClick={onOpenProviders}>{t("settings.webSearchModelConnections")}</button>}</p>}
              </div>
              <span className="model-assignment-connection">{!s.webSearchModel || s.webSearchModel === "auto" ? t("settings.webSearchModelAutomatic") : (s.providers.find(p => p.name === s.webSearchModel?.split("/")[0])?.displayName || s.webSearchModel.split("/")[0])}</span>
            </SettingsField>

            <SettingsField className="model-assignment-row" label={<ModelSettingHelp label={t("settings.subagentModel")} text={t("providerUI.subagentModelHelp")} />}>
              <ModelPicker
                s={s}
                refs={refs}
                value={subagentRef}
                disabled={busy}
                ariaLabel={t("settings.subagentModel")}
                emptyOptionLabel={t("settings.subagentModelDefault")}
                emptyOptionHint={t("settings.subagentModelFollowHint")}
                onPick={(ref) => void apply(() => saveModelSettings(s, {kind: "preference", field: "subagent", ref: ref}))}
              />
            <span className="model-assignment-connection">{subagentRef && subagentRef !== "auto" ? modelOptionMeta(modelOptionFromRef(subagentRef, s)!, t) : t("settings.connectionFollowParent")}</span>
            </SettingsField>

            <SettingsField className="model-assignment-effort" label={<ModelSettingHelp label={t("settings.subagentReasoning")} text={t("providerUI.subagentEffortHelp")} />}>
              <SettingsSelect
                className="mem-select set-grow"
                aria-label={t("settings.subagentReasoning")}
                value={s.subagentEffort || ""}
                disabled={busy}
                onValueChange={(value) => void apply(() => saveModelSettings(s, {kind: "preference", field: "subagent_effort", ref: value}))}
              >
                <option value="">{t("settings.subagentEffortDefault")}</option>
                {s.subagentEffort && !subagentLevels.includes(s.subagentEffort) && <option value={s.subagentEffort} disabled>{s.subagentEffort}</option>}
                {subagentLevels.map((level) => (
                  <option key={level} value={level}>
                    {level}
                  </option>
                ))}
              </SettingsSelect>
            </SettingsField>

            <details className="settings-subagent-advanced"><summary>{t("settings.subagentAdvanced")}</summary>
            <SettingsField label={t("settings.subagentDepth")} hint={t("settings.subagentDepthHint")}>
              <SettingsOptions className="provider-add-segmented" role="group" aria-label={t("settings.subagentDepth")}>
                {[1, 2].map((depth) => (
                  <button
                    key={depth}
                    type="button"
                    className={subagentDepth === depth ? "provider-add-segmented__item provider-add-segmented__item--active" : "provider-add-segmented__item"}
                    disabled={busy}
                    aria-pressed={subagentDepth === depth}
                    onClick={() => void apply(() => saveModelSettings(s, {kind: "preference", field: "depth", number: depth}))}
                  >
                    {depth === 1 ? t("settings.subagentDepthOne") : t("settings.subagentDepthTwo")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>

            <SettingsField label={t("settings.subagentConcurrency")} hint={t("settings.subagentConcurrencyHint")}>
              <input
                className="mem-input"
                type="number"
                min={1}
                max={32}
                value={subagentConcurrency}
                disabled={busy}
                onChange={(e) => {
                  const n = Number(e.target.value);
                  if (!Number.isFinite(n)) return;
                  void apply(() => saveModelSettings(s, {kind: "preference", field: "concurrency", number: n}));
                }}
              />
            </SettingsField>

            <SettingsField label={t("settings.parallelWriters")} hint={t("settings.parallelWritersHint")}>
              <input
                className="mem-input"
                type="number"
                min={1}
                max={subagentConcurrency}
                value={parallelWriters}
                disabled={busy}
                onChange={(e) => {
                  const n = Number(e.target.value);
                  if (!Number.isFinite(n)) return;
                  void apply(() => saveModelSettings(s, {kind: "preference", field: "writers", number: n}));
                }}
              />
            </SettingsField>

            {modelIssue && <div className="provider-fetch-banner provider-fetch-banner--warn"><ErrorMessage error={modelIssue} /></div>}
          </details>
          </SettingsSection>
          <SettingsSection className="model-runtime-preferences" title={t("settings.runtimePreferences")}>
            <SettingsField label={t("settings.reasoningLanguage")} hint={t("settings.reasoningLanguageHint")}>
              <SettingsOptions layout="field" className="set-seg" role="radiogroup" aria-label={t("settings.reasoningLanguage")}>
                {(["auto", "zh", "en"] as const).map((lang) => (
                  <button
                    key={lang}
                    type="button"
                    role="radio"
                    aria-checked={agent.reasoningLanguage === lang}
                    className={`set-seg__btn${agent.reasoningLanguage === lang ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetReasoningLanguage(lang))}
                  >
                    {t(`settings.reasoningLanguage.${lang}`)}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            <SettingsField label={t("settings.compactRatio")} hint={t("settings.compactRatioHint")} stacked>
              <div className="compact-ratio-controls">
                <fieldset className="compact-ratio-choice-list">
                  <legend className="sr-only">{t("settings.compactRatio")}</legend>
                  {COMPACT_RATIO_PRESETS.map(([ratio, labelKey, effectKey]) => {
                    const selected = Math.abs(compactRatio - ratio) < 0.0001;
                    const active = selected;
                    const [percent, name] = t(labelKey).split(" · ");
                    return (
                      <div key={ratio} className="compact-ratio-choice" data-selected={active || undefined}>
                        <label className="compact-ratio-choice__row"
                          onPointerDown={(event) => {
                            if (event.button === 0 && document.activeElement === compactRatioCustomInputRef.current) event.preventDefault();
                          }}
                          onMouseDown={(event) => {
                            // Keep focus until click so blur cannot disable the intended preset.
                            if (event.button === 0 && document.activeElement === compactRatioCustomInputRef.current) event.preventDefault();
                          }}
                          onClick={() => {
                            if (selected && !busy) void selectCompactRatioPreset(ratio);
                          }}
                        >
                          <input
                            type="radio"
                            name="settings-compact-ratio"
                            value={ratio}
                            checked={active}
                            disabled={busy}
                            aria-label={t(labelKey)}
                            onChange={() => void selectCompactRatioPreset(ratio)}
                          />
                          <span className="compact-ratio-choice__name">{name}</span>
                          <span className="compact-ratio-choice__percent">{percent}</span>
                          <span className="compact-ratio-choice__effect">— {t(effectKey)}</span>
                          {Math.abs(ratio - 0.8) < 0.0001 && <span className="compact-ratio-choice__badge">{t("settings.recommended")}</span>}
                        </label>
                      </div>
                    );
                  })}
                  <div className="compact-ratio-choice" data-selected={!compactRatioPreset || undefined}>
                    <div className="compact-ratio-choice__row compact-ratio-choice__row--custom">
                      <input
                        id="settings-compact-ratio-custom-choice"
                        type="radio"
                        name="settings-compact-ratio"
                        value="custom"
                        checked={!compactRatioPreset}
                        disabled={busy}
                        aria-label={t("settings.compactRatioCustomOption")}
                        onChange={focusCompactRatioCustom}
                      />
                      <label className="compact-ratio-choice__name" htmlFor="settings-compact-ratio-custom-choice">
                        {t("settings.typography.customized")}
                      </label>
                      <label className="compact-ratio-choice__inline-input" htmlFor="settings-compact-ratio-custom">
                        <input
                          ref={compactRatioCustomInputRef}
                          id="settings-compact-ratio-custom"
                          type="number"
                          min={COMPACT_RATIO_MIN_PERCENT}
                          max={COMPACT_RATIO_MAX_PERCENT}
                          step={0.1}
                          inputMode="decimal"
                          value={compactRatioDraft}
                          placeholder={t("settings.compactRatioCustomPlaceholder")}
                          disabled={busy}
                          aria-label={t("settings.compactRatioCustomAria")}
                          aria-invalid={compactRatioDraft !== "" && !compactRatioDraftValid}
                          onFocus={() => setCompactRatioCustomEditing(true)}
                          onInput={(event) => setCompactRatioDraft(event.currentTarget.value)}
                          onBlur={(event) => {
                            if (compactRatioCancelBlurRef.current) {
                              compactRatioCancelBlurRef.current = false;
                              return;
                            }
                            void commitCompactRatioDraft(event.currentTarget.value);
                          }}
                          onKeyDown={(event) => {
                            if (event.key === "Enter") {
                              event.preventDefault();
                              event.currentTarget.blur();
                            }
                            if (event.key === "Escape") {
                              event.preventDefault();
                              compactRatioCancelBlurRef.current = true;
                              setCompactRatioDraft(compactRatioPreset ? "" : String(compactRatioPercent));
                              setCompactRatioCustomEditing(false);
                              event.currentTarget.blur();
                            }
                          }}
                        />
                        <span aria-hidden="true">%</span>
                      </label>
                    </div>
                  </div>
                </fieldset>
                <div className="compact-ratio-impact">{compactRatioImpact}</div>
              </div>
            </SettingsField>
            {compactRatioOverrideHint && <div className="provider-fetch-banner provider-fetch-banner--warn">{compactRatioOverrideHint}</div>}
          </SettingsSection>
        </div>
      ) : null}
      <div className="model-access-page" hidden={subtab !== "access"}>
        <ProvidersSection s={s} busy={busy} apply={apply} onboarding={onboarding} focusProvider={focusProvider} focusRequestId={focusRequestId} onOnboardingComplete={onOnboardingComplete} onOnboardingStart={onOnboardingStart} />
      </div>
      {subtab === "stats" && (
        <Suspense fallback={<div className="empty">{t("settings.loading")}</div>}>
          <UsageStatsPanel />
        </Suspense>
      )}
    </>
  );
}

type ModelPickerOption = {
  ref: string;
  provider: string;
  model: string;
  providerView?: ProviderView;
};

export function ModelPicker({
  s,
  refs,
  value,
  disabled,
  includeSameDefault = false,
  ariaLabel,
  emptyOptionLabel,
  emptyOptionHint,
  autoOptionLabel,
  autoOptionHint,
  onPick,
}: {
  s: SettingsView;
  refs: string[];
  value: string;
  disabled: boolean;
  includeSameDefault?: boolean;
  ariaLabel?: string;
  emptyOptionLabel?: string;
  emptyOptionHint?: string;
  autoOptionLabel?: string;
  autoOptionHint?: string;
  onPick: (ref: string) => void;
}) {
  const t = useT();
  const emptyLabel = includeSameDefault ? t("settings.plannerNone") : emptyOptionLabel;
  const emptyHint = includeSameDefault ? t("settings.plannerNoneHint") : emptyOptionHint;
  const emptyMeta = includeSameDefault ? t("settings.plannerNoneHintShort") : emptyOptionHint;
  const autoLabel = autoOptionLabel;
  const autoHint = autoOptionHint;
  const selected = refs.includes(value) ? modelOptionFromRef(value, s) : null;
  const selectedLabel = value === "auto" && autoLabel
    ? autoLabel
    : value === "" && emptyLabel
    ? emptyLabel
    : selected?.model || value || t("common.none");
  const selectedMeta = value === "auto" && autoLabel
    ? autoHint || ""
    : value === "" && emptyLabel
    ? emptyMeta || ""
    : selected
    ? modelOptionMeta(selected, t)
    : t("settings.noModelsConfigured");

  const groups = useMemo(() => {
    const providerOrder: string[] = [];
    const providerSeen = new Set<string>();
    for (const p of s.providers) {
      const id = p.name;
      if (!providerSeen.has(id)) {
        providerOrder.push(id);
        providerSeen.add(id);
      }
    }
    const options = refs
      .map((ref) => modelOptionFromRef(ref, s))
      .filter((opt): opt is ModelPickerOption => Boolean(opt))
;
    for (const opt of options) {
      const groupID = modelOptionGroupID(opt);
      if (!providerSeen.has(groupID)) {
        providerOrder.push(groupID);
        providerSeen.add(groupID);
      }
    }
    return providerOrder
      .map((groupID) => {
        const providerViews = s.providers.filter((p) => p.name === groupID);
        const firstProvider = providerViews[0];
        return {
          groupID,
          label: firstProvider ? (firstProvider.displayName || firstProvider.name) : groupID,
          keySet: providerViews.some((p) => p.keySet),
          requiresKey: providerViews.every((p) => providerRequiresKey(p)),
          options: uniqueModelOptions(options.filter((opt) => modelOptionGroupID(opt) === groupID)),
        };
      })
      .filter((group) => group.options.length > 0);
  }, [refs, s, t]);

  return (
    <div className="settings-model-picker">
      <SettingsSelect
        value={value}
        selectedLabel={selectedLabel}
        title={selectedMeta || undefined}
        disabled={disabled || (!emptyLabel && !autoLabel && refs.length === 0)}
        aria-label={ariaLabel}
        searchPlaceholder={t("settings.searchModels")}
        emptyLabel={t("settings.noMatchingModels")}
        onValueChange={onPick}
        options={[
          ...(emptyLabel ? [{ value: "", label: emptyLabel, hint: emptyHint }] : []),
          ...(autoLabel ? [{ value: "auto", label: autoLabel, hint: autoHint }] : []),
          ...groups.flatMap(group => group.options.map(opt => ({
            value: opt.ref,
            label: opt.model,
            group: group.groupID,
            groupLabel: group.requiresKey && !group.keySet ? `${group.label} · ${t("settings.noKey")}` : group.label,
            searchText: `${opt.ref} ${modelProviderLabel(opt.provider, opt.providerView, t)}`,
          }))),
        ]}
      />
    </div>
  );
}

function modelOptionFromRef(ref: string, s: SettingsView): ModelPickerOption | null {
  if (!ref) return null;
  const [provider, ...modelParts] = ref.split("/");
  const model = modelParts.join("/") || ref;
  return {
    ref,
    provider,
    model,
    providerView: s.providers.find((p) => p.name === provider),
  };
}

function modelOptionMeta(option: ModelPickerOption, t: ReturnType<typeof useT>): string {
  const key = option.providerView ? providerKeyStatusLabel(option.providerView, t) : t("settings.noKey");
  return `${option.providerView?.displayName || option.provider}${option.providerView && providerRequiresKey(option.providerView) && !option.providerView.keySet ? ` · ${key}` : ""}`;
}

function providerKeyStatusLabel(provider: { keySet: boolean; requiresKey?: boolean; apiKeyEnv?: string }, t: ReturnType<typeof useT>): string {
  if (!providerRequiresKey(provider)) return t("settings.noKeyRequired");
  return provider.keySet ? t("settings.keySet") : t("settings.noKey");
}

function modelProviderLabel(provider: string, providerView: ProviderView | undefined, t: ReturnType<typeof useT>): string {
  return providerView?.displayName || (providerView ? providerGroupLabel(providerView, t) : provider);
}

function modelOptionGroupID(option: ModelPickerOption): string {
  return option.provider;
}

function uniqueModelOptions(options: ModelPickerOption[]): ModelPickerOption[] {
  const indexByModel = new Map<string, number>();
  const out: ModelPickerOption[] = [];
  for (const option of options) {
    const existingIndex = indexByModel.get(option.model);
    if (existingIndex !== undefined) {
      const existing = out[existingIndex];
      if (!existing?.providerView?.webSearch && option.providerView?.webSearch) {
        out[existingIndex] = option;
      }
      continue;
    }
    indexByModel.set(option.model, out.length);
    out.push(option);
  }
  return out;
}

function sameStringList(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((value, i) => value === b[i]);
}

function proxyModeLabel(mode: ProxyMode, t: ReturnType<typeof useT>): string {
  switch (mode) {
    case "auto":
      return t("settings.proxyMode.auto");
    case "custom":
      return t("settings.proxyMode.custom");
    case "off":
      return t("settings.proxyMode.off");
  }
}

export function ProvidersSection({ s, busy, apply, onboarding, focusProvider, focusRequestId, onOnboardingComplete, onOnboardingStart }: SectionProps & { onboarding?: boolean; focusProvider?: string; focusRequestId?: number; onOnboardingComplete?: () => void; onOnboardingStart?: () => Promise<void> }) {
  const t = useT();
  const existingConnection = s.providers.find(p => p.added);
  const [editing, setEditing] = useState<string | null>(() => onboarding ? existingConnection?.name ?? null : null);
  const [adding, setAdding] = useState<AddProviderMode>(() => onboarding && !existingConnection ? "official" : null);
  const [revealedProvider, setRevealedProvider] = useState<string | null>(() => onboarding ? existingConnection?.name ?? null : null);
  useEffect(() => {
    if (!focusProvider) return;
    if (s.providers.some((provider) => provider.name === focusProvider)) {
      setRevealedProvider(focusProvider);
      setEditing(focusProvider);
    } else {
      setRevealedProvider(null);
      setEditing(null);
    }
  }, [focusProvider, focusRequestId, s.providers]);
  const [startingConversation, setStartingConversation] = useState(false);
  const startingConversationRef = useRef(false);
  const startUsing = async (provider: ProviderView) => {
    if (startingConversationRef.current || !provider.added || !providerIsConfigured(provider) || !provider.models.length) return;
    startingConversationRef.current = true;
    setStartingConversation(true);
    try {
      const model = providerDefaultModel(provider.default, provider.models);
      if (await apply(() => saveModelSettings(s, {kind: "preference", field: "default", ref: `${provider.name}/${model}`}))) {
        if (onOnboardingStart) await onOnboardingStart();
        else onOnboardingComplete?.();
      }
    } finally {
      startingConversationRef.current = false;
      setStartingConversation(false);
    }
  };
  const [fetchingProviders, setFetchingProviders] = useState<Set<string>>(() => new Set());
  const fetchGate = useMemo(createLatestRequestGate, []);
  const [fetchResults, setFetchResults] = useState<Record<string, ProviderFetchResult>>({});
  const [modelDrafts, setModelDrafts] = useState<Record<string, ProviderModelDraft>>({});
  const visibleProviders = useMemo(() => s.providers.filter((p) => p.added || p.name === revealedProvider), [s.providers, revealedProvider]);
  const groups = useMemo(() => visibleProviders.map(p => ({...providerAccessGroups([p], t)[0], id: `connection:${p.name}`, label: p.displayName || p.name})), [visibleProviders, t]);

  useEffect(() => {
    if (revealedProvider && !s.providers.some((p) => p.name === revealedProvider)) {
      setRevealedProvider(null);
      if (editing === revealedProvider) setEditing(null);
    }
  }, [editing, revealedProvider, s.providers]);

  const setGroupFetchResult = (groupID: string, result: ProviderFetchResult | null) => {
    setFetchResults((prev) => {
      const next = { ...prev };
      if (result) next[groupID] = result;
      else delete next[groupID];
      return next;
    });
  };

  const setGroupModelDraft = (groupID: string, draft: ProviderModelDraft | null) => {
    setModelDrafts((prev) => {
      const next = { ...prev };
      if (draft) next[groupID] = draft;
      else delete next[groupID];
      return next;
    });
  };

  const beginGroupFetch = (groupID: string): number => {
    const generation = fetchGate.begin(groupID);
    setFetchingProviders((current) => {
      if (current.has(groupID)) return current;
      const next = new Set(current);
      next.add(groupID);
      return next;
    });
    return generation;
  };

  const groupFetchIsCurrent = (groupID: string, generation: number): boolean => (
    fetchGate.isCurrent(groupID, generation)
  );

  const finishGroupFetch = (groupID: string, generation: number) => {
    if (!groupFetchIsCurrent(groupID, generation)) return;
    setFetchingProviders((current) => {
      if (!current.has(groupID)) return current;
      const next = new Set(current);
      next.delete(groupID);
      return next;
    });
  };

  const cancelGroupFetch = (groupID: string) => {
    fetchGate.cancel(groupID);
    setFetchingProviders((current) => {
      if (!current.has(groupID)) return current;
      const next = new Set(current);
      next.delete(groupID);
      return next;
    });
  };

  const modelDraftForFetch = (p: ProviderView, fetched: string[], capabilities: ProviderModelCapabilityView[] = []): ProviderModelDraft => {
    const candidates = providerModelCandidates(p.models, fetched);
    const selected = mergedFetchedProviderModels(p.models, fetched, { preserveCurated: true });
    const configuredVision = providerVisionModelsForView(p, candidates);
    return {
      providerName: p.name,
      candidates,
      selected: candidates.filter((model) => selected.includes(model)),
      visionModels: configuredVision,

      modelCapabilities: capabilities,
    };
  };

  const updateModelDraftSelection = (groupID: string, nextSelected: (draft: ProviderModelDraft) => string[]) => {
    setModelDrafts((prev) => {
      const draft = prev[groupID];
      if (!draft) return prev;
      const selectedSet = new Set(nextSelected(draft));
      return {
        ...prev,
        [groupID]: {
          ...draft,
          selected: draft.candidates.filter((model) => selectedSet.has(model)),
        },
      };
    });
  };


  const refreshModels = async (group: ProviderAccessGroup, p: ProviderView) => {
    const generation = beginGroupFetch(group.id);
    setGroupFetchResult(group.id, null);
    setGroupModelDraft(group.id, null);
    try {
      let fetched: string[];
      let fetchedCapabilities: ProviderModelCapabilityView[] = [];
      try {
        fetchedCapabilities = await cachedFetchProviderModelCatalog((provider) => app.FetchProviderModelCatalog(provider), p, true);
        fetched = fetchedCapabilities.map((item) => item.model);
      } catch (e) {
        if (!groupFetchIsCurrent(group.id, generation)) return;
        setGroupFetchResult(group.id, {
          kind: "warn",
          text: t("settings.fetchModelsFailedForProvider", { provider: group.label, err: String((e as Error)?.message ?? e) }),
        });
        return;
      }
      if (!groupFetchIsCurrent(group.id, generation)) return;
      if (fetched.length === 0) {
        setGroupFetchResult(group.id, {
          kind: "warn",
          text: t("settings.fetchModelsEmptyForProvider", { provider: group.label }),
        });
        return;
      }
      const draft = modelDraftForFetch(p, fetched, fetchedCapabilities);
      startTransition(() => {
        setGroupModelDraft(group.id, draft);
        setGroupFetchResult(group.id, {
          kind: "ok",
          text: t("settings.fetchModelsReadyForProvider", { provider: group.label, n: draft.candidates.length }),
        });
      });
    } finally {
      finishGroupFetch(group.id, generation);
    }
  };

  const saveProviderKey = async (group: ProviderAccessGroup, apiKeyEnv: string, value: string) => {
    if (!apiKeyEnv) return;
    cancelGroupFetch(group.id);
    setGroupFetchResult(group.id, null);
    setGroupModelDraft(group.id, null);
    const saved = await apply(async () => {
      const warning = await saveModelSettings(s, {kind: "credential", names: group.providers.filter(p => p.apiKeyEnv === apiKeyEnv).map(p => p.name), key: value});
      invalidateProviderCacheByAPIKeyEnv(apiKeyEnv);
      return warning;
    });
    if (!saved) throw new Error(t("settings.models.keySaveFailed"));
  };

  const clearProviderKey = async (group: ProviderAccessGroup, apiKeyEnv: string) => {
    if (!apiKeyEnv) return;
    cancelGroupFetch(group.id);
    await apply(async () => {
      await saveModelSettings(s, {kind: "credential", names: group.providers.filter(p => p.apiKeyEnv === apiKeyEnv).map(p => p.name), key: ""});
      invalidateProviderCacheByAPIKeyEnv(apiKeyEnv);
    });
  };

  const saveProvider = async (provider: ProviderView, key: string, create = false) => {
    if (create || !s.providers.some(p => p.name === provider.name)) {
      const id = crypto.randomUUID().replaceAll("-", "");
      provider = {...provider, displayName: provider.displayName || provider.name, name: `${provider.name}-${id}`, apiKeyEnv: `DEEPZORA_CONNECTION_${id.toUpperCase()}_KEY`};
    }
    if (key) {
      provider = {...provider, apiKeyEnv: `DEEPZORA_CONNECTION_${crypto.randomUUID().replaceAll("-", "").toUpperCase()}_KEY`};
      const warning = await saveModelSettings(s, {kind: "provider_save", provider, key});
      invalidateProviderCacheByAPIKeyEnv(provider.apiKeyEnv);
      return warning;
    }
    return saveModelSettings(s, {kind: "provider_save", provider});
  };

  const saveModelDraft = async (group: ProviderAccessGroup) => {
    const draft = modelDrafts[group.id];
    const provider = draft ? group.providers.find((p) => p.name === draft.providerName) : null;
    const models = uniqueStrings(draft?.selected ?? []);
    if (!draft || !provider || models.length === 0) return;
    let saved = false;
    await apply(async () => {
      const result = await saveModelSettings(s, {kind: "provider_save", provider: {
        ...provider,
        models,
        // Vision capability is derived from model metadata. Keep legacy
        // fields untouched so old configurations remain readable.
        default: providerDefaultModel(provider.default, models),
      }});
      saved = true;
      return result;
    });
    if (!saved) return;
    setGroupModelDraft(group.id, null);
    setGroupFetchResult(group.id, {
      kind: "ok",
      text: t("settings.enabledModelsSavedForProvider", { provider: group.label, n: models.length }),
    });
  };

  return (
    <SettingsSection
      title={t("settings.providerAccess")}
      description={t("settings.providerAccessHint")}
      actions={
        <button className="btn btn--small" disabled={busy || adding !== null} onClick={() => setAdding("official")}>
          {t("settings.addProvider")}
        </button>
      }
    >
      <div className="provider-access-grid">
        {groups.length === 0 && adding === null && (
          <div className="provider-empty">
            <strong>{t("settings.providerAccessEmptyTitle")}</strong>
            <span>{t("settings.providerAccessEmptyHint")}</span>
            <div className="provider-empty__actions">
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => setAdding("official")}>
                {t("settings.addProvider.officialChoice")}
              </button>
              <button type="button" className="btn btn--small" disabled={busy} onClick={() => setAdding("custom")}>
                {t("settings.addProvider.customChoice")}
              </button>
            </div>
          </div>
        )}
        {adding !== null && (
          <AddProviderPanel
            mode={adding}
            kinds={s.providerKinds}
            officialProviders={s.officialProviders}
            providerPresets={s.providerPresets}
            busy={busy}
            onMode={setAdding}
            onCancel={() => onboarding ? onOnboardingComplete?.() : setAdding(null)}
            onAddOfficial={(kind, key, baseURL, format) => apply(() => saveModelSettings(s, {kind: "connection_add", name: s.providers.find(p => officialProviderKind(p) === kind)?.name ?? "deepseek-flash", key, baseURL, protocol: format})).then(saved => { if (saved) setAdding(null); })}
            onAddPreset={(id, key, baseURL, format) => apply(() => saveModelSettings(s, {kind: "connection_add", presetId: id, key, baseURL, protocol: format})).then(saved => { if (saved) setAdding(null); })}
            onViewPresetConflict={(providerName) => {
              setRevealedProvider(providerName);
              setEditing(providerName);
              setAdding(null);
            }}
            onResetPreset={(id) => apply(() => saveModelSettings(s, {kind: "preset_reset", presetId: id})).then(saved => { if (saved) setAdding(null); })}
            onAddCustom={(pv, key) => apply(() => saveProvider(pv, key ?? "", true)).then(saved => { if (saved) setAdding(null); })}
          />
        )}
        <ProviderConnections groups={groups} presets={s.providerPresets} revealedProvider={revealedProvider} hidden={adding !== null} busy={busy} onAdd={() => setAdding("official")} onSelect={() => setRevealedProvider(null)} renderDetail={(group) => (
          <ProviderAccessCard
            key={group.id}
            detail
            onCopy={() => void apply(() => saveModelSettings(s, {kind: "connection_add", name: group.providers[0].name, key: ""}))}
            onRename={(label) => apply(() => {
              if (typeof app.RenameProviderConnections !== "function") throw new Error(t("settings.connections.restartRequired"));
              return saveModelSettings(s, {kind: "rename", names: group.providers.map(p => p.name), ref: label});
            })}
            group={group}
            onOnboardingStart={onboarding ? startUsing : undefined}
            onOnboardingLater={onboarding ? onOnboardingComplete : undefined}
            providerPresets={s.providerPresets}
            busy={busy || startingConversation}
            fetching={fetchingProviders.has(group.id)}
            fetchResult={fetchResults[group.id]}
            modelDraft={modelDrafts[group.id]}
            editing={editing}
            kinds={s.providerKinds}
            onEdit={setEditing}
            onCancelEdit={() => setEditing(null)}
            onSave={(pv, key) => {
              cancelGroupFetch(group.id);
              return apply(() => saveProvider(pv, key ?? "")).then((saved) => {
                if (!saved) throw new Error(t("settings.connections.renameFailed"));
                setGroupModelDraft(group.id, null);
              });
            }}
            onRefresh={(provider) => void refreshModels(group, provider)}
            onToggleDraftModel={(model) => updateModelDraftSelection(group.id, (draft) => (
              draft.selected.includes(model)
                ? draft.selected.filter((candidate) => candidate !== model)
                : [...draft.selected, model]
            ))}
            onSelectAllDraftModels={() => updateModelDraftSelection(group.id, (draft) => draft.candidates)}
            onClearDraftModels={() => updateModelDraftSelection(group.id, () => [])}
            onCancelDraftModels={() => {
              setGroupModelDraft(group.id, null);
              setGroupFetchResult(group.id, null);
            }}
            onSaveDraftModels={() => void saveModelDraft(group)}
            onToggleWebSearch={(enabled) => {
              if (group.id === "custom:opencode-go") {
                const searchProviderNames = group.providers
                  .map((provider) => provider.name)
                  .filter((name) => name.startsWith("opencode-go-deepseek-"));
                if (enabled) {
                  void apply(() => saveModelSettings(s, {kind: "preset_add", presetId: "opencode-go-deepseek-responses", key: ""}));
                } else if (searchProviderNames.length > 0) {
                  void apply(() => saveModelSettings(s, {kind: "access_remove", names: searchProviderNames}));
                }
                return;
              }
              const providerNames = group.providers.map((provider) => provider.name);
              if (providerNames.length === 0) return;
              void apply(() => saveModelSettings(s, {kind: "web_search_capability", names: providerNames, enabled}));
            }}
            onSaveEditorKey={(env, value) => saveProviderKey(group, env, value)}
            onClearEditorKey={(env) => clearProviderKey(group, env)}
            onDelete={(providers) => {
              cancelGroupFetch(group.id);
              const providerNames = providers.map(({ name }) => name);
              return apply(() => saveModelSettings(s, {kind: "access_remove", names: providerNames})).then(() => {
                if (revealedProvider && providerNames.includes(revealedProvider)) {
                  setRevealedProvider(null);
                  setEditing(null);
                }
              });
            }}
          />
        )} />
      </div>
    </SettingsSection>
  );
}

export type ProviderAccessGroup = {
  id: string;
  label: string;
  description: string;
  builtIn: boolean;
  providers: ProviderView[];
  apiKeyEnv: string;
  keySet: boolean;
  requiresKey: boolean;
  configured: boolean;
  keySource?: string;
  keySourcePath?: string;
  baseUrl: string;
  kind: string;
  models: string[];
};

type ProviderFetchResult = {
  kind: "ok" | "warn";
  text: string;
};

type ProviderModelDraft = {
  providerName: string;
  candidates: string[];
  selected: string[];
  visionModels: string[];
  modelCapabilities: ProviderModelCapabilityView[];
};

type AddProviderMode = null | "official" | "custom";
type OfficialProviderKind = "deepseek";

const OFFICIAL_PROVIDER_CHOICES: Array<{ kind: OfficialProviderKind; labelKey: DictKey; descKey: DictKey; keyEnv: string }> = [
  { kind: "deepseek", labelKey: "settings.addProvider.official.deepseek", descKey: "settings.addProvider.official.deepseekDesc", keyEnv: "DEEPSEEK_API_KEY" },
];

type ProviderTemplateChoice =
  | { id: string; source: "official"; kind: OfficialProviderKind; label: string; description: string; keyEnv: string; added: boolean; keySet: boolean }
  | {
    id: string; source: "preset"; presetID: string; label: string; description: string; keyEnv: string;
    added: boolean; status: ProviderPresetStatus; statusProviderNames: string[]; missingProviderNames: string[]; keySet: boolean;
    recommended: boolean; billingMode: string; displayGroup: string; displaySection: string;
    displayTier: string; routeKind: string; optional: boolean; displayOrder: number;
  };

export function AddProviderPanel({
  mode,
  kinds,
  officialProviders,
  providerPresets,
  busy,
  onMode,
  onCancel,
  onAddOfficial,
  onAddPreset,
  onViewPresetConflict,
  onResetPreset,
  onAddCustom,
}: {
  mode: AddProviderMode; kinds: string[]; officialProviders: ProviderView[];
  providerPresets: ProviderPresetView[];
  busy: boolean;
  onMode: (mode: AddProviderMode) => void;
  onCancel: () => void;
  onAddOfficial: (kind: OfficialProviderKind, key: string, baseURL?: string, format?: string) => Promise<void>;
  onAddPreset: (id: string, key: string, baseURL?: string, format?: string) => Promise<void>;
  onViewPresetConflict: (providerName: string) => void;
  onResetPreset: (id: string) => Promise<void>;
  onAddCustom: (p: ProviderView, key?: string) => void | Promise<void>;
}) {
  const t = useT();
  const choices: CatalogChoice[] = [
    ...OFFICIAL_PROVIDER_CHOICES.map(choice => {
      const provider = officialProviders.find(p => officialProviderKind(p) === choice.kind);

      return {
        id: `official:${choice.kind}`,
        catalog: { brandId: "deepseek", brandLabel: "DeepSeek", region: "global", product: "api", format: "anthropic", baseUrl: "https://api.deepseek.com/anthropic" },
        keyEnv: provider?.apiKeyEnv || choice.keyEnv, keySet: Boolean(provider?.keySet),
        models: providerPresets.find(p => p.id === "deepseek-chat")?.models ?? ["deepseek-flash", "deepseek-v4-pro"],
        canAdd: true, status: "available",
        statusLabel: "",
        actionLabel: t("settings.addProvider.confirm"),
      };
    }),
    ...providerPresets.map(preset => {
      const choice: ProviderTemplateChoice = {
        id: `preset:${preset.id}`, source: "preset", presetID: preset.id,
        label: preset.label, description: preset.description, keyEnv: preset.keyEnv,
        added: preset.added, status: normalizeProviderPresetStatus(preset.status, preset.added),
        statusProviderNames: asArray(preset.statusProviderNames), missingProviderNames: asArray(preset.missingProviderNames),
        keySet: preset.keySet, recommended: Boolean(preset.recommended), billingMode: preset.billingMode ?? "",
        displayGroup: preset.displayGroup ?? "", displaySection: preset.displaySection ?? "",
        displayTier: preset.displayTier ?? "", routeKind: preset.routeKind ?? "", optional: Boolean(preset.optional), displayOrder: preset.displayOrder ?? 0,
      };
      return {
        id: choice.id, label: preset.label,
        // Older hosts can omit the additive catalog field; their presets remain usable.
        catalog: catalogForPreset(preset),
        keyEnv: preset.keyEnv, keySet: preset.keySet, models: preset.models,
        canAdd: true, status: "available",
        statusLabel: "", actionLabel: t("settings.addProvider.confirm"),
        conflictName: undefined,
      };
    }),
  ];
  return <div className="provider-add-panel">
    <div className="provider-add-panel__head">
      <div><strong>{t("settings.addProvider.chooseTitle")}</strong><span>{t("settings.catalog.hint")}</span></div>
      <button type="button" className="btn btn--small" disabled={busy} onClick={onCancel}>{t("common.cancel")}</button>
    </div>
    <SettingsOptions className="provider-add-segmented" role="tablist" aria-label={t("settings.addProvider.chooseTitle")}>
      <button type="button" role="tab" aria-selected={mode === "official"} className="provider-add-segmented__item" disabled={busy} onClick={() => onMode("official")}>{t("settings.addProvider.officialChoice")}</button>
      <button type="button" role="tab" aria-selected={mode === "custom"} className="provider-add-segmented__item" disabled={busy} onClick={() => onMode("custom")}>{t("settings.addProvider.customChoice")}</button>
    </SettingsOptions>
    {mode === "official" && <ProviderCatalogPicker choices={choices} busy={busy}
      onConnect={(id, key, baseURL, format) => { if (id.startsWith("official:")) void onAddOfficial("deepseek", key, baseURL, format); else void onAddPreset(id.slice(7), key, baseURL, format); }}
      onView={onViewPresetConflict} onReset={id => { if (id.startsWith("preset:")) void onResetPreset(id.slice(7)); }} />}
    {mode === "custom" && <ProviderEditor kinds={kinds} busy={busy} onCancel={onCancel} onSave={onAddCustom} />}
  </div>;
}

export function ProviderAccessCard({
  detail = false,
  onOnboardingStart,
  onOnboardingLater,
  onCopy,
  onRename,
  group,
  providerPresets,
  busy,
  fetching,
  fetchResult,
  modelDraft,
  editing,
  kinds,
  onEdit,
  onCancelEdit,
  onSave,
  onRefresh,
  onToggleDraftModel,
  onSelectAllDraftModels,
  onClearDraftModels,
  onCancelDraftModels,
  onSaveDraftModels,
  onToggleWebSearch,
  onSaveEditorKey,
  onClearEditorKey,
  onDelete,
}: {
  detail?: boolean;
  onOnboardingStart?: (provider: ProviderView) => Promise<void>;
  onOnboardingLater?: () => void;
  onCopy?: () => void;
  onRename?: (label: string) => Promise<boolean>;
  group: ProviderAccessGroup;
  providerPresets?: ProviderPresetView[];
  busy: boolean;
  fetching: boolean;
  fetchResult?: ProviderFetchResult;
  modelDraft?: ProviderModelDraft;
  editing: string | null;
  kinds: string[];
  onEdit: (name: string) => void;
  onCancelEdit: () => void;
  onSave: (p: ProviderView, key?: string) => void | Promise<void>;
  onRefresh: (p: ProviderView) => void;
  onToggleDraftModel: (model: string) => void;
  onSelectAllDraftModels: () => void;
  onClearDraftModels: () => void;
  onCancelDraftModels: () => void;
  onSaveDraftModels: () => void;
  onToggleWebSearch: (enabled: boolean) => void;
  onSaveEditorKey: (apiKeyEnv: string, value: string) => Promise<void>;
  onClearEditorKey?: (apiKeyEnv: string) => Promise<void>;
  onDelete?: (providers: ProviderView[]) => Promise<void>;
}) {
  const t = useT();
  const editableProvider = group.providers[0];
  const brand = onOnboardingStart ? connectionBrand(group, providerPresets ?? []) : undefined;
  const isOpenCodeGoConnection = group.id === "custom:opencode-go";
  const [editorRevision, setEditorRevision] = useState(0);
  const editingProvider = group.providers.find((p) => editing === p.name) ?? (detail && !isOpenCodeGoConnection ? editableProvider : undefined);
  const primaryProviderExpanded = Boolean(editableProvider && editing === editableProvider.name);
  const supportsServerWebSearch = isOpenCodeGoConnection
    || (group.providers.length > 0 && group.providers.every(providerSupportsServerWebSearchForView));
  const webSearchEnabled = supportsServerWebSearch && (isOpenCodeGoConnection
    ? group.providers.some((provider) => provider.name.startsWith("opencode-go-deepseek-") && Boolean(provider.webSearch))
    : group.providers.every((provider) => Boolean(provider.webSearch)));
  const visibleModels = group.models.slice(0, 6);
  const hiddenModelCount = Math.max(0, group.models.length - visibleModels.length);
  const providerProfiles = (
    <div className="provider-profiles">
      {group.providers.map((p) => {
        const profileExpanded = editing === p.name;
        return (
          <div className="provider-profile-row" key={p.name}>
            <span>{p.name}</span>
            <span>{p.models.join(", ") || t("common.none")}</span>
            <button
              className="btn btn--small provider-profile-row__refresh"
              disabled={busy || fetching || !p.baseUrl || !providerIsConfigured(p)}
              onClick={() => onRefresh(p)}
            >
              {fetching ? t("settings.fetchingModels") : t("settings.fetchModels")}
            </button>
            <button
              className="btn btn--small provider-profile-row__configure"
              disabled={busy}
              aria-expanded={profileExpanded}
              onClick={() => profileExpanded ? onCancelEdit() : onEdit(p.name)}
            >
              {profileExpanded ? t("common.collapse") : t("settings.configureProfile")}
            </button>
          </div>
        );
      })}
    </div>
  );
  return (
    <article className={`provider-access-card${detail ? " provider-access-card--detail" : ""}${group.builtIn ? " provider-access-card--builtin" : ""}${onOnboardingStart ? " provider-access-card--onboarding" : ""}`}>
      <div className="provider-access-card__head">
        <div className="provider-access-card__identity">
          <div className="provider-access-card__title">
            {brand && providerBrandIcons.has(brand.id) && <span className="provider-catalog__icon provider-onboarding-brand" style={{ maskImage: `url(/provider-icons/${brand.id}.svg)`, WebkitMaskImage: `url(/provider-icons/${brand.id}.svg)` }} aria-hidden="true" />}
            {onRename ? <ConnectionTitle label={group.label} busy={busy} onSave={onRename} /> : group.label}
            {!detail && <span className={`badge ${group.builtIn ? "badge--project" : "badge--neutral"}`}>
              {group.builtIn ? t("settings.builtinProviderBadge") : t("settings.customProviderBadge")}
            </span>}
            <span className={`badge ${group.keySet ? "badge--project" : "badge--feedback"}`}>
              {providerKeyStatusLabel(group, t)}
            </span>
          </div>
        </div>
        <div className="provider-access-card__actions">
          {onCopy && <button type="button" className="btn provider-icon-action" disabled={busy} onClick={onCopy} title={t("settings.connections.copy")} aria-label={t("settings.connections.copy")}><Files size={17} /></button>}
          {editableProvider && !isOpenCodeGoConnection && !detail && (
            <button
              className="btn btn--small"
              disabled={busy}
              aria-expanded={primaryProviderExpanded}
              onClick={() => primaryProviderExpanded ? onCancelEdit() : onEdit(editableProvider.name)}
            >
              {primaryProviderExpanded ? t("common.collapse") : t("settings.configureProvider")}
            </button>
          )}
          {editableProvider && group.providers.length === 1 && !detail && (
            <button
              className="btn btn--small"
              disabled={busy || fetching || !editableProvider.baseUrl || !group.configured}
              onClick={() => onRefresh(editableProvider)}
            >
              {fetching ? t("settings.fetchingModels") : t("settings.fetchModels")}
            </button>
          )}
          {editableProvider && onDelete && (
            <ProviderAccessMoreMenu
              busy={busy}
              builtIn={group.builtIn}
              onRemove={() => onDelete(group.providers)}
            />
          )}
        </div>
      </div>
      {group.description && !editingProvider && <div className="provider-access-card__desc">{group.description}</div>}


      {!supportsServerWebSearch && !(detail && editingProvider) && (
        <ProviderModelSummary
          configured={group.configured}
          models={visibleModels}
          hiddenModelCount={hiddenModelCount}
        />
      )}

      {!group.configured && group.requiresKey && (
        <div className="provider-card-status provider-card-status--warn">
          {t("settings.modelsRequireKey")}
        </div>
      )}
      {fetchResult && (
        <div className={`provider-card-status provider-card-status--${fetchResult.kind}`}>
          {fetchResult.kind === "warn" ? <ErrorMessage error={fetchResult.text} /> : fetchResult.text}
        </div>
      )}

      {modelDraft && (
        <ProviderModelDraftPicker
          draft={modelDraft}
          busy={busy}
          fetching={fetching}
          onToggle={onToggleDraftModel}
          onSelectAll={onSelectAllDraftModels}
          onClear={onClearDraftModels}
          onCancel={onCancelDraftModels}
          onSave={onSaveDraftModels}
        />
      )}

      {editableProvider && !(detail && editingProvider) && (
        <ProviderServiceCapabilities
          supported={supportsServerWebSearch}
          configured={group.configured}
          models={visibleModels}
          hiddenModelCount={hiddenModelCount}
          showModelSummary
          enabled={webSearchEnabled}
          disabled={busy}
          onChange={onToggleWebSearch}
        />
      )}

      {!detail && <ProviderTechnicalDetails group={group} />}

      {group.providers.length > 1 && (isOpenCodeGoConnection ? (
        <details className="provider-route-settings">
          <summary>{t("settings.addProvider.manageRoutes")}</summary>
          {providerProfiles}
        </details>
      ) : providerProfiles)}

      {editingProvider && (
        <ProviderEditor
          key={`${editingProvider.name}:${editorRevision}`}
          initial={editingProvider}
          hideConnectionName={detail}
          providerPresets={providerPresets}
          kinds={kinds}
          busy={busy}
          onCancel={() => { onCancelEdit(); setEditorRevision((n) => n + 1); }}
          onOnboardingStart={onOnboardingStart ? () => onOnboardingStart(editingProvider) : undefined}
          onOnboardingLater={onOnboardingLater}
          onSave={onSave}
          onSaveKey={onSaveEditorKey}
          onClearKey={onClearEditorKey}
        />
      )}
    </article>
  );
}

function ProviderModelSummary({
  configured,
  models,
  hiddenModelCount,
  compact = false,
}: {
  configured: boolean;
  models: string[];
  hiddenModelCount: number;
  compact?: boolean;
}) {
  const t = useT();
  const label = t(configured ? "settings.enabledModels" : "settings.modelList");
  return (
    <div className={`provider-card-block${compact ? " provider-card-block--inline" : ""}`}>
      <div className="provider-card-block__label">{label}</div>
      <div className="provider-model-chips" aria-label={label}>
        {models.length > 0 ? models.map((model) => (
          <span className="provider-model-chip" key={model}>
            {model}
          </span>
        )) : <span className="provider-model-chip provider-model-chip--empty">{t("settings.noModelsConfigured")}</span>}
        {hiddenModelCount > 0 && (
          <span className="provider-model-chip provider-model-chip--more">
            {t("settings.moreModels", { n: hiddenModelCount })}
          </span>
        )}
      </div>
    </div>
  );
}

function ProviderTechnicalDetails({ group }: { group: ProviderAccessGroup }) {
  const t = useT();
  const imageInputUnsupported = group.providers.length > 0 && group.providers.every((provider) => providerVisionCapabilityForView(provider) === "unsupported");
  return (
    <details className="provider-technical-details">
      <summary>{t("settings.providerAccess")}</summary>
      <dl>
        {group.providers.length === 1 ? (
          <>
            <div><dt>{t("settings.providerProtocol")}</dt><dd>{providerProtocolDisplayName(group.kind)}</dd></div>
            <div><dt>{t("settings.providerBaseUrlLabel")}</dt><dd>{group.baseUrl || t("common.none")}</dd></div>
          </>
        ) : group.providers.map((provider) => (
          <div key={provider.name}><dt>{provider.name}</dt><dd>{providerProtocolDisplayName(provider.kind)} · {provider.baseUrl || t("common.none")}</dd></div>
        ))}
        <div>
          <dt>{t("settings.providerApiKeyEnv")}</dt>
          <dd>{group.apiKeyEnv || t("common.none")}</dd>
        </div>
        {imageInputUnsupported && (
          <div>
            <dt>{t("settings.visionModel")}</dt>
            <dd>{t("settings.imageInputUnsupported")}</dd>
          </div>
        )}
        {group.keySource && (
          <div>
            <dt>{t("settings.providerKey")}</dt>
            <dd title={group.keySourcePath || undefined}>{group.keySource}</dd>
          </div>
        )}
      </dl>
    </details>
  );
}

function providerProtocolDisplayName(kind: string): string {
  return providerProtocolLabel(kind);
}

function ProviderAccessMoreMenu({
  busy,
  builtIn,
  onRemove,
}: {
  busy: boolean;
  builtIn: boolean;
  onRemove: () => void | Promise<void>;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const disabled = busy;
  const tooltip = t("settings.themeGallery.moreActions");

  return (
    <div className="provider-access-more">
      <Tooltip label={tooltip}>
        <button
          ref={triggerRef}
          type="button"
          className="btn btn--small provider-access-more__trigger"
          aria-label={t("settings.themeGallery.moreActions")}
          aria-haspopup="menu"
          aria-expanded={open}
          disabled={disabled}
          onClick={() => setOpen((current) => !current)}
        >
          <MoreHorizontal size={16} aria-hidden="true" />
        </button>
      </Tooltip>
      <AnchoredPopover
        open={open && !disabled}
        anchorRef={triggerRef}
        onClose={() => setOpen(false)}
        className="provider-access-more__menu"
        align="end"
        placement="bottom"
      >
        <div className="provider-access-more__items" role="menu" aria-label={t("settings.themeGallery.moreActions")}>
          <InlineConfirmButton
            label={<><Trash2 size={14} aria-hidden="true" />{t("settings.removeProviderAccess")}</>}
            confirmLabel={builtIn ? t("settings.confirmRemoveProviderAccess") : t("settings.confirmDeleteProvider")}
            cancelLabel={t("common.cancel")}
            danger={!builtIn}
            buttonRole="menuitem"
            onConfirm={async () => {
              setOpen(false);
              await onRemove();
            }}
          />
        </div>
      </AnchoredPopover>
    </div>
  );
}

function ProviderModelDraftPicker({
  draft,
  busy,
  fetching,
  onToggle,
  onSelectAll,
  onClear,
  onCancel,
  onSave,
}: {
  draft: ProviderModelDraft;
  busy: boolean;
  fetching: boolean;
  onToggle: (model: string) => void;
  onSelectAll: () => void;
  onClear: () => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  // Debounce search to avoid expensive filtering on every keystroke
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(query), 150);
    return () => clearTimeout(timer);
  }, [query]);
  const selected = new Set(draft.selected);
  const q = debouncedQuery.trim().toLowerCase();
  const visibleCandidates = useMemo(
    () => (q ? draft.candidates.filter((model) => model.toLowerCase().includes(q)) : draft.candidates),
    [draft.candidates, q],
  );
  const deferredCandidates = useDeferredValue(visibleCandidates);
  const disabled = busy || fetching;

  return (
    <div className="provider-model-draft">
      <div className="provider-model-draft__head">
        <div>
          <span>{t("settings.modelCandidatesSelected", { n: draft.selected.length })}</span>
        </div>
        <div className="provider-model-draft__tools">
          <button type="button" className="btn btn--small" disabled={disabled || draft.selected.length === draft.candidates.length} onClick={onSelectAll} title={t("settings.selectAllModels")} aria-label={t("settings.selectAllModels")}>
            <ListChecks size={17} />
          </button>
          <button type="button" className="btn btn--small" disabled={disabled || draft.selected.length === 0} onClick={onClear} title={t("settings.clearModelSelection")} aria-label={t("settings.clearModelSelection")}>
            <Check size={17} style={{ opacity: .4 }} />
          </button>
        </div>
      </div>
      <input
        className="mem-input provider-model-draft__search"
        placeholder={t("settings.modelCandidateSearch")}
        value={query}
        disabled={disabled}
        onChange={(e) => setQuery(e.target.value)}
      />
      <div className="provider-model-draft__list" role="list" aria-label={t("settings.modelList")}>
        {deferredCandidates.length > 0 ? deferredCandidates.map((model) => {
          const enabled = selected.has(model);
          const capability = providerModelVisionCapability(
            {modelCapabilities: draft.modelCapabilities},
            model,
            draft.visionModels,
          );
          return (
            <div className="provider-model-draft__option" key={model} role="listitem">
              <label className="provider-model-draft__model">
                <input
                  type="checkbox"
                  checked={enabled}
                  disabled={disabled}
                  onChange={() => onToggle(model)}
                />
                <span>{model}</span>
              </label>
              <div className="provider-model-draft__capabilities" aria-label={t("settings.modelCapabilitiesAria", { model })}>
                <span>{t("settings.textInput")}</span>
                <span>{capability === "supported"
                  ? t("settings.visionModel")
                  : capability === "unsupported"
                    ? t("settings.imageInputUnsupported")
                    : t("settings.imageInputUnknown")}</span>
              </div>
            </div>
          );
        }) : (
          <div className="provider-model-draft__empty">{t("settings.noMatchingCandidateModels")}</div>
        )}
      </div>
      <div className="provider-model-draft__actions">
        <button type="button" className="btn btn--small" disabled={disabled} onClick={onCancel}>
          {t("common.cancel")}
        </button>
        <button type="button" className="btn btn--primary btn--small" disabled={disabled || draft.selected.length === 0} onClick={onSave}>
          {t("settings.saveEnabledModels")}
        </button>
      </div>
    </div>
  );
}

function ProviderServiceCapabilities({
  supported,
  configured,
  models,
  hiddenModelCount,
  showModelSummary = false,
  enabled,
  disabled,
  onChange,
}: {
  supported: boolean;
  configured?: boolean;
  models: string[];
  hiddenModelCount?: number;
  showModelSummary?: boolean;
  enabled: boolean;
  disabled: boolean;
  onChange: (enabled: boolean) => void;
}) {
  const t = useT();
  const capabilityID = useId();
  if (!supported) return null;
  return (
    <section className="provider-capabilities" aria-labelledby={capabilityID}>
      <div className="provider-card-block__label" id={capabilityID}>
        {t("settings.providerCapabilities")}
      </div>
      {showModelSummary && (
        <ProviderModelSummary
          configured={Boolean(configured)}
          models={models}
          hiddenModelCount={hiddenModelCount ?? 0}
          compact
        />
      )}
      <label className="provider-capability-row">
        <span className="provider-capability-row__copy">
          <span className="provider-capability-row__title">
            {t("settings.serverWebSearch")}
            <span className="badge badge--project">{t("settings.recommended")}</span>
          </span>
          <span>{t("settings.serverWebSearchHint")}</span>
        </span>
        <input
          className="provider-capability-row__switch"
          type="checkbox"
          role="switch"
          checked={enabled}
          disabled={disabled}
          onChange={(event) => onChange(event.target.checked)}
        />
      </label>
    </section>
  );
}

export function providerAccessGroups(providers: ProviderView[], t: ReturnType<typeof useT>): ProviderAccessGroup[] {
  const groups = new Map<string, ProviderAccessGroup>();
  for (const p of providers) {
    const id = providerGroupID(p);
    const builtIn = id.startsWith("builtin:");
    const existing = groups.get(id);
    if (existing) {
      existing.providers.push(p);
      existing.keySet = existing.keySet || p.keySet;
      existing.requiresKey = existing.requiresKey && providerRequiresKey(p);
      existing.configured = existing.configured || providerIsConfigured(p);
      if (!existing.keySource && p.keySource) existing.keySource = p.keySource;
      if (!existing.keySourcePath && p.keySourcePath) existing.keySourcePath = p.keySourcePath;
      existing.models = uniqueStrings([...existing.models, ...p.models]);
      continue;
    }
    groups.set(id, {
      id,
      label: providerGroupLabel(p, t),
      description: providerGroupDescription(p, t),
      builtIn,
      providers: [p],
      apiKeyEnv: p.apiKeyEnv,
      keySet: p.keySet,
      requiresKey: providerRequiresKey(p),
      configured: providerIsConfigured(p),
      keySource: p.keySource,
      keySourcePath: p.keySourcePath,
      baseUrl: p.baseUrl,
      kind: p.kind,
      models: uniqueStrings(p.models),
    });
  }
  return Array.from(groups.values());
}

function providerBaseHost(baseUrl: string): string {
  try {
    return new URL(baseUrl).hostname.toLowerCase();
  } catch {
    return "";
  }
}

type ProviderVisionCapability = "configurable" | "unsupported";



export function providerSupportsServerWebSearchForView(
  provider: Pick<ProviderView, "kind" | "baseUrl" | "serverWebSearchCapability">,
): boolean {
  if (typeof provider.serverWebSearchCapability === "boolean") {
    return provider.serverWebSearchCapability;
  }
  return providerSupportsServerWebSearch(provider.kind, provider.baseUrl);
}

export function providerVisionCapabilityForView(
  provider: Pick<ProviderView, "kind" | "baseUrl" | "visionCapability">,
): ProviderVisionCapability {
  if (provider.visionCapability === "unsupported") {
    return "unsupported";
  }
  return "configurable";
}

function canonicalOfficialProviderName(name: string): string {
  switch (name.trim()) {
    case "deepseek-flash":
    case "deepseek-pro":
      return "deepseek";
    default:
      return name.trim();
  }
}

function officialProviderKind(p: ProviderView): string {
  if (!p.builtIn) return "";
  const name = canonicalOfficialProviderName(p.name);
  const host = providerBaseHost(p.baseUrl);
  if (name === "deepseek" && host === "api.deepseek.com") return "deepseek";
  return "";
}

function providerGroupID(p: ProviderView): string {
  const official = officialProviderKind(p);
  if (official) return `builtin:${official}`;
  if (isOpenCodeGoProviderName(p.name)) return "custom:opencode-go";
  if (p.name === "opencode-zen-anthropic") return "custom:opencode-zen";
  return `custom:${p.name}`;
}

function providerGroupLabel(p: ProviderView, t?: ReturnType<typeof useT>): string {
  if (p.displayName?.trim()) return p.displayName.trim();
  const id = providerGroupID(p);
  if (id === "builtin:deepseek") return t ? t("settings.providerLabel.deepseek") : "DeepSeek";
  if (id === "custom:opencode-go") return t ? t("settings.providerLabel.opencodeGo") : "OpenCode Go";
  if (id === "custom:opencode-zen") return t ? t("settings.providerLabel.opencodeZen") : "OpenCode Zen";
  return p.name;
}

function providerGroupDescription(p: ProviderView, t: ReturnType<typeof useT>): string {
  const id = providerGroupID(p);
  if (id === "builtin:deepseek") {
    return providerProtocolLabel(p.kind);
  }
  if (id === "custom:opencode-go") return t("settings.providerDesc.opencodeGo");
  if (id === "custom:opencode-zen") return t("settings.providerDesc.opencodeZen");
  return "";
}

function isOpenCodeGoProviderName(name: string): boolean {
  switch (name.trim()) {
    case "opencode-go":
    case "opencode-go-anthropic":
    case "opencode-go-responses":
    case "opencode-go-deepseek-anthropic":
    case "opencode-go-deepseek-responses":
      return true;
    default:
      return false;
  }
}

function uniqueStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    if (value && !seen.has(value)) {
      seen.add(value);
      out.push(value);
    }
  }
  return out;
}

function parseProviderListInput(value: string): string[] {
  return uniqueStrings(value
    .split(/[,，]/)
    .map((entry) => entry.trim())
    .filter(Boolean));
}

const ProviderModelDialog = lazy(() => import("./ProviderModelDialog"));

export const ProviderEditorModelPicker = memo(function ProviderEditorModelPicker({
  actions,
  conversationModel,
  candidates,
  selectedModels,
  visionModels,
  modelCapabilities,
  contextWindows,
  inheritedContextWindow,
  disabled,
  onToggleModel,
  onEditModel,
  onSelectAll,
  onClear,
}: {
  actions?: ReactNode;
  conversationModel?: string;
  candidates: string[];
  selectedModels: string[];
  visionModels: string[];
  modelCapabilities: ProviderModelCapabilityView[];
  contextWindows: Record<string, string>;
  inheritedContextWindow?: number;
  disabled: boolean;
  onToggleModel: (model: string) => void;
  onEditModel?: (model: string) => void;
  onSelectAll: () => void;
  onClear: () => void;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(query), 150);
    return () => clearTimeout(timer);
  }, [query]);
  const q = debouncedQuery.trim().toLowerCase();
  const visibleCandidates = q
    ? candidates.filter((model) => model.toLowerCase().includes(q))
    : candidates;
  const deferredCandidates = useDeferredValue(visibleCandidates);
  const selected = new Set(selectedModels);
  return (
    <div className="provider-model-draft provider-model-draft--inline">
      <div className="provider-model-draft__head provider-model-toolbar">
        <strong>{t("settings.modelList")}</strong>
        <span>{t("settings.modelCandidatesSelected", { n: selectedModels.length })}</span>
        <div className="provider-model-draft__tools">
          {actions}
          <details className="provider-key-compact__more">
            <summary className="btn provider-icon-action" title={t("settings.themeGallery.moreActions")} aria-label={t("settings.themeGallery.moreActions")}><MoreHorizontal size={17} /></summary>
            <div className="provider-key-compact__menu">
              <button type="button" className="btn" disabled={disabled || selectedModels.length === candidates.length} onClick={onSelectAll}>{t("settings.selectAllModels")}</button>
              <button type="button" className="btn" disabled={disabled || selectedModels.length === 0} onClick={onClear}>{t("settings.clearModelSelection")}</button>
            </div>
          </details>
        </div>
      </div>

      {candidates.length > 8 && (
        <input
          className="mem-input provider-model-draft__search"
          placeholder={t("settings.modelCandidateSearch")}
          value={query}
          disabled={disabled}
          onChange={(e) => setQuery(e.target.value)}
        />
      )}
      <div className="provider-model-draft__list" role="list" aria-label={t("settings.modelList")}>
        {deferredCandidates.length > 0 ? deferredCandidates.map((model) => {
          const enabled = selected.has(model);
          const capability = providerModelVisionCapability(
            {modelCapabilities},
            model,
            visionModels,
          );
          return (
            <div className="provider-model-draft__option" key={model} role="listitem">
              <label className="provider-model-draft__model">
                <input
                  type="checkbox"
                  checked={enabled}
                  disabled={disabled}
                  onChange={() => onToggleModel(model)}
                />
                <span title={model}>{model}</span>
                {conversationModel === model && <small className="provider-conversation-model">{t("onboarding.conversationModel")}</small>}
              </label>
              <div className="provider-model-draft__capabilities" aria-label={t("settings.modelCapabilitiesAria", { model })}>
                {capability === "supported" && <span>{t("settings.visionModel")}</span>}
                {capability === "unknown" && <span>{t("settings.imageInputUnknown")}</span>}
              </div>
              <span className="provider-context-badge">{Number(contextWindows[model]) || inheritedContextWindow ? new Intl.NumberFormat("en", {notation:"compact",maximumFractionDigits:1}).format(Number(contextWindows[model]) || inheritedContextWindow!) : t("settings.models.inherit")}</span>
              <button type="button" className="btn provider-icon-action" title={t("settings.models.options")} aria-label={`${t("settings.models.options")}: ${model}`} aria-haspopup="dialog" disabled={disabled || !onEditModel} onClick={()=>onEditModel?.(model)}><SlidersHorizontal size={17}/></button>
            </div>
          );
        }) : (
          <div className="provider-model-draft__empty">{t("settings.noMatchingCandidateModels")}</div>
        )}
      </div>
    </div>
  );
});

export function ProviderEditor({
  hideConnectionName = false,
  onOnboardingStart,
  onOnboardingLater,
  initial,
  providerPresets = [],
  kinds,
  busy,
  onCancel,
  onSave,
}: {
  hideConnectionName?: boolean;
  onOnboardingStart?: () => Promise<void>;
  onOnboardingLater?: () => void;
  initial?: ProviderView;
  providerPresets?: ProviderPresetView[];
  kinds: string[];
  busy: boolean;
  onCancel: () => void;
  onSave: (p: ProviderView, key?: string) => void | Promise<void>;
  onSaveKey?: (apiKeyEnv: string, value: string) => Promise<void>;
  onClearKey?: (apiKeyEnv: string) => Promise<void>;
}) {
  const t = useT();
  const [name, setName] = useState(initial?.name ?? "");
  const [displayName, setDisplayName] = useState(initial?.displayName ?? "");
  const [kind, setKind] = useState(initial?.kind ?? "openai");
  const [requestUrl, setRequestUrl] = useState(() => providerRequestURLFromConfig(initial?.kind ?? "openai", initial?.baseUrl ?? "", initial?.requestUrl ?? ""));
  const providerNameInputId = useId();
  const providerUrlInputId = useId();
  const providerUrlHelpId = useId();
  const [models, setModels] = useState((initial?.models ?? []).join(", "));
  const [modelDialog, setModelDialog] = useState<string | null>(null);
  const [modelOverrides, setModelOverrides] = useState(initial?.modelOverrides ?? []);
  const [showKey, setShowKey] = useState(false);
  const [modelCandidates, setModelCandidates] = useState<string[]>(initial?.models ?? []);
  const [modelCapabilities, setModelCapabilities] = useState<ProviderModelCapabilityView[]>(initial?.modelCapabilities ?? []);
  const [modelsUrl, setModelsUrl] = useState(initial?.modelsUrl ?? "");
  const [apiKeyEnv, setApiKeyEnv] = useState(initial?.apiKeyEnv ?? "");
  useEffect(() => { if (initial) setApiKeyEnv(initial.apiKeyEnv); }, [initial?.apiKeyEnv]);
  const [headersDraft, setHeadersDraft] = useState(formatProviderHeaders(initial?.headers));
  const [extraBodyDraft, setExtraBodyDraft] = useState(formatProviderExtraBody(initial?.extraBody));
  const [authHeader, setAuthHeader] = useState(Boolean(initial?.authHeader));
  const [noProxy, setNoProxy] = useState(Boolean(initial?.noProxy));
  const [keyDraft, setKeyDraft] = useState("");
  const [balanceUrl, setBalanceUrl] = useState(initial?.balanceUrl ?? "");
  // Empty when unset so the placeholder (and its "0 = disabled" hint) reads instead
  // of a bare "0"; saved back as 0.
  const [ctx, setCtx] = useState(initial?.contextWindow ? String(initial.contextWindow) : "");
  const [modelContextWindows, setModelContextWindows] = useState<Record<string, string>>(
    () => providerModelContextWindowDrafts(initial?.modelOverrides),
  );
  const [reasoningProtocol, setReasoningProtocol] = useState(normalizeReasoningProtocol(initial?.reasoningProtocol));
  const [thinking, setThinking] = useState(normalizeThinkingMode(initial?.thinking));
  const [webSearch, setWebSearch] = useState(Boolean(initial?.webSearch));
  const [supportedEfforts] = useState<string[]>(initial?.supportedEfforts ?? []);
  const [defaultEffort] = useState(initial?.defaultEffort ?? "");
  const [fetchingModels, setFetchingModels] = useState(false);
  const [fetchStatus, setFetchStatus] = useState<string | null>(null);
  const [fetchFallback, setFetchFallback] = useState<string | null>(null);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const draftSnapshot = JSON.stringify([name, hideConnectionName ? "" : displayName, kind, requestUrl, models, modelsUrl, headersDraft, extraBodyDraft, authHeader, noProxy, keyDraft, balanceUrl, ctx, modelContextWindows, modelOverrides, modelCapabilities, reasoningProtocol, thinking, webSearch]);
  const [savedSnapshot, setSavedSnapshot] = useState(draftSnapshot);
  const latestDraftSnapshot = useRef(draftSnapshot);
  const saveGeneration = useRef(0);
  latestDraftSnapshot.current = draftSnapshot;
  const dirty = draftSnapshot !== savedSnapshot;
  const onboardingReady = Boolean(onOnboardingStart && initial?.added && providerIsConfigured(initial) && initial.models.length && !dirty);
  const conversationModel = onboardingReady ? providerDefaultModel(initial!.default, initial!.models) : undefined;
  const startConversation = async () => {
    if (!onboardingReady || busy) return;
    setFetchFallback(null);
    try { await onOnboardingStart?.(); }
    catch (error) { setFetchFallback(String((error as Error)?.message ?? error)); }
  };
  const isNewCustomProvider = !initial;
  const editorCatalog = useMemo(() => {
    if (initial?.catalog) return initial.catalog;
    if (initial?.presetId) {
      const preset = providerPresets.find(item => item.id === initial.presetId);
      if (preset) return catalogForPreset(preset);
    }
    const catalogs = providerPresets.map(catalogForPreset);
    return catalogs.find(catalog => Object.entries(catalog.protocols ?? {}).some(([routeKind, route]) =>
      providerRequestURLFromConfig(routeKind, route.baseUrl, "") === requestUrl,
    ));
  }, [initial?.catalog, initial?.presetId, providerPresets, requestUrl]);
  const providerKindChoices = useMemo(() => {
    const registered = editorCatalog ? Object.keys(editorCatalog.protocols ?? {}) : kinds;
    const choices = providerProtocolChoices(kind, initial?.kind, registered, Boolean(editorCatalog));
    return choices.length > 0 ? choices : ["openai"];
  }, [editorCatalog, kind, initial?.kind, kinds]);
  const effectiveKind = providerEditorEffectiveKind(isNewCustomProvider, kind, providerKindChoices);
  const effectiveRequestUrl = requestUrl.trim();
  const endpointMismatch = providerEndpointMismatchDetail(effectiveKind, effectiveRequestUrl, editorCatalog);
  const effectiveBaseUrl = providerBaseURLForSave(initial, effectiveKind, effectiveRequestUrl);
  const effectiveModelsUrl = modelsUrl.trim();
  const initialEffectiveBaseUrl = initial ? trimmedBaseURL(initial.baseUrl) : "";
  const retainedServerWebSearchCapability = initial &&
    effectiveKind.trim().toLowerCase() === initial.kind.trim().toLowerCase() &&
    trimmedBaseURL(effectiveBaseUrl) === initialEffectiveBaseUrl
    ? initial.serverWebSearchCapability
    : undefined;
  const effectiveServerWebSearchCapability = retainedServerWebSearchCapability ??
    providerSupportsServerWebSearch(effectiveKind, effectiveBaseUrl);
  const effectiveHeaders = parseProviderHeaders(headersDraft);
  const extraBodyParse = useMemo(() => {
    try {
      return { value: parseProviderExtraBody(extraBodyDraft, t), error: "" };
    } catch (e) {
      return { value: {}, error: providerExtraBodyParseError(e, t) };
    }
  }, [extraBodyDraft, t]);
  const effectiveExtraBody = extraBodyParse.value;
  const extraBodyInvalid = Boolean(extraBodyDraft.trim() && extraBodyParse.error);
  const modelNames = useMemo(
    () => parseProviderListInput(models),
    [models],
  );
  const modelCandidateNames = useMemo(
    () => uniqueStrings([...modelCandidates, ...modelNames]),
    [modelCandidates, modelNames],
  );
  const visionModelNames = useMemo(
    () => providerVisionModelsForView({models:modelNames, modelOverrides, modelCapabilities}),
    [modelNames, modelOverrides, modelCapabilities],
  );

  // Empty supportedEfforts means "use protocol defaults". The simplified
  // provider flow no longer edits these levels directly, but it preserves
  // existing advanced TOML unless the user explicitly disables reasoning.
  const cleanedSupportedEfforts = reasoningProtocol !== "none"
    ? uniqueStrings(
        supportedEfforts
          .map((level) => level.toLowerCase().trim())
          .filter((level) => level && level !== "auto")
      )
    : [];
  const normalizedDefaultEffort = defaultEffort.toLowerCase().trim();
  const cleanDefaultEffort = cleanedSupportedEfforts.includes(normalizedDefaultEffort) ? normalizedDefaultEffort : "";

  const fetchModels = async () => {
    if (extraBodyInvalid) return;
    setFetchingModels(true);
    setFetchStatus(null);
    setFetchFallback(null);
    try {
      const effectiveApiKeyEnv = providerApiKeyEnvForSave(name, apiKeyEnv, keyDraft);
      if (!apiKeyEnv.trim()) setApiKeyEnv(effectiveApiKeyEnv);
      const fetchCatalog = (provider: ProviderView) => keyDraft.trim()
        ? app.FetchProviderModelCatalogDraft(provider, keyDraft.trim())
        : cachedFetchProviderModelCatalog(p => app.FetchProviderModelCatalog(p), provider, true);
      const fetchedCapabilities = await fetchCatalog({
        name: name.trim() || t("settings.newProviderDraftName"),
        builtIn: initial?.builtIn ?? false,
        added: initial?.added ?? true,
        kind: effectiveKind,
        baseUrl: effectiveBaseUrl,
        requestUrl: effectiveRequestUrl,
        modelsUrl: effectiveModelsUrl,
        models: [],
        visionModels: [],

        default: "",
        apiKeyEnv: effectiveApiKeyEnv,
        headers: effectiveHeaders,
        extraBody: effectiveExtraBody,
        authHeader,
        noProxy,
        keySet: Boolean(keyDraft.trim()) || (initial?.keySet ?? false),
        balanceUrl: balanceUrl.trim(),
        contextWindow: Number(ctx) || 0,
        reasoningProtocol,
        thinking,
        webSearch: effectiveServerWebSearchCapability && webSearch,
        serverWebSearchCapability: effectiveServerWebSearchCapability,
        supportedEfforts: cleanedSupportedEfforts,
        defaultEffort: cleanDefaultEffort,
        modelOverrides: mergeProviderModelContextWindows(modelOverrides, parseProviderListInput(models), modelContextWindows),
      });
      const fetched = fetchedCapabilities.map((item) => item.model);
      if (fetched.length === 0) {
        setFetchFallback(t("settings.fetchModelsManualFallbackEmpty"));
        return;
      }
      setModelCandidates(current => uniqueStrings([...current, ...fetched]));
      setModelCapabilities(current => [...current.filter(item => !fetched.includes(item.model)), ...fetchedCapabilities]);
      setFetchStatus(t("settings.fetchModelsSuccess", { n: fetched.length }));
    } catch (e) {
      setFetchFallback(providerModelFetchFallbackMessage(e, t));
    } finally {
      setFetchingModels(false);
    }
  };

  const save = async () => {
    if (extraBodyInvalid) return;
    const generation = ++saveGeneration.current;
    setFetchStatus(null);
    setFetchFallback(null);
    const ms = parseProviderListInput(models);
    const vms = providerVisionModelsForView({models: ms, modelOverrides, modelCapabilities});
    const effectiveApiKeyEnv = providerApiKeyEnvForSave(name, apiKeyEnv, keyDraft);
    const provider: ProviderView = {
      name: name.trim(),
      ...(hideConnectionName ? {} : { displayName: displayName.trim() }),
      ...(initial?.presetId ? { presetId: initial.presetId } : {}),
      ...(initial?.catalog ? { catalog: initial.catalog } : {}),
      builtIn: initial?.builtIn ?? false,
      added: initial?.added ?? true,
      kind: effectiveKind,
      baseUrl: effectiveBaseUrl,
      requestUrl: effectiveRequestUrl,
      models: ms,
      visionModels: vms,
      default: providerDefaultModel(initial?.default ?? "", ms),
      apiKeyEnv: effectiveApiKeyEnv,
      headers: effectiveHeaders,
      extraBody: effectiveExtraBody,
      authHeader,
      noProxy,
      modelsUrl: effectiveModelsUrl,
      keySet: Boolean(keyDraft.trim()) || (initial?.keySet ?? false),
      balanceUrl: balanceUrl.trim(),
      contextWindow: Number(ctx) || 0,
      reasoningProtocol,
      thinking,
      webSearch: effectiveServerWebSearchCapability && webSearch,
      serverWebSearchCapability: effectiveServerWebSearchCapability,
      supportedEfforts: cleanedSupportedEfforts,
      // Clear the stored default if no levels are selected; the backend's
      // NormalizeEffort would otherwise silently ignore an unsupported value.
      defaultEffort: cleanedSupportedEfforts.length > 0 ? cleanDefaultEffort : "",
      modelOverrides: mergeProviderModelContextWindows(modelOverrides, ms, modelContextWindows),
      modelCapabilities,
    };
    try {
      await onSave(provider, keyDraft.trim() || undefined);
      if (generation !== saveGeneration.current) return;
      setShowKey(false);
      if (latestDraftSnapshot.current === draftSnapshot) {
        const committed = JSON.parse(draftSnapshot) as unknown[];
        committed[10] = ""; // The key draft is cleared only if no newer edit exists.
        setKeyDraft("");
        setSavedSnapshot(JSON.stringify(committed));
      } else {
        setSavedSnapshot(draftSnapshot);
      }
    } catch (e) {
      if (generation === saveGeneration.current && latestDraftSnapshot.current === draftSnapshot) {
        setFetchFallback(String((e as Error)?.message ?? e));
      }
    }
  };

  const canFetch = Boolean(name.trim() && effectiveBaseUrl);

  const setModelsFromList = (nextModels: string[]) => {
    setModels(uniqueStrings(nextModels).join(", "));
  };

  const toggleEditorModel = (model: string) => {
    const selected = new Set(modelNames);
    if (selected.has(model)) selected.delete(model);
    else selected.add(model);
    setModelsFromList(modelCandidateNames.filter((candidate) => selected.has(candidate)));
  };

  const applyModelDetails = (draft: ModelDetailsDraft) => {
    if (modelDialog === "") {
      setModelCandidates(current => uniqueStrings([...current, draft.model]));
      setModels(current => uniqueStrings([...parseProviderListInput(current), draft.model]).join(", "));
    }
    setModelContextWindows(current => ({...current, [draft.model]:draft.contextWindow}));
    setModelOverrides(current => {
      const previous = current.find(item => item.model === draft.model);
      return [...current.filter(item => item.model !== draft.model), {...previous, model:draft.model, reasoningProtocol:previous?.reasoningProtocol ?? "", supportedEfforts:draft.supportedEfforts, defaultEffort:draft.defaultEffort, vision:draft.vision, maxOutputTokens:draft.maxOutputTokens}];
    });
    setModelDialog(null);
  };

  const deleteModel = () => {
    if (!modelDialog || busy || fetchingModels) return;
    const model = modelDialog;
    setModels(current => parseProviderListInput(current).filter(item => item !== model).join(", "));
    setModelCandidates(current => current.filter(item => item !== model));
    setModelOverrides(current => current.filter(item => item.model !== model));
    setModelCapabilities(current => current.filter(item => item.model !== model));
    setModelContextWindows(current => Object.fromEntries(Object.entries(current).filter(([key]) => key !== model)));
    setModelDialog(null);
  };

  const selectAllEditorModels = () => {
    setModelsFromList(modelCandidateNames);
  };

  const clearEditorModels = () => {
    setModels("");
  };

  const serviceCapabilities = <ProviderServiceCapabilities
    supported={effectiveServerWebSearchCapability} models={modelNames} enabled={webSearch}
    disabled={busy || fetchingModels} onChange={setWebSearch} />;
  const advancedFields = (
    <details className="provider-editor-advanced" open={advancedOpen} onToggle={(e) => setAdvancedOpen(e.currentTarget.open)}>
      <summary>
        <span className="provider-editor-advanced__title">
          <ChevronDown className="provider-editor-advanced__icon" size={16} aria-hidden="true" />
          {t("settings.providerAdvancedSettings")}
        </span>
        <span className="provider-editor-advanced__hint">
          {advancedOpen ? t("settings.providerAdvancedCollapseHint") : t("settings.providerAdvancedExpandHint")}
        </span>
      </summary>
      <div className="provider-editor-advanced__body">
        {onOnboardingStart ? serviceCapabilities : null}
        <label className="set-label">{t("settings.providerModelsUrl")}</label>
        <input
          className="mem-input"
          placeholder={t("settings.providerModelsUrlPlaceholder")}
          value={modelsUrl}
          onChange={(e) => setModelsUrl(e.target.value)}
        />
        <div className="mem-hint">{t("settings.providerModelsUrlHint")}</div>
        <label className="set-label">{t("settings.providerHeaders")}</label>
        <textarea
          className="mem-textarea provider-headers-textarea"
          placeholder={t("settings.providerHeadersPlaceholder")}
          value={headersDraft}
          onChange={(e) => setHeadersDraft(e.target.value)}
          rows={3}
        />
        <div className="mem-hint">{t("settings.providerHeadersHint")}</div>
        <label className="set-label">{t("settings.providerExtraBody")}</label>
        <textarea
          className="mem-textarea provider-headers-textarea"
          placeholder={t("settings.providerExtraBodyPlaceholder")}
          value={extraBodyDraft}
          onChange={(e) => setExtraBodyDraft(e.target.value)}
          rows={4}
        />
        <div className={`mem-hint${extraBodyInvalid ? " mem-hint--error" : ""}`}>
          {extraBodyInvalid ? <ErrorMessage error={extraBodyParse.error} /> : t("settings.providerExtraBodyHint")}
        </div>
        <label className="set-check">
          <input
            type="checkbox"
            checked={authHeader}
            onChange={(e) => setAuthHeader(e.target.checked)}
          />
          {t("settings.providerAuthHeader")}
        </label>
        <div className="mem-hint">{t("settings.providerAuthHeaderHint")}</div>
        <label className="set-check">
          <input
            type="checkbox"
            checked={noProxy}
            onChange={(e) => setNoProxy(e.target.checked)}
          />
          {t("settings.providerNoProxy")}
        </label>
        <div className="mem-hint">{t("settings.providerNoProxyHint")}</div>
        <label className="set-label">{t("settings.reasoningProtocol")}</label>
        <SettingsSelect className="mem-select" aria-label={t("settings.reasoningProtocol")} value={reasoningProtocol} onValueChange={(value) => setReasoningProtocol(value)}>
          {REASONING_PROTOCOLS.map((protocol) => (
            <option key={protocol || "auto"} value={protocol}>
              {reasoningProtocolLabel(protocol, t)}
            </option>
          ))}
        </SettingsSelect>
        <div className="mem-hint">{t("settings.reasoningProtocolHint")}</div>
        <label className="set-label">{t("settings.thinkingMode")}</label>
        <SettingsSelect className="mem-select" aria-label={t("settings.thinkingMode")} value={thinking} onValueChange={(value) => setThinking(normalizeThinkingMode(value))}>
          {THINKING_MODES.map((mode) => (
            <option key={mode || "auto"} value={mode}>
              {thinkingModeLabel(mode, t)}
            </option>
          ))}
        </SettingsSelect>
        <div className="mem-hint">{t("settings.thinkingModeHint")}</div>
        <label className="set-label">{t("settings.providerBalanceUrl")}</label>
        <input
          className="mem-input"
          placeholder={t("settings.balanceUrlPlaceholder")}
          value={balanceUrl}
          onChange={(e) => setBalanceUrl(e.target.value)}
        />
        <div className="mem-hint">{t("settings.balanceUrlHint")}</div>
        <label className="set-label">{t("settings.providerContextWindow")}</label>
        <input
          className="mem-input"
          inputMode="numeric"
          min={0}
          placeholder={t("settings.contextWindowPlaceholder")}
          type="number"
          value={ctx}
          onChange={(e) => setCtx(e.target.value)}
        />
        <div className="mem-hint">{t("settings.contextWindowHint")}</div>
      </div>
    </details>
  );

  return (
    <div className={`provider-editor provider-editor--compact${isNewCustomProvider ? " provider-editor--wizard" : ""}`}>
      <div className="provider-editor__body">
      {!hideConnectionName && <>
      <label className="set-label" htmlFor={providerNameInputId}>{t(initial ? "settings.connections.name" : "settings.customProviderName")}</label>
      <input id={providerNameInputId} className="mem-input provider-name-input"
        placeholder={initial ? initial.name : t("settings.customProviderNamePlaceholder")}
        value={initial ? displayName : name} disabled={busy}
        onChange={(e) => initial ? setDisplayName(e.target.value) : setName(e.target.value)} />
      {initial && <details className="mem-hint"><summary>{t("settings.connections.id")}</summary><code>{initial.name}</code></details>}
      </>}
      <section className="provider-connection-fields">
      <div className="provider-connection-field">
      <label className="set-label" htmlFor={providerUrlInputId}>
        {t("settings.providerBaseUrlLabel")}
      </label>
      <input
        id={providerUrlInputId}
        className="mem-input provider-url-input"
        aria-describedby={providerUrlHelpId}
        placeholder={t("settings.providerChatUrlPlaceholder")}
        value={requestUrl}
        onChange={(e) => setRequestUrl(e.target.value)}
      />
      <span id={providerUrlHelpId} className="provider-field-help">{t("settings.providerRequestUrlHint")}</span>
      </div>
      <div className="provider-connection-field">
      <label className="set-label">{t("settings.providerProtocol")}</label>
      <SettingsSelect className="mem-select" aria-label={t("settings.providerProtocol")} title={providerKindHint(effectiveKind, t)} value={kind} disabled={busy || fetchingModels} onValueChange={(value) => {
        const nextKind = value;
        setRequestUrl(current => {
          return providerRequestURLForCatalogFormatChange(kind, nextKind, current, editorCatalog);
        });
        setKind(nextKind);
      }}>
        {providerKindChoices.map((choice) => (
          <option key={choice} value={choice}>
            {providerKindLabel(choice, t)}
          </option>
        ))}
      </SettingsSelect>
      {endpointMismatch.mismatch && <div role="alert" className="banner banner--warning">
        <span>{t("settings.providerProtocolMismatch")}</span>
        {endpointMismatch.recommendedUrl && <button type="button" className="btn btn--small" title={endpointMismatch.recommendedUrl} onClick={() => setRequestUrl(endpointMismatch.recommendedUrl)}>{t("settings.compactRatioApply")}</button>}
      </div>}
      </div>
      <div className="provider-key-single">
        <label htmlFor={`provider-key-${initial?.name ?? "new"}`}>API Key</label>
        <div className="provider-key-single__input">
          <input id={`provider-key-${initial?.name ?? "new"}`} className="mem-input"
            type={showKey ? "text" : "password"} autoComplete="new-password" spellCheck={false}
            placeholder={initial?.keySet ? "••••••••••••••••••••" : "API Key"}
            value={keyDraft} disabled={busy}
            onChange={e => setKeyDraft(e.target.value)} />
          <button type="button" className="btn provider-icon-action" aria-label={showKey ? "Hide API Key" : "Show API Key"}
            title={showKey ? "Hide API Key" : "Show API Key"} aria-pressed={showKey}
            disabled={!keyDraft} onClick={() => setShowKey(value => !value)}>
            {showKey ? <EyeOff size={17} /> : <Eye size={17} />}
          </button>
        </div>
      {onOnboardingStart && initial?.keySet && !keyDraft && <p className="provider-key-saved" role="status"><CheckCircle2 size={18} aria-hidden="true" />{t("onboarding.keySaved")}</p>}
      </div>
      </section>
      {fetchStatus && <div role="status" className="provider-fetch-status provider-fetch-status--ok">{fetchStatus}</div>}
      {fetchFallback && <div role="alert" className="provider-fetch-status provider-fetch-status--warn"><ErrorMessage error={fetchFallback} /></div>}
      {modelDialog !== null && <Suspense fallback={null}><ProviderModelDialog
        baseURL={effectiveRequestUrl} candidates={modelCandidateNames} contextDefault={Number(ctx) || undefined}
        effortOptions={Array.from(new Set([...(modelCapabilities.find(item=>item.model === modelDialog)?.reasoning?.options ?? []).map(option=>option.id), ...(modelOverrides.find(item=>item.model === modelDialog)?.supportedEfforts ?? supportedEfforts ?? [])])).filter(Boolean)}
        initial={modelDialog ? (() => { const override = modelOverrides.find(item=>item.model === modelDialog); return {model:modelDialog, contextWindow:modelContextWindows[modelDialog] ?? "", maxOutputTokens:override?.maxOutputTokens ?? 0, vision:override?.vision ?? null, supportedEfforts:override?.supportedEfforts ?? [], defaultEffort:override?.defaultEffort ?? ""}; })() : undefined}
        capability={modelCapabilities.find(item=>item.model === modelDialog)} busy={busy || fetchingModels}
        onClose={()=>setModelDialog(null)} onApply={applyModelDetails} onDelete={deleteModel}/></Suspense>}
      <ProviderEditorModelPicker
        conversationModel={conversationModel}
        actions={<>
        <button type="button" className="btn provider-icon-action" title={t("settings.fetchModels")} aria-label={t(fetchingModels ? "settings.fetchingModels" : "settings.fetchModels")} disabled={busy || fetchingModels || !canFetch || extraBodyInvalid} onClick={() => void fetchModels()}>{fetchingModels ? <Loader2 size={17} /> : <RefreshCw size={17} />}</button>
        <button className="btn btn--small" disabled={busy || fetchingModels} onClick={() => setModelDialog("")}>{t("settings.models.add")}</button>
        </>}
        candidates={modelCandidateNames}
        selectedModels={modelNames}
        visionModels={visionModelNames}
        modelCapabilities={modelCandidateNames.map(model => { const override = modelOverrides.find(item=>item.model === model)?.vision; const info = modelCapabilities.find(item=>item.model === model); return override == null ? info : {...info, model, state:override ? "supported" : "unsupported", inputModalities:override ? ["text","image"] : ["text"], source:"override"}; }).filter((item): item is ProviderModelCapabilityView => Boolean(item))}
        contextWindows={modelContextWindows}
        inheritedContextWindow={Number(ctx) || undefined}
        disabled={busy || fetchingModels}
        onToggleModel={toggleEditorModel}
        onEditModel={setModelDialog}
        onSelectAll={selectAllEditorModels}
        onClear={clearEditorModels}
      />
      {!onOnboardingStart ? serviceCapabilities : null}
      {advancedFields}
      </div>
      {onboardingReady ? <div data-provider-onboarding className="provider-editor-footer provider-onboarding-footer">
        <div className="provider-onboarding-footer__status" role="status">
          <CheckCircle2 size={22} aria-hidden="true" />
          <div><strong>{t("onboarding.saved")}</strong><span>{t("onboarding.startWithModel", { model: conversationModel! })}</span></div>
        </div>
        <div className="provider-onboarding-footer__actions">
          {onOnboardingLater && <button type="button" className="provider-onboarding-footer__later" disabled={busy} onClick={onOnboardingLater}>{t("onboarding.later")}</button>}
          <button type="button" className="btn btn--primary" disabled={busy || fetchingModels} onClick={() => void startConversation()}>{t("onboarding.finishAndStart")}</button>
        </div>
      </div> : <div className="prov-card__actions provider-editor-footer">
        <span role="status">{t(dirty ? "settings.models.unsaved" : "settings.models.saved")}</span>
        <button className="btn btn--small" onClick={onCancel} disabled={busy}>
          {t("common.cancel")}
        </button>
        <button className="btn btn--primary btn--small" onClick={() => void save()} disabled={busy || fetchingModels || (Boolean(initial) && !dirty) || !name.trim() || !effectiveBaseUrl || !models.trim() || extraBodyInvalid || endpointMismatch.mismatch}>
          {t("settings.models.saveChanges")}
        </button>
      </div>}
    </div>
  );
}

function PermissionsSection({ s, busy, apply }: SectionProps) {
  const t = useT();
  return (
    <SettingsSection title={t("settings.permissionRules")} description={t("settings.ruleForm")}>
      <div className="set-rules-grid">
        {(["deny", "ask", "allow"] as const).map((list) => (
          <RuleList
            key={list}
            list={list}
            rules={s.permissions[list]}
            busy={busy}
            onAdd={(rule) => apply(() => app.AddPermissionRule(list, rule))}
            onRemove={async (rule) => { await apply(() => app.RemovePermissionRule(list, rule)); }}
          />
        ))}
      </div>
    </SettingsSection>
  );
}

type HookScope = "global" | "project";

function HooksSection({ onChanged }: { onChanged: (settings?: SettingsView | null) => void }) {
  const t = useT();
  const [scope, setScope] = useState<HookScope>("global");
  const [view, setView] = useState<HooksSettingsView | null>(null);
  const [jsonText, setJsonText] = useState("");
  const [jsonMessage, setJsonMessage] = useState<string | null>(null);
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [pathMessage, setPathMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async (nextScope: HookScope) => {
    setBusy(true);
    setErr(null);
    try {
      const next = normalizeHooksSettingsView(await app.HooksSettings(nextScope), nextScope);
      setView(next);
      setJsonText(formatHooksJSON(next.hooks, next.events));
      setJsonMessage(null);
      setJsonError(null);
      setPathMessage(null);
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      setView(null);
      setJsonText("");
      setJsonMessage(null);
      setJsonError(null);
      setPathMessage(null);
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load(scope);
  }, [load, scope]);

  const parseHooksEditorJSON = (raw = jsonText): { hooks: HookConfigView[]; text: string } | null => {
    try {
      const hooks = parseHooksJSON(raw, view?.events ?? [], t);
      const text = formatHooksJSON(hooks, view?.events ?? []);
      setJsonText(text);
      setJsonError(null);
      return { hooks, text };
    } catch (e) {
      setJsonError(t("settings.hooksJsonInvalid", { error: String((e as Error)?.message ?? e) }));
      setJsonMessage(null);
      return null;
    }
  };
  const copyHooksJSON = async () => {
    const parsed = parseHooksEditorJSON();
    if (!parsed) return;
    try {
      await navigator.clipboard?.writeText(parsed.text);
      setJsonMessage(t("settings.hooksJsonCopied"));
    } catch {
      setJsonMessage(t("settings.hooksJsonClipboardUnavailable"));
    }
  };
  const formatHooksEditorJSON = (raw = jsonText) => {
    const parsed = parseHooksEditorJSON(raw);
    if (parsed) setJsonMessage(t("settings.hooksJsonFormatted"));
  };
  const pasteHooksJSON = async () => {
    try {
      const raw = await navigator.clipboard?.readText();
      if (!raw) throw new Error(t("settings.hooksJsonClipboardEmpty"));
      setJsonText(raw);
      formatHooksEditorJSON(raw);
    } catch (e) {
      setJsonError(t("settings.hooksJsonPasteFailed", { error: String((e as Error)?.message ?? e) }));
      setJsonMessage(null);
    }
  };
  const copyHooksPath = async () => {
    const path = view?.path?.trim();
    if (!path) {
      setPathMessage(t("settings.hooksPathUnavailable"));
      return;
    }
    try {
      await navigator.clipboard?.writeText(path);
      setPathMessage(t("settings.hooksPathCopied"));
    } catch {
      setPathMessage(t("settings.hooksJsonClipboardUnavailable"));
    }
  };
  const approve = async () => {
    setBusy(true);
    setErr(null);
    try {
      await app.TrustProjectHooksForRoot(view?.projectRoot?.trim() ?? "");
      await load(scope);
      onChanged();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  };
  const save = async () => {
    setBusy(true);
    setErr(null);
    try {
      const parsed = parseHooksEditorJSON();
      if (!parsed) return;
      await app.SaveHooksSettingsForRoot(scope, view?.projectRoot?.trim() ?? "", parsed.hooks);
      await load(scope);
      onChanged();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      {err && <div className="banner banner--error"><ErrorMessage error={err} /></div>}
      <SettingsSection title={t("settings.hooksScopeSection")} description={t("settings.hooksScopeHint")}>
        <SettingsField label={t("settings.hooksScopeField")}>
          <SettingsSelect name="hooks-scope" className="mem-select set-grow" value={scope} disabled={busy} onValueChange={(value) => setScope(value === "project" ? "project" : "global")}>
            <option value="global">{t("settings.hooksGlobal")}</option>
            <option value="project">{t("settings.hooksProject")}</option>
          </SettingsSelect>
        </SettingsField>
        <SettingsField label={t("settings.hooksPath")} hint={scope === "project" ? t("settings.hooksPathProjectHint") : t("settings.hooksPathGlobalHint")}>
          <div className="hooks-path-stack">
            <div className={`hooks-path-display${view?.path ? "" : " hooks-path-display--empty"}`}>
              <code className="hooks-path-display__value" title={view?.path || t("settings.hooksPathUnavailable")}>
                {view?.path || t("settings.hooksPathUnavailable")}
              </code>
              <button className="btn btn--small" disabled={busy || !view?.path} onClick={() => void copyHooksPath()}>{t("settings.hooksPathCopy")}</button>
            </div>
            {pathMessage && <div className="hooks-path-display__message">{pathMessage}</div>}
          </div>
        </SettingsField>
      </SettingsSection>

      <SettingsSection
        title={t("settings.hooks")}
        description={scope === "project" ? t("settings.hooksProjectHint") : t("settings.hooksGlobalHint")}
      >
        {view && scope === "project" && !view.trusted && view.hooks.length > 0 && (
          <div className="banner banner--warning" role="status">
            <span>{t("settings.hooksAwaitingApproval")}</span>
            <button className="btn btn--small" disabled={busy} onClick={() => void approve()}>{t("settings.hooksApprove")}</button>
          </div>
        )}
        {view && (
          <div className="hooks-json-panel">
            <div className="hooks-json-panel__head">
              <div>
                <div className="set-rules__label">{t("settings.hooksJsonTitle")}</div>
                <div className="set-rules__hint">{t("settings.hooksJsonHint")}</div>
              </div>
              <div className="hooks-json-panel__actions">
                <button className="btn btn--small" disabled={busy} onClick={() => void copyHooksJSON()}>{t("settings.hooksJsonCopy")}</button>
                <button className="btn btn--small" disabled={busy} onClick={() => void pasteHooksJSON()}>{t("settings.hooksJsonPaste")}</button>
                <button className="btn btn--small" disabled={busy || !jsonText.trim()} onClick={() => formatHooksEditorJSON()}>{t("settings.hooksJsonApply")}</button>
              </div>
            </div>
            <textarea
              name="hooks-json"
              className="mem-textarea hooks-json-panel__textarea"
              value={jsonText}
              disabled={busy}
              spellCheck={false}
              onChange={(e) => {
                setJsonText(e.target.value);
                setJsonMessage(null);
                setJsonError(null);
              }}
            />
            {jsonError && <div className="hooks-json-panel__message hooks-json-panel__message--error"><ErrorMessage error={jsonError} /></div>}
            {jsonMessage && <div className="hooks-json-panel__message">{jsonMessage}</div>}
          </div>
        )}
        {!view && <div className="empty">{t("settings.loading")}</div>}
        <div className="settings-save-bar">
          <span role="status">{t(view && jsonText !== formatHooksJSON(view.hooks, view.events) ? "settings.models.unsaved" : "settings.models.saved")}</span>
          <button className="btn" disabled={busy || !view} onClick={() => { if (view) { setJsonText(formatHooksJSON(view.hooks, view.events)); setJsonError(null); setJsonMessage(null); } }}>{t("common.cancel")}</button>
          <button className="btn btn--primary" disabled={busy || !view || jsonText === formatHooksJSON(view.hooks, view.events)} onClick={() => void save()}>{t("common.save")}</button>
        </div>
      </SettingsSection>
    </>
  );
}

function normalizeHooksSettingsView(view: HooksSettingsView, scope: HookScope): HooksSettingsView {
  const events = asArray(view?.events).filter(Boolean);
  return {
    scope: view?.scope === "project" ? "project" : scope,
    path: view?.path ?? "",
    projectRoot: view?.projectRoot ?? "",
    trusted: !!view?.trusted,
    events,
    hooks: asArray(view?.hooks).map(normalizeHookConfig).filter((h) => h.event),
  };
}

function formatHooksJSON(hooks: HookConfigView[], eventOrder: string[]): string {
  const grouped: Record<string, Array<Record<string, string | number>>> = {};
  const events = new Set(eventOrder);
  for (const hook of hooks.map(normalizeHookConfig).filter((h) => h.event)) {
    events.add(hook.event);
    const entry: Record<string, string | number> = { command: hook.command };
    if (hook.match) entry.match = hook.match;
    if (hook.description) entry.description = hook.description;
    if ((hook.timeout ?? 0) > 0) entry.timeout = hook.timeout ?? 0;
    if (hook.cwd) entry.cwd = hook.cwd;
    (grouped[hook.event] ||= []).push(entry);
  }
  const ordered: typeof grouped = {};
  for (const event of [...eventOrder, ...Array.from(events).sort()]) {
    if (grouped[event]?.length && !ordered[event]) ordered[event] = grouped[event];
  }
  return JSON.stringify({ hooks: ordered }, null, 2);
}

function parseHooksJSON(raw: string, validEvents: string[], t: ReturnType<typeof useT>): HookConfigView[] {
  const trimmed = raw.trim();
  if (!trimmed) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch (e) {
    throw new Error(String((e as Error)?.message ?? e));
  }
  if (Array.isArray(parsed)) {
    return parsed.map((item) => normalizeHookConfig(parseHookArrayItem(item, validEvents, t))).filter((h) => h.event);
  }
  if (!parsed || typeof parsed !== "object") {
    throw new Error(t("settings.hooksJsonExpectedObjectArray"));
  }
  const obj = parsed as Record<string, unknown>;
  const hooksValue = obj.hooks && typeof obj.hooks === "object" && !Array.isArray(obj.hooks) ? obj.hooks : obj;
  return flattenHooksMap(hooksValue as Record<string, unknown>, validEvents, t);
}

function parseHookArrayItem(item: unknown, validEvents: string[], t: ReturnType<typeof useT>): HookConfigView {
  if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error(t("settings.hooksJsonItemObject"));
  const obj = item as Record<string, unknown>;
  const event = stringField(obj, "event") || "PreToolUse";
  if (validEvents.length > 0 && !validEvents.includes(event)) throw new Error(t("settings.hooksJsonUnknownEvent", { event }));
  return {
    event,
    match: stringField(obj, "match"),
    command: stringField(obj, "command"),
    description: stringField(obj, "description"),
    timeout: numberField(obj, "timeout"),
    cwd: stringField(obj, "cwd"),
  };
}

function flattenHooksMap(hooks: Record<string, unknown>, validEvents: string[], t: ReturnType<typeof useT>): HookConfigView[] {
  const valid = new Set(validEvents);
  const out: HookConfigView[] = [];
  for (const [event, value] of Object.entries(hooks)) {
    if (valid.size > 0 && !valid.has(event)) throw new Error(t("settings.hooksJsonUnknownEvent", { event }));
    const items = Array.isArray(value) ? value : [value];
    for (const item of items) {
      if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error(t("settings.hooksJsonEventItemObject", { event }));
      const obj = item as Record<string, unknown>;
      out.push(normalizeHookConfig({
        event,
        match: stringField(obj, "match"),
        command: stringField(obj, "command"),
        description: stringField(obj, "description"),
        timeout: numberField(obj, "timeout"),
        cwd: stringField(obj, "cwd"),
      }));
    }
  }
  return out.filter((h) => h.event);
}

function stringField(obj: Record<string, unknown>, key: string): string {
  const value = obj[key];
  return typeof value === "string" ? value : "";
}

function numberField(obj: Record<string, unknown>, key: string): number {
  const value = obj[key];
  return typeof value === "number" && Number.isFinite(value) ? Math.floor(value) : 0;
}

function normalizeHookConfig(h: HookConfigView): HookConfigView {
  return {
    event: h.event || "PreToolUse",
    match: h.match ?? "",
    command: h.command ?? "",
    description: h.description ?? "",
    timeout: h.timeout && h.timeout > 0 ? Math.floor(h.timeout) : 0,
    cwd: h.cwd ?? "",
  };
}

function SandboxSection({ s, busy, apply, windows }: SectionProps & { windows: boolean }) {
  const t = useT();
  const sb = s.sandbox;
  // A draft belongs to the snapshot it was edited against. A fresh authoritative
  // root wins immediately; a failed save of the same snapshot retains the draft.
  const [rootDraft, setRootDraft] = useState<{ base: string; value: string } | null>(null);
  const root = rootDraft?.base === sb.workspaceRoot ? rootDraft.value : sb.workspaceRoot;
  const rootSaving = useRef(false);
  const [saveResult, setSaveResult] = useState<boolean | null>(null);
  const effectiveWriteRoots = asArray(sb.effectiveWriteRoots).filter((path) => String(path).trim());
  const set = async (next: Partial<typeof sb>) => {
    const result = await apply(() => app.SetSandbox(next.bash ?? sb.bash, next.network ?? sb.network, next.workspaceRoot ?? sb.workspaceRoot, next.allowWrite ?? sb.allowWrite, next.shell ?? sb.shell));
    setSaveResult(result);
    return result;
  };
  const saveRoot = async () => {
    if (busy || rootSaving.current || root === sb.workspaceRoot) return;
    rootSaving.current = true;
    try {
      if (await set({ workspaceRoot: root })) {
        setRootDraft(current => current?.base === sb.workspaceRoot && current.value === root ? null : current);
      }
    }
    finally { rootSaving.current = false; }
  };
  const reloadSession = async () => {
    if (await apply(() => app.ReloadSettings())) { setRootDraft(null); setSaveResult(null); }
  };

  return (
    <SettingsSection
      className="sandbox-settings"
      title={t("settings.sandboxTitle")}
      description={t("settings.sandboxSaveScope")}
      actions={
        <Tooltip label={t("settings.reloadSessionConfigHint")}>
          <button type="button" className="btn btn--small" disabled={busy} onClick={() => void reloadSession()}>
            <RefreshCw size={14} aria-hidden="true" />
            <span>{t("settings.reloadSessionConfig")}</span>
          </button>
        </Tooltip>
      }
    >
      <ShellInterpreterFields sb={sb} windows={windows} busy={busy} setShell={(prefer) => void apply(() => app.SetShellPreference(prefer))} reloadSession={() => void reloadSession()} />
      <SettingsField className="sandbox-network" label={t(windows ? "settings.shellNetworkAccess" : "settings.allowNetwork")}>
        {windows ? <div className="sandbox-network__status"><span>{t("settings.shellNetworkUnrestricted")}</span><p className="sandbox-group__hint">{t("settings.shellNetworkUnrestrictedHint")}</p></div> :
        <label className="set-check set-check--inline">
          <input type="checkbox" checked={sb.network} disabled={busy} onChange={(e) => void set({ network: e.target.checked })} />
          {t("settings.allowNetwork")}
        </label>}
      </SettingsField>
      <div className="sandbox-group sandbox-group--files">
        <div className="sandbox-group__heading"><h3>{t(windows ? "settings.fileToolWriteScope" : "settings.writeScope")}</h3>
          <span className="sandbox-save-status" role="status">{busy ? t("settings.sandboxSaving") : saveResult === false ? t("settings.sandboxSaveFailed") : root !== sb.workspaceRoot ? t("settings.models.unsaved") : saveResult === true ? <><CheckCircle2 size={14} aria-hidden="true" />{t("settings.sandboxSaved")}</> : null}</span>
        </div>
        <p className="sandbox-group__hint">{t(windows ? "settings.fileToolWriteScopeHint" : "settings.writeScopeHint")}</p>
      <SettingsField label={t("settings.workspaceRoot")}>
        <div className="sandbox-root-control">
        <input
          className="mem-input set-grow"
          placeholder={t("settings.workspaceDefault")}
          aria-label={t("settings.workspaceRoot")}
          value={root}
          disabled={busy}
          onChange={(e) => { setRootDraft({ base: sb.workspaceRoot, value: e.target.value }); setSaveResult(null); }}
          onBlur={() => void saveRoot()}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); void saveRoot(); } }}
        />
          <div className="sandbox-write-roots">
            <span>{t("settings.effectiveWriteRoots")}</span>
            {effectiveWriteRoots.length === 0 && <span className="mem-empty">{t("settings.noEffectiveWriteRoots")}</span>}
            {effectiveWriteRoots.map((path, index) => (
              <span className="set-rule set-rule--path" key={`${path}-${index}`}>
                <span>{path}</span><CopyButton text={path} showInlineLabel={false} label={t("settings.copyDirectory")} />
              </span>
            ))}
          </div>
        </div>
      </SettingsField>
      <RuleList
        list="allow_write"
        rules={sb.allowWrite}
        busy={busy}
        onAdd={(d) => set({ allowWrite: [...sb.allowWrite, d] })}
        onRemove={(d) => set({ allowWrite: sb.allowWrite.filter((x) => x !== d) })}
      />
      </div>
    </SettingsSection>
  );
}

const MB = 1024 * 1024;
const mb = (n: number) => (n / MB).toFixed(1);

// AboutSection always exposes build, privacy, configuration, changelog, and
// feedback information. Updater controls are an exact stable-build capability.
function AboutSection({
  configPath,
  shadowedByPath,
  checkUpdates,
  updaterEnabled,
  telemetry,
  metrics,
  settingsBusy,
  applySettings,
}: {
  configPath: string;
  shadowedByPath?: string;
  checkUpdates: boolean;
  updaterEnabled: boolean;
  telemetry: boolean;
  metrics: boolean;
  settingsBusy: boolean;
  applySettings: (fn: () => Promise<void>) => Promise<boolean>;
}) {
  const t = useT();
  const { status, check, apply: applyUpdate, openDownload, abandonPending } = useUpdater();
  const [version, setVersion] = useState("");
  useEffect(() => {
    app.Version().then(setVersion).catch(() => {});
  }, []);

  const updaterBusy =
    status.kind === "checking" ||
    status.kind === "downloading" ||
    status.kind === "verifying" ||
    status.kind === "authorizing" ||
    status.kind === "installing" ||
    status.kind === "relaunching";
  const updateStatus =
    status.kind === "checking" ? t("updater.checking") :
    status.kind === "upToDate" ? t("updater.upToDate") :
    status.kind === "available" ? t("updater.available", { v: status.info.latest }) :
    status.kind === "downloading" ? t("updater.downloading", {
      done: mb(status.received),
      total: mb(status.total),
      pct: status.total > 0 ? Math.round((status.received / status.total) * 100) : 0,
    }) :
    status.kind === "verifying" ? t("updater.verifying") :
    status.kind === "authorizing" ? t("updater.authorizing") :
    status.kind === "installing" ? (
      status.info?.requiresElevation || status.info?.installMode === "deb"
        ? t("updater.installingPackage")
        : t("updater.installing")
    ) :
    status.kind === "relaunching" || status.kind === "done" ? t("updater.done") :
    status.kind === "error" ? "" :
    "";
  const updateStatusTone =
    status.kind === "error" ? "error" :
    status.kind === "available" ? "available" :
    status.kind === "upToDate" || status.kind === "done" || status.kind === "relaunching" ? "success" :
    status.kind === "checking" || updaterBusy ? "busy" :
    "neutral";
  const updateErrorTitle = status.kind === "error"
    ? status.disposition === "recovery"
      ? t("updater.recoveryBlocked")
      : status.disposition === "manual"
        ? t("updater.manualUpdateRequired")
        : t("updater.failed", { msg: status.message })
    : "";
  const updateErrorHint = status.kind === "error"
    ? status.disposition === "recovery"
      ? t("updater.recoveryHint")
      : status.disposition === "manual"
        ? t("updater.manualFallbackHint")
        : ""
    : "";
  const downloadIsPrimary = status.kind === "error" && status.disposition !== "retryable";

  return (
    <SettingsSection>
      <SettingsField
        className="settings-field--wide-copy updates-control"
        label={
          <div className="updates-control__summary">
            <div className="updates-control__version">
              {t("updater.currentVersion", { v: version || "…" })}
            </div>
            {updaterEnabled && (
              <div className={`updates-control__status updates-control__status--${updateStatusTone}`} role="status" aria-live="polite">
                {updateStatus && (
                  <>
                    {updateStatusTone === "success" && <CheckCircle2 size={14} aria-hidden="true" />}
                    {updateStatusTone === "busy" && <Loader2 className="updates-control__spinner" size={14} aria-hidden="true" />}
                    <span>{updateStatus}</span>
                  </>
                )}
              </div>
            )}
          </div>
        }
      >
        {updaterEnabled && <div className="updates-control__controls">
          <Tooltip label={t("updater.checkButton")}>
            <button
              className="chip chip--icon"
              type="button"
              disabled={settingsBusy || updaterBusy}
              aria-label={t("updater.checkButton")}
              onClick={() => void check()}
            >
              <RefreshCw className={status.kind === "checking" ? "updates-control__spinner" : undefined} size={14} aria-hidden="true" />
            </button>
          </Tooltip>
        </div>}
      </SettingsField>
      <SettingsField
        className="settings-field--wide-copy"
        label={t("updater.buildIdentity")}
        hint={t("updater.buildIdentityHint")}
      >
        <span className="mem-hint">{typeof __BUILD_CHANNEL__ === "string" ? __BUILD_CHANNEL__ : "development"}</span>
      </SettingsField>
      {updaterEnabled && <div
        className="updates-control__hint"
        style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "4px 8px" }}
      >
        <span>{t("updater.officialReleaseHint")}</span>
        <button
          className="btn btn--small"
          type="button"
          onClick={openDownload}
          style={{
            height: "auto",
            minHeight: 0,
            padding: 0,
            borderColor: "transparent",
            background: "transparent",
            color: "var(--fg-dim)",
            textDecoration: "underline",
            textUnderlineOffset: 2,
          }}
        >
          {t("updater.officialDownload")}
          <ExternalLink size={13} aria-hidden="true" />
        </button>
      </div>}
      {updaterEnabled && status.kind === "available" && (
        <div className="updates-control__action">
          <div className="updates-control__action-copy">
            {!status.info.canSelfUpdate && <div>{status.info.manualReason || t("updater.macHint")}</div>}
          </div>
          <button
            className="btn btn--primary btn--small"
            disabled={settingsBusy || updaterBusy}
            onClick={() => applyUpdate(status.info)}
          >
            {status.info.canSelfUpdate ? t("updater.updateAndRestart") : t("updater.goToDownload")}
          </button>
        </div>
      )}
      {updaterEnabled && status.kind === "error" && (
        <div
          className="banner banner--update banner--error"
          role="alert"
          style={{ alignItems: "flex-start", flexWrap: "wrap", marginBottom: 12 }}
        >
          <div style={{ flex: "1 1 360px", minWidth: 0 }}>
            <div>{updateErrorTitle}</div>
            {updateErrorHint && <div className="banner__hint">{updateErrorHint}</div>}
            {status.disposition !== "retryable" && (
              <div className="banner__hint" style={{ overflowWrap: "anywhere" }}>
                {t("updater.errorDetails", { msg: status.message })}
              </div>
            )}
          </div>
          <span className="banner__spacer" />
          {status.disposition === "recovery" && (
            <button
              className="btn btn--small"
              type="button"
              disabled={settingsBusy || updaterBusy}
              onClick={() => void abandonPending()}
            >
              {t("updater.discardPrevious")}
            </button>
          )}
          {downloadIsPrimary && (
            <button className="btn btn--primary btn--small" type="button" onClick={openDownload}>
              {t("updater.officialDownload")}
              <ExternalLink size={14} aria-hidden="true" />
            </button>
          )}
          <button
            className={`btn btn--small${downloadIsPrimary ? "" : " btn--primary"}`}
            type="button"
            disabled={settingsBusy || updaterBusy}
            onClick={() => status.info ? applyUpdate(status.info) : void check()}
          >
            {t("updater.retry")}
          </button>
        </div>
      )}
      <SettingsField
        className="settings-field--wide-copy"
        label={t("changelog.title")}
        hint={t("changelog.subtitle")}
      >
        <button className="btn btn--small" onClick={() => void openExternal("https://github.com/SivanCola/deepzora/releases/tag/")}>
          {t("changelog.openWeb")}
          <ExternalLink size={14} aria-hidden="true" />
        </button>
      </SettingsField>
      <SettingsField
        className="settings-field--wide-copy settings-field--stacked"
        label={t("feedback.title")}
        hint={t("feedback.subtitle")}
      >
        <div className="settings-inline-controls">
          <button
            className="btn btn--small"
            onClick={() => void openExternal("https://github.com/SivanCola/deepzora/issues/new/choose")}
          >
            {t("feedback.submitIssue")}
            <ExternalLink size={14} aria-hidden="true" />
          </button>
          <button
            className="btn btn--small"
            onClick={() => void openExternal("https://github.com/SivanCola/deepzora/issues")}
          >
            {t("feedback.viewIssues")}
            <ExternalLink size={14} aria-hidden="true" />
          </button>
        </div>
        <FeedbackReportForm />
      </SettingsField>
      {updaterEnabled && <details
        className="provider-editor-advanced"
        style={{
          marginTop: 0,
          borderRight: 0,
          borderBottom: 0,
          borderLeft: 0,
          borderRadius: 0,
          background: "transparent",
        }}
      >
        <summary style={{ padding: "0 2px" }}>
          <span className="provider-editor-advanced__title">
            <ChevronDown className="provider-editor-advanced__icon" size={16} aria-hidden="true" />
            {t("updater.updatePreferences")}
          </span>
        </summary>
        <div className="provider-editor-advanced__body">
          <SettingsField
            className="settings-field--wide-copy"
            label={t("updater.autoCheckLabel")}
            hint={t("updater.autoCheckHint")}
          >
            <ToggleSegment
              value={checkUpdates}
              disabled={settingsBusy}
              onChange={(enabled) => void applySettings(() => app.SetDesktopCheckUpdates(enabled))}
            />
          </SettingsField>
        </div>
      </details>}
      <details
        className="provider-editor-advanced"
        style={{
          marginTop: 0,
          borderRight: 0,
          borderBottom: 0,
          borderLeft: 0,
          borderRadius: 0,
          background: "transparent",
        }}
      >
        <summary style={{ padding: "0 2px" }}>
          <span className="provider-editor-advanced__title">
            <ChevronDown className="provider-editor-advanced__icon" size={16} aria-hidden="true" />
            {t("updater.privacyAndConfig")}
          </span>
        </summary>
        <div className="provider-editor-advanced__body">
          <SettingsField className="settings-field--wide-copy" label={t("settings.telemetryLabel")} hint={t("settings.telemetryHint")}>
            <ToggleSegment value={telemetry} disabled={settingsBusy} onChange={(enabled) => void applySettings(() => app.SetDesktopTelemetry(enabled))} />
          </SettingsField>
          <SettingsField className="settings-field--wide-copy" label={t("settings.metricsLabel")} hint={t("settings.metricsHint")}>
            <ToggleSegment value={metrics} disabled={settingsBusy} onChange={(enabled) => void applySettings(() => app.SetDesktopMetrics(enabled))} />
          </SettingsField>
          {configPath && (
            <Tooltip label={configPath} fill block className="mem-hint settings-config-path">
              {t("settings.config", { path: configPath })}
            </Tooltip>
          )}
          {shadowedByPath && (
            <Tooltip label={shadowedByPath} fill block className="mem-hint settings-config-path settings-config-path--shadowed">
              {t("settings.configShadowed", { path: shadowedByPath })}
            </Tooltip>
          )}
        </div>
      </details>
    </SettingsSection>
  );
}
