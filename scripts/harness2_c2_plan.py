"""Read-only resource planner. Never creates/deletes VM, disk, directory or journal.

Output is private operator review material, not an authorization capability.
Python 3.12+, standard library only. Hyper-V inventory/preflight is separate.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import uuid


def read_json(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('duplicate key')
            result[key] = value
        return result
    with Path(path).open('rb') as f:
        raw = f.read(131073)
    if len(raw) > 131072:
        raise ValueError('bounded document exceeded')
    return json.loads(raw, object_pairs_hook=unique)


def private_target(raw):
    p = Path(raw)
    if not p.is_absolute() or '..' in p.parts or str(p).startswith(('\\\\', '//')):
        raise ValueError('explicit local absolute path required')
    for part in (p, *p.parents):
        if part.is_symlink() or (hasattr(part, 'is_junction') and part.is_junction()):
            raise ValueError('reparse/link boundary rejected')
    if p.exists():
        raise ValueError('target already exists')
    if p.parent == p or not p.parent.is_dir():
        raise ValueError('controlled existing parent required')
    return p


def artifact(path, size, digest):
    p = Path(path)
    for part in (p, *p.parents):
        if part.is_symlink() or (hasattr(part, 'is_junction') and part.is_junction()):
            raise ValueError('artifact link boundary rejected')
    if not p.is_file() or p.stat().st_size != size:
        raise ValueError('artifact missing or wrong size')
    with p.open('rb') as f:
        actual = hashlib.file_digest(f, 'sha256').hexdigest()
    if actual != digest:
        raise ValueError('artifact digest mismatch')


def create_plan(manifest, root, name, iso, package):
    if manifest.get('schema_version') != 1:
        raise ValueError('unsupported deployment schema')
    if not re.fullmatch(r'[A-Za-z0-9-]{1,48}', name):
        raise ValueError('invalid VM name')
    target = private_target(root)
    u, g = manifest['ubuntu'], manifest['gvisor']['apt']
    artifact(iso, u['observed_http_content_length'], u['candidate_sha256'])
    artifact(package, g['package_size'], g['published_package_sha256'])
    return {
        'schema_version': 1, 'mode': 'create-dry-run', 'administrative_actions': 0,
        'status': 'BLOCKED',
        'blockers': ['Windows patch/ESU approval', 'independent runtime authentication',
                     'complete deployment closure', 'live elevated inventory and ACL review',
                     'memory admission', 'explicit creation authorization'],
        'vm': {'name': name, 'generation': 2, 'vcpus': 2,
               'minimum_memory_gib': 3, 'startup_memory_gib': 3,
               'maximum_memory_gib': 4, 'vhdx_max_gib': 48,
               'network': 'disconnected', 'automatic_start': False},
        'journal_intent': {'version': 1, 'operation_id': str(uuid.uuid4()),
                           'state': 'intent-not-persisted', 'vm_id': None,
                           'root': str(target), 'vhdx': str(target / 'lab.vhdx'),
                           'manifest_sha256': hashlib.sha256(json.dumps(
                               manifest, sort_keys=True).encode()).hexdigest()},
        'next': 'Persist protected intent before creation; record returned VMId and disk identities; never infer ownership from names.'}


def discard_plan(journal, expected_vm_id, expected_root):
    # Pure identity checks only. Live VM/disk/ACL verification still mandatory.
    if journal.get('version') != 1 or journal.get('state') != 'created':
        raise ValueError('uncommitted ownership journal')
    identity = str(uuid.UUID(expected_vm_id))
    if journal.get('vm_id') != identity:
        raise ValueError('VM identity mismatch')
    root = Path(expected_root)
    disk = Path(journal['vhdx'])
    if not root.is_absolute() or '..' in root.parts or '..' in disk.parts:
        raise ValueError('invalid ownership path')
    if str(root) != journal.get('root') or disk != root / 'lab.vhdx':
        raise ValueError('disk/root identity mismatch')
    for p in (root, disk, *root.parents):
        if p.is_symlink() or (hasattr(p, 'is_junction') and p.is_junction()):
            raise ValueError('ownership reparse boundary')
    return {'schema_version': 1, 'mode': 'discard-dry-run', 'status': 'BLOCKED',
            'administrative_actions': 0, 'vm_id': identity,
            'required': ['authenticate protected journal and ACL',
                         'query exact VMId; require Off',
                         'match live disk identifiers and exclusive references',
                         'recheck containment and links immediately before deletion',
                         'explicit disposal authorization'],
            'shared_switch_actions': [], 'deletion_performed': False}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--mode', choices=['create', 'discard'], required=True)
    p.add_argument('--root', required=True)
    p.add_argument('--manifest')
    p.add_argument('--name', default='daimon-c2-lab')
    p.add_argument('--iso')
    p.add_argument('--package')
    p.add_argument('--journal')
    p.add_argument('--vm-id')
    args = p.parse_args()
    try:
        if args.mode == 'create':
            result = create_plan(read_json(args.manifest),
                                 args.root, args.name, args.iso, args.package)
        else:
            result = discard_plan(read_json(args.journal),
                                  args.vm_id, args.root)
        print(json.dumps(result, indent=2))
        return 3  # Never an admission/authorization success.
    except (ValueError, OSError, KeyError, TypeError):
        print(json.dumps({'status': 'REFUSED', 'administrative_actions': 0,
                          'reason': 'invalid/incomplete artifacts or ownership'}))
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
