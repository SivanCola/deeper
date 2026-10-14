import { pathToFileURL } from "node:url";

// Explicit first-release exception approved by the maintainer. Credential
// absence never selects this mode, and subsequent versions require signing.
export function windowsSigningMode(version, channel) {
  if (!/^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$/.test(version ?? "")) {
    throw new Error("invalid Windows release version");
  }
  if (!["stable", "prerelease"].includes(channel)) throw new Error("invalid Windows release channel");
  return version.replace(/^v/, "") === "0.0.1" && channel === "stable" ? "unsigned" : "certum";
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv.length !== 4) throw new Error("usage: release-windows-policy.mjs VERSION CHANNEL");
  console.log(windowsSigningMode(process.argv[2], process.argv[3]));
}
