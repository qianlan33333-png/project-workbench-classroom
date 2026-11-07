import test from 'node:test';
import assert from 'node:assert/strict';
import {toolNamespace,namespaceOfTool} from './tool-namespace.js';
test('external source attribution cannot collide at the MCP namespace separator',()=>{
 const sources=new Map([['docs','public'],['docs-private','restricted']]);
 assert.equal(sources.get(namespaceOfTool('mcp__docs-private__read')),'restricted');
 assert.equal(sources.get(namespaceOfTool('mcp__docs__private__read')),'public');
 for(const name of ['docs_','docs__private','_docs','docs_private','',null])assert.throws(()=>toolNamespace(name));
 for(const name of sources.keys())assert.equal(toolNamespace(name),name);
 assert.equal(namespaceOfTool('mcp__docs___read'),'docs'); // raw tool _read, never a docs_ server
});
