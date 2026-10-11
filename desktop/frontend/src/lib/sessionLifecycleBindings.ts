import type { AppBindings } from "./bridge";
import type { SessionRef } from "./sessionRef";

export interface CanonicalProjectNodeFields {
  session?: SessionRef;
  canArchive?: boolean;
}

export interface SessionLifecycleBindings {
	ApplySessionLifecycle(request: import("../generated/desktopContract.generated").SessionLifecycleRequest): Promise<import("../generated/desktopContract.generated").SessionLifecycleResult>;
	ListTrashEntries(query: string, cursor: string, limit: number): Promise<import("../generated/desktopContract.generated").TrashEntryPage>;
  PurgeCanonicalSession(ref: SessionRef): Promise<void>;
}

export type MockSessionLifecycleBindings = SessionLifecycleBindings & Pick<AppBindings, "InspectTopicRemoval" | "RemoveTopic">;

export function makeMockSessionLifecycleBindings(...args: Parameters<typeof import("./sessionLifecycleMock").makeMockSessionLifecycleBindings>): MockSessionLifecycleBindings {
  const bindings = import("./sessionLifecycleMock").then(module => module.makeMockSessionLifecycleBindings(...args));
  return Object.fromEntries(["PurgeCanonicalSession","ListTrashEntries","ApplySessionLifecycle","InspectTopicRemoval","RemoveTopic"].map(name => [name, async function(this: AppBindings, ...values: unknown[]) {
    const owner = await bindings;
    return (owner[name as keyof typeof owner] as (...params: unknown[]) => unknown).apply(this, values);
  }])) as unknown as MockSessionLifecycleBindings;
}
