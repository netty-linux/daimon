"""Strict, network-free normalization of pinned scanner output. Data is never code."""
import json
import re

LANES = {'security-go', 'security-python', 'security-npm', 'security-workflows', 'security-provenance'}


class Incomplete(ValueError):
    pass


class Contradiction(ValueError):
    pass


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise Contradiction('Duplicate JSON key: ' + key)
        result[key] = value
    return result


def decode(text):
    try:
        return json.loads(text, object_pairs_hook=pairs, parse_constant=lambda _: (_ for _ in ()).throw(Contradiction('Nonfinite JSON')))
    except json.JSONDecodeError as error:
        raise Incomplete('Malformed/truncated scanner JSON') from error


def stream(text):
    decoder = json.JSONDecoder(object_pairs_hook=pairs, parse_constant=lambda _: (_ for _ in ()).throw(Contradiction('Nonfinite JSON')))
    records = []
    offset = 0
    try:
        while text[offset:].strip():
            offset += len(text[offset:]) - len(text[offset:].lstrip())
            value, offset = decoder.raw_decode(text, offset)
            if not isinstance(value, dict) or len(value) != 1:
                raise Contradiction('Expected a single-message Go JSON object')
            records.append(value)
    except json.JSONDecodeError as error:
        raise Incomplete('Truncated govulncheck stream') from error
    return records


def record(advisory, package, version, severity=None, fixes=None, **extra):
    if not all(isinstance(s, str) and s for s in (advisory, package, version)):
        raise Incomplete('Advisory identity/package/version missing')
    return dict(advisory=advisory, package=package, affected_version=version, severity=severity,
                recommended_fix=fixes or [], **extra)


def go(text, code, context):
    if code != 0:
        raise Incomplete('govulncheck failed; partial JSON is not a clean audit')
    messages = stream(text)
    configs = [m['config'] for m in messages if 'config' in m]
    sboms = [m['SBOM'] for m in messages if 'SBOM' in m]
    if len(configs) > 1 or len(sboms) > 1:
        raise Contradiction('Duplicate Go scan config/SBOM')
    if len(configs) != 1 or len(sboms) != 1 or not sboms[0].get('modules') or not sboms[0].get('roots'):
        raise Incomplete('govulncheck config/SBOM/roots missing')
    c = configs[0]
    if c.get('protocol_version') != 'v1.0.0' or c.get('scanner_name') != 'govulncheck' or c.get('scanner_version') != context['versions']['govulncheck'] or c.get('scan_mode') != 'source' or c.get('db') != 'https://vuln.go.dev' or c.get('go_version') != sboms[0].get('go_version'):
        raise Contradiction('Unexpected Go scanner/version/database/mode')
    definitions = {}; duplicate_definitions = 0; duplicate_findings = 0
    for m in messages:
        if not set(m) <= {'config', 'SBOM', 'osv', 'finding', 'progress'}:
            raise Incomplete('Unrecognized Go protocol message')
        if 'osv' in m:
            osv = m['osv']
            if osv['id'] in definitions:
                if definitions[osv['id']] != osv:
                    raise Contradiction('Repeated OSV definition contradicts previous data')
                duplicate_definitions += 1
            definitions[osv['id']] = osv
    findings, seen = [], set()
    for m in messages:
        if 'finding' not in m:
            continue
        f = m['finding']
        if f.get('osv') not in definitions or not f.get('trace'):
            raise Incomplete('Finding has no advisory definition/trace')
        signature = json.dumps(f, sort_keys=True)
        if signature in seen:
            duplicate_findings += 1
            continue
        seen.add(signature)
        trace = f['trace'][0]
        osv = definitions[f['osv']]
        findings.append(record(f['osv'], trace.get('module'), trace.get('version'),
                               osv.get('severity'), [f['fixed_version']] if f.get('fixed_version') else [],
                               aliases=sorted(osv.get('aliases', [])), url='https://pkg.go.dev/vuln/' + f['osv'],
                               affected_ranges=osv.get('affected', []), trace=f['trace'],
                               reachability='symbol' if trace.get('function') else ('package' if trace.get('package') else 'module')))
    return findings, [], {'database': c['db'], 'database_modified': c.get('db_last_modified'),
                         'go_version': c.get('go_version'), 'scan_level': c.get('scan_level'), 'modules': sboms[0]['modules'],
                         'duplicate_definitions': duplicate_definitions, 'duplicate_findings': duplicate_findings,
                         'limitations': ['Current GOOS/GOARCH only; reflective/unsafe reachability may be incomplete.']}


def python(text, code, context):
    data = decode(text)
    if not isinstance(data, dict) or not isinstance(data.get('dependencies'), list) or 'fixes' not in data:
        raise Incomplete('pip-audit result incomplete')
    if data['fixes']:
        raise Contradiction('Dependency fixes were not authorized')
    expected = {(p['name'], p['version']) for p in context['python_targets']}
    actual, findings, skipped, indexed = set(), [], [], {}
    for dep in data['dependencies']:
        key = (re.sub(r'[-_.]+', '-', dep['name']).lower(), dep.get('version'))
        if key in actual:
            raise Contradiction('Duplicate audited Python dependency')
        actual.add(key)
        if dep.get('skip_reason'):
            skipped.append({'package': dep['name'], 'reason': dep['skip_reason']})
            continue
        if not isinstance(dep.get('vulns'), list):
            raise Incomplete('Missing Python vulnerability list')
        for vuln in dep['vulns']:
            ident = (key, vuln['id'])
            fixes = sorted(vuln['fix_versions'])
            aliases = sorted(set(vuln.get('aliases', [])))
            if ident in indexed:
                prior = indexed[ident]
                if prior['recommended_fix'] != fixes or prior['aliases'] != aliases:
                    raise Contradiction('Repeated Python advisory has conflicting remediation/aliases')
                prior['source_occurrences'] += 1
                continue  # Some registry records repeat equivalent IDs/descriptions.
            entry = record(vuln['id'], key[0], key[1], vuln.get('severity'), fixes, aliases=aliases,
                           url='https://osv.dev/vulnerability/' + vuln['id'], source_occurrences=1,
                           description=vuln.get('description', ''))
            indexed[ident] = entry
            findings.append(entry)
    if actual != expected:
        raise Incomplete('pip-audit omitted or added target package/version')
    if code not in (0, 1) or (bool(findings) != (code == 1) and not skipped):
        raise Contradiction('pip-audit exit code contradicts findings')
    skipped.append({'package': 'harness2@' + context['vcs_revision'],
                    'reason': 'Git-pinned source is integrity-checked by DA-2; PyPI advisory coverage for this VCS identity is unverified. Never query an unrelated PyPI namesake.'})
    return findings, skipped, {'targets': context['python_targets'],
                              'limitations': ['Severity is null when the scanner does not provide it.', 'No CPython/OS or VCS-source vulnerability coverage.']}


def npm(text, code, context):
    data = decode(text)
    if data.get('error'):
        raise Incomplete('npm registry/scanner error: ' + str(data['error'].get('code', 'unknown')))
    if data.get('auditReportVersion') != 2 or not isinstance(data.get('vulnerabilities'), dict):
        raise Incomplete('Unexpected npm audit schema')
    counts = data.get('metadata', {}).get('vulnerabilities', {})
    vulnerabilities = data['vulnerabilities']
    if any(type(counts.get(k)) is not int or counts[k] < 0 for k in ('info', 'low', 'moderate', 'high', 'critical', 'total')):
        raise Incomplete('npm counts absent or not integers')
    if counts.get('total') != len(vulnerabilities) or sum(counts.get(k, -1000) for k in ('info', 'low', 'moderate', 'high', 'critical')) != counts.get('total'):
        raise Contradiction('npm severity/advisory totals contradict package records')
    dependencies = data.get('metadata', {}).get('dependencies', {}).get('total', 0)
    if type(dependencies) is not int or dependencies <= 0:
        raise Incomplete('npm audited no dependencies')
    if code not in (0, 1) or (bool(vulnerabilities) != (code == 1)):
        raise Contradiction('npm exit code contradicts vulnerabilities')
    findings = []
    for name, vuln in vulnerabilities.items():
        if vuln.get('name') != name or not vuln.get('nodes') or not isinstance(vuln.get('via'), list):
            raise Incomplete('npm affected package/nodes/via missing')
        fix = vuln.get('fixAvailable')
        fixes = [fix['name'] + '@' + fix['version']] if isinstance(fix, dict) else []
        for node in vuln['nodes']:
            package = context['npm_packages'].get(node)
            if not package or not package.get('version'):
                raise Incomplete('npm finding is not bound to the audited lockfile')
            sources = [v for v in vuln['via'] if isinstance(v, dict)]
            for v in sources or [dict(url=None, source='meta:' + name, title='Indirect vulnerable dependency', range=vuln.get('range'))]:
                url = v.get('url')
                advisory = url.rsplit('/', 1)[-1] if url else str(v['source'])
                findings.append(record(advisory, name, package['version'], v.get('severity', vuln.get('severity')), fixes,
                                       url=url, affected_range=v.get('range'), dependency_path=node,
                                       description=v.get('title'), fix_available=fix,
                                       remediation_requires_review=True))
    return findings, [], {'counts': counts, 'dependencies': data['metadata']['dependencies'],
                          'limitations': ['Registry advisory snapshot; dependency metadata is sent to the public npm registry.', 'Not a guarantee against malicious packages or unknown vulnerabilities.']}


def workflows(text, code, context):
    if context.get('workflow_policy_error'):
        raise Incomplete('Workflow policy could not parse all inputs: ' + context['workflow_policy_error'])
    data = decode(text)
    if code != 0 or data.get('version') != '2.1.0' or len(data.get('runs', [])) != 1:
        raise Incomplete('zizmor SARIF not completed')
    run = data['runs'][0]
    driver = run.get('tool', {}).get('driver', {})
    if driver.get('name') != 'zizmor' or driver.get('version') != context['versions']['zizmor'] or not isinstance(run.get('results'), list):
        raise Contradiction('Unexpected zizmor tool version or missing results')
    findings, seen = [], set()
    for item in run['results']:
        locations = item.get('locations', [])
        if not locations or not item.get('ruleId') or not item.get('message', {}).get('text'):
            raise Incomplete('Workflow finding incomplete')
        location = locations[0]['physicalLocation']
        file = location['artifactLocation']['uri']
        line = location.get('region', {}).get('startLine')
        key = (item['ruleId'], file, line)
        if key in seen:
            raise Contradiction('Duplicate workflow finding')
        seen.add(key)
        findings.append(record(item['ruleId'], file, context['commit'], item.get('level'), [],
                               line=line, description=item['message']['text'],
                               url='https://docs.zizmor.sh/audits/#' + item['ruleId'].split('/')[-1]))
    findings.extend(context.get('workflow_policy_findings', []))
    return findings, [], {'mode': 'offline/no-config/no-ignores',
                          'limitations': ['Online action advisories, impostor commit and producer authentication checks NOT RUN without credentials.']}


def provenance(text, code, context):
    data = decode(text)
    if code != 0 or data.get('schema_version') != 1 or not isinstance(data.get('checks'), list) or not data['checks']:
        raise Incomplete('Integrity checks were not completed')
    names = [c['name'] for c in data['checks']]
    if len(set(names)) != len(names) or set(names) != {'git-clean', 'locked-python', 'locked-npm', 'toolchain', 'inputs-unchanged'}:
        raise Contradiction('Missing/duplicate integrity criteria')
    if any(c.get('status') not in ('PASS', 'FAIL') for c in data['checks']):
        raise Incomplete('Integrity check unknown')
    findings = [record('DA3-INTEGRITY-' + c['name'], 'CI inputs', context['commit'], 'high', [], description=c.get('reason'))
                for c in data['checks'] if c['status'] == 'FAIL']
    return findings, [], {'checks': data['checks'], 'input_hashes': context['input_hashes'],
                          'limitations': ['Content hashes and Go sumdb are integrity/provenance evidence, not independent producer signatures or OS attestation.']}


def normalize(lane, text, code, context, unavailable=None):
    if unavailable:
        return dict(status='BLOCKED', reason=unavailable, complete=False, findings=[], finding_count=None, coverage={})
    try:
        findings, skipped, coverage = {'security-go': go, 'security-python': python, 'security-npm': npm,
                                       'security-workflows': workflows, 'security-provenance': provenance}[lane](text, code, context)
        status = 'FAIL' if findings else ('UNVERIFIED' if skipped else 'PASS')
        return dict(status=status, reason='Known findings require review' if findings else ('Explicit coverage gaps' if skipped else 'Complete audit within declared scope; no known findings'),
                    complete=True, findings=findings, finding_count=len(findings), advisory_count=len({f['advisory'] for f in findings}), skipped=skipped, coverage=coverage)
    except Incomplete as error:
        return dict(status='BLOCKED' if code else 'UNVERIFIED', reason=str(error), complete=False, findings=[], finding_count=None, coverage={})
    except (Contradiction, KeyError, TypeError, AttributeError) as error:
        return dict(status='FAIL', reason='Invalid/contradictory scanner evidence: ' + str(error), complete=False, findings=[], finding_count=None, coverage={})
