import { desktopHost } from "./desktopHost";

export function browserMockScenarioParam(): string {
  if (typeof window === "undefined" || desktopHost().kind !== "none") return "";
  return new URLSearchParams(window.location.search).get("mock")?.trim().toLowerCase() ?? "";
}
