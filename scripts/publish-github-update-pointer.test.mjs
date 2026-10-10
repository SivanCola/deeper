import assert from "node:assert/strict";
import { test } from "node:test";
import { cliPointer, repository, shouldAdvance, validatePointer } from "./publish-github-update-pointer.mjs";

test("recovery preserves newer updates and rejects ambiguous identities", () => {
  assert.equal(shouldAdvance("1.2.3", null), true);
  assert.equal(shouldAdvance("1.2.3", "v1.2.3"), true);
  assert.equal(shouldAdvance("1.2.3", "v1.2.4"), false);
  assert.equal(shouldAdvance("2.0.0", "desktop-v1.9.9"), true);
  assert.throws(() => shouldAdvance("1.2.3", "npm-v1.2.3"));
  assert.throws(() => shouldAdvance("1.2.3-rc.1", "v1.2.2"));
});

test("update publication rejects upstream assets and mismatched signatures", () => {
  const make = () => ({
    version: "v1.2.3", download_page: `https://github.com/${repository}/releases`,
    platforms: Object.fromEntries(["darwin-arm64", "darwin-amd64", "linux-amd64", "windows-amd64", "windows-arm64"].map(platform => {
      const url = `https://github.com/${repository}/releases/download/desktop-v1.2.3/Deeper-${platform}.zip`;
      return [platform, { url, sig: `${url}.minisig`, size: 100, sha256: "a".repeat(64) }];
    })),
  });
  validatePointer(make(), "1.2.3");
  const upstream = make();
  upstream.platforms["darwin-arm64"].url = "https://github.com/esengine/DeepSeek-Reasonix/releases/download/desktop-v1.2.3/Reasonix-darwin-arm64.zip";
  assert.throws(() => validatePointer(upstream, "1.2.3"));
  const bad = make();
  bad.platforms["darwin-arm64"].sig += ".evil";
  assert.throws(() => validatePointer(bad, "1.2.3"));
  assert.throws(() => validatePointer(make(), "1.2.4"));
});

test("CLI pointer remains immutable across downloads and later release records", () => {
  const release = { tag_name: "v1.2.3", prerelease: false, assets:
    ["SHA256SUMS", ...["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"]
      .map(target => `deeper-${target}.${target.startsWith("windows") ? "zip" : "tar.gz"}`)]
      .map(name => ({ name, size: 100, download_count: 0,
        browser_download_url: `https://github.com/${repository}/releases/download/v1.2.3/${name}` })) };
  const before = cliPointer(release, "1.2.3");
  release.assets.reverse().forEach(asset => { asset.download_count = 99; });
  release.assets.push({ name: "release-event.json" });
  assert.deepEqual(cliPointer(release, "1.2.3"), before);
  release.assets[0].browser_download_url = "https://github.com/esengine/DeepSeek-Reasonix/releases/download/v1.2.3/SHA256SUMS";
  assert.throws(() => cliPointer(release, "1.2.3"));
});
