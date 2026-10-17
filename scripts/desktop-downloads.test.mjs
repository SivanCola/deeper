import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { desktopAssets, desktopName, prepareDownloads, renderDesktopDownloads } from "./desktop-downloads.mjs";

test("v0.0.1 navigation uses versioned URLs and explains architectures and auxiliary files", () => {
  const text = renderDesktopDownloads("0.0.1");
  assert.match(text, /M 系列/);
  assert.match(text, /Intel／AMD/);
  assert.match(text, /ARM64/);
  assert.match(text, /\.minisig/);
  assert.match(text, /latest.json/);
  assert.equal([...text.matchAll(/\]\(https:/g)].length, 9);
  assert.match(text, /desktop-v0\.0\.1\/Deepzora-0\.0\.1-macOS-arm64\.dmg/);
  assert.doesNotMatch(text, /Deepzora-darwin-/);
});

test("build outputs become one versioned artifact each with idempotent retries", () => {
 for (const version of ["0.0.1", "0.0.2"]) {
  const dir=mkdtempSync(path.join(tmpdir(),"deepzora-downloads-"));
  try {
   for (const spec of desktopAssets) writeFileSync(path.join(dir,spec.buildName),`payload:${spec.suffix}`);
   prepareDownloads(dir,version); prepareDownloads(dir,version);
   assert.deepEqual(readdirSync(dir).sort(),desktopAssets.map(spec=>desktopName(version,spec)).sort());
   for (const spec of desktopAssets) assert.equal(readFileSync(path.join(dir,desktopName(version,spec)),"utf8"),`payload:${spec.suffix}`);
   const first=desktopAssets[0];
   writeFileSync(path.join(dir,first.buildName),"different");
   assert.throws(()=>prepareDownloads(dir,version),/conflicting download/);
   assert.throws(()=>desktopName("../../bad",first),/invalid/);
  } finally {rmSync(dir,{recursive:true,force:true});}
 }
});

test("official validator accepts current routes and rejects missing or inconsistent downloads", () => {
  const dir = mkdtempSync(path.join(tmpdir(), "deepzora-named-manifest-"));
  const file = path.join(dir, "latest.json");
  const base = "https://github.com/SivanCola/deepzora/releases/download/desktop-v0.0.2/";
  const asset = name => ({ url: base + name, sig: base + name + ".minisig", sha256: "a".repeat(64), size: 42, install_layout: "electron-v1" });
  const m = { version: "v0.0.2", artifact_naming: "versioned-v1", download_page: "https://github.com/SivanCola/deepzora/releases",
    release_notes_url: "https://github.com/SivanCola/deepzora/releases/tag/v0.0.2/", platforms: {}, native_packages: {}, downloads: {} };
  for (const spec of desktopAssets) {
    const name=desktopName(m.version,spec);
    m.downloads[name]=asset(name);
    if(spec.channel) m[spec.channel][spec.platform]=asset(name);
  }
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
      value => { value.downloads[desktopAssets[0].buildName] = asset(desktopAssets[0].buildName); },
    ];
    for (const mutate of cases) { const value = structuredClone(m); mutate(value); assert.notEqual(validate(value).status, 0); }
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
