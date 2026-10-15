import assert from "node:assert/strict";
import test from "node:test";
import { desktopPlan, validateDesktopPlan, verifyDesktopResults } from "./ci-plan.mjs";

test("desktop gate requires each planned job, including detection", () => {
  for (const [desktop, native] of [[false, false], [true, false], [true, true]]) {
    const plan = desktopPlan({ desktop, native });
    const needs = { changes: { result: "success" } };
    for (const name of ["desktop-frontend", "desktop-tests", "desktop-browser"]) needs[name] = { result: desktop ? "success" : "skipped" };
    needs["desktop-go"] = { result: native ? "success" : "skipped" };
    assert.doesNotThrow(() => verifyDesktopResults(plan, needs));
    for (const name of Object.keys(needs)) {
      for (const status of ["failure", "cancelled", "", "skipped", "success"]) {
        if (status === needs[name].result) continue;
        assert.throws(() => verifyDesktopResults(plan, { ...needs, [name]: { result: status } }), name);
      }
      assert.throws(() => verifyDesktopResults(plan, { ...needs, [name]: undefined }), name);
    }
  }
});

test("malformed, missing and inconsistent plans cannot approve skipped checks", () => {
  const plan = desktopPlan({ desktop: true, native: true });
  for (const value of [null, {}, { ...plan, schemaVersion: 2 }, { ...plan, native: "true" }, { ...plan, extra: true }, { ...plan, build: false }, { ...plan, tests: false }]) {
    assert.throws(() => validateDesktopPlan(value));
  }
});

test("missing job outputs cannot turn an otherwise valid execution plan into success", () => {
  for (const native of [false, true]) {
    const plan = desktopPlan({ desktop: true, native });
    for (const needs of [undefined, null, {}, { changes: { result: "success" } }]) {
      assert.throws(() => verifyDesktopResults(plan, needs));
    }
  }
});
