"""Original deterministic functions; sources are bound by the adapter."""
from pathlib import Path
import hashlib, json, re

ROOT=Path(__file__).resolve().parents[1]

ASSET='docs/product-operation-packs/shared/owner-service-copy.v1.json'

def asset(root=None):
 raw=(Path(root or ROOT)/ASSET).read_bytes();value=json.loads(raw)
 if value.get('version')!='owner-service-copy.v1.20261026':raise ValueError('unknown_owner_service_copy_version')
 return value,hashlib.sha256(raw).hexdigest()

def render(key,values,root=None):
 data,digest=asset(root);body=data['templates'][key]['text']
 for field in set(re.findall(r'{{([^{}]+)}}',body)):
  if field not in values or values[field] in (None,''):raise ValueError('owner_copy_missing_'+field)
  body=body.replace('{{'+field+'}}',str(values[field]))
 if '{{'in body or'}}'in body:raise ValueError('owner_copy_unfilled')
 return {'text':body,'version':data['version'],'key':key,'source':ASSET,'sha256':digest}

def activation(opening,questionnaire_confirmed=False,root=None):
 data,_=asset(root);a=data['templates']['activation_unknown_68']
 return render('activation_unknown_68',{'报名开头':opening,'问卷承接':a['known_questionnaire']if questionnaire_confirmed else a['unknown_questionnaire']},root)

def reference(key,root=None):
 data,digest=asset(root);value=data['templates'][key]
 return {'id':'owner.service-copy.'+key,'version':data['version'],'text':value['text'],
         'instructions':value['rules'],'source_asset':ASSET,'source_sha256':digest}

def expiry(name,deadline,root=None):
 """Use the confirmed no-report variant; generation is not provision evidence.

 The original expiry asset explicitly permits omitting its middle report
 paragraph. Do not infer a report delivery from a profile/report-ready flag.
 """
 from datetime import datetime
 from zoneinfo import ZoneInfo
 end=datetime.fromisoformat(str(deadline).replace('Z','+00:00'))
 if end.tzinfo is None:raise ValueError('owner_expiry_timezone_required')
 end=end.astimezone(ZoneInfo('Asia/Shanghai'))
 date=f'{end.month}月{end.day}日{end.hour}点'+(f'{end.minute}分'if end.minute else'')
 data,digest=asset(root);parts=data['templates']['expiry_with_report']['text'].split('\n\n')
 if len(parts)!=3 or '{{报告时间}}'not in parts[1]:raise ValueError('owner_expiry_structure_changed')
 body='\n\n'.join((parts[0],parts[2])).replace('{{称呼}}',name).replace('{{到期日期}}',date)
 if not name or '{{'in body:raise ValueError('owner_expiry_unfilled')
 return {'text':body,'version':data['version'],'key':'expiry_with_report','source':ASSET,
         'sha256':digest,'report_paragraph':'omitted_without_bound_delivery_evidence'}
