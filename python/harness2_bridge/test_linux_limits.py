"""Linux-only hard resource-limit validation; run explicitly on Linux."""
from pathlib import Path
import subprocess
import sys
import unittest

HERE=Path(__file__).resolve().parent
if sys.platform != 'linux':
    raise SystemExit('This suite requires Linux; Windows Job limits are tested in Go.')


class ResourceTests(unittest.TestCase):
    def test_linux_hard_limits(self):
        source = ('import runpy,sys,resource; m=runpy.run_path(sys.argv[1]); '
                  'm["resource_limits"](); '
                  'assert resource.getrlimit(resource.RLIMIT_AS)==(268435456,268435456); '
                  'assert resource.getrlimit(resource.RLIMIT_CPU)==(5,5); '
                  'assert resource.getrlimit(resource.RLIMIT_FSIZE)==(0,0); '
                  'bytearray(400*1024*1024)')
        proc=subprocess.run([sys.executable,'-I','-S','-B','-c',source,str(HERE/'worker.py')],
                            env={},capture_output=True,timeout=10)
        self.assertNotEqual(proc.returncode,0)
        self.assertIn(b'MemoryError',proc.stderr)


if __name__=='__main__':
    unittest.main()
