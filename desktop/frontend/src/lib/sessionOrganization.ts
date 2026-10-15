import type { SessionOrganizationMutation, SessionOrganizationSnapshot, SessionOrganizationWorkspace, SessionSelector } from "../generated/desktopContract.generated";
import type { ProjectNode, ProjectTreeOrganizationBindings } from "./types";

export function projectNodeSelector(node: ProjectNode): SessionSelector {
  const remote = node.remoteSession;
  if (remote) return remote.sessionId ? { ref: { hostId: remote.hostId, sessionId: remote.sessionId } }
    : { ref: { hostId: remote.hostId, sessionId: "" } };
  return { ref: node.session, sessionPath: node.sessionPath };
}

export async function mutateSessionOrganization(bindings: ProjectTreeOrganizationBindings, workspace: SessionOrganizationWorkspace,
  mutation: SessionOrganizationMutation): Promise<SessionOrganizationSnapshot> {
  let snapshot = await bindings.GetSessionOrganization(workspace);
  for (let attempt = 0; attempt < 3; attempt++) {
    snapshot = await bindings.UpdateSessionOrganization(workspace, snapshot.revision, mutation);
    if (snapshot.applied) return snapshot;
  }
  throw new Error("Session organization changed in another window. Please retry.");
}
