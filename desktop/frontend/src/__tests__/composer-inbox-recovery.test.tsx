import { act } from "react";
import { installDom, installBridgeApp, renderComposer } from "./composerInboxHarness";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

const flush = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

async function waitFor(label: string, check: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    if (check()) return;
    await act(flush);
  }
  ok(false, label);
}

function recoveredSnapshot(count = 3) {
  const items = Array.from({ length: count }, (_, index) => ({
    id: `recovered-${index + 1}`,
    intent: "followup",
    state: "uncertain",
    preview: `Recovered instruction ${index + 1}`,
    byteSize: 128,
    position: index + 1,
  }));
  return {
    mutationsSupported: true,
    revision: 1,
    paused: true,
    recovered: true,
    recoveredCount: count,
    sessionPath: "session-a",
    items,
    itemsCount: items.length,
    bytes: items.length * 128,
    maxItems: 64,
    maxBytes: 64 * 1024 * 1024,
  };
}

console.log("\ncomposer inbox recovery");

{
  const dom = installDom();
  let snapshot = recoveredSnapshot();
  let rejectPause = true;
  const pauseCalls: boolean[] = [];
  installBridgeApp({
    InboxSnapshot: async () => snapshot,
    InboxQueueForTarget: async (target: { sessionPath: string }, request: { kind: string; paused: boolean }) => {
      ok(target.sessionPath === "session-a" && request.kind === "pause", "pause mutation uses the captured session target");
      pauseCalls.push(request.paused);
      if (rejectPause) return { outcome: "unavailable", reason: "stale_target", snapshot };
      snapshot = { ...snapshot, paused: request.paused, recovered: request.paused && snapshot.recovered };
      return { outcome: "applied", snapshot };
    },
  });
  const { root } = await renderComposer();

  await waitFor("recovery banner rendered", () => document.querySelector(".composer-inbox-recovery") !== null);
  ok(document.querySelector(".composer-inbox-recovery")?.textContent?.includes("Recovered 3 pending instructions") === true, "banner reports the recovered count");
  ok(document.querySelectorAll(".inbox-queue__row").length === 2, "queue starts with a bounded preview");

  const review = document.querySelector(".composer-inbox-recovery .btn") as HTMLButtonElement;
  await act(async () => { review.click(); await flush(); });
  ok(document.querySelectorAll(".inbox-queue__row").length === 3, "review expands the recovered queue");

  const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>(".composer-inbox-recovery .btn"));
  await act(async () => { buttons[2].click(); await flush(); });
  ok(!buttons[2].disabled && buttons[2].textContent === "Keep paused", "rejected stale-target mutation is not shown as a confirmed decision");
  rejectPause = false;
  await act(async () => { buttons[2].click(); await flush(); });
  ok(pauseCalls.at(-1) === true, "keep paused records the current queue decision");
  ok(buttons[2].disabled && buttons[2].textContent === "Paused", "keep paused is visible and idempotent");

  await act(async () => { buttons[1].click(); await flush(); });
  ok(pauseCalls.at(-1) === false, "continue resumes the durable inbox");
  await waitFor("resumed banner removed", () => document.querySelector(".composer-inbox-recovery") === null);

  await act(async () => root.unmount());
  dom.window.close();
}

process.stdout.write(`\ncomposer inbox recovery: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
