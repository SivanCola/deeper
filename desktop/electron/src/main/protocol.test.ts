import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";
import { isForwardedPath, registerAppProtocol, resolveDistRoot, routeAppRequest } from "./protocol.js";

let dist = "";
const isFile = (path: string) => {
  try {
    return statSync(path).isFile();
  } catch {
    return false;
  }
};
const route = (url: string) => routeAppRequest(url, dist, isFile);

test("normal app documents do not pre-enable a profiling engine", async () => {
  let handler!: (request: Request) => Promise<Response> | Response;
  registerAppProtocol({
    protocol: { handle: (_scheme, callback) => { handler = callback; } },
    fetch: async () => new Response("forwarded"), distRoot: dist, resources: () => null,
    log: { info() {}, warn() {}, error() {} },
  });
  const response = await handler(new Request("deeper://app/"));
  assert.equal(response.headers.get("Document-Policy"), null);
  await response.text();
});

before(() => {
  dist = mkdtempSync(join(tmpdir(), "deeper-dist-"));
  mkdirSync(join(dist, "assets"), { recursive: true });
  writeFileSync(join(dist, "index.html"), "<!doctype html>");
  writeFileSync(join(dist, "assets", "app.js"), "export {};");
  writeFileSync(join(dist, "assets", "styles.css"), "body{}");
  writeFileSync(join(dist, "assets", "font.woff2"), "");
});

after(() => rmSync(dist, { recursive: true, force: true }));

test("the root and existing files under dist are served with their MIME types", () => {
  assert.deepEqual(route("deeper://app/"), { kind: "file", path: join(dist, "index.html"), mime: "text/html; charset=utf-8" });
  assert.deepEqual(route("deeper://app"), { kind: "file", path: join(dist, "index.html"), mime: "text/html; charset=utf-8" });
  assert.deepEqual(route("deeper://app/index.html?boot=1"), { kind: "file", path: join(dist, "index.html"), mime: "text/html; charset=utf-8" });
  assert.deepEqual(route("deeper://app/assets/app.js"), { kind: "file", path: join(dist, "assets", "app.js"), mime: "text/javascript; charset=utf-8" });
  assert.equal(route("deeper://app/assets/styles.css").kind, "file");
  assert.equal((route("deeper://app/assets/font.woff2") as { mime: string }).mime, "font/woff2");
});

test("index.html is not a fallback for other paths", () => {
  assert.equal(route("deeper://app/settings").kind, "notFound");
  assert.equal(route("deeper://app/assets/").kind, "notFound");
  assert.equal(route("deeper://app/assets/missing.js").kind, "notFound");
});

test("traversal, absolute escapes and odd hosts are rejected", () => {
  // The WHATWG parser collapses literal and %2e-encoded dot segments before
  // routing, exactly as Chromium does for a standard scheme, so these can only
  // land inside dist; the encoded-slash and backslash forms must still fail.
  assert.equal(route("deeper://app/../index.html").kind, "file");
  assert.equal(route("deeper://app/%2e%2e/index.html").kind, "file");
  assert.equal(route("deeper://app/assets/../../etc/passwd").kind, "notFound");
  assert.equal(route("deeper://app/assets/%2e%2e/%2e%2e/etc/passwd").kind, "notFound");
  for (const url of [
    "deeper://app//etc/passwd",
    "deeper://app/assets/..%2Fapp.js",
    "deeper://app/assets%5Capp.js",
    "deeper://app/%00",
    "deeper://evil/index.html",
    "http://app/index.html",
    "not a url",
  ]) {
    assert.equal(route(url).kind, "notFound", url);
  }
});

test("only the resource prefixes forward to the loopback origin, query included", () => {
  assert.deepEqual(route("deeper://app/__deeper_workspace_media/tok/a.png"), { kind: "forward", target: "/__deeper_workspace_media/tok/a.png" });
  assert.deepEqual(route("deeper://app/__deeper_theme_asset/x.jpg?v=2"), { kind: "forward", target: "/__deeper_theme_asset/x.jpg?v=2" });
  assert.deepEqual(route("deeper://app/__deeper_remote_markdown_image?url=https%3A%2F%2Fx"), {
    kind: "forward",
    target: "/__deeper_remote_markdown_image?url=https%3A%2F%2Fx",
  });
  assert.equal(isForwardedPath("/__deeper_remote_markdown_imagex"), false);
  assert.equal(isForwardedPath("/__deeper_workspace_media"), false);
  assert.equal(route("deeper://app/__shell/quit").kind, "notFound");
});

test("the dist root honours the override, then packaging layout", () => {
  assert.equal(resolveDistRoot({ env: { DEEPER_FRONTEND_DIST: "/tmp/dist" }, appPath: "/app/electron", resourcesPath: "/res", packaged: true }), "/tmp/dist");
  assert.equal(resolveDistRoot({ env: {}, appPath: "/app/electron", resourcesPath: "/res", packaged: false }), "/app/frontend/dist");
  assert.equal(resolveDistRoot({ env: {}, appPath: "/app/electron", resourcesPath: "/res", packaged: true }), "/res/app");
});
