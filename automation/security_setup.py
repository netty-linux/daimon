"""Install reviewed scanner pins into an ephemeral, non-root CI directory only."""
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tarfile
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ROOT / '.automation-results/security-tools'
INPUTS = ('go.mod', 'ui/package-lock.json', 'python/harness2_bridge/pyproject.toml',
          'python/harness2_bridge/uv.lock', 'python/harness2_bridge/upstream.lock.json',
          'automation/build-requirements.txt', 'automation/security-tools.in',
          'automation/security-tools.lock', 'automation/security-tools.json',
          '.github/workflows/ci.yml', '.github/workflows/development-automation.yml')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def environment():
    # Never pass production/account credentials or a GitHub token to scanners.
    keep = {'PATH', 'HOME', 'USERPROFILE', 'SYSTEMROOT', 'TEMP', 'TMP', 'TMPDIR',
            'LANG', 'LC_ALL', 'SSL_CERT_FILE', 'SSL_CERT_DIR', 'GOCACHE', 'GOPATH', 'GOMODCACHE'}
    env = {k: v for k, v in os.environ.items() if k in keep}
    env.update(GOTOOLCHAIN='local', GOWORK='off', GOSUMDB='sum.golang.org',
               GOPROXY='https://proxy.golang.org', GOTELEMETRY='off', GOFLAGS='-mod=readonly',
               NPM_CONFIG_REGISTRY='https://registry.npmjs.org/',
               NPM_CONFIG_USERCONFIG=str(TOOLS / 'empty.npmrc'),
               NPM_CONFIG_GLOBALCONFIG=str(TOOLS / 'empty-global.npmrc'),
               NPM_CONFIG_CACHE=str(TOOLS / 'npm-cache'), NPM_CONFIG_IGNORE_SCRIPTS='true',
               NPM_CONFIG_UPDATE_NOTIFIER='false')
    return env


def main():
    TOOLS.mkdir(parents=True, exist_ok=False)
    pins = json.loads((ROOT / 'automation/security-tools.json').read_text())
    try:
        if platform.system() != 'Linux' or os.geteuid() == 0 or sys.version_info[:2] != (3, 12):
            raise RuntimeError('Disposable non-root Linux/Python 3.12 required')
        for name in ('empty.npmrc', 'empty-global.npmrc'):
            (TOOLS / name).write_text('', encoding='utf-8')
        env = environment()
        def run(argv):
            with (TOOLS / 'setup.log').open('ab') as output:
                subprocess.run(list(map(str, argv)), cwd=TOOLS, env=env, stdout=output,
                               stderr=subprocess.STDOUT, timeout=600, check=True)
        def capture(argv):
            return subprocess.check_output(list(map(str, argv)), cwd=ROOT, env=env, text=True, timeout=60).strip()
        if not capture(['uv', '--version']).startswith('uv ' + pins['uv'] + ' '):
            raise ValueError('Unexpected installer version')
        venv = TOOLS / 'venv'
        run(['uv', 'venv', '--python', sys.executable, '--no-managed-python', venv])
        python = venv / 'bin/python'
        run(['uv', 'pip', 'install', '--python', python, '--require-hashes', '--only-binary', ':all:',
             '-r', ROOT / 'automation/security-tools.lock'])
        (TOOLS / 'bin').mkdir()
        env['GOBIN'] = str(TOOLS / 'bin')
        run(['go', 'install', 'golang.org/x/vuln/cmd/govulncheck@' + pins['govulncheck']])
        govuln = TOOLS / 'bin/govulncheck'
        build = capture(['go', 'version', '-m', govuln])
        if 'golang.org/x/vuln\t' + pins['govulncheck'] + '\t' + pins['govulncheck_module_sum'] not in build:
            raise ValueError('Go scanner module/sumdb identity mismatch')
        tarball = TOOLS / 'npm.tgz'
        with urlopen(pins['npm_tarball'], timeout=60) as response:
            data = response.read(10 * 1024 * 1024 + 1)
        expected = pins['npm_integrity'].removeprefix('sha512-')
        if len(data) > 10 * 1024 * 1024 or base64.b64encode(hashlib.sha512(data).digest()).decode() != expected:
            raise ValueError('Pinned npm tarball integrity failed')
        tarball.write_bytes(data)
        npm_root = TOOLS / 'npm'
        npm_root.mkdir()
        with tarfile.open(tarball) as archive:
            archive.extractall(npm_root, filter='data')
        npm = npm_root / 'package/bin/npm-cli.js'
        if capture(['node', npm, '--version']) != pins['npm']:
            raise ValueError('Unexpected npm scanner version')
        versions = json.loads(capture([python, '-I', '-c',
            'import importlib.metadata as m,json; print(json.dumps({n:m.version(n) for n in ("pip-audit","zizmor","PyYAML")}))']))
        if any(versions[k] != pins[k] for k in versions):
            raise ValueError('Python scanner version mismatch')
        installed = json.loads(capture([python, '-I', '-c',
            'import importlib.metadata as m,json; print(json.dumps({d.metadata["Name"]:d.version for d in m.distributions()}))']))
        expected = {re.sub(r'[-_.]+', '-', n).lower(): v for n, v in re.findall(r'^([A-Za-z0-9_.-]+)==([^\s\\]+)',
                     (ROOT / 'automation/security-tools.lock').read_text(), re.M)}
        if expected != {re.sub(r'[-_.]+', '-', n).lower(): v for n, v in installed.items()}:
            raise ValueError('Installed scanner dependency inventory differs from hash lock')
        entrypoints = {'govulncheck': govuln, 'pip-audit': venv / 'bin/pip-audit',
                       'zizmor': venv / 'bin/zizmor', 'npm': npm}
        report = dict(schema_version=1, commit=capture(['git', 'rev-parse', 'HEAD']),
                      run_id=os.environ.get('GITHUB_RUN_ID', 'local'), run_attempt=os.environ.get('GITHUB_RUN_ATTEMPT', 'local'),
                      created_utc=datetime.now(timezone.utc).isoformat(), pins=pins, python_versions=versions, installed_python=installed,
                      python=sys.version, runner_image=os.environ.get('ImageVersion', 'unknown'),
                      go_build=build, go_version=capture(['go', 'version']), node_version=capture(['node', '--version']),
                      inputs={name: sha(ROOT / name) for name in INPUTS},
                      entrypoints={name: {'path': str(p.relative_to(ROOT)), 'sha256': sha(p)} for name, p in entrypoints.items()})
        (TOOLS / 'ready.json').write_text(json.dumps(report, indent=2) + '\n', encoding='utf-8')
        print('PASS pinned scanner installation and integrity; no project dependencies updated.')
        return 0
    except Exception as error:
        (TOOLS / 'setup-error.json').write_text(json.dumps({'status': 'BLOCKED', 'error': str(error)}), encoding='utf-8')
        print('BLOCKED scanner installation: ' + str(error))
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
