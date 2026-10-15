import { asArray } from "./array";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "./types";
import { projectSessionIdentity } from "./projectSessionIdentity";

type OrganizationBindings = ProjectTreeOrganizationBindings;

const groupsByKey: Record<string, SessionGroup[]> = {};
const groupRevisionsByKey: Record<string, number> = {};

// Match the native project-tree:changed notification across mounted browsers.
const treeListeners = new Set<() => void>();
export function subscribeMockProjectTreeChanged(listener: () => void): () => void {
  treeListeners.add(listener);
  return () => { treeListeners.delete(listener); };
}
export function notifyMockProjectTreeChanged(): void {
  treeListeners.forEach(listener => listener());
}

function organizationKey(scope: string, workspaceRoot: string): string {
  return scope === "global" ? "global|" : `project|${workspaceRoot}`;
}

export function mockProjectGroups(scope: string, workspaceRoot: string): SessionGroup[] {
  return structuredClone(groupsByKey[organizationKey(scope, workspaceRoot)] ?? []);
}

export function makeMockProjectTreeOrganizationBindings(tree: ProjectNode[]): OrganizationBindings {
  return {
    async GetSessionOrganization(workspace) {
      const key = organizationKey(workspace.scope, workspace.workspaceRoot ?? "");
      const parent = tree.find((node) => workspace.scope === "global" ? node.kind === "global_folder" : node.kind === "project" && node.root === workspace.workspaceRoot);
      const order = asArray(parent?.children).map(projectSessionIdentity).filter(Boolean);
      return { groups: structuredClone(groupsByKey[key] ?? []), revision: groupRevisionsByKey[key] ?? 0, applied: true, manualOrderEnabled: false, order };
    },
    async UpdateSessionOrganization(workspace, expectedRevision, mutation) {
      const key = organizationKey(workspace.scope, workspace.workspaceRoot ?? ""), revision = groupRevisionsByKey[key] ?? 0;
      if (revision !== expectedRevision) {
        return { groups: structuredClone(groupsByKey[key] ?? []), revision, applied: false, manualOrderEnabled: false, order: [] };
      }
      const groups = structuredClone(groupsByKey[key] ?? []);
      if (mutation.kind === "create-group") groups.push({ id: mutation.groupId ?? "", title: mutation.title ?? "", sessionKeys: [] });
      if (mutation.kind === "rename-group") {
        for (const group of groups) if (group.id === mutation.groupId) group.title = mutation.title ?? group.title;
      }
      if (mutation.kind === "delete-group") groups.splice(0, groups.length, ...groups.filter(group => group.id !== mutation.groupId));
      groupsByKey[key] = groups;
      groupRevisionsByKey[key] = revision + 1;
      return { groups: structuredClone(groups), revision: revision + 1, applied: true, manualOrderEnabled: false, order: [] };
    },
  };
}
