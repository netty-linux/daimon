"""Verify the checked-in integrity inventory against Git objects, without imports.
Usage: python -I -S -B verify_pin.py /absolute/audited/upstream/checkout
The checkout can be temporary; it is not needed by the runtime worker.
"""
import hashlib
import json
from pathlib import Path
import subprocess
import sys


def verify(checkout):
    lock=json.loads(Path(__file__).with_name('upstream.lock.json').read_text())
    def git(*args):
        return subprocess.check_output(['git','-C',str(checkout),*args])
    revision=lock['revision']
    if git('rev-parse','HEAD').decode().strip()!=revision:
        raise ValueError('revision mismatch')
    files={p for p in git('ls-tree','-r','--name-only',revision).decode().splitlines()
           if p.startswith('harness2/') and p.endswith('.py')}
    if files!=set(lock['files']):
        raise ValueError('inventory mismatch')
    for rel,expected in lock['files'].items():
        data=git('show',revision+':'+rel)
        actual={'lf':hashlib.sha256(data).hexdigest(),
                'crlf':hashlib.sha256(data.replace(b'\n',b'\r\n')).hexdigest()}
        if actual!=expected:
            raise ValueError('source integrity mismatch')
    license=git('show',revision+':LICENSE')
    if lock['license']!='Apache-2.0' or hashlib.sha256(license).hexdigest()!=lock['license_sha256']:
        raise ValueError('license mismatch')
    if Path(__file__).with_name('LICENSE.harness2').read_bytes()!=license:
        raise ValueError('license copy mismatch')
    print(f'PASS: {revision}; {len(files)} original source files; Apache-2.0 license')


if __name__=='__main__':
    if len(sys.argv)!=2:
        raise SystemExit('usage: verify_pin.py CHECKOUT')
    verify(Path(sys.argv[1]))
