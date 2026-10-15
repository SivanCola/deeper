import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { decideCaskUpdate, verifyCaskReferences } from "./publish-homebrew-cask.mjs";
import { renderHomebrewCask } from "./build-release-cli-candidate.mjs";

test("Homebrew verifies four CLI URLs and checksums independently of Desktop names", () => {
  const sums = Object.fromEntries(["darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"].map((target, index) => [`deepzora-${target}.tar.gz`, String(index).repeat(64)]));
  const checksums = Object.entries(sums).map(([name, hash]) => `${hash}  ${name}`).join("\n");
  const generated = renderHomebrewCask("0.0.2", sums);
  verifyCaskReferences(generated, "0.0.2", checksums);
  assert.throws(() => verifyCaskReferences(generated.replace("download/v#{version}", "download/desktop-v#{version}"), "0.0.2", checksums), /invalid Homebrew/);
  assert.throws(() => verifyCaskReferences(generated.replace("0".repeat(64), "a".repeat(64)), "0.0.2", checksums), /invalid Homebrew/);
  assert.throws(() => verifyCaskReferences(generated, "0.0.3", checksums), /version mismatch/);
  assert.throws(() => verifyCaskReferences(generated.replace("deepzora-darwin-arm64.tar.gz", "Deepzora-0.0.2-macOS-arm64.dmg"), "0.0.2", checksums), /invalid Homebrew/);
  assert.throws(() => verifyCaskReferences(generated.replaceAll("on_arm", "on_temp").replaceAll("on_intel", "on_arm").replaceAll("on_temp", "on_intel"), "0.0.2", checksums), /invalid Homebrew/);
});

const cask = version => `cask "deepzora" do\n  version "${version}"\nend\n`;

test("reuses an identical cask", () => assert.equal(decideCaskUpdate(cask("1.2.3"), cask("1.2.3")), "reuse"));
test("publishes a newer cask", () => assert.equal(decideCaskUpdate(cask("1.2.3"), cask("1.2.4")), "publish"));
test("rejects downgrade and same-version conflict", () => {
  assert.throws(() => decideCaskUpdate(cask("2.0.0"), cask("1.9.9")), /backwards/);
  assert.throws(() => decideCaskUpdate(`${cask("1.2.3")}# old`, cask("1.2.3")), /conflicting/);
});

function runPublisher(t, scenario, candidate = cask("1.2.4")) {
  const directory = mkdtempSync(path.join(tmpdir(), "deepzora-homebrew-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const trace = path.join(directory, "requests.json");
  const mock = path.join(directory, "fetch.mjs");
  const file = path.join(directory, "deepzora.rb");
  writeFileSync(file, candidate);
  writeFileSync(mock, `
    import { writeFileSync } from "node:fs";
    const scenario = ${JSON.stringify(scenario)};
    const requests = [];
    globalThis.fetch = async (url, options) => {
      requests.push({ url, method: options.method ?? "GET", body: options.body ? JSON.parse(options.body) : null });
      writeFileSync(${JSON.stringify(trace)}, JSON.stringify(requests));
      const status = options.method === "PUT" ? 201 : url.includes("/contents/") ? scenario.caskStatus : scenario.repositoryStatus;
      const body = options.method === "PUT" ? {} : url.includes("/contents/") ?
        { content: Buffer.from(scenario.existing ?? "").toString("base64"), sha: "existing-sha" } : { default_branch: "main" };
      return new Response(JSON.stringify(body), { status });
    };
  `);
  const result = spawnSync(process.execPath, ["--import", mock, fileURLToPath(new URL("./publish-homebrew-cask.mjs", import.meta.url)), file], {
    env: { ...process.env, HOMEBREW_TAP_TOKEN: "test-token", NODE_OPTIONS: "" }, encoding: "utf8", timeout: 10000,
  });
  return { ...result, requests: JSON.parse(readFileSync(trace, "utf8")) };
}

test("first publication creates a cask in an existing fresh tap", t => {
  const result = runPublisher(t, { caskStatus: 404, repositoryStatus: 200 });
  assert.equal(result.status, 0, result.stderr);
  const put = result.requests.find(request => request.method === "PUT");
  assert.ok(put);
  assert.ok(result.requests.some(request => request.url.endsWith("/branches/main")));
  assert.equal(put.body.branch, "main");
  assert.equal(Object.hasOwn(put.body, "sha"), false);
  assert.equal(Buffer.from(put.body.content, "base64").toString("utf8"), cask("1.2.4"));
});

test("updating an existing cask retains its SHA and reuses identical content", t => {
  const update = runPublisher(t, { caskStatus: 200, existing: cask("1.2.3") });
  assert.equal(update.status, 0, update.stderr);
  assert.ok(update.requests[0].url.endsWith("?ref=main"));
  assert.equal(update.requests.find(request => request.method === "PUT").body.sha, "existing-sha");
  const reuse = runPublisher(t, { caskStatus: 200, existing: cask("1.2.4") });
  assert.equal(reuse.status, 0, reuse.stderr);
  assert.equal(reuse.requests.some(request => request.method === "PUT"), false);
});

test("repository and permission errors never create a replacement cask", t => {
  for (const scenario of [{ caskStatus: 404, repositoryStatus: 404 }, { caskStatus: 403 }]) {
    const result = runPublisher(t, scenario);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /GitHub API (403|404)/);
    assert.equal(result.requests.some(request => request.method === "PUT"), false);
  }
});
