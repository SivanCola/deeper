import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { readLinuxArchiveMember, readProcessPipeline } from "./linux-members.mjs";

const node = (script, args = []) => [process.execPath, ["-e", script, ...args]];
const echo = node("process.stdin.pipe(process.stdout)");

test("pipeline transports argv literally and bounds extracted output", async () => {
  const input = "spaces 'quotes' ; $() & 中文";
  const result = await readProcessPipeline([node("process.stdout.write(process.argv[1])", [input]), echo]);
  assert.equal(result.toString(), input);
  await assert.rejects(readProcessPipeline([node("process.stdout.write('too much')")], { maxBytes: 3 }), /output budget/);
});

test("a failing producer invalidates successful consumer output", async () => {
  await assert.rejects(readProcessPipeline([node("process.stdout.write('partial member'); process.exitCode = 17"), echo]), /failed \(17\)/);
  await assert.rejects(readProcessPipeline([node("process.stdout.write('member')"), node("process.stdin.resume(); process.exitCode = 18")]), /failed \(18\)/);
  await assert.rejects(readProcessPipeline([[join(tmpdir(), "nonexistent-deepzora-archive-reader"), []]]), /ENOENT/);
});

test("tar extraction handles metacharacter paths without invoking a shell", { skip: process.platform === "win32" }, async (t) => {
  const dir = mkdtempSync(join(tmpdir(), "deepzora archive & "));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const member = "member '; $(touch injected) 中文";
  const artifact = join(dir, "archive $() &.tar.gz");
  writeFileSync(join(dir, member), "binary payload");
  const packed = spawnSync("tar", ["-czf", artifact, "-C", dir, "--", member]);
  assert.equal(packed.status, 0, packed.stderr?.toString());
  assert.equal((await readLinuxArchiveMember(artifact, member, "linux-tar")).toString(), "binary payload");
  await assert.rejects(readLinuxArchiveMember(artifact, "missing", "linux-tar"), /failed/);
});
