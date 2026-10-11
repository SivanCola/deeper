import { isShellToolName } from "../lib/shellToolIdentity";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { useT, type Translator } from "../lib/i18n";
import { normalizeToolApprovalMode, type DirEntry } from "../lib/types";
import {
  DecisionConfirmBar,
  PromptAction,
  PromptBadge,
  PromptDescriptionDisclosure,
  PromptHeaderAction,
  PromptShelf,
} from "./PromptShelf";
import { animateElementExit, DUR_FAST } from "../lib/motion";
import {
  FileReferenceMenu,
  insertTextAtSelection,
  pickInlineFileReference,
  useFileReferenceMenu,
} from "./FileReferenceMenu";
import { WriteAccessApprovalDetails, writeAccessDecisionActions, type DecisionAction } from "./WriteAccessApproval";
import type { ApprovalModalProps } from "./approvalTypes";
import { approvalToolLabel } from "./approvalToolLabel";
import { usePromptStop } from "../lib/usePromptStop";
export { approvalToolLabel } from "./approvalToolLabel";

function requiresFreshHumanApproval(tool: string): boolean {
  return tool === "remember" || tool === "forget" || tool === "exit_plan_mode" || tool === "sandbox_escape" || tool === "config_write";
}

const APPROVAL_MODE_RANK = { "read-only": 0, "workspace-write": 1, "danger-full-access": 2 } as const;

const sandboxEscapeEnglishSubjectFallback = "run shell command unconfined once";
const sandboxEscapeEnglishSubjectPrefix = "run unconfined once: ";
const configWriteEnglishSubjectPrefix = "write Deepzora config: ";
const planModeBashEnglishSubject = /^Trust (.+) as a read-only command prefix while planning\r?\nCommand: ([\s\S]+)$/;

function localizeApprovalSubject(tool: string, subject: string, t: Translator): string {
  const trimmed = subject.trim();
  if (tool === "sandbox_escape") {
    if (!trimmed || trimmed === sandboxEscapeEnglishSubjectFallback) return t("approval.sandboxEscapeSubjectFallback");
    const localizedPrefix = t("approval.sandboxEscapeSubjectPrefix");
    if (trimmed.startsWith(sandboxEscapeEnglishSubjectPrefix)) {
      return trimmed.slice(sandboxEscapeEnglishSubjectPrefix.length).trim() || t("approval.sandboxEscapeSubjectFallback");
    }
    if (localizedPrefix !== sandboxEscapeEnglishSubjectPrefix && trimmed.startsWith(localizedPrefix)) {
      return trimmed.slice(localizedPrefix.length).trim() || t("approval.sandboxEscapeSubjectFallback");
    }
    return trimmed;
  }
  if (tool === "config_write") {
    if (trimmed.startsWith(configWriteEnglishSubjectPrefix)) {
      return `${t("approval.configWriteSubjectPrefix")}${trimmed.slice(configWriteEnglishSubjectPrefix.length)}`;
    }
    return trimmed;
  }
  if (tool === "remember") {
    return trimmed
      .replace(/^Save\/update memory/, t("approval.memorySaveUpdate"))
      .replace(/\bbody: /g, `${t("approval.memoryBodyLabel")}: `);
  }
  if (tool === "forget" && trimmed.startsWith("Archive memory ")) {
    return `${t("approval.memoryArchivePrefix")}${trimmed.slice("Archive memory ".length)}`;
  }
  const bashTrust = trimmed.match(planModeBashEnglishSubject);
  if (bashTrust) {
    return t("approval.planModeBashTrustSubject", { prefix: bashTrust[1] ?? "", command: bashTrust[2] ?? "" });
  }
  return trimmed;
}

function localizeApprovalReason(tool: string, reason: string | undefined, t: Translator): string {
  let trimmed = reason?.trim() ?? "";
  let matchedRule = "";
  const matchedRulePrefix = "Matched permission rule: ";
  if (trimmed.startsWith(matchedRulePrefix)) {
    const [ruleLine, ...remainingLines] = trimmed.split(/\r?\n/);
    matchedRule = t("approval.matchedPermissionRule", { rule: ruleLine.slice(matchedRulePrefix.length).trim() });
    trimmed = remainingLines.join("\n").trim();
  }
  let localized = trimmed;
  if (
    isShellToolName(tool) &&
    (trimmed.includes("nested or indirect shell execution") || trimmed.includes("requests access outside the active permission preset"))
  ) {
    localized = t("approval.dynamicBashReason");
  }
  if (tool === "config_write") {
    localized = !trimmed || trimmed.includes("Deepzora-managed configuration file") ? t("approval.configWriteReason") : trimmed;
  }
  if (tool === "sandbox_escape") {
    if (trimmed.includes("could not wrap this command") || trimmed.includes("does not provide an OS-level Bash sandbox")) {
      localized = t("approval.sandboxEscapeWrapReason");
    } else if (
      trimmed.includes("failed while starting this command") ||
      trimmed.includes("could not start this command") ||
      trimmed.includes("Run this command unconfined once?")
    ) {
      localized = t("approval.sandboxEscapeRuntimeReason");
    } else {
      localized ||= t("approval.sandboxEscapeRuntimeReason");
    }
  }
  return [matchedRule, localized].filter(Boolean).join(" ");
}

function localizePlanModeApprovalReason(tool: string, reason: string, t: Translator): string {
  if (tool === "plan_mode_read_only_command" && reason.includes("built-in read-only set")) {
    return t("approval.planModeBashTrustReason");
  }
  return reason;
}

export function ApprovalModal(props: ApprovalModalProps) {
  const { approval } = props;
  const identity = JSON.stringify([props.tabId, approval.id, approval.kind,
    approval.turnId, approval.runtimeEpoch, approval.generation, approval.permissionRevision]);
  return <InteractiveApprovalModal key={identity} {...props} />;
}

function InteractiveApprovalModal({
  approval,
  onAnswer,
  onRevisePlan,
  onExitPlan,
  onStop,
  cwd,
  tabId,
  workspaceScopeKey,
  insertRequest,
  onRevisionActiveChange,
  toolApprovalMode,
}: ApprovalModalProps) {
  const t = useT();
  const isPlanApproval = approval.tool === "exit_plan_mode";
  const isWriteAccessApproval = approval.kind === "write_access" || Boolean(approval.write_access);
  const toolLabel = approvalToolLabel(approval.tool, t);
  const isFreshHumanApproval = approval.fresh === true || requiresFreshHumanApproval(approval.tool);
  const hasFreshSessionGrant = approval.tool === "sandbox_escape" || approval.tool === "config_write";
  // Switching the approval segmented control to a more permissive mode does not
  // resolve an already-pending request; say so on the card instead of leaving
  // the user to wonder why the switch "did nothing".
  const initialToolApprovalModeRef = useRef(toolApprovalMode);
  const approvalModeRelaxed =
    !isPlanApproval &&
    toolApprovalMode !== undefined &&
    initialToolApprovalModeRef.current !== undefined &&
    APPROVAL_MODE_RANK[normalizeToolApprovalMode(toolApprovalMode)] > APPROVAL_MODE_RANK[normalizeToolApprovalMode(initialToolApprovalModeRef.current)];
  const subject = localizeApprovalSubject(approval.tool, approval.subject, t);
  const reason = localizePlanModeApprovalReason(approval.tool, localizeApprovalReason(approval.tool, approval.reason, t), t);
  // Plan approvals already show the plan above; keep a short hint. Tool
  // approvals render their command/subject in the details block, so header
  // metadata is only a fallback when there is no subject to show there.
  const toolMeta = isPlanApproval ? t("approval.planReadyHint") : (!subject ? (reason || approval.tool) : undefined);
  const hasToolDetails = Boolean(reason || subject);
  // Subject (command) is visible by default; long reason can collapse.
  const [reasonOpen, setReasonOpen] = useState(() => {
    return Boolean(reason) && reason.length <= 160;
  });
  // Immediate Plan/Auto decisions have no hidden selection. Ordinary tool
  // approvals retain select-then-confirm and default to Allow once.
  const [selectedIndex, setSelectedIndex] = useState(() => (isPlanApproval ? -1 : 0));
  const [expandedDescriptionId, setExpandedDescriptionId] = useState<string | null>(null);
  const [descriptionTruncated, setDescriptionTruncated] = useState(false);
  const [revisionOpen, setRevisionOpen] = useState(false);
  const [revisionText, setRevisionText] = useState("");
  const [answerPending, setSubmitting] = useState(false);
  const { stopping, stopFailed, stopTask } = usePromptStop(onStop);
  const [submitFailed, setSubmitFailed] = useState(false);
  const submitting = answerPending || stopping;
  const instanceId = useId();
  const cardRef = useRef<HTMLDivElement | null>(null);
  const shelfRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const consumedInsertIdRef = useRef(0);
  const onRevisionActiveChangeRef = useRef(onRevisionActiveChange);
  const revisionActiveRef = useRef(false);
  onRevisionActiveChangeRef.current = onRevisionActiveChange;
  // When consecutive approvals arrive, animate the old card out before the
  // new one slides in so a queue of pending approvals does not visibly pop.
  const closingRef = useRef(false);
  const fileMenu = useFileReferenceMenu(revisionText, cwd, tabId, workspaceScopeKey);

  const answerWithExit = (fn: () => void | Promise<void>) => {
    if (closingRef.current || submitting) return;
    closingRef.current = true;
    setSubmitting(true);
    setSubmitFailed(false);
    let result: void | Promise<void>;
    try {
      result = fn();
    } catch {
      closingRef.current = false;
      setSubmitting(false);
      setSubmitFailed(true);
      return;
    }
    void Promise.resolve(result).catch(() => {
      closingRef.current = false;
      setSubmitting(false);
      setSubmitFailed(true);
    });
    const el = shelfRef.current;
    if (el) animateElementExit(el, { opacity: 0, y: 8, duration: DUR_FAST, onComplete: () => undefined });
  };

  const toolActions: DecisionAction[] = isWriteAccessApproval
    ? writeAccessDecisionActions(t, onAnswer)
    : isPlanApproval
    ? [
        {
          key: "1",
          label: t("approval.startExecution"),
          desc: t("approval.startExecutionDesc"),
          primary: true,
          kind: "direct",
          run: () => onAnswer(true, false, false),
        },
        {
          key: "2",
          label: t("approval.revisePlan"),
          desc: t("approval.revisePlanDesc"),
          kind: "toggle-revision",
        },
        ...(onExitPlan
          ? [{
              key: "3",
              label: t("approval.exitPlanWithoutExecution"),
              desc: t("approval.exitPlanWithoutExecutionDesc"),
              kind: "direct" as const,
              run: () => onExitPlan(),
            }]
          : []),
      ]
    : [
        {
          key: "1",
          label: t("approval.allowOnce"),
          desc: t("approval.allowOnceDesc"),
          kind: "submit",
          run: () => onAnswer(true, false, false),
        },
        ...(isFreshHumanApproval
          ? hasFreshSessionGrant
            ? [
                {
                  key: "2",
                  label: t(approval.tool === "config_write" ? "approval.allowConfigWriteSession" : "approval.allowSandboxEscapeSession"),
                  desc: t(approval.tool === "config_write" ? "approval.allowConfigWriteSessionDesc" : "approval.allowSandboxEscapeSessionDesc"),
                  kind: "submit" as const,
                  run: () => onAnswer(true, true, false),
                },
                {
                  key: "3",
                  label: t("approval.deny"),
                  desc: t("approval.denyDesc"),
                  tone: "danger" as const,
                  kind: "submit" as const,
                  run: () => onAnswer(false, false, false),
                },
              ]
            : [
                {
                  key: "2",
                  label: t("approval.deny"),
                  desc: t("approval.denyDesc"),
                  tone: "danger" as const,
                  kind: "submit" as const,
                  run: () => onAnswer(false, false, false),
                },
              ]
          : [
              {
                key: "2",
                label: t("approval.allowRuleSession"),
                desc: t("approval.allowRuleSessionDesc"),
                kind: "submit" as const,
                run: () => onAnswer(true, true, false),
              },
              {
                key: "3",
                label: t("approval.deny"),
                desc: t("approval.denyDesc"),
                tone: "danger" as const,
                kind: "submit" as const,
                run: () => onAnswer(false, false, false),
              },
            ]),
      ];

  const actionCount = toolActions.length;
  const selectedIndexRef = useRef(selectedIndex);
  selectedIndexRef.current = selectedIndex;
  const selectedAction = toolActions[Math.min(selectedIndex, actionCount - 1)] ?? toolActions[0];
  const selectedDescriptionId = !isPlanApproval && selectedIndex >= 0
    ? `${instanceId}-description-${selectedIndex}`
    : undefined;
  const descriptionExpanded = selectedDescriptionId !== undefined && expandedDescriptionId === selectedDescriptionId;

  useEffect(() => {
    // Ordinary permission cards must not steal focus from the composer or an
    // active IME composition. Plan and recovery decisions retain focus because
    // they replace the composer interaction rather than supplement it.
    if (isPlanApproval) cardRef.current?.focus();
  }, [isPlanApproval]);

  useEffect(() => {
    setExpandedDescriptionId(null);
  }, [approval.id]);

  const confirmSelected = useCallback(() => {
    if (submitting || closingRef.current) return;
    if (isPlanApproval) return;
    const action = toolActions[selectedIndexRef.current];
    if (!action) return;
    if (action.kind === "toggle-revision") {
      setRevisionOpen((open) => !open);
      return;
    }

    if (action.run) answerWithExit(action.run);
  }, [submitting, toolActions, isPlanApproval]);

  const activateAction = useCallback((action: DecisionAction, index: number) => {
    if (submitting) return;
    if (action.kind === "direct" && action.run) {
      answerWithExit(action.run);
      return;
    }
    if (action.kind === "toggle-revision") {
      setRevisionOpen((open) => !open);
      return;
    }

    setSelectedIndex(index);
  }, [submitting]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && submitting) {
        event.preventDefault();
        stopTask();
        return;
      }
      if (submitting) return;

      const target = event.target instanceof Element ? event.target : null;
      const tag = target?.tagName.toLowerCase();
      // Editing revision / file menu owns arrows and digits while focused.
      // Custom recovery guidance owns all decision shortcuts while expanded.
      const editing =
        tag === "input" ||
        tag === "textarea" ||
        tag === "select" ||
        (target instanceof HTMLElement && target.isContentEditable);
      if (editing && (event.key === "1" || event.key === "2" || event.key === "3" || event.key === "4")) {
        return;
      }
      if (tag === "input" || tag === "textarea" || tag === "select" || (target instanceof HTMLElement && target.isContentEditable)) return;
      const immediateDecision = isPlanApproval;
      if (immediateDecision && (event.key === "ArrowUp" || event.key === "ArrowDown" || event.key === "Enter")) {
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setSelectedIndex((i) => {
          const base = i < 0 ? 0 : i;
          return (base - 1 + actionCount) % actionCount;
        });
      } else if (event.key === "ArrowDown") {
        event.preventDefault();
        setSelectedIndex((i) => {
          const base = i < 0 ? -1 : i;
          return (base + 1) % actionCount;
        });
      } else if (event.key === "Enter") {
        event.preventDefault();
        confirmSelected();
      } else if (event.key === "1" || event.key === "2" || event.key === "3" || event.key === "4") {
        const index = Number(event.key) - 1;
        if (index < 0 || index >= actionCount) return;
        event.preventDefault();
        if (immediateDecision) {
          const action = toolActions[index];
          if (action) activateAction(action, index);
          return;
        }
        setSelectedIndex(index);
      } else if (event.key === "Escape") {
        event.preventDefault();
        stopTask();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [actionCount, activateAction, confirmSelected, stopTask, submitting, isPlanApproval, toolActions]);

  useEffect(() => {
    revisionActiveRef.current = revisionOpen;
    onRevisionActiveChangeRef.current?.(revisionOpen);
    if (revisionOpen) inputRef.current?.focus();
  }, [revisionOpen]);

  useEffect(() => () => {
    if (revisionActiveRef.current) onRevisionActiveChangeRef.current?.(false);
  }, []);

  const focusRevisionInput = (caret = revisionText.length) => {
    requestAnimationFrame(() => {
      const input = inputRef.current;
      if (!input) return;
      input.focus();
      input.setSelectionRange(caret, caret);
    });
  };

  const insertRevisionText = useCallback((text: string) => {
    const input = inputRef.current;
    const start = input?.selectionStart ?? revisionText.length;
    const end = input?.selectionEnd ?? start;
    const next = insertTextAtSelection(revisionText, text, start, end);
    setRevisionText(next.value);
    focusRevisionInput(next.caret);
  }, [revisionText]);

  useEffect(() => {
    if (!insertRequest || insertRequest.id === consumedInsertIdRef.current) return;
    consumedInsertIdRef.current = insertRequest.id;
    insertRevisionText(insertRequest.text);
  }, [insertRequest, insertRevisionText]);

  const pickRevisionFile = (entry: DirEntry) => {
    const next = pickInlineFileReference(revisionText, fileMenu.atRaw, fileMenu.atDir, entry);
    setRevisionText(next);
    focusRevisionInput(next.length);
  };

  const onRevisionKeyDown = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      submitRevision();
      event.stopPropagation();
      return;
    }
    if (fileMenu.open) {
      if (event.key === "ArrowDown" && fileMenu.count > 0) {
        event.preventDefault();
        fileMenu.setActive((index) => (index + 1) % fileMenu.count);
        return;
      }
      if (event.key === "ArrowUp" && fileMenu.count > 0) {
        event.preventDefault();
        fileMenu.setActive((index) => (index - 1 + fileMenu.count) % fileMenu.count);
        return;
      }
      if ((event.key === "Enter" || event.key === "Tab") && fileMenu.count > 0) {
        event.preventDefault();
        const entry = fileMenu.items[fileMenu.active];
        if (entry) pickRevisionFile(entry);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        fileMenu.dismiss();
        return;
      }
    }
    event.stopPropagation();
  };

  const submitRevision = () => {
    const text = revisionText.trim();
    if (!text) {
      inputRef.current?.focus();
      return;
    }
    answerWithExit(() => onRevisePlan?.(text));
  };

  const confirmIsDanger = selectedAction?.tone === "danger";
  const confirmLabel =
    selectedAction?.kind === "toggle-revision"
      ? revisionOpen
        ? t("common.cancel")
        : t("approval.revisePlan")
      : t("decision.confirm");

  return (
    <div ref={shelfRef}>
      <PromptShelf
        decision
        actionsRole={isPlanApproval ? "group" : "listbox"}
        className={isPlanApproval ? "prompt-shelf--plan-approval" : "prompt-shelf--tool-approval"}
        barRef={cardRef}
        titleId={isPlanApproval ? "plan-approval-title" : "tool-approval-title"}
        title={
          isPlanApproval
            ? t("approval.planReady")
            : isWriteAccessApproval
              ? t("approval.writeAccessPending")
             : t("approval.toolPending")
        }
        badges={
          <>
            {!isPlanApproval && <PromptBadge tone="amber">{toolLabel}</PromptBadge>}
            {isPlanApproval && revisionOpen && <PromptBadge>{t("approval.revisePlan")}</PromptBadge>}
          </>
        }
        meta={toolMeta}
        headerActions={
          <>
            {!isPlanApproval && hasToolDetails && reason && (
              <PromptHeaderAction onClick={() => setReasonOpen((open) => !open)} disabled={submitting}>
                {t(reasonOpen ? "approval.hideDetails" : "approval.details")}
              </PromptHeaderAction>
            )}
            {!isPlanApproval && (
              <PromptHeaderAction
                onClick={stopTask}
                ariaLabel={t("decision.stopTask")}
                disabled={stopping}
              >
                {t("decision.stopTask")}
              </PromptHeaderAction>
            )}
          </>
        }
        actions={
          <>
            {toolActions.map((action, index) => {
              const actionNode = (
                <PromptAction
                  key={action.key}
                  keyLabel={action.key}
                  label={action.label}
                  description={action.desc}
                  descriptionId={`${instanceId}-description-${index}`}
                  descriptionDisclosure
                  onDescriptionOverflowChange={!isPlanApproval && selectedIndex === index
                    ? setDescriptionTruncated
                    : undefined}
                  onClick={() => {
                    activateAction(action, index);
                  }}
                  primary={action.primary}
                  selected={selectedIndex === index}
                  tone={action.tone}
                  role={isPlanApproval ? "button" : "option"}
                  disabled={submitting}
                />
              );

              return actionNode;
            })}
          </>
        }
        note={
          !isPlanApproval && selectedDescriptionId && descriptionTruncated ? (
            <PromptDescriptionDisclosure
              descriptionId={`${selectedDescriptionId}-detail`}
              label={selectedAction?.label}
              description={selectedAction?.desc ?? ""}
              expanded={descriptionExpanded}
              onToggle={() => setExpandedDescriptionId((current) => current === selectedDescriptionId ? null : selectedDescriptionId)}
              disabled={submitting}
            />
          ) : undefined
        }
        footer={
          isPlanApproval ? undefined : (
            <DecisionConfirmBar
              hint={t("decision.selectHint")}
              confirmLabel={confirmLabel}
              onConfirm={confirmSelected}
              disabled={submitting}
              danger={confirmIsDanger}
            />
          )
        }
      >
        {(submitFailed || stopFailed) && <p role="alert">{t("approval.submitFailed")}</p>}
        {(approvalModeRelaxed ||
          (!isPlanApproval && (subject || isWriteAccessApproval || (reasonOpen && reason))) ||
          (isPlanApproval && revisionOpen)) && (
          <>
            {approvalModeRelaxed && (
              <div className="approval-mode-hint">{t("approval.modeSwitchPendingHint")}</div>
            )}
            {!isPlanApproval && isWriteAccessApproval && (
              <WriteAccessApprovalDetails approval={approval} subject={subject} reason={reason} reasonOpen={reasonOpen} t={t} />
            )}
            {!isPlanApproval && !isWriteAccessApproval && subject && (
              <div className="approval-details">
                <pre className="approval-subject">{subject}</pre>
                {reasonOpen && reason && <div className="approval-reason">{reason}</div>}
              </div>
            )}
            {isPlanApproval && revisionOpen && (
              <div className="plan-revision">
                <textarea
                  ref={inputRef}
                  className="plan-revision__input"
                  value={revisionText}
                  rows={3}
                  placeholder={t("approval.revisePlanPlaceholder")}
                  onChange={(event) => setRevisionText(event.target.value)}
                  onFocus={() => onRevisionActiveChange?.(true)}
                  onKeyDown={onRevisionKeyDown}
                  disabled={submitting}
                />
                {fileMenu.open && (
                  <FileReferenceMenu
                    items={fileMenu.items}
                    activeIndex={fileMenu.active}
                    onPick={pickRevisionFile}
                    onHover={fileMenu.setActive}
                  />
                )}
                <div className="plan-revision__actions">
                  <button className="btn" type="button" onClick={() => setRevisionOpen(false)} disabled={submitting}>
                    {t("common.cancel")}
                  </button>
                  <button className="btn btn--primary" type="button" onClick={submitRevision} disabled={submitting}>
                    {t("approval.sendRevision")}
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </PromptShelf>
    </div>
  );
}
