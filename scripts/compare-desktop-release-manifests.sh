#!/usr/bin/env bash
set -euo pipefail

candidate="${1:-}"
existing="${2:-}"

for manifest in "$candidate" "$existing"; do
	if [ ! -f "$manifest" ]; then
		echo "Desktop release manifest does not exist: $manifest" >&2
		exit 1
	fi
done

# Immutable release metadata must be semantically identical.
jq -e -s '.[0] == .[1]' "$candidate" "$existing" >/dev/null
