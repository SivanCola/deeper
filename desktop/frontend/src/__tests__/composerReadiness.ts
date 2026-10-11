import { act } from "react";

export async function waitForComposerReadiness(label: string, predicate: () => boolean) {
  // Completion includes a 120ms debounce and a React commit; zero-delay
  // event-loop turns do not bound either phase under concurrent CI load.
  const deadline = performance.now() + 2000;
  while (performance.now() < deadline) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}
