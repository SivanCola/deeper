import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const repository = "SivanCola/deeper";
const stable = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;

export function shouldAdvance(version, currentTag) {
  assert.match(version, stable);
  if (!currentTag) return true;
  const current = currentTag.replace(/^(desktop-)?v/, "");
  assert.match(current, stable, "GitHub latest must identify a stable Deeper release");
  const wanted = version.split(".").map(Number);
  const actual = current.split(".").map(Number);
  for (let index = 0; index < 3; index++) {
    if (wanted[index] !== actual[index]) return wanted[index] > actual[index];
  }
  return true;
}

export function validatePointer(manifest, version) {
  assert.equal(manifest.version, `v${version}`);
  assert.equal(manifest.download_page, `https://github.com/${repository}/releases`);
  const assets = [...Object.values(manifest.platforms ?? {}), ...Object.values(manifest.native_packages ?? {}), ...Object.values(manifest.downloads ?? {})];
  assert.ok(assets.length >= 5, "desktop pointer must include the platform matrix");
  const base = `https://github.com/${repository}/releases/download/desktop-v${version}/`;
  for (const asset of assets) {
    assert.ok(asset.url.startsWith(base), "desktop asset belongs to another repository or release");
    assert.equal(asset.sig, `${asset.url}.minisig`);
    assert.match(asset.sha256, /^[0-9a-f]{64}$/);
    assert.ok(Number.isSafeInteger(asset.size) && asset.size > 0);
  }
}

export function cliPointer(release, version) {
  assert.equal(release.tag_name, `v${version}`);
  assert.equal(release.prerelease, false);
  const names = ["SHA256SUMS", ...["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64",
    "windows-amd64", "windows-arm64"].map(target => `deeper-${target}.${target.startsWith("windows") ? "zip" : "tar.gz"}`)];
  const assets = names.sort().map(name => {
    const matches = release.assets.filter(asset => asset.name === name);
    assert.equal(matches.length, 1, `missing or duplicate CLI asset: ${name}`);
    const asset = matches[0];
    assert.equal(asset.browser_download_url, `https://github.com/${repository}/releases/download/v${version}/${name}`);
    assert.ok(Number.isSafeInteger(asset.size) && asset.size > 0);
    // Download counters, timestamps and later ledger assets are mutable GitHub
    // metadata and must not affect this immutable updater document.
    return { name, size: asset.size, browser_download_url: asset.browser_download_url };
  });
  return { tag_name: release.tag_name, prerelease: false, assets };
}

function gh(...args) {
  return execFileSync("gh", args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
}

function api(endpoint) {
  return JSON.parse(gh("api", `repos/${repository}/${endpoint}`));
}

function latestRelease() {
  try {
    return api("releases/latest");
  } catch (error) {
    const body = String(error.stdout ?? "");
    let status;
    try { status = Number(JSON.parse(body).status); } catch { throw error; }
    if (status !== 404) throw error;
    return null;
  }
}

function uploadImmutable(tag, file, directory) {
  const name = path.basename(file);
  const assets = api(`releases/tags/${tag}`).assets.filter(asset => asset.name === name);
  if (assets.length === 0) {
    gh("release", "upload", tag, file, "--repo", repository);
  } else {
    assert.equal(assets.length, 1, `duplicate ${name}`);
    const existing = path.join(directory, `existing-${name}`);
    gh("release", "download", tag, "--repo", repository, "--pattern", name, "--output", existing);
    const digest = target => createHash("sha256").update(readFileSync(target)).digest("hex");
    assert.equal(digest(file), digest(existing), `immutable ${name} conflicts with the published release`);
  }
}

export function publish(version, sourceSHA) {
  assert.match(version, stable);
  assert.match(sourceSHA, /^[0-9a-f]{40}$/);
  const cliTag = `v${version}`;
  const desktopTag = `desktop-v${version}`;
  for (const tag of [cliTag, desktopTag]) {
    const ref = api(`git/ref/tags/${tag}`);
    assert.equal(ref.object.type, "commit", "release tags must be lightweight immutable commit refs");
    assert.equal(ref.object.sha, sourceSHA);
    const release = api(`releases/tags/${tag}`);
    assert.equal(release.draft, false);
    assert.equal(release.prerelease, false);
  }
  const directory = mkdtempSync(path.join(tmpdir(), "deeper-update-pointer-"));
  try {
    const manifestPath = path.join(directory, "latest.json");
    gh("release", "download", desktopTag, "--repo", repository, "--pattern", "latest.json", "--output", manifestPath);
    validatePointer(JSON.parse(readFileSync(manifestPath, "utf8")), version);
    execFileSync("bash", ["scripts/validate-desktop-release-manifest.sh", "stable", `v${version}`, `https://github.com/${repository}/releases/download/${desktopTag}/`, manifestPath, `v${version}`], { stdio: "inherit" });
    uploadImmutable(cliTag, manifestPath, directory);
    const cliPath = path.join(directory, "cli-latest.json");
    const cli = api(`releases/tags/${cliTag}`);
    writeFileSync(cliPath, `${JSON.stringify(cliPointer(cli, version))}\n`);
    uploadImmutable(cliTag, cliPath, directory);
    const current = latestRelease();
    if (shouldAdvance(version, current?.tag_name)) {
      gh("release", "edit", cliTag, "--repo", repository, "--latest");
      assert.equal(latestRelease().tag_name, cliTag);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  publish(process.env.RELEASE_VERSION, process.env.RELEASE_EXPECTED_SHA);
}
