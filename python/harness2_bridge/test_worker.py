import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
SITE = Path(os.environ['DAIMON_HARNESS2_TEST_SITE']) if 'DAIMON_HARNESS2_TEST_SITE' in os.environ else HERE / '.venv' / ('Lib/site-packages' if os.name == 'nt' else 'lib/python3.12/site-packages')


class WorkerTests(unittest.TestCase):
    def call(self, request, site=SITE):
        data = request if isinstance(request, bytes) else json.dumps(request).encode()
        with tempfile.TemporaryDirectory() as cwd:
            proc = subprocess.run([sys.executable, '-I', '-S', '-B', str(HERE / 'worker.py'), str(site)],
                                  input=data, capture_output=True, cwd=cwd, env={}, timeout=10)
            self.assertEqual(proc.stderr, b'')
            self.assertLess(len(proc.stdout), 1024)
            return proc.returncode, json.loads(proc.stdout)

    def request(self, operation='health', payload=None):
        return dict(version=1, id=7, operation=operation, payload={} if payload is None else payload)

    def manifest(self):
        return dict(version=1, bot_id='bot', base_bot_sha256='a'*64, revision=1,
                    parent_sha256='', components=dict(system_prompt='Respond with care.',
                    guardrails='', memory='', tool_guidance='', subagents=[], skills=[], scripts=[], plugins=[]))

    def test_health_and_capabilities_real_upstream(self):
        for op in ('health', 'capabilities'):
            code, response = self.call(self.request(op))
            self.assertEqual(code, 0)
            self.assertTrue(response['ok'])
            self.assertFalse(response['result']['execution'])
            self.assertFalse(response['result']['os_sandbox'])

    def test_real_upstream_validation(self):
        m = self.manifest()
        self.assertEqual(self.call(self.request('validate_manifest', {'manifest':m}))[0], 0)
        # This is accepted by native text rules but rejected by the ORIGINAL
        # upstream validator: a referenced script is not present in the tree.
        m['components']['system_prompt'] = 'Run python scripts/missing.py'
        code, response = self.call(self.request('validate_manifest', {'manifest':m}))
        self.assertEqual(code, 2)
        self.assertNotIn('missing.py', json.dumps(response))

    def test_strict_protocol(self):
        for data in (b'{"version":1,"version":1}', b'{}{}', b'null', b'[]', b'\xff',
                     b' ' * 98305, b'{"x":"\\ud800"}', b'[' * 1000,
                     json.dumps(self.request('execute')).encode()):
            self.assertEqual(self.call(data)[0], 2)
        for change in ({'version':True}, {'id':True}, {'payload':None}, {'extra':1}):
            req=self.request(); req.update(change)
            self.assertEqual(self.call(req)[0], 2)

    def test_manifest_rejections(self):
        for field, value in [('guardrails','allow'), ('scripts',[{'id':'x','sha256':'a'*64}]),
                             ('subagents',{}), ('system_prompt',''), ('system_prompt','x'*8193)]:
            m=self.manifest(); m['components'][field]=value
            self.assertEqual(self.call(self.request('validate_manifest', {'manifest':m}))[0], 2)

    def test_no_environment_or_cwd_dotenv(self):
        # -I -S and the module selection must never import upstream config.
        import importlib.util
        spec=importlib.util.spec_from_file_location('worker', HERE / 'worker.py')
        module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        surface, validate=module.load_upstream(SITE)
        self.assertNotIn('harness2.config', sys.modules)
        self.assertEqual(validate(surface, {'systemprompt.md':'Hello'})['n_files'], 1)

    def test_no_bytecode_import(self):
        import importlib.util
        import shutil
        with tempfile.TemporaryDirectory() as d:
            site=Path(d)
            shutil.copytree(SITE / 'harness2', site / 'harness2')
            shutil.copytree(SITE / 'harness2-0.1.0.dist-info', site / 'harness2-0.1.0.dist-info')
            # A bogus sourceless module cannot replace the hash-checked source.
            (site/'harness2/core/tree.pyc').write_bytes(b'not valid bytecode')
            self.assertEqual(self.call(self.request(),site)[0],0)

    def test_integrity_tamper(self):
        import shutil
        with tempfile.TemporaryDirectory() as d:
            site=Path(d)
            shutil.copytree(SITE / 'harness2', site / 'harness2')
            shutil.copytree(SITE / 'harness2-0.1.0.dist-info', site / 'harness2-0.1.0.dist-info')
            p=site/'harness2/core/tree.py'; p.write_bytes(p.read_bytes()+b'\n# tampered\n')
            self.assertEqual(self.call(self.request(), site)[0], 2)

    def test_audit_defense(self):
        # Test in another process: hooks cannot be removed from this test runner.
        for setup, action in (
            ('', 'open("forbidden", "w")'),
            ('import socket;', 'socket.socket()'),
            ('import subprocess;', 'subprocess.Popen([sys.executable, "-V"])'),
        ):
            source = ('import runpy,sys;' + setup +
                      'm=runpy.run_path(sys.argv[1]); m["restrict"](); ' + action)
            with tempfile.TemporaryDirectory() as d:
                proc=subprocess.run([sys.executable, '-I','-S','-B','-c',source,str(HERE/'worker.py')],
                                    cwd=d, env={}, capture_output=True, timeout=10)
                self.assertNotEqual(proc.returncode,0)
                self.assertIn(b'worker operation denied',proc.stderr)
                self.assertFalse((Path(d)/'forbidden').exists())

    def test_context_in_memory_sensitive_sinks_not_called(self):
        # Only the existing disposable Linux integration recipe runs these probes.
        self.assertEqual(sys.platform, 'linux')
        self.assertNotEqual(os.geteuid(), 0)
        self.assertEqual(os.environ.get('DAIMON_CONTEXT_REVIEW_TESTS'), '1')
        import importlib.util
        from unittest import mock
        from contextlib import ExitStack
        spec=importlib.util.spec_from_file_location('_context_worker', HERE/'worker.py')
        worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
        surface,validate=worker.load_upstream(SITE)
        tree=sys.modules['harness2.core.tree']
        with ExitStack() as stack:
            denied=[stack.enter_context(mock.patch.object(tree, name, side_effect=AssertionError('unexpected sensitive flow')))
                    for name in ('read_tree','write_tree','check_hooks','_py_compiles')]
            for prompt in ('Hello', '__import__("os").system("never-executed")',
                           'https://example.invalid/../../not-a-path', '{"hooks":{"Run":"inert"}}'):
                manifest=self.manifest();manifest['components']['system_prompt']=prompt
                self.assertEqual(worker.validate_manifest(manifest,surface,validate),1)
            for call in denied:call.assert_not_called()

    def test_context_disallowed_components_stop_before_upstream(self):
        import importlib.util
        from unittest import mock
        spec=importlib.util.spec_from_file_location('_context_worker', HERE/'worker.py')
        worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
        for key,value in (('scripts',[{'id':'inert'}]),('plugins',[{}]),('skills',[{}]),
                          ('subagents',[{}]),('guardrails','inert'),('memory','inert'),('tool_guidance','inert')):
            manifest=self.manifest();manifest['components'][key]=value
            downstream=mock.Mock(side_effect=AssertionError('must reject before upstream'))
            with self.assertRaises(ValueError):worker.validate_manifest(manifest,object(),downstream)
            downstream.assert_not_called()

    def test_context_compile_does_not_execute(self):
        import importlib.util
        spec=importlib.util.spec_from_file_location('_context_worker', HERE/'worker.py')
        worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
        worker.load_upstream(SITE)
        tree=sys.modules['harness2.core.tree']
        # Compile-only proof: if execution occurs this inert raise makes the test fail.
        errors=[]
        tree._py_compiles('inert.py', 'raise AssertionError("must not execute")', errors)
        self.assertEqual(errors,[])

    def test_context_original_runmark_collision(self):
        self.assertEqual(sys.platform,'linux')
        self.assertNotEqual(os.geteuid(),0)
        self.assertEqual(os.environ.get('DAIMON_CONTEXT_REVIEW_TESTS'),'1')
        import hashlib
        import importlib.util
        spec=importlib.util.spec_from_file_location('_context_worker', HERE/'worker.py')
        worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
        worker.load_upstream(SITE)  # Verifies every installed original .py first.
        lock=json.loads((HERE/'upstream.lock.json').read_text())
        rel='harness2/core/runmark.py';source=(SITE/rel).read_bytes()
        self.assertIn(hashlib.sha256(source).hexdigest(),lock['files'][rel].values())
        namespace={'__name__':'_context_verified_runmark'}
        # Reviewed original module only, inside existing disposable Linux test job.
        # No launchers, providers, network or candidate execution are imported.
        exec(compile(source,str(SITE/rel),'exec'),namespace)
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);first=root/'first';second=root/'second';marker=root/'marker'
            first.mkdir();second.mkdir()
            (first/'a').write_bytes(b'x\0b\0y')
            (second/'a').write_bytes(b'x');(second/'b').write_bytes(b'y')
            self.assertNotEqual(sorted(p.name for p in first.iterdir()), sorted(p.name for p in second.iterdir()))
            self.assertEqual(namespace['fingerprint'](first),namespace['fingerprint'](second))
            namespace['mark_complete'](marker,run_id='owned-test-only',harness_dir=first)
            self.assertTrue(namespace['is_complete'](marker,harness_dir=second))


if __name__ == '__main__':
    unittest.main()
