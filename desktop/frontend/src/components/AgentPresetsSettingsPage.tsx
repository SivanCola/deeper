import "./AgentPresets.css";
import { useEffect, useRef, useState } from "react";
import { Copy, LockKeyhole, Plus, RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { agentPresetError, nextAgentPresetCopyID, notifyAgentPresetsChanged, renderAgentPreset, type AgentPreset } from "../lib/agentPresets";
import { useAgentPresets } from "../lib/useAgentPresets";
import { useT } from "../lib/i18n";
import { AgentPresetEditor, type AgentPresetDraft } from "./AgentPresetEditor";
import { ErrorMessage } from "./ErrorMessage";
import { InlineConfirmButton } from "./InlineConfirmButton";
import { SettingsSection } from "./SettingsForm";

export function AgentPresetsSettingsPage({ tabId, workspaceKey }: { tabId: string; workspaceKey: string }) {
  const t = useT();
  const { view, loading, error, reload } = useAgentPresets(tabId, workspaceKey);
  const [editor, setEditor] = useState<{ draft: AgentPresetDraft; editing: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [notice, setNotice] = useState("");
  const addRef = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const closeEditor = () => { setEditor(null); requestAnimationFrame(() => addRef.current?.focus()); };
  const mutate = async (operation: () => Promise<unknown>) => {
    if (busy) return false;
    setBusy(true); setMutationError(null); setNotice("");
    try {
      await operation();
      if (!alive.current) return false;
      await reload();
      notifyAgentPresetsChanged();
      setNotice(t("agentPresets.saved"));
      return true;
    } catch (cause) {
      if (alive.current) setMutationError(agentPresetError(cause));
      return false;
    } finally { if (alive.current) setBusy(false); }
  };
  const openCopy = (preset: AgentPreset) => {
    setMutationError(null);
    setEditor({ editing: false, draft: { ...preset, id: nextAgentPresetCopyID(preset.id, view?.presets ?? []),
      name: t("agentPresets.copyName", { name: preset.id === "default" ? t("agentPresets.default") : preset.name }),
      scope: preset.scope === "project" ? "project" : "user" } });
  };

  return <section className="mem-section agent-presets" aria-label={t("settings.tab.agentPresets")} aria-busy={busy || loading}>
    {(error || mutationError) && <div className="banner banner--error" role="alert"><ErrorMessage error={mutationError || error} />
      {!editor && <button className="btn btn--small" type="button" disabled={loading} onClick={() => void reload()}>{t("common.retry")}</button>}
    </div>}
    {!!view?.problems.length && <div className="banner banner--warning" role="status"><span>{t("agentPresets.problems", { n: view.problems.length })}</span>
      {view.problems.map((problem, index) => <div key={`${index}:${problem}`}><ErrorMessage error={problem} /></div>)}
    </div>}
    {notice && <div className="set-hint" role="status">{notice}</div>}
    {editor ? <AgentPresetEditor initial={editor.draft} editing={editor.editing} presets={view?.presets ?? []} busy={busy}
      onCancel={closeEditor} onSave={async draft => {
        if (await mutate(() => app.SaveAgentPreset(tabId, draft.scope, draft.id, renderAgentPreset(draft)))) closeEditor();
      }} /> : <SettingsSection title={<h2>{t("settings.tab.agentPresets")}</h2>} description={<p>{t("agentPresets.hint")}</p>}
      actions={<><button className="btn btn--small" type="button" disabled={busy || loading} onClick={() => void reload()} aria-label={t("agentPresets.refresh")}><RefreshCw size={14} aria-hidden="true" /></button>
        <button ref={addRef} className="btn btn--small" type="button" disabled={busy || !view} onClick={() => {
          setMutationError(null); setEditor({ editing: false, draft: { id: "", name: "", description: "", body: "", keepCoding: true, tools: null, scope: "user" } });
        }}><Plus size={14} aria-hidden="true" />{t("agentPresets.new")}</button></>}>
      {!view && loading && <div className="empty" role="status">{t("agentPresets.loading")}</div>}
      {view && !view.presets.length && <div className="mem-empty" role="status">{t("agentPresets.empty")}</div>}
      <div className="cap-skills">
        {view?.presets.map(preset => <AgentPresetRow key={`${preset.scope}:${preset.id}`} preset={preset} busy={busy} current={view.current === preset.id}
          onCopy={() => openCopy(preset)} onEdit={() => setEditor({ editing: true, draft: { ...preset, scope: preset.scope === "project" ? "project" : "user" } })}
          onDelete={() => mutate(() => app.DeleteAgentPreset(tabId, preset.scope, preset.id))} />)}
      </div>
    </SettingsSection>}
  </section>;
}

function AgentPresetRow({ preset, busy, current, onCopy, onEdit, onDelete }: {
  preset: AgentPreset; busy: boolean; current: boolean; onCopy: () => void; onEdit: () => void; onDelete: () => Promise<boolean>;
}) {
  const t = useT();
  const builtin = preset.scope === "builtin";
  const name = builtin && preset.id === "default" ? t("agentPresets.default") : preset.name;
  return <article className="cap-skill-card agent-preset-row" aria-label={name}>
    <div className="agent-preset-row__head">
      <h3>{name}</h3><code>{preset.id}</code>
      <span className="cap-skill-badge">{t(`agentPresets.scope.${preset.scope}`)}</span>
      {current && <span className="cap-skill-badge">{t("agentPresets.current")}</span>}
    </div>
    <p className="cap-skill-card__desc">{builtin ? t("agentPresets.defaultDescription") : preset.description || t("agentPresets.noDescription")}</p>
    <div className="agent-preset-row__summary"><span>{t(preset.keepCoding ? "agentPresets.codingKept" : "agentPresets.codingReplaced")}</span><span>{preset.tools === null ? t("agentPresets.allTools") : t("agentPresets.toolCount", { n: preset.tools.length })}</span></div>
    {(preset.body || preset.path) && <details className="agent-preset-row__details"><summary>{t("agentPresets.details")}</summary>
      {preset.body && <pre>{preset.body}</pre>}{preset.path && <code>{preset.path}</code>}
    </details>}
    <div className="agent-preset-row__actions">
      {builtin && <span className="set-hint"><LockKeyhole size={13} aria-hidden="true" />{t("agentPresets.builtinHint")}</span>}
      <button className="btn btn--small" type="button" disabled={busy} onClick={onCopy}><Copy size={13} aria-hidden="true" />{t("common.copy")}</button>
      {!builtin && <><button className="btn btn--small" type="button" disabled={busy} onClick={onEdit}>{t("common.edit")}</button>
        <InlineConfirmButton label={t("common.delete")} confirmLabel={t("agentPresets.confirmDelete", { name })} cancelLabel={t("common.cancel")} disabled={busy} danger onConfirm={async () => { await onDelete(); }} /></>}
    </div>
  </article>;
}
