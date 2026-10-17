#!/usr/bin/env bash
set -euo pipefail

channel="${1:-}"
version="${2:-}"
asset_base="${3:-}"
manifest="${4:-}"
notes_version="${5:-$version}"

stable_version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

case "$channel" in
	stable)
		version_pattern="$stable_version_pattern"
		;;
	*)
		echo "Desktop manifest channel must be stable: $channel" >&2
		exit 2
		;;
esac

if [[ ! "$version" =~ $version_pattern ]]; then
	echo "invalid $channel Desktop manifest version: $version" >&2
	exit 1
fi
if [[ ! "$notes_version" =~ $stable_version_pattern ]]; then
	echo "invalid Desktop release-notes version: $notes_version" >&2
	exit 1
fi

expected_tag="desktop-${version}"
github_base="https://github.com/SivanCola/deepzora/releases/download/${expected_tag}/"
if [ "$asset_base" != "$github_base" ]; then
	echo "invalid official Desktop asset base: $asset_base" >&2
	exit 1
fi
if [ ! -f "$manifest" ]; then
	echo "Desktop release manifest does not exist: $manifest" >&2
	exit 1
fi
script_dir="$(cd "$(dirname "$0")" && pwd)"
named_files="$(node "$script_dir/desktop-downloads.mjs" names "$version")"
asset_specs="$(cat "$script_dir/../internal/releaseassets/desktop.json")"

jq -e \
	--argjson named_files "$named_files" \
 --argjson specs "$asset_specs" \
	--arg version "$version" \
	--arg notes_version "$notes_version" \
	--arg base "$asset_base" \
'
	def exact_keys($expected):
		(type == "object") and ((keys | sort) == ($expected | sort));
	def valid_asset($name):
		(type == "object") and
		(.url == ($base + $name)) and
		(.sig == ($base + $name + ".minisig")) and
			(.size | type == "number" and . > 0 and . <= 1073741824 and floor == .) and
			(.sha256 | type == "string" and test("^[0-9a-f]{64}$"));

	(type == "object") and
	(.version == $version) and
	(.download_page == "https://github.com/SivanCola/deepzora/releases") and
	(.release_notes_url == ("https://github.com/SivanCola/deepzora/releases/tag/" + $notes_version + "/")) and
	(.platforms | exact_keys([
		"darwin-arm64",
		"darwin-amd64",
		"windows-amd64",
		"windows-arm64",
		"linux-amd64"
	])) and
 (.native_packages | exact_keys(["linux-amd64"])) and
 (.downloads | exact_keys($named_files)) and
 (.artifact_naming == "versioned-v1") and
 (. as $m | all($specs[];
   . as $spec | ("Deepzora-" + ($version | ltrimstr("v")) + "-" + $spec.suffix) as $name |
   ($m.downloads[$name] | valid_asset($name)) and
   (if $spec.channel then $m[$spec.channel][$spec.platform] == $m.downloads[$name] else true end)))

' "$manifest" >/dev/null
node "$script_dir/desktop-downloads.mjs" verify "$manifest"
