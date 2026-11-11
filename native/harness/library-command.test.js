import {test} from "node:test";
import assert from "node:assert/strict";
import {nativeLibraryCommand,activeLibraryCommand} from "./library-command.js";

const message=(source,text="给自动化运营开通黄小璨业务口径的只读权限")=>({role:"user",source,content:[{type:"text",text}]});
test("library command requires a newly observed native owner RPC",()=>{
 const native=message({kind:"user",rpcId:"rpc-1"});
 const event={...nativeLibraryCommand(native),received:Date.now()};
 assert.equal(activeLibraryCommand([native],event)?.requestId,"rpc-1");
 assert.equal(activeLibraryCommand([native],undefined),undefined);
 assert.equal(activeLibraryCommand([native],{...event,received:0}),undefined);
 assert.equal(activeLibraryCommand([native,message({kind:"workbench-delegation",runId:"test"})],event),undefined);
 assert.equal(activeLibraryCommand([message({kind:"user",rpcId:"rpc-1"},"another instruction")],event),undefined);
 assert.equal(nativeLibraryCommand(message({kind:"user"})),undefined);
 assert.equal(nativeLibraryCommand({...native,content:[...native.content,{type:"image",data:"fixture"}]}),undefined);
});
