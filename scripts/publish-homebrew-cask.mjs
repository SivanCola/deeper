import { readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const VERSION_RE = /version "(\d+)\.(\d+)\.(\d+)"/;

// Homebrew installs the CLI, not the Desktop DMG. Verify every architecture's
// reference against the published CLI checksum list before declaring success.
export function verifyCaskReferences(cask, version, checksums) {
  if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version)
      || cask.match(VERSION_RE)?.slice(1).join(".") !== version) throw new Error("Homebrew version mismatch");
  const sums = new Map(checksums.trim().split(/\r?\n/).map(line => {
    const match = /^([a-f0-9]{64})  ([A-Za-z0-9._-]+)$/.exec(line);
    if (!match) throw new Error("invalid CLI checksum line");
    return [match[2], match[1]];
  }));
  const expected = new Map(["darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"].map(target => {
    const name = `deepzora-${target}.tar.gz`;
    if (!sums.has(name)) throw new Error(`missing CLI checksum: ${name}`);
    return [`https://github.com/SivanCola/deepzora/releases/download/v${version}/${name}`, sums.get(name)];
  }));
  const references = [...cask.matchAll(/on_(macos|linux) do\n([\s\S]*?)\n  end/g)].flatMap(([, os, section]) =>
    [...section.matchAll(/on_(arm|intel) do\s+sha256 "([a-f0-9]{64})"\s+url "([^"]+)"/g)]
      .map(([, arch, hash, url]) => ({ target: `${os === "macos" ? "darwin" : "linux"}-${arch === "arm" ? "arm64" : "amd64"}`, hash, url })));
  if (references.length !== expected.size) throw new Error("Homebrew must reference all four CLI architectures");
  for (const { target, hash, url: rawURL } of references) {
    const url = rawURL.replaceAll("#{version}", version);
    if (expected.get(url) !== hash || !url.endsWith(`/deepzora-${target}.tar.gz`)) throw new Error(`invalid Homebrew CLI reference: ${url}`);
    expected.delete(url);
  }
}

function compareVersion(left, right) {
  const a = left.map(Number); const b = right.map(Number);
  for (let i = 0; i < 3; i += 1) if (a[i] !== b[i]) return a[i] > b[i] ? 1 : -1;
  return 0;
}

export function decideCaskUpdate(existing, candidate) {
  if (existing === candidate) return "reuse";
  const current = existing.match(VERSION_RE);
  const next = candidate.match(VERSION_RE);
  if (!next) throw new Error("candidate cask has no canonical version");
  if (current && compareVersion(next.slice(1), current.slice(1)) < 0) throw new Error("refusing to move Homebrew cask backwards");
  if (current && compareVersion(next.slice(1), current.slice(1)) === 0) throw new Error("existing Homebrew version has conflicting content");
  return "publish";
}

async function request(url, token, { allowMissing = false, ...options } = {}) {
  const response = await fetch(url, {
    ...options,
    headers: { Accept: "application/vnd.github+json", Authorization: `Bearer ${token}`, "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28", ...options.headers },
  });
  if (allowMissing && response.status === 404) return null;
  if (!response.ok) throw new Error(`GitHub API ${response.status}: ${await response.text()}`);
  return response.json();
}

async function publish(file) {
  const token = process.env.HOMEBREW_TAP_TOKEN;
  if (!token) throw new Error("HOMEBREW_TAP_TOKEN is required");
  const candidate = readFileSync(file, "utf8");
  const repository = "https://api.github.com/repos/SivanCola/homebrew-deepzora";
  const api = `${repository}/contents/Casks/deepzora.rb`;
  const current = await request(`${api}?ref=main`, token, { allowMissing: true });
  if (!current) await request(`${repository}/branches/main`, token);
  const existing = current ? Buffer.from(current.content, "base64").toString("utf8") : "";
  if (decideCaskUpdate(existing, candidate) === "reuse") return;
  await request(api, token, {
    method: "PUT",
    body: JSON.stringify({
      message: `chore: update Deepzora to ${candidate.match(VERSION_RE).slice(1).join(".")}`,
      content: Buffer.from(candidate).toString("base64"),
      ...(current ? { sha: current.sha } : {}),
      branch: "main",
      committer: { name: "SivanCola", email: "32437197+SivanCola@users.noreply.github.com" },
    }),
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv[2] === "verify") {
    const [, , , file, version, sums] = process.argv;
    verifyCaskReferences(readFileSync(file, "utf8"), version, readFileSync(sums, "utf8"));
  } else {
    if (!process.argv[2]) throw new Error("usage: publish-homebrew-cask.mjs CASK_FILE | verify CASK_FILE VERSION SHA256SUMS");
    await publish(process.argv[2]);
  }
}
