import ast
import copy
import importlib.util
import json
from pathlib import Path
import unittest

ROOT=Path(__file__).resolve().parents[2]
def module(name,file):
    spec=importlib.util.spec_from_file_location(name,ROOT/'automation'/file)
    result=importlib.util.module_from_spec(spec);spec.loader.exec_module(result);return result
r=module('review_tests','context_review.py')
p=module('review_parser_tests','security_parsers.py')
e=module('review_evidence_tests','evidence.py')
i=module('review_integration_tests','integrations.py')


def fixture():
    sources={'harness2/__init__.py':b'', 'harness2/core/__init__.py':b'',
      'harness2/substrate/base.py':b'class Substrate: pass\n',
      'harness2/core/tree.py':b'def validate(*a,**kw): return helper()\ndef helper(): return 1\ndef write_tree(): return open("inert")\n',
      'harness2/core/selection.py':b'def assign_ids(labels,task_id,k,mode):\n seed=int(hashlib.sha1(task_id.encode()).hexdigest()[:12],16)\n ordered=sorted(labels)\n random.Random(seed).shuffle(ordered)\n return {_anon_id(j):v for j,v in enumerate(ordered)}\n',
      'harness2/core/runmark.py':b'def fingerprint(root):\n h=hashlib.sha1()\n for p in root:\n  h.update(str(p).encode())\n  h.update(b"\\0")\n  h.update(p.read_bytes())\n  h.update(b"\\0")\n return h.hexdigest()[:16]\n',
      'harness2/benchmarks/workbuddy.py':b'def run_batch(label):\n with _label_lock(label):\n  digest=hashlib.sha1(label.encode()).hexdigest()[:8]\n  yaml_path.write_text(digest)\n  yaml_path.unlink()\n'}
    raw=dict(snapshot=dict(revision='a'*40),bandit=dict(results=[]),analysis=dict(risk_signals=[]))
    for path,line in [('harness2/core/selection.py',2),('harness2/core/runmark.py',2),('harness2/benchmarks/workbuddy.py',3)]:
        raw['bandit']['results'].append(dict(filename=path,line_number=line,col_offset=0,test_id='B324',issue_severity='HIGH',issue_text='fixture'))
    integration={path:(ROOT/path).read_bytes().replace(b'\r\n',b'\n') for path in r.INTEGRATION_FILES}
    claims={'harness2/core/selection.py':('demonstrated-false-positive','public-deterministic-shuffle'),
            'harness2/core/runmark.py':('confirmed-vulnerability','unframed-run-reuse'),
            'harness2/benchmarks/workbuddy.py':('inconclusive','truncated-shared-job-name')}
    decisions={}
    for row in r.records(raw,sources):
        classification,claim=claims[row['path']]
        decisions[row['id']]=dict(classification=classification,claim=claim,reason='specific source proof',evidence=['owned deterministic test'])
    ledger=dict(upstream_revision='a'*40,baseline_count=3,integration_hashes={p:r.digest(b) for p,b in integration.items()},decisions=decisions)
    return raw,sources,integration,ledger


class ReviewTests(unittest.TestCase):
    def test_all_original_findings_retained(self):
        args=fixture();result=r.review(*args)
        self.assertEqual(result['original_findings_retained'],3)
        self.assertEqual(result['integrity'],'PASS')
        self.assertFalse(result['security_gate_promoted'])
        self.assertEqual(result['counts']['inconclusive'],1)

    def test_unframed_collision_requires_no_hash_attack(self):
        first=[('a',b'x\0b\0y')];second=[('a',b'x'),('b',b'y')]
        self.assertNotEqual(first,second)
        self.assertEqual(r.legacy_stream(first),r.legacy_stream(second))
        self.assertNotEqual(r.framed_reference(first),r.framed_reference(second))

    def test_high_shuffle_is_public_determinism(self):
        raw,sources,integration,ledger=fixture()
        result=r.review(raw,sources,integration,ledger)
        row=next(x for x in result['high_reviews'] if x['function']=='assign_ids')
        self.assertEqual(row['classification'],'demonstrated-false-positive')
        sources['harness2/core/selection.py']=sources['harness2/core/selection.py'].replace(b'random.Random(seed)',b'unknown(seed)')
        with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_missing_duplicate_or_new_findings_rejected(self):
        for change in ('missing','new','duplicate'):
            raw,sources,integration,ledger=fixture()
            if change=='missing':raw['bandit']['results'].pop()
            elif change=='duplicate':raw['bandit']['results'].append(copy.deepcopy(raw['bandit']['results'][0]))
            else:raw['bandit']['results'][0]['line_number']=1
            with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_source_hash_changes_invalidate_review(self):
        raw,sources,integration,ledger=fixture()
        sources['harness2/core/runmark.py']+=b'\n# changed\n'
        with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_integration_change_invalidates_contextual_claims(self):
        raw,sources,integration,ledger=fixture()
        integration['python/harness2_bridge/worker.py']+=b'\n# drift\n'
        with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_revision_mismatch_rejected(self):
        raw,sources,integration,ledger=fixture();raw['snapshot']['revision']='b'*40
        with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_unknown_disposition_and_unsupported_claim_rejected(self):
        for key,value in (('classification','accepted'),('claim','generic-allowlist')):
            raw,sources,integration,ledger=fixture();next(iter(ledger['decisions'].values()))[key]=value
            with self.assertRaises(ValueError):r.review(raw,sources,integration,ledger)

    def test_false_positive_cannot_waive_other_process_functions(self):
        raw,sources,integration,ledger=fixture()
        record=next(x for x in r.records(raw,sources) if x['function']=='run_batch')
        decision=dict(classification='demonstrated-false-positive',claim='public-deterministic-shuffle',reason='wrong scope',evidence=['not enough'])
        with self.assertRaises(ValueError):r.verify_claim(record,decision,sources)

    def test_noncredential_claims_are_exact_not_password_allowlist(self):
        sources={'harness2/benchmarks/jobbench.py':b'def f():\n token = "production-secret"\n'}
        row=dict(path='harness2/benchmarks/jobbench.py',function='f',line=2,rule='B105')
        decision=dict(classification='demonstrated-false-positive',claim='noncredential-scalar',reason='not valid',evidence=['bad'])
        with self.assertRaises(ValueError):r.verify_claim(row,decision,sources)

    def test_graph_includes_guarded_and_unresolved_edges(self):
        raw,sources,integration,ledger=fixture();graph=r.approximate_graph(sources,integration)
        self.assertIn('harness2/core/tree.py:helper',graph['paths'])
        self.assertNotIn('harness2/core/tree.py:write_tree',graph['paths'])
        self.assertTrue(any(x['resolution']=='unresolved-dispatch-or-external' for x in graph['edges']))
        self.assertTrue(any('not proof of unreachability' in x for x in graph['limitations']))

    def test_grouping_preserves_every_individual_id(self):
        result=r.review(*fixture())
        ids=[ident for group in result['groups'].values() for ident in group['ids']]
        self.assertEqual(sorted(ids),sorted(x['id'] for x in result['entries']))

    def test_production_registry_keeps_540_and_three_high_claims(self):
        ledger=json.loads((ROOT/'automation/context-review.json').read_text())
        self.assertEqual(ledger['baseline_count'],540)
        self.assertEqual(len(ledger['decisions']),540)
        claims=[v['claim'] for v in ledger['decisions'].values()]
        for claim in ('public-deterministic-shuffle','unframed-run-reuse','truncated-shared-job-name'):
            self.assertIn(claim,claims)
        self.assertEqual(sum(v['classification']=='confirmed-vulnerability' for v in ledger['decisions'].values()),1)

    def test_absent_original_evidence_is_blocked_not_clean(self):
        result=p.normalize('security-triage',json.dumps(dict(error='missing upstream',category='review-unavailable')),2,{})
        self.assertEqual(result['status'],'BLOCKED');self.assertIsNone(result['finding_count'])

    def test_adulterated_review_evidence_is_fail(self):
        result=p.normalize('security-triage',json.dumps(dict(error='source hash mismatch',category='review-integrity')),2,{})
        self.assertEqual(result['status'],'FAIL')

    def test_cross_job_proof_tests_cannot_be_silently_omitted(self):
        reports={'security-triage':{'security':{'complete':True,'coverage':{'source_scan_sha256':'a'*64}}},
                 'security-upstream':{'security':{'complete':True,'coverage':{'revision':'a'*40,'dependencies':{'build_declared':[]}}},'original_output_bytes':{'stdout':1},'_verified_scan_sha256':'a'*64},
                 'security-python':{'security':{'complete':True,'coverage':{'targets':[{'name':n} for n in ('setuptools','wheel','packaging')]}}},
                 'python-upstream-integration':{'status':'PASS','integration':{'provenance':{'upstream_revision':'a'*40},'stages':[dict(name='worker',status='PASS',executed_test_ids=[])]}}}
        self.assertTrue(any('regression identities' in error for error in e.cross_job_provenance(reports)))

    def test_python_duplicate_test_ids_rejected(self):
        record=dict(suite='worker',tests=2,successful=True,failures=0,errors=0,skipped=[],executed_test_ids=['same','same'])
        with self.assertRaises(ValueError):i.unittest_facts('worker','DAIMON_UNITTEST_RESULT '+json.dumps(record))


if __name__=='__main__':unittest.main()
