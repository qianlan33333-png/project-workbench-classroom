import {readdir,realpath,readFile,stat,lstat,writeFile,rename,unlink} from 'node:fs/promises';
import {join,sep} from 'node:path';
import {createHash,randomUUID} from 'node:crypto';
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
  const list=async(cwd,name,edit)=>{
    const result=await provider.list({cwd,signal:abort.signal});
    const candidates=Array.isArray(result)?result:result.candidates;
    if(!Array.isArray(result)&&!result.complete)throw Error('专家目录暂未读取完整，请刷新重试');
    const byName=new Map();
    for(const c of candidates)if(!byName.has(c.name)||byName.get(c.name).rank>c.rank)byName.set(c.name,c);
    if(!name)return [...byName.values()].map(c=>({name:c.name,description:c.description})).sort((a,b)=>a.name.localeCompare(b.name));
    const candidate=byName.get(name);if(!candidate)throw Error('该项目没有这个专家');
    const skill=await provider.get(candidate,{cwd,signal:abort.signal});
    if(!skill)throw Error('专家方法暂不可读');
    const writable=await editableFile(candidate,skill);
    if(edit){
      if(!writable)throw Object.assign(Error('此方法随安装发布，仅可阅读'),{status:403});
      if(edit.revision!==writable.revision)throw Object.assign(Error('方法已更新，请重新打开并合并修改'),{status:409});
      const text=edit.content;
      if(typeof text!=='string'||!text.trim()||Buffer.byteLength(writable.header+text)>128*1024)
        throw Object.assign(Error('请输入 128 KiB 以内的方法正文'),{status:400});
      const temp=writable.path+'.'+randomUUID()+'.tmp';
      try{await writeFile(temp,writable.header+text,{flag:'wx',mode:0o600});await rename(temp,writable.path)}
      finally{await unlink(temp).catch(e=>{if(e.code!=='ENOENT')throw e})}
      provider.observeHostMutation(writable.path);
      return list(cwd,name);
    }
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
    return{name:skill.name,description:skill.description,content:skill.content,references,revision:writable?.revision};
  };
  let pending=Promise.resolve();
  list.save=(cwd,input)=>{
    const result=pending.then(()=>{
      if(typeof input.name!=='string'||!input.name)throw Object.assign(Error('请选择专家'),{status:400});
      return list(cwd,input.name,input);
    });
    pending=result.catch(()=>{});
    return result;
  };
  return list;
}
