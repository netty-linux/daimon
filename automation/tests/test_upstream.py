import ast
import base64
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'automation' / file)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module
c = load('upstream_checks_tests', 'upstream_checks.py')
p = load('upstream_parser_tests', 'security_parsers.py')
e = load('upstream_evidence_tests', 'evidence.py')


def fixture():
    loaded = ['harness2/__init__.py', 'harness2/core/__init__.py', 'harness2/core/tree.py']
    worker = ('def load_upstream(site):\n    for name, rel, package in ' + repr(tuple(('m'+str(i), f, False) for i, f in enumerate(loaded))) + ':\n        pass\n').encode()
    native = b'def load(site):\n    rel = "harness2/substrate/base.py"\n'
    sources = {name:b'"""Inert source data, never executed."""\n' for name in loaded + ['harness2/substrate/base.py']}
    sources['pyproject.toml'] = b'[build-system]\nrequires=["setuptools>=77", "wheel"]\nbuild-backend="setuptools.build_meta"\n[project]\nname="harness2"\nversion="0.1.0"\ndependencies=[]\n[project.optional-dependencies]\nextra=["requests>=2"]\n'
    entries = {n:dict(mode='100644',oid=hashlib.sha1(b'blob '+str(len(b)).encode()+b'\0'+b).hexdigest(),bytes=len(b),sha256=c.digest(b)) for n,b in sources.items()}
    policy = dict(repository='https://github.com/google-research/harness2',revision='a'*40,tree='b'*40,entries=entries,worker_loaded=loaded,runtime_loaded=loaded+['harness2/substrate/base.py'],worker_sha256=c.digest(worker),native_adapter_sha256=c.digest(native))
    snapshot = dict(repository=policy['repository'],revision=policy['revision'],tree=policy['tree'],entries=copy.deepcopy(entries),sources={n:base64.b64encode(b).decode() for n,b in sources.items()})
    facts = c.analyze(sources,policy,worker,native)
    raw = dict(errors=[],results=[],metrics={n:dict(skipped_tests=0) for n in [*facts['modules'],'_totals']})
    raw['metrics']['_totals'].update({'SEVERITY.'+s:0 for s in ('LOW','MEDIUM','HIGH','UNDEFINED')})
    data = dict(schema_version=1,scanner=dict(name='bandit',version='1.9.4',ignore_nosec=True),snapshot=snapshot,analysis=facts,bandit=raw,bandit_exit=0)
    return data,policy,worker,native


class UpstreamIntegrity(unittest.TestCase):
    def test_valid_inventory_and_zero_findings_remain_inconclusive(self):
        data,policy,worker,native=fixture()
        findings,facts=c.evaluate(data,policy,worker,'1.9.4',native)
        self.assertEqual(findings,[])
        self.assertTrue(facts['coverage_gaps'])
        self.assertEqual(facts['dependencies']['runtime_declared'],[])
        self.assertEqual(len(facts['runtime_loaded']),4)

    def test_origin_commit_or_tree_change_rejected(self):
        for key in ('repository','revision','tree'):
            data,policy,_,_=fixture();data['snapshot'][key]='foreign'
            with self.assertRaises(c.IntegrityError):c.verify_snapshot(data['snapshot'],policy)

    def test_tampered_source_or_blob_identity_rejected(self):
        for field in ('sha256','oid','bytes'):
            data,policy,_,_=fixture();path='harness2/core/tree.py'
            policy['entries'][path][field]=0 if field=='bytes' else '0'*64
            data['snapshot']['entries']=copy.deepcopy(policy['entries'])
            with self.assertRaises(c.IntegrityError):c.verify_snapshot(data['snapshot'],policy)

    def test_missing_extra_and_uninventoried_source_rejected(self):
        for mode in ('missing','extra','tree-extra'):
            data,policy,_,_=fixture()
            if mode=='missing':data['snapshot']['sources'].pop('harness2/core/tree.py')
            elif mode=='extra':data['snapshot']['sources']['hidden.py']='eA=='
            else:data['snapshot']['entries']['hidden.py']=dict(mode='100644',oid='c'*40)
            with self.assertRaises(c.IntegrityError):c.verify_snapshot(data['snapshot'],policy)

    def test_symlink_submodule_and_traversal_rejected(self):
        for mode in ('120000','160000'):
            data,policy,_,_=fixture();policy['entries']['harness2/core/tree.py']['mode']=mode
            data['snapshot']['entries']=copy.deepcopy(policy['entries'])
            with self.assertRaises(c.IntegrityError):c.verify_snapshot(data['snapshot'],policy)
        for path in ('../escape.py','/absolute.py','C:/escape.py','back\\slash.py','a//b.py','--option.py','newline\n.py'):
            self.assertFalse(c.safe_path(path))

    def test_worker_and_native_adapter_boundary_change_rejected(self):
        data,policy,worker,native=fixture()
        for a,b in ((worker+b'\nx=1',native),(worker,native+b'\nx=1')):
            with self.assertRaises(c.IntegrityError):c.evaluate(data,policy,a,'1.9.4',b)

    def test_rehashed_analysis_does_not_override_source_reparse(self):
        data,policy,worker,native=fixture();data['analysis']['dependencies']['runtime_declared']=['foreign==1']
        with self.assertRaises(c.IntegrityError):c.evaluate(data,policy,worker,'1.9.4',native)

    def test_new_runtime_requirements_not_claimed_audited(self):
        data,policy,worker,native=fixture()
        sources=c.verify_snapshot(data['snapshot'],policy)
        sources['pyproject.toml']=sources['pyproject.toml'].replace(b'dependencies=[]',b'dependencies=["requests>=2"]')
        facts=c.analyze(sources,policy,worker,native)
        self.assertTrue(any(g['scope']=='runtime-dependencies' for g in facts['coverage_gaps']))

    def test_optional_dependencies_explicitly_not_installed(self):
        data,_,_,_=fixture()
        self.assertEqual(data['analysis']['dependencies']['optional_not_installed'],{'extra':['requests>=2']})
        self.assertTrue(any(g['scope']=='optional-and-vendored' for g in data['analysis']['coverage_gaps']))


class SecuritySignals(unittest.TestCase):
    def test_aliases_process_filesystem_deserialization_dynamic_and_network(self):
        source='import subprocess as sp\nfrom pathlib import Path as P\nimport pickle\nimport requests\np=P("x")\nsp.run(["cmd"])\np.write_text("x")\npickle.loads(b"x")\nexec("x")\nrequests.get("url")\n'
        rules={f['rule'] for f in c.risk_calls(ast.parse(source))}
        self.assertEqual(rules,{'DA32-PROCESS','DA32-FILESYSTEM','DA32-DESERIALIZE','DA32-DYNAMIC-CODE','DA32-NETWORK'})

    def test_json_is_recorded_as_boundary_not_code_execution(self):
        findings=c.risk_calls(ast.parse('import json\njson.loads("{}")'))
        self.assertEqual(findings[0]['rule'],'DA32-JSON-INPUT')
        self.assertFalse(findings[0]['confirmed_vulnerability'])

    def test_string_replace_is_not_filesystem_operation(self):
        self.assertEqual(c.risk_calls(ast.parse('text.replace("a","b")')),[])


class BanditEvidence(unittest.TestCase):
    def finding(self):
        return dict(filename='harness2/core/tree.py',line_number=1,test_id='B101',issue_severity='LOW',issue_confidence='HIGH',issue_text='assert used',col_offset=0)

    def test_real_findings_preserved(self):
        data,policy,worker,native=fixture();data['bandit']['results']=[self.finding()];data['bandit_exit']=1
        data['bandit']['metrics']['_totals']['SEVERITY.LOW']=1
        findings,_=c.evaluate(data,policy,worker,'1.9.4',native)
        self.assertEqual(findings[0]['rule'],'B101')

    def test_failed_scanner_errors_and_missing_metrics_rejected(self):
        for variant in ('errors','exit','metrics','skips'):
            data,policy,worker,native=fixture()
            if variant=='errors':data['bandit']['errors']=[{'reason':'parse failure'}]
            elif variant=='exit':data['bandit_exit']=2
            elif variant=='metrics':data['bandit']['metrics'].pop('harness2/core/tree.py')
            else:data['bandit']['metrics']['_totals']['skipped_tests']=1
            with self.assertRaises(ValueError):c.evaluate(data,policy,worker,'1.9.4',native)

    def test_duplicate_findings_or_exit_contradiction_rejected(self):
        for duplicate in (False,True):
            data,policy,worker,native=fixture();data['bandit']['results']=[self.finding()]*(2 if duplicate else 1);data['bandit_exit']=1 if duplicate else 0
            with self.assertRaises(c.IntegrityError):c.evaluate(data,policy,worker,'1.9.4',native)

    def test_version_and_nosec_policy_cannot_be_downgraded(self):
        for field,value in (('version','0'),('ignore_nosec',False)):
            data,policy,worker,native=fixture();data['scanner'][field]=value
            with self.assertRaises(c.IntegrityError):c.evaluate(data,policy,worker,'1.9.4',native)

    def test_static_findings_are_not_advisories_or_exploit_proof(self):
        finding=dict(advisory='B404',kind='scanner-finding',confirmed_vulnerability=False)
        with mock.patch.object(p,'upstream',return_value=([finding],[],{})):
            result=p.normalize('security-upstream','{}',0,{})
        self.assertEqual(result['status'],'FAIL')
        self.assertEqual(result['advisory_count'],0)
        self.assertEqual(result['static_rule_count'],1)

    def test_missing_scanner_is_blocked_and_integrity_error_is_fail(self):
        ctx={'versions':{'bandit':'1.9.4'}}
        missing=p.normalize('security-upstream','',0,ctx,'not installed')
        self.assertEqual(missing['status'],'BLOCKED');self.assertIsNone(missing['finding_count'])
        corrupt=p.normalize('security-upstream',json.dumps(dict(error='hash mismatch',category='integrity')),2,ctx)
        self.assertEqual(corrupt['status'],'FAIL')

    def test_zero_findings_with_gap_cannot_promote_gate(self):
        with mock.patch.object(p,'upstream',return_value=([],[dict(scope='coverage',reason='unresolved')],{})):
            self.assertEqual(p.normalize('security-upstream','{}',0,{})['status'],'UNVERIFIED')

    def test_cross_job_revision_disagreement_is_failure(self):
        reports={'security-upstream':{'security':{'complete':True,'coverage':{'revision':'a'*40}}},'python-upstream-integration':{'integration':{'provenance':{'upstream_revision':'b'*40}}}}
        self.assertIn('Independent upstream scan and real worker revisions differ',e.cross_job_provenance(reports))


if __name__=='__main__':unittest.main()
