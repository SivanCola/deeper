import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { beneath, internalRoots, listPackages, runGoTest } from "./go-test-groups.mjs";

// Start the long filesystem suites immediately on independent runners instead
// of leaving them behind hundreds of short packages in the residual queue.
export const isolatedGroups = ["acp", "agent", "boot", "bot", "control", "serve", "session", "worktree"];
// The complete control package exceeds its 8m alarm on hosted Windows runners.
// Complementary selectors retain all tests, examples and fuzz seeds, including
// future names outside the conventional uppercase range.
export const controlShards = {
  "A-M": ["-skip", "^(Test|Example|Fuzz)[N-Z]"],
  "N-Z": ["-run", "^(Test|Example|Fuzz)[N-Z]"],
};

export function verifyControlInventory(output) {
  const names = output.split(/\r?\n/).filter(name => /^(Test|Example|Fuzz)\S*$/u.test(name));
  if (!names.length) throw new Error("Go returned no control test inventory");
  const counts = Object.fromEntries(Object.keys(controlShards).map(shard => [shard, 0]));
  for (const name of names) {
    const owners = Object.entries(controlShards).filter(([, [flag, pattern]]) =>
      new RegExp(pattern).test(name) === (flag === "-run"));
    if (owners.length !== 1) throw new Error(`${name} belongs to ${owners.length} control shards`);
    counts[owners[0][0]]++;
  }
  for (const [shard, count] of Object.entries(counts)) {
    if (!count) throw new Error(`Empty control test shard: ${shard}`);
  }
  return counts;
}
const smokeRoots = internalRoots(
  "appidentity", "checkpoint", "cli", "desktoplauncher", "extension/sidecar",
  "desktopinstance", "filelock", "fileops", "fileutil", "hook", "identitylock", "instruction", "mcplaunch", "notify",
  // persistentshell exercises native Git Bash pipes and PowerShell named pipes;
  // Windows is the only lane that can prove the actual Windows runtimes.
  "persistentshell", "proc",
  "lsp", "pathidentity", "projectiondb", "remote", "repair", "sandbox", "sessioncatalog", "sqliteuri", "sysproxy",
  "topicstate", "winaclresidue", "workspacelease",
).concat("deepzora/cmd");

export function selectPackages(packages, group) {
  if (!["full", "smoke", ...isolatedGroups].includes(group)) {
    throw new Error(`Unknown Windows test group: ${group}`);
  }
  const isolated = pkg => isolatedGroups.some(name => beneath(pkg, `deepzora/internal/${name}`));
  return packages.filter(pkg => {
    if (isolatedGroups.includes(group)) return beneath(pkg, `deepzora/internal/${group}`);
    return !isolated(pkg) && (group === "full" || smokeRoots.some(root => beneath(pkg, root)));
  });
}

export function testArgs(packages, group, controlShard) {
  const selected = selectPackages(packages, group);
  if (selected.length === 0) throw new Error(`Empty Windows test group: ${group}`);
  const args = ["test", "-p", isolatedGroups.includes(group) ? "1" : "4", "-timeout=8m"];
  // Bot has produced process-level Windows exits without a Go stack or test
  // name. JSON preserves the last start/output event and the subprocess exit
  // status while keeping the whole package in one process (no retry or split).
  if (group === "bot") args.push("-json");
  if (controlShard) {
    if (group !== "control") throw new Error("Test shards are only supported for control");
    if (!Object.hasOwn(controlShards, controlShard)) throw new Error(`Unknown control shard: ${controlShard}`);
    args.push("-json", ...controlShards[controlShard]);
  }
  return [...args, ...selected];
}

function main(group, controlShard) {
  const { packages, status } = listPackages();
  if (!packages) return status;
  const selected = selectPackages(packages, group);
  const args = testArgs(packages, group, controlShard);
  if (controlShard) {
    const listed = spawnSync("go", ["test", "-list", ".", ...selected], { encoding: "utf8", maxBuffer: 16 << 20 });
    if (listed.error) throw listed.error;
    if (listed.status !== 0) {
      process.stderr.write(listed.stdout + listed.stderr);
      return listed.status ?? 1;
    }
    console.log("Control test partition:", verifyControlInventory(listed.stdout));
  }
  return runGoTest(`Windows ${group}${controlShard ? ` ${controlShard}` : ""}`, selected, args);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv[2], process.argv[3]);
}
