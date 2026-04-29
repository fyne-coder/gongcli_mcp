#!/usr/bin/env sh
set -eu

patterns='(-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----|ghp_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{20,}|AKIA[0-9A-Z]{16}|GONG_ACCESS_KEY_SECRET[[:space:]]*[:=][[:space:]]*["'\'']?[A-Za-z0-9_/+=.-]{20,})'

if git grep -n -E "$patterns" -- \
	':!go.sum' \
	':!scripts/secret-scan.sh'; then
	echo "secret-scan: possible secret detected" >&2
	exit 1
fi

echo "secret-scan: no tracked secret patterns found"
