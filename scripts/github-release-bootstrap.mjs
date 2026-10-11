import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

// A fresh repository has no update pointer. Once a stable product release exists,
// a missing/broken pointer is a failure and must never be treated as bootstrap.
export function isFirstRelease(releases) {
  return !releases.some(release => !release.draft && !release.prerelease
    && /^(desktop-)?v\d+\.\d+\.\d+$/.test(release.tag_name));
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  const pages = JSON.parse(execFileSync("gh", ["api", "--paginate", "--slurp",
    "repos/SivanCola/deepzora/releases?per_page=100"], { encoding: "utf8" }));
  console.log(isFirstRelease(pages.flat()) ? "empty" : "ready");
}
