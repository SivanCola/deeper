import { rename, readFile, writeFile, mkdir } from "node:fs/promises";
import { dirname, join } from "node:path";

export const MIN_APP_ZOOM = 0.5;
export const MAX_APP_ZOOM = 2;
export const APP_ZOOM_STEP = 0.05;
export type AppZoomSource = "default" | "user";
export interface AppZoomState { version: 1; appZoomFactor: number; source: AppZoomSource; }

const DEFAULT: AppZoomState = { version: 1, appZoomFactor: 1, source: "default" };

export function normalizeAppZoom(value: number): number {
  if (!Number.isFinite(value)) return 1;
  const clamped = Math.min(MAX_APP_ZOOM, Math.max(MIN_APP_ZOOM, value));
  return Number((MIN_APP_ZOOM + Math.round((clamped - MIN_APP_ZOOM) / APP_ZOOM_STEP) * APP_ZOOM_STEP).toFixed(2));
}

export class AppZoomStore {
  private state: AppZoomState = DEFAULT;
  constructor(private readonly file: string) {}
  async load(): Promise<AppZoomState> {
    try {
      const parsed = JSON.parse(await readFile(this.file, "utf8")) as Partial<AppZoomState>;
      if (parsed.version === 1 && typeof parsed.appZoomFactor === "number" && Number.isFinite(parsed.appZoomFactor)
        && (parsed.source === "default" || parsed.source === "user")) {
        this.state = { version: 1, appZoomFactor: normalizeAppZoom(parsed.appZoomFactor), source: parsed.source };
        return this.state;
      }
    } catch { /* first launch or corrupt file */ }
    this.state = DEFAULT;
    await this.persist(this.state);
    return this.state;
  }
  async set(factor: number, source: AppZoomSource = "user"): Promise<AppZoomState> {
    if (!Number.isFinite(factor)) throw new Error("app zoom factor must be finite");
    this.state = { version: 1, appZoomFactor: normalizeAppZoom(factor), source };
    await this.persist(this.state);
    return this.state;
  }
  async reset(): Promise<AppZoomState> { return this.set(1, "user"); }
  get current(): AppZoomState { return this.state; }
  private async persist(state: AppZoomState): Promise<void> {
    await mkdir(dirname(this.file), { recursive: true });
    const temp = join(dirname(this.file), `.${state.version}.zoom.tmp`);
    await writeFile(temp, `${JSON.stringify(state)}\n`, "utf8");
    await rename(temp, this.file);
  }
}
