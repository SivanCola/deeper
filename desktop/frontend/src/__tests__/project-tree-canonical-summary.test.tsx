import assert from "node:assert/strict";
import React, { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useProjectTreeSessionSummary } from "../lib/useProjectTreeSessionSummary";
import { installDesktopHostStub } from "./desktopHostStub";
import type { ProjectNode } from "../lib/types";
import type { SessionRef } from "../lib/sessionRef";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, {window:dom.window,document:dom.window.document,IS_REACT_ACT_ENVIRONMENT:true});
const root = createRoot(document.getElementById("root")!);
const row = (id:string, generation=1): ProjectNode => ({key:id,kind:"global_topic",label:id,topicId:"shared",session:{hostId:"local",sessionId:id},sessionPath:`session-id:${id}`,lifecycleGeneration:generation,children:[]});
const requests: SessionRef[] = [];
const gates: ((node:ProjectNode)=>void)[] = [];
installDesktopHostStub({GetSessionSummary:(ref:SessionRef)=>{ requests.push(ref); return new Promise(resolve=>gates.push(resolve)); }});
let tree:ProjectNode[];
let update:React.Dispatch<React.SetStateAction<ProjectNode[]>>;
const excluded = new Set<string>();
const exclusions = () => excluded;
function Probe({id}:{id?:string}) {
  const [nodes,setNodes]=useState<ProjectNode[]>([{key:"global",kind:"global_folder",label:"Global",children:[]}]);
  tree=nodes; update=setNodes;
  useProjectTreeSessionSummary({tree:nodes,setTree:setNodes,scope:"global",session:id?{hostId:"local",sessionId:id}:undefined,remote:false,excluded:exclusions});
  return null;
}
try {
  await act(async()=>root.render(<Probe id="a"/>));
  await act(async()=>root.render(<Probe id="b"/>));
  await act(async()=>gates[0](row("a")));
  assert.equal(tree![0].children?.length,0,"navigation cancels the old completion even if its row is still valid");
  await act(async()=>gates[1](row("b")));
  assert.deepEqual(tree![0].children?.map(n=>n.label),["b"]);
  await act(async()=>root.render(<Probe id="a"/>));
  await act(async()=>update(previous=>previous.map(p=>({...p,label:"unrelated runtime update"}))));
  assert.equal(requests.length,3,"unrelated tree updates do not restart pending summary");
  await act(async()=>gates[2](row("b")));
  assert.deepEqual(tree![0].children?.map(n=>n.label),["b"],"wrong session response is rejected");
  await act(async()=>root.render(<Probe id="c"/>));
  excluded.add("ref\0local\0c");
  await act(async()=>gates[3](row("c")));
  assert.deepEqual(tree![0].children?.map(n=>n.label),["b"],"a late summary cannot revive an archived session");
  await act(async()=>root.render(<Probe id="d"/>));
  await act(async()=>update(previous=>previous.map(p=>({...p,children:[...(p.children??[]),row("d",9)]}))));
  await act(async()=>gates[4](row("d",1)));
  assert.equal(tree![0].children?.find(n=>n.session?.sessionId==="d")?.lifecycleGeneration,9,"directory arrival retires an older summary");
  await act(async()=>root.render(<Probe id="e"/>));
  await act(async()=>root.unmount());
  await act(async()=>gates[5](row("e")));
} finally { dom.window.close(); }
console.log("PASS sidebar summaries: navigation, lifecycle, directory arrival and unmount races");
