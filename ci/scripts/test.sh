#!/usr/bin/env bash
set -euo pipefail

echo "Running tests..."
go install gotest.tools/gotestsum@latest
go install github.com/mfridman/tparse@latest
export PATH=$(go env GOPATH)/bin:$PATH
mkdir -p test-results
gotestsum --format=standard-verbose --jsonfile test-output.json -- -v $(go list ./... | grep -v /vendor/) -cover
tparse -all -file=test-output.json
