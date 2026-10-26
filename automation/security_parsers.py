"""Strict, network-free normalization of pinned scanner output. Data is never code."""
import json
import re

LANES = {'security-go', 'security-python', 'security-npm', 'security-workflows', 'security-provenance', 'security-upstream', 'security-triage'}


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


def upstream(text, code, context):
    from pathlib import Path
    import importlib.util
    root = Path(__file__).resolve().parents[1]
    spec = importlib.util.spec_from_file_location('independent_upstream_checks', root / 'automation/upstream_checks.py')
    checks = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(checks)
    data = decode(text)
    if data.get('error'):
        if data.get('category') == 'integrity':
            raise Contradiction(data['error'])
        raise Incomplete(data['error'])
    if code != 0:
        raise Incomplete('Upstream scanner process did not complete')
    policy = decode((root / 'automation/upstream-inventory.lock.json').read_text(encoding='utf-8'))
    try:
        signals, facts = checks.evaluate(data, policy, (root / 'python/harness2_bridge/worker.py').read_bytes(), context['versions']['bandit'],
                                        (root / 'python/harness2_native_substrate/substrate.py').read_bytes())
    except checks.IntegrityError as error:
        raise Contradiction(str(error)) from error
    except (ValueError, SyntaxError, StopIteration) as error:
        raise Incomplete(str(error)) from error
    findings = [record(f['rule'], f['path'], policy['revision'], f['severity'], [], **{k:v for k,v in f.items() if k not in ('rule','path','severity')}) for f in signals]
    coverage = dict(repository=policy['repository'], revision=policy['revision'], git_tree=policy['tree'],
                    inventory={p:{**item, 'usage': facts['modules'].get(p, {}).get('usage', 'build-input' if p in ('pyproject.toml', 'MANIFEST.in', 'README.md') else 'repository-data-not-executed')} for p,item in policy['entries'].items()},
                    python_files=len(facts['modules']), runtime_loaded=facts['runtime_loaded'], imports=facts['imports'],
                    dependencies=facts['dependencies'], rules=facts['rules'], bandit=data['scanner'],
                    bandit_findings=len(data['bandit']['results']), custom_risk_signals=len(facts['risk_signals']),
                    limitations=['Syntactic security signals require review; no interprocedural taint or exploit proof.', 'No upstream execution, build, extra resolution, VM or operational gate attestation.'])
    return findings, facts['coverage_gaps'], coverage


def triage(text, code, context):
    from pathlib import Path
    import importlib.util
    root=Path(__file__).resolve().parents[1]
    spec=importlib.util.spec_from_file_location('independent_context_review',root/'automation/context_review.py')
    review=importlib.util.module_from_spec(spec);spec.loader.exec_module(review)
    checks_spec=importlib.util.spec_from_file_location('triage_source_checks',root/'automation/upstream_checks.py')
    checks=importlib.util.module_from_spec(checks_spec);checks_spec.loader.exec_module(checks)
    data=decode(text)
    if data.get('error'):
        if data.get('category')=='review-unavailable':raise Incomplete(data['error'])
        raise Contradiction('Triage evidence invalid: '+data['error'])
    if code!=0 or data.get('schema_version')!=1:raise Incomplete('Triage process did not complete')
    raw_text=data['upstream_raw']
    if review.digest(raw_text.encode())!=data['upstream_sha256']:raise Contradiction('Triage source hash mismatch')
    # Re-run the existing verifier against preserved raw bytes, not a clean summary.
    upstream(raw_text,0,context)
    raw=decode(raw_text)
    sources=checks.verify_snapshot(raw['snapshot'],decode((root/'automation/upstream-inventory.lock.json').read_text()))
    integration={p:(root/p).read_bytes().replace(b'\r\n',b'\n') for p in review.INTEGRATION_FILES}
    ledger=decode((root/'automation/context-review.json').read_text())
    try:actual=review.review(raw,sources,integration,ledger)
    except ValueError as error:raise Contradiction(str(error)) from error
    if actual!=data['review']:raise Contradiction('Triage decisions/graph differ from independent recomputation')
    confirmed=[r for r in actual['entries'] if r['classification']=='confirmed-vulnerability']
    findings=[record('DA33-'+r['id'],r['path'],raw['snapshot']['revision'],r['severity'],[],
                     description=r['reason'],original_rule=r['rule'],line=r['line'],classification=r['classification'],
                     remediation='Do not expose runmark reuse; propose length-framed SHA-256 plus marker schema/cache invalidation in a reviewed upstream patch.') for r in confirmed]
    gaps=[dict(scope='contextual-review',reason=str(actual['counts'].get('inconclusive',0))+' findings remain inconclusive; approximate graph does not prove unreachability'),
          dict(scope='whole-upstream',reason='Original scanner gate and coverage gaps retained; no automatic PASS promotion')]
    coverage={k:actual[k] for k in ('integrity','counts','high_reviews','original_findings_retained','proof_tests_required','security_gate_promoted','coverage','limitations')}
    coverage.update(groups=len(actual['groups']),graph_nodes=len(actual['graph']['nodes']),graph_edges=len(actual['graph']['edges']),
                    graph_sha256=review.digest(json.dumps(actual['graph'],sort_keys=True).encode()),
                    source_scan_sha256=data['upstream_sha256'],integration_hashes=ledger['integration_hashes'],
                    note='Annotations never remove, suppress or reclassify the original scanner bundle.')
    return findings,gaps,coverage


def normalize(lane, text, code, context, unavailable=None):
    if unavailable:
        return dict(status='BLOCKED', reason=unavailable, complete=False, findings=[], finding_count=None, coverage={})
    try:
        findings, skipped, coverage = {'security-go': go, 'security-python': python, 'security-npm': npm,
                                       'security-workflows': workflows, 'security-provenance': provenance,
                                       'security-upstream': upstream, 'security-triage': triage}[lane](text, code, context)
        status = 'FAIL' if findings else ('UNVERIFIED' if skipped else 'PASS')
        result = dict(status=status, reason='Known findings require review' if findings else ('Explicit coverage gaps' if skipped else 'Complete audit within declared scope; no known findings'),
                    complete=True, findings=findings, finding_count=len(findings), advisory_count=len({f['advisory'] for f in findings}), skipped=skipped, coverage=coverage)
        if lane == 'security-upstream':
            result.update(advisory_count=0, static_rule_count=len({f['advisory'] for f in findings}))
            if findings:
                result['reason'] = 'Static findings/risk signals require review; not confirmed vulnerabilities'
        if lane=='security-triage':
            result.update(advisory_count=0,static_rule_count=len({f['original_rule'] for f in findings}))
            result['reason']='Contextual integrity defect confirmed; original findings and uncertainty retained' if findings else 'Contextual coverage remains unverified'
        return result
    except Incomplete as error:
        return dict(status='BLOCKED' if code else 'UNVERIFIED', reason=str(error), complete=False, findings=[], finding_count=None, coverage={})
    except (Contradiction, KeyError, TypeError, AttributeError) as error:
        return dict(status='FAIL', reason='Invalid/contradictory scanner evidence: ' + str(error), complete=False, findings=[], finding_count=None, coverage={})
