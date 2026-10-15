import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { desktopAssets, desktopName, prepareDownloads, renderDesktopDownloads } from "./desktop-downloads.mjs";

test("v0.0.1 navigation preserves published URLs and explains architectures and auxiliary files", () => {
  const text = renderDesktopDownloads("0.0.1");
  assert.match(text, /M 系列/);
  assert.match(text, /Intel／AMD/);
  assert.match(text, /ARM64/);
  assert.match(text, /\.minisig/);
  assert.match(text, /latest.json/);
  assert.equal([...text.matchAll(/\]\(https:/g)].length, 9);
  assert.match(text, /desktop-v0\.0\.1\/Deepzora-darwin-arm64\.dmg/);
  assert.doesNotMatch(text, /Deepzora-0\.0\.1-/);
});

test("future packages keep byte-identical aliases, refuse conflicts and reject unsafe versions", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "deepzora-downloads-"));
  try {
    for (const asset of desktopAssets) writeFileSync(path.join(dir, asset.legacy), `payload:${asset.legacy}`);
    prepareDownloads(dir, "v0.0.1");
    assert.equal(readdirSync(dir).length, 11);
    prepareDownloads(dir, "v0.0.2");
    prepareDownloads(dir, "v0.0.2"); // retries may reuse the same bytes
    assert.equal(readdirSync(dir).length, 22);
    for (const asset of desktopAssets) assert.deepEqual(readFileSync(path.join(dir, desktopName("0.0.2", asset))), readFileSync(path.join(dir, asset.legacy)));
    assert.match(renderDesktopDownloads("0.0.2", "en"), /Deepzora-0\.0\.2-Windows-x64-Setup\.exe/);
    writeFileSync(path.join(dir, "Deepzora-0.0.2-macOS-arm64.dmg"), "conflicting payload");
    assert.throws(() => prepareDownloads(dir, "0.0.2"), /conflicting download/);
    assert.throws(() => desktopName("../../bad", desktopAssets[0]), /invalid/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("official validator accepts the new manifest with legacy routes and rejects changed names or alias content", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "deepzora-named-manifest-"));
  const file = path.join(dir, "latest.json");
  const base = "https://github.com/SivanCola/deepzora/releases/download/desktop-v0.0.2/";
  const asset = name => ({ url: base + name, sig: base + name + ".minisig", sha256: "a".repeat(64), size: 42, install_layout: "electron-v1" });
  const m = { version: "v0.0.2", artifact_naming: "versioned-v1", download_page: "https://github.com/SivanCola/deepzora/releases",
    release_notes_url: "https://github.com/SivanCola/deepzora/releases/tag/v0.0.2/", platforms: {}, native_packages: {}, downloads: {} };
  for (const platform of ["darwin-arm64", "darwin-amd64", "windows-amd64", "windows-arm64", "linux-amd64"]) {
    const suffix = platform.startsWith("darwin") ? ".zip" : platform.startsWith("windows") ? "-installer.exe" : ".tar.gz";
    m.platforms[platform] = asset(`Deepzora-${platform}${suffix}`);
  }
  m.native_packages["linux-amd64"] = asset("Deepzora-linux-amd64.deb");
  for (const name of ["Deepzora-darwin-arm64.dmg", "Deepzora-darwin-amd64.dmg", "Deepzora-darwin-universal.dmg", "Deepzora-windows-amd64.zip"]) m.downloads[name] = asset(name);
  for (const spec of desktopAssets) m.downloads[desktopName(m.version, spec)] = asset(desktopName(m.version, spec));
  const validate = value => {
    writeFileSync(file, JSON.stringify(value));
    return spawnSync("bash", ["scripts/validate-desktop-release-manifest.sh", "stable", "v0.0.2", base, file], { encoding: "utf8" });
  };
  try {
    const accepted = validate(m);
    assert.equal(accepted.status, 0, accepted.stderr);
    const cases = [
      value => { value.downloads["Deepzora-0.0.2-macOS-arm64.zip"].sha256 = "b".repeat(64); },
      value => { delete value.downloads["Deepzora-0.0.2-Windows-arm64-Portable.zip"]; },
      value => { value.downloads["Deepzora-0.0.2-macOS-x64.dmg"].url = base + "Deepzora-0.0.3-macOS-x64.dmg"; },
      value => { value.artifact_naming = "unknown"; },
      value => { delete value.artifact_naming; },
    ];
    for (const mutate of cases) { const value = structuredClone(m); mutate(value); assert.notEqual(validate(value).status, 0); }
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
