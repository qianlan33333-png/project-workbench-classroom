#!/usr/bin/env python3
"""Release recovery behavior, including crashes at the external-consumer boundary."""
import importlib.util,json,tempfile,types,unittest
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('native_release',Path(__file__).with_name('release-native.py'))
n=importlib.util.module_from_spec(spec);spec.loader.exec_module(n)
class Controller:
 def __init__(self,root):
  self.BASE=root;self.DATA=root/'data';self.DATA.mkdir();self.USER='test';self.RESERVE=0;self.calls=[];self.pointers={};self.active=set();self.failed_hook=False
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
  with patch.object(n,'verify'),patch.object(n,'start',self.start),patch.object(n,'health'),patch.object(n,'cleanup'),patch.dict(n.os.environ,{'PW_ACCEPTANCE_SCRIPT':'/acceptance'}),patch.object(n.Path,'resolve',lambda path,strict=False:path):
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
  with patch.object(n,'start',self.start),patch.object(n,'cleanup'):n.recover(self.c,state)
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
class DependencyTest(unittest.TestCase):
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
if __name__=='__main__':unittest.main()
