import assert from "node:assert/strict";
import { en } from "../locales/en";
import { zh } from "../locales/zh";

assert.equal(en["notice.deliveryIncompleteTitle"], "Checks are not complete");
assert.equal(en["notice.deliveryIncompleteBody"], "The response was generated, but required verification or review is still incomplete.");
assert.equal(en["notice.deliveryIncompleteContinue"], "Continue checks");
assert.equal(en["notice.deliveryRequirementTask"], "remaining implementation work");

assert.equal(zh["notice.deliveryIncompleteTitle"], "检查尚未完成");
assert.equal(zh["notice.deliveryIncompleteBody"], "内容已经生成，但必要的验证或复审仍未完成。");
assert.equal(zh["notice.deliveryIncompleteContinue"], "继续检查");
assert.equal(zh["notice.deliveryRequirementTask"], "剩余实施工作");

console.log("  PASS  final readiness recovery copy is aligned across locales");
