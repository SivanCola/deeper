import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { desktopAssets, desktopName } from "./desktop-downloads.mjs";
import { renderHomebrewCask } from "./build-release-cli-candidate.mjs";

const workflow = name => readFileSync(`.github/workflows/${name}`, "utf8");
test("release workflows only publish the current product line", () => {
 for (const file of ["release.yml","release-npm.yml","release-promote.yml","release-verify.yml"]) {
  assert.doesNotMatch(workflow(file), /CLI_PUBLISH_FROZEN|legacy-v1/);
 }
 assert.match(workflow("release-npm.yml"), /Finalize official npm publication/);
});

const REQUIRED_CLI = ["SHA256SUMS", "deepzora-darwin-amd64.tar.gz", "deepzora-darwin-arm64.tar.gz", "deepzora-linux-amd64.tar.gz", "deepzora-linux-arm64.tar.gz", "deepzora-windows-amd64.zip", "deepzora-windows-arm64.zip"];
const DESKTOP = ["Deepzora-darwin-arm64.dmg", "Deepzora-darwin-amd64.dmg", "Deepzora-darwin-universal.dmg", "Deepzora-darwin-arm64.zip", "Deepzora-darwin-amd64.zip", "Deepzora-linux-amd64.deb", "Deepzora-linux-amd64.tar.gz", "Deepzora-windows-amd64-installer.exe", "Deepzora-windows-amd64.zip", "Deepzora-windows-arm64-installer.exe"];
const sha = "a".repeat(40);

function verifier(t, { latest = "1.2.3", missingNamed = false, badCask = false } = {}) {
  const root = realpathSync(mkdtempSync(path.join(tmpdir(), "cli-freeze-")));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const bin = path.join(root, "bin");
  mkdirSync(bin);
  const asset = name => ({ name, size: 1 });
  const cli = { isDraft: false, isPrerelease: false, assets: REQUIRED_CLI.map(asset) };
  const names = [...DESKTOP, ...(missingNamed ? [] : desktopAssets.map(spec => desktopName("1.2.3", spec)))];
  const desktop = { isDraft: false, isPrerelease: false, assets: [asset("latest.json"), ...names.flatMap(name => [asset(name), asset(`${name}.minisig`)])] };
  const sums = Object.fromEntries(REQUIRED_CLI.filter(name => name.endsWith(".tar.gz")).map(name => [name, "b".repeat(64)]));
  const checksums = Object.entries(sums).map(([name, hash]) => `${hash}  ${name}`).join("\n");
  let cask = renderHomebrewCask("1.2.3", sums);
  if (badCask) cask = cask.replace("download/v#{version}", "download/desktop-v#{version}");
  writeFileSync(path.join(root, "state.json"), JSON.stringify({ cli, desktop, latest, cask, checksums }));
  writeFileSync(path.join(bin, "git"), `#!/usr/bin/env node\nconsole.log('${sha}\\trefs/tags/x');`, { mode: 0o755 });
  writeFileSync(path.join(bin, "gh"), `#!/usr/bin/env node
const fs=require('node:fs'), path=require('node:path');
const args=process.argv.slice(2), root=process.env.FIXTURE, state=JSON.parse(fs.readFileSync(path.join(root,'state.json'),'utf8'));
fs.appendFileSync(path.join(root,'calls'),JSON.stringify(['gh',...args])+'\\n');
if(args[0]==='release'&&args[1]==='view') console.log(JSON.stringify(args[2].startsWith('desktop-')?state.desktop:state.cli));
else if(args[0]==='api'&&args.join(' ').includes('homebrew-deepzora')) console.log(state.cask);
else if(args[0]==='release'&&args[1]==='download'&&args.includes('SHA256SUMS')) fs.writeFileSync(args[args.indexOf('--output')+1],state.checksums);
else process.exit(4);
`, { mode: 0o755 });
  writeFileSync(path.join(bin, "npm"), `#!/usr/bin/env node
const fs=require('node:fs'), path=require('node:path');
const args=process.argv.slice(2), root=process.env.FIXTURE, state=JSON.parse(fs.readFileSync(path.join(root,'state.json'),'utf8'));
fs.appendFileSync(path.join(root,'calls'),JSON.stringify(['npm',...args])+'\\n');
const [spec]=args.slice(1); const name=spec.slice(0,spec.lastIndexOf('@'));
const out={name,version:'1.2.3',deepzoraCandidateSha:'${sha}',gitHead:'${sha}','dist.integrity':'sha512-x'};
for (const field of args) if (field.startsWith('dist-tags.')) { const tag=field.slice(10); const value=tag==='latest'?state.latest:undefined; if(value) out[field]=value; }
console.log(JSON.stringify(out));
`, { mode: 0o755 });
  writeFileSync(path.join(bin, "go"), `#!/usr/bin/env node
const args=process.argv.slice(2);
require('node:fs').writeFileSync(args[3], JSON.stringify({version:'v1.2.3',platforms:{'darwin-arm64':{url:'https://github.com/SivanCola/deepzora/releases/download/desktop-v1.2.3/x',sig:'s'}}}));
`, { mode: 0o755 });
  const env = { ...process.env, FIXTURE: root, PATH: `${bin}:${process.env.PATH}`, RELEASE_REPOSITORY: "SivanCola/deepzora", RELEASE_VERSION: "1.2.3", CLI_TAG: "v1.2.3", DESKTOP_TAG: "desktop-v1.2.3", RELEASE_EXPECTED_SHA: sha, RELEASE_OPERATION: "publish", VERIFY_PUBLIC_POINTERS: "true", VERIFY_ATTEMPTS: "1", VERIFY_DELAY_SECONDS: "0", RELEASE_LEDGER_OUTPUT: path.join(root, "ledger.json") };
  return {
    run: extra => spawnSync("bash", ["scripts/verify-stable-release-artifacts.sh"], { env: { ...env, ...extra }, encoding: "utf8" }),
    calls: () => { try { return readFileSync(path.join(root, "calls"), "utf8"); } catch { return ""; } },
    ledger: () => JSON.parse(readFileSync(env.RELEASE_LEDGER_OUTPUT, "utf8")),
  };
}

test("postflight with the variable unset still requires npm latest and the Homebrew cask", t => {
  const f = verifier(t), result = f.run({});
  assert.equal(result.status, 0, result.stderr);
  assert.match(f.calls(), /dist-tags\.latest/);
  assert.doesNotMatch(f.calls(), /legacy-v1/);
  assert.match(f.calls(), /homebrew-deepzora/);
  const ledger = f.ledger();
  assert.ok(ledger.surfaces.homebrew);
  assert.ok(ledger.surfaces.npm.packages.every(item => item.latest === "1.2.3" && item.distTag === undefined));
  const stale = verifier(t, { latest: "1.2.2" }).run({});
  assert.notEqual(stale.status, 0);
});

test("postflight rejects missing versioned downloads and incorrect Homebrew release references", t => {
  assert.notEqual(verifier(t, { missingNamed: true }).run({}).status, 0);
  const bad = verifier(t, { badCask: true }).run({});
  assert.notEqual(bad.status, 0);
  assert.match(bad.stderr, /invalid Homebrew CLI reference/);
});
