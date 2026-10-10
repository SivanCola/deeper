import assert from "node:assert/strict";
import { test } from "node:test";
import { isFirstRelease } from "./github-release-bootstrap.mjs";

test("a new repository can prepare its first stable update pointer", () => {
  assert.equal(isFirstRelease([]), true);
  assert.equal(isFirstRelease([{ tag_name: "v2.0.0", draft: true }]), true);
  assert.equal(isFirstRelease([{ tag_name: "v2.0.0-rc.1", prerelease: true }]), true);
});

test("an existing stable CLI or desktop release cannot bypass pointer checks", () => {
  for (const tag_name of ["v2.0.0", "desktop-v2.0.0"]) {
    assert.equal(isFirstRelease([{ tag_name, draft: false, prerelease: false }]), false);
  }
});
