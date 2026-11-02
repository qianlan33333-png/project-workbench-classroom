#!/usr/bin/env python3
"""Verify two local integration fixtures. Never targets public deployments."""
import argparse
from datetime import datetime, timezone
import hashlib
import hmac
import json
import os
from pathlib import Path
import sqlite3
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parent.parent
CRM = 'https://127.0.0.1:18442'
WB = 'https://127.0.0.1:18441'
ADMIN = '/api/admin/integrations/auto-reply?binding=joint'
HOOK = '/api/v1/gateway/hooks/joint'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *unused):
        raise RuntimeError('fixture redirect refused')


def verify(directory, crm_repo):
    auth_path = directory / 'crm-auth.json'
    auth = json.loads(auth_path.read_text())
    meta = json.loads((directory / 'wb-meta.json').read_text())
    db = sqlite3.connect((Path(meta['store']) / 'workbench.sqlite').as_uri() + '?mode=ro', uri=True)
    client = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), NoRedirect,
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(directory/'ca.pem'))))
    admin = {'Cookie': f"aicrm_admin_session={auth['adminSession']}; aicrm_admin_csrf={auth['adminCSRF']}",
             'X-CSRF-Token': auth['adminCSRF']}

    def request(url, headers, data=None, method='GET'):
        obj = urllib.request.Request(url, data=data, headers={'Content-Type':'application/json', **headers}, method=method)
        with client.open(obj, timeout=20) as res:
            return json.load(res)

    def setting(on):
        current = request(CRM + ADMIN, admin)
        result = request(CRM + ADMIN, admin, json.dumps({
            'enabled': on, 'workbenchUrl': current['workbenchUrl'], 'callbackUrl': current['callbackUrl'],
            'revision': current['revision']}).encode(), 'PUT')
        assert result['enabled'] == on and not result['pending'], 'pairing not confirmed'

    def browser(mode):
        subprocess.run(['node', 'scripts/auto-reply-chromium.mjs', str(auth_path),
                        str(directory / 'screenshots'), mode], cwd=crm_repo, check=True, timeout=90)

    def incoming(text):
        event_id = str(uuid.uuid4())
        at = datetime.now(timezone.utc).isoformat()
        body = json.dumps({'eventId':event_id, 'tenantId':'fixture-tenant', 'accountId':'account-a',
            'occurredAt':at,'type':'message.received','data':{'message':{
                'messageId':str(uuid.uuid4()), 'direction':'in','accountId':'account-a','contactId':'contact-a',
                'externalUserId':'external-a','type':'text','sender':{'contactId':'contact-a','name':'Fixture'},
                'sentAt':at,'content':{'type':'text','text':text}}}}).encode()
        stamp = str(int(time.time()))
        signature = hmac.new(b'joint-fixture-hook', stamp.encode()+b'.'+body, hashlib.sha256).hexdigest()
        assert request(WB + HOOK, {'X-Gateway-Event-Id': event_id, 'X-Gateway-Signature':f't={stamp},v1={signature}'}, body, 'POST')['ok']

    def scalar(query):
        row = db.execute(query).fetchone()
        return row[0] if row else None

    def until(predicate):
        for _ in range(100):
            if predicate():return
            time.sleep(0.15)
        raise AssertionError('timed out awaiting fixture state')

    runs = 'SELECT count(*) FROM runs WHERE source=\'wecom-gateway\'' if 'source' in {
        row[1] for row in db.execute('PRAGMA table_info(runs)')} else "SELECT count(*) FROM runs WHERE json_extract(data,'$.source')='wecom-gateway'"
    answers = "SELECT count(*) FROM deliveries WHERE json_extract(data,'$.kind')='gateway.answer'"
    mode = "SELECT json_extract(data,'$.mode') FROM gateway_sessions"
    notices = "SELECT count(*) FROM deliveries WHERE json_extract(data,'$.kind')='gateway.crm' AND state='sent'"
    assert scalar(runs) == 0
    browser('off')
    browser('admin')
    setting(True)
    incoming('joint-handoff')
    until(lambda: scalar(mode)=='human' and scalar(notices)==1)
    for msg in ['partial context', 'violet-paper-713', 'end of human interval']:
        incoming(msg)
    assert scalar(runs)==1 and scalar(answers)==0
    browser('resume')
    assert scalar(mode)=='auto' and scalar(runs)==1 and scalar(answers)==0
    incoming('joint-next')
    until(lambda: scalar("SELECT count(*) FROM deliveries WHERE state='sent' AND json_extract(data,'$.text')='violet-paper-713'")==1)
    until(lambda: scalar(notices)>=2)
    setting(False)
    browser('off')
    assert scalar(mode)=='auto' and scalar(runs)==2 and scalar(answers)==1
    print(json.dumps({'actualServices':['CRM/PostgreSQL','workbench/SQLite','two signed HTTPS directions','Chromium sidebar'],
        'doubles':['synthetic staff and customer','device SDK','model','external message delivery'],
        'pausedMessages':3,'resumeRuns':0,'resumeSends':0,'nextRuns':1,'nextAnswers':1,'confirmedStateNotifications':scalar(notices),'result':'pass'}))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('fixture_dir',type=Path);parser.add_argument('crm_repo',type=Path)
    args=parser.parse_args();os.umask(0o077);verify(args.fixture_dir.resolve(),args.crm_repo.resolve())
