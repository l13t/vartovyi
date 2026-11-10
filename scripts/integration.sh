#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
export VARTOVYI_HTTP_PORT=0
vartovyi_test_project="vartovyi-test-$$"
compose() { docker compose -p "$vartovyi_test_project" -f deploy/compose.test.yml "$@"; }
cleanup() {
  result=$?
  if [ "$result" -ne 0 ]; then compose logs --no-color; fi
  compose down -v
  exit "$result"
}
trap cleanup EXIT
compose up --build -d postgres nats source media api recorder detector notifier
if [ -n "${VARTOVYI_TEST_RUN:-}" ]; then
  compose run --build --rm --no-deps tests -test.run "$VARTOVYI_TEST_RUN"
else
  compose run --build --rm --no-deps tests
fi
