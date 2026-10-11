import { useMemo, useSyncExternalStore } from "react";

export type SessionExperience = "standard" | "deep";

export type WorkProcessPresentation = {
  experience: SessionExperience;
  showWhileRunning: true;
  keepExpandedAfterCompletion: boolean;
};

const SESSION_EXPERIENCE_EVENT = "deepzora:session-experience";

let current: SessionExperience = "standard";
let hydrated = false;
const listeners = new Set<() => void>();
const beforeChangeListeners = new Set<(previous: SessionExperience, next: SessionExperience) => void>();

function normalize(value: unknown): SessionExperience {
  return value === "deep" ? "deep" : "standard";
}

function emit(): void {
  for (const listener of listeners) listener();
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(SESSION_EXPERIENCE_EVENT, { detail: current }));
  }
}

export function getSessionExperience(): SessionExperience {
  // The backend snapshot is authoritative. Before it arrives, use the safe
  // default instead of reviving a stale value written by an older frontend.
  if (!hydrated) return "standard";
  return current;
}

export function hydrateSessionExperience(value: unknown): void {
  const next = normalize(value);
  hydrated = true;
  current = next;
  emit();
}

export function applySessionExperience(value: SessionExperience): void {
  const next = normalize(value);
  hydrated = true;
  if (next === current) {
    return;
  }
  for (const listener of beforeChangeListeners) listener(current, next);
  current = next;
  emit();
}

/** Synchronously capture layout state before a mode change updates row
 * geometry. Layout owners use this to preserve the reader's logical anchor. */
export function onSessionExperienceWillChange(
  listener: (previous: SessionExperience, next: SessionExperience) => void,
): () => void {
  beforeChangeListeners.add(listener);
  return () => beforeChangeListeners.delete(listener);
}

export function resolveWorkProcessPresentation(value: SessionExperience): WorkProcessPresentation {
  return {
    experience: value,
    showWhileRunning: true,
    keepExpandedAfterCompletion: value === "deep",
  };
}

export function useSessionExperience(): SessionExperience {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSessionExperience,
    () => "standard",
  );
}

export function useWorkProcessPresentation(): WorkProcessPresentation {
  const experience = useSessionExperience();
  return useMemo(() => resolveWorkProcessPresentation(experience), [experience]);
}
