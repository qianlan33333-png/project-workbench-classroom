import {readdir,realpath,readFile,stat,lstat} from 'node:fs/promises';
import {join,sep} from 'node:path';
import {createHash} from 'node:crypto';
import { FileSystemSkillProvider } from '@deepseek-ai/dsh-skill-filesystem';

// Use the exact upstream project/user/bundled discovery and parser. Do not
// duplicate Skill parsing or require an Agent just to configure a project.
export function ownerSkillCatalog(ctx){
  const abort=new AbortController();
  const provider=new FileSystemSkillProvider(ctx,{signal:abort.signal,invalidate(){}},{agentsHome:process.env.DSH_HOME+'/agents'});
  ctx.effect(()=>()=>{abort.abort();return provider.dispose()},'workbench: skill catalog');
  const digest=value=>createHash('sha256').update(value).digest('hex');
  const editableFile=async(candidate,skill)=>{
    if(!['user-dsh','user-agents'].includes(candidate.source))return;
    const base=await realpath(join(process.env.DSH_HOME,...(candidate.source==='user-agents'?['agents']:[]),'skills'));
    if(!(await lstat(skill.path)).isFile())return;
    const path=await realpath(skill.path);
    if(!path.startsWith(base+sep))return;
    const raw=await readFile(path,'utf8');
    const header=raw.match(/^---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*\r?\n/)?.[0];
    if(header&&Buffer.byteLength(raw)<=128*1024)return {path,header,revision:digest(raw)};
  };
  return async(cwd,name,scope)=>{
    const result=await provider.list({cwd,signal:abort.signal});
    const candidates=Array.isArray(result)?result:result.candidates;
    if(!Array.isArray(result)&&!result.complete)throw Error('专家目录暂未读取完整，请刷新重试');
    const byName=new Map();
    for(const c of candidates){
      const project=c.source==='project-dsh'||c.source==='project-agents';
      if(scope==='shared'&&project||scope==='project'&&!project)continue;
      if(!byName.has(c.name)||byName.get(c.name).rank>c.rank)byName.set(c.name,c);
    }
    if(!name)return [...byName.values()].map(c=>({name:c.name,description:c.description,source:c.source,metadata:c.metadata,invocation:c.invocation})).sort((a,b)=>a.name.localeCompare(b.name));
    const candidate=byName.get(name);if(!candidate)throw Error('该项目没有这个专家');
    const skill=await provider.get(candidate,{cwd,signal:abort.signal});
    if(!skill)throw Error('专家方法暂不可读');
    const writable=await editableFile(candidate,skill);
    const references=[];
    if(skill.resourceBase?.kind==='directory'){
      const base=await realpath(skill.resourceBase.path);const folder=join(base,'references');
      let entries=[];try{entries=await readdir(folder)}catch(e){if(e.code!=='ENOENT')throw e}
      let bytes=0;
      for(const name of entries.filter(n=>n.endsWith('.md')).sort().slice(0,20)){
        const path=await realpath(join(folder,name));if(!path.startsWith(base+sep))continue;
        const info=await stat(path);if(!info.isFile()||info.size>65536||bytes+info.size>262144)continue;
        bytes+=info.size;references.push({name,content:await readFile(path,'utf8')});
      }
    }
    return{name:skill.name,description:skill.description,content:skill.content,references,source:skill.source,metadata:skill.metadata,invocation:skill.invocation,revision:writable?.revision};
  };
}
