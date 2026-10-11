import { useEffect } from "react";

declare global {
  interface Window {
    __deepzoraFlushSessionDraft?: () => Promise<void>;
    __deepzoraResumeSessionDraftEditing?: () => void;
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
    window.__deepzoraFlushSessionDraft = flush;
    window.__deepzoraResumeSessionDraftEditing = resume;
    return () => {
      if (window.__deepzoraFlushSessionDraft === flush) delete window.__deepzoraFlushSessionDraft;
      if (window.__deepzoraResumeSessionDraftEditing === resume) delete window.__deepzoraResumeSessionDraftEditing;
    };
  }, []);
}
