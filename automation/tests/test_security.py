import copy
import base64
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / file)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


p = load('scanner_protocol_tests', 'automation/security_parsers.py')
e = load('security_bundle_tests', 'automation/evidence.py')
s = load('workflow_policy_tests', 'automation/security.py')
SHA = 'a' * 40


def context():
    return dict(versions=json.loads((ROOT / 'automation/security-tools.json').read_text()), commit=SHA,
                vcs_revision='b' * 40, python_targets=[dict(name='wheel', version='0.45.1')],
                npm_packages={'node_modules/example': {'version': '1.0.0'}}, input_hashes={},
                workflow_policy_findings=[])


def go_messages(finding=False):
    messages = [dict(config=dict(protocol_version='v1.0.0', scanner_name='govulncheck', scanner_version=context()['versions']['govulncheck'],
                                 db='https://vuln.go.dev', scan_mode='source', scan_level='symbol', go_version='go1.27.1')),
                dict(SBOM=dict(go_version='go1.27.1', modules=[dict(path='stdlib', version='v1.27.1')], roots=['main']))]
    if finding:
        messages += [dict(osv=dict(id='GO-TEST', aliases=['CVE-TEST'], affected=[])),
                     dict(finding=dict(osv='GO-TEST', fixed_version='v1.27.2', trace=[dict(module='stdlib', version='v1.27.1', package='crypto/example', function='Parse')]))]
    return messages


def go_text(rows):
    return '\n'.join(json.dumps(row) for row in rows)


def py_data(vulnerable=False):
    vulns = [dict(id='CVE-TEST', aliases=['GHSA-test'], fix_versions=['0.46.2'])] if vulnerable else []
    return dict(dependencies=[dict(name='wheel', version='0.45.1', vulns=vulns)], fixes=[])


def npm_data(vulnerable=False):
    values = {'info': 0, 'low': 0, 'moderate': 0, 'high': int(vulnerable), 'critical': 0, 'total': int(vulnerable)}
    vulns = {'example': dict(name='example', severity='high', nodes=['node_modules/example'],
                            fixAvailable=dict(name='example', version='1.0.1'),
                            via=[dict(source=1, url='https://github.com/advisories/GHSA-test', severity='high', range='<1.0.1', title='Fixture')])} if vulnerable else {}
    return dict(auditReportVersion=2, vulnerabilities=vulns, metadata=dict(vulnerabilities=values, dependencies=dict(total=1)))


def sarif(finding=False):
    results = [dict(ruleId='zizmor/test', level='warning', message=dict(text='Unsafe fixture'),
                    locations=[dict(physicalLocation=dict(artifactLocation=dict(uri='.github/workflows/test.yml'), region=dict(startLine=2)))])] if finding else []
    return dict(version='2.1.0', runs=[dict(tool=dict(driver=dict(name='zizmor', version=context()['versions']['zizmor'])), results=results)])


class GoScanner(unittest.TestCase):
    def test_clean_complete_stream(self):
        self.assertEqual(p.normalize('security-go', go_text(go_messages()), 0, context())['status'], 'PASS')

    def test_findings_fail_even_with_json_exit_zero(self):
        r = p.normalize('security-go', go_text(go_messages(True)), 0, context())
        self.assertEqual(r['status'], 'FAIL')
        self.assertEqual(r['findings'][0]['recommended_fix'], ['v1.27.2'])
        self.assertIsNone(r['findings'][0]['severity'])

    def test_consistent_repetitions_are_counted_without_inflating_advisories(self):
        rows = go_messages(True); rows += copy.deepcopy(rows[2:])
        r = p.normalize('security-go', go_text(rows), 0, context())
        self.assertEqual(r['advisory_count'], 1)
        self.assertEqual(r['finding_count'], 1)
        self.assertEqual(r['coverage']['duplicate_findings'], 1)

    def test_conflicting_duplicate_definition_rejected(self):
        rows = go_messages(True); changed = copy.deepcopy(rows[2]); changed['osv']['aliases'] = ['foreign']; rows.append(changed)
        r = p.normalize('security-go', go_text(rows), 0, context())
        self.assertFalse(r['complete'])
        self.assertEqual(r['status'], 'FAIL')

    def test_partial_empty_and_truncated_are_not_clean(self):
        for text in ('', go_text(go_messages()[:1]), go_text(go_messages())[:-2]):
            with self.subTest(text=text[:20]):
                self.assertNotEqual(p.normalize('security-go', text, 0, context())['status'], 'PASS')

    def test_duplicate_config_is_failure(self):
        rows = go_messages(); rows.append(rows[0])
        self.assertEqual(p.normalize('security-go', go_text(rows), 0, context())['status'], 'FAIL')

    def test_failed_scanner_does_not_clear_vulnerabilities(self):
        r = p.normalize('security-go', go_text(go_messages()), 2, context())
        self.assertEqual(r['status'], 'BLOCKED')
        self.assertIsNone(r['finding_count'])


class PythonScanner(unittest.TestCase):
    def test_no_pypi_findings_still_unverified_for_vcs(self):
        r = p.normalize('security-python', json.dumps(py_data()), 0, context())
        self.assertEqual(r['status'], 'UNVERIFIED')
        self.assertIn('Git-pinned', r['skipped'][0]['reason'])

    def test_pypi_advisory_is_actionable(self):
        r = p.normalize('security-python', json.dumps(py_data(True)), 1, context())
        self.assertEqual(r['status'], 'FAIL')
        self.assertEqual(r['findings'][0]['affected_version'], '0.45.1')

    def test_equivalent_source_duplicate_is_explicit(self):
        data = py_data(True); data['dependencies'][0]['vulns'] *= 2
        r = p.normalize('security-python', json.dumps(data), 1, context())
        self.assertEqual(r['finding_count'], 1)
        self.assertEqual(r['findings'][0]['source_occurrences'], 2)

    def test_conflicting_fix_or_dependency_duplicate_rejected(self):
        for variant in ('fix', 'dependency'):
            data = py_data(True)
            if variant == 'fix':
                extra = copy.deepcopy(data['dependencies'][0]['vulns'][0]); extra['fix_versions'] = ['9.0']; data['dependencies'][0]['vulns'].append(extra)
            else:
                data['dependencies'] *= 2
            r = p.normalize('security-python', json.dumps(data), 1, context())
            self.assertFalse(r['complete'])
            self.assertEqual(r['status'], 'FAIL')

    def test_unavailable_or_missing_target_never_passes(self):
        for data in ({}, dict(dependencies=[], fixes=[])):
            self.assertNotEqual(p.normalize('security-python', json.dumps(data), 0, context())['status'], 'PASS')

    def test_no_automatic_fixes_are_accepted(self):
        data = py_data(); data['fixes'] = [{'package': 'wheel'}]
        self.assertEqual(p.normalize('security-python', json.dumps(data), 0, context())['status'], 'FAIL')

    def test_exit_contradiction_rejected(self):
        self.assertEqual(p.normalize('security-python', json.dumps(py_data(True)), 0, context())['status'], 'FAIL')


class NpmScanner(unittest.TestCase):
    def test_clean_result_requires_complete_counts(self):
        self.assertEqual(p.normalize('security-npm', json.dumps(npm_data()), 0, context())['status'], 'PASS')

    def test_vulnerability_binds_version_and_remediation(self):
        r = p.normalize('security-npm', json.dumps(npm_data(True)), 1, context())
        self.assertEqual(r['status'], 'FAIL')
        self.assertEqual(r['findings'][0]['recommended_fix'], ['example@1.0.1'])
        self.assertEqual(r['findings'][0]['severity'], 'high')

    def test_count_exit_and_unknown_nodes_are_rejected(self):
        for variant in ('count', 'exit', 'node', 'bool'):
            data = npm_data(True); code = 1
            if variant == 'count': data['metadata']['vulnerabilities']['total'] = 0
            elif variant == 'exit': code = 0
            elif variant == 'node': data['vulnerabilities']['example']['nodes'] = ['foreign']
            else: data['metadata']['vulnerabilities']['total'] = True
            with self.subTest(variant=variant):
                self.assertNotEqual(p.normalize('security-npm', json.dumps(data), code, context())['status'], 'PASS')

    def test_network_error_not_no_findings(self):
        r = p.normalize('security-npm', '{"error":{"code":"ECONNRESET"}}', 1, context())
        self.assertEqual(r['status'], 'BLOCKED')
        self.assertIsNone(r['finding_count'])

    def test_duplicate_json_keys_are_failed_evidence(self):
        r = p.normalize('security-npm', '{"auditReportVersion":2,"auditReportVersion":2}', 0, context())
        self.assertEqual(r['status'], 'FAIL')


class WorkflowScanner(unittest.TestCase):
    def test_clean_offline_scope_and_limitations(self):
        r = p.normalize('security-workflows', json.dumps(sarif()), 0, context())
        self.assertEqual(r['status'], 'PASS')
        self.assertIn('NOT RUN', r['coverage']['limitations'][0])

    def test_sarif_findings_fail_even_with_zero_exit(self):
        self.assertEqual(p.normalize('security-workflows', json.dumps(sarif(True)), 0, context())['status'], 'FAIL')

    def test_duplicate_findings_and_wrong_scanner_version_rejected(self):
        for variant in ('duplicate', 'version'):
            data = sarif(True)
            if variant == 'duplicate': data['runs'][0]['results'] *= 2
            else: data['runs'][0]['tool']['driver']['version'] = 'foreign'
            self.assertEqual(p.normalize('security-workflows', json.dumps(data), 0, context())['status'], 'FAIL')

    def test_semantic_permissions_and_untrusted_execution(self):
        doc = {'on': {'pull_request': None}, 'permissions': {'contents': 'read'},
               'jobs': {'test': {'steps': [{'uses': 'actions/checkout@' + SHA, 'with': {'persist-credentials': False}}]}}}
        self.assertEqual(s.workflow_policy(doc, 'test.yml', SHA), [])
        for changes in ({'permissions': {'contents': 'write'}}, {'on': {'pull_request_target': None}}):
            bad = copy.deepcopy(doc); bad.update(changes)
            self.assertTrue(s.workflow_policy(bad, 'test.yml', SHA))
        bad = copy.deepcopy(doc); bad['jobs']['test']['steps'] = [{'run': 'echo ${{ github.event.issue.title }}'}]
        self.assertTrue(s.workflow_policy(bad, 'test.yml', SHA))


class SecurityBundle(unittest.TestCase):
    def bundle(self, folder):
        raw = json.dumps(npm_data())
        source = folder / 'scan-source'; source.write_text(raw, encoding='utf-8')
        err = folder / 'stderr-source'; err.write_text('')
        ctx = context()
        ctx['toolchain_verified'] = True
        normalized = p.normalize('security-npm', raw, 0, ctx)
        report = dict(schema_version=1, lane='security-npm', scope='safe', commit=SHA, status=normalized['status'],
                      reason=normalized['reason'], security=normalized, security_context=ctx,
                      scanner_capsule=dict(commit=SHA, run_id='123', run_attempt='1', pins=ctx['versions']),
                      unavailable=None, exit_code=0, operational_proof=False, isolation_gates='UNVERIFIED', worktree_dirty=False,
                      run_id='123', run_attempt='1', attachments=['scan.json', 'scanner.stderr.log'])
        out = folder / 'bundle'
        e.write_bundle(out, report, 'scanner log', {'scan.json': source, 'scanner.stderr.log': err})
        return out

    def test_valid_bundle_and_raw_hash_tampering(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = self.bundle(Path(tmp))
            self.assertEqual(e.load_verified(out, SHA, '123', '1', {'security-npm': 'safe'})['status'], 'PASS')
            (out / 'scan.json').write_text('{}')
            with self.assertRaises(ValueError): e.load_verified(out, SHA)

    def test_rehashed_false_clean_summary_still_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = self.bundle(Path(tmp))
            report = json.loads((out / 'report.json').read_text()); report['security']['finding_count'] = 99
            (out / 'report.json').write_text(json.dumps(report), encoding='utf-8')
            manifest = json.loads((out / 'manifest.json').read_text()); manifest['files']['report.json'] = {'sha256': e.digest(out / 'report.json'), 'bytes': (out / 'report.json').stat().st_size}
            (out / 'manifest.json').write_text(json.dumps(manifest))
            with self.assertRaises(ValueError): e.load_verified(out, SHA)

    def test_missing_scanner_is_blocked_not_zero_findings(self):
        r = p.normalize('security-go', '', 0, context(), unavailable='scanner binary not installed')
        self.assertEqual(r['status'], 'BLOCKED')
        self.assertIsNone(r['finding_count'])

    def test_complete_scan_without_capsule_is_rejected_even_after_rehash(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = self.bundle(Path(tmp))
            report = json.loads((out / 'report.json').read_text()); report['scanner_capsule'] = None
            (out / 'report.json').write_text(json.dumps(report), encoding='utf-8')
            manifest = json.loads((out / 'manifest.json').read_text()); manifest['files']['report.json'] = {'sha256': e.digest(out / 'report.json'), 'bytes': (out / 'report.json').stat().st_size}
            (out / 'manifest.json').write_text(json.dumps(manifest))
            with self.assertRaises(ValueError): e.load_verified(out, SHA)

    def test_npm_integrity_requires_full_sha512_digest(self):
        self.assertTrue(s.strong_npm_integrity('sha512-' + base64.b64encode(b'x' * 64).decode()))
        for value in (None, '', 'sha512-', 'sha1-short', 'sha512-bad%%%'):
            self.assertFalse(s.strong_npm_integrity(value))

    def test_cross_job_runtime_or_lock_disagreement_is_rejected(self):
        reports = {'security-provenance': {'security': {'complete': True},
                   'security_context': {'input_hashes': {'ui/package-lock.json': 'expected'}},
                   'scanner_capsule': {'node_version': 'v24', 'go_version': 'go1.27.1'}},
                   'ui-existing-ci': {'status': 'PASS', 'integration': {'provenance': {'node_version': 'v25', 'node_lock_sha256': 'different'}}},
                   'linux-existing-ci': {'status': 'PASS', 'integration': {'provenance': {'go_version': 'foreign'}}}}
        self.assertEqual(len(e.cross_job_provenance(reports)), 3)

    def test_cross_job_matching_provenance_accepted(self):
        reports = {'security-provenance': {'security': {'complete': True},
                   'security_context': {'input_hashes': {'ui/package-lock.json': 'expected'}},
                   'scanner_capsule': {'node_version': 'v24', 'go_version': 'go1.27.1'}},
                   'ui-existing-ci': {'status': 'PASS', 'integration': {'provenance': {'node_version': 'v24', 'node_lock_sha256': 'expected'}}}}
        self.assertEqual(e.cross_job_provenance(reports), [])


if __name__ == '__main__':
    unittest.main()
