import { useEffect, type Dispatch, type SetStateAction } from "react";
import { app } from "./bridge";
import { asArray } from "./array";
import { projectSessionExcluded, sameProjectSession } from "./projectSessionIdentity";
import { mergeProjectTopicPage } from "./projectTreeTopic";
import { useCommittedCommand } from "./useCommittedCommand";
import type { SessionRef } from "./sessionRef";
import type { ProjectNode } from "./types";

/** A missing active row is fetched by its exact durable identity. Navigation,
 * unmount, and lifecycle fences all retire delayed completions. Tree activity
 * unrelated to this row does not cancel or restart an in-flight request.
 */
export function useProjectTreeSessionSummary(input: {
  tree: ProjectNode[];
  setTree: Dispatch<SetStateAction<ProjectNode[]>>;
  scope?: string;
  workspaceRoot?: string;
  session?: SessionRef;
  remote: boolean;
  excluded: () => ReadonlySet<string>;
}) {
  const { tree, setTree, scope, workspaceRoot, session, remote } = input;
  const hostId = session?.hostId;
  const sessionId = session?.sessionId;
  const project = tree.find(node => scope === "global" ? node.kind === "global_folder"
    : scope === "project" && node.kind === "project" && node.root === workspaceRoot);
  const projectKey = project?.key;
  const present = Boolean(session && asArray(project?.children).some(row => row.session && sameProjectSession(row, { key: "", kind: "topic", label: "", session })));
  const excluded = useCommittedCommand(input.excluded);
  useEffect(() => {
    if (remote || hostId !== "local" || !sessionId || !projectKey || present) return;
    let current = true;
    const ref = { hostId, sessionId };
    void app.GetSessionSummary(ref).then(summary => {
      if (!current || summary.session?.hostId !== hostId || summary.session.sessionId !== sessionId
        || projectSessionExcluded(summary, excluded())) return;
      if ((scope === "global" && summary.kind !== "global_topic")
        || (scope === "project" && (summary.kind !== "topic" || summary.root !== workspaceRoot))) return;
      setTree(previous => {
        if (!current || projectSessionExcluded(summary, excluded())) return previous;
        return previous.map(node => node.key === projectKey
          ? { ...node, children: mergeProjectTopicPage(asArray(node.children), [summary], true) }
          : node);
      });
    }).catch(() => {});
    return () => { current = false; };
  }, [excluded, hostId, present, projectKey, remote, scope, sessionId, setTree, workspaceRoot]);
}
