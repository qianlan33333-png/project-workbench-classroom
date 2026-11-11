import io,json,sys,types,unittest
from pathlib import Path
sys.path.insert(0,str(Path(__file__).parent))
from source_common import read_proxy

class SourceReadRecoveryTests(unittest.TestCase):
    def call(self,responses):
        process=types.SimpleNamespace(stdin=io.StringIO(),stdout=io.StringIO(''.join(json.dumps(r)+'\n'for r in responses)))
        waits=[];budget=types.SimpleNamespace(request_slot=lambda:None,wait=waits.append)
        params={'resource':'content-activity','cursor':'same-frozen-page','limit':100}
        try:return read_proxy(process,params,budget,ValueError),process,waits
        finally:
            sent=[json.loads(s)for s in process.stdin.getvalue().splitlines()]
            self.assertTrue(all(s==params for s in sent))
    def test_temporary_503_repeats_only_the_same_read(self):
        result,p,waits=self.call([{'http_error':503},{'http_error':503},{'data':{'items':['original page']}}])
        self.assertEqual(result,{'items':['original page']});self.assertEqual(waits,[1,2])
        self.assertEqual(len(p.stdin.getvalue().splitlines()),3)
    def test_denial_and_expired_snapshot_never_become_empty_success(self):
        for code in (401,403,409):
            with self.assertRaisesRegex(RuntimeError,str(code)):self.call([{'http_error':code},{'data':{}}])
        with self.assertRaisesRegex(RuntimeError,'503'):self.call([{'http_error':503}]*4)
        with self.assertRaisesRegex(ValueError,'response_size_exceeded'):self.call([{'error':'response_too_large'}])

if __name__=='__main__':unittest.main()
