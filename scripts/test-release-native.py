#!/usr/bin/env python3
"""Release recovery behavior, including crashes at the external-consumer boundary."""
import importlib.util,io,json,tempfile,types,unittest,urllib.error
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('native_release',Path(__file__).with_name('release-native.py'))
n=importlib.util.module_from_spec(spec);spec.loader.exec_module(n)
class Controller:
 def __init__(self,root):
  self.BASE=root;self.DATA=root/'data';self.DATA.mkdir();self.USER='test';self.RESERVE=0;self.calls=[];self.pointers={};self.active=set();self.failed_hook=False;self.states={}
 def pointer(self,name):return self.pointers.get(name)
 def point(self,name,path):self.pointers[name]=path
 def release(self,path,exists=True):return Path(path)
 def write(self,path,text,mode=0o600):path.write_text(text);path.chmod(mode)
 def sync_dir(self,path):pass
 def quiet(self):pass
 def remove(self,path,keep):self.calls.append(('remove',str(path)))
 def run(self,*args,**kwargs):
  self.calls.append(args)
  if args[0]=='systemctl':
   op=args[1];units=args[2:]
   if op=='is-active':return types.SimpleNamespace(returncode=0 if any(u in self.active for u in units) else 3)
   if op=='show':return types.SimpleNamespace(returncode=0,stdout='LoadState=loaded\nActiveState='+self.states.get(units[0],'active' if units[0] in self.active else 'inactive')+'\n')
   if op=='stop':self.active.difference_update(units)
   if op=='start':
    if 'project-workbench-channels' in units:
     state=json.loads((self.BASE/'.deploying').read_text())
     if not state.get('committed') and self.pointer('current').name=='candidate':raise AssertionError('consumer enabled before commit')
    self.active.update(units)
  if args[0]=='/acceptance' and self.failed_hook:raise RuntimeError('acceptance failed')
  return types.SimpleNamespace(returncode=0)
 def start(self,path,channels=True):
  self.point('current',path);self.active.add('project-workbench')
  if channels:self.active.add('project-workbench-channels')
class RecoveryTest(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);root=Path(self.tmp.name)
  self.c=Controller(root);self.old=root/'old';self.candidate=root/'candidate'
  for path in (self.old,self.candidate):path.mkdir()
  (self.old/'native-release.json').write_text('{}');(self.candidate/'native-release.json').write_text('{}')
  self.c.point('current',self.old);self.c.active.update(['project-workbench','project-workbench-channels'])
 def start(self,c,path,channels=False):c.start(path,channels)
 def channels(self,c,state):
  if state['channels']:c.run('systemctl','start','project-workbench-channels')
 def test_acceptance_failure_restores_old_before_any_new_receiving(self):
  self.c.failed_hook=True
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'health'),patch.object(n,'cleanup'),patch.object(n,'restore_channels',self.channels),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
   with self.assertRaises(RuntimeError):n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.old)
  self.assertFalse((self.c.BASE/'.deploying').exists())
  self.assertIn('project-workbench-channels',self.c.active)
 def test_commit_then_crash_never_restores_old_and_recovers_consumer(self):
  attempts=[]
  def crash_once(c,state):
   attempts.append(True)
   if len(attempts)==1:raise RuntimeError('publish process interrupted after durable commit')
   self.channels(c,state)
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'health'),patch.object(n,'cleanup'),patch.object(n,'restore_channels',crash_once),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
   with self.assertRaises(RuntimeError):n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.candidate)
  self.assertEqual(self.c.pointer('previous'),self.old)
  self.assertIn('project-workbench-channels',self.c.active)
  self.assertEqual(len(attempts),2)
 def test_uncommitted_restart_restores_previous_and_plugin(self):
  state={'kind':'native','candidate':str(self.candidate),'old_current':str(self.old),'old_previous':None,'channels':True,'old_plugin_active':True,'committed':False}
  self.c.point('current',self.candidate);self.c.write(self.c.BASE/'.deploying',json.dumps(state))
  with patch.object(n,'start',self.start),patch.object(n,'cleanup'),patch.object(n,'restore_channels',self.channels):n.recover(self.c,state)
  self.assertEqual(self.c.pointer('current'),self.old)
  self.assertIn('workbench-context-plugin',self.c.active)
  self.assertFalse((self.c.BASE/'.deploying').exists())
 def quiesced_guard(self):
  return {'oldCurrent':str(self.old),'localBackupVerified':True,'localBackupSHA256':'a'*64,'legacyQuiesced':True,'legacyChannelsActive':True,'legacyPluginActive':True}
 def guard_reader(self,guard):
  original=Path.read_text
  def read(path,*args,**kwargs):
   if str(path)=='/etc/project-workbench/native-cutover.json':return json.dumps(guard)
   return original(path,*args,**kwargs)
  return read
 def test_quiesced_legacy_commit_starts_new_receiver_only(self):
  (self.old/'native-release.json').unlink();self.c.active.clear()
  with patch.object(n,'verify'),patch.object(n,'legacy_quiet'),patch.object(n,'start',self.start),patch.object(n,'restore_channels',self.channels),patch.object(n.Path,'read_text',self.guard_reader(self.quiesced_guard())),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
   n.deploy(self.c,self.candidate)
  state=json.loads((self.c.BASE/'.deploying').read_text())
  self.assertTrue(state['committed']);self.assertTrue(state['channels']);self.assertTrue(state['old_plugin_active'])
  self.assertEqual(self.c.pointer('current'),self.candidate);self.assertIsNone(self.c.pointer('previous'))
  self.assertIn('project-workbench-channels',self.c.active)
  self.assertNotIn('workbench-context-plugin',self.c.active)
 def test_quiesced_legacy_acceptance_failure_restores_old_receiver_and_plugin(self):
  (self.old/'native-release.json').unlink();self.c.active.clear();self.c.failed_hook=True
  with patch.object(n,'verify'),patch.object(n,'legacy_quiet'),patch.object(n,'start',self.start),patch.object(n,'restore_channels',self.channels),patch.object(n.Path,'read_text',self.guard_reader(self.quiesced_guard())),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
   with self.assertRaisesRegex(RuntimeError,'acceptance failed'):n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.old)
  self.assertIn('project-workbench-channels',self.c.active);self.assertIn('workbench-context-plugin',self.c.active)
  self.assertFalse((self.c.BASE/'.deploying').exists())
 def test_native_upgrade_does_not_restore_stale_legacy_guard_flags(self):
  self.c.active.clear()
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'cleanup'),patch.object(n,'health'),patch.object(n,'restore_channels',self.channels),patch.object(n.Path,'read_text',self.guard_reader(self.quiesced_guard())),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
   n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.candidate)
  self.assertNotIn('project-workbench-channels',self.c.active);self.assertNotIn('workbench-context-plugin',self.c.active)
 def test_standard_rollback_uses_the_verified_previous_without_extra_hook(self):
  self.c.point('previous',self.candidate)
  (self.candidate/'native-release.json').write_text('{"workerStartupGate":1}')
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'health'),patch.object(n,'cleanup'),patch.object(n,'restore_channels',self.channels),patch.dict(n.os.environ,{},clear=True):
   n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.candidate);self.assertEqual(self.c.pointer('previous'),self.old)
 def test_unsafe_old_generation_is_rejected_before_stopping_current(self):
  self.c.point('previous',self.candidate)
  with patch.object(n,'verify'),patch.dict(n.os.environ,{},clear=True):
   with self.assertRaisesRegex(RuntimeError,'lacks the safe background'):n.deploy(self.c,self.candidate)
  self.assertFalse(any(call[:2]==('systemctl','stop') for call in self.c.calls))
 def test_new_candidate_still_requires_acceptance(self):
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'health'),patch.object(n,'cleanup'),patch.object(n,'restore_channels',self.channels),patch.dict(n.os.environ,{},clear=True):
   with self.assertRaisesRegex(RuntimeError,'requires the reviewed acceptance hook'):n.deploy(self.c,self.candidate)
  self.assertEqual(self.c.pointer('current'),self.old)
 def test_stop_drains_worker_before_dependencies_and_rejects_deactivating(self):
  n.stop(self.c)
  stops=[call for call in self.c.calls if call[:2]==('systemctl','stop')]
  self.assertEqual(stops[0],('systemctl','stop','project-workbench-worker'))
  self.c.states['project-workbench-worker']='deactivating'
  with self.assertRaisesRegex(RuntimeError,'did not stop'):n.stop(self.c)
class HealthTest(unittest.TestCase):
 def probe(self,index_status,bridge_ready=True):
  def request(url,**kwargs):
   address=url if isinstance(url,str) else url.full_url
   if address=='http://127.0.0.1:3082/':
    if index_status!=200:raise urllib.error.HTTPError(address,index_status,'fixture',{},io.BytesIO(b'unauthorized'))
    return io.BytesIO(b'<html></html>')
   return io.BytesIO(json.dumps({'ready':bridge_ready if ':7382/' in address else True}).encode())
  c=types.SimpleNamespace(run=lambda *args,**kwargs:types.SimpleNamespace(returncode=0))
  with patch.object(n.Path,'read_text',return_value=json.dumps({'common':{'WORKBENCH_HOST_TOKEN':'fixture'}})),patch.object(n.urllib.request,'urlopen',request),patch.object(n.time,'sleep'):
   n.health(c)
 def test_protected_native_index_is_ready_for_authenticated_acceptance(self):
  self.probe(401)
 def test_other_http_errors_or_unready_plugin_do_not_pass(self):
  for status,ready in [(403,True),(500,True),(401,False)]:
   with self.subTest(status=status,ready=ready),self.assertRaisesRegex(RuntimeError,'did not become ready'):
    self.probe(status,ready)
class DependencyTest(unittest.TestCase):
 def test_normal_rotation_preserves_an_independent_container_dependency(self):
  with tempfile.TemporaryDirectory() as temp:
   root=Path(temp);c=Controller(root);releases=root/'releases';releases.mkdir()
   current=releases/'current-release';current.mkdir();(current/'native-release.json').write_text('{}');c.point('current',current)
   retired=releases/'retired-release';retired.mkdir()
   def command(*args,**kwargs):return types.SimpleNamespace(stdout='independent\n' if args[1]=='ps' else json.dumps([{'Name':'external-service','Mounts':[{'Source':str(retired/'tools')}]}]))
   c.as_user=command
   with patch.object(n,'check_retirement_dependencies'),patch.object(n.Path,'read_text',return_value='{}'):
    with self.assertRaisesRegex(RuntimeError,'external-service'):n.cleanup(c)
   self.assertFalse(any(call[0]=='remove' for call in c.calls))
 def test_stopped_container_bind_blocks_retirement(self):
  calls=[]
  def command(*args,**kwargs):
   calls.append(args)
   return types.SimpleNamespace(stdout='container1\n' if args[1]=='ps' else json.dumps([{'Name':'external-service','State':{'Running':False},'Mounts':[{'Source':'/retiring/tools'}]}]))
  with self.assertRaisesRegex(RuntimeError,'external-service'):
   n.check_container_dependencies(types.SimpleNamespace(as_user=command),[Path('/retiring')],[])
  self.assertIn('--all',calls[0])
 def test_startup_definition_blocks_even_without_container(self):
  with tempfile.TemporaryDirectory() as temp:
   path=Path(temp);(path/'external.container').write_text('Volume=/retiring/tools:/tools:ro\n')
   c=types.SimpleNamespace(as_user=lambda *args,**kwargs:types.SimpleNamespace(stdout=''))
   with self.assertRaisesRegex(RuntimeError,'Startup definition'):
    n.check_container_dependencies(c,[Path('/retiring')],[path])
 def test_moved_resource_and_similar_prefix_are_preserved(self):
  with tempfile.TemporaryDirectory() as temp:
   path=Path(temp);(path/'native.service').write_text('WorkingDirectory=/retiring-native\n')
   def command(*args,**kwargs):return types.SimpleNamespace(stdout='moved\n' if args[1]=='ps' else json.dumps([{'Name':'external','Mounts':[{'Source':'/integrations/tools'}]}]))
   n.check_container_dependencies(types.SimpleNamespace(as_user=command),[Path('/retiring')],[path])
class MethodPackages(unittest.TestCase):
 def test_version_switch_and_interrupted_restore(self):
  with tempfile.TemporaryDirectory() as tmp:
   root=Path(tmp).resolve();projects=root/'projects';target=projects/'p/.agents/skills/expert';target.mkdir(parents=True);(target/'SKILL.md').write_text('initial')
   old=root/'old';new=root/'new';registry={}
   for release,value in [(old,'previous method'),(new,'candidate method')]:
    source=release/'methods/expert';source.mkdir(parents=True);(source/'SKILL.md').write_text(value)
    registry[str(release)]=[{'source':str(source),'target':str(target),'files':{'SKILL.md':n.hashlib.sha256(value.encode()).hexdigest()}}]
   bindings=root/'bindings.json';bindings.write_text(json.dumps(registry));account=types.SimpleNamespace(pw_uid=n.os.getuid(),pw_gid=n.os.getgid())
   with patch.object(n,'METHOD_BINDINGS',bindings),patch.object(n,'PROJECT_ROOT',projects),patch.object(n,'METHOD_BACKUPS',root/'backup'),patch.object(n.pwd,'getpwnam',return_value=account):
    n.restore_project_methods(new);self.assertEqual((target/'SKILL.md').read_text(),'candidate method')
    n.restore_project_methods(old);self.assertEqual((target/'SKILL.md').read_text(),'previous method')
    n.shutil.rmtree(target) # process died after removing target, before rename
    n.restore_project_methods(old);self.assertEqual((target/'SKILL.md').read_text(),'previous method')
    (new/'methods/expert/SKILL.md').write_text('unexpected bytes')
    with self.assertRaisesRegex(RuntimeError,'checksum'):n.restore_project_methods(new)
    self.assertEqual((target/'SKILL.md').read_text(),'previous method')
    (new/'methods/expert/SKILL.md').write_text('candidate method');(new/'methods/expert/link').symlink_to(target/'SKILL.md')
    with self.assertRaisesRegex(RuntimeError,'symlinks'):n.restore_project_methods(new)
 def test_start_restores_before_services(self):
  order=[];c=types.SimpleNamespace(run=lambda *a,**kw:order.append('run'),point=lambda *a:None)
  response=types.SimpleNamespace(close=lambda:None)
  with patch.object(n,'is_native',return_value=True),patch.object(n,'restore_project_methods',side_effect=lambda p:order.append('restore')),patch.object(n,'configure_units'),patch.object(n,'health'),patch.object(n.urllib.request,'urlopen',return_value=response):
   n.start(c,Path('/selected'))
  self.assertEqual(order[0],'restore')

if __name__=='__main__':unittest.main()
