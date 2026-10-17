import { useEffect, useId, useRef, useState } from "react";
import { AGENT_PRESET_ID_PATTERN, agentPresetTools, type AgentPreset, type WritableAgentPresetScope } from "../lib/agentPresets";
import { useT } from "../lib/i18n";
import { SettingsField, SettingsSection } from "./SettingsForm";
import { SettingsOptions } from "./SettingsOptions";
import { SettingsSelect } from "./SettingsSelect";

export type AgentPresetDraft = Pick<AgentPreset, "id" | "name" | "description" | "body" | "keepCoding" | "tools"> & { scope: WritableAgentPresetScope };

export function AgentPresetEditor({ initial, editing, presets, busy, onSave, onCancel }: {
  initial: AgentPresetDraft;
  editing: boolean;
  presets: AgentPreset[];
  busy: boolean;
  onSave: (draft: AgentPresetDraft) => Promise<void>;
  onCancel: () => void;
}) {
  const t = useT();
  const prefix = useId();
  const nameRef = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(initial);
  const [toolText, setToolText] = useState(initial.tools?.join("\n") ?? "");
  const id = draft.id.trim();
  const idInvalid = !AGENT_PRESET_ID_PATTERN.test(id) || id === "default";
  const idTaken = !editing && presets.some(preset => preset.id === id && preset.scope === draft.scope);
  const replacementMissing = !draft.keepCoding && !draft.body.trim();
  const invalid = idInvalid || idTaken || !draft.name.trim() || replacementMissing;
  const patch = (next: Partial<AgentPresetDraft>) => setDraft(previous => ({ ...previous, ...next }));
  useEffect(() => { nameRef.current?.focus(); }, []);

  return <form className="agent-preset-editor" aria-label={t(editing ? "agentPresets.edit" : "agentPresets.new")}
    onSubmit={event => { event.preventDefault(); if (!invalid && !busy) void onSave({ ...draft, id, tools: draft.tools === null ? null : agentPresetTools(toolText) }); }}>
    <button className="subagents-form-back" type="button" disabled={busy} onClick={onCancel}>{t("agentPresets.back")}</button>
    <SettingsSection title={<h2>{t(editing ? "agentPresets.edit" : "agentPresets.new")}</h2>} description={t("agentPresets.editHint")}>
      <SettingsField label={<label htmlFor={`${prefix}-name`}>{t("agentPresets.name")}</label>}>
        <input ref={nameRef} id={`${prefix}-name`} className="mem-input set-grow" required value={draft.name} disabled={busy} onChange={event => patch({ name: event.target.value })} />
      </SettingsField>
      <SettingsField label={<label htmlFor={`${prefix}-id`}>{t("agentPresets.id")}</label>} hint={t(editing ? "agentPresets.idFixed" : "agentPresets.idHint")}>
        <div className="agent-preset-editor__field">
          <input id={`${prefix}-id`} className="mem-input set-grow" required value={draft.id} disabled={editing || busy} autoCapitalize="none" spellCheck={false}
            aria-invalid={!!id && (idInvalid || idTaken)} aria-describedby={`${prefix}-id-error`}
            onChange={event => patch({ id: event.target.value })} />
          <span id={`${prefix}-id-error`} className="agent-preset-editor__error" role="status">{id && idInvalid ? t("agentPresets.idInvalid") : idTaken ? t("agentPresets.idTaken") : ""}</span>
        </div>
      </SettingsField>
      <SettingsField label={<label htmlFor={`${prefix}-description`}>{t("agentPresets.description")}</label>}>
        <input id={`${prefix}-description`} className="mem-input set-grow" value={draft.description} disabled={busy} onChange={event => patch({ description: event.target.value })} />
      </SettingsField>
      <SettingsField label={t("agentPresets.scope")} hint={t("agentPresets.scopeHint")}>
        <SettingsSelect value={draft.scope} disabled={editing || busy} aria-label={t("agentPresets.scope")}
          onValueChange={value => patch({ scope: value as WritableAgentPresetScope })}
          options={[{ value: "user", label: t("agentPresets.scope.user") }, { value: "project", label: t("agentPresets.scope.project") }]} />
      </SettingsField>
      <SettingsField label={t("agentPresets.keepCoding")} hint={t("agentPresets.keepCodingHint")}>
        <SettingsOptions className="set-seg" role="group" aria-label={t("agentPresets.keepCoding")}>
          {[true, false].map(value => <button type="button" key={String(value)} disabled={busy} aria-pressed={draft.keepCoding === value}
            className={`set-seg__btn${draft.keepCoding === value ? " set-seg__btn--on" : ""}`} onClick={() => patch({ keepCoding: value })}>
            {t(value ? "agentPresets.keep" : "agentPresets.replace")}
          </button>)}
        </SettingsOptions>
      </SettingsField>
      <SettingsField label={<label htmlFor={`${prefix}-body`}>{t("agentPresets.body")}</label>} stacked>
        <textarea id={`${prefix}-body`} className="mem-textarea" rows={8} value={draft.body} disabled={busy} required={!draft.keepCoding}
          aria-invalid={replacementMissing} aria-describedby={`${prefix}-body-hint`} onChange={event => patch({ body: event.target.value })} />
        <span id={`${prefix}-body-hint`} className={replacementMissing ? "agent-preset-editor__error" : "set-hint"}>{t(replacementMissing ? "agentPresets.bodyRequired" : "agentPresets.bodyHint")}</span>
      </SettingsField>
      <SettingsField label={t("agentPresets.tools")} hint={t("agentPresets.toolsHint")} stacked>
        <label className="agent-preset-editor__restrict"><input type="checkbox" checked={draft.tools !== null} disabled={busy}
          onChange={event => patch({ tools: event.target.checked ? agentPresetTools(toolText) : null })} />{t("agentPresets.restrictTools")}</label>
        {draft.tools !== null && <>
          <label className="sr-only" htmlFor={`${prefix}-tools`}>{t("agentPresets.toolList")}</label>
          <textarea id={`${prefix}-tools`} className="mem-textarea" rows={4} value={toolText} disabled={busy} spellCheck={false}
            placeholder={"read_file\ngrep\nmcp:research"} onChange={event => setToolText(event.target.value)} aria-describedby={`${prefix}-tools-hint`} />
          <div className="agent-preset-editor__tools-footer">
            <span id={`${prefix}-tools-hint`} className="set-hint" role="status">{t(agentPresetTools(toolText).length ? "agentPresets.toolListHint" : "agentPresets.noTools")}</span>
            <button type="button" className="btn btn--small" disabled={busy || !toolText} onClick={() => setToolText("")}>{t("agentPresets.clearTools")}</button>
          </div>
        </>}
      </SettingsField>
    </SettingsSection>
    <div className="settings-save-bar">
      <span role="status">{t("agentPresets.saveHint")}</span>
      <button className="btn btn--small" type="button" disabled={busy} onClick={onCancel}>{t("common.cancel")}</button>
      <button className="btn btn--primary btn--small" type="submit" disabled={busy || invalid}>{t(busy ? "agentPresets.saving" : "common.save")}</button>
    </div>
  </form>;
}
