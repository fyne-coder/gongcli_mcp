#!/usr/bin/env sh
set -eu

# Public-surface preflight for exact customer/internal wording.
# Avoid bare "tc" so test variables and unrelated abbreviations are not flagged.
patterns='(trade[[:space:]_-]*centric|TC[[:space:]/-]+sales|Mihai|Gerco|ofid_[A-Za-z0-9]+|docker\.transcripts|fyne-llc|jc-direct|jumpcloud-clean|tc-jumpcloud|review-agent)'

usage() {
	cat <<'EOF'
usage:
  scripts/public-surface-scan.sh
  scripts/public-surface-scan.sh --input PATH [--label LABEL]
  scripts/public-surface-scan.sh --stdin [--label LABEL]
  scripts/public-surface-scan.sh --release-bodies [--repo OWNER/REPO] [--limit N]
EOF
}

scan_file_quiet() {
	path=$1
	label=$2
	success_message=$3

	if grep -Eiq "$patterns" "$path"; then
		echo "public-surface-scan: customer/internal public-surface wording detected in ${label}" >&2
		exit 1
	fi

	echo "$success_message"
}

scan_tree() {
	if git grep -n -i -E "$patterns" -- \
		':!go.sum' \
		':!scripts/public-surface-scan.sh' \
		':!scripts/public-surface-scan-test.sh' \
		':!scripts/secret-scan.sh'; then
		echo "public-surface-scan: customer/internal public-surface wording detected" >&2
		exit 1
	fi

	echo "public-surface-scan: no tracked public-surface wording found"
}

scan_stdin() {
	label=$1
	tmp=$(mktemp "${TMPDIR:-/tmp}/gongctl-public-surface-scan.XXXXXX")
	trap 'rm -f "$tmp"' EXIT
	trap 'rm -f "$tmp"; exit 130' HUP INT TERM
	cat >"$tmp"
	scan_file_quiet "$tmp" "$label" "public-surface-scan: no public-surface wording found in ${label}"
}

scan_release_bodies() {
	repo=$1
	limit=$2
	tmp=$(mktemp "${TMPDIR:-/tmp}/gongctl-release-public-surface.XXXXXX")
	tags=$(mktemp "${TMPDIR:-/tmp}/gongctl-release-tags.XXXXXX")
	trap 'rm -f "$tmp" "$tags"' EXIT
	trap 'rm -f "$tmp" "$tags"; exit 130' HUP INT TERM

	if ! command -v gh >/dev/null 2>&1; then
		echo "public-surface-scan: gh CLI is required for --release-bodies" >&2
		exit 2
	fi

	: >"$tmp"
	gh release list --repo "$repo" --limit "$limit" --json tagName --jq '.[].tagName' >"$tags"

	count=0
	while IFS= read -r tag; do
		[ -n "$tag" ] || continue
		gh release view "$tag" --repo "$repo" --json tagName,name,body \
			--jq '"release: \(.tagName) \(.name // "")\n\(.body // "")\n"' </dev/null >>"$tmp"
		count=$((count + 1))
	done <"$tags"

	if [ "$count" -eq 0 ]; then
		echo "public-surface-scan: no GitHub Releases found for ${repo}; check repo, auth, network, or --limit" >&2
		exit 2
	fi

	scan_file_quiet "$tmp" "GitHub Release bodies for ${repo}" "public-surface-scan: no public-surface wording found in ${count} GitHub Release bodies for ${repo}"
}

mode=tree
input_path=
label=input
repo=${GITHUB_REPOSITORY:-fyne-coder/gongcli_mcp}
limit=${GONGCTL_RELEASE_SCAN_LIMIT:-100}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--input)
			mode=input
			input_path=${2:-}
			if [ -z "$input_path" ]; then
				usage >&2
				exit 2
			fi
			shift 2
			;;
		--stdin)
			mode=stdin
			shift
			;;
		--label)
			label=${2:-}
			if [ -z "$label" ]; then
				usage >&2
				exit 2
			fi
			shift 2
			;;
		--release-bodies)
			mode=release_bodies
			shift
			;;
		--repo)
			repo=${2:-}
			if [ -z "$repo" ]; then
				usage >&2
				exit 2
			fi
			shift 2
			;;
		--limit)
			limit=${2:-}
			if [ -z "$limit" ]; then
				usage >&2
				exit 2
			fi
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			usage >&2
			exit 2
			;;
	esac
done

case "$mode" in
	tree)
		scan_tree
		;;
	input)
		if [ ! -f "$input_path" ]; then
			echo "public-surface-scan: input file not found: ${input_path}" >&2
			exit 2
		fi
		scan_file_quiet "$input_path" "$label" "public-surface-scan: no public-surface wording found in ${label}"
		;;
	stdin)
		scan_stdin "$label"
		;;
	release_bodies)
		scan_release_bodies "$repo" "$limit"
		;;
esac
