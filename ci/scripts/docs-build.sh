#!/usr/bin/env bash
set -euo pipefail

# Build the Hugo documentation site.
# Output goes to docs/public/ (Hugo default for the configured source dir).
hugo --source docs --minify
