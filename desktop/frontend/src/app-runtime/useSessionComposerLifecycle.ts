import { useEffect } from "react";

declare global {
  interface Window {
    __deeperFlushSessionDraft?: () => Promise<void>;
    __deeperResumeSessionDraftEditing?: () => void;
  }
}

/** The shell's historical callback names now belong only to formal input. */
export function useSessionComposerLifecycle() {
  useEffect(() => {
    const flush = async () => {
      const input = await import("../lib/sessionComposerPersistence");
      try {
        await input.flushAllSessionComposers();
      } catch (error) {
        input.resumeSessionComposerEditing();
        throw error;
      }
    };
    const resume = () => {
      void import("../lib/sessionComposerPersistence").then(input => input.resumeSessionComposerEditing());
    };
    window.__deeperFlushSessionDraft = flush;
    window.__deeperResumeSessionDraftEditing = resume;
    return () => {
      if (window.__deeperFlushSessionDraft === flush) delete window.__deeperFlushSessionDraft;
      if (window.__deeperResumeSessionDraftEditing === resume) delete window.__deeperResumeSessionDraftEditing;
    };
  }, []);
}
