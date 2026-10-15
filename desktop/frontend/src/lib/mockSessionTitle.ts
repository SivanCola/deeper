import type { ProjectNode } from "./types";
import type { SessionMutationResult, SessionSelector } from "../generated/desktopContract.generated";
import { sessionTitleTarget } from "./sessionTitleOperation";

export interface SessionTitleBindings {
  AIRenameSessionTarget(selector: SessionSelector): Promise<SessionMutationResult>;
  RenameSessionTarget(selector: SessionSelector, title: string): Promise<SessionMutationResult>;
}

export function mockAIRenameSession(topic?: ProjectNode | null): string {
  if (!topic) return "";
  const title = topic.preview?.trim() || topic.label?.replace(/^●\s*/, "").trim() || "";
  if (title) topic.label = `${topic.label?.startsWith("● ") ? "● " : ""}${title}`;
  return title;
}

export function mockSessionTitleTarget(nodes: ProjectNode[], selector: SessionSelector): ProjectNode | undefined {
  const target = selector.ref?.sessionId
    ? `session-id:${selector.ref.sessionId}`
    : selector.sessionPath?.trim() || selector.topicId?.trim() || "";
  if (!target) return undefined;
  const find = (rows: ProjectNode[]): ProjectNode | undefined => {
    for (const node of rows) {
      if (sessionTitleTarget(node) === target || node.topicId === target || node.sessionPath === target) return node;
      const child = find(node.children ?? []);
      if (child) return child;
    }
    return undefined;
  };
  return find(nodes);
}
