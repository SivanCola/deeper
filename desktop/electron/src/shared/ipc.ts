export const IPC = {
  contract: "deepzora:contract",
  invoke: "deepzora:invoke",
  event: "deepzora:event",
  serviceState: "deepzora:service-state",
  serviceStateGet: "deepzora:service-state:get",
  processDiagnostics: "deepzora:native:process-diagnostics",
  captureRendererProfile: "deepzora:native:capture-renderer-profile",
  cancelRendererProfile: "deepzora:native:cancel-renderer-profile",
  exportHeapSnapshot: "deepzora:native:export-heap-snapshot",
  rendererDiagnostic: "deepzora:native:renderer-diagnostic",
  openExternal: "deepzora:native:open-external",
  clipboardWrite: "deepzora:native:clipboard-write",
  clipboardRead: "deepzora:native:clipboard-read",
  windowMinimise: "deepzora:native:window-minimise",
  windowToggleMaximise: "deepzora:native:window-toggle-maximise",
  windowIsMaximised: "deepzora:native:window-is-maximised",
  windowClose: "deepzora:native:window-close",
  windowGetBounds: "deepzora:native:window-get-bounds",
  windowSetTheme: "deepzora:native:window-set-theme",
  windowSetBackground: "deepzora:native:window-set-background",
  appZoomGet: "deepzora:native:app-zoom-get",
  appZoomSet: "deepzora:native:app-zoom-set",
  appZoomReset: "deepzora:native:app-zoom-reset",
  graphicsGet: "deepzora:native:graphics-get",
  graphicsSet: "deepzora:native:graphics-set",
  browserControlGet: "deepzora:native:browser-control-get",
  browserControlSetEnabled: "deepzora:native:browser-control-set-enabled",
  browserControlSetIgnoreCertificateErrors: "deepzora:native:browser-control-set-ignore-certificate-errors",
  browserControlClearCache: "deepzora:native:browser-control-clear-cache",
  browserControlClearAll: "deepzora:native:browser-control-clear-all",
  browserControlImportChrome: "deepzora:native:browser-control-import-chrome",
  browserList: "deepzora:browser:list",
  browserOpen: "deepzora:browser:open",
  browserClose: "deepzora:browser:close",
  browserActivate: "deepzora:browser:activate",
  browserNavigate: "deepzora:browser:navigate",
  browserSetZoom: "deepzora:browser:set-zoom",
  browserSetViewport: "deepzora:browser:set-viewport",
  browserPickElement: "deepzora:browser:pick-element",
  browserRecord: "deepzora:browser:record",
  browserDiagnostics: "deepzora:browser:diagnostics",
  browserScreenshot: "deepzora:browser:screenshot",
  browserRestorePreview: "deepzora:browser:restore-preview",
  browserToggleDevTools: "deepzora:browser:toggle-devtools",
  browserResume: "deepzora:browser:resume",
  browserUserTakeover: "deepzora:browser:user-takeover",
  browserSetLayout: "deepzora:browser:set-layout",
  browserSetOverlay: "deepzora:browser:set-overlay",
  browserTabs: "deepzora:browser:tabs",
  browserDownload: "deepzora:browser:download",
  browserTakeover: "deepzora:browser:takeover",
} as const;

export type ServicePhase = "starting" | "ready" | "restarting" | "stopping" | "failed" | "exited";

export interface ServiceState {
  phase: ServicePhase;
  generation: string;
  error?: string;
}

export interface ContractInfo {
  protocolVersion: number;
  digest: string;
  commands: readonly string[];
}

export interface EventFrame {
  seq: number;
  generation: string;
  name: string;
  args: unknown[];
}

export type IpcResult = { ok: true; value: unknown } | { ok: false; message: string; code?:number; data?:unknown };

export interface WindowBounds {
  x: number;
  y: number;
  width: number;
  height: number;
  maximised: boolean;
}

export type WindowTheme = "system" | "light" | "dark";

export type HostOS = "darwin" | "windows" | "linux";

export type BrowserControlWarning = "invalid-config" | "unreadable-config" | "unsupported-version";

export interface BrowserControlState {
  controlEnabled: boolean;
  ignoreCertificateErrors: boolean;
  writable: boolean;
  warning: BrowserControlWarning | null;
}

export type ChromeImportFailure =
  | "chrome-missing"
  | "profile-not-found"
  | "cookies-unreadable"
  | "safe-storage-denied"
  | "safe-storage-unavailable"
  | "unsupported-platform";

export type ChromeImportOutcome =
  | { ok: true; profile: string; cookies: number; skipped: number }
  | { ok: false; reason: ChromeImportFailure };

export function hostOS(platform: string): HostOS {
  if (platform === "darwin") return "darwin";
  if (platform === "win32") return "windows";
  return "linux";
}

export type BrowserTabMode = "agent" | "human";

export interface BrowserTabView {
  restorePreview?: boolean;
  sessionId?: string;
  id: string;
  taskId: string;
  url: string;
  title: string;
  loading: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  temporary: boolean;
  mode: BrowserTabMode;
  epoch: number;
  zoom: number;
  viewport?: { width: number; height: number; scale: "fit" | number } | null;
  operation?: { id: string; phase: string; message?: string };
  active: boolean;
  error: { code: number; description: string } | null;
}

export type BrowserDownloadState = "progressing" | "completed" | "cancelled" | "interrupted";

export interface BrowserDownloadView {
  id: string;
  tabId: string;
  url: string;
  filename: string;
  path: string;
  state: BrowserDownloadState;
  received: number;
  total: number;
}

export interface BrowserLayoutRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface BrowserOpenOptions {
  temporary?: boolean;
  taskId?: string;
}

export type BrowserNavigateAction = "back" | "forward" | "reload" | "stop";

export interface BrowserNavigateTarget {
  url?: string;
  action?: BrowserNavigateAction;
}

export type BrowserTakeoverKind = "mousedown" | "keydown" | "wheel" | "touchstart" | "pointerdown";
