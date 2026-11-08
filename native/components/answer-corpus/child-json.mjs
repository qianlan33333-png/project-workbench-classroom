import {spawn} from 'node:child_process';
export const activeGroups=new Set();

// A one-shot tool owns the entire OS process group, including captured CLI
// grandchildren. Completing a request or stopping the server waits for it.
export function runJSONChild(args,input,signal,timeout=45000,options={}){
 return new Promise((resolve,reject)=>{
  const p=spawn(options.command||'python3',args,{stdio:['pipe','pipe','pipe'],detached:true,env:options.env||process.env});
  if(p.pid)activeGroups.add(p.pid);
  let size=0,chunks=[],failure,closed=false,code,hardTimer,settled=false,killing=false;
  const exists=()=>{if(!p.pid)return false;try{process.kill(-p.pid,0);return true}catch(e){return e.code!=='ESRCH'}};
  const terminate=()=>{if(killing)return;killing=true;try{process.kill(-p.pid,'SIGTERM')}catch{}
   // This timer intentionally stays referenced even if the direct child exits.
   hardTimer=setTimeout(()=>{try{process.kill(-p.pid,'SIGKILL')}catch{}},2000);
  };
  const finish=()=>{
   if(settled||!closed)return;
   if(exists()){failure??=Error('tool descendants did not exit');terminate();return}
   settled=true;clearTimeout(timer);clearTimeout(hardTimer);clearInterval(poll);signal.removeEventListener('abort',abort);activeGroups.delete(p.pid);
   if(failure||code!==0){reject(failure||Error('tool unavailable'));return}
   try{resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')))}catch{reject(Error('invalid JSON output'))}
  };
  const abort=()=>{failure??=Error('cancelled');terminate()};
  const timer=setTimeout(()=>{failure??=Error('timeout');terminate()},timeout),poll=setInterval(finish,50);
  signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
  p.stdout.on('data',b=>{size+=b.length;if(size>1048576){failure??=Error('output budget');terminate()}else chunks.push(b)});p.stderr.resume();
  p.on('error',e=>{failure??=e});p.on('close',value=>{closed=true;code=value;finish()});p.stdin.on('error',()=>{});p.stdin.end(input===undefined?'':JSON.stringify(input));
 });
}
