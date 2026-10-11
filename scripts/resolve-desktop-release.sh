#!/usr/bin/env bash
# Resolve the current official release format. All publications use the sealed candidate flow.
set -euo pipefail
[ "${IN_CHANNEL:-stable}" = stable ] || { echo "::error::unsupported release channel" >&2; exit 1; }
tag="${IN_TAG:-${REF_NAME:-}}"
pattern='^desktop-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$tag" =~ $pattern ]] || { echo "::error::release tag must be desktop-vMAJOR.MINOR.PATCH: $tag" >&2; exit 1; }
version="${tag#desktop-}"
{
  echo "tag=$tag"
  echo "version=$version"
  echo "channel=stable"
  echo "prerelease=false"
  echo "notes_version=$version"
} >>"${GITHUB_OUTPUT:-/dev/stdout}"
