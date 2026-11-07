import PQueue from 'p-queue';

// Only in-flight admission lives here. Temporal owns durable queued work.
export function modelCapacity() {
  const lanes = { batch: new PQueue({ concurrency: 4 }), interactive: new PQueue({ concurrency: 2 }), owner: new PQueue({concurrency:2}) };
  return async (lane, signal, work) => {
    const queue = lanes[lane || 'interactive'];
    if (!queue) throw new Error('invalid model lane');
    if (queue.size >= 64) throw new Error('model admission queue full');
    signal.throwIfAborted();
    let started=false;
    const waiting=new AbortController();
    // PQueue cancels queued admission immediately. Once started, the actual
    // provider must exit before its slot is released, even after cancellation.
    const cancel=()=>{if(!started)waiting.abort(signal.reason)};
    signal.addEventListener('abort',cancel,{once:true});
    try{return await queue.add(async()=>{
      started=true;signal.throwIfAborted();return work(signal);
    },{signal:waiting.signal})}
    finally{signal.removeEventListener('abort',cancel)}
  };
}

export async function* admittedStream(admit,lane,signal,next) {
  const pipe=new TransformStream(),writer=pipe.writable.getWriter();
  const work=admit(lane,signal,async()=>{
    try { for await(const chunk of next())await writer.write(chunk);await writer.close(); }
    catch(e){await writer.abort(e);throw e}
  }).catch(async e=>{await writer.abort(e).catch(()=>{});});
  try {yield* pipe.readable;} finally {await work;}
}
