// theme.ts manages the appearance override. The stylesheet follows the OS via
// prefers-color-scheme unless data-theme forces "dark" or "light". A separate
// data-theme-style attribute selects a visual direction (graphite/aurora/slate/
// carbon/nocturne/amber) — orthogonal to theme, so every direction supports both
// light & dark.
//
// When running inside the desktop shell, applyTheme also syncs the native window
// theme (title bar, traffic lights, etc.) so the OS chrome matches the webview.

import { baseCodeReadabilityStylesheet } from "./codeReadability";
import { desktopHost } from "./desktopHost";

export type Theme = "auto" | "light" | "dark";
export type ResolvedTheme = Exclude<Theme, "auto">;

export const THEME_STYLES = [
  "graphite",
  "aurora",
  "slate",
  "carbon",
  "nocturne",
  "amber",
] as const;

export type ThemeStyle = (typeof THEME_STYLES)[number];

const DEFAULT_THEME_STYLE: ThemeStyle = "graphite";
const DEFAULT_THEME: Theme = "auto";

const AUTO_THEME_MEDIA_QUERY = "(prefers-color-scheme: light)";
const BASE_CODE_READABILITY_STYLE_ID = "deepzora-base-code-readability";
let currentTheme: Theme = DEFAULT_THEME;
let currentThemeStyle: ThemeStyle = DEFAULT_THEME_STYLE;
let autoThemeMediaQuery: MediaQueryList | null = null;

export function normalizeThemePreference(value: unknown): Theme {
  if (typeof value !== "string") return DEFAULT_THEME;
  switch (value) {
    case "auto":
      return "auto";
    case "light":
      return "light";
    case "dark":
      return "dark";
    default:
      return DEFAULT_THEME;
  }
}

export function isThemeStyle(value: unknown): value is ThemeStyle {
  return typeof value === "string" && (THEME_STYLES as readonly string[]).includes(value);
}

export function getTheme(): Theme {
  return currentTheme;
}

export function getResolvedTheme(theme: Theme = getTheme()): ResolvedTheme {
  if (theme === "light" || theme === "dark") return theme;
  if (typeof window !== "undefined" && window.matchMedia?.(AUTO_THEME_MEDIA_QUERY).matches) return "light";
  return "dark";
}

export function getThemeStyle(): ThemeStyle {
  return currentThemeStyle;
}

export function normalizeThemeStyle(style: string | undefined): ThemeStyle {
  if (typeof style !== "string") return DEFAULT_THEME_STYLE;
  if (isThemeStyle(style)) return style;
  return DEFAULT_THEME_STYLE;
}

export function applyTheme(theme: Theme, style: ThemeStyle = getThemeStyle(), options: { persist?: boolean } = {}): void {
  if (typeof document === "undefined") return;
  ensureBaseCodeReadabilityStyle();
  const root = document.documentElement;
  root.removeAttribute("data-theme-mode");
  root.removeAttribute("data-theme-scheme");
  if (theme === "auto") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);

  const nextStyle: ThemeStyle = isThemeStyle(style) ? style : DEFAULT_THEME_STYLE;
  currentTheme = theme;
  currentThemeStyle = nextStyle;
  root.setAttribute("data-theme-style", nextStyle);

  // Sync the native window theme (title bar, traffic lights) to match.
  const host = desktopHost();
  if (host.kind !== "none") {
    syncAutoThemeBackgroundListener(theme);
    host.native.setWindowTheme(theme === "auto" ? "system" : theme);
    syncNativeWindowBackground(theme);
  }

  void options;
}

function ensureBaseCodeReadabilityStyle(): void {
  if (document.getElementById(BASE_CODE_READABILITY_STYLE_ID)) return;
  const style = document.createElement("style");
  style.id = BASE_CODE_READABILITY_STYLE_ID;
  style.textContent = baseCodeReadabilityStylesheet(THEME_STYLES);
  document.head.appendChild(style);
}

function syncAutoThemeBackgroundListener(theme: Theme): void {
  if (theme !== "auto") {
    clearAutoThemeBackgroundListener();
    return;
  }
  if (autoThemeMediaQuery || typeof window === "undefined" || !window.matchMedia) return;
  autoThemeMediaQuery = window.matchMedia(AUTO_THEME_MEDIA_QUERY);
  if (typeof autoThemeMediaQuery.addEventListener === "function") {
    autoThemeMediaQuery.addEventListener("change", syncAutoThemeBackground);
  } else {
    autoThemeMediaQuery.addListener(syncAutoThemeBackground);
  }
}

function clearAutoThemeBackgroundListener(): void {
  if (!autoThemeMediaQuery) return;
  if (typeof autoThemeMediaQuery.removeEventListener === "function") {
    autoThemeMediaQuery.removeEventListener("change", syncAutoThemeBackground);
  } else {
    autoThemeMediaQuery.removeListener(syncAutoThemeBackground);
  }
  autoThemeMediaQuery = null;
}

function syncAutoThemeBackground(): void {
  if (currentTheme === "auto" && desktopHost().kind !== "none") syncNativeWindowBackground("auto");
}

// initTheme runs before React mounts. It applies the saved theme to the DOM and
// sets the native window background colour to match the resolved theme, avoiding
// a white (or wrong-colour) flash while the webview paints its first frame.
export function initTheme(): void {
  const theme = getTheme();
  applyTheme(theme, getThemeStyle(), { persist: false });
}

function syncNativeWindowBackground(theme: Theme): void {
  const host = desktopHost();
  if (host.kind === "none") return;
  if (getResolvedTheme(theme) === "light") {
    // Light shell: matches graphite --bg (#f4f3ef).
    host.native.setWindowBackground(244, 243, 239, 255);
  } else {
    // Dark shell: matches :root --bg (#090a0c).
    host.native.setWindowBackground(9, 10, 12, 255);
  }
}
