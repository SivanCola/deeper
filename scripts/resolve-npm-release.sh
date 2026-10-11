#!/usr/bin/env bash
set -euo pipefail
[ "${IN_CHANNEL:-stable}" = stable ] || { echo "::error::unsupported npm channel" >&2; exit 1; }
tag="${IN_TAG:-${REF_NAME:-}}"
pattern='^npm-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$tag" =~ $pattern ]] || { echo "::error::npm tag must be npm-vMAJOR.MINOR.PATCH: $tag" >&2; exit 1; }
if [ -n "${IN_BASE_VERSION:-}" ] && [ "$tag" != "npm-v${IN_BASE_VERSION#v}" ]; then
  echo "::error::npm tag does not match requested version" >&2; exit 1
fi
echo "arg=${tag#npm-}" >>"${GITHUB_OUTPUT:-/dev/stdout}"
