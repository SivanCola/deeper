import { useCallback, useEffect, useRef, useState } from "react";
import { app, onRemoteTabUpdated, onTabMeta } from "./bridge";
import { AGENT_PRESETS_CHANGED, agentPresetError, type AgentPresetsView } from "./agentPresets";

export function useAgentPresets(tabId: string, refreshKey = "") {
  const scopeKey = `${tabId}\u0000${refreshKey}`;
  const [state, setState] = useState<{ scopeKey: string; view: AgentPresetsView | null; loading: boolean; error: string | null }>({ scopeKey, view: null, loading: true, error: null });
  const request = useRef(0);
  const reload = useCallback(async () => {
    const sequence = ++request.current;
    setState(previous => ({ scopeKey, view: previous.scopeKey === scopeKey ? previous.view : null, loading: true, error: null }));
    try {
      const next = await app.ListAgentPresets(tabId);
      if (request.current !== sequence) return;
      setState({ scopeKey, view: { ...next, presets: next.presets ?? [], problems: next.problems ?? [], current: next.current || "default" }, loading: false, error: null });
    } catch (cause) {
      if (request.current === sequence) setState(previous => ({ ...previous, loading: false, error: agentPresetError(cause) }));
    }
  }, [tabId, scopeKey]);

  useEffect(() => {
    void reload();
    const refresh = () => { void reload(); };
    const offMeta = onTabMeta(event => { if (!tabId || event.tabId === tabId) refresh(); });
    const offRemote = onRemoteTabUpdated(tab => { if (!tabId || tab.id === tabId) refresh(); });
    window.addEventListener(AGENT_PRESETS_CHANGED, refresh);
    window.addEventListener("focus", refresh);
    return () => {
      request.current += 1;
      offMeta();
      offRemote();
      window.removeEventListener(AGENT_PRESETS_CHANGED, refresh);
      window.removeEventListener("focus", refresh);
    };
  }, [tabId, refreshKey, reload]);
  return { view: state.scopeKey === scopeKey ? state.view : null, loading: state.scopeKey !== scopeKey || state.loading, error: state.scopeKey === scopeKey ? state.error : null, reload };
}
