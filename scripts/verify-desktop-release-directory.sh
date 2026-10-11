#!/usr/bin/env bash
set -euo pipefail

allow_missing=false
allow_signature_differences=false
allow_authenticated_payload_differences=false
while [ "$#" -gt 0 ]; do
	case "$1" in
	--allow-missing)
		allow_missing=true
		shift
		;;
	--allow-signature-differences)
		# Callers must cryptographically verify both signature sets before using
		# this comparison mode. Minisign trusted comments make two valid
		# signatures for identical content byte-distinct across recovery runs.
		allow_signature_differences=true
		shift
		;;
	--allow-authenticated-payload-differences)
		# Callers must cryptographically verify both complete payload/signature
		# sets before using this comparison mode. Signed Desktop packages can be
		# byte-distinct across rebuilds because platform signing and packaging
		# embed timestamps and other non-deterministic data.
		allow_authenticated_payload_differences=true
		allow_signature_differences=true
		shift
		;;
	*) break ;;
	esac
done
if [ "$#" -ne 2 ]; then
	echo "usage: $0 [--allow-missing] [--allow-signature-differences] [--allow-authenticated-payload-differences] CANDIDATE_DIRECTORY EXISTING_DIRECTORY" >&2
	exit 2
fi

candidate_dir="${1%/}"
existing_dir="${2%/}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ ! -d "$candidate_dir" ] || [ ! -d "$existing_dir" ]; then
	echo "Desktop release comparison requires two directories" >&2
	exit 2
fi

verify_subset() {
	local source_dir="$1"
	local target_dir="$2"
	local source_file relative target_file
	while IFS= read -r -d '' source_file; do
		relative="${source_file#"$source_dir"/}"
		target_file="$target_dir/$relative"
		if [ ! -f "$target_file" ]; then
			echo "Desktop release directory is missing $relative" >&2
			return 1
		fi
 if [ "$relative" = "latest.json" ]; then
  bash "$script_dir/compare-desktop-release-manifests.sh" "$source_file" "$target_file" || return 1
  continue
 fi
		if [ "$allow_signature_differences" = "true" ] && [[ "$relative" = *.minisig ]]; then
			if [ ! -s "$source_file" ] || [ ! -s "$target_file" ]; then
				echo "Desktop release directory has an empty signature for $relative" >&2
				return 1
			fi
			continue
		fi
		if [ "$allow_authenticated_payload_differences" = "true" ] &&
			[ -s "$source_file.minisig" ] && [ -s "$target_file.minisig" ]; then
			continue
		fi
		if ! cmp -s "$source_file" "$target_file"; then
			echo "Desktop release directory has conflicting content for $relative" >&2
			return 1
		fi
	done < <(find "$source_dir" -type f -print0)
}

# Existing objects may never disagree with or fall outside the candidate set.
verify_subset "$existing_dir" "$candidate_dir"
if [ "$allow_missing" != "true" ]; then
	verify_subset "$candidate_dir" "$existing_dir"
fi
