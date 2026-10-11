#!/usr/bin/env bash
# Resolve the current official release format. All publications use the sealed candidate flow.
set -euo pipefail
if [ "${IN_ORCHESTRATED:-false}" != true ] && [ "${EVENT_NAME:-}" = workflow_dispatch ]; then
  [ "${CALLER_REF:-}" = refs/heads/main ] && [ "${CALLER_REF_PROTECTED:-}" = true ] || {
    echo "::error::manual CLI releases must run from protected main" >&2; exit 1;
  }
fi
[ "${IN_CHANNEL:-stable}" = stable ] || { echo "::error::unsupported release channel" >&2; exit 1; }
tag="${IN_TAG:-${REF_NAME:-}}"
pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$tag" =~ $pattern ]] || { echo "::error::release tag must be vMAJOR.MINOR.PATCH: $tag" >&2; exit 1; }
version="${tag#v}"
{
  echo "tag=$tag"
  echo "version=$version"
  echo "channel=stable"
  echo "prerelease=false"
  echo "notes_version=$tag"
  echo "base_version=${tag#v}"
} >>"${GITHUB_OUTPUT:-/dev/stdout}"
