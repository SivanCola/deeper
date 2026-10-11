#!/usr/bin/env bash
set -euo pipefail
test "$#" -eq 1 || { echo 'usage: check-release-public-access.sh VERSION' >&2; exit 2; }
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [ "${DEEPZORA_RELEASE_BOOTSTRAP_CHECK:-false}" = "true" ]; then
	state="$(node "$script_dir/github-release-bootstrap.mjs")"
	if [ "$state" = "empty" ]; then
		echo 'First Deepzora stable release: pointer access is verified after publication'
		exit 0
	fi
	test "$state" = ready
fi
manifest="$(mktemp)"
trap 'rm -f -- "$manifest"' EXIT
# The current Stable version normally predates a new candidate. This gate checks
# accessibility and JSON, not ownership of a version that is not published yet.
bash "$script_dir/fetch-stable-release-manifest.sh" "$1" "$manifest"
echo 'public Stable manifest preflight: PASS'
