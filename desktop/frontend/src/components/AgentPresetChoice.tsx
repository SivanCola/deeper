import "./AgentPresets.css";
import { useEffect, useRef, useState } from "react";
import { Bot, LockKeyhole } from "lucide-react";
import { app } from "../lib/bridge";
import { agentPresetError } from "../lib/agentPresets";
import { useAgentPresets } from "../lib/useAgentPresets";
import { useT } from "../lib/i18n";
import { ComposerChoice } from "./ComposerChoice";
import { ErrorMessage } from "./ErrorMessage";
import { Tooltip } from "./Tooltip";

export function AgentPresetChoice({ tabId, sessionKey, current, locked = false, running, disabled, dismissSignal, onPendingChange }: {
  tabId: string;
  sessionKey?: string;
  current?: string;
  locked?: boolean;
  running: boolean;
  disabled?: boolean;
  dismissSignal?: number;
  onPendingChange: (pending: boolean) => void;
}) {
  const t = useT();
  const { view, error, loading, reload } = useAgentPresets(tabId, `${sessionKey}:${running}:${current}:${locked}`);
  const [pending, setPending] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; onPendingChange(false); };
  }, [onPendingChange]);
  const selected = view?.current || current || "default";
  const preset = view?.presets.find(item => item.id === selected);
  const name = selected === "default" ? t("agentPresets.default") : preset?.name || selected;
  const isLocked = locked || view?.locked || running;
  const choose = async (id: string) => {
    if (id === selected || pending || isLocked) return;
    setPending(true);
    onPendingChange(true);
    setSaveError(null);
    try {
      await app.SetAgentPresetForTab(tabId, id);
      if (alive.current) await reload();
    } catch (cause) {
      if (alive.current) setSaveError(agentPresetError(cause));
    } finally {
      if (alive.current) { setPending(false); onPendingChange(false); }
    }
  };
  if (error && !view) return <div className="agent-preset-choice agent-preset-choice--error" role="status">
    <Tooltip label={error}><span>{t("agentPresets.unavailable")}</span></Tooltip>
    <button className="chip" type="button" disabled={loading} onClick={() => void reload()}>{t("common.retry")}</button>
  </div>;
  return <div className="agent-preset-choice" aria-busy={pending || loading}>
    <Tooltip label={t(isLocked ? "agentPresets.lockedHint" : "agentPresets.chooseHint")}>
      <span>
        <ComposerChoice label={loading && !view ? t("agentPresets.loading") : name}
          ariaLabel={`${t("agentPresets.picker")}: ${name}${isLocked ? ` · ${t("agentPresets.locked")}` : ""}`}
          icon={isLocked ? <LockKeyhole size={16} aria-hidden="true" /> : <Bot size={16} aria-hidden="true" />}
          showChevron={!isLocked} value={selected} disabled={disabled || pending || loading && !view || isLocked}
          dismissSignal={dismissSignal} onPick={id => void choose(id)}
          options={(view?.presets ?? []).map(item => ({ value: item.id, label: item.id === "default" ? t("agentPresets.default") : item.name,
            description: item.id === "default" ? t("agentPresets.defaultDescription") : item.description,
            badge: t(`agentPresets.scope.${item.scope}`) }))} />
      </span>
    </Tooltip>
    {isLocked && <span className="sr-only">{t("agentPresets.lockedHint")}</span>}
    {(saveError || error) && <span role="alert" className="agent-preset-choice__error"><ErrorMessage error={saveError || error} /></span>}
    {!!view?.problems.length && <Tooltip label={view.problems.join("\n")}><span className="agent-preset-choice__warning" role="status">{t("agentPresets.problems", { n: view.problems.length })}</span></Tooltip>}
  </div>;
}
