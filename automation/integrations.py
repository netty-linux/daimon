"""DA-2 fixed, non-privileged recipes. Never imports C2 or invokes a real provider."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
MAX_STAGE = 32 * 1024 * 1024
UPSTREAM = '1cfe254e8d25b15f37048c3730134686abe60335'
REQUIRED_WORKER_TESTS = {
    'TestRealWorkerEndToEnd', 'TestRealWorkerLifecycle', 'TestRealWorkerEnvironment',
    'TestActiveWorkerCancellationAndClose', 'TestStartupDeadlinePreservesContext',
    'TestExecutableStartFailureCleansScratch', 'TestLinuxOrdinaryDescendantGroupTermination',
}


def go_results(text, require_worker=True):
    events = [json.loads(line) for line in text.splitlines() if line.strip()]
    passed = [e for e in events if e.get('Action') == 'pass' and e.get('Test')]
    skips, no_tests = [], []
    for event in events:
        if event.get('Action') != 'skip':
            continue
        item = {'package': event.get('Package'), 'test': event.get('Test'), 'reason': ''.join(
            x.get('Output', '') for x in events if x.get('Action') == 'output' and x.get('Package') == event.get('Package') and x.get('Test') == event.get('Test'))}
        # Go emits package-level skip for [no test files]. Keep that NOT RUN
        # explicitly, without conflating it with a skipped, existing test case.
        if not item['test'] and '[no test files]' in item['reason']:
            no_tests.append(dict(status='NOT RUN', **item))
        else:
            skips.append(item)
    if not passed or any(e.get('Action') == 'fail' for e in events):
        raise ValueError('Missing executed Go tests or explicit Go failure')
    worker = {e['Test'] for e in passed if e.get('Package', '').endswith('/internal/harness2bridge')}
    if require_worker and (not REQUIRED_WORKER_TESTS <= worker or any('harness2' in (s['package'] or '') for s in skips)):
        raise ValueError('Required real worker Go integration was absent or skipped')
    return {'tests': len(passed), 'skipped': skips, 'not_run_packages': no_tests,
            'worker_tests_confirmed': sorted(worker & REQUIRED_WORKER_TESTS)}


def vitest_results(data):
    skipped = [{'test': item.get('fullName', item.get('title', '?')), 'reason': item.get('status')}
               for suite in data.get('testResults', []) for item in suite.get('assertionResults', [])
               if item.get('status') in ('pending', 'skipped', 'todo', 'disabled')]
    total = data.get('numTotalTests', 0)
    pending = data.get('numPendingTests', 0) + data.get('numTodoTests', 0)
    if pending and not skipped:
        skipped = [{'test': 'unidentified', 'reason': f'{pending} pending/todo tests'}]
    if not data.get('success') or not total or data.get('numFailedTests', 0):
        raise ValueError('Vitest did not confirm successful executed tests')
    if not skipped and data.get('numPassedTests') != total:
        raise ValueError('Incomplete Vitest assertion count')
    return {'tests': total, 'skipped': skipped}


class Pipeline:
    def __init__(self, lane):
        self.lane = lane
        self.directory = ROOT / '.automation-results' / 'stages' / lane
        self.directory.mkdir(parents=True, exist_ok=False)
        self.stages = []
        self.attachments = {}
        self.provenance = {}

    def run(self, name, argv, cwd=ROOT, inspect=None, env=None, timeout=600):
        index = len(self.stages)
        raw = self.directory / f'stage-{index:02d}.raw'
        stage = {'name': name, 'command': list(map(str, argv)), 'status': 'FAIL', 'tests': 0, 'skipped': []}
        self.stages.append(stage)
        started = time.monotonic()
        print('START ' + name, flush=True)
        try:
            with raw.open('xb') as capture:
                result = subprocess.run(stage['command'], cwd=cwd, env=env, stdout=capture,
                                        stderr=subprocess.STDOUT, check=False, timeout=timeout, shell=False)
            stage['exit_code'] = result.returncode
            if raw.stat().st_size > MAX_STAGE:
                raise ValueError('Stage log bound exceeded; no completeness claim')
            text = raw.read_text(encoding='utf-8', errors='strict')
            facts = inspect(text) if inspect else {}
            stage.update(facts or {})
            if result.returncode and not stage['skipped']:
                raise ValueError(f'{name} command failed: {result.returncode}; inspect compressed log')
            stage['status'] = 'UNVERIFIED' if stage['skipped'] else 'PASS'
            return text
        finally:
            stage['elapsed_seconds'] = round(time.monotonic() - started, 3)
            if raw.exists():
                stage['raw_bytes'] = raw.stat().st_size
                name_gz = f'stage-{index:02d}.log.gz'
                packed = self.directory / name_gz
                with raw.open('rb') as source, gzip.open(packed, 'xb') as dest:
                    # Compression does not turn a truncated/oversized stage into PASS.
                    remaining = MAX_STAGE
                    while remaining:
                        block = source.read(min(65536, remaining))
                        if not block:
                            break
                        dest.write(block)
                        remaining -= len(block)
                stage['log'] = name_gz
                self.attachments[name_gz] = str(packed.relative_to(ROOT))
            print(stage['status'] + ' ' + name, flush=True)


def unittest_facts(name, text):
    rows = [json.loads(line.removeprefix('DAIMON_UNITTEST_RESULT ')) for line in text.splitlines() if line.startswith('DAIMON_UNITTEST_RESULT ')]
    if len(rows) != 1 or rows[0]['suite'] != name or rows[0]['tests'] <= 0 or rows[0].get('failures', 0) or rows[0].get('errors', 0):
        raise ValueError('Missing, skipped or unsuccessful real Python suite')
    if not rows[0]['successful'] and not rows[0]['skipped']:
        raise ValueError('Unsuccessful Python suite')
    return {'tests': rows[0]['tests'], 'skipped': rows[0]['skipped']}


def python_environment(p):
    if sys.version_info[:2] != (3, 12):
        raise ValueError('Existing upstream contract requires Python 3.12')
    lock = json.loads((ROOT / 'python/harness2_bridge/upstream.lock.json').read_text())
    if lock['revision'] != UPSTREAM or lock['repository'] != 'https://github.com/google-research/harness2':
        raise ValueError('Unreviewed upstream identity')
    checkout = p.directory / 'upstream'
    p.run('fetch-upstream', ['git', 'clone', '--no-checkout', '--filter=blob:none', lock['repository'], checkout])
    p.run('checkout-upstream', ['git', '-C', checkout, 'checkout', '--detach', UPSTREAM])
    p.run('verify-upstream', [sys.executable, '-I', '-S', '-B', 'python/harness2_bridge/verify_pin.py', checkout])
    version = p.run('uv-version', ['uv', '--version'])
    if not version.startswith('uv 0.12.17 '):
        raise ValueError('Unreviewed uv version')
    bridge = ROOT / 'python/harness2_bridge'
    venv = bridge / '.venv'
    if venv.exists():
        raise ValueError('Fresh Python environment required')
    p.run('create-venv', ['uv', 'venv', '--python', sys.executable, '--no-managed-python', venv])
    python = venv / ('Scripts/python.exe' if os.name == 'nt' else 'bin/python')
    p.run('build-dependencies', ['uv', 'pip', 'install', '--python', python, '--require-hashes',
                                '--only-binary', ':all:', '-r', ROOT / 'automation/build-requirements.txt'])
    p.run('sync-upstream', ['uv', 'sync', '--project', bridge, '--locked', '--inexact', '--no-build-isolation',
                           '--no-managed-python', '--python', sys.executable])
    site = venv / ('Lib/site-packages' if os.name == 'nt' else 'lib/python3.12/site-packages')
    env = dict(os.environ, DAIMON_HARNESS2_TEST_PYTHON=str(python), DAIMON_HARNESS2_TEST_SITE=str(site))
    p.provenance = {'upstream_revision': UPSTREAM, 'python': sys.version,
                    'uv': version.strip(), 'pins': {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
                     for name in ('python/harness2_bridge/uv.lock', 'python/harness2_bridge/pyproject.toml',
                                  'python/harness2_bridge/upstream.lock.json', 'automation/build-requirements.txt')}}
    github_env = os.environ.get('GITHUB_ENV')
    if github_env:
        with open(github_env, 'a', encoding='utf-8') as target:
            target.write(f'DAIMON_HARNESS2_TEST_PYTHON={python}\nDAIMON_HARNESS2_TEST_SITE={site}\n')
    return python, env


def python_suite(p):
    python, env = python_environment(p)
    for name in ('worker', 'native-substrate', 'linux-limits'):
        p.run(name, [python, '-I', '-S', '-B', ROOT / 'automation/unittest_integration.py', name],
              env=env, inspect=lambda text, name=name: unittest_facts(name, text))


def linux_ci(p):
    p.provenance = {'go_version': p.run('go-version', ['go', 'version']).strip()}
    if not all(os.environ.get(k) for k in ('DAIMON_HARNESS2_TEST_PYTHON', 'DAIMON_HARNESS2_TEST_SITE')):
        raise ValueError('Real worker prerequisite absent; refusing optional integration skips')
    def formatting(text):
        if text.strip():
            raise ValueError('Go formatting drift')
        return {}
    tracked_go = subprocess.check_output(['git', 'ls-files', '--', '*.go'], cwd=ROOT, text=True).splitlines()
    if not tracked_go:
        raise ValueError('No tracked Go source found')
    p.run('gofmt', ['gofmt', '-l', *tracked_go], inspect=formatting)
    p.run('vet', ['go', 'vet', './...'])
    p.run('go-test', ['go', 'test', '-json', '-count=1', './...'], inspect=go_results)
    p.run('go-race', ['go', 'test', '-json', '-race', '-count=1', './...'], inspect=go_results)


def ui_ci(p):
    ui = ROOT / 'ui'
    node_version = p.run('node-version', ['node', '--version']).strip()
    p.run('npm-ci', ['npm', 'ci'], cwd=ui)
    result = p.directory / 'vitest.json'
    try:
        p.run('vitest', ['npm', 'test', '--', '--reporter=json', '--outputFile=' + str(result)], cwd=ui,
              inspect=lambda _: vitest_results(json.loads(result.read_text(encoding='utf-8'))))
    finally:
        if result.exists():
            p.attachments['vitest.json'] = str(result.relative_to(ROOT))
    p.run('build', ['npm', 'run', 'build'], cwd=ui)
    p.run('embed-diff', ['git', 'diff', '--exit-code', '--', 'internal/server/ui'])
    p.provenance = {'node_version': node_version, 'node_lock_sha256': hashlib.sha256((ui / 'package-lock.json').read_bytes()).hexdigest()}


def browser(p):
    ui = ROOT / 'ui'
    p.run('install-browser', ['node', 'node_modules/playwright/cli.js', 'install', 'chromium'], cwd=ui)
    for name, script, marker in (
        ('ui-smoke', 'scripts/smoke.mjs', 'PASS: embedded UI,'),
        ('memory-smoke', 'scripts/memory-smoke.mjs', 'PASS: manual UI CRUD/reload/restart;')):
        def facts(text, marker=marker):
            if marker not in text:
                raise ValueError('Browser completion evidence missing')
            return {'tests': 1, 'skipped': []}
        p.run(name, ['node', script], cwd=ui, inspect=facts)
    core = ui / 'node_modules/playwright-core'
    p.provenance = {'playwright': json.loads((ui / 'node_modules/playwright/package.json').read_text())['version'],
                    'browsers': json.loads((core / 'browsers.json').read_text())['browsers'],
                    'node_lock_sha256': hashlib.sha256((ui / 'package-lock.json').read_bytes()).hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('lane', choices=['python-upstream-integration', 'linux-existing-ci', 'ui-existing-ci', 'browser-integration'])
    args = parser.parse_args()
    p = Pipeline(args.lane)
    result = {'schema_version': 1, 'lane': args.lane, 'status': 'FAIL', 'stages': p.stages,
              'attachments': p.attachments, 'skipped': [], 'provenance': p.provenance}
    try:
        if platform.system() != 'Linux' or os.geteuid() == 0:
            raise PermissionError('Disposable non-root Linux runner required')
        {'python-upstream-integration': python_suite, 'linux-existing-ci': linux_ci,
         'ui-existing-ci': ui_ci, 'browser-integration': browser}[args.lane](p)
        result['skipped'] = [dict(stage=s['name'], **skip) for s in p.stages for skip in s['skipped']]
        result['status'] = 'UNVERIFIED' if result['skipped'] else 'PASS'
        result['reason'] = 'Explicit skipped tests' if result['skipped'] else 'All required integration stages executed'
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        result.update(status='BLOCKED' if isinstance(error, (PermissionError, FileNotFoundError)) else 'FAIL',
                      reason=str(error))
    result['provenance'] = p.provenance
    print('DAIMON_INTEGRATION_RESULT ' + json.dumps(result), flush=True)
    # Only the known non-applicable platform test may keep the legacy Go job green.
    # Its coverage remains UNVERIFIED, with exact names/reasons, never PASS.
    known_skips = result['skipped'] and all(s.get('test') == 'TestManagedPreimageUnsupportedBeforeEffects' for s in result['skipped'])
    return 0 if result['status'] == 'PASS' or (args.lane == 'linux-existing-ci' and result['status'] == 'UNVERIFIED' and known_skips) else 1


if __name__ == '__main__':
    raise SystemExit(main())
