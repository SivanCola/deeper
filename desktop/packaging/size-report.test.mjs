import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { generateReport, inspectTree } from "./size-report.mjs";

test("size report classifies files, duplicates and links without following links", () => {
  const root = mkdtempSync(join(tmpdir(), "deepzora-size-report-"));
  mkdirSync(join(root, "bundle", "service"), { recursive: true });
  writeFileSync(join(root, "bundle", "service", "deepzora-desktop"), "service");
  writeFileSync(join(root, "bundle", "deepzora"), "same");
  writeFileSync(join(root, "bundle", "copy"), "same");
  symlinkSync("service/deepzora-desktop", join(root, "bundle", "service-link"));
  mkdirSync(join(root, "dist"));
  writeFileSync(join(root, "dist", "Deepzora-linux-amd64.tar.gz"), "archive");
  writeFileSync(join(root, "dist", "Deepzora-1.2.3-Linux-x64.tar.gz"), "archive");
  writeFileSync(join(root, "dist", "Deepzora-1.2.4-Linux-x64.tar.gz"), "archive");

  const tree = inspectTree(join(root, "bundle"));
  assert.equal(tree.fileCount, 3);
  assert.equal(tree.symlinks[0].target, "service/deepzora-desktop");
  assert.equal(tree.duplicates.length, 1);

  const report = generateReport({
    platform: "linux/amd64", version: "v1.2.3", sourceSHA: "a".repeat(40),
    bundle: join(root, "bundle"), dist: join(root, "dist"), output: join(root, "out"),
  });
  assert.equal(report.artifacts[0].name, "Deepzora-1.2.3-Linux-x64.tar.gz");
  assert.equal(report.artifacts.length, 1); // aliases and other versions are not counted twice
  assert.match(readFileSync(join(root, "out", "size-report.md"), "utf8"), /Bundle categories/);

  const baseline = join(root, "baseline.json");
  writeFileSync(baseline, JSON.stringify(report));
  const compared = generateReport({
    platform: "linux/amd64", version: "v1.2.4", sourceSHA: "b".repeat(40),
    bundle: join(root, "bundle"), dist: join(root, "dist"), output: join(root, "compared"), baseline,
  });
  assert.equal(compared.comparison.downloadBytesDelta, 0);
  assert.equal(compared.comparison.bundleLogicalBytesDelta, 0);
  const legacy = generateReport({
    platform: "linux/amd64", version: "v0.0.1", sourceSHA: "a".repeat(40),
    bundle: join(root, "bundle"), dist: join(root, "dist"), output: join(root, "legacy"),
  });
  writeFileSync(baseline, JSON.stringify(legacy));
  const migrated = generateReport({
    platform: "linux/amd64", version: "v1.2.4", sourceSHA: "b".repeat(40),
    bundle: join(root, "bundle"), dist: join(root, "dist"), output: join(root, "migrated"), baseline,
  });
  assert.equal(migrated.comparison.downloadBytesDelta, 0);
});
