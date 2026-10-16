import assert from "node:assert/strict";
import { installDesktopHostStub } from "./desktopHostStub";
import { desktopHost } from "../lib/desktopHost";

Object.defineProperty(globalThis, "window", { configurable: true, value: {} });
const commands: { Version: () => Promise<string>; TranscriptContentForTab?: () => Promise<unknown> } = { Version: async () => "test", TranscriptContentForTab: undefined };
const stub = installDesktopHostStub(commands);
assert.equal(typeof desktopHost().app?.Version, "function");
assert.equal(desktopHost().app?.TranscriptContentForTab, undefined, "an unimplemented optional method must not advertise protocol support");
commands.TranscriptContentForTab = async () => ({});
assert.equal(typeof desktopHost().app?.TranscriptContentForTab, "function", "a newly installed method is discovered without reinstalling the host");
delete commands.TranscriptContentForTab;
assert.equal(desktopHost().app?.TranscriptContentForTab, undefined);
stub.uninstall();
console.log("desktop host stub advertises only callable commands");
