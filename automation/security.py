"""Run all DA-3 scanners independently; findings/failures never suppress another scan."""
from datetime import datetime, timezone
import base64
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(Path(__file__).resolve().parent))
import evidence
import security_parsers as parsers
from security_setup import TOOLS, INPUTS, environment, sha


def python_targets():
    targets = {}
    for name in ('automation/build-requirements.txt', 'automation/security-tools.lock'):
        for package, version in re.findall(r'^([A-Za-z0-9_.-]+)==([^\s\\]+)', (ROOT / name).read_text(), re.M):
            package = re.sub(r'[-_.]+', '-', package).lower()
            if package in targets and targets[package] != version:
                raise ValueError('Conflicting Python target versions')
            targets[package] = version
    return [{'name': p, 'version': v} for p, v in sorted(targets.items())]


def strong_npm_integrity(value):
    try:
        return isinstance(value, str) and value.startswith('sha512-') and len(base64.b64decode(value[7:], validate=True)) == 64
    except ValueError:
        return False


def workflow_policy(document, file, commit):
    issues = []
    def fail(rule, description):
        issues.append(parsers.record('DA3-WORKFLOW-' + rule, file, commit, 'high', [], description=description))
    permissions = document.get('permissions')
    if permissions != {'contents': 'read'}:
        fail('permissions', 'Explicit contents: read only required at workflow level')
    events = document.get('on', {})
    if not isinstance(events, dict) or set(events) & {'pull_request_target', 'workflow_run'}:
        fail('triggers', 'Unsafe or unknown trigger definition')
    jobs = document.get('jobs')
    if not isinstance(jobs, dict) or not jobs:
        fail('jobs', 'No reviewable jobs')
        return issues
    for name, job in jobs.items():
        if job.get('permissions', permissions) != {'contents': 'read'}:
            fail('job-permissions-' + name, 'Job expands or obscures token permission scope')
        if job.get('secrets'):
            fail('secrets-' + name, 'Passing/inheriting secrets is forbidden in this pipeline')
        if 'self-hosted' in json.dumps(job.get('runs-on', '')):
            fail('runner-' + name, 'Dedicated privileged runners are outside this workflow')
        uses = job.get('uses')
        if uses and uses != '$/.github/workflows/ci.yml':
            fail('reusable-' + name, 'Only reviewed same-repository CI reference allowed')
        for index, step in enumerate(job.get('steps', [])):
            action = step.get('uses')
            if action and not re.fullmatch(r'[\w-]+/[\w.-]+@[0-9a-f]{40}', action):
                fail('pin-' + name + str(index), 'Action is not commit pinned')
            if action and action.startswith('actions/checkout@') and str(step.get('with', {}).get('persist-credentials')).lower() != 'false':
                fail('checkout-' + name + str(index), 'Checkout may persist credentials')
            if re.search(r'\$\{\{\s*(github\.event|inputs\.|needs\.)', step.get('run', '')):
                fail('injection-' + name + str(index), 'Untrusted expression inserted into shell program')
            if re.search(r'\b(sudo|Start-VM|Stop-VM|Set-VM)\b|secrets\.|github\.token', json.dumps(step), re.I):
                fail('authority-' + name + str(index), 'Privileged command or explicit secret/token use')
    return issues


def workflow_findings(commit):
    sys.path.insert(0, str(TOOLS / 'venv/lib/python3.12/site-packages'))
    import yaml
    pins = json.loads((ROOT / 'automation/security-tools.json').read_text())
    if yaml.__version__ != pins['PyYAML']:
        raise ValueError('Workflow parser version differs from reviewed pin')
    class Loader(yaml.SafeLoader):
        pass
    # YAML 1.1 treats the Actions key `on` as a boolean; preserve it as a string.
    Loader.yaml_implicit_resolvers = {k: [(tag, expr) for tag, expr in values if tag != 'tag:yaml.org,2002:bool']
                                      for k, values in Loader.yaml_implicit_resolvers.items()}
    Loader.add_implicit_resolver('tag:yaml.org,2002:bool', re.compile(r'^(?:true|false)$', re.I), list('tTfF'))
    def mapping(loader, node):
        result = {}
        for key_node, value_node in node.value:
            key = loader.construct_object(key_node)
            if key in result:
                raise ValueError('Duplicate workflow YAML key')
            result[key] = loader.construct_object(value_node)
        return result
    Loader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, mapping)
    result = []
    directory = ROOT / '.github/workflows'
    for path in sorted([*directory.glob('*.yml'), *directory.glob('*.yaml')]):
        result.extend(workflow_policy(yaml.load(path.read_text(), Loader=Loader), path.relative_to(ROOT).as_posix(), commit))
    return result


def integrity(context, ready):
    lock = json.loads((ROOT / 'ui/package-lock.json').read_text())
    import tomllib
    uv = tomllib.loads((ROOT / 'python/harness2_bridge/uv.lock').read_text())
    vcs = [p for p in uv['package'] if p.get('source', {}).get('git')]
    locked = len(vcs) == 1 and context['vcs_revision'] in vcs[0]['source']['git']
    packages = [p for name, p in lock['packages'].items() if name]
    npm_locked = all(p.get('version') and p.get('resolved', '').startswith('https://registry.npmjs.org/') and
                     strong_npm_integrity(p.get('integrity')) for p in packages)
    checks = [dict(name='git-clean', status='PASS' if not context['dirty'] else 'FAIL', reason='Tracked checkout state'),
              dict(name='locked-python', status='PASS' if locked else 'FAIL', reason='Git dependency revision agrees with upstream lock'),
              dict(name='locked-npm', status='PASS' if npm_locked and packages else 'FAIL', reason='Registry archives have versions and integrity digests'),
              dict(name='toolchain', status='PASS' if ready and context['toolchain_verified'] else 'FAIL', reason='Fresh run-bound scanner capsule and entrypoint hashes'),
              dict(name='inputs-unchanged', status='PASS' if ready and ready['inputs'] == context['input_hashes'] else 'FAIL', reason='No dependency/workflow/lock mutation by scanning')]
    return dict(schema_version=1, checks=checks)


def main():
    commit = evidence.revision()
    pins = json.loads((ROOT / 'automation/security-tools.json').read_text())
    scopes = {r['id']: r['scope'] for r in json.loads((ROOT / 'automation/coverage.json').read_text())['lanes']}
    ready, setup_error = None, None
    try:
        ready = json.loads((TOOLS / 'ready.json').read_text())
        if ready['commit'] != commit or ready['run_id'] != os.environ.get('GITHUB_RUN_ID', 'local') or ready['run_attempt'] != os.environ.get('GITHUB_RUN_ATTEMPT', 'local') or ready['pins'] != pins:
            raise ValueError('Stale or changed scanner capsule')
        if any(not (ROOT / entry['path']).resolve().is_relative_to(TOOLS.resolve()) or sha(ROOT / entry['path']) != entry['sha256'] for entry in ready['entrypoints'].values()):
            raise ValueError('Scanner entrypoint was modified after installation')
    except (OSError, ValueError, KeyError) as error:
        setup_error = str(error)
    dirty = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip())
    context = dict(versions=pins, commit=commit, dirty=dirty, toolchain_verified=setup_error is None,
                   input_hashes={name: sha(ROOT / name) for name in INPUTS}, python_targets=python_targets(),
                   vcs_revision=json.loads((ROOT / 'python/harness2_bridge/upstream.lock.json').read_text())['revision'],
                   npm_packages=json.loads((ROOT / 'ui/package-lock.json').read_text())['packages'],
                   workflow_policy_findings=[])
    if not setup_error:
        try:
            context['workflow_policy_findings'] = workflow_findings(commit)
        except Exception as error:
            context['workflow_policy_error'] = str(error)
    stage = ROOT / '.automation-results/security-stage'
    stage.mkdir(parents=True, exist_ok=False)
    env = environment()
    ve = TOOLS / 'venv/bin'
    commands = {
        'security-go': [str(TOOLS / 'bin/govulncheck'), '-json', './...'],
        'security-python': [str(ve / 'pip-audit'), '--disable-pip', '--no-deps', '--require-hashes', '--progress-spinner', 'off',
                            '--format', 'json', '-r', str(ROOT / 'automation/build-requirements.txt'), '-r', str(ROOT / 'automation/security-tools.lock')],
        'security-npm': ['node', str(TOOLS / 'npm/package/bin/npm-cli.js'), 'audit', '--json', '--package-lock-only', '--ignore-scripts', '--audit-level=low'],
        'security-workflows': [str(ve / 'zizmor'), '--offline', '--no-config', '--no-ignores', '--format', 'sarif', '.github/workflows'],
        'security-provenance': [],
        'security-upstream': [sys.executable, '-I', '-B', str(ROOT / 'automation/upstream_security.py')],
        'security-triage': [sys.executable, '-I', '-B', str(ROOT / 'automation/context_triage.py'),
                            '--run-id', os.environ.get('GITHUB_RUN_ID','local'),
                            '--run-attempt', os.environ.get('GITHUB_RUN_ATTEMPT','local')],
    }
    failed = False
    for lane, command in commands.items():
        directory = stage / lane
        directory.mkdir()
        stdout, stderr = directory / 'scan.json', directory / 'scanner.stderr.log'
        code, unavailable = 0, setup_error
        started = datetime.now(timezone.utc).isoformat(); clock = time.monotonic()
        if not unavailable:
            try:
                if lane == 'security-provenance':
                    context['input_hashes'] = {name: sha(ROOT / name) for name in INPUTS}
                    stdout.write_text(json.dumps(integrity(context, ready)), encoding='utf-8'); stderr.write_text('')
                else:
                    with stdout.open('xb') as out, stderr.open('xb') as err:
                        result = subprocess.run(command, cwd=ROOT / 'ui' if lane == 'security-npm' else ROOT,
                                                env=env, stdout=out, stderr=err, timeout=240, check=False, shell=False)
                    code = result.returncode
            except (OSError, subprocess.SubprocessError) as error:
                unavailable = str(error)
        if not stdout.exists():
            stdout.write_text('', encoding='utf-8')
        if not stderr.exists():
            stderr.write_text(unavailable or '', encoding='utf-8')
        original_sizes = {'stdout': stdout.stat().st_size, 'stderr': stderr.stat().st_size}
        for source in (stdout, stderr):
            if source.stat().st_size > evidence.MAX_ATTACHMENT:
                with source.open('rb') as capture:
                    bounded = capture.read(evidence.MAX_ATTACHMENT)
                source.write_bytes(bounded)
                unavailable = 'Scanner output exceeded evidence bounds; retained prefix only, no completeness claim'
        try:
            text = stdout.read_text(encoding='utf-8', errors='strict')
        except UnicodeDecodeError:
            text = ''
            unavailable = 'Scanner returned invalid UTF-8; raw bytes preserved, no clean result'
        normalized = parsers.normalize(lane, text, code, context, unavailable)
        report = dict(schema_version=1, lane=lane, scope=scopes[lane], commit=commit,
                      created_utc=started, finished_utc=datetime.now(timezone.utc).isoformat(),
                      elapsed_seconds=round(time.monotonic() - clock, 3), run_id=os.environ.get('GITHUB_RUN_ID', 'local'),
                      run_attempt=os.environ.get('GITHUB_RUN_ATTEMPT', 'local'), worktree_dirty=dirty,
                      os=sys.platform, python=sys.version, operational_proof=False, isolation_gates='UNVERIFIED',
                      command=command, exit_code=code, original_output_bytes=original_sizes, status=normalized['status'], reason=normalized['reason'],
                      security=normalized, security_context=context, unavailable=unavailable,
                      scanner_capsule=ready, attachments=['scan.json', 'scanner.stderr.log'])
        evidence.write_bundle(ROOT / '.automation-results' / lane, report,
                              'Scanner status is based on schema, completeness and findings, not exit zero alone.\n',
                              {'scan.json': stdout, 'scanner.stderr.log': stderr})
        print(lane + ': ' + report['status'] + '; ' + report['reason'], flush=True)
        failed = failed or report['status'] != 'PASS'
    return 1 if failed else 0


if __name__ == '__main__':
    raise SystemExit(main())
