"""Compare frozen entitlement facts with scoped SELECTs; never alter HXC."""
import json,sys,socket,subprocess,time
from pathlib import Path
from collections import defaultdict
from source_common import RIGHTS_COLUMNS,rights_fingerprint
from source_ssh import connection

def check(settings,items):
    import pymysql
    expected={}
    for item in items:
        if item.get('product')!='hxc':continue
        rights=item.get('rights')
        if not isinstance(rights,dict):return False
        if not rights and item.get('account_optional')is True:continue
        if len(rights)!=1:return False
        for uid,fingerprint in rights.items():
            if not isinstance(uid,str)or not uid or not isinstance(fingerprint,str)or len(fingerprint)!=64:return False
            if uid in expected and expected[uid]!=fingerprint:return False
            expected[uid]=fingerprint
    if not expected:return True
    config=settings['hxc'];db=config['db']
    sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close()
    ssh,env,target=connection(config['ssh'])
    process=subprocess.Popen(ssh+['-o','ExitOnForwardFailure=yes','-N','-L',f'127.0.0.1:{port}:{db["DB_HOST"]}:{db.get("DB_PORT","3306")}',target],env=env,stderr=subprocess.DEVNULL)
    try:
        for _ in range(100):
            if process.poll()is not None:raise ValueError('source_tunnel_unavailable')
            try:
                with socket.create_connection(('127.0.0.1',port),timeout=.1):break
            except OSError:time.sleep(.1)
        else:raise ValueError('source_tunnel_unavailable')
        with pymysql.connect(host='127.0.0.1',port=port,user=db['DB_USER'],password=db['DB_PASSWORD'],database=db['DB_NAME'],charset='utf8mb4',cursorclass=pymysql.cursors.DictCursor,connect_timeout=10,read_timeout=10,write_timeout=10)as connection_:
            with connection_.cursor()as cur:
                cur.execute('SET TRANSACTION READ ONLY')
                cur.execute('START TRANSACTION WITH CONSISTENT SNAPSHOT')
                actual=defaultdict(lambda:defaultdict(list))
                tables={'subscriptions':'new_version_user_subscriptions','memberships':'new_version_memberships','subscription_activations':'new_version_subscription_activations'}
                ids=list(expected)
                active=set()
                for offset in range(0,len(ids),100):
                    scope=ids[offset:offset+100]
                    cur.execute('SELECT id FROM new_version_users WHERE COALESCE(is_deleted,0)=0 AND id IN ('+','.join(['%s']*len(scope))+')',scope)
                    active.update(str(row['id'])for row in cur.fetchall())
                if active!=set(ids):return False
                for name,table in tables.items():
                    cur.execute('SHOW COLUMNS FROM `'+table+'`')
                    available={row['Field']for row in cur.fetchall()}
                    columns=[key for key in RIGHTS_COLUMNS[name]if key in available]
                    if 'user_id'not in columns:raise ValueError('source_rights_contract_missing')
                    for offset in range(0,len(ids),100):
                        scope=ids[offset:offset+100]
                        cur.execute('SELECT '+','.join('`'+key+'`'for key in columns)+' FROM `'+table+'` WHERE user_id IN ('+','.join(['%s']*len(scope))+')',scope)
                        for row in cur.fetchall():actual[str(row['user_id'])][name].append(row)
                connection_.rollback()
        return all(rights_fingerprint(actual[uid])==fingerprint for uid,fingerprint in expected.items())
    finally:
        process.terminate()
        try:process.wait(timeout=5)
        except subprocess.TimeoutExpired:process.kill();process.wait()

if __name__=='__main__':
    settings=json.loads(Path(sys.argv[1]).read_text())
    print(json.dumps({'ready':check(settings,json.load(sys.stdin))}))
