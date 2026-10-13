// Run: tsx src/components/TaskMonitorPanel.test.tsx

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import { installDesktopHostStub } from "../__tests__/desktopHostStub";

import type { TaskActionRequest, TaskEventPageRequest, TaskPageRequest } from "../lib/taskCatalogTypes";

type Task = Record<string, unknown>;
type Event = Record<string, unknown>;

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function snap(overrides: Task = {}): Task {
  return {
    schema_version: 1,
    task_id: "task-0001",
    session_id: "sess-1",
    state: "running",
    runtime_state: "alive",
    version: 1,
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T01:00:00Z",
    ...overrides,
  };
}

function taskEvent(overrides: Event = {}): Event {
  return {
    sequence: 1,
    timestamp: "2025-01-01T00:00:01Z",
    event_type: "state_change",
    task_id: "task-0001",
    session_id: "sess-1",
    state: "running",
    runtime_state: "alive",
    ...overrides,
  };
}

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
// Pin the locale source to the JSDOM navigator (en-US): Node's own global
// navigator follows the machine's system language, which flips detectLocale
// to Chinese on zh hosts and breaks the English assertions below.
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.SVGElement = dom.window.SVGElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let listTasksImpl: () => Promise<Task[]> = async () => [];
let listEventsImpl: () => Promise<Event[]> = async () => [];
const listTaskTabIDs: string[] = [];
const pageRequests: TaskPageRequest[] = [];
const listEventCalls: TaskEventPageRequest[] = [];
const stopCalls: TaskActionRequest[] = [];
const requeueCalls: TaskActionRequest[] = [];
const mockApp = {
  ListTaskPage: async (request: TaskPageRequest) => {
    pageRequests.push(request);
    listTaskTabIDs.push(request.tabId);
    const tasks = await listTasksImpl();
    return {
      items: tasks.map((task) => ({ projectKey: "project-a", projectLabel: "Project A", task })),
      nextCursor: "", revision: 1, partial: false, staleCursor: false,
      status: { state: "ready", mode: "memory", revision: 1, indexed: tasks.length, total: tasks.length, pending: 0, failed: 0 },
    };
  },
  ListTaskEventPage: async (request: TaskEventPageRequest) => {
    listEventCalls.push(request);
    const items = await listEventsImpl();
    return { items, nextSequence: items[items.length - 1]?.sequence ?? request.after, partial: false };
  },
  StopTaskByKey: async (request: TaskActionRequest) => {
    stopCalls.push(request);
    return { schema_version: 1, command: "stop", task_id: request.taskId, accepted: true, idempotent: false };
  },
  RequeueTaskByKey: async (request: TaskActionRequest) => {
    requeueCalls.push(request);
    return { schema_version: 1, command: "requeue", task_id: request.taskId, state: "queued", runtime_state: "exited", version: 2, accepted: true, idempotent: false };
  },
  OpenTaskSessionByKey: async () => ({ schema_version: 1, command: "open_session", task_id: "", session_id: "sess-1", accepted: true, idempotent: false }),
};
const defaultListTaskPage = mockApp.ListTaskPage;
installDesktopHostStub(mockApp);

const { TaskMonitorPanel } = await import("./TaskMonitorPanel");

let activeRoot: Root | null = null;
let activeHost: HTMLElement | null = null;

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 25));
}

async function renderPanel(
  onClose?: () => void,
  onOpenSession?: (tabID: string, taskID: string) => Promise<boolean> | boolean,
  tabID = "tab-a",
) {
  activeHost = document.createElement("div");
  document.body.appendChild(activeHost);
  activeRoot = createRoot(activeHost);
  await act(async () => {
    activeRoot?.render(
      <LocaleProvider>
        <TaskMonitorPanel tabID={tabID} onClose={onClose} onOpenSession={onOpenSession} />
      </LocaleProvider>,
    );
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) {
    await act(async () => activeRoot?.unmount());
  }
  activeHost?.remove();
  activeRoot = null;
  activeHost = null;
  listTasksImpl = async () => [];
  listEventsImpl = async () => [];
  listTaskTabIDs.length = 0;
  pageRequests.length = 0;
  mockApp.ListTaskPage = defaultListTaskPage;
  listEventCalls.length = 0;
  stopCalls.length = 0;
  requeueCalls.length = 0;
}

function buttonByLabel(label: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((candidate) => candidate.getAttribute("aria-label") === label);
  if (!button) throw new Error(`missing button: ${label}`);
  return button;
}

function buttonByText(label: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((candidate) => candidate.textContent?.trim() === label);
  if (!button) throw new Error(`missing button text: ${label}`);
  return button;
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click();
    await flush();
  });
}

async function keyDown(element: HTMLElement, key: string) {
  await act(async () => {
    element.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key, bubbles: true }));
    await flush();
  });
}

async function openPanel() {
  await click(buttonByLabel("Expand tasks"));
}

async function check(label: string, run: () => Promise<boolean>) {
  try {
    ok(await run(), label);
  } catch (error) {
    process.stderr.write(`  ERROR ${label}: ${String(error)}\n`);
    ok(false, label);
  } finally {
    await cleanup();
  }
}

console.log("\nTask Monitor panel");

await check("renders the panel header", async () => {
  await renderPanel();
  return document.body.textContent?.includes("Tasks") === true;
});

await check("shows the empty state", async () => {
  await renderPanel();
  await openPanel();
  return document.body.textContent?.includes("No background tasks") === true;
});

await check("shows task-fetch errors", async () => {
  listTasksImpl = async () => { throw new Error("Network error"); };
  await renderPanel();
  await openPanel();
  return document.body.textContent?.includes("Network error") === true;
});

await check("binds task reads to the source tab", async () => {
  listTasksImpl = async () => [snap({ session_id: "sess-current" })];
  await renderPanel(undefined, undefined, "tab-source");
  return listTaskTabIDs.length === 1 && listTaskTabIDs[0] === "tab-source";
});

await check("shows catalog errors returned in the page status", async () => {
  mockApp.ListTaskPage = async (request) => {
    const page = await defaultListTaskPage(request);
    return { ...page, partial: true, status: { ...page.status, lastError: "Catalog unavailable" } };
  };
  await renderPanel();
  await openPanel();
  return document.body.textContent?.includes("Catalog unavailable") === true;
});

await check("keeps colliding task IDs separate across paginated projects", async () => {
  mockApp.ListTaskPage = async (request) => {
    const page = await defaultListTaskPage(request);
    const second = Boolean(request.cursor);
    return {
      ...page,
      items: [{ projectKey: second ? "project-b" : "project-a", projectLabel: second ? "Project B" : "Project A", task: snap({ state: "failed", runtime_state: "exited" }) }],
      nextCursor: second ? "" : "next-project",
    };
  };
  await renderPanel();
  await openPanel();
  const select = document.querySelector<HTMLSelectElement>('select[aria-label="Task scope"]');
  if (!select) throw new Error("missing task scope");
  await act(async () => {
    select.value = "all";
    select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
    await flush();
  });
  await click(buttonByText("Load more"));
  const rows = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-label="Task task-000 — Failed"]'));
  if (rows.length !== 2) return false;
  await click(rows[1]);
  await click(buttonByText("Requeue"));
  return pageRequests.some((request) => request.scope === "all" && request.cursor === "next-project" && request.limit === 50)
    && listEventCalls[0]?.projectKey === "project-b"
    && requeueCalls[0]?.projectKey === "project-b";
});

await check("restarts pagination when the catalog cursor is stale", async () => {
  let refreshed = false;
  mockApp.ListTaskPage = async (request) => {
    const page = await defaultListTaskPage(request);
    if (request.cursor) {
      refreshed = true;
      return { ...page, staleCursor: true };
    }
    return {
      ...page,
      items: [{ projectKey: "project-a", projectLabel: "Project A", task: snap({ task_id: refreshed ? "new-task" : "old-task" }) }],
      nextCursor: refreshed ? "" : "old-cursor",
    };
  };
  await renderPanel();
  await openPanel();
  await click(buttonByText("Load more"));
  return pageRequests.length === 3 && pageRequests[2].cursor === ""
    && Boolean(document.querySelector('button[aria-label="Task new-task — Running"]'))
    && !document.querySelector('button[aria-label="Task old-task — Running"]');
});

await check("renders lifecycle badges", async () => {
  listTasksImpl = async () => [snap({ task_id: "a1" }), snap({ task_id: "b2", state: "failed" })];
  await renderPanel();
  await openPanel();
  const text = document.body.textContent ?? "";
  return text.includes("Running") && text.includes("Failed");
});

await check("separates lifecycle state from runtime liveness", async () => {
  listTasksImpl = async () => [
    snap({ task_id: "failed-1", state: "failed", runtime_state: "exited" }),
    snap({ task_id: "legacy-1", runtime_state: undefined }),
  ];
  await renderPanel();
  await openPanel();
  const text = document.body.textContent ?? "";
  return text.includes("Exited") && text.includes("Runtime unknown");
});

await check("offers one working stop action for active tasks", async () => {
  listTasksImpl = async () => [snap()];
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  const actionLabels = Array.from(document.querySelectorAll<HTMLButtonElement>(".taskmonitor__actions button"))
    .map((button) => button.textContent?.trim());
  const hasMergedActions = actionLabels.filter((label) => label === "Stop").length === 1
    && !actionLabels.includes("Cancel")
    && actionLabels.includes("Open session");
  await click(buttonByText("Stop"));
  const confirmStop = buttonByText("Stop");
  const detailValues = Array.from(document.querySelectorAll<HTMLElement>(".taskmonitor__detail dd"))
    .map((value) => value.textContent?.trim());
  const hasReplacementConfirmation = document.querySelector(".taskmonitor__actions") === null
    && document.activeElement === confirmStop
    && detailValues.includes("Running")
    && !detailValues.includes("running");
  await click(confirmStop);
  return hasMergedActions
    && hasReplacementConfirmation
    && JSON.stringify(stopCalls[0]) === JSON.stringify({ projectKey: "project-a", taskId: "task-0001", expectedVersion: 1, reason: "desktop request", idempotencyKey: "desktop-stop-task-0001-1" });
});

await check("dismisses stop confirmation with Escape and restores focus", async () => {
  listTasksImpl = async () => [snap()];
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Stop"));
  await keyDown(buttonByText("Stop"), "Escape");
  const restoredStop = buttonByText("Stop");
  return document.querySelector(".taskmonitor__confirm") === null
    && document.activeElement === restoredStop
    && stopCalls.length === 0;
});

await check("dismisses stop confirmation when polling observes a terminal task", async () => {
  listTasksImpl = async () => [snap()];
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Stop"));
  listTasksImpl = async () => [snap({ state: "succeeded", runtime_state: "exited", version: 2 })];
  await click(buttonByLabel("Refresh"));
  return document.querySelector(".taskmonitor__confirm") === null
    && document.body.textContent?.includes("Succeeded") === true
    && stopCalls.length === 0;
});

await check("requeues failed exited tasks", async () => {
  listTasksImpl = async () => [snap({ task_id: "failed-1", state: "failed", runtime_state: "exited", version: 7 })];
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task failed-1 — Failed"));
  await click(buttonByText("Requeue"));
  return JSON.stringify(requeueCalls[0]) === JSON.stringify({ projectKey: "project-a", taskId: "failed-1", expectedVersion: 7, reason: "desktop request", idempotencyKey: "desktop-requeue-failed-1-7" });
});

await check("expands and collapses task details", async () => {
  listTasksImpl = async () => [snap({ state: "succeeded" })];
  await renderPanel();
  await openPanel();
  const row = buttonByLabel("Task task-000 — Succeeded");
  await click(row);
  const expanded = document.body.textContent?.includes("Task ID") === true;
  await click(row);
  return expanded && document.body.textContent?.includes("Task ID") !== true;
});

await check("loads recent task events", async () => {
  listTasksImpl = async () => [snap({ state: "failed" })];
  listEventsImpl = async () => [taskEvent({ event_type: "error", error_code: "CRASH" })];
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Failed"));
  return document.body.textContent?.includes("CRASH") === true
    && JSON.stringify(listEventCalls[0]) === JSON.stringify({ projectKey: "project-a", taskId: "task-0001", after: 0, limit: 50 });
});

await check("shows task-event errors", async () => {
  listTasksImpl = async () => [snap()];
  listEventsImpl = async () => { throw new Error("Event failure"); };
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  return document.body.textContent?.includes("Event failure") === true;
});

await check("calls the close callback", async () => {
  let closeCalls = 0;
  await renderPanel(() => { closeCalls += 1; });
  await click(buttonByLabel("Close session summary"));
  return closeCalls === 1;
});

await check("opens the task session through the navigation callback", async () => {
  listTasksImpl = async () => [snap()];
  let openedTarget: string[] = [];
  await renderPanel(undefined, async (tabID, taskID) => {
    openedTarget = [tabID, taskID];
    return true;
  });
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Open session"));
  return JSON.stringify(openedTarget) === JSON.stringify(["tab-a", "task-0001"]);
});

await check("does not close the panel for a stale open completion", async () => {
  listTasksImpl = async () => [snap()];
  let closeCalls = 0;
  await renderPanel(() => { closeCalls += 1; }, async () => false);
  await openPanel();
  await click(buttonByLabel("Task task-000 — Running"));
  await click(buttonByText("Open session"));
  return closeCalls === 0;
});

await check("refreshes tasks on request", async () => {
  let calls = 0;
  listTasksImpl = async () => (++calls === 1 ? [] : [snap({ task_id: "ok" })]);
  await renderPanel();
  await openPanel();
  await click(buttonByLabel("Refresh"));
  return document.body.textContent?.includes("ok") === true;
});

await check("shows the task count", async () => {
  listTasksImpl = async () => [snap({ task_id: "a" }), snap({ task_id: "b" })];
  await renderPanel();
  return document.querySelector(".taskmonitor__count")?.textContent === "2";
});

await check("marks only terminal tasks", async () => {
  listTasksImpl = async () => [snap({ task_id: "t1", state: "succeeded" }), snap({ task_id: "t2" })];
  await renderPanel();
  await openPanel();
  return document.querySelectorAll(".taskmonitor__terminal").length === 1;
});

await check("freezes terminal task elapsed time", async () => {
  const realNow = Date.now;
  try {
    Date.now = () => Date.parse("2025-01-01T02:00:00Z");
    listTasksImpl = async () => [snap({ state: "cancelled", runtime_state: "exited" })];
    await renderPanel();
    await openPanel();
    const atFinish = document.querySelector(".taskmonitor__time")?.textContent;
    Date.now = () => Date.parse("2025-01-01T03:00:00Z");
    await click(buttonByLabel("Refresh"));
    const afterRefresh = document.querySelector(".taskmonitor__time")?.textContent;
    return atFinish === "1h" && afterRefresh === "1h";
  } finally {
    Date.now = realNow;
  }
});

await check("uses the expired runtime lease for stale task elapsed time", async () => {
  const realNow = Date.now;
  try {
    Date.now = () => Date.parse("2025-01-01T02:00:00Z");
    listTasksImpl = async () => [snap({
      state: "stale",
      runtime_state: "exited",
      updated_at: "2025-01-01T00:00:00Z",
      runtime_lease_until: "2025-01-01T00:30:00Z",
    })];
    await renderPanel();
    await openPanel();
    const atDetection = document.querySelector(".taskmonitor__time")?.textContent;
    Date.now = () => Date.parse("2025-01-01T03:00:00Z");
    await click(buttonByLabel("Refresh"));
    const afterRefresh = document.querySelector(".taskmonitor__time")?.textContent;
    return atDetection === "30m" && afterRefresh === "30m";
  } finally {
    Date.now = realNow;
  }
});

await check("does not present requeue age as elapsed runtime", async () => {
  listTasksImpl = async () => [snap({ state: "queued", runtime_state: "exited" })];
  await renderPanel();
  await openPanel();
  return document.querySelector(".taskmonitor__time")?.textContent === "—";
});

dom.window.close();
console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
