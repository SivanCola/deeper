#!/usr/bin/env bash
set -euo pipefail

# Keep the current channel pointer monotonic; retrying the same candidate is idempotent.
if [ "$#" -ne 3 ]; then
  echo "usage: $0 CHANNEL CANDIDATE_MANIFEST CURRENT_MANIFEST|-" >&2
  exit 2
fi
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
compare="$script_dir/compare-desktop-release-versions.sh"
channel="$1"
candidate_file="$2"
current_file="$3"
case "$channel" in
  stable | preview) ;;
  *) echo "unsupported Desktop pointer channel: $channel" >&2; exit 2 ;;
esac
if [ ! -f "$candidate_file" ]; then
  echo "candidate Desktop manifest is not a readable file: $candidate_file" >&2
  exit 2
fi
candidate_version="$(jq -er '.version | strings' "$candidate_file")"
bash "$compare" "$channel" "$candidate_version" "" >/dev/null
if [ "$current_file" = "-" ]; then
  printf 'update\t%s\n' "$candidate_file"
  exit 0
fi
if [ ! -f "$current_file" ]; then
  echo "current Desktop manifest is not a readable file: $current_file" >&2
  exit 2
fi
current_version="$(jq -er '.version | strings' "$current_file")"
if [ "$(bash "$compare" "$channel" "$candidate_version" "$current_version")" = "skip" ] || cmp -s "$candidate_file" "$current_file"; then
  echo skip
else
  printf 'update\t%s\n' "$candidate_file"
fi
