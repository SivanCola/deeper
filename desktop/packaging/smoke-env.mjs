import { join } from "node:path";

// A disposable home is sufficient isolation. Development mode would bypass
// the very build mismatch that a production startup smoke must catch.
export function packagedSmokeEnv(parent, home) {
  const env = { ...parent };
  for (const key of Object.keys(env)) {
    if (/^DEEPER_/i.test(key) || /^(NODE_OPTIONS|ELECTRON_RUN_AS_NODE)$/i.test(key)) delete env[key];
  }
  return {
    ...env,
    DEEPER_HOME: home,
    DEEPER_STATE_HOME: home,
    DEEPER_CACHE_HOME: join(home, "cache"),
  };
}
