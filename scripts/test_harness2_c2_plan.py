import hashlib
import tempfile
from pathlib import Path
import unittest
import uuid

from harness2_c2_plan import artifact, private_target, discard_plan, read_json


class PlannerTests(unittest.TestCase):
    def test_bounded_unique_json(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).parent.parent / '.git') as d:
            p = Path(d) / 'journal.json'
            p.write_text('{"version":1,"version":2}')
            with self.assertRaises(ValueError):
                read_json(p)
            p.write_bytes(b' ' * 131073)
            with self.assertRaises(ValueError):
                read_json(p)

    def test_artifact_bytes_and_missing(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).parent.parent / '.git') as d:
            p = Path(d) / 'sentinel'
            p.write_bytes(b'controlled')
            artifact(p, 10, hashlib.sha256(b'controlled').hexdigest())
            with self.assertRaises(ValueError):
                artifact(p, 10, '0' * 64)
            with self.assertRaises(ValueError):
                artifact(p.with_name('absent'), 10, '0' * 64)

    def test_path_conflicts(self):
        with self.assertRaises(ValueError):
            private_target(Path(__file__).parent)
        with self.assertRaises(ValueError):
            private_target('relative')
        with self.assertRaises(ValueError):
            private_target(Path(__file__).parent / '..' / 'new')

    def test_exact_disposal_identity(self):
        root = Path(__file__).absolute().parent / 'proposed'
        vm = str(uuid.uuid4())
        j = dict(version=1, state='created', vm_id=vm, root=str(root),
                 vhdx=str(root / 'lab.vhdx'))
        r = discard_plan(j, vm, str(root))
        self.assertEqual(r['status'], 'BLOCKED')
        self.assertFalse(r['deletion_performed'])
        with self.assertRaises(ValueError):
            discard_plan(j, str(uuid.uuid4()), str(root))
        j['vhdx'] = str(root.parent / 'other.vhdx')
        with self.assertRaises(ValueError):
            discard_plan(j, vm, str(root))

    def test_uncommitted_journal(self):
        with self.assertRaises(ValueError):
            discard_plan({'version': 1, 'state': 'intent'}, str(uuid.uuid4()), '/')


if __name__ == '__main__':
    unittest.main()
