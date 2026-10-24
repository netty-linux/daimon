#!/usr/bin/env bash
set -euo pipefail
# Validation only, in the disposable B image. No adversarial fixture is launched.
if [[ $(id -u) == 0 ]]; then
  printf 'Run the validation as an unprivileged Linux identity.\n' >&2
  exit 2
fi
bash /source/python/harness2_bridge/test-linux.sh
cd /work/project
go build -o /work/isolation-audit ./cmd/harness2-isolation-audit
set +e
/work/isolation-audit
audit_exit=$?
set -e
if [[ $audit_exit != 3 ]]; then
  printf 'Diagnostic failed to refuse fixture execution.\n' >&2
  exit 1
fi
printf 'Diagnostic exit=3; fixtures NOT RUN; no isolation attested.\n'
