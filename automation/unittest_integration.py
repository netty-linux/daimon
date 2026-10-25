"""Run the existing fixed Python suites with machine-readable skip evidence."""
import argparse
import json
from pathlib import Path
import runpy
import types
import unittest

ROOT = Path(__file__).resolve().parents[1]
SUITES = {
    'worker': 'python/harness2_bridge/test_worker.py',
    'native-substrate': 'python/harness2_native_substrate/test_substrate.py',
    'linux-limits': 'python/harness2_bridge/test_linux_limits.py',
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('suite', choices=sorted(SUITES))
    args = parser.parse_args()
    module = types.ModuleType('audited_integration')
    module.__dict__.update(runpy.run_path(str(ROOT / SUITES[args.suite]), run_name='audited_integration'))
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromModule(module))
    record = {'suite': args.suite, 'tests': result.testsRun,
              'failures': len(result.failures), 'errors': len(result.errors),
              'skipped': [{'test': test.id(), 'reason': reason} for test, reason in result.skipped],
              'successful': result.wasSuccessful() and result.testsRun > 0 and not result.skipped}
    print('DAIMON_UNITTEST_RESULT ' + json.dumps(record), flush=True)
    return 0 if record['successful'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
