import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { _electron } from "playwright";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const home = mkdtempSync(join(tmpdir(), "deepzora-agent-presets-"));
const workspace = join(home, "workspace");
mkdirSync(workspace);
const requests = [];
const providerURLs = [];
const provider = createServer(async (request, response) => {
  providerURLs.push(request.url);
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  requests.push(JSON.parse(Buffer.concat(chunks).toString()));
  response.writeHead(200, { "Content-Type": "text/event-stream" });
  response.end('data: {"choices":[{"delta":{"content":"PRESET_NATIVE_REPLY"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n');
});
await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
writeFileSync(join(home, "config.toml"), `default_model = "preset-smoke/test"
[desktop]
language = "en"
[[providers]]
name = "preset-smoke"
kind = "openai"
base_url = "http://127.0.0.1:${provider.address().port}/v1"
models = ["test"]
api_key_env = "DEEPZORA_PRESET_SMOKE_KEY"
`);
const report = { checks: [], errors: [] };
const check = name => { report.checks.push(name); console.log(`PASS ${name}`); };
const invoke = (page, name, args = []) => page.evaluate(({ name, args }) => window.deepzoraDesktop.invoke(name, args), { name, args });
async function until(predicate) {
  const deadline = Date.now() + 60000;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error("Agent preset smoke condition timed out");
}
let shell, page, tab;
async function launch() {
  shell = await _electron.launch({ executablePath: process.env.DEEPZORA_TEST_ELECTRON,
    args: [root, `--user-data-dir=${join(home, "electron")}`], env: {
      ...process.env, DEEPZORA_HOME: home, DEEPZORA_STATE_HOME: home, DEEPZORA_CACHE_HOME: join(home, "cache"),
      DEEPZORA_PRESET_SMOKE_KEY: "disposable-preset-fixture", DEEPZORA_DEV: "1",
      DEEPZORA_DESKTOP_SERVICE: process.env.DEEPZORA_DESKTOP_SERVICE || resolve(root, "../build/bin/deepzora-desktop-service" + (process.platform === "win32" ? ".exe" : "")),
    }, timeout: 60000 });
  page = await shell.firstWindow();
  page.on("pageerror", error => report.errors.push(error.message));
  await page.waitForURL("deepzora://app/index.html", { waitUntil: "domcontentloaded", timeout: 60000 });
  await page.waitForFunction(() => Boolean(window.deepzoraDesktop) && !document.querySelector(".boot-shell"), null, { timeout: 60000 });
  await until(async () => { try { await invoke(page, "Version"); return true; } catch { return false; } });
}
async function settings() {
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.getByRole("navigation", { name: "Settings", exact: true }).getByRole("button", { name: "Agent presets", exact: true }).click();
  await page.locator(".agent-presets").waitFor();
}
async function composer() {
  await page.locator(".settings-screen .management-screen__back").click();
  await page.locator("#composer-input").waitFor({ state: "visible" });
}
const row = name => page.getByRole("article", { name, exact: true });
const editor = () => page.locator(".agent-preset-editor");
try {
  await launch();
  await invoke(page, "SetDesktopLanguage", ["en"]);
  const operationId = "agent-preset-native-smoke";
  await invoke(page, "BeginManualSessionCreation", [{ operationId, scope: "project", workspaceRoot: workspace }]);
  await until(async () => {
    const creation = await invoke(page, "GetManualSessionCreation", [operationId]);
    if (creation.phase === "failed") throw new Error(creation.error);
    return creation.phase === "ready";
  });
  const creation = await invoke(page, "GetManualSessionCreation", [operationId]);
  report.session = creation.ref;
  tab = (await invoke(page, "ListTabs")).find(item => item.sessionId === creation.ref.sessionId);
  assert.ok(tab);
  await invoke(page, "SetActiveTab", [tab.id]);
  await page.reload();
  await page.locator("#composer-input").waitFor({ state: "visible", timeout: 60000 });
  await settings();
  await row("Default").waitFor();
  assert.equal(await row("Default").getByRole("button", { name: "Edit", exact: true }).count(), 0);
  assert.equal(await row("Default").getByRole("button", { name: "Delete", exact: true }).count(), 0);
  await row("Default").getByRole("button", { name: "Copy", exact: true }).click();
  assert.equal(await editor().getByLabel("ID", { exact: true }).inputValue(), "default-copy");
  await editor().getByLabel("Name", { exact: true }).fill("Native reviewer");
  await editor().getByLabel("ID", { exact: true }).fill("native-reviewer");
  await editor().getByLabel("Description", { exact: true }).fill("Native preset acceptance");
  await editor().getByLabel("Persona body", { exact: true }).fill("NATIVE_PERSONA_FIRST");
  await editor().getByLabel("Restrict available tools", { exact: true }).check();
  await editor().getByRole("button", { name: "Save", exact: true }).click();
  await row("Native reviewer").waitFor();
  let list = await invoke(page, "ListAgentPresets", [tab.id]);
  assert.deepEqual(list.presets.find(item => item.id === "native-reviewer").tools, []);
  assert.match(readFileSync(join(home, "presets/native-reviewer.md"), "utf8"), /NATIVE_PERSONA_FIRST/);
  check("read-only Default copies to a real user preset and preserves an empty tool allowlist");

  await row("Native reviewer").getByRole("button", { name: "Edit", exact: true }).click();
  await editor().getByLabel("Restrict available tools", { exact: true }).uncheck();
  await editor().getByRole("button", { name: "Save", exact: true }).click();
  await row("Native reviewer").waitFor();
  list = await invoke(page, "ListAgentPresets", [tab.id]);
  assert.equal(list.presets.find(item => item.id === "native-reviewer").tools, null);
  await row("Native reviewer").getByRole("button", { name: "Edit", exact: true }).click();
  await editor().getByLabel("Restrict available tools", { exact: true }).check();
  await editor().getByRole("button", { name: "Save", exact: true }).click();
  await row("Native reviewer").waitFor();
  check("editing preserves the distinction between unrestricted tools and an empty allowlist");

  await row("Native reviewer").getByRole("button", { name: "Copy", exact: true }).click();
  await editor().getByLabel("Name", { exact: true }).fill("Project reviewer");
  await editor().getByLabel("ID", { exact: true }).fill("project-reviewer");
  await editor().getByRole("button", { name: "Save to", exact: true }).click();
  await page.getByRole("option", { name: "Project", exact: true }).click();
  await editor().getByRole("button", { name: "Save", exact: true }).click();
  await row("Project reviewer").waitFor();
  assert.match(readFileSync(join(workspace, ".deepzora/presets/project-reviewer.md"), "utf8"), /NATIVE_PERSONA_FIRST/);
  await row("Project reviewer").getByRole("button", { name: "Delete", exact: true }).click();
  await row("Project reviewer").getByRole("button", { name: "Delete Project reviewer?", exact: true }).click();
  await row("Project reviewer").waitFor({ state: "detached" });
  check("project scope writes to the workspace and delete removes the selected definition");
  await page.screenshot({ path: join(home, "preset-settings.png") });
  await composer();
  await page.getByRole("button", { name: "Agent preset: Default", exact: true }).click();
  await page.getByRole("menuitemradio", { name: /Native reviewer/ }).click();
  await page.getByRole("button", { name: "Agent preset: Native reviewer", exact: true }).waitFor();
  list = await invoke(page, "ListAgentPresets", [tab.id]);
  assert.equal(list.current, "native-reviewer");
  assert.equal(list.locked, false);
  check("composer selection publishes the preset through the real Go controller");
  await page.locator("#composer-input").fill("Native preset first turn");
  await page.locator(".composer__btn--send").click();
  await until(async () => requests.length >= 1 && (await invoke(page, "ListAgentPresets", [tab.id])).locked);
  await page.getByText("PRESET_NATIVE_REPLY", { exact: true }).first().waitFor();
  await until(async () => !(await invoke(page, "ListTabs")).find(item => item.id === tab.id)?.running);
  const locked = page.getByRole("button", { name: /^(Agent preset|智能体预设): Native reviewer · (Locked|已锁定)$/ });
  await locked.waitFor();
  assert.equal(await locked.isDisabled(), true);
  const first = requests[0];
  assert.match(JSON.stringify(first.messages.filter(item => item.role === "system")), /NATIVE_PERSONA_FIRST/);
  assert.deepEqual((first.tools ?? []).map(item => item.function.name), ["use_capability"]);
  check("first native turn sends the selected persona and admitted schemas, then locks the picker");
  await settings();
  await row("Native reviewer").getByRole("button", { name: "Edit", exact: true }).click();
  await editor().getByLabel("Persona body", { exact: true }).fill("NATIVE_PERSONA_LATEST");
  await editor().getByRole("button", { name: "Save", exact: true }).click();
  await row("Native reviewer").waitFor();
  await composer();
  await page.locator("#composer-input").fill("Native preset second turn");
  await page.locator(".composer__btn--send").click();
  await until(async () => requests.length >= 2);
  await until(async () => !(await invoke(page, "ListTabs")).find(item => item.id === tab.id)?.running);
  assert.deepEqual(requests[1].messages.filter(item => item.role === "system"), first.messages.filter(item => item.role === "system"));
  assert.deepEqual(requests[1].tools, first.tools);
  check("editing a definition leaves the running session's provider prefix and schemas stable");
  await page.screenshot({ path: join(home, "preset-locked.png") });
  await shell.close();
  shell = null;
  await launch();
  await until(async () => (await invoke(page, "ListTabs")).some(item => item.sessionId === creation.ref.sessionId && item.ready));
  tab = (await invoke(page, "ListTabs")).find(item => item.sessionId === creation.ref.sessionId);
  await invoke(page, "SetActiveTab", [tab.id]);
  await page.reload();
  await page.locator("#composer-input").waitFor({ state: "visible", timeout: 60000 });
  await page.locator("#composer-input").fill("Native restored preset turn");
  await page.locator(".composer__btn--send").click();
  await until(async () => requests.length >= 3);
  const restored = JSON.stringify(requests[2].messages.filter(item => item.role === "system"));
  assert.match(restored, /NATIVE_PERSONA_LATEST/);
  assert.doesNotMatch(restored, /NATIVE_PERSONA_FIRST/);
  assert.equal((await invoke(page, "ListAgentPresets", [tab.id])).current, "native-reviewer");
  await until(async () => !(await invoke(page, "ListTabs")).find(item => item.id === tab.id)?.running);
  await page.screenshot({ path: join(home, "preset-restored.png") });
  assert.equal(report.errors.length, 0, report.errors.join("\n"));
  check("cold app restart restores the recorded preset ID using the latest definition");
} catch (error) {
  report.failure = String(error);
  report.providerURLs = providerURLs;
  report.requestCount = requests.length;
  if (page && !page.isClosed()) {
    report.input = await page.locator("#composer-input").inputValue().catch(String);
    report.submit = await page.locator(".composer__btn--send").evaluate(el => ({ disabled: el.disabled, label: el.getAttribute("aria-label"), title: el.title })).catch(String);
    report.runtime = await invoke(page, "GetRuntimeStateSnapshot").catch(String);
    report.tabs = await invoke(page, "ListTabs").catch(String);
    report.inbox = tab ? await invoke(page, "InboxSnapshot", [tab.id]).catch(String) : null;
    await page.screenshot({ path: join(home, "preset-failure.png") }).catch(() => {});
    report.dom = await page.locator("body").innerText().catch(() => "");
  }
  throw error;
} finally {
  if (shell) await shell.close();
  provider.closeAllConnections();
  await new Promise(resolve => provider.close(resolve));
  report.providerURLs = providerURLs;
  report.requestCount = requests.length;
  writeFileSync(join(home, "preset-smoke-results.json"), JSON.stringify(report, null, 2));
  console.log(`Evidence: ${home}`);
}
