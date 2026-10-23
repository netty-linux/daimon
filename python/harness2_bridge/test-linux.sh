#!/usr/bin/env bash
set -euo pipefail
# /source is mounted read-only. All tests run on a private Linux filesystem,
# preserving symlink/mode semantics and same-filesystem temporary fixtures.
mkdir -p /work/project /work/test-tmp
umask 0022
tar -C /source --exclude=.git --exclude=.harness2-upstream --exclude=node_modules --exclude=.venv --exclude=__pycache__ -cf - . | tar -C /work/project -xf -
cd /work/project
export TMPDIR=/work/test-tmp
export DAIMON_HARNESS2_TEST_PYTHON=/opt/bridge/.venv/bin/python
export DAIMON_HARNESS2_TEST_SITE=/opt/bridge/.venv/lib/python3.12/site-packages
python --version
go version
/opt/bridge/.venv/bin/python -I -S -B python/harness2_bridge/test_worker.py
/opt/bridge/.venv/bin/python -I -S -B python/harness2_native_substrate/test_substrate.py
/opt/bridge/.venv/bin/python -I -S -B python/harness2_bridge/test_linux_limits.py
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/daimon demo
