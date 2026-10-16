// Production macOS package: current session history stays readable when
// an external writer prevents execution recovery. Fixtures never call a model.
// Usage: node desktop/packaging/history-current-smoke.mjs /path/Deepzora.app
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createRequire } from "node:module";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { packagedSmokeEnv } from "./smoke-env.mjs";
import { parseServiceReady, waitForSmokeCondition } from "./smoke-poll.mjs";
import { closeAndVerify } from "./smoke-lifecycle.mjs";

assert.equal(process.platform, "darwin", "This fixture exercises the macOS production bundle");
const require = createRequire(new URL("../electron/package.json", import.meta.url));
const { _electron } = require("playwright");
const bundle = resolve(process.argv[2]);
const home = mkdtempSync(join(tmpdir(), "deepzora-current-history-"));
writeFileSync(join(home, "config.toml"), `default_model = "fixture/model"\n[desktop]\nprovider_access = ["fixture"]\n[[providers]]\nname = "fixture"\nkind = "openai"\nbase_url = "http://127.0.0.1:1/v1"\nmodels = ["model"]\ndefault = "model"\napi_key_env = "HISTORY_FIXTURE_KEY"\n`);
const answer = "完整内容🧭".repeat(90000);
let fixtures;
let application, lease, page;
let passed = false;
try {
  // This separate current-session process holds real writer locks.
  lease = spawn("go", ["run", "./cmd/history-fixture"], {
    cwd: fileURLToPath(new URL("../", import.meta.url)),
    env: packagedSmokeEnv(process.env, home), stdio: ["pipe", "pipe", "inherit"],
  });
  const output = createInterface({ input: lease.stdout });
  const [line] = await Promise.race([once(output, "line"), once(lease, "exit").then(() => { throw new Error("fixture writer exited before readiness"); })]);
  output.close();
  fixtures = JSON.parse(line).map(fixture => ({ ...fixture,
    body: readFileSync(join(fixture.path, "events.frames")), manifest: readFileSync(join(fixture.path, "manifest.json")) }));
  application = await _electron.launch({ executablePath: join(bundle, "Contents/MacOS/Deepzora"),
    env: { ...packagedSmokeEnv(process.env, home), HISTORY_FIXTURE_KEY: "local-fixture" } });
  await waitForSmokeCondition(async () => {
    for (const candidate of application.windows()) {
      if (await candidate.evaluate(() => Boolean(window.deepzoraDesktop)).catch(() => false)) { page = candidate; return true; }
    }
    return false;
  });
  const invoke = (method, args = []) => page.evaluate(({ method, args }) => window.deepzoraDesktop.invoke(method, args), { method, args });
  const build = JSON.parse(readFileSync(join(bundle, "Contents/Resources/build.json"), "utf8"));
  assert.equal(await invoke("Version"), build.version);
  assert.equal(await application.evaluate(({ app }) => app.isPackaged && !process.env.DEEPZORA_DEV), true);
  const folder = page.locator(".project-tree__folder-main").first();
  await folder.waitFor();
  if (await folder.getAttribute("aria-expanded") !== "true") await folder.click();
  for (const fixture of fixtures) {
    await page.locator(".project-tree__topic-main").filter({ has: page.getByText(fixture.kind, { exact: true }) }).click();
    let selected;
    await waitForSmokeCondition(async () => {
      selected = (await invoke("ListTabs")).find(tab => tab.active && tab.sessionId === fixture.id);
      return Boolean(selected);
    });
    const ticket = { tabId: selected.id };
    await page.waitForFunction(kind => document.querySelector(".chat-transcript")?.textContent?.includes(`CURRENT_${kind}_USER_139`), fixture.kind);
    const visible = await page.locator(".chat-transcript").innerText();
    for (const other of fixtures.filter(other => other !== fixture)) {
      assert.equal(visible.includes(`CURRENT_${other.kind}_USER_`), false, "previous session remained visible");
    }
    const composer = page.locator("#composer-input");
    await composer.fill("Unsaved draft while the external writer owns execution");
    assert.equal(await page.locator(".composer__btn--send").isEnabled(), false);
    await composer.fill("");
    const handle = await invoke("BeginSessionHistoryReadForTab", [ticket.tabId]);
    let newest;
    await waitForSmokeCondition(async () => {
      newest = await invoke("ReadSessionHistoryWindow", [handle.id, { anchor: "newest", limit: 32 }]);
      assert.ok(["preparing", "ready"].includes(newest.status));
      return newest.status === "ready";
    });
    assert.equal(newest.hasOlder, true);
    assert.equal(newest.messages.length <= 32, true);
    const outline = await invoke("ReadSessionHistoryOutline", [handle.id, { limit: 128 }]);
    assert.equal(outline.status, "ready");
    assert.equal(outline.entries.length, 128);
    assert.equal(outline.totalTurns, 140);
    const target = outline.entries[3];
    const location = await invoke("LocateSessionHistoryMessage", [handle.id, target.messageId, outline.snapshotSequence]);
    assert.equal(location.status, "ready");
    const located = await invoke("ReadSessionHistoryWindow", [handle.id, { anchor: "cursor", cursor: location.cursor, limit: 2, generation: outline.generation, snapshotSequence: outline.snapshotSequence }]);
    assert.equal(located.status, "ready");
    assert.equal(located.messages.at(-1).messageId, target.messageId);
    let search;
    await waitForSmokeCondition(async () => {
      search = await invoke("SearchSessionHistoryRead", [handle.id, `CURRENT_${fixture.kind}_USER_`, "", 2]);
      assert.ok(["preparing", "ready"].includes(search.status));
      if (search.status === "preparing") assert.equal(search.hits.length, 0);
      return search.status === "ready";
    });
    assert.equal(search.hits.length, 2);
    assert.equal(search.hasMore, true);
    assert.equal(search.hits[0].preview, `CURRENT_${fixture.kind}_USER_139`);
    const searchLocation = await invoke("LocateSessionHistoryMessage", [handle.id, search.hits[0].messageId, search.snapshotSequence]);
    assert.equal(searchLocation.status, "ready");
    const nextSearch = await invoke("SearchSessionHistoryRead", [handle.id, `CURRENT_${fixture.kind}_USER_`, search.nextCursor, 2]);
    assert.equal(nextSearch.status, "ready");
    assert.equal(nextSearch.hits[0].preview, `CURRENT_${fixture.kind}_USER_137`);
    const ref = newest.messages.find(message => message.contentRef)?.contentRef;
    assert.ok(ref);
    const chunks = [];
    for (let offset = 0; ;) {
      const chunk = await invoke("ReadSessionHistoryContent", [handle.id, ref, offset]);
      chunks.push(Buffer.from(chunk.data ?? "", "base64"));
      if (chunk.done) break;
      assert.ok(chunk.nextOffset > offset);
      offset = chunk.nextOffset;
    }
    assert.equal(JSON.parse(Buffer.concat(chunks).toString("utf8")).content, answer);
    await invoke("ReleaseSessionHistoryRead", [handle.id]);
    await assert.rejects(invoke("ReadSessionHistoryContent", [handle.id, ref, 0]));
    const reopen = await invoke("BeginSessionHistoryReadForTab", [ticket.tabId]);
    assert.equal((await invoke("ReadSessionHistoryWindow", [reopen.id, { anchor: "newest", limit: 2 }])).status, "ready");
    await assert.rejects(invoke("ReadSessionHistoryContent", [handle.id, ref, 0]));
    await invoke("ReleaseSessionHistoryRead", [reopen.id]);
    await waitForSmokeCondition(async () => (await invoke("ListTabs")).find(tab => tab.id === ticket.tabId)?.runtime?.phase === "lease_blocked");
    assert.deepEqual(readFileSync(join(fixture.path, "events.frames")), fixture.body);
    assert.deepEqual(readFileSync(join(fixture.path, "manifest.json")), fixture.manifest);
    console.log(`PASS ${fixture.kind}: current paging, outline, search, Unicode content, canceled readers and external writer exclusion; source unchanged`);
  }
  await waitForSmokeCondition(async () => {
    const topics = await invoke("ListProjectTopics", [{ scope: "global", limit: 50 }]);
    assert.equal(topics.failedDirectories, 0, "optional absent history roots reported as failed");
    return topics.complete && topics.pendingDirectories === 0;
  });
  const workspace = { scope: "global" };
  let organization = await invoke("GetSessionOrganization", [workspace]);
  const mutate = async mutation => {
    // Match the production client's current organization CAS contract.
    for (let attempt = 0; attempt < 3; attempt++) {
      organization = await invoke("UpdateSessionOrganization", [workspace, organization.revision, mutation]);
      if (organization.applied) return;
    }
    assert.fail("organization mutation could not settle under the production CAS contract");
  };
  await mutate({ kind: "create-group", groupId: "current-fixtures", title: "Current fixtures" });
  const sources = (await invoke("ListProjectTopics", [{ scope: "global", limit: 50 }])).items
    .filter(node => fixtures.some(fixture => fixture.id === node.session?.sessionId));
  assert.equal(sources.length, 2);
  for (const node of sources) await mutate({ kind: "set-group", groupId: "current-fixtures", target: { sessionId: node.session.sessionId } });
  const filtered = await invoke("ListProjectTopics", [{ scope: "global", groupFilter: "group", groupId: "current-fixtures", query: "ALPHA", limit: 1 }]);
  assert.equal(filtered.items.length, 1);
  assert.equal(filtered.items[0].session.sessionId, fixtures.find(fixture => fixture.kind === "alpha").id);
  assert.equal(filtered.nextCursor ?? "", "");
  await invoke("ReleaseReadSnapshot", [filtered.snapshotId]);
  const groupedRequest = { scope: "global", groupFilter: "group", groupId: "current-fixtures", limit: 1 };
  const first = await invoke("ListProjectTopics", [groupedRequest]);
  assert.equal(first.items.length, 1);
  assert.ok(first.nextCursor);
  const removed = sources.find(node => node.sessionPath !== first.items[0].sessionPath);
  await mutate({ kind: "set-group", groupId: "", target: { sessionId: removed.session.sessionId } });
  const retained = await invoke("ListProjectTopics", [{ ...groupedRequest, cursor: first.nextCursor }]);
  assert.equal(retained.items.length, 1);
  assert.equal(retained.items[0].sessionPath, removed.sessionPath);
  assert.equal(retained.nextCursor ?? "", "");
  await invoke("ReleaseReadSnapshot", [first.snapshotId]);
  const fresh = await invoke("ListProjectTopics", [groupedRequest]);
  assert.equal(fresh.items.length, 1);
  assert.equal(fresh.items[0].sessionPath, first.items[0].sessionPath);
  assert.equal(fresh.nextCursor ?? "", "");
  await invoke("ReleaseReadSnapshot", [fresh.snapshotId]);
  console.log("PASS current group paging: source membership, text filtering, retained cursor, refreshed membership");
  const ready = parseServiceReady(readFileSync(join(home, "desktop-shell/logs/shell.log"), "utf8"));
  assert.ok(ready);
  await closeAndVerify(application, { shellPid: await application.evaluate(() => process.pid), servicePid: ready.pid });
  application = undefined;
  console.log(`PASS production history ${build.version} ${build.commit ?? ""}: normal shell/service exit`);
  passed = true;
} catch (error) {
  if (page) {
    await page.screenshot({ path: join(home, "failure.png"), fullPage: true }).catch(() => {});
    const state = await page.evaluate(async () => ({ body: document.body.innerText,
      tabs: await window.deepzoraDesktop.invoke("ListTabs", []),
      tree: await window.deepzoraDesktop.invoke("GetProjectTreeSnapshot", []),
      topics: await window.deepzoraDesktop.invoke("ListProjectTopics", [{ scope: "global", limit: 50 }]),
    })).catch(error => ({ error: String(error) }));
    writeFileSync(join(home, "failure.json"), JSON.stringify(state, null, 2));
  }
  throw error;
} finally {
  await application?.close();
  if (lease && lease.exitCode === null) { const exited = once(lease, "exit"); lease.stdin.end(); await exited; }
  if (passed) rmSync(home, { recursive: true, force: true });
  else console.error(`Current history evidence retained at ${home}`);
}
