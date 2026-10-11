import type { SettingsView } from "./types";

type MockSessionSettings = Pick<SettingsView, "sessionExperience">;

export function applyMockSessionExperience(settings: MockSessionSettings, value: unknown): void {
  if (value !== "standard" && value !== "deep") throw new Error("invalid session experience");
  settings.sessionExperience = value;
}
