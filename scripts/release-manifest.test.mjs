import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const repository = "SivanCola/deepzora";
const notes = `https://github.com/${repository}/releases/tag/v1.0.0/`;

test("official manifests validate complete assets and reject historical channels or corrupt identity", () => {
  const directory = mkdtempSync(path.join(tmpdir(), "deepzora-manifest-"));
  const file = path.join(directory, "manifest.json");
  function validate(surface, manifest, channel = "stable", notesVersion = "v1.0.0") {
    writeFileSync(file, JSON.stringify(manifest));
    const args = surface === "cli"
      ? [channel, "v1.0.0", repository, file, notesVersion]
      : [channel, "v1.0.0", `https://github.com/${repository}/releases/download/desktop-v1.0.0/`, file, notesVersion];
    const result = spawnSync("bash", [`scripts/validate-${surface}-release-manifest.sh`, ...args], { encoding: "utf8" });
    assert.ifError(result.error);
    return result;
  }
  try {
    const assetNames = ["deepzora-darwin-amd64.tar.gz", "deepzora-darwin-arm64.tar.gz", "deepzora-linux-amd64.tar.gz",
      "deepzora-linux-arm64.tar.gz", "deepzora-windows-amd64.zip", "deepzora-windows-arm64.zip", "SHA256SUMS"];
    const cli = { tag_name: "v1.0.0", prerelease: false, html_url: `https://github.com/${repository}/releases/tag/v1.0.0`,
      release_notes_url: notes, assets: assetNames.map(name => ({ name, size: 1,
        browser_download_url: `https://github.com/${repository}/releases/download/v1.0.0/${name}` })) };
    const base = `https://github.com/${repository}/releases/download/desktop-v1.0.0/`;
    const asset = name => ({ url: base + name, sig: base + name + ".minisig", size: 1, sha256: "a".repeat(64) });
    const desktop = { version: "v1.0.0", download_page: `https://github.com/${repository}/releases`, release_notes_url: notes,
      platforms: Object.fromEntries(["darwin-arm64", "darwin-amd64", "windows-amd64", "windows-arm64", "linux-amd64"].map(platform => [platform,
        asset(`Deepzora-${platform}${platform.startsWith("windows") ? "-installer.exe" : platform.startsWith("darwin") ? ".zip" : ".tar.gz"}`)])),
      native_packages: { "linux-amd64": asset("Deepzora-linux-amd64.deb") },
      downloads: Object.fromEntries(["Deepzora-darwin-arm64.dmg", "Deepzora-darwin-amd64.dmg", "Deepzora-darwin-universal.dmg", "Deepzora-windows-amd64.zip"].map(name => [name, asset(name)])) };
    for (const [surface, manifest] of [["cli", cli], ["desktop", desktop]]) {
      const accepted = validate(surface, manifest);
      assert.equal(accepted.status, 0, accepted.stderr);
      for (const channel of ["preview", "canary", "any"]) assert.notEqual(validate(surface, manifest, channel).status, 0);
      assert.notEqual(validate(surface, manifest, "stable", "v1.0.0-preview.1").status, 0);
      assert.notEqual(validate(surface, { ...manifest, release_notes_url: "https://example.invalid/notes" }).status, 0);
    }
    assert.notEqual(validate("cli", { ...cli, prerelease: true }).status, 0);
    assert.notEqual(validate("cli", { ...cli, assets: cli.assets.slice(1) }).status, 0);
    const corrupt = structuredClone(desktop);
    corrupt.platforms["windows-arm64"].sha256 = "unverified";
    assert.notEqual(validate("desktop", corrupt).status, 0);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
