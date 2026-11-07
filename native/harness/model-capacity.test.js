import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as tick } from 'node:timers/promises';
import { modelCapacity, admittedStream } from './model-capacity.js';

test('bulk saturation reserves interactive capacity and canceled providers retain their slots until exit', async () => {
  const admit = modelCapacity(), controllers = Array.from({length: 7}, () => new AbortController());
  let active = 0, maximum = 0, started = 0;
  const releases = [];
  const requests = controllers.map((c) => admit('batch', c.signal, async () => {
    started++; maximum = Math.max(maximum, ++active);
    await new Promise(resolve => releases.push(resolve));
    active--;
  }).catch(e => e));
  await tick();
  assert.equal(started, 4);
  controllers[0].abort(); // Simulates a provider that takes time to stop.
  controllers[4].abort(); // This queued request must never invoke the provider.
  await tick();
  assert.equal(started, 4);
  assert.equal(await admit('interactive', new AbortController().signal, async () => 'available'), 'available');
  releases.shift()(); await tick();
  assert.equal(started, 5);
  while (releases.length) { releases.shift()(); await tick(); }
  await Promise.all(requests);
  assert.equal(started, 6);
  assert.equal(maximum, 4);
  assert.equal(active, 0);
});

test('native streaming preserves chunks and releases canceled streams',async()=>{
 const admit=modelCapacity(),signal=new AbortController().signal;
 let closed=0;
 async function* provider(){try{yield 'first';yield 'second';yield 'third'}finally{closed++}}
 for await(const chunk of admittedStream(admit,'interactive',signal,provider)){assert.equal(chunk,'first');break}
 assert.equal(closed,1);
 const seen=[];for await(const chunk of admittedStream(admit,'owner',signal,provider))seen.push(chunk);
 assert.deepEqual(seen,['first','second','third']);assert.equal(closed,2);
});
