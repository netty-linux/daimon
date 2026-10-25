"""Credential-free evidence collection. Fixed commands only; never invokes C2."""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
STATES = {'PASS', 'FAIL', 'BLOCKED', 'NOT RUN', 'UNVERIFIED'}
MAX_LOG = 2 * 1024 * 1024
MAX_ATTACHMENT = 8 * 1024 * 1024
SECURITY_LANES = {'security-go', 'security-python', 'security-npm', 'security-workflows', 'security-provenance', 'security-upstream'}


def attachment_name(name):
    return bool(re.fullmatch(r'stage-\d\d\.log\.gz|vitest\.json|scan\.json|scanner\.stderr\.log', name))


def security_result(directory, report):
    spec = importlib.util.spec_from_file_location('audited_security_parser', ROOT / 'automation/security_parsers.py')
    parser = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(parser)
    context = report['security_context']
    pins = json.loads((ROOT / 'automation/security-tools.json').read_text())
    if context['commit'] != report['commit'] or context['versions'] != pins:
        raise ValueError('Scanner identity/pin/commit mismatch')
    capsule = report.get('scanner_capsule')
    if capsule and any(capsule[k] != report[k] for k in ('commit', 'run_id', 'run_attempt')):
        raise ValueError('Stale scanner environment capsule')
    try:
        text = (directory / 'scan.json').read_text(encoding='utf-8')
    except UnicodeDecodeError:
        if not report.get('unavailable'):
            raise ValueError('Invalid scanner UTF-8 without explicit incomplete status')
        text = ''
    actual = parser.normalize(report['lane'], text,
                              report['exit_code'], context, report.get('unavailable'))
    if actual['complete'] and (not capsule or context.get('toolchain_verified') is not True or capsule.get('pins') != pins):
        raise ValueError('Complete scan lacks a verified run-bound toolchain capsule')
    if actual != report['security'] or actual['status'] != report['status']:
        raise ValueError('Scanner findings/status differ from preserved raw evidence')
    return actual
INTEGRATIONS = {
    'python-upstream-integration': {'verify-upstream', 'build-dependencies', 'sync-upstream', 'worker', 'native-substrate', 'linux-limits'},
    'linux-existing-ci': {'gofmt', 'vet', 'go-test', 'go-race'},
    'ui-existing-ci': {'npm-ci', 'vitest', 'build', 'embed-diff'},
    'browser-integration': {'install-browser', 'ui-smoke', 'memory-smoke'},
}
COMMANDS = {
    'automation-linux': ('Linux', [sys.executable, '-I', '-B', '-m', 'unittest', 'discover', '-s', 'automation/tests', '-v']),
    'automation-windows': ('Windows', [sys.executable, '-I', '-B', '-m', 'unittest', 'discover', '-s', 'automation/tests', '-v']),
    'windows-contracts': ('Windows', ['go', 'test', '-json', '-count=1', './internal/harness2']),
    'linux-demo': ('Linux', ['go', 'run', './cmd/daimon', 'demo']),
    'automation-policy': ('Linux', [sys.executable, '-I', '-B', 'automation/policy.py']),
}
COMMANDS.update({lane: ('Linux', [sys.executable, '-I', '-B', 'automation/integrations.py', lane]) for lane in INTEGRATIONS})


def integration_result(lane, log):
    records = [json.loads(line.removeprefix('DAIMON_INTEGRATION_RESULT ')) for line in log.splitlines() if line.startswith('DAIMON_INTEGRATION_RESULT ')]
    if len(records) != 1:
        raise ValueError('Exactly one structured integration result required')
    record = records[0]
    if record.get('schema_version') != 1 or record.get('lane') != lane or record.get('status') not in STATES:
        raise ValueError('Invalid integration schema, lane or status')
    stages = record['stages']
    if len({s['name'] for s in stages}) != len(stages):
        raise ValueError('Duplicate integration stage')
    if record['status'] in ('PASS', 'UNVERIFIED'):
        if not INTEGRATIONS[lane] <= {s['name'] for s in stages} or any(s['status'] not in ('PASS', 'UNVERIFIED') for s in stages):
            raise ValueError('Incomplete integration stages')
        if any((s['status'] == 'PASS' and (s['exit_code'] != 0 or s['skipped'])) or
               (s['status'] == 'UNVERIFIED' and not s['skipped']) for s in stages):
            raise ValueError('Stage exit/skip evidence contradicts status')
        skipped = [dict(stage=s['name'], **skip) for s in stages for skip in s['skipped']]
        if skipped != record['skipped'] or (record['status'] == 'PASS' and skipped):
            raise ValueError('Hidden skipped tests')
        for s in stages:
            if s['name'] in ('worker', 'native-substrate', 'linux-limits', 'vitest', 'ui-smoke', 'memory-smoke', 'go-test', 'go-race') and s['tests'] <= 0:
                raise ValueError('No executed tests for mandatory stage')
    return record


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def revision(root=ROOT):
    value = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    if not re.fullmatch('[0-9a-f]{40}', value):
        raise ValueError('Invalid checked-out revision')
    return value


def classify(lane, code, log):
    if lane in INTEGRATIONS:
        record = integration_result(lane, log)
        if code and record['status'] == 'PASS':
            return 'FAIL', 'Process exit contradicts structured evidence', record['skipped']
        return record['status'], record['reason'], record['skipped']
    if code != 0:
        return 'FAIL', 'Command failed', []
    if lane == 'windows-contracts':
        events = [json.loads(line) for line in log.splitlines() if line.strip()]
        skipped = [e.get('Test', e.get('Package', '?')) for e in events if e.get('Action') == 'skip']
        if any(e.get('Action') == 'fail' for e in events):
            return 'FAIL', 'Go reported failure despite exit status', skipped
        if skipped:
            return 'UNVERIFIED', 'Skipped tests are not evidence of coverage', skipped
        if not any(e.get('Action') == 'pass' and e.get('Test') for e in events):
            return 'UNVERIFIED', 'No executed Go tests confirmed', []
    elif lane.startswith('automation-') and lane != 'automation-policy':
        match = re.search(r'Ran (\d+) tests?', log)
        if not match or int(match[1]) == 0 or not re.search(r'^OK\s*$', log, re.M):
            return 'UNVERIFIED', 'No complete unittest success confirmed (including skip cases)', []
    elif lane == 'linux-demo':
        if 'Resposta final: DAIMON' not in log or 'loop_stopped' not in log:
            return 'UNVERIFIED', 'Expected existing demo contract not observed', []
    elif 'PASS workflow policy;' not in log:
        return 'UNVERIFIED', 'Policy completion not observed', []
    return 'PASS', 'Executed this exact non-operational scope successfully', []


def write_bundle(output, report, log=None, attachments=None):
    output.mkdir(parents=True, exist_ok=False)
    if log is not None:
        (output / 'raw.log').write_text(log, encoding='utf-8')
    for name, source in (attachments or {}).items():
        if not attachment_name(name) or source.is_symlink() or source.stat().st_size > MAX_ATTACHMENT:
            raise ValueError('Unsafe or oversized integration attachment')
        shutil.copyfile(source, output / name)
    (output / 'report.json').write_text(json.dumps(report, indent=2, ensure_ascii=True) + '\n', encoding='utf-8')
    rows = report.get('coverage', [report])
    summary = ['# DAIMON Development Automation', '', 'CI evidence only; not operational isolation proof.', '',
               '| Scope | Status | Reason |', '| --- | --- | --- |']
    for row in rows:
        cells = [str(row[k]).replace('|', '\\|').replace('\n', ' ') for k in ('lane', 'status', 'reason')]
        summary.append('| ' + ' | '.join(cells) + ' |')
        security = row.get('security')
        if security:
            count = (str(security['advisory_count']) + ' advisories / ' + str(security['finding_count']) + ' records') if security['complete'] else 'unknown (scanner incomplete)'
            if row['lane'] == 'security-upstream' and security['complete']:
                count = str(security['static_rule_count']) + ' static rules / ' + str(security['finding_count']) + ' risk records (not advisories)'
            summary.append('| ' + row['lane'] + ' findings | ' + count + ' | No automatic remediation |')
    (output / 'summary.md').write_text('\n'.join(summary) + '\n', encoding='utf-8')
    files = {p.name: {'sha256': digest(p), 'bytes': p.stat().st_size} for p in sorted(output.iterdir())}
    (output / 'manifest.json').write_text(json.dumps({'schema_version': 1, 'files': files}, indent=2) + '\n', encoding='utf-8')


def run_lane(lane, output):
    wanted_os, command = COMMANDS[lane]
    report = {'schema_version': 1, 'lane': lane, 'commit': revision(),
              'created_utc': datetime.now(timezone.utc).isoformat(), 'status': 'BLOCKED',
              'reason': 'Command not started', 'command': command, 'os': platform.platform(),
              'python': platform.python_version(), 'runner_image': os.environ.get('ImageVersion', 'unknown'),
              'run_id': os.environ.get('GITHUB_RUN_ID', 'local'),
              'run_attempt': os.environ.get('GITHUB_RUN_ATTEMPT', 'local'),
              'worktree_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT, text=True).strip()),
              'operational_proof': False, 'isolation_gates': 'UNVERIFIED'}
    inventory = json.loads((ROOT / 'automation/coverage.json').read_text(encoding='utf-8'))
    report['scope'] = next(r['scope'] for r in inventory['lanes'] if r['id'] == lane)
    if output.exists():
        raise ValueError('Fresh evidence directory required; prior results are never overwritten')
    log = ''; attachments = {}
    if platform.system() != wanted_os:
        report['reason'] = f'Requires {wanted_os}; no cross-platform proof inferred'
    elif not shutil.which(command[0]):
        report['reason'] = 'Required runtime is unavailable; no automatic installation'
    else:
        # Logs go to a file, not an unbounded in-memory capture. Temporary file is
        # exclusively owned by this collector; only its direct child can be stopped.
        import tempfile
        with tempfile.TemporaryFile() as capture:
            try:
                result = subprocess.run(command, cwd=ROOT, stdout=capture, stderr=subprocess.STDOUT,
                                        timeout=1200 if lane in INTEGRATIONS else 240, check=False, shell=False)
                report['exit_code'] = result.returncode
                capture.seek(0, 2)
                size = capture.tell()
                capture.seek(0)
                log = capture.read(MAX_LOG).decode('utf-8', errors='replace')
                report['original_log_bytes'] = size
                if size > MAX_LOG:
                    report.update(status='FAIL' if result.returncode else 'UNVERIFIED', reason='Log exceeded evidence bound; truncated, cannot attest full output')
                else:
                    status, reason, skipped = classify(lane, result.returncode, log)
                    report.update(status=status, reason=reason, skipped=skipped)
                    if lane in INTEGRATIONS:
                        details = integration_result(lane, log)
                        report['integration'] = details
                        for name, source in details['attachments'].items():
                            path = (ROOT / source).resolve(strict=True)
                            if not path.is_relative_to((ROOT / '.automation-results/stages' / lane).resolve()):
                                raise ValueError('Integration attachment outside its own staging directory')
                            attachments[name] = path
                        report['attachments'] = sorted(attachments)
            except subprocess.TimeoutExpired:
                capture.seek(0)
                log = capture.read(MAX_LOG).decode('utf-8', errors='replace')
                report.update(status='FAIL', reason='Command timeout; direct child stopped; descendant cleanup NOT attested')
            except (OSError, ValueError, json.JSONDecodeError) as error:
                report.update(status='FAIL', reason='Invalid execution/evidence: ' + type(error).__name__)
    write_bundle(output, report, log, attachments)
    print(report['status'] + ': ' + report['reason'])
    return 0 if report['status'] == 'PASS' or (lane == 'linux-existing-ci' and report['status'] == 'UNVERIFIED' and report.get('exit_code') == 0) else 1


def load_verified(directory, expected_commit, expected_run=None, expected_attempt=None, expected_scopes=None):
    if directory.is_symlink() or (directory / 'manifest.json').is_symlink() or (directory / 'manifest.json').stat().st_size > MAX_LOG or (directory / 'report.json').stat().st_size > MAX_LOG * 2:
        raise ValueError('Unsafe or oversized manifest')
    manifest = json.loads((directory / 'manifest.json').read_text(encoding='utf-8'))
    report = json.loads((directory / 'report.json').read_text(encoding='utf-8'))
    extra = report.get('attachments', [])
    if len(extra) != len(set(extra)):
        raise ValueError('Duplicate artifact attachment declaration')
    if any(not attachment_name(name) for name in extra):
        raise ValueError('Unknown attachment name')
    if manifest.get('schema_version') != 1 or set(manifest['files']) != {'report.json', 'summary.md', 'raw.log', *extra}:
        raise ValueError('Incomplete or unknown evidence bundle')
    for name, expected in manifest['files'].items():
        path = directory / name
        if path.is_symlink() or path.stat().st_size > (MAX_ATTACHMENT if name in extra else MAX_LOG * 2) or path.stat().st_size != expected['bytes'] or digest(path) != expected['sha256']:
            raise ValueError('Evidence bytes changed or exceeded bounds')
    report = json.loads((directory / 'report.json').read_text(encoding='utf-8'))
    if report.get('schema_version') != 1 or report['commit'] != expected_commit or report['status'] not in STATES or report.get('worktree_dirty') is not False or report.get('operational_proof') is not False or report.get('isolation_gates') != 'UNVERIFIED':
        raise ValueError('Invalid revision, schema, state or authority claim')
    if expected_run is not None and report.get('run_id') != expected_run:
        raise ValueError('Evidence belongs to another workflow run')
    if expected_attempt is not None and report.get('run_attempt') != expected_attempt:
        raise ValueError('Evidence belongs to another workflow attempt')
    if expected_scopes is not None and (report['lane'] not in expected_scopes or report.get('scope') != expected_scopes[report['lane']]):
        raise ValueError('Evidence scope mismatch')
    if report['lane'] in INTEGRATIONS:
        details = integration_result(report['lane'], (directory / 'raw.log').read_text(encoding='utf-8'))
        if details != report.get('integration') or details['status'] != report['status']:
            raise ValueError('Imported structured integration evidence mismatch')
    if report['lane'] in SECURITY_LANES:
        security_result(directory, report)
    return report


def cross_job_provenance(reports):
    errors = []
    upstream = reports.get('security-upstream')
    worker = reports.get('python-upstream-integration')
    if upstream and upstream.get('security', {}).get('complete') and worker:
        covered = upstream['security']['coverage']
        actual = worker.get('integration', {}).get('provenance', {}).get('upstream_revision')
        if actual != covered.get('revision'):
            errors.append('Independent upstream scan and real worker revisions differ')
    if upstream and upstream.get('security', {}).get('complete'):
        py = reports.get('security-python', {})
        if not py.get('security', {}).get('complete'):
            errors.append('Upstream build/scanner dependency advisory evidence incomplete')
        else:
            targets = {p['name'] for p in py['security']['coverage']['targets']}
            declared = upstream['security']['coverage'].get('dependencies', {}).get('build_declared', [])
            names = {re.split(r'[<>=!~;\[\s]', requirement)[0].lower() for requirement in declared}
            if not names <= targets or not {'setuptools', 'wheel', 'packaging'} <= targets:
                errors.append('Upstream build requirements absent from PyPI advisory targets')
    audited = reports.get('security-provenance')
    if not audited or not audited.get('security', {}).get('complete'):
        return errors  # Missing/incomplete required scanner remains non-PASS separately.
    pins = audited['security_context']['input_hashes']
    capsule = audited.get('scanner_capsule') or {}
    for lane in ('python-upstream-integration', 'linux-existing-ci', 'ui-existing-ci', 'browser-integration'):
        row = reports.get(lane, {})
        if row.get('status') not in ('PASS', 'UNVERIFIED'):
            continue
        facts = row.get('integration', {}).get('provenance', {})
        if lane == 'python-upstream-integration':
            claimed = facts.get('pins', {})
            expected_names = {'python/harness2_bridge/uv.lock', 'python/harness2_bridge/pyproject.toml',
                              'python/harness2_bridge/upstream.lock.json', 'automation/build-requirements.txt'}
            if set(claimed) != expected_names or any(pins.get(name) != value for name, value in claimed.items()):
                errors.append('Cross-job Python dependency pin mismatch')
        elif lane == 'linux-existing-ci' and facts.get('go_version') != capsule.get('go_version'):
            errors.append('Cross-job Go toolchain mismatch')
        elif lane in ('ui-existing-ci', 'browser-integration'):
            if facts.get('node_lock_sha256') != pins.get('ui/package-lock.json'):
                errors.append('Cross-job npm lock mismatch: ' + lane)
            if lane == 'ui-existing-ci' and facts.get('node_version') != capsule.get('node_version'):
                errors.append('Cross-job Node toolchain mismatch')
    return errors


def aggregate(inputs, output, inventory=None):
    inventory = inventory or json.loads((ROOT / 'automation/coverage.json').read_text(encoding='utf-8'))
    commit = revision()
    reports, errors = {}, []
    allowed = {row['id'] for row in inventory['lanes'] if row['implemented']}
    scopes = {row['id']: row['scope'] for row in inventory['lanes'] if row['implemented']}
    for path in sorted(inputs.rglob('report.json')):
        try:
            report = load_verified(path.parent, commit, os.environ.get('GITHUB_RUN_ID'), os.environ.get('GITHUB_RUN_ATTEMPT'), scopes)
            lane = report['lane']
            if lane not in allowed or lane in reports:
                raise ValueError('Unknown or duplicate lane')
            reports[lane] = report
        except (OSError, ValueError, KeyError, TypeError):
            errors.append('Invalid or duplicate evidence bundle: ' + path.parent.name)
    errors.extend(cross_job_provenance(reports))
    rows = []
    for source in inventory['lanes']:
        row = {'lane': source['id'], 'scope': source['scope'], 'status': source['status'], 'reason': source['reason']}
        if source['id'] in reports:
            row.update({key: reports[source['id']][key] for key in ('status', 'reason')})
            row['skipped'] = reports[source['id']].get('skipped', [])
            row['source'] = {k: reports[source['id']].get(k) for k in ('commit', 'run_id', 'run_attempt', 'scope')}
            row['stages'] = reports[source['id']].get('integration', {}).get('stages', [])
            if 'security' in reports[source['id']]:
                row['security'] = reports[source['id']]['security']
        rows.append(row)
    priority = ['FAIL', 'BLOCKED', 'UNVERIFIED', 'NOT RUN', 'PASS']
    status = 'FAIL' if errors else next(value for value in priority if any(r['status'] == value for r in rows))
    report = {'schema_version': 1, 'lane': 'coverage-matrix', 'commit': commit, 'status': status,
              'created_utc': datetime.now(timezone.utc).isoformat(), 'coverage': rows, 'errors': errors,
              'reason': 'All scopes reported separately; CI success never unlocks operational gates',
              'operational_proof': False, 'isolation_gates': 'UNVERIFIED'}
    write_bundle(output, report)
    print('Coverage: ' + status)
    # A green reporting job means only that required safe lanes supplied PASS.
    # Planned/privileged rows can remain BLOCKED without suppressing their status.
    return 1 if errors or any(reports.get(row['id'], {}).get('status') not in
                             (('PASS', 'UNVERIFIED') if row.get('allow_unverified') and reports.get(row['id'], {}).get('exit_code') == 0 else ('PASS',))
                             for row in inventory['lanes'] if row['implemented']) else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    task = parser.add_mutually_exclusive_group(required=True)
    task.add_argument('--lane', choices=sorted(COMMANDS))
    task.add_argument('--aggregate', type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    return run_lane(args.lane, args.output) if args.lane else aggregate(args.aggregate, args.output)


if __name__ == '__main__':
    raise SystemExit(main())
