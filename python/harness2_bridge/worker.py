"""Fixed, one-request Phase B worker. No engine, providers or candidate execution."""
import hashlib
import json
from pathlib import Path
import re
import sys

REVISION = "1cfe254e8d25b15f37048c3730134686abe60335"
MAX_FRAME = 98304
OPERATIONS = ["health", "capabilities", "validate_manifest"]


def pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("duplicate")
        result[key] = value
    return result


def decode(data):
    value = json.loads(data.decode("utf-8"), object_pairs_hook=pairs,
                       parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    def walk(v, depth=0):
        if depth > 8 or v is None:
            raise ValueError("shape")
        if isinstance(v, str):
            v.encode("utf-8", errors="strict")
        elif isinstance(v, dict):
            for k, x in v.items():
                walk(k, depth + 1)
                walk(x, depth + 1)
        elif isinstance(v, list):
            for x in v:
                walk(x, depth + 1)
    walk(value)
    return value


def exact(value, keys):
    if not isinstance(value, dict) or set(value) != set(keys.split()):
        raise ValueError("shape")


def load_upstream(site):
    # -I -S avoids site startup, .pth, user site, PYTHONPATH and cwd imports.
    # Every installed source file is checked BEFORE any upstream import.
    lock_path = Path(__file__).with_name("upstream.lock.json")
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    if lock["revision"] != REVISION or lock["license"] != "Apache-2.0":
        raise ValueError("integrity")
    site = Path(site).resolve(strict=True)
    package = site / "harness2"
    actual = {"harness2/" + p.relative_to(package).as_posix()
              for p in package.rglob("*.py")}
    if actual != set(lock["files"]):
        raise ValueError("integrity")
    sources = {}
    for rel, expected in lock["files"].items():
        p = site / rel
        data = p.read_bytes()
        if p.is_symlink() or len(data) > 1024 * 1024 or hashlib.sha256(data).hexdigest() not in expected.values():
            raise ValueError("integrity")
        sources[rel] = data
    metadata = json.loads((site / "harness2-0.1.0.dist-info/direct_url.json").read_text())
    if metadata.get("vcs_info", {}).get("commit_id") != REVISION:
        raise ValueError("provenance")
    license_file = Path(__file__).with_name("LICENSE.harness2")
    if hashlib.sha256(license_file.read_bytes()).hexdigest() != lock["license_sha256"]:
        raise ValueError("license")
    installed_license = site / "harness2-0.1.0.dist-info/licenses/LICENSE"
    license_bytes = installed_license.read_bytes().replace(b"\r\n", b"\n")
    if hashlib.sha256(license_bytes).hexdigest() != lock["license_sha256"]:
        raise ValueError("license")
    # Execute the verified source snapshots, never cached .pyc/extension modules.
    # These are ORIGINAL upstream modules, not stand-ins or patched algorithms.
    import importlib.util
    for name, rel, is_package in (
        ("harness2", "harness2/__init__.py", True),
        ("harness2.core", "harness2/core/__init__.py", True),
        ("harness2.core.tree", "harness2/core/tree.py", False),
    ):
        path = site / rel
        spec = importlib.util.spec_from_file_location(name, path,
            submodule_search_locations=[str(path.parent)] if is_package else None)
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        exec(compile(sources[rel], str(path), "exec"), module.__dict__)
    from harness2.core.tree import SurfaceSpec, validate
    surface = SurfaceSpec(name="daimon-phase-b", always_on=("systemprompt.md",),
        always_on_max=8192, always_on_cap_reason="native byte limit",
        skill_dir="skills", skill_name_rule="must_match", trigger_hint="",
        autotrigger_file="systemprompt.md", trigger_line="", subagent_requires_mode=True,
        plugin_dir=None, hooks_file=None, reject_empty_always_on=True)
    return surface, validate


def restrict():
    # Defense in depth for this trusted fixed worker, NOT an OS sandbox.
    # Installed after verified imports; no application file I/O is needed afterwards.
    def audit(event, args):
        if event == "open" or event.startswith(("socket.", "subprocess.", "os.exec", "os.spawn")) or event in {
            "os.system", "os.remove", "os.rename", "os.mkdir", "os.rmdir", "os.chdir",
            "os.link", "os.symlink", "os.truncate", "os.fork", "ctypes.dlopen"}:
            raise PermissionError("worker operation denied")
    sys.addaudithook(audit)


def validate_manifest(m, surface, upstream_validate):
    exact(m, "version bot_id base_bot_sha256 revision parent_sha256 components")
    ident = r"[a-z][a-z0-9-]{0,63}"
    sha = r"[0-9a-f]{64}"
    if type(m["version"]) is not int or m["version"] != 1:
        raise ValueError("version")
    if not isinstance(m["bot_id"], str) or not re.fullmatch(ident, m["bot_id"]):
        raise ValueError("id")
    if not isinstance(m["base_bot_sha256"], str) or not re.fullmatch(sha, m["base_bot_sha256"]):
        raise ValueError("hash")
    revision = m["revision"]
    if type(revision) is not int or not 1 <= revision <= 2**64 - 1:
        raise ValueError("revision")
    parent = m["parent_sha256"]
    if not isinstance(parent, str) or (parent != "" if revision == 1 else not re.fullmatch(sha, parent)):
        raise ValueError("parent")
    c = m["components"]
    exact(c, "system_prompt guardrails memory tool_guidance subagents skills scripts plugins")
    for key in ("system_prompt", "guardrails", "memory", "tool_guidance"):
        if not isinstance(c[key], str) or len(c[key].encode("utf-8")) > 8192:
            raise ValueError("text")
    if not c["system_prompt"].strip():
        raise ValueError("empty")
    # Phase B does not dereference any artifacts or run any surface.
    if any(c[key] for key in ("guardrails", "memory", "tool_guidance", "subagents", "skills", "scripts", "plugins")):
        raise ValueError("unsupported")
    for key in ("subagents", "skills", "scripts", "plugins"):
        if not isinstance(c[key], list):
            raise ValueError("collection")
    census = upstream_validate(surface, {"systemprompt.md": c["system_prompt"]}, allow_subagents=False)
    return census["n_files"]


def resource_limits():
    if sys.platform == "linux":
        import resource
        for kind, value in ((resource.RLIMIT_AS, 256 * 1024 * 1024),
                            (resource.RLIMIT_CPU, 5), (resource.RLIMIT_FSIZE, 0),
                            (resource.RLIMIT_NOFILE, 64), (resource.RLIMIT_CORE, 0)):
            resource.setrlimit(kind, (value, value))
    elif sys.platform != "win32":
        raise ValueError("unsupported platform")


def main():
    request_id, operation = 0, "invalid"
    result = {"revision": REVISION, "operations": OPERATIONS, "manifest_valid": False,
              "tree_files": 0, "execution": False, "os_sandbox": False}
    code, ok = "invalid_request", False
    try:
        if len(sys.argv) != 2 or not sys.flags.isolated or not sys.flags.no_site or sys.version_info[:2] != (3, 12):
            raise ValueError("startup")
        resource_limits()
        surface, validate = load_upstream(sys.argv[1])
        restrict()
        data = sys.stdin.buffer.read(MAX_FRAME + 1)
        if len(data) > MAX_FRAME:
            raise ValueError("limit")
        req = decode(data)
        exact(req, "version id operation payload")
        if type(req["version"]) is not int or req["version"] != 1:
            raise ValueError("version")
        if type(req["id"]) is not int or not 1 <= req["id"] <= 2**53 - 1:
            raise ValueError("id")
        request_id = req["id"]
        if req["operation"] not in OPERATIONS:
            raise ValueError("operation")
        operation = req["operation"]
        if operation == "validate_manifest":
            exact(req["payload"], "manifest")
            result["tree_files"] = validate_manifest(req["payload"]["manifest"], surface, validate)
            result["manifest_valid"] = True
        else:
            exact(req["payload"], "")
        code, ok = "ok", True
    except Exception:
        pass  # Never return exception text, paths, manifest content or traceback.
    response = {"version": 1, "id": request_id, "operation": operation,
                "ok": ok, "code": code, "result": result}
    sys.stdout.buffer.write(json.dumps(response, separators=(",", ":")).encode("ascii") + b"\n")
    sys.stdout.buffer.flush()
    return 0 if ok else 2


if __name__ == "__main__":
    raise SystemExit(main())
