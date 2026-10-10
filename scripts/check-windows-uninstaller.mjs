#!/usr/bin/env node
import assert from "node:assert/strict";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { nsisProjectDefines } from "../desktop/packaging/lib.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const temp = mkdtempSync(join(tmpdir(), "deeper nsis contract "));
try {
  const installer = join(temp, "windows", "installer");
  mkdirSync(join(temp, "bin"), { recursive: true });
  mkdirSync(installer, { recursive: true });
  copyFileSync(join(root, "desktop/build/windows/installer/project.nsi"), join(installer, "project.nsi"));
  copyFileSync(join(root, "desktop/build/windows/icon.ico"), join(temp, "windows", "icon.ico"));
  writeFileSync(join(installer, "deeper_project.nsh"), nsisProjectDefines({
    projectName: "deeper-desktop", companyName: "Deeper", productName: "Deeper", copyright: "Deeper",
  }, "v0.0.0-ci"));
  if (process.platform !== "win32") {
    writeFileSync(join(installer, "deeper_host.nsh"), `!define DEEPER_UNINST_FINALIZE '/bin/cp -f "%1" "deeper-uninstall.exe"'\n`);
  }
  for (const arch of ["AMD64", "ARM64"]) {
    mkdirSync(join(installer, "app"), { recursive: true });
    for (const name of ["deeper-desktop.exe", "deeper-launcher.exe", "deeper-guard.exe", "deeper-update-helper.exe", "deeper-cli.exe", "app/Deeper.exe"]) {
      writeFileSync(join(installer, name), Buffer.alloc(1024, 42));
    }
    const output = join(installer, "deeper-uninstall.exe");
    function compile(only, success = true) {
      rmSync(output, { force: true });
      const args = [`-DARG_DEEPER_${arch}_BINARY=deeper-desktop.exe`];
      if (only) args.push("-DARG_DEEPER_UNINSTALLER_ONLY");
      const result = spawnSync("makensis", [...args, "project.nsi"], { cwd: installer, encoding: "utf8" });
      assert.ifError(result.error);
      if (!success) {
        assert.notEqual(result.status, 0, "normal installer must require its payload");
        return;
      }
      assert.equal(result.status, 0, result.stdout + result.stderr);
      const bytes = readFileSync(output);
      assert.equal(bytes.subarray(0, 2).toString(), "MZ");
      const preprocessed = spawnSync("makensis", ["-PPO", ...args, "project.nsi"], { cwd: installer, encoding: "utf8" });
      assert.equal(preprocessed.status, 0, preprocessed.stderr);
      const start = preprocessed.stdout.search(/^Section\s+"?uninstall"?\s*$/m);
      assert.notEqual(start, -1, "preprocessed uninstall section is missing");
      // LogicLib allocates private jump labels across the entire installer.
      // Renumber by first occurrence while preserving every jump relationship.
      const labels = new Map();
      const instructions = preprocessed.stdout.slice(start).replace(/_LogicLib_[A-Za-z]+_[0-9]+/g, label => {
        if (!labels.has(label)) labels.set(label, label.replace(/[0-9]+$/, String(labels.size)));
        return labels.get(label);
      });
      return { bytes, instructions };
    }
    const original = compile(false);
    const bootstrap = compile(true);
    assert.equal(bootstrap.instructions, original.instructions, `${arch}: uninstall instructions changed`);
    rmSync(join(installer, "app"), { recursive: true });
    rmSync(join(installer, "deeper-desktop.exe"));
    assert.deepEqual(compile(true), bootstrap, `${arch}: uninstaller depends on install payload`);
    compile(false, false);
    console.log(`${arch}: shared uninstall instructions preserved; bootstrap needs no Electron payload`);
  }
} finally {
  rmSync(temp, { recursive: true, force: true });
}
