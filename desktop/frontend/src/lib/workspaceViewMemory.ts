import * as projectMemory from "./workspaceTreeMemory";
import { createWorkspaceTreePersistenceScheduler } from "./workspaceTreePersistence";
import type { WorkspaceTreeMemorySnapshot } from "./workspaceTreeMemory";

interface ViewState {
  openDirs: string[];
  selectedFilePath: string | null;
  selectedChangePath: string | null;
  openTabs: string[];
  filter: string;
  scrollTop: number;
  treeWidth: number | null;
  treeWidthMode: "manual" | "even";
}
interface ProjectViews {
  views: Record<string, ViewState>;
}
type Snapshot = WorkspaceTreeMemorySnapshot & { openTabs?: string[]; filter?: string };
const STORAGE_KEY = "deepzora.workspaceViews.v1";
const PREFIX = "dock-view:";
const projects = new Map<string, ProjectViews>();
let hydrated = false;
let writable = true;

export function workspaceViewMemoryKey(project: string, tab: string): string {
  return PREFIX + JSON.stringify([project, tab]);
}

function identity(key: string): [string, string] | null {
  if (!key.startsWith(PREFIX)) return null;
  try {
    const value = JSON.parse(key.slice(PREFIX.length));
    return Array.isArray(value) && typeof value[0] === "string" && typeof value[1] === "string"
      ? [value[0], value[1]] : null;
  } catch { return null; }
}

function sanitize(value: Partial<ViewState> = {}): ViewState {
  const paths = (items: unknown): string[] => Array.isArray(items)
    ? items.filter((path): path is string => typeof path === "string") : [];
  return {
    openDirs: paths(value.openDirs).length ? paths(value.openDirs) : [""],
    selectedFilePath: typeof value.selectedFilePath === "string" ? value.selectedFilePath : null,
    selectedChangePath: typeof value.selectedChangePath === "string" ? value.selectedChangePath : null,
    openTabs: [...new Set(paths(value.openTabs))].slice(-5),
    filter: typeof value.filter === "string" ? value.filter : "",
    scrollTop: Number.isFinite(value.scrollTop) && value.scrollTop! >= 0 ? value.scrollTop! : 0,
    treeWidth: Number.isFinite(value.treeWidth) && value.treeWidth! > 0 ? value.treeWidth! : null,
    treeWidthMode: value.treeWidthMode === "even" ? "even" : "manual",
  };
}

function hydrate() {
  if (hydrated) return;
  hydrated = true;
  try {
    const data = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "null");
    if (!data) return;
    if (data.version !== 1) { writable = false; return; }
    for (const [key, value] of Object.entries(data.projects ?? {})) {
      const project = value as ProjectViews;
      if (!project || typeof project.views !== "object" || !project.views) continue;
      projects.set(key, { views: Object.fromEntries(
        Object.entries(project.views).filter(([, view]) => view && typeof view === "object")
          .map(([id, view]) => [id, sanitize(view)]),
      ) });
    }
  } catch { /* Storage is optional. */ }
}

function persist() {
  if (!writable) return;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ version: 1, projects: Object.fromEntries(projects) }));
  } catch { /* Keep the session's in-memory state when storage is unavailable. */ }
}
const deferred = createWorkspaceTreePersistenceScheduler(persist);

function resolve(key: string) {
  const id = identity(key);
  if (!id) return null;
  hydrate();
  const [projectKey, tab] = id;
  const project = projects.get(projectKey) ?? { views: Object.create(null) as Record<string, ViewState> };
  const shared = projectMemory.readWorkspaceTreeMemory(projectKey);
  if (!Object.prototype.hasOwnProperty.call(project.views, tab)) {
    project.views[tab] = sanitize();
    projects.set(projectKey, project);
    persist();
  }
  return { projectKey, project, tab, shared };
}

export function readWorkspaceTreeMemory(key: string): Snapshot | null {
  const view = resolve(key);
  if (!view) return projectMemory.readWorkspaceTreeMemory(key);
  const state = view.project.views[view.tab];
  return {
    ...state, openDirs: new Set(state.openDirs), openTabs: [...state.openTabs], visitId: 0,
    recentPaths: view.shared?.recentPaths ?? [],
    dockTreeWidth: view.shared?.dockTreeWidth ?? null,
    dockPreviewWidth: view.shared?.dockPreviewWidth ?? null,
  };
}

export function rememberWorkspaceTreeState(
  key: string, patch: Partial<Omit<Snapshot, "openDirs">> & { openDirs?: ReadonlySet<string> },
) {
  const view = resolve(key);
  if (!view) { projectMemory.rememberWorkspaceTreeState(key, patch); return; }
  const { recentPaths, dockTreeWidth, dockPreviewWidth, visitId: _visitId, ...browse } = patch;
  const projectPatch = {
    ...(recentPaths !== undefined ? { recentPaths } : {}),
    ...(dockTreeWidth !== undefined ? { dockTreeWidth } : {}),
    ...(dockPreviewWidth !== undefined ? { dockPreviewWidth } : {}),
  };
  if (Object.keys(projectPatch).length) projectMemory.rememberWorkspaceTreeState(view.projectKey, projectPatch);
  if (Object.keys(browse).length) {
    view.project.views[view.tab] = sanitize({
      ...view.project.views[view.tab], ...browse,
      openDirs: browse.openDirs ? [...browse.openDirs] : view.project.views[view.tab].openDirs,
    });
    deferred.cancel();
    persist();
  }
}

export function rememberWorkspaceTreeScroll(key: string, scrollTop: number) {
  const view = resolve(key);
  if (!view) { projectMemory.rememberWorkspaceTreeScroll(key, scrollTop); return; }
  if (!Number.isFinite(scrollTop) || scrollTop < 0) return;
  view.project.views[view.tab] = { ...view.project.views[view.tab], scrollTop };
  deferred.schedule(key);
}
export function flushWorkspaceTreeMemory() { deferred.flush(); projectMemory.flushWorkspaceTreeMemory(); }
export function workspaceTreeVisitId(key: string) { return identity(key) ? 0 : projectMemory.workspaceTreeVisitId(key); }
export function touchWorkspaceTreeVisit(key: string, visitId: number) {
  if (!identity(key)) projectMemory.touchWorkspaceTreeVisit(key, visitId);
}
export function rememberWorkspaceTreeOpenDirs(key: string, openDirs: ReadonlySet<string>, visitId: number) {
  rememberWorkspaceTreeState(key, { openDirs, visitId });
}

export function reloadWorkspaceViewMemoryForTests() {
  deferred.flush();
  projects.clear();
  hydrated = false;
  writable = true;
}
