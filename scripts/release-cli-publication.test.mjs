import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { test } from "node:test";

const repository = "SivanCola/deepzora";
const tag = "v1.2.3";
const binaries = ["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"]
  .map(target => `deepzora-${target}.${target.startsWith("windows") ? "zip" : "tar.gz"}`);
const digest = value => createHash("sha256").update(value).digest("hex");
const asset = name => ({ name, state: "uploaded", size: 10, digest: `sha256:${digest(name)}`,
  browser_download_url: `https://github.com/${repository}/releases/download/${tag}/${name}` });

function decide(t, metadata = [], mutate = () => {}, alterChecksums = value => value) {
  const directory = mkdtempSync(path.join(tmpdir(), "deepzora-cli-reuse-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const checksums = alterChecksums(binaries.map(name => `${digest(name)}  ${name}\n`).join(""));
  const checksumAsset = { ...asset("SHA256SUMS"), digest: `sha256:${digest(checksums)}` };
  const release = { tag_name: tag, draft: false, prerelease: false,
    html_url: `https://github.com/${repository}/releases/tag/${tag}`,
    assets: [...binaries.map(asset), checksumAsset, ...metadata.map(asset)] };
  mutate(release);
  const releasePath = path.join(directory, "release.json");
  const checksumsPath = path.join(directory, "SHA256SUMS");
  writeFileSync(releasePath, JSON.stringify(release));
  writeFileSync(checksumsPath, checksums);
  return spawnSync("bash", ["scripts/decide-cli-release-publication.sh", "stable", tag, repository, releasePath, checksumsPath], { encoding: "utf8" });
}

test("CLI recovery accepts published pointers and release event without changing binary checksums", t => {
  for (const metadata of [[], ["latest.json"], ["latest.json", "cli-latest.json"], ["latest.json", "cli-latest.json", "release-event.json"]]) {
    const result = decide(t, metadata);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout.trim(), "reuse");
  }
});

test("CLI recovery still rejects missing, duplicate, unknown and foreign assets", t => {
  const cases = [
    release => { release.assets.shift(); },
    release => { release.assets.push({ ...release.assets[0] }); },
    release => { release.assets.push(asset("unexpected.json")); },
    release => { release.assets[0].browser_download_url = "https://example.com/foreign.zip"; },
  ];
  for (const mutate of cases) assert.notEqual(decide(t, ["latest.json", "cli-latest.json"], mutate).status, 0);
});

test("CLI recovery retains the six-archive checksum and metadata digest binding", t => {
  assert.notEqual(decide(t, ["cli-latest.json"], release => {
    release.assets[0].digest = `sha256:${"0".repeat(64)}`;
  }).status, 0);
  assert.notEqual(decide(t, [], () => {}, value => value.replace(binaries[0], "unexpected.zip")).status, 0);
  assert.notEqual(decide(t, ["release-event.json"], release => {
    release.assets.find(item => item.name === "SHA256SUMS").digest = `sha256:${"0".repeat(64)}`;
  }).status, 0);
});
