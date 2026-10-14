import assert from "node:assert/strict";
import { mergeProjectTopicPage } from "../lib/projectTreeTopic";
import { createProjectTreeRuntimeProjection } from "../lib/projectTreeRuntime";
import { projectSessionIdentity, projectSessionRowKey, sameProjectSession } from "../lib/projectSessionIdentity";
import type { ProjectNode } from "../lib/types";

const id="desktop-manual-8a4fd6104fe2763ef0e0b60a879edb70";
const full:ProjectNode={key:`canonical_${id}`,kind:"global_topic",label:"你是谁",topicId:"shared",session:{hostId:"local",sessionId:id},sessionPath:`session-id:${id}`,pinned:true,sortOrder:2,lifecycleGeneration:4,children:[]};
const incomplete:ProjectNode={key:"global_session_59fd459457c647b0",kind:"global_topic",label:"New session",topicId:"shared",sessionPath:full.sessionPath,children:[]};
assert.equal(projectSessionIdentity(full),projectSessionIdentity(incomplete));
for (const rows of [[full,incomplete],[incomplete,full]]) {
  const merged=mergeProjectTopicPage(rows,[incomplete],true);
  assert.deepEqual(merged,[full],"a stale completion or resident duplicate retains authoritative title, pin and order");
  const projected=createProjectTreeRuntimeProjection().apply([{key:"global",kind:"global_folder",label:"Global",children:rows}], [{scope:"global",node:{...full,open:true}}]);
  assert.equal(projected[0].children?.length,1);
  assert.equal(projected[0].children?.[0].label,"你是谁");
}
const sibling={...full,key:"fork",session:{hostId:"local",sessionId:"fork"},sessionPath:"session-id:fork"};
const remote={...full,key:"remote",session:{hostId:"remote",sessionId:id}};
assert.equal(sameProjectSession(full,sibling),false);
assert.equal(sameProjectSession(full,remote),false);
assert.notEqual(projectSessionRowKey({...full,identityAliases:["tab\0local\0reused"]}),projectSessionRowKey({...sibling,identityAliases:["tab\0local\0reused"]}),"a reused surface never lends a previous session's mounted row key");
assert.equal(mergeProjectTopicPage([full],[sibling,remote],true).length,3,"a shared topic or session id on another host is distinct");
const newer={...full,label:"Restored name",lifecycleGeneration:10};
assert.deepEqual(mergeProjectTopicPage([newer],[full],true),[newer]);
assert.deepEqual(mergeProjectTopicPage([newer],[full],false),[newer],"replacement pages also reject old lifecycle metadata");
const residentProjection=createProjectTreeRuntimeProjection();
residentProjection.apply([{key:"global",kind:"global_folder",label:"Global",children:[newer]}],[]);
const staleRuntime=residentProjection.apply([{key:"global",kind:"global_folder",label:"Global",children:[]}],[{scope:"global",node:{...full,topicId:"old-topic",label:"Old name",running:true}}]);
assert.equal(staleRuntime[0].children?.[0].label,"Restored name");
assert.equal(staleRuntime[0].children?.[0].topicId,newer.topicId);
assert.equal(staleRuntime[0].children?.[0].lifecycleGeneration,10);
assert.equal(staleRuntime[0].children?.[0].running,undefined,"stale runtime cannot restore activity on a newer lifecycle");
assert.equal(sameProjectSession(full,{...incomplete,sessionPath:"/tmp/desktop-manual-8a4fd6104fe2763ef0e0b60a879edb70.jsonl"}),false,"file names never prove canonical identity");
console.log("PASS canonical sidebar dedupe: actual session fields, metadata retention, forks and hosts");
