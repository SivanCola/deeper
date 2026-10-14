import assert from "node:assert/strict";
import test from "node:test";
import { loadCatalog, releaseForVersion, renderGitHubRelease, validateCatalog } from "./release-notes.mjs";
import { validateReleaseEvent } from "./release-event.mjs";

test("first release is a reviewed baseline, resolved across tag namespaces", async () => {
  const catalog = await loadCatalog();
  assert.equal(catalog.releases.length, 1);
  for (const prefix of ["v", "desktop-v", "npm-v"]) assert.equal(releaseForVersion(catalog, `${prefix}0.0.1`).status, "reviewed");
  assert.equal(catalog.releases[0].previousRelease, undefined);
  for (const lang of ["en", "zh"]) {
    const text = renderGitHubRelease(catalog.releases[0], lang);
    assert.match(text, /Deepzora/);
    assert.doesNotMatch(text, /github\.com\/esengine/);
    assert.match(text, /Authenticode/);
    const suffix = lang === "zh" ? ".zh-CN" : "";
    assert.ok(text.includes(`https://github.com/SivanCola/deepzora/blob/main/CODE_SIGNING_POLICY${suffix}.md`));
    assert.ok(text.includes(`https://github.com/SivanCola/deepzora/blob/main/PRIVACY${suffix}.md`));
    assert.match(text, /Code signing policy/);
    assert.doesNotMatch(text, /Free code signing provided by SignPath/);
  }
});

test("validation enforces bilingual notes, targets and current build identities", async () => {
  const catalog = await loadCatalog();
  for (const [change, expected] of [
    [r => { r.title.zh = ""; }, /title.zh/],
    [r => { delete r.targetingVersion; }, /targetingVersion/],
    [r => { r.highlights[0].targets = ["cli", "desktop"]; }, /canonical order/],
    [r => { r.builds.npm = "1.0.1"; }, /builds.npm/],
    [r => { r.channel = "prerelease"; }, /channel/],
  ]) {
    const next = structuredClone(catalog); change(next.releases[0]);
    assert.throws(() => validateCatalog(next), expected);
  }
  const next = structuredClone(catalog.releases[0]);
  Object.assign(next, { version: "1.0.1", releaseId: "1.0.1", baseVersion: "1.0.1", builds: {cli:"v1.0.1", desktop:"v1.0.1", npm:"1.0.1"} });
  assert.throws(() => validateCatalog({schemaVersion:1,releases:[next, catalog.releases[0]]}), /previousRelease/);
  next.previousRelease = "0.0.1";
  assert.doesNotThrow(() => validateCatalog({schemaVersion:1,releases:[next, catalog.releases[0]]}));
});

test("publication evidence binds the reviewed version and candidate", async () => {
  const release = { ...(await loadCatalog()).releases[0], candidateSha: "a".repeat(40) };
  const event = {schemaVersion:1,releaseId:"0.0.1",channel:"stable",candidateSha:release.candidateSha,publishedAt:"2026-10-14T00:00:00Z",releaseNotesUrl:"https://github.com/SivanCola/deepzora/releases/tag/v0.0.1/",builds:release.builds};
  assert.doesNotThrow(() => validateReleaseEvent(event, release));
  assert.throws(() => validateReleaseEvent({...event,candidateSha:"b".repeat(40)},release), /candidateSha/);
});
