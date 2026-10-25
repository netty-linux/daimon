"""Credential-free evidence collection. Fixed commands only; never invokes C2."""
import argparse
from datetime import datetime, timezone
import hashlib
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
COMMANDS = {
    'automation-linux': ('Linux', [sys.executable, '-I', '-B', '-m', 'unittest', 'discover', '-s', 'automation/tests', '-v']),
    'automation-windows': ('Windows', [sys.executable, '-I', '-B', '-m', 'unittest', 'discover', '-s', 'automation/tests', '-v']),
    'windows-contracts': ('Windows', ['go', 'test', '-json', '-count=1', './internal/harness2']),
    'linux-demo': ('Linux', ['go', 'run', './cmd/daimon', 'demo']),
    'automation-policy': ('Linux', [sys.executable, '-I', '-B', 'automation/policy.py']),
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def revision(root=ROOT):
    value = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    if not re.fullmatch('[0-9a-f]{40}', value):
        raise ValueError('Invalid checked-out revision')
    return value


def classify(lane, code, log):
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


def write_bundle(output, report, log=None):
    output.mkdir(parents=True, exist_ok=False)
    if log is not None:
        (output / 'raw.log').write_text(log, encoding='utf-8')
    (output / 'report.json').write_text(json.dumps(report, indent=2, ensure_ascii=True) + '\n', encoding='utf-8')
    rows = report.get('coverage', [report])
    summary = ['# DAIMON Development Automation', '', 'CI evidence only; not operational isolation proof.', '',
               '| Scope | Status | Reason |', '| --- | --- | --- |']
    for row in rows:
        cells = [str(row[k]).replace('|', '\\|').replace('\n', ' ') for k in ('lane', 'status', 'reason')]
        summary.append('| ' + ' | '.join(cells) + ' |')
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
    if output.exists():
        raise ValueError('Fresh evidence directory required; prior results are never overwritten')
    log = ''
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
                                        timeout=240, check=False, shell=False)
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
            except subprocess.TimeoutExpired:
                capture.seek(0)
                log = capture.read(MAX_LOG).decode('utf-8', errors='replace')
                report.update(status='FAIL', reason='Command timeout; direct child stopped; descendant cleanup NOT attested')
            except (OSError, ValueError, json.JSONDecodeError) as error:
                report.update(status='FAIL', reason='Invalid execution/evidence: ' + type(error).__name__)
    write_bundle(output, report, log)
    print(report['status'] + ': ' + report['reason'])
    return 0 if report['status'] == 'PASS' else 1


def load_verified(directory, expected_commit, expected_run=None, expected_attempt=None):
    if directory.is_symlink() or (directory / 'manifest.json').is_symlink() or (directory / 'manifest.json').stat().st_size > MAX_LOG:
        raise ValueError('Unsafe or oversized manifest')
    manifest = json.loads((directory / 'manifest.json').read_text(encoding='utf-8'))
    if manifest.get('schema_version') != 1 or set(manifest['files']) != {'report.json', 'summary.md', 'raw.log'}:
        raise ValueError('Incomplete or unknown evidence bundle')
    for name, expected in manifest['files'].items():
        path = directory / name
        if path.is_symlink() or path.stat().st_size > MAX_LOG * 2 or path.stat().st_size != expected['bytes'] or digest(path) != expected['sha256']:
            raise ValueError('Evidence bytes changed or exceeded bounds')
    report = json.loads((directory / 'report.json').read_text(encoding='utf-8'))
    if report.get('schema_version') != 1 or report['commit'] != expected_commit or report['status'] not in STATES or report.get('worktree_dirty') is not False or report.get('operational_proof') is not False or report.get('isolation_gates') != 'UNVERIFIED':
        raise ValueError('Invalid revision, schema, state or authority claim')
    if expected_run is not None and report.get('run_id') != expected_run:
        raise ValueError('Evidence belongs to another workflow run')
    if expected_attempt is not None and report.get('run_attempt') != expected_attempt:
        raise ValueError('Evidence belongs to another workflow attempt')
    return report


def aggregate(inputs, output, inventory=None):
    inventory = inventory or json.loads((ROOT / 'automation/coverage.json').read_text(encoding='utf-8'))
    commit = revision()
    reports, errors = {}, []
    allowed = {row['id'] for row in inventory['lanes'] if row['implemented']}
    for path in sorted(inputs.rglob('report.json')):
        try:
            report = load_verified(path.parent, commit, os.environ.get('GITHUB_RUN_ID'), os.environ.get('GITHUB_RUN_ATTEMPT'))
            lane = report['lane']
            if lane not in allowed or lane in reports:
                raise ValueError('Unknown or duplicate lane')
            reports[lane] = report
        except (OSError, ValueError, KeyError, TypeError):
            errors.append('Invalid or duplicate evidence bundle: ' + path.parent.name)
    rows = []
    for source in inventory['lanes']:
        row = {'lane': source['id'], 'scope': source['scope'], 'status': source['status'], 'reason': source['reason']}
        if source['id'] in reports:
            row.update({key: reports[source['id']][key] for key in ('status', 'reason')})
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
    return 1 if errors or any(reports.get(lane, {}).get('status') != 'PASS' for lane in allowed) else 0


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
