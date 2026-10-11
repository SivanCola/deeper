#!/usr/bin/env bash
# Resolve and authorize the immutable source commit for a Desktop publication.
# The protected control plane verifies a sealed candidate or an immutable official tag.
set -euo pipefail

channel="${RELEASE_CHANNEL:?RELEASE_CHANNEL is required}"
tag="${RELEASE_TAG:?RELEASE_TAG is required}"
orchestrated="${IN_ORCHESTRATED:-false}"
orchestrator="${IN_ORCHESTRATOR:-}"
candidate_preparation="${CANDIDATE_PREPARATION:-false}"
candidate_rehearsal="${CANDIDATE_REHEARSAL:-false}"
approved_sha="${APPROVED_SHA:-}"
caller_event="${CALLER_EVENT_NAME:-}"
caller_ref="${CALLER_REF:-}"
caller_ref_protected="${CALLER_REF_PROTECTED:-}"
caller_sha="${CALLER_SHA:-}"
caller_workflow_sha="${CALLER_WORKFLOW_SHA:-}"
release_remote="${RELEASE_REMOTE:-origin}"
require_current_main="${REQUIRE_CURRENT_MAIN:-true}"
verify_checkout="${VERIFY_RELEASE_CHECKOUT:-false}"

case "$orchestrated" in
true | false) ;;
*)
	echo "::error::IN_ORCHESTRATED must be true or false, got: $orchestrated" >&2
	exit 2
	;;
esac
if [ "$orchestrated" = "true" ]; then
	case "$orchestrator" in
	stable | candidate | promote) ;;
	*)
		echo "::error::IN_ORCHESTRATOR must be stable, candidate or promote for an orchestrated Desktop release, got: $orchestrator" >&2
		exit 2
		;;
	esac
	if [ "$orchestrator" != "$channel" ] && [ "$orchestrator" != candidate ] && [ "$orchestrator" != promote ]; then
		echo "::error::the $orchestrator orchestrator cannot authorize a Desktop $channel candidate" >&2
		exit 1
	fi
fi
case "$candidate_preparation" in
true | false) ;;
*)
	echo "::error::CANDIDATE_PREPARATION must be true or false, got: $candidate_preparation" >&2
	exit 2
	;;
esac
if [ "$candidate_preparation" = true ] && { [ "$orchestrated" != true ] || [ "$orchestrator" != candidate ] || [ "$channel" != stable ]; }; then
	echo "::error::candidate preparation requires the protected candidate orchestrator" >&2
	exit 1
fi
case "$candidate_rehearsal" in
true)
	[ "$candidate_preparation" = true ] || { echo "::error::rehearsal requires non-publishing candidate preparation" >&2; exit 1; }
	;;
false) ;;
*) echo "::error::CANDIDATE_REHEARSAL must be true or false" >&2; exit 2 ;;
esac
case "$require_current_main" in
true | false) ;;
*)
	echo "::error::REQUIRE_CURRENT_MAIN must be true or false, got: $require_current_main" >&2
	exit 2
	;;
esac
case "$verify_checkout" in
true | false) ;;
*)
	echo "::error::VERIFY_RELEASE_CHECKOUT must be true or false, got: $verify_checkout" >&2
	exit 2
	;;
esac

[ "$channel" = stable ] || { echo "::error::unsupported Desktop release channel" >&2; exit 1; }
stable_tag_pattern='^desktop-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$tag" =~ $stable_tag_pattern ]] || { echo "::error::Desktop tag must be desktop-vMAJOR.MINOR.PATCH" >&2; exit 1; }

git fetch "$release_remote" main --tags
main_sha="$(git rev-parse "$release_remote/main^{commit}")"

if [ "$orchestrated" = "true" ]; then
	candidate="$approved_sha"
else
	if [ "$caller_event" != "workflow_dispatch" ] ||
		[ "$caller_ref" != "refs/heads/main" ] ||
		[ "$caller_ref_protected" != "true" ]; then
		echo "::error::standalone Desktop releases must run from protected main" >&2
		exit 1
	fi
	if [ "$caller_sha" != "$caller_workflow_sha" ]; then
		echo "::error::standalone Desktop workflow SHA is $caller_workflow_sha, expected caller SHA $caller_sha" >&2
		exit 1
	fi
	candidate="$(git rev-parse "$tag^{commit}")"
fi

if [[ ! "$candidate" =~ ^[0-9a-f]{40}$ ]]; then
	echo "::error::Desktop candidate SHA must be a full commit SHA, got: $candidate" >&2
	exit 1
fi
if ! git cat-file -e "$candidate^{commit}" 2>/dev/null; then
	echo "::error::Desktop candidate commit is unavailable: $candidate" >&2
	exit 1
fi
if ! git merge-base --is-ancestor "$candidate" "$main_sha"; then
	echo "::error::Desktop candidate $candidate is not on main history at $main_sha" >&2
	exit 1
fi

if [ "$channel" = "stable" ]; then
	if [ "$candidate_preparation" = true ]; then
		if [ "$candidate_rehearsal" != true ] && git show-ref --verify --quiet "refs/tags/$tag"; then
			echo "::error::candidate preparation refuses an existing release tag: $tag" >&2
			exit 1
		fi
	else
		tag_sha="$(git rev-parse "$tag^{commit}")"
		if [ "$tag_sha" != "$candidate" ]; then
			echo "::error::$tag points to $tag_sha, expected Desktop candidate $candidate" >&2
			exit 1
		fi
	fi
fi

if [ "$verify_checkout" = "true" ]; then
	head_sha="$(git rev-parse HEAD^{commit})"
	if [ "$head_sha" != "$candidate" ]; then
		echo "::error::Desktop release checkout is $head_sha, expected candidate $candidate" >&2
		exit 1
	fi
fi

echo "sha=$candidate" >>"${GITHUB_OUTPUT:-/dev/stdout}"
echo "Desktop candidate verified: channel=$channel tag=$tag sha=$candidate"
