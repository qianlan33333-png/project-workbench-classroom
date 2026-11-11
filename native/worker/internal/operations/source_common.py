"""Server-owned source configuration; never supplied by a model or tool argument."""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from http.client import IncompleteRead
from pathlib import Path

def load():
    root, path = Path(sys.argv[1]), Path(sys.argv[2])
    if not root.is_absolute() or not path.is_absolute() or path.stat().st_mode & 0o077:
        raise ValueError('private_source_configuration_required')
    return root, json.loads(path.read_text())


def write_json(path, value):
    path=Path(path);path.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    temporary=path.with_name('.'+path.name+'.partial')
    with temporary.open('w')as stream:
        os.chmod(temporary,0o600);json.dump(value,stream,ensure_ascii=False,default=str)
        stream.flush();os.fsync(stream.fileno())
    temporary.replace(path)


def original_aliases(users, original_refs):
    by_customer={}
    for ref,row in original_refs.items():
        for cid in [row.get('customer_id'),*(row.get('_mergedCustomerIDs')or[])]:
            if str(cid or '').isdigit()and int(cid)>0:by_customer.setdefault(str(cid),set()).add(ref)
    result={}
    for ref,row in users.items():
        aliases={ref}
        for cid in [row.get('customer_id'),*(row.get('_mergedCustomerIDs')or[])]:
            if str(cid or '').isdigit()and int(cid)>0:aliases.update(by_customer.get(str(cid),set()))
        result[ref]=sorted(aliases)
    return result


def original_reviews(records, aliases):
    result={}
    for alias in aliases:
        for kind,value in records.get(alias,{}).items():
            if kind in result and result[kind]!=value:
                # Conflicting original human decisions remain a hold. A new
                # reference never silently discards an old defer/correction.
                result[kind]={'decision':'defer','reason':'merged_original_reviews_require_verification'}
            else:result[kind]=value
    return result


def alias_execution_requires_review(history, ref, aliases):
    for alias in aliases:
        if alias==ref:continue
        if (history.get('renewal_slots',{}).get(alias)
            or any(v.get('state')!='cancelled' for v in history.get('copy_index',{}).get(alias,[]))
            or any(v[0]==alias for v in history.get('legacy_successful_cycles',[]))):
            return True
    return False


def original_pack_redlines(assets, account_ids):
    result={}
    for kind,filename in [('review','weekly-review-pack-latest.json'),('content','content-service-pack-latest.json')]:
        pack=assets.get('var/service-packs/'+filename)or{}
        result[kind]={'rows':[{'hxc_user_id':r.get('hxc_user_id'),'redline_flags':r.get('redline_flags')}
            for r in pack.get('rows')or[] if str(r.get('hxc_user_id'))in account_ids]}
    return result


def generation_profile(user, conversation):
    # Long-lived classification strings are not attributed current user facts.
    # Keep them in the archive; generation receives the selected action plus
    # the original dated, attributed conversation projection.
    result={key:user[key] for key in ('safe_user_ref','customer_id','CRM展示名',
        '原CRM显示称呼','source_snapshot_id') if key in user}
    result['conversation']=conversation
    return result


def generation_method(key, resources):
    instructions='\n\n'.join('\n'.join(str(line)for line in x['instructions'])
        if isinstance(x.get('instructions'),list)else str(x.get('instructions')or'')for x in resources)
    instructions+='''\n\n本工具的事实输入合同：名单和动作已经由原策略确定，不能重新选人、换产品、换动作或新增服务承诺。
只执行 actions[0].generation 的本轮动作，选定 material.kind 为 followup 时只做跟进，不暗示存在已选文章、报告或已交付内容。
引用资料必须保留 source_at 的时间含义；旧事实只可明确作为历史线索询问现在是否仍适用，不能直接断言是当前状态。
conversation 中 status=inferred、internal_reference_only=true 或缺来源的内容只供内部参考，不转述为确定事实、用户原话或诊断；无状态标签的长期档案分类不作为本轮事实。
报告中的分析和建议必须明确归因于该报告；涉及旧对话状态时询问现在是否仍适用，不直接断言客户当前状态。你是在给发送员工起草消息，模型读到报告不等于员工读过。正文不得写“我看了整份内容”“我看过报告”“我研究了你的情况”或其他员工的个人经历；直接写“报告里提到……”并保留推测语气。此要求优先于原方法中第一人称推荐的写法。
没有本轮核准的内容、报告和链接时不得承诺可查看、已生成、已发送或必定持续交付。每条只有一个主要下一步。
返回JSON：status为draft或blocked，text为待审正文，blocked_reason为阻断理由，evidence_refs为本轮引用的原来源编号。draft时正文非空；blocked时正文必须为空。'''
    return {'name':'原方法 · '+key,'instructions':instructions,'requiredFields':['profile','actions'],'fixedOutputPointer':'/fixedOutput',
        'maxInputBytes':100000,'maxOutputTokens':1200,'outputSchema':{
            'type':'object','required':['status','text','blocked_reason','evidence_refs'],
            'properties':{'status':{'enum':['draft','blocked']},'text':{'type':'string'},
                'blocked_reason':{'type':'string'},'evidence_refs':{'type':'array','items':{'type':'string'}}},
            'additionalProperties':False,'allOf':[
                {'if':{'properties':{'status':{'const':'draft'}}},'then':{'properties':{'text':{'minLength':1},'evidence_refs':{'minItems':1}}}},
                {'if':{'properties':{'status':{'const':'blocked'}}},'then':{'properties':{'text':{'const':''},'blocked_reason':{'minLength':1}}}}]}}


def read_proxy(process, params, budget, too_large):
    """Repeat only transient failures of the exact same read-only source page."""
    for attempt in range(4):
        budget.request_slot();process.stdin.write(json.dumps(params)+'\n');process.stdin.flush()
        line=process.stdout.readline(24*1024*1024)
        if not line or not line.endswith('\n'):raise RuntimeError('original_read_proxy_incomplete')
        value=json.loads(line)
        if 'data'in value:return value['data']
        if value.get('error')=='response_too_large':raise too_large('response_size_exceeded')
        transient=value.get('http_error')in (408,429,500,502,503,504) or value.get('error')in ('TimeoutError','URLError','RemoteDisconnected')
        if not transient or attempt==3:raise RuntimeError('original_read_proxy_rejected_'+str(value.get('http_error')or value.get('error')))
        budget.wait(2**attempt)


def direct_hxc_reader(token, budget, too_large, oauth=None):
    """Read the fixed upstream directly, without environment proxies or redirects."""
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    expires = 0

    def authorization():
        nonlocal token, expires
        if not oauth:
            return 'Bearer ' + token
        import base64
        import time

        if time.monotonic() >= expires:
            credentials = base64.b64encode((oauth['clientId'] + ':' + oauth['clientSecret']).encode()).decode('ascii')
            form = urllib.parse.urlencode({'grant_type': 'client_credentials', 'scope': 'operations.sync.read',
                                          'audience': 'external_integration'}).encode('ascii')
            request = urllib.request.Request('https://ip.lhbl.com.cn/oauth/token', data=form,
                                             headers={'Authorization': 'Basic ' + credentials,
                                                      'Content-Type': 'application/x-www-form-urlencoded'})
            with opener.open(request, timeout=30) as response:
                value = json.loads(response.read(64 * 1024))
            if value.get('scope') != 'operations.sync.read' or value.get('token_type') != 'Bearer':
                raise RuntimeError('hxc_oauth_scope_mismatch')
            token = value['access_token']
            expires = time.monotonic() + max(1, int(value['expires_in']) - 60)
        return 'Bearer ' + token

    def read(params):
        query = urllib.parse.urlencode(params)
        url = 'https://ip.lhbl.com.cn/api/ops/private-domain/v1/sync?' + query
        for attempt in range(4):
            budget.request_slot()
            try:
                request = urllib.request.Request(url, headers={'Authorization': authorization()})
                with opener.open(request, timeout=60) as response:
                    raw = response.read(8 * 1024 * 1024 + 1)
                if len(raw) > 8 * 1024 * 1024:
                    raise too_large('response_size_exceeded')
                return json.loads(raw)
            except urllib.error.HTTPError as error:
                code = error.code
                error.close()
                if code not in (408, 429, 500, 502, 503, 504) or attempt == 3:
                    raise RuntimeError('hxc_unified_http_' + str(code)) from None
            except (urllib.error.URLError, TimeoutError, ConnectionError, IncompleteRead):
                if attempt == 3:
                    raise RuntimeError('hxc_unified_connection_failed') from None
            budget.wait(2 ** attempt)
    return read
