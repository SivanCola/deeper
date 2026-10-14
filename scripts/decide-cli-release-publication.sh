#!/usr/bin/env bash
set -euo pipefail

channel="${1:-}"
tag="${2:-}"
repository="${3:-}"
release_json="${4:-}"
checksums="${5:-}"

stable_tag_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

case "$channel" in
	stable)
		tag_pattern="$stable_tag_pattern"
		expected_prerelease=false
		;;
	*)
		echo "CLI publication channel must be stable: $channel" >&2
		exit 2
		;;
esac

if [[ ! "$tag" =~ $tag_pattern ]]; then
	echo "invalid $channel CLI publication tag: $tag" >&2
	exit 1
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
	echo "invalid GitHub repository: $repository" >&2
	exit 1
fi

if [ "$release_json" = "-" ]; then
	echo publish
	exit 0
fi
if [ ! -f "$release_json" ]; then
	echo "CLI GitHub release record does not exist: $release_json" >&2
	exit 1
fi
if [ ! -f "$checksums" ]; then
	echo "CLI GitHub release checksum asset does not exist: $checksums" >&2
	exit 1
fi

required_assets='[
  "deepzora-darwin-amd64.tar.gz",
  "deepzora-darwin-arm64.tar.gz",
  "deepzora-linux-amd64.tar.gz",
  "deepzora-linux-arm64.tar.gz",
  "deepzora-windows-amd64.zip",
  "deepzora-windows-arm64.zip",
  "SHA256SUMS"
]'

# These documents are added by the post-publication reconciler. They are not
# binaries covered by SHA256SUMS and must not make an otherwise complete
# immutable CLI release ineligible for recovery.
metadata_assets='["latest.json", "cli-latest.json", "release-event.json"]'

jq -e \
	--arg tag "$tag" \
	--arg repository "$repository" \
	--arg expected_prerelease "$expected_prerelease" \
	--argjson required "$required_assets" \
	--argjson metadata "$metadata_assets" '
	(.assets | map(.name)) as $names |
	(type == "object") and
	(.tag_name == $tag) and
	(.draft == false) and
	(.prerelease == ($expected_prerelease == "true")) and
	(.html_url == ("https://github.com/" + $repository + "/releases/tag/" + $tag)) and
	(.assets | type == "array") and
	(($names | length) == ($names | unique | length)) and
	all($required[]; . as $name | $names | index($name) != null) and
	all($names[]; . as $name | ($required + $metadata) | index($name) != null) and
	(.assets | all(
		(type == "object") and
		(.state == "uploaded") and
		(.size | type == "number" and . > 0 and . <= 1073741824 and floor == .) and
		(.browser_download_url ==
			("https://github.com/" + $repository + "/releases/download/" + $tag + "/" + .name)) and
		(.digest | type == "string" and test("^sha256:[0-9a-f]{64}$"))
	))
' "$release_json" >/dev/null || {
	echo "existing CLI GitHub release is incomplete or does not match approved release $tag" >&2
	exit 1
}

expected_checksums="$(mktemp)"
actual_checksums="$(mktemp)"
trap 'rm -f "$expected_checksums" "$actual_checksums"' EXIT

jq -r '
	.assets[] |
	select(.name | test("^deepzora-(darwin|linux|windows)-(amd64|arm64)\\.(tar\\.gz|zip)$")) |
	((.digest | sub("^sha256:"; "")) + "  " + .name)
' "$release_json" | LC_ALL=C sort >"$expected_checksums"

if ! awk '
	BEGIN { valid = 1 }
	$0 !~ /^[0-9a-f]{64}  deepzora-(darwin|linux|windows)-(amd64|arm64)\.(tar\.gz|zip)$/ {
		valid = 0
	}
	{ print }
	END { if (NR != 6 || !valid) exit 1 }
' "$checksums" | LC_ALL=C sort >"$actual_checksums"; then
	echo "existing CLI SHA256SUMS is malformed or incomplete" >&2
	exit 1
fi

if ! cmp -s "$expected_checksums" "$actual_checksums"; then
	echo "existing CLI SHA256SUMS does not match GitHub asset digests" >&2
	exit 1
fi

checksum_asset_digest="$(
	jq -er '.assets[] | select(.name == "SHA256SUMS") | .digest | sub("^sha256:"; "")' \
		"$release_json"
)"
downloaded_checksum_digest="$(shasum -a 256 "$checksums" | awk '{print $1}')"
if [ "$downloaded_checksum_digest" != "$checksum_asset_digest" ]; then
	echo "downloaded CLI SHA256SUMS digest does not match GitHub release metadata" >&2
	exit 1
fi

echo reuse
