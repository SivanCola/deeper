import { useEffect } from "react";

declare global {
  interface Window {
    __deepzoraFlushComposerInput?: () => Promise<void>;
    __deepzoraResumeComposerEditing?: () => void;
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
    window.__deepzoraFlushComposerInput = flush;
    window.__deepzoraResumeComposerEditing = resume;
    return () => {
      if (window.__deepzoraFlushComposerInput === flush) delete window.__deepzoraFlushComposerInput;
      if (window.__deepzoraResumeComposerEditing === resume) delete window.__deepzoraResumeComposerEditing;
    };
  }, []);
}
