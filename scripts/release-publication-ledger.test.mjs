import assert from "node:assert/strict";
import test from "node:test";
import { createCoreLedger, createPointerLedger, mergeLedgers, npmPackageNames, ownsStablePointer } from "./release-publication-ledger.mjs";

const sha = "a".repeat(40);
const release = { isDraft: false, isPrerelease: false, assets: [{ name: "asset.zip", size: 42, digest: "sha256:abc" }] };
const packages = latest => npmPackageNames.map(name => ({
  name,
  version: "1.2.3",
  deepzoraCandidateSha: sha,
  gitHead: sha,
  integrity: "sha512-example",
  latest,
}));

test("records immutable files and exact current pointers", () => {
  const core = createCoreLedger({
    version: "1.2.3", sourceSHA: sha, operation: "publish",
    cliRelease: release, desktopRelease: release, npmPackages: packages("1.2.3"),
  });
  assert.equal(core.surfaces.tags.items.length, 3);
  assert.ok(core.surfaces.npm.packages.every(item => item.pointerState === "public-entry-updated"));
  assert.equal(core.surfaces.cli.assets[0].digest, "sha256:abc");
});

test("recovery preserves newer public pointers", () => {
  const core = createCoreLedger({
    version: "1.2.3", sourceSHA: sha, operation: "recover",
    cliRelease: release, desktopRelease: release, npmPackages: packages("1.3.0"),
  });
  assert.ok(core.surfaces.npm.packages.every(item => item.pointerState === "newer-entry-preserved"));
  assert.throws(() => createCoreLedger({
    version: "1.2.3", sourceSHA: sha, operation: "publish",
    cliRelease: release, desktopRelease: release, npmPackages: packages("1.3.0"),
  }), /npm latest is inconsistent/);
});

test("pointer evidence merges only for the same candidate", () => {
  const core = createCoreLedger({
    version: "1.2.3", sourceSHA: sha, operation: "publish",
    cliRelease: release, desktopRelease: release, npmPackages: packages("1.2.3"),
  });
  const pointers = createPointerLedger({
    version: "1.2.3", sourceSHA: sha, operation: "publish", manifest: { version: "v1.2.3" }, homebrewVersion: "1.2.3",
  });
  assert.deepEqual(Object.keys(mergeLedgers(core, pointers).surfaces).sort(), ["cli", "desktop", "homebrew", "npm", "stableManifest", "tags"]);
  assert.throws(() => mergeLedgers(core, { ...pointers, sourceSHA: "b".repeat(40) }), /one release/);
  assert.throws(() => createPointerLedger({
    version: "1.2.3", sourceSHA: sha, operation: "publish", manifest: { version: "v1.2.3" }, homebrewVersion: "1.2.2",
  }), /Homebrew cask/);
});

test("pointer ownership requires an exact release or a proven newer recovery pointer", () => {
  for (const operation of ["publish", "recover"]) {
    assert.equal(ownsStablePointer("1.2.3", operation, { version: "v1.2.3" }), true);
    for (const version of [undefined, "", "v1.2.2", "1.2.3", "v1.2.3-beta.1"]) {
      assert.throws(() => ownsStablePointer("1.2.3", operation, { version }));
    }
  }
  assert.equal(ownsStablePointer("1.2.3", "recover", { version: "v1.10.0" }), false);
  assert.throws(() => ownsStablePointer("1.2.3", "publish", { version: "v1.10.0" }));
  assert.throws(() => ownsStablePointer("1.2.3", "unknown", { version: "v1.2.3" }));
});

test("retired publication observations cannot bypass current public pointers", () => {
 const args = {version:"1.2.3", sourceSHA:sha, operation:"publish", cliRelease:release, desktopRelease:release};
 assert.throws(() => createCoreLedger({...args,npmPackages:packages("1.2.3").map(p => ({...p,distTag:"legacy-v1"}))}), /retired/);
 assert.throws(() => createCoreLedger({...args,npmPackages:packages("1.2.3").map(p => ({...p,deepzoraCandidateSha:undefined}))}), /candidate/);
 assert.throws(() => createPointerLedger({...args,manifest:{version:"v1.2.3"},homebrewVersion:"frozen"}), /Homebrew/);
});
