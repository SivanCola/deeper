#!/usr/bin/env bash
# Executable contracts for the supported release formats and protected caller.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
for surface in cli desktop npm; do
  case "$surface" in cli) tag=v1.0.0;; desktop) tag=desktop-v1.0.0;; npm) tag=npm-v1.0.0;; esac
  output="$(IN_TAG="$tag" IN_BASE_VERSION=1.0.0 IN_CHANNEL=stable bash "scripts/resolve-$surface-release.sh")"
  [[ "$output" == *1.0.0* ]]
  for bad in "$tag-preview.1" "$tag-rc.1" invalid; do
    if IN_TAG="$bad" bash "scripts/resolve-$surface-release.sh" >/dev/null 2>&1; then
      echo "unsupported release accepted: $bad" >&2; exit 1
    fi
  done
done
ACTUAL_CALLER_WORKFLOW_REF='SivanCola/deepzora/.github/workflows/release-promote.yml@refs/heads/main' \
EXPECTED_CALLER_WORKFLOW_REF='SivanCola/deepzora/.github/workflows/release-promote.yml@refs/heads/main' \
CALLER_EVENT_NAME=workflow_dispatch CALLER_REF=refs/heads/main CALLER_REF_PROTECTED=true \
CALLER_WORKFLOW_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa CALLER_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
APPROVED_CLI_TAG=v1.0.0 APPROVED_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
bash scripts/verify-release-authorization.sh
go run ./cmd/release-signing-contract validate
bash scripts/release-candidate-tags.test.sh
bash scripts/release-stable.test.sh
echo 'current release workflow contracts: PASS'
