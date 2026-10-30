import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import {z} from 'zod';
const server=new McpServer({name:'workbench-acceptance',version:'1'});
server.registerTool('echo',{description:'Return the acceptance token from a global tool.',inputSchema:{text:z.string()}},async({text})=>({content:[{type:'text',text:'GLOBAL_MCP:'+text}]}));
await server.connect(new StdioServerTransport());
