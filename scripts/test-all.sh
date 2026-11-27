#!/bin/sh
# Run every Go check, including database integration tests, inside the API
# container (or any host with Go and PostgreSQL 16).
#
#   docker compose ... exec -T api sh /workspace/scripts/test-all.sh
#
# TEST_DATABASE_URL must point at a database whose user may create roles. The
# default is the development stack's pamoja_test database; override it for CI.
set -eu

cd "$(dirname "$0")/../backend"

if [ -z "${TEST_DATABASE_URL:-}" ]; then
    TEST_DATABASE_URL="postgres://pamoja:pamoja_dev_password@db:5432/pamoja_test?sslmode=disable"
    export TEST_DATABASE_URL
fi

echo "== gofmt"
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
    echo "these files need gofmt:"
    echo "$unformatted"
    exit 1
fi

echo "== go vet"
go vet ./...

echo "== go test -race -p 1"
go test -race -p 1 ./...

echo "== coverage gate"
COVERAGE_THRESHOLD="${COVERAGE_THRESHOLD:-85}" \
    go test -p 1 -coverprofile=/tmp/coverage.out -covermode=atomic ./... >/dev/null
../scripts/check-coverage.sh /tmp/coverage.out
