import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { exportSPKI, generateKeyPair, SignJWT } from 'jose';
import { activationAudience, createActivation, installActivation } from './activation.js';

test('activation denies both Harness HTTP and WebSocket admission until a grant is active', () => {
  const activation = { active: false };
  const peer = {};
  const connection = {
    authorizeIndex: () => true,
    admitConnection(req) { return this.admit(req); },
    requestRejection(req) { return this.admit(req).rejection; },
    admit: req => req.authenticated ? { peer } : { rejection: 401 },
    browserPeer: peer,
  };
  const original = connection.admit;
  const disposers = [];
  let activationPage;
  installActivation({
    connection, effect: install => disposers.push(install()),
    webServer: { register: route => { activationPage = route.handler; } },
  }, activation);
  const request = { authenticated: true };
  assert.deepEqual(connection.admitConnection(request), { rejection: 403 });
  assert.deepEqual(connection.admit(request), { rejection: 403 });
  assert.equal(connection.requestRejection(request), 403);
  assert.equal(typeof activationPage, 'function');
  activation.active = true;
  assert.deepEqual(connection.admitConnection(request), { peer });
  assert.deepEqual(connection.admitConnection({}), { rejection: 401 });
  assert.equal(connection.requestRejection(request), undefined);
  disposers[0]();
  assert.equal(connection.admit, original);
});

test('activation retries the same installation and restores a valid grant offline', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'workbench-activation-'));
  try {
    const keys = await generateKeyPair('EdDSA');
    const other = await generateKeyPair('EdDSA');
    const issuer = 'https://activate.example.test';
    const publicKey = await exportSPKI(keys.publicKey);
    let attempts = 0;
    let firstInstallation;
    const activate = ({ fetcher }) => createActivation({ directory, issuer, publicKey, fetcher });
    const workbench = await activate({
      fetcher: async (url, req) => {
        assert.equal(url, issuer + '/workbench-activation/redeem');
        const { installation } = JSON.parse(req.body);
        firstInstallation ??= installation;
        assert.equal(installation, firstInstallation);
        if (++attempts === 1) throw new Error('response lost');
        const grant = await new SignJWT({}).setProtectedHeader({ alg: 'EdDSA', typ: 'workbench-activation+jwt' })
          .setIssuer(issuer).setAudience(activationAudience).setSubject(installation).setIssuedAt()
          .sign(attempts === 2 ? other.privateKey : keys.privateKey);
        return Response.json({ grant });
      },
    });
    const code = 'A'.repeat(32);
    await assert.rejects(workbench.activate(code));
    await assert.rejects(workbench.activate(code));
    assert.equal(workbench.active, false);
    await workbench.activate(code);
    assert.equal(workbench.active, true);
    const restarted = await activate({ fetcher: () => assert.fail('offline activation must not call issuer') });
    assert.equal(restarted.active, true);

    const second = await mkdtemp(join(tmpdir(), 'workbench-activation-'));
    try {
      await writeFile(join(second, 'grant.jwt'), await readFile(join(directory, 'grant.jwt')));
      const copied = await createActivation({ directory: second, issuer, publicKey });
      assert.equal(copied.active, false);
    } finally { await rm(second, { recursive: true }); }
  } finally { await rm(directory, { recursive: true }); }
});
