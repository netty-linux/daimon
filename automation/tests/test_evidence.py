import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('development_evidence', ROOT / 'automation/evidence.py')
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
spec = importlib.util.spec_from_file_location('development_policy', ROOT / 'automation/policy.py')
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)
SHA = 'a' * 40


class ClassificationTests(unittest.TestCase):
    def test_go_requires_actual_test_not_just_package_success(self):
        self.assertEqual(evidence.classify('windows-contracts', 0, '{"Action":"pass","Package":"pkg"}')[0], 'UNVERIFIED')

    def test_go_executed_test_pass(self):
        self.assertEqual(evidence.classify('windows-contracts', 0, '{"Action":"pass","Test":"TestOne"}')[0], 'PASS')

    def test_go_skip_never_passes(self):
        state, _, skipped = evidence.classify('windows-contracts', 0, '{"Action":"skip","Test":"Optional"}')
        self.assertEqual(state, 'UNVERIFIED')
        self.assertEqual(skipped, ['Optional'])

    def test_go_failure_overrides_exit_zero(self):
        self.assertEqual(evidence.classify('windows-contracts', 0, '{"Action":"fail"}')[0], 'FAIL')

    def test_nonzero_is_failure_even_with_success_text(self):
        self.assertEqual(evidence.classify('automation-linux', 1, 'Ran 3 tests\nOK\n')[0], 'FAIL')

    def test_unittest_empty_skip_or_partial_is_unverified(self):
        for text in ('', 'Ran 0 tests\nOK\n', 'Ran 2 tests\nOK (skipped=1)', 'Ran 2 tests'):
            with self.subTest(text=text):
                self.assertEqual(evidence.classify('automation-linux', 0, text)[0], 'UNVERIFIED')

    def test_unittest_complete_success(self):
        self.assertEqual(evidence.classify('automation-linux', 0, 'Ran 4 tests in 0.5s\n\nOK\n')[0], 'PASS')

    def test_demo_needs_both_contract_markers(self):
        for text in ('', 'Resposta final: DAIMON', 'loop_stopped'):
            with self.subTest(text=text):
                self.assertEqual(evidence.classify('linux-demo', 0, text)[0], 'UNVERIFIED')
        self.assertEqual(evidence.classify('linux-demo', 0, 'loop_stopped\nResposta final: DAIMON')[0], 'PASS')

    def test_no_privileged_or_c2_command_in_allowlist(self):
        text = json.dumps(evidence.COMMANDS).lower()
        for forbidden in ('powershell', 'hyper-v', 'harness2-c2', 'sudo', 'docker', 'codex exec'):
            self.assertNotIn(forbidden, text)


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def bundle(self, name='one', lane='automation-linux', **fields):
        row = dict(schema_version=1, lane=lane, commit=SHA, status='PASS', reason='Own scope',
                   scope='safe',
                   operational_proof=False, isolation_gates='UNVERIFIED', worktree_dirty=False,
                   run_id=os.environ.get('GITHUB_RUN_ID', 'local'), run_attempt=os.environ.get('GITHUB_RUN_ATTEMPT', 'local'))
        row.update(fields)
        path = self.root / name
        evidence.write_bundle(path, row, 'Own test log\n')
        return path

    def inventory(self):
        return {'lanes': [dict(id='automation-linux', implemented=True, scope='safe', status='NOT RUN', reason='Missing'),
                          dict(id='real-vm', implemented=False, scope='VM', status='BLOCKED', reason='Human approval required'),
                          dict(id='18-gates', implemented=False, scope='gates', status='UNVERIFIED', reason='No proof')]}

    def test_roundtrip_and_manifest(self):
        self.assertEqual(evidence.load_verified(self.bundle(), SHA)['status'], 'PASS')

    def test_changed_log_rejected(self):
        path = self.bundle()
        (path / 'raw.log').write_text('Changed', encoding='utf-8')
        with self.assertRaises(ValueError):
            evidence.load_verified(path, SHA)

    def test_other_revision_rejected(self):
        with self.assertRaises(ValueError):
            evidence.load_verified(self.bundle(), 'b' * 40)

    def test_imported_scope_must_match_inventory(self):
        with self.assertRaises(ValueError):
            evidence.load_verified(self.bundle(scope='foreign coverage'), SHA, expected_scopes={'automation-linux': 'safe'})

    def test_compressed_stage_attachment_hash_is_checked(self):
        source = self.root / 'owned.gz'
        source.write_bytes(b'fixed compressed fixture')
        original = self.bundle()
        report = json.loads((original / 'report.json').read_text())
        report['attachments'] = ['stage-00.log.gz']
        output = self.root / 'attached'
        evidence.write_bundle(output, report, 'Own test log', {'stage-00.log.gz': source})
        self.assertEqual(evidence.load_verified(output, SHA)['status'], 'PASS')
        (output / 'stage-00.log.gz').write_bytes(b'tampered')
        with self.assertRaises(ValueError):
            evidence.load_verified(output, SHA)

    def test_stale_run_or_attempt_is_rejected(self):
        path = self.bundle(run_id='123', run_attempt='1')
        for run, attempt in (('other', '1'), ('123', '2')):
            with self.subTest(run=run, attempt=attempt), self.assertRaises(ValueError):
                evidence.load_verified(path, SHA, run, attempt)

    def test_dirty_or_operational_claim_or_invalid_status_rejected(self):
        for index, fields in enumerate(({'worktree_dirty': True}, {'operational_proof': True},
                                         {'isolation_gates': 'PASS'}, {'status': 'SUCCESS'}, {'schema_version': 2})):
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                evidence.load_verified(self.bundle(str(index), **fields), SHA)

    def test_prior_evidence_never_overwritten(self):
        self.bundle()
        with self.assertRaises(FileExistsError):
            self.bundle()

    def test_manifest_path_traversal_rejected(self):
        path = self.bundle()
        (path / 'manifest.json').write_text(json.dumps({'schema_version': 1, 'files': {'../foreign': {}}}), encoding='utf-8')
        with self.assertRaises(ValueError):
            evidence.load_verified(path, SHA)

    def test_missing_log_rejected(self):
        path = self.bundle()
        (path / 'raw.log').unlink()
        with self.assertRaises(OSError):
            evidence.load_verified(path, SHA)

    def test_aggregate_keeps_privileged_rows_blocked_and_gates_unverified(self):
        self.bundle()
        output = self.root / 'matrix'
        with patch.object(evidence, 'revision', return_value=SHA):
            self.assertEqual(evidence.aggregate(self.root / 'one', output, self.inventory()), 0)
        report = json.loads((output / 'report.json').read_text())
        self.assertEqual(report['status'], 'BLOCKED')
        self.assertEqual([r['status'] for r in report['coverage']], ['PASS', 'BLOCKED', 'UNVERIFIED'])

    def test_missing_required_result_fails_reporting_job_and_is_not_run(self):
        output = self.root / 'matrix'
        with patch.object(evidence, 'revision', return_value=SHA):
            self.assertEqual(evidence.aggregate(self.root / 'absent', output, self.inventory()), 1)
        report = json.loads((output / 'report.json').read_text())
        self.assertEqual(report['coverage'][0]['status'], 'NOT RUN')

    def test_duplicate_lane_fails_not_last_writer_wins(self):
        self.bundle('one')
        self.bundle('two')
        with patch.object(evidence, 'revision', return_value=SHA):
            self.assertEqual(evidence.aggregate(self.root, self.root / 'matrix', self.inventory()), 1)

    def test_unknown_lane_cannot_supply_required_evidence(self):
        self.bundle(lane='foreign')
        with patch.object(evidence, 'revision', return_value=SHA):
            self.assertEqual(evidence.aggregate(self.root, self.root / 'matrix', self.inventory()), 1)

    def test_markdown_table_escapes_untrusted_reason(self):
        path = self.bundle(reason='a|b\nnext')
        self.assertIn('a\\|b next', (path / 'summary.md').read_text())


class CollectorTests(unittest.TestCase):
    def collect(self, os_name='Windows', runtime='go', child=None):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        output = Path(temp.name) / 'own'
        with patch.object(evidence, 'revision', return_value=SHA), \
             patch.object(evidence.subprocess, 'check_output', return_value=''), \
             patch.object(evidence.platform, 'system', return_value=os_name), \
             patch.object(evidence.shutil, 'which', return_value=runtime), \
             patch.object(evidence.subprocess, 'run', side_effect=child) as run:
            code = evidence.run_lane('windows-contracts', output)
        report = json.loads((output / 'report.json').read_text())
        return code, report, run

    def test_wrong_os_is_blocked_without_execution(self):
        code, report, run = self.collect(os_name='Linux')
        self.assertEqual(report['status'], 'BLOCKED')
        self.assertNotEqual(code, 0)
        run.assert_not_called()

    def test_missing_runtime_is_blocked_without_installation(self):
        _, report, run = self.collect(runtime=None)
        self.assertEqual(report['status'], 'BLOCKED')
        run.assert_not_called()

    def test_start_failure_is_reported(self):
        _, report, _ = self.collect(child=OSError('Own runtime unavailable'))
        self.assertEqual(report['status'], 'FAIL')

    def test_timeout_is_not_cleanup_proof(self):
        _, report, _ = self.collect(child=evidence.subprocess.TimeoutExpired('go', 240))
        self.assertEqual(report['status'], 'FAIL')
        self.assertIn('descendant cleanup NOT attested', report['reason'])

    def test_truncated_failed_command_stays_failed(self):
        def command(*args, **kwargs):
            kwargs['stdout'].write(b'x' * (evidence.MAX_LOG + 1))
            return evidence.subprocess.CompletedProcess(args[0], 1)
        code, report, _ = self.collect(child=command)
        self.assertEqual(report['status'], 'FAIL')
        self.assertNotEqual(code, 0)

    def test_truncated_success_is_unverified(self):
        def command(*args, **kwargs):
            kwargs['stdout'].write(b'x' * (evidence.MAX_LOG + 1))
            return evidence.subprocess.CompletedProcess(args[0], 0)
        code, report, _ = self.collect(child=command)
        self.assertEqual(report['status'], 'UNVERIFIED')
        self.assertNotEqual(code, 0)


class PolicyTests(unittest.TestCase):
    def test_current_workflows_meet_narrow_policy(self):
        self.assertEqual(policy.check(ROOT), [])

    def test_forbidden_privilege_and_unpinned_action(self):
        for forbidden in ('self-hosted', 'pull_request_target', 'contents: write', 'sudo go test',
                          'Start-VM', '${{ secrets.KEY }}', 'uses: actions/checkout@v7'):
            with self.subTest(forbidden=forbidden), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                workflows = root / '.github/workflows'
                workflows.mkdir(parents=True)
                (workflows / 'unsafe.yml').write_text('contents: read\npersist-credentials: false\n' + forbidden)
                self.assertTrue(policy.check(root))

    def test_inventory_uses_valid_states_and_no_automatic_operational_lane(self):
        inventory = json.loads((ROOT / 'automation/coverage.json').read_text())
        self.assertEqual(inventory['schema_version'], 1)
        self.assertEqual(len({r['id'] for r in inventory['lanes']}), len(inventory['lanes']))
        for row in inventory['lanes']:
            self.assertIn(row['status'], evidence.STATES)
            if row['id'].startswith('c2-'):
                self.assertFalse(row['implemented'])


if __name__ == '__main__':
    unittest.main()
