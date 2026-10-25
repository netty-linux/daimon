import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


integration = load('integration_recipes', 'automation/integrations.py')
evidence = load('integration_evidence', 'automation/evidence.py')


class IntegrationFacts(unittest.TestCase):
    def go_events(self):
        return [dict(Action='pass', Package='github.com/netty-linux/daimon/internal/harness2bridge', Test=name)
                for name in sorted(integration.REQUIRED_WORKER_TESTS)]

    def test_real_worker_required_go_tests_are_verified(self):
        facts = integration.go_results('\n'.join(map(json.dumps, self.go_events())))
        self.assertEqual(set(facts['worker_tests_confirmed']), integration.REQUIRED_WORKER_TESTS)
        self.assertEqual(facts['skipped'], [])

    def test_missing_worker_integration_fails_even_with_package_pass(self):
        rows = self.go_events()[1:] + [dict(Action='pass', Package='pkg')]
        with self.assertRaises(ValueError):
            integration.go_results('\n'.join(map(json.dumps, rows)))

    def test_skipped_real_worker_is_never_accepted(self):
        rows = self.go_events() + [dict(Action='skip', Package='internal/harness2bridge', Test='Optional')]
        with self.assertRaises(ValueError):
            integration.go_results('\n'.join(map(json.dumps, rows)))

    def test_platform_skip_names_and_reasons_are_retained(self):
        rows = self.go_events() + [dict(Action='output', Package='cmd', Test='Gate', Output='unsupported-platform gate'),
                                 dict(Action='skip', Package='cmd', Test='Gate')]
        facts = integration.go_results('\n'.join(map(json.dumps, rows)))
        self.assertEqual(facts['skipped'][0]['reason'], 'unsupported-platform gate')

    def test_package_without_tests_is_explicitly_not_run_not_a_skipped_case(self):
        rows = self.go_events() + [dict(Action='output', Package='fixture', Output='? fixture [no test files]\n'),
                                 dict(Action='skip', Package='fixture')]
        facts = integration.go_results('\n'.join(map(json.dumps, rows)))
        self.assertEqual(facts['skipped'], [])
        self.assertEqual(facts['not_run_packages'][0]['status'], 'NOT RUN')

    def test_unknown_package_skip_is_not_silently_exempted(self):
        rows = self.go_events() + [dict(Action='output', Package='fixture', Output='unexplained skip'),
                                 dict(Action='skip', Package='fixture')]
        facts = integration.go_results('\n'.join(map(json.dumps, rows)))
        self.assertEqual(len(facts['skipped']), 1)
        self.assertEqual(facts['not_run_packages'], [])

    def test_go_failure_is_not_hidden_by_other_passes(self):
        with self.assertRaises(ValueError):
            integration.go_results('\n'.join(map(json.dumps, self.go_events() + [dict(Action='fail')])) )

    def test_python_marker_binding_and_count(self):
        record = dict(suite='worker', tests=8, successful=True, failures=0, errors=0, skipped=[])
        text = 'DAIMON_UNITTEST_RESULT ' + json.dumps(record)
        self.assertEqual(integration.unittest_facts('worker', text)['tests'], 8)
        with self.assertRaises(ValueError):
            integration.unittest_facts('native-substrate', text)

    def test_python_skip_is_preserved_not_silenced(self):
        record = dict(suite='worker', tests=8, successful=False, failures=0, errors=0,
                      skipped=[dict(test='WorkerTests.one', reason='missing dependency')])
        self.assertEqual(integration.unittest_facts('worker', 'DAIMON_UNITTEST_RESULT ' + json.dumps(record))['skipped'], record['skipped'])

    def test_python_failure_or_zero_tests_is_rejected(self):
        for count, errors in ((0, 0), (1, 1)):
            with self.subTest(count=count), self.assertRaises(ValueError):
                integration.unittest_facts('worker', 'DAIMON_UNITTEST_RESULT ' + json.dumps(
                    dict(suite='worker', tests=count, successful=False, errors=errors, skipped=[])))

    def test_vitest_positive_assertion_count(self):
        data = dict(success=True, numTotalTests=10, numPassedTests=10, numFailedTests=0, numPendingTests=0)
        self.assertEqual(integration.vitest_results(data)['tests'], 10)

    def test_vitest_missing_or_failed_assertions_rejected(self):
        for data in ({}, dict(success=True, numTotalTests=2, numPassedTests=1),
                     dict(success=False, numTotalTests=2, numPassedTests=1, numFailedTests=1)):
            with self.subTest(data=data), self.assertRaises(ValueError):
                integration.vitest_results(data)

    def test_vitest_skipped_names_retained(self):
        data = dict(success=True, numTotalTests=2, numPassedTests=1, numPendingTests=1,
                    testResults=[dict(assertionResults=[dict(fullName='pending case', status='pending')])])
        self.assertEqual(integration.vitest_results(data)['skipped'][0]['test'], 'pending case')


class StructuredImport(unittest.TestCase):
    def record(self, lane='python-upstream-integration'):
        return dict(schema_version=1, lane=lane, status='PASS', reason='Executed', skipped=[], attachments={},
                    stages=[dict(name=name, status='PASS', tests=1, skipped=[], exit_code=0)
                            for name in sorted(evidence.INTEGRATIONS[lane])])

    def text(self, record):
        return 'DAIMON_INTEGRATION_RESULT ' + json.dumps(record)

    def test_complete_required_scopes_accepted(self):
        for lane in evidence.INTEGRATIONS:
            with self.subTest(lane=lane):
                self.assertEqual(evidence.classify(lane, 0, self.text(self.record(lane)))[0], 'PASS')

    def test_missing_or_duplicate_marker_rejected(self):
        text = self.text(self.record())
        for value in ('', text + '\n' + text):
            with self.subTest(value=value[:10]), self.assertRaises(ValueError):
                evidence.integration_result('python-upstream-integration', value)

    def test_missing_duplicate_or_wrong_scope_is_rejected(self):
        for mutation in ('missing', 'duplicate', 'scope', 'zero', 'nonzero'):
            r = self.record()
            if mutation == 'missing':
                r['stages'].pop()
            elif mutation == 'duplicate':
                r['stages'].append(r['stages'][0])
            elif mutation == 'scope':
                r['lane'] = 'browser-integration'
            elif mutation == 'zero':
                for s in r['stages']:
                    s['tests'] = 0
            else:
                r['stages'][0]['exit_code'] = 1
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                evidence.integration_result('python-upstream-integration', self.text(r))

    def test_process_failure_cannot_become_pass(self):
        self.assertEqual(evidence.classify('python-upstream-integration', 1, self.text(self.record()))[0], 'FAIL')

    def test_hidden_skip_cannot_become_pass(self):
        r = self.record()
        r['stages'][0]['skipped'] = [dict(test='hidden', reason='not available')]
        with self.assertRaises(ValueError):
            evidence.integration_result('python-upstream-integration', self.text(r))

    def test_explicit_skips_remain_unverified_not_pass(self):
        r = self.record()
        r['status'] = 'UNVERIFIED'
        s = r['stages'][0]
        s.update(status='UNVERIFIED', skipped=[dict(test='Skipped', reason='unavailable')])
        r['skipped'] = [dict(stage=s['name'], **s['skipped'][0])]
        self.assertEqual(evidence.classify(r['lane'], 1, self.text(r))[0], 'UNVERIFIED')


class WorkflowTopology(unittest.TestCase):
    def test_existing_ci_is_called_once_without_independent_duplicate_triggers(self):
        ci = (ROOT / '.github/workflows/ci.yml').read_text()
        outer = (ROOT / '.github/workflows/development-automation.yml').read_text()
        self.assertIn('workflow_call:', ci)
        self.assertNotIn('pull_request:', ci)
        self.assertNotIn('  push:', ci)
        self.assertEqual(outer.count('uses: $/.github/workflows/ci.yml'), 1)
        self.assertIn('policy, existing-ci, security]', outer)

    def test_no_privileged_browser_install_or_docker_recipe(self):
        ci = (ROOT / '.github/workflows/ci.yml').read_text()
        source = (ROOT / 'automation/integrations.py').read_text()
        self.assertNotIn('--with-deps', source)
        self.assertNotIn('sudo', ci)
        self.assertNotIn("['docker'", source)


if __name__ == '__main__':
    unittest.main()
