"""Fetch Git objects and scan source as data; no upstream checkout/import/build."""
import base64
import json
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import upstream_checks as checks
from security_setup import ROOT, TOOLS, environment


def collect(directory, policy, env):
    repository = directory / 'objects.git'
    # No credential helpers, local config, hooks, checkout, submodules or LFS.
    env = dict(env, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(directory / 'no-global-config'),
               GIT_TERMINAL_PROMPT='0', GIT_LFS_SKIP_SMUDGE='1')
    def git(*args):
        return subprocess.check_output(['git', '-c', 'credential.helper=', '-c', 'core.hooksPath=' + str(directory / 'no-hooks'),
                                        '--git-dir=' + str(repository), *args], env=env, stderr=sys.stderr, timeout=90)
    subprocess.run(['git', 'init', '--bare', str(repository)], env=env, stdout=sys.stderr, stderr=sys.stderr, check=True, timeout=30)
    git('fetch', '--no-tags', '--depth=1', '--filter=blob:limit=1048576', policy['repository'], policy['revision'])
    revision = git('rev-parse', 'FETCH_HEAD').decode().strip()
    tree = git('rev-parse', 'FETCH_HEAD^{tree}').decode().strip()
    entries, sources = {}, {}
    rows = git('ls-tree', '-rz', 'FETCH_HEAD').split(b'\0')
    for row in rows:
        if not row:
            continue
        meta, name = row.split(b'\t', 1)
        mode, kind, oid = meta.decode().split()
        path = name.decode('utf-8')
        if not checks.safe_path(path) or kind != 'blob' or mode not in ('100644', '100755'):
            raise checks.IntegrityError('Unsupported/symlink/submodule entry in upstream Git tree')
        item = dict(mode=mode, oid=oid)
        if checks.security_path(path):
            size = int(git('cat-file', '-s', oid))
            if size > 1024 * 1024:
                raise checks.IntegrityError('Oversized upstream text blob')
            data = git('cat-file', 'blob', oid)
            item.update(bytes=len(data), sha256=checks.digest(data))
            sources[path] = base64.b64encode(data).decode()
        entries[path] = item
    snapshot = dict(repository=policy['repository'], revision=revision, tree=tree, entries=entries, sources=sources)
    checks.verify_snapshot(snapshot, policy)
    return snapshot


def run(directory, executable, env):
    directory.mkdir(exist_ok=False, parents=True)
    policy = json.loads((ROOT / 'automation/upstream-inventory.lock.json').read_text(encoding='utf-8'))
    bridge = json.loads((ROOT / 'python/harness2_bridge/upstream.lock.json').read_text(encoding='utf-8'))
    if (policy['repository'], policy['revision']) != (bridge['repository'], bridge['revision']):
        raise checks.IntegrityError('Independent inventory differs from bridge source pin')
    if checks.digest((ROOT / 'python/harness2_bridge/verify_pin.py').read_bytes().replace(b'\r\n', b'\n')) != policy['verifier_sha256']:
        raise checks.IntegrityError('Existing acquisition verifier changed')
    snapshot = collect(directory, policy, env)
    sources = checks.verify_snapshot(snapshot, policy)
    native_source = (ROOT / 'python/harness2_native_substrate/substrate.py').read_bytes()
    facts = checks.analyze(sources, policy, (ROOT / 'python/harness2_bridge/worker.py').read_bytes(), native_source)
    source_dir = directory / 'source'
    files = []
    for name in facts['modules']:
        path = source_dir / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(sources[name])
        files.append(name)
    output = directory / 'bandit.json'
    prefix = [str(executable), '-I', '-B', '-m', 'bandit']
    version = subprocess.check_output([*prefix, '--version'], env=env, text=True, timeout=30).splitlines()[0]
    pins = json.loads((ROOT / 'automation/security-tools.json').read_text())
    if version not in ('bandit ' + pins['bandit'], '__main__.py ' + pins['bandit']):
        raise checks.IntegrityError('Bandit version differs from reviewed pin')
    command = [*prefix, '--ignore-nosec', '-c', str(ROOT / 'automation/bandit.json'), '-f', 'json', '-o', str(output), *files]
    result = subprocess.run(command, cwd=source_dir, env=env, stdout=sys.stderr, stderr=sys.stderr, timeout=120, check=False)
    raw = json.loads(output.read_text(encoding='utf-8'))
    # Canonicalize Windows separators and the scanner's ./ prefix only.
    # Refuse collisions; a duplicate cannot silently replace another file's metrics.
    metrics = {k.replace('\\', '/').removeprefix('./'): v for k, v in raw['metrics'].items()}
    if len(metrics) != len(raw['metrics']):
        raise checks.IntegrityError('Bandit metric paths collide after canonicalization')
    raw['metrics'] = metrics
    for finding in raw['results']:
        finding['filename'] = finding['filename'].replace('\\', '/').removeprefix('./')
    data = dict(schema_version=1, snapshot=snapshot, analysis=facts, bandit=raw, bandit_exit=result.returncode,
                scanner=dict(name='bandit', version=pins['bandit'], ignore_nosec=True, command=command))
    checks.evaluate(data, policy, (ROOT / 'python/harness2_bridge/worker.py').read_bytes(), pins['bandit'], native_source)
    return data


def main():
    try:
        data = run(ROOT / '.automation-results/upstream-static', TOOLS / 'venv/bin/python', environment())
        print(json.dumps(data))
        return 0  # Findings are normalized by the independent parent, not hidden by exit zero.
    except Exception as error:
        print(json.dumps(dict(schema_version=1, error=str(error), category='integrity' if isinstance(error, checks.IntegrityError) else 'scanner-unavailable')))
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
