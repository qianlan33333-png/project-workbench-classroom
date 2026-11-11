import io
import json
import tempfile
import unittest
from unittest.mock import patch, mock_open
from raw_transport import _spool_root
from raw_transport import DiskRows, read_wire, write_wire, write_json


class RawTransportTests(unittest.TestCase):
    def test_stable_bounded_sort_matches_original_null_number_text_order(self):
        for descending, expected in [(False, [None, 2, 2, 10, '10', '2']), (True, ['2', '10', 10, 2, 2, None])]:
            with self.subTest(descending=descending):
                rows = DiskRows(order_field='at', descending=descending)
                try:
                    rows.extend([{'at': value, 'i': i} for i, value in enumerate([2, '2', None, 10, '10', 2])])
                    actual = list(rows)
                    self.assertEqual([r['at'] for r in actual], expected)
                    self.assertEqual([r['i'] for r in actual if r['at'] == 2], [0, 5])
                    self.assertEqual(list(rows), actual)
                finally: rows.close()

    def test_multi_pass_merge_and_original_created_id_ties(self):
        rows = DiskRows()
        try:
            rows.extend([{'created_at': str(i % 3), 'id': str(i % 2), 'n': i} for i in range(16501)])
            rows.order_by_created_id()
            actual = list(rows)
            self.assertEqual(actual, sorted(actual, key=lambda r: (r['created_at'], r['id'], r['n'])))
            self.assertEqual(len(actual), 16501)
        finally: rows.close()

    def test_linux_spool_rejects_memory_filesystem_before_creating_files(self):
        for filesystem in ('tmpfs','ramfs'):
            with patch('raw_transport.sys.platform','linux'), patch.dict('os.environ',{'WORKBENCH_SOURCE_SPOOL_DIR':'/controlled/disk'}), patch('builtins.open',mock_open(read_data='1 0 0:1 / / rw - '+filesystem+' source rw\n')), patch('raw_transport.tempfile.TemporaryDirectory') as create:
                with self.assertRaisesRegex(RuntimeError,'source_spool_requires_disk_filesystem'):DiskRows()
                create.assert_not_called()
    def test_disk_headroom_failure_preserves_rows(self):
        rows=DiskRows()
        try:
            with patch('raw_transport.shutil.disk_usage',return_value=type('Disk',(),{'free':100})()):
                with self.assertRaisesRegex(RuntimeError,'headroom'):rows.extend([{'id':1}])
            self.assertEqual(len(rows),0)
        finally:rows.close()

    def test_wire_roundtrip_digest_and_truncation(self):
        rows = DiskRows(); rows.extend([{'id': i} for i in range(1003)])
        try:
            wire = io.StringIO(); write_wire({'ok': True, 'users': [{'id': 'person'}], 'messages': rows}, wire)
            raw = wire.getvalue().encode()
            out = read_wire(io.BytesIO(raw))
            try:
                self.assertEqual(len(out['messages']), 1003)
                value = io.StringIO(); write_json(out, value)
                self.assertEqual(json.loads(value.getvalue())['messages'][-1], {'id': 1002})
            finally: out['messages'].close()
            with self.assertRaises(RuntimeError): read_wire(io.BytesIO(raw.rsplit(b'\n', 2)[0] + b'\n'))
            with self.assertRaises(RuntimeError): read_wire(io.BytesIO(raw.replace(b'person', b'changed')))
        finally: rows.close()


if __name__ == '__main__': unittest.main()
