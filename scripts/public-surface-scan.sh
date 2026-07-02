#!/usr/bin/env sh
set -eu

# Public-surface preflight for exact customer/internal wording.
# Avoid bare "tc" so test variables and unrelated abbreviations are not flagged.
patterns='(trade[[:space:]_-]*centric|TC[[:space:]/-]+sales|Mihai|Gerco|ofid_[A-Za-z0-9]+|docker\.transcripts|fyne-llc|jc-direct|jumpcloud-clean|tc-jumpcloud|review-agent)'

if git grep -n -i -E "$patterns" -- \
	':!go.sum' \
	':!scripts/public-surface-scan.sh' \
	':!scripts/secret-scan.sh'; then
	echo "public-surface-scan: customer/internal public-surface wording detected" >&2
	exit 1
fi

echo "public-surface-scan: no tracked public-surface wording found"
