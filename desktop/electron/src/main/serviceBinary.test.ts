import assert from "node:assert/strict";
import { test } from "node:test";
import { resolveServiceBinary } from "./serviceBinary.js";

const files = (...present: string[]) => (path: string) => present.includes(path);

test("a configured service path wins without probing the install tree", () => {
  const result = resolveServiceBinary({
    env: { DEEPZORA_DESKTOP_SERVICE: " C:\\dev\\deepzora-desktop.exe " },
    platform: "win32",
    execPath: String.raw`C:\Apps\Deepzora\versions\v1.38.6\app\Deepzora.exe`,
    resourcesPath: String.raw`C:\Apps\Deepzora\versions\v1.38.6\app\resources`,
    isFile: () => { throw new Error("must not probe"); },
  });
  assert.deepEqual(result, { binary: String.raw`C:\dev\deepzora-desktop.exe`, probed: [] });
});

test("Windows shells find the service beside app/ in versioned and flat installs", () => {
  for (const [execPath, service] of [
    [String.raw`C:\Apps\Deepzora test\versions\v1.38.6\app\Deepzora.exe`, String.raw`C:\Apps\Deepzora test\versions\v1.38.6\deepzora-desktop.exe`],
    [String.raw`C:\Apps\Deepzora test\app\Deepzora.exe`, String.raw`C:\Apps\Deepzora test\deepzora-desktop.exe`],
  ]) {
    const resourcesPath = win32Resources(execPath);
    const result = resolveServiceBinary({ env: {}, platform: "win32", execPath, resourcesPath, isFile: files(service, win32Alias(execPath)) });
    assert.equal(result.binary, service);
    assert.deepEqual(result.probed, [service, `${resourcesPath}\\service\\deepzora-desktop.exe`]);
  }
});

test("macOS keeps the bundled Resources/service binary and never probes a sibling", () => {
  const execPath = "/Applications/Deepzora.app/Contents/MacOS/Deepzora";
  const resourcesPath = "/Applications/Deepzora.app/Contents/Resources";
  const bundled = `${resourcesPath}/service/deepzora-desktop`;
  const result = resolveServiceBinary({ env: {}, platform: "darwin", execPath, resourcesPath, isFile: files(bundled, "/Applications/Deepzora.app/Contents/MacOS/deepzora-desktop") });
  assert.deepEqual(result, { binary: bundled, probed: [bundled] });
});

test("Linux shells mirror the Go bootstrap table for portable and packaged layouts", () => {
  for (const [execPath, service] of [
    ["/opt/deepzora/app/Deepzora", "/opt/deepzora/deepzora-desktop"],
    ["/opt/deepzora/versions/v1.39.0/app/Deepzora", "/opt/deepzora/versions/v1.39.0/deepzora-desktop"],
    ["/usr/lib/deepzora/app/Deepzora", "/usr/bin/deepzora-desktop"],
  ]) {
    const resourcesPath = `${execPath.slice(0, -"/Deepzora".length)}/resources`;
    const result = resolveServiceBinary({ env: {}, platform: "linux", execPath, resourcesPath, isFile: files(service) });
    assert.equal(result.binary, service, execPath);
    assert.ok(result.probed.includes(service));
  }
});

test("a missing service keeps the bundled location and reports every probe", () => {
  const execPath = String.raw`C:\Apps\Deepzora\versions\v1.38.6\app\Deepzora.exe`;
  const resourcesPath = win32Resources(execPath);
  const result = resolveServiceBinary({ env: { DEEPZORA_DESKTOP_SERVICE: "" }, platform: "win32", execPath, resourcesPath, isFile: () => false });
  assert.equal(result.binary, `${resourcesPath}\\service\\deepzora-desktop.exe`);
  assert.deepEqual(result.probed, [String.raw`C:\Apps\Deepzora\versions\v1.38.6\deepzora-desktop.exe`, result.binary]);
});

test("an executable outside an app directory only checks the bundled location", () => {
  const result = resolveServiceBinary({ env: {}, platform: "win32", execPath: String.raw`C:\Other\electron.exe`, resourcesPath: String.raw`C:\Other\resources`, isFile: () => false });
  assert.deepEqual(result.probed, [String.raw`C:\Other\resources\service\deepzora-desktop.exe`]);
});

function win32Resources(execPath: string): string {
  return `${execPath.slice(0, -"\\Deepzora.exe".length)}\\resources`;
}

// The install root also carries a launcher alias named Deepzora.exe; it must
// never be mistaken for the service.
function win32Alias(execPath: string): string {
  const appDir = execPath.slice(0, -"\\Deepzora.exe".length);
  return `${appDir.slice(0, -"\\app".length)}\\Deepzora.exe`;
}
