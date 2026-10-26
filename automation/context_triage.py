"""Attach a reproducible contextual review to this run's existing static evidence."""
import json
import argparse
import os
from pathlib import Path
import sys

sys.path.insert(0,str(Path(__file__).resolve().parent))
import context_review
import evidence
import upstream_checks
from security_setup import ROOT


def identity_args(argv=None):
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--run-id',required=True)
    parser.add_argument('--run-attempt',required=True)
    return parser.parse_args(argv)


def main():
    identity=identity_args()
    try:
        scopes={r['id']:r['scope'] for r in json.loads((ROOT/'automation/coverage.json').read_text())['lanes']}
        source=ROOT/'.automation-results/security-upstream'
        evidence.load_verified(source,evidence.revision(),identity.run_id,identity.run_attempt,scopes)
        raw_text=(source/'scan.json').read_text(encoding='utf-8')
        raw=json.loads(raw_text)
        policy=json.loads((ROOT/'automation/upstream-inventory.lock.json').read_text())
        sources=upstream_checks.verify_snapshot(raw['snapshot'],policy)
        integration={p:(ROOT/p).read_bytes().replace(b'\r\n',b'\n') for p in context_review.INTEGRATION_FILES}
        ledger=json.loads((ROOT/'automation/context-review.json').read_text())
        review=context_review.review(raw,sources,integration,ledger)
        print(json.dumps(dict(schema_version=1,upstream_raw=raw_text,upstream_sha256=context_review.digest(raw_text.encode()),review=review)))
        return 0
    except Exception as error:
        print(json.dumps(dict(schema_version=1,error=str(error),category='review-unavailable' if isinstance(error,FileNotFoundError) else 'review-integrity')))
        return 2


if __name__=='__main__':raise SystemExit(main())
