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

jq -e \
	--argjson named_files "$named_files" \
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
	(.platforms["darwin-arm64"] | valid_asset("Deepzora-darwin-arm64.zip")) and
	(.platforms["darwin-amd64"] | valid_asset("Deepzora-darwin-amd64.zip")) and
	(.platforms["windows-amd64"] | valid_asset("Deepzora-windows-amd64-installer.exe")) and
	(.platforms["windows-arm64"] | valid_asset("Deepzora-windows-arm64-installer.exe")) and
	(.platforms["linux-amd64"] | valid_asset("Deepzora-linux-amd64.tar.gz")) and
	(.native_packages | exact_keys(["linux-amd64"])) and
	(.native_packages["linux-amd64"] | valid_asset("Deepzora-linux-amd64.deb")) and
 (. as $m | .downloads | exact_keys(["Deepzora-darwin-arm64.dmg","Deepzora-darwin-amd64.dmg","Deepzora-darwin-universal.dmg","Deepzora-windows-amd64.zip"] + (if $m.artifact_naming == "versioned-v1" then $named_files else [] end))) and
 (.downloads["Deepzora-darwin-arm64.dmg"] | valid_asset("Deepzora-darwin-arm64.dmg")) and
 (.downloads["Deepzora-darwin-amd64.dmg"] | valid_asset("Deepzora-darwin-amd64.dmg")) and
 (.downloads["Deepzora-darwin-universal.dmg"] | valid_asset("Deepzora-darwin-universal.dmg")) and
 (.downloads["Deepzora-windows-amd64.zip"] | valid_asset("Deepzora-windows-amd64.zip")) and
 (if (.artifact_naming // "") == "" then true
  elif .artifact_naming == "versioned-v1" and $version != "v0.0.1" then
    . as $m | all($named_files[]; . as $name | $m.downloads[$name] | valid_asset($name))
  else false end)

' "$manifest" >/dev/null
node "$script_dir/desktop-downloads.mjs" verify "$manifest"
