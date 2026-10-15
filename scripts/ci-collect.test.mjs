import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync, rmSync, existsSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

test("collector archives full root evidence for current and historical jobs, but not intentional skips", t => {
  const root = mkdtempSync(path.join(os.tmpdir(), "ci collector fixture "));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const workflow = path.join(root, "ci.yml");
  writeFileSync(workflow, "jobs:\n  windows-root:\n    needs: []\n");
  writeFileSync(path.join(root, "gh"), `#!/usr/bin/env node
const fs = require('node:fs'), path = require('node:path');
const a = process.argv.slice(2), stamp = n => new Date(Date.UTC(2026,0,1,0,0,n)).toISOString();
const event = {Action:'pass',Package:'deepzora/example',Test:'TestExample',Elapsed:2};
if(a[0]==='run' && a[1]==='download') {
  if(process.env.FIXTURE_SKIP==='true') throw Error('download attempted for skipped job');
  const folder=path.join(a[a.indexOf('--dir')+1], 'windows-root-partition-1-42');
  fs.mkdirSync(folder,{recursive:true}); fs.writeFileSync(path.join(folder,'tests.jsonl'),JSON.stringify(event)+'\\n');
} else if(a[1]==='--help') { console.log('fixture API');
} else if(a[1].includes('/jobs/7/logs')) { console.log(JSON.stringify(event));
} else if(a[1].includes('/jobs?')) { console.log(JSON.stringify([{jobs:[{id:7,name:process.env.FIXTURE_JOB,
  conclusion:process.env.FIXTURE_SKIP==='true'?'skipped':'success',started_at:stamp(1),completed_at:stamp(5),steps:[]}]}]));
} else { console.log(JSON.stringify({id:42,head_sha:'a'.repeat(40),run_attempt:1,created_at:stamp(0),run_started_at:stamp(0)})); }
`, { mode: 0o755 });
  for (const [i, [name, skip]] of [["windows-root", false], ["windows-root (partition 1)", false], ["windows-root", true]].entries()) {
    const output = path.join(root, `output-${i}`);
    const result = spawnSync(process.execPath, ["scripts/ci-collect.mjs", "--repository", "example/repo", "--run", "42",
      "--workflow", workflow, "--output", output], {
      env: { ...process.env, PATH: `${root}${path.delimiter}${process.env.PATH}`, FIXTURE_JOB: name, FIXTURE_SKIP: String(skip), GITHUB_STEP_SUMMARY: "" },
      encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.deepEqual(JSON.parse(readFileSync(path.join(output, "timings.json"))).warnings, []);
    const evidence = path.join(output, "root-partitions/windows-root-partition-1-42/tests.jsonl.timings.json");
    assert.equal(existsSync(evidence), !skip);
    if (!skip) assert.equal(JSON.parse(readFileSync(evidence)).topLevel[0].name, "TestExample");
  }
});
