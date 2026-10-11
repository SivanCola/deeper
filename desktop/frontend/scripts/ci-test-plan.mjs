import { readdirSync, readFileSync } from "node:fs";

export const testManifest = JSON.parse(readFileSync(new URL("./test-manifest.json", import.meta.url), "utf8"));
export const ciUnitScripts = Object.keys(testManifest.groups).map(group => `test:${group}`);

export function discoveredTests(directory = "src/__tests__") {
  return readdirSync(directory).filter(name => /\.test\.tsx?$/.test(name)).sort();
}

export function testPlan(_scripts, discovered, group = "all") {
  if (group !== "all" && !testManifest.groups[group]) throw new Error(`unknown test group: ${group}`);
  const commands = group === "all" ? Object.values(testManifest.groups).flat() : testManifest.groups[group];
  const unique = new Map();
  function add(args) {
    const file = args.find(arg => /^src\/.*\.(?:test\.tsx?|tsx)$/.test(arg));
    if (file) args = ["node", "--import", "./scripts/svg-stub-register.mjs", "--import", "tsx", file];
    const key = file ?? JSON.stringify(args);
    unique.set(key, { key, args, serial: key.endsWith("history-performance-benchmark.tsx") });
  }
  if (group === "all") {
    add(["tsc", "--noEmit", "-p", "tsconfig.test.json"]);
    for (const file of discovered) add(["tsx", `src/__tests__/${file}`]);
  }
  for (const args of commands) add(args);
  return [...unique.values()];
}
