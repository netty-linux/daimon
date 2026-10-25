"""Pure source/snapshot checks. Never imports, compiles or executes upstream code."""
import ast
import base64
import hashlib
import json
from pathlib import PurePosixPath
import sys
import tomllib


class IntegrityError(ValueError):
    pass


def digest(data):
    return hashlib.sha256(data).hexdigest()


def security_path(path):
    return path.endswith(('.py', '.sh', '.toml', '.cfg', '.ini', '.diff', '.json', '.yaml', '.yml')) or path in ('LICENSE', 'README.md', 'MANIFEST.in') or PurePosixPath(path).name == 'Dockerfile'


def safe_path(path):
    return isinstance(path, str) and bool(path) and not path.startswith(('/', '-')) and not any(ord(c) < 32 for c in path) and '\\' not in path and all(p not in ('', '.', '..') for p in path.split('/')) and ':' not in path


def verify_snapshot(snapshot, policy):
    if snapshot.get('repository') != policy['repository'] or snapshot.get('revision') != policy['revision'] or snapshot.get('tree') != policy['tree']:
        raise IntegrityError('Upstream origin/commit/tree mismatch')
    if snapshot.get('entries') != policy['entries']:
        raise IntegrityError('Git tree contains missing, changed or uninventoried entries')
    contents = snapshot.get('sources', {})
    selected = {p for p in policy['entries'] if security_path(p)}
    if set(contents) != selected:
        raise IntegrityError('Source inventory incomplete or extended')
    result = {}
    for path, item in policy['entries'].items():
        if not safe_path(path) or item['mode'] not in ('100644', '100755'):
            raise IntegrityError('Unsafe path, symlink or submodule in inventory')
        if path not in selected:
            continue  # Binary/document blobs remain explicitly Git-object inventoried.
        data = base64.b64decode(contents[path], validate=True)
        oid = hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest()
        if len(data) != item['bytes'] or len(data) > 1024 * 1024 or digest(data) != item['sha256'] or oid != item['oid']:
            raise IntegrityError('Source bytes/hash/Git blob identity mismatch: ' + path)
        result[path] = data
    return result


def aliases(tree):
    names = {}
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for value in node.names:
                names[value.asname or value.name.split('.')[0]] = value.name if value.asname else value.name.split('.')[0]
        elif isinstance(node, ast.ImportFrom) and node.module:
            for value in node.names:
                names[value.asname or value.name] = node.module + '.' + value.name
    return names


def qualified(node, names):
    if isinstance(node, ast.Name):
        return names.get(node.id, node.id)
    if isinstance(node, ast.Attribute):
        return qualified(node.value, names) + '.' + node.attr
    return '<dynamic>'


def risk_calls(tree):
    names, findings = aliases(tree), []
    paths = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign) and isinstance(node.value, ast.Call) and qualified(node.value.func, names).startswith('pathlib.'):
            paths.update(t.id for t in node.targets if isinstance(t, ast.Name))
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        name = qualified(node.func, names)
        tail = name.rsplit('.', 1)[-1]
        rule = None
        if name.startswith(('subprocess.', 'os.exec', 'os.spawn')) or name in ('os.system', 'os.popen', 'os.fork'):
            rule = 'PROCESS'
        elif name in ('eval', 'exec', 'compile', '__import__') or name.startswith(('importlib.', 'runpy.')):
            rule = 'DYNAMIC-CODE'
        elif name.startswith(('pickle.', 'dill.', 'marshal.')) or name in ('yaml.load', 'yaml.unsafe_load'):
            rule = 'DESERIALIZE'
        elif name in ('json.load', 'json.loads'):
            rule = 'JSON-INPUT'
        elif tail in ('open', 'read_text', 'read_bytes', 'write_text', 'write_bytes', 'unlink', 'rmdir', 'mkdir', 'rename', 'rglob', 'glob', 'iterdir', 'chmod', 'symlink_to', 'hardlink_to') or name.startswith(('shutil.', 'tempfile.')) or name in ('os.remove', 'os.makedirs', 'os.replace') or (tail == 'replace' and name.split('.')[0] in paths):
            rule = 'FILESYSTEM'
        elif name.startswith(('socket.', 'requests.', 'httpx.', 'urllib.request.')):
            rule = 'NETWORK'
        if rule:
            findings.append(dict(rule='DA32-' + rule, line=node.lineno, column=node.col_offset, call=name,
                                 severity='medium', kind='risk-signal', confirmed_vulnerability=False))
    return sorted(findings, key=lambda f: (f['line'], f['column'], f['rule']))


def analyze(sources, policy, worker_source, native_source):
    # Bind the reviewed runtime boundary to actual DAIMON content, not only a label.
    if digest(worker_source.replace(b'\r\n', b'\n')) != policy['worker_sha256']:
        raise IntegrityError('Worker boundary changed without reviewed inventory')
    worker = ast.parse(worker_source)
    loader = next(n for n in worker.body if isinstance(n, ast.FunctionDef) and n.name == 'load_upstream')
    tuples = [n for n in ast.walk(loader) if isinstance(n, ast.For) and isinstance(n.iter, ast.Tuple)]
    loaded = []
    for node in tuples:
        try:
            rows = ast.literal_eval(node.iter)
        except (ValueError, TypeError):
            continue
        if rows and all(isinstance(row, tuple) and len(row) == 3 and isinstance(row[1], str) and row[1].startswith('harness2/') for row in rows):
            loaded.extend(row[1] for row in rows)
    if loaded != policy['worker_loaded']:
        raise IntegrityError('Runtime import/exec boundary differs from reviewed policy')
    if digest(native_source.replace(b'\r\n', b'\n')) != policy['native_adapter_sha256']:
        raise IntegrityError('Native adapter boundary changed without reviewed inventory')
    native = ast.parse(native_source)
    extra = [n.value.value for n in ast.walk(native) if isinstance(n, ast.Assign) and
             any(isinstance(t, ast.Name) and t.id == 'rel' for t in n.targets) and isinstance(n.value, ast.Constant) and isinstance(n.value.value, str)]
    if extra != ['harness2/substrate/base.py'] or loaded + extra != policy['runtime_loaded']:
        raise IntegrityError('Native adapter source import differs from reviewed boundary')
    loaded += extra
    modules, imports, risks = {}, [], []
    for path, data in sorted(sources.items()):
        if not path.endswith('.py'):
            continue
        tree = ast.parse(data, filename=path)
        usage = 'runtime-loaded' if path in loaded else 'installed-not-loaded' if path.startswith('harness2/') else 'repository-not-installed'
        modules[path] = dict(sha256=digest(data), usage=usage)
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                values = [v.name for v in node.names]
            elif isinstance(node, ast.ImportFrom):
                values = [('.' * node.level) + (node.module or '')]
            else:
                continue
            for name in values:
                root = name.split('.')[0]
                classification = 'relative' if name.startswith('.') else 'stdlib' if root in sys.stdlib_module_names or root == '__future__' else 'upstream' if root == 'harness2' else 'unresolved-external'
                imports.append(dict(path=path, line=node.lineno, module=name, classification=classification, usage=usage))
        risks.extend(dict(path=path, usage=usage, **finding) for finding in risk_calls(tree))
    project = tomllib.loads(sources['pyproject.toml'].decode())
    dependencies = dict(runtime_declared=project['project'].get('dependencies', []),
                        optional_not_installed=project['project'].get('optional-dependencies', {}),
                        build_declared=project['build-system']['requires'],
                        build_backend=project['build-system']['build-backend'],
                        entrypoints_not_invoked=project['project'].get('scripts', {}),
                        dynamic_metadata=project['project'].get('dynamic', []))
    gaps = [dict(scope='whole-upstream', reason='Static syntactic rules are not interprocedural taint/reachability or proof of absence of vulnerabilities; security signals need review'),
            dict(scope='optional-and-vendored', reason='Extras, third-party tools, shell scripts and patches are inventoried but not installed/resolved/executed; their transitive advisory coverage is NOT RUN')]
    if dependencies['runtime_declared'] or dependencies['dynamic_metadata']:
        gaps.append(dict(scope='runtime-dependencies', reason='New or dynamic runtime requirements require a separately reviewed complete hash lock and advisory audit'))
    if any(i['usage'] == 'runtime-loaded' and i['classification'] not in ('stdlib', 'upstream', 'relative') for i in imports):
        gaps.append(dict(scope='runtime-imports', reason='Unresolved external imports in loaded modules'))
    return dict(modules=modules, imports=imports, risk_signals=risks, dependencies=dependencies, coverage_gaps=gaps,
                runtime_loaded=loaded, rules=['DA32-PROCESS', 'DA32-DYNAMIC-CODE', 'DA32-DESERIALIZE', 'DA32-JSON-INPUT', 'DA32-FILESYSTEM', 'DA32-NETWORK'])


def bandit_findings(raw, exit_code, files):
    if exit_code not in (0, 1) or not isinstance(raw, dict) or raw.get('errors') != [] or not isinstance(raw.get('results'), list):
        raise ValueError('Bandit scanner failed or omitted results/errors')
    metrics = raw.get('metrics', {})
    if set(metrics) != set(files) | {'_totals'}:
        raise ValueError('Bandit did not scan exactly every Python file')
    if any(metrics[p].get('skipped_tests') != 0 for p in metrics):
        raise ValueError('Bandit skipped tests/rules')
    if bool(raw['results']) != (exit_code == 1):
        raise IntegrityError('Bandit exit contradicts findings')
    for severity in ('LOW', 'MEDIUM', 'HIGH', 'UNDEFINED'):
        expected = sum(r.get('issue_severity') == severity for r in raw['results'])
        if metrics['_totals'].get('SEVERITY.' + severity) != expected:
            raise IntegrityError('Bandit severity metrics contradict findings')
    result, seen = [], set()
    for row in raw['results']:
        if row['filename'] not in files or row['issue_severity'] not in ('LOW', 'MEDIUM', 'HIGH') or row.get('issue_confidence') not in ('LOW', 'MEDIUM', 'HIGH') or type(row['line_number']) is not int or row['line_number'] <= 0:
            raise IntegrityError('Bandit finding identity/location invalid')
        ident = (row['filename'], row['line_number'], row['test_id'], row.get('col_offset'))
        if ident in seen:
            raise IntegrityError('Duplicate Bandit finding')
        seen.add(ident)
        result.append(dict(path=row['filename'], line=row['line_number'], rule=row['test_id'], severity=row['issue_severity'].lower(),
                           confidence=row['issue_confidence'], description=row['issue_text'], kind='scanner-finding', confirmed_vulnerability=False))
    return result


def evaluate(data, policy, worker_source, expected_bandit, native_source):
    if data.get('schema_version') != 1 or data.get('scanner', {}).get('version') != expected_bandit or data['scanner'].get('name') != 'bandit' or data['scanner'].get('ignore_nosec') is not True:
        raise IntegrityError('Unexpected upstream scanner/version/suppression policy')
    sources = verify_snapshot(data['snapshot'], policy)
    facts = analyze(sources, policy, worker_source, native_source)
    if facts != data.get('analysis'):
        raise IntegrityError('Static analysis differs from independently recomputed source facts')
    findings = bandit_findings(data['bandit'], data['bandit_exit'], facts['modules'])
    findings.extend(facts['risk_signals'])
    return findings, facts
