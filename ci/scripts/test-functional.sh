#!/usr/bin/env bash
# Run all functional test scenarios sequentially (for local development).
# In CI these run as parallel jobs — see .circleci/config.yml.
#
# Usage: mise run test-functional
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
FAILED=0

run() {
  if ! "$SCRIPT_DIR/test-functional-scenario.sh" "$@"; then
    FAILED=1
  fi
}

run "single-instance"  1  33 55 10
run "5-instances"       5  36 50 15
run "10-instances"     10  38 45 20

if [ "$FAILED" -ne 0 ]; then
  echo "==> SOME FUNCTIONAL TEST SCENARIOS FAILED"
  exit 1
fi

echo "==> All functional test scenarios passed!"
