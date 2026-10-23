"""Optional C1 in-memory surface adapter. Never an executable installation."""
from dataclasses import dataclass, field, replace
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import types

MAX_PROMPT_BYTES = 8192


class ExecutionUnavailable(RuntimeError):
    def __init__(self):
        super().__init__("C1 real execution unavailable: isolation gates unverified")


@dataclass(frozen=True)
class Preview:
    # No Install/staging_dir: the upstream Install represents a REAL installation.
    _prompt: str = field(repr=False)

    def tree(self):
        return {"systemprompt.md": self._prompt}


class NativeSubstrate:
    name = "daimon-native-c1"

    def __init__(self, surface, validate):
        self._surface = replace(surface, name=self.name)
        self._validate = validate

    def surface(self):
        return self._surface  # Original frozen SurfaceSpec.

    def preview(self, tree):
        if type(tree) is not dict or set(tree) != {"systemprompt.md"}:
            raise ValueError("unsupported C1 surface")
        prompt = tree["systemprompt.md"]
        if type(prompt) is not str:
            raise ValueError("invalid C1 surface")
        try:
            if not prompt.strip() or len(prompt.encode("utf-8")) > MAX_PROMPT_BYTES:
                raise ValueError()
            self._validate(self.surface(), {"systemprompt.md": prompt}, allow_subagents=False)
        except Exception:
            raise ValueError("invalid C1 surface") from None
        return Preview(prompt)

    def materialise(self, tree_dir, spec, ctx):
        raise ExecutionUnavailable()

    def execute(self, task_ref, install, run_ids, timeout):
        raise ExecutionUnavailable()

    def read_trajectory(self, stream_path, *, obs_trunc, max_chars, args_trunc=400):
        raise ExecutionUnavailable()

    def preflight(self):
        raise ExecutionUnavailable()


def load(site):
    """Trusted deployment paths only; reuse Phase B verification without new RPC.

    core.spec imports config and substrate/__init__ imports unsafe launchers.
    Neither is executed. An inert package namespace lets the VERIFIED original
    substrate/base.py import the original core.tree already loaded by Phase B.
    The namespace replaces no contract or algorithm.
    """
    site = Path(site)
    if not site.is_absolute() or not sys.flags.isolated or not sys.flags.no_site or sys.version_info[:2] != (3, 12):
        raise ValueError("invalid C1 configuration")
    bridge = Path(__file__).resolve().parents[1] / "harness2_bridge"
    spec = importlib.util.spec_from_file_location("_daimon_c1_verified_bridge", bridge / "worker.py")
    worker = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(worker)
    surface, validate = worker.load_upstream(site)
    lock = json.loads((bridge / "upstream.lock.json").read_text(encoding="utf-8"))
    rel = "harness2/substrate/base.py"
    path = site / rel
    source = path.read_bytes()
    if path.is_symlink() or len(source) > 1024 * 1024 or hashlib.sha256(source).hexdigest() not in lock["files"][rel].values():
        raise ValueError("C1 upstream integrity failure")
    namespace = types.ModuleType("harness2.substrate")
    namespace.__path__ = []  # No discovery/import of codex, opencode or config.
    sys.modules[namespace.__name__] = namespace
    name = "harness2.substrate.base"
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    exec(compile(source, str(path), "exec"), module.__dict__)
    return NativeSubstrate(surface, validate)
