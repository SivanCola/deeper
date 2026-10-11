import { join } from "node:path";

// A disposable home is sufficient isolation. Development mode would bypass
// the very build mismatch that a production startup smoke must catch.
export function packagedSmokeEnv(parent, home) {
  const env = { ...parent };
  for (const key of Object.keys(env)) {
    if (/^DEEPZORA_/i.test(key) || /^(NODE_OPTIONS|ELECTRON_RUN_AS_NODE)$/i.test(key)) delete env[key];
  }
  return {
    ...env,
    DEEPZORA_HOME: home,
    DEEPZORA_STATE_HOME: home,
    DEEPZORA_CACHE_HOME: join(home, "cache"),
  };
}
