"""Narrow, non-privileged workflow policy checks, NOT a security certification."""
from pathlib import Path
import re


def check(root):
    errors = []
    workflows = sorted((root / '.github' / 'workflows').glob('*.yml'))
    if not workflows:
        return ['No workflows found']
    for path in workflows:
        text = path.read_text(encoding='utf-8-sig')
        for action in re.findall(r'^\s*-?\s*uses:\s*([^\s#]+)', text, re.M):
            if action == './.github/workflows/ci.yml':
                continue  # Reviewed local reusable CI; same checked-out revision.
            if not re.fullmatch(r'[\w-]+/[\w.-]+@[0-9a-f]{40}', action):
                errors.append(f'{path.name}: unpinned or unsupported action {action}')
        for expression in (r'pull_request_target', r'workflow_run', r'self-hosted',
                           r'\bsudo\b', r'-Verb\s+RunAs', r'\bwrite-all\b',
                           r'\b[\w-]+:\s*write\b', r'\bsecrets\.',
                           r'Start-VM|Stop-VM|Set-VM|New-VM', r'openai/codex-action'):
            if re.search(expression, text, re.I):
                errors.append(f'{path.name}: forbidden hosted execution/authority: {expression}')
        if 'contents: read' not in text or 'persist-credentials: false' not in text:
            errors.append(f'{path.name}: missing explicit read-only checkout policy')
    return errors


if __name__ == '__main__':
    issues = check(Path(__file__).resolve().parents[1])
    for issue in issues:
        print(issue)
    if issues:
        raise SystemExit(1)
    print('PASS workflow policy; no vulnerability scan or operational proof claimed.')
