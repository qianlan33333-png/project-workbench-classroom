import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


class CachedSourceTest(unittest.TestCase):
    def test_retry_rechecks_the_current_reference_list(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp)
            refs = state / "refs.txt"
            cached = state / "run-1.json"
            body = json.dumps({"watermark": "frozen", "records": [{"id": "allowed"}, {"id": "revoked"}]})
            cached.write_text(body)
            args = [sys.executable, str(Path(__file__).with_name("collect.py")),
                    "--refs", str(refs), "--operation-key", "run-1", "--state-dir", str(state)]
            refs.write_text("allowed\nrevoked\n")
            same = subprocess.run(args, capture_output=True, text=True, check=True)
            self.assertEqual(same.stdout.strip(), body)

            refs.write_text("allowed\n")
            blocked = subprocess.run(args, capture_output=True, text=True)
            self.assertNotEqual(blocked.returncode, 0)
            self.assertEqual(blocked.stdout, "")
            self.assertEqual(cached.read_text(), body)
