#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scanner="${script_dir}/public-surface-scan.sh"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/gongctl-public-surface-test.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

clean_fixture="${tmp_dir}/clean-release-body.txt"
bad_fixture="${tmp_dir}/bad-release-body.txt"
bad_stderr="${tmp_dir}/bad-stderr.txt"

printf '%s\n' 'Generic Gong evidence workbench release notes.' >"$clean_fixture"
"$scanner" --input "$clean_fixture" --label clean-fixture >/dev/null

bad_phrase="Trade""Centric"
printf 'Generic body with %s tenant wording.\n' "$bad_phrase" >"$bad_fixture"
if "$scanner" --input "$bad_fixture" --label bad-fixture >"$tmp_dir/bad-stdout.txt" 2>"$bad_stderr"; then
	echo "public-surface-scan-test: bad fixture unexpectedly passed" >&2
	exit 1
fi

if ! grep -q 'bad-fixture' "$bad_stderr"; then
	echo "public-surface-scan-test: bad fixture did not identify the input label" >&2
	exit 1
fi

if grep -q "$bad_phrase" "$bad_stderr"; then
	echo "public-surface-scan-test: bad fixture leaked matching release text" >&2
	exit 1
fi

if grep -q "$bad_phrase" "$tmp_dir/bad-stdout.txt"; then
	echo "public-surface-scan-test: bad fixture leaked matching release text to stdout" >&2
	exit 1
fi

tc_sales_phrase="TC"" sales"
if printf 'Generic body with %s wording.\n' "$tc_sales_phrase" |
	"$scanner" --stdin --label stdin-bad-fixture >"$tmp_dir/stdin-bad-stdout.txt" 2>"$tmp_dir/stdin-bad-stderr.txt"; then
	echo "public-surface-scan-test: stdin bad fixture unexpectedly passed" >&2
	exit 1
fi

if grep -q "$tc_sales_phrase" "$tmp_dir/stdin-bad-stdout.txt" "$tmp_dir/stdin-bad-stderr.txt"; then
	echo "public-surface-scan-test: stdin bad fixture leaked matching release text" >&2
	exit 1
fi

printf '%s\n' 'public-surface-scan-test: fixtures passed'
