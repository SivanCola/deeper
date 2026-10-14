// Exercise the production shell with a fresh, isolated home, including layout
// restoration across normal quits. No real credentials or model requests.
// node desktop/packaging/first-launch-smoke.mjs <Deepzora.app> <evidence-dir>
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { packagedSmokeEnv } from "./smoke-env.mjs";

const require = createRequire(new URL("../electron/package.json", import.meta.url));
const { _electron } = require("playwright");
const [bundleArg, outputArg, ...options] = process.argv.slice(2);
const keepWorkspaceOpen = options.includes("--open-before-setup");
const theme = options.includes("--dark") ? "dark" : "light";
if (!bundleArg || !outputArg) throw new Error("usage: first-launch-smoke.mjs <Deepzora.app> <evidence-dir>");
const bundle = resolve(bundleArg);
const output = resolve(outputArg);
const home = join(output, "home");
mkdirSync(output, { recursive: true });
mkdirSync(home, { recursive: false });
writeFileSync(join(home, "config.toml"), `[desktop]\nlanguage = "zh"\ntheme = "${theme}"\ntheme_style = "graphite"\ncheck_updates = false\ntelemetry = false\nmetrics = false\n`);
const env = packagedSmokeEnv(process.env, home);
const executable = join(bundle, "Contents/MacOS/Deepzora");
let application;
let page;
const errors = [];
const evidence = [];
const pass = (message) => { evidence.push(message); console.log(`PASS  ${message}`); };
const invoke = (method, args = []) => page.evaluate(([method, args]) => window.deepzoraDesktop.invoke(method, args), [method, args]);
const dock = () => page.locator(".topicbar__chrome-btn--workspace");
const shot = (name) => page.screenshot({ path: join(output, name + ".png") });
async function launch() {
  application = await _electron.launch({ executablePath: executable, args: [], env, timeout: 60000 });
  await application.evaluate(({ app }) => { if (!app.isPackaged) throw new Error("must run the packaged app"); });
  for (let retry = 0; retry < 240; retry++) {
    for (const window of application.windows()) {
      try {
        if (await window.locator(".topicbar__chrome-btn--workspace").isVisible()) { page = window; break; }
      } catch { /* starting page may be replaced */ }
    }
    if (page && !page.isClosed()) break;
    await new Promise(resolve => setTimeout(resolve, 250));
  }
  assert(page && !page.isClosed(), "production workspace must become visible");
  page.on("pageerror", error => errors.push(error.message));
  await application.evaluate(({ BrowserWindow }) => {
    const window = BrowserWindow.getAllWindows().find(window => !window.isDestroyed() && window.isVisible());
    window.setContentSize(1240, 720);
  });
  await page.waitForFunction(() => window.deepzoraDesktop !== undefined);
  const expected = JSON.parse(readFileSync(join(bundle, "Contents/Resources/build.json"), "utf8")).version;
  assert.equal(await invoke("Version"), expected);
  await page.locator(".startup-splash").waitFor({ state: "hidden", timeout: 15000 });
}
async function quit() {
  await application.close();
  application = null;
  page = null;
}
try {
  await launch();
  await page.locator(".first-launch-welcome").waitFor();
  assert.equal(await invoke("NeedsOnboarding"), true);
  assert.equal(await dock().getAttribute("aria-pressed"), "false");
  assert.equal(await page.locator(".workbench-dock").isVisible(), false);
  assert.equal(await page.locator(".banner--warning").count(), 0);
  assert.equal(await page.locator(".first-launch-welcome__connect").count(), 1);
  await shot("01-first-launch");
  pass("fresh unconfigured home shows option 3 with one connection action and a collapsed workspace");
  await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().find(window => window.isVisible()).setContentSize(800, 600));
  await page.waitForTimeout(150);
  const primary = await page.locator(".first-launch-welcome__connect").boundingBox();
  assert(primary && primary.x >= 0 && primary.y >= 0 && primary.x + primary.width <= 800 && primary.y + primary.height <= 600);
  await shot("01-first-launch-compact");
  await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().find(window => window.isVisible()).setContentSize(1240, 720));

  await page.locator(".first-launch-welcome__connect").click();
  await page.locator(".provider-catalog").waitFor();
  await shot("02-connect-model");
  pass("connection action opens the existing model-access setup");
  await page.locator("#management-back").click();
  await page.locator(".first-launch-welcome").waitFor();
  // Observe the real renderer -> service -> native folder-picker dispatch.
  // Cancel at Electron's dialog seam to keep CLI acceptance non-interactive.
  await application.evaluate(({ dialog }) => {
    globalThis.__acceptanceFolderDialogs = [];
    dialog.showOpenDialog = async (...args) => {
      globalThis.__acceptanceFolderDialogs.push(args.at(-1));
      return { canceled: true, filePaths: [] };
    };
  });
  await page.locator(".first-launch-welcome__project-button").click();
  for (let retry = 0; retry < 100; retry++) {
    if (await application.evaluate(() => globalThis.__acceptanceFolderDialogs.length > 0)) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  const dialogs = await application.evaluate(() => globalThis.__acceptanceFolderDialogs);
  assert.equal(dialogs.length, 1);
  assert(dialogs[0].properties.includes("openDirectory"));
  await page.locator(".first-launch-welcome").waitFor({ state: "visible" });
  await page.waitForFunction(() => !document.querySelector(".first-launch-welcome__project-button")?.disabled);
  assert.equal(await page.locator(".banner--warning").count(), 0);
  await shot("03-add-project-cancelled");
  pass("optional project action reaches the native directory dialog; cancellation retains the welcome");

  for (const entry of ["files", "changed", "browser"]) {
    if (await dock().getAttribute("aria-pressed") !== "true") await dock().click();
    const picker = page.locator(`[data-dock-picker-entry="${entry}"]`);
    await page.locator(".workbench-dock").waitFor();
    if (await picker.isVisible()) await picker.click();
    else {
      await page.locator(".workbench-dock__tab-add").click();
      const label = { files: "文件", changed: "改动", browser: "浏览器" }[entry];
      await page.getByRole("menuitem", { name: label, exact: true }).click();
    }
    assert.equal(await dock().getAttribute("aria-pressed"), "true");
    await page.locator(".workbench-dock").waitFor();
    await shot("04-open-" + entry);
    await dock().click();
  }
  pass("files, changes and browser expand on demand before any model is configured");

  await dock().click();
  const files = page.locator('[data-dock-picker-entry="files"]');
  if (await files.isVisible()) await files.click();
  await quit();
  await launch();
  await page.locator(".first-launch-welcome").waitFor();
  assert.equal(await dock().getAttribute("aria-pressed"), "true");
  await shot("05-restored-open");
  pass("normal restart restores an explicitly open workspace without requiring a model");
  await dock().click();
  await quit();
  await launch();
  await page.locator(".first-launch-welcome").waitFor();
  assert.equal(await dock().getAttribute("aria-pressed"), "false");
  pass("normal restart preserves the user's collapsed layout");
  if (keepWorkspaceOpen) await dock().click();

  await page.locator(".first-launch-welcome__connect").click();
  await page.locator(".provider-catalog").waitFor();
  await page.locator(".provider-catalog__brand").filter({ hasText: "DeepSeek" }).click();
  const catalog = page.locator(".provider-catalog__detail");
  if (!options.includes("--official-visual")) await catalog.locator(".provider-catalog__url-control input").fill("http://127.0.0.1:9/v1");
  await catalog.locator('input[type="password"]').fill("local-test-only");
  await shot("06-key-entered");
  await catalog.locator(".btn--primary").click();
  const activeDetail = page.locator('.provider-connections__detail > div:not([hidden])');
  const completion = activeDetail.locator(".provider-onboarding-footer");
  await completion.waitFor();
  assert.equal(await invoke("NeedsOnboarding"), false);
  assert.equal(await page.locator(".banner--actionable").count(), 0, "the old detached onboarding banner is gone");
  for (const row of await activeDetail.locator('.provider-model-draft__option').all()) {
    if (!(await row.innerText()).includes("deepseek-flash")) await row.locator('input[type="checkbox"]').uncheck();
  }
  await activeDetail.locator('.provider-key-single input').fill("local-test-edited-key");
  assert.equal(await completion.count(), 0, "unsaved credentials cannot finish onboarding");
  assert.equal(await activeDetail.locator(".provider-key-saved").count(), 0, "a replacement key is not announced as saved");
  await shot("06-key-edit-unsaved");
  await activeDetail.locator(".provider-editor-footer .btn--primary").click();
  await completion.waitFor();
  await activeDetail.locator(".provider-key-saved").waitFor();
  const contrasts = await page.evaluate(() => {
    const canvas = document.createElement("canvas"); canvas.width = canvas.height = 1;
    const ctx = canvas.getContext("2d");
    const base = getComputedStyle(document.documentElement).getPropertyValue("--bg-elev");
    const pixel = () => [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3);
    const luminance = values => values.map(v => v / 255).map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4)
      .reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
    return [".provider-key-saved", ".provider-conversation-model"].map(selector => {
      const el = [...document.querySelectorAll(selector)].find(el => el.getBoundingClientRect().width > 0);
      const style = getComputedStyle(el);
      const ancestors = []; for (let node = el; node; node = node.parentElement) ancestors.unshift(node);
      ctx.fillStyle = base; ctx.fillRect(0, 0, 1, 1);
      for (const node of ancestors) { ctx.fillStyle = getComputedStyle(node).backgroundColor; ctx.fillRect(0, 0, 1, 1); }
      const bg = pixel();
      ctx.fillStyle = style.color; ctx.fillRect(0, 0, 1, 1);
      const fg = pixel();
      const a = luminance(bg), b = luminance(fg);
      return { selector, ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05), foreground: fg, background: bg };
    });
  });
  for (const color of contrasts) assert(color.ratio >= 4.5, `${color.selector} must have readable small-text contrast: ${color.ratio}`);
  writeFileSync(join(output, "contrast.json"), JSON.stringify(contrasts, null, 2));
  const configured = await invoke("Settings");
  const provider = configured.providers.find(provider => provider.added && provider.keySet);
  assert(provider && provider.models.includes("deepseek-flash"));
  assert.equal(provider.models.length, 1);
  assert((await completion.innerText()).includes("deepseek-flash"));
  await application.evaluate(({ BrowserWindow }) => {
    const window = BrowserWindow.getAllWindows().find(window => window.isVisible());
    window.setContentSize(800, 600);
  });
  await page.waitForTimeout(150);
  const finishBox = await completion.locator(".btn--primary").boundingBox();
  assert(finishBox && finishBox.x >= 0 && finishBox.y >= 0 && finishBox.x + finishBox.width <= 800 && finishBox.y + finishBox.height <= 600);
  await shot("06-configured-compact");
  await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().find(window => window.isVisible()).setContentSize(1240, 720));
  await shot("06-configured");
  pass("real API Key input and provider save reach the inline saved status and pinned completion footer; newer key/model edits require saving first");
  await completion.locator(".btn--primary").click();
  await page.locator("#composer-input").waitFor();
  assert.equal(await page.locator(".first-launch-welcome").count(), 0);
  assert.equal(await dock().getAttribute("aria-pressed"), String(keepWorkspaceOpen));
  await page.locator("#composer-input").fill("首段对话草稿（不发送）");
  await shot("07-first-conversation");
  await page.waitForFunction(async () => {
    const tabs = await window.deepzoraDesktop.invoke("ListTabs", []);
    return tabs.some(tab => tab.active && tab.ready);
  }, undefined, { timeout: 30000 });
  const tabs = await invoke("ListTabs");
  assert.equal(tabs.length, 1, "setup completion creates exactly one session");
  assert.equal((await invoke("Settings")).defaultModel, `${provider.name}/deepseek-flash`);
  const tabState = JSON.parse(readFileSync(join(home, "desktop-tabs.json"), "utf8"));
  assert.equal(tabState.tabs.find(tab => tab.id === tabs[0].id).model, `${provider.name}/deepseek-flash`,
    "the ready session's durable model binding resolves the selected connection and model");
  assert.equal(await page.locator(".project-tree__topic--active").count(), 1, "the first session renders once in the sidebar");
  pass(`setup completion creates one editable conversation and preserves the ${keepWorkspaceOpen ? "open" : "collapsed"} workspace`);
  await application.evaluate(({ dialog }) => {
    globalThis.__acceptanceFolderDialogs = [];
    dialog.showOpenDialog = async (...args) => {
      globalThis.__acceptanceFolderDialogs.push(args.at(-1));
      return { canceled: true, filePaths: [] };
    };
  });
  await page.getByRole("button", { name: "添加新项目", exact: true }).click();
  const localFolder = page.getByRole("menuitem", { name: /本地|文件夹/ });
  assert.equal(await localFolder.count(), 1);
  await localFolder.click();
  for (let retry = 0; retry < 100; retry++) {
    if (await application.evaluate(() => globalThis.__acceptanceFolderDialogs.length > 0)) break;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.equal(await application.evaluate(() => globalThis.__acceptanceFolderDialogs.length), 1);
  pass("adding a local project still reaches the native folder picker immediately after model setup");
  const folder = join(output, "selected-project");
  mkdirSync(folder);
  await application.evaluate(({ dialog }, folder) => {
    dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] });
  }, folder);
  await page.getByRole("button", { name: "添加新项目", exact: true }).click();
  await page.getByRole("menuitem", { name: /本地|文件夹/ }).click();
  await page.waitForFunction(async folder => {
    const tabs = await window.deepzoraDesktop.invoke("ListTabs", []);
    return tabs.some(tab => tab.active && tab.scope === "project" && tab.workspaceRoot === folder);
  }, folder, { timeout: 30000 });
  await page.locator("#composer-input").waitFor();
  await shot("08-project-added");
  pass("selecting an existing folder creates and selects the project with an editable conversation");
  assert.deepEqual(errors, []);
  writeFileSync(join(output, "acceptance.json"), JSON.stringify({ bundle, home, theme, keepWorkspaceOpen, viewport: [1240, 720], evidence, pageErrors: errors }, null, 2) + "\n");
} catch (error) {
  if (page && !page.isClosed()) await shot("failure").catch(() => {});
  throw error;
} finally {
  if (application) await quit();
}
