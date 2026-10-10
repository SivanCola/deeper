export const IPC = {
  contract: "deeper:contract",
  invoke: "deeper:invoke",
  event: "deeper:event",
  serviceState: "deeper:service-state",
  serviceStateGet: "deeper:service-state:get",
  processDiagnostics: "deeper:native:process-diagnostics",
  captureRendererProfile: "deeper:native:capture-renderer-profile",
  cancelRendererProfile: "deeper:native:cancel-renderer-profile",
  exportHeapSnapshot: "deeper:native:export-heap-snapshot",
  rendererDiagnostic: "deeper:native:renderer-diagnostic",
  openExternal: "deeper:native:open-external",
  clipboardWrite: "deeper:native:clipboard-write",
  clipboardRead: "deeper:native:clipboard-read",
  windowMinimise: "deeper:native:window-minimise",
  windowToggleMaximise: "deeper:native:window-toggle-maximise",
  windowIsMaximised: "deeper:native:window-is-maximised",
  windowClose: "deeper:native:window-close",
  windowGetBounds: "deeper:native:window-get-bounds",
  windowSetTheme: "deeper:native:window-set-theme",
  windowSetBackground: "deeper:native:window-set-background",
  appZoomGet: "deeper:native:app-zoom-get",
  appZoomSet: "deeper:native:app-zoom-set",
  appZoomReset: "deeper:native:app-zoom-reset",
  graphicsGet: "deeper:native:graphics-get",
  graphicsSet: "deeper:native:graphics-set",
  browserControlGet: "deeper:native:browser-control-get",
  browserControlSetEnabled: "deeper:native:browser-control-set-enabled",
  browserControlSetIgnoreCertificateErrors: "deeper:native:browser-control-set-ignore-certificate-errors",
  browserControlClearCache: "deeper:native:browser-control-clear-cache",
  browserControlClearAll: "deeper:native:browser-control-clear-all",
  browserControlImportChrome: "deeper:native:browser-control-import-chrome",
  browserList: "deeper:browser:list",
  browserOpen: "deeper:browser:open",
  browserClose: "deeper:browser:close",
  browserActivate: "deeper:browser:activate",
  browserNavigate: "deeper:browser:navigate",
  browserSetZoom: "deeper:browser:set-zoom",
  browserSetViewport: "deeper:browser:set-viewport",
  browserPickElement: "deeper:browser:pick-element",
  browserRecord: "deeper:browser:record",
  browserDiagnostics: "deeper:browser:diagnostics",
  browserScreenshot: "deeper:browser:screenshot",
  browserRestorePreview: "deeper:browser:restore-preview",
  browserToggleDevTools: "deeper:browser:toggle-devtools",
  browserResume: "deeper:browser:resume",
  browserUserTakeover: "deeper:browser:user-takeover",
  browserSetLayout: "deeper:browser:set-layout",
  browserSetOverlay: "deeper:browser:set-overlay",
  browserTabs: "deeper:browser:tabs",
  browserDownload: "deeper:browser:download",
  browserTakeover: "deeper:browser:takeover",
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
