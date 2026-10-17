import type { TranscriptSessionFollowerRuntime } from "./transcriptSessionFollowerRuntime";
import { desktopHost } from "./desktopHost";
import type { Action } from "./useController";

type FollowMetrics = TranscriptSessionFollower["metrics"];
type FollowerOwner = {
  key: string;
  follower: TranscriptSessionFollower;
  connection: "syncing" | "connected" | "disconnected";
  startup?: Promise<FollowMetrics>;
  retire?: () => void;
};

/** Startup and connection belong to the same owner as the subscription. */
export class TranscriptFollowerLifecycle {
  private readonly owners = new Map<string, FollowerOwner>();

  connected(tabId: string, key: string): boolean {
    const owner = this.owners.get(tabId);
    return owner?.key === key && owner.connection === "connected";
  }

  start(tabId: string, path: string, key: string, dispatch: (action: Action) => void, reuseConnected = false): Promise<FollowMetrics> {
    const existing = this.owners.get(tabId);
    if (existing?.key === key) {
      if (existing.startup) return existing.startup;
      if (reuseConnected && existing.connection === "connected") return Promise.resolve(existing.follower.metrics);
    }
    this.stop(tabId);
    const owner: FollowerOwner = {
      key, connection: "syncing",
      follower: new TranscriptSessionFollower(tabId, path, false, action => {
        if (this.owners.get(tabId) !== owner) return;
        if (action.type === "transcript_connection") owner.connection = action.status;
        dispatch(action);
      }),
    };
    this.owners.set(tabId, owner);
    dispatch({ type: "transcript_connection", status: "syncing" });
    const retired = new Promise<FollowMetrics>(resolve => { owner.retire = () => resolve(owner.follower.metrics); });
    const startup = Promise.race([owner.follower.start().then(() => owner.follower.metrics), retired]).catch(error => {
      // A rejected baseline has no follow loop. Retire only its own entry;
      // an older completion must not disconnect a replacement subscription.
      if (this.owners.get(tabId) === owner) {
        this.stop(tabId);
        dispatch({ type: "transcript_connection", status: "disconnected", error: String(error) });
      }
      throw error;
    }).finally(() => {
      if (owner.startup === startup) owner.startup = undefined;
      owner.retire = undefined;
    });
    owner.startup = startup;
    return startup;
  }

  async ready(tabId: string, path: string, key: string, dispatch: (action: Action) => void): Promise<FollowMetrics> {
    let metrics = await this.start(tabId, path, key, dispatch, true);
    // A rebuild can replace the observer without replacing the execution
    // runtime. Wait for that current owner's baseline before admitting a send.
    let owner = this.owners.get(tabId);
    while (owner?.key === key && owner.startup) {
      metrics = await owner.startup;
      owner = this.owners.get(tabId);
    }
    return metrics;
  }

  stop(tabId: string): void {
    const owner = this.owners.get(tabId);
    this.owners.delete(tabId);
    owner?.follower.stop();
    owner?.retire?.();
  }

  clear(stop = true): void {
    const owners = [...this.owners.values()];
    this.owners.clear();
    for (const owner of owners) {
      if (stop) owner.follower.stop();
      owner.retire?.();
    }
  }
}

/** Load the synchronization engine only when a session starts following. */
export class TranscriptSessionFollower {
  private runtime?: TranscriptSessionFollowerRuntime;
  private generation = 0;
  private releaseServiceState?: () => void;
  private readonly args: ConstructorParameters<typeof TranscriptSessionFollowerRuntime>;

  constructor(...args: ConstructorParameters<typeof TranscriptSessionFollowerRuntime>) {
    this.args = args;
  }

  get metrics() { return this.runtime?.metrics ?? { entries: 0, inlineBytes: 0 }; }

  async start(): Promise<void> {
    this.stop();
    const generation = ++this.generation;
    // Every local and remote owner observes the shell, including followers
    // created by late hydration callbacks after stopping was already published.
    const release = desktopHost().native.onServiceState(service => {
      if (generation === this.generation && (service.phase === "stopping" || service.phase === "exited")) {
        this.stop(false, "service_stopping");
      }
    });
    // The preload replays its current state synchronously during registration.
    if (generation !== this.generation) { release(); return; }
    this.releaseServiceState = release;
    const { TranscriptSessionFollowerRuntime } = await import("./transcriptSessionFollowerRuntime");
    if (generation !== this.generation) return;
    const runtime = new TranscriptSessionFollowerRuntime(...this.args);
    this.runtime = runtime;
    try { await runtime.start(); }
    catch (error) { if (generation === this.generation) throw error; }
  }

  stop(closeSubscription = true, reason?: "service_stopping"): void {
    this.generation++;
    this.releaseServiceState?.();
    this.releaseServiceState = undefined;
    this.runtime?.stop(closeSubscription, reason);
    this.runtime = undefined;
  }
}
