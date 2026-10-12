import { lazy, Suspense, type ReactNode } from "react";
import { TopicbarActionsRegion } from "./TopicbarActionsRegion";
import { shouldMountExternalOpener } from "../components/ExternalOpener";
import { tabWorkspaceTitle, topicDisplayTitle, topicTitle } from "../lib/sessionTitles";
import type { TopicbarView } from "./TopicbarRegion";
import type { TabMeta } from "../lib/types";
import type { Translator } from "../lib/i18n";
import type { SessionExportFormat } from "../app-runtime/useSessionExportCommands";

const TaskMonitorPanel = lazy(() => import("../components/TaskMonitorPanel").then((module) => ({ default: module.TaskMonitorPanel })));

/** Topicbar view projection: IM/bot detail identity, workspace label/subtitle,
 *  worktree merge entry and rename gating. Pure function of the committed tab
 *  and preferences; ownership stays in the caller. */
export function buildTopicbarView(input: {
  t: Translator;
  locale: string;
  activeTab: TabMeta | undefined;
  cwd: string | undefined;
  chromeHidden: boolean;
  windowsBrand: boolean;
  automationReturn: boolean;
  sidebar: { title: string; blocked: boolean; pressed: boolean; collapsed: boolean };
  rename: { editing: boolean; draft: string };
  draft?: { title: string; workspaceLabel: string };
}): TopicbarView {
  const { t, locale, activeTab } = input;
  const topicbarTitle = input.draft?.title ?? topicDisplayTitle(activeTab);
  const topicbarWorkspaceLabel = input.draft?.workspaceLabel ?? (activeTab ? tabWorkspaceTitle(activeTab) : "");
  const topicbarWorkspacePath = activeTab?.scope === "project" ? activeTab.workspaceRoot || input.cwd : "";
  const topicbarSubtitleVisible = Boolean(activeTab?.isolatedWorktree);
  const topicbarSubtitleTitle = topicbarWorkspacePath || topicbarWorkspaceLabel;
  const topicbarCanRename = !input.draft && (Boolean(activeTab?.topicId) || Boolean(activeTab?.remote));
  return {
    automationReturn: input.automationReturn,
    automationReturnLabel: locale === "en" ? "Back to automation" : "返回自动化",
    chromeHidden: input.chromeHidden,
    brand: input.windowsBrand,
    sidebar: input.sidebar,
    title: { text: topicbarTitle, hover: topicTitle(activeTab),
      renameLabel: t("topicBar.renameSession"), editing: input.rename.editing, draft: input.rename.draft,
      canRename: topicbarCanRename, workspaceLabel: topicbarWorkspaceLabel },
    subtitle: { visible: topicbarSubtitleVisible, title: topicbarSubtitleTitle, worktreeTabId: activeTab?.isolatedWorktree ? activeTab.id : undefined,
      mergeLabel: t("worktree.mergeAction"), mergeTooltip: t("worktree.mergeButtonTooltip"),
      sourcePlatform: undefined, sourceLabel: "" },
  };
}

/** The topicbar actions stack: per-session actions, the dock toggle and the
 *  task-monitor popover. Pure prop-driven. */
export function TopicbarActionsStack(props: {
  t: Translator;
  activeTab: TabMeta | undefined;
  activeTabId: string | undefined;
  dismissSignal: number;
  sessionHasContent: boolean;
  exportCommands: {
    getSessionMarkdown: () => Promise<string>;
    exportSession: (format: SessionExportFormat) => Promise<void>;
  };
  terminal: { toggle: () => void; enabled: boolean; open: boolean; prefetch: () => void };
  tasksOpen: false | "session" | "all";
  setTasksOpen: (update: (open: false | "session" | "all") => false | "session" | "all") => void;
  onCloseTasks: () => void;
  onOpenTaskSession: (tabID: string, taskID: string) => Promise<boolean>;
  dockToggle: ReactNode;
  launcherToggle?: ReactNode;
}) {
  const { t, activeTab } = props;
  return (
    <div className="topicbar__actions">
      {props.launcherToggle}
      <TopicbarActionsRegion sessionIdentity={activeTab?.id}
        external={shouldMountExternalOpener(activeTab) && activeTab
          ? { tabId: activeTab.id, dismissSignal: props.dismissSignal } : undefined}
        session={{
          sessionHasContent: props.sessionHasContent,
          getSessionMarkdown: props.exportCommands.getSessionMarkdown,
          exportSession: (format) => void props.exportCommands.exportSession(format),
          toggleTerminal: props.terminal.toggle, terminalEnabled: props.terminal.enabled,
          terminalOpen: props.terminal.open, prefetchTerminal: props.terminal.prefetch,
          openSessionSummary: () => props.setTasksOpen((open) => open ? false : "session"), tasksOpen: Boolean(props.tasksOpen),
        }}
      />
      {props.dockToggle}
      {props.tasksOpen && (
        <div className="taskmonitor-popover" role="dialog" aria-label={t("summary.session")}>
          <Suspense fallback={null}>
            <TaskMonitorPanel
              key={`${activeTab?.id || props.activeTabId || "none"}:${activeTab?.workspaceRoot || "global"}:${activeTab?.sessionPath || ""}:${props.tasksOpen}`}
              tabID={activeTab?.id || props.activeTabId || ""}
              initialOpen initialScope={props.tasksOpen || "session"}
              popover
              summaryMode
              onClose={props.onCloseTasks}
              onOpenSession={props.onOpenTaskSession}
            />
          </Suspense>
        </div>
      )}
    </div>
  );
}
