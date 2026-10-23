import dataclasses
import importlib.util
import inspect
import json
import os
import shutil
from pathlib import Path
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("daimon_native_substrate", HERE / "substrate.py")
native = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = native
spec.loader.exec_module(native)


class NativeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # Required, no fallback or silent skip when integration is requested.
        cls.adapter = native.load(os.environ["DAIMON_HARNESS2_TEST_SITE"])
        cls.base = sys.modules["harness2.substrate.base"]

    def test_original_contract_and_signatures(self):
        self.assertIsInstance(self.adapter, self.base.Substrate)
        from harness2.core.tree import SurfaceSpec
        self.assertIsInstance(self.adapter.surface(), SurfaceSpec)
        self.assertTrue(dataclasses.is_dataclass(self.base.Install))
        self.assertTrue(self.base.Install.__dataclass_params__.frozen)
        for name in ("surface", "materialise", "execute", "read_trajectory", "preflight"):
            original = inspect.signature(getattr(self.base.Substrate, name))
            actual = inspect.signature(getattr(native.NativeSubstrate, name))
            shape = lambda s: [(p.name, p.kind, p.default) for p in s.parameters.values()]
            self.assertEqual(shape(original), shape(actual))
        for module in ("harness2.config", "harness2.core.spec", "harness2.substrate.codex", "harness2.substrate.opencode"):
            self.assertNotIn(module, sys.modules)

    def test_shared_native_cases_and_original_validation(self):
        cases = json.loads((HERE.parents[1] / "internal/harness2substrate/testdata/surface_cases.json").read_text(encoding="utf-8"))
        for case in cases:
            tree = {"systemprompt.md": case["prompt"]}
            if case["upstream_valid"]:
                preview = self.adapter.preview(tree)
                self.assertEqual(preview.tree(), tree)
                tree["systemprompt.md"] = "changed"
                self.assertEqual(preview.tree()["systemprompt.md"], case["prompt"])
                with self.assertRaises(dataclasses.FrozenInstanceError):
                    preview._prompt = "changed"
            else:
                with self.assertRaisesRegex(ValueError, "^invalid C1 surface$"):
                    self.adapter.preview(tree)

    def test_reject_surfaces_types_and_limits(self):
        for tree in ({}, {"guardrails.md": "x"}, {"systemprompt.md": "ok", "scripts/x.py": "raise Exception()"},
                     {"systemprompt.md": ""}, {"systemprompt.md": "x" * 8193},
                     {"systemprompt.md": "é" * 4097}, {"systemprompt.md": "\ud800"},
                     {"systemprompt.md": None}, [], None):
            with self.assertRaises(ValueError):
                self.adapter.preview(tree)

    def test_all_execution_methods_refuse_without_touching_paths(self):
        class ForbiddenPath:
            def __fspath__(self):
                raise AssertionError("path was dereferenced")
        path = ForbiddenPath()
        calls = (lambda: self.adapter.materialise(path, object(), {}),
                 lambda: self.adapter.execute("task", object(), ["run"], 1),
                 lambda: self.adapter.read_trajectory(path, obs_trunc=1, max_chars=1),
                 self.adapter.preflight)
        for call in calls:
            with self.assertRaises(native.ExecutionUnavailable):
                call()

    def test_preview_has_no_io_and_surface_is_immutable(self):
        # Observe the REAL upstream validator, without replacing it. This is a
        # regression for the fixed preview path, not an OS sandbox attestation.
        events = []
        observing = False

        def audit(event, args):
            if observing and (event == "open" or event.startswith(
                    ("socket.", "subprocess.", "os.exec", "os.spawn"))):
                events.append(event)  # Never retain private arguments.

        sys.addaudithook(audit)
        try:
            observing = True
            preview = self.adapter.preview({"systemprompt.md": "Treat credentials and paths as inert text."})
            self.assertEqual(set(preview.tree()), {"systemprompt.md"})
            for method in (self.adapter.preflight,
                           lambda: self.adapter.execute("task", None, [], 0)):
                with self.assertRaises(native.ExecutionUnavailable):
                    method()
        finally:
            observing = False
        self.assertEqual(events, [])
        surface = self.adapter.surface()
        with self.assertRaises(dataclasses.FrozenInstanceError):
            surface.always_on = ("scripts/unsafe.py",)
        self.assertEqual(surface.always_on, ("systemprompt.md",))

    def test_modified_original_contract_is_rejected(self):
        site = Path(os.environ["DAIMON_HARNESS2_TEST_SITE"])
        with tempfile.TemporaryDirectory() as directory:
            isolated = Path(directory)
            shutil.copytree(site / "harness2", isolated / "harness2")
            shutil.copytree(site / "harness2-0.1.0.dist-info", isolated / "harness2-0.1.0.dist-info")
            contract = isolated / "harness2/substrate/base.py"
            contract.write_bytes(contract.read_bytes() + b"\n# modified contract\n")
            with self.assertRaises(ValueError):
                native.load(isolated)


if __name__ == "__main__":
    unittest.main()
