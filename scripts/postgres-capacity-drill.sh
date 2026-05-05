#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOAD_SMOKE="$ROOT/scripts/postgres-load-smoke.sh"

PROJECT="${GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT:-gongctl-postgres-capacity-drill-$$}"
PORT="${GONGCTL_POSTGRES_CAPACITY_PORT:-55445}"
CALL_COUNT="${GONGCTL_POSTGRES_CAPACITY_CALLS:-5000}"
PROFILE_ROWS="${GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS:-$CALL_COUNT}"

case "$PROJECT" in
  ''|*[!a-zA-Z0-9_-]*)
    echo "GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT must contain only letters, numbers, underscores, or dashes" >&2
    exit 1
    ;;
  gongctl-postgres-capacity*)
    ;;
  *)
    echo "GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT must start with gongctl-postgres-capacity" >&2
    exit 1
    ;;
esac

case "$PORT" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CAPACITY_PORT must be an integer" >&2
    exit 1
    ;;
esac
if [ "$PORT" -lt 1024 ] || [ "$PORT" -gt 65535 ]; then
  echo "GONGCTL_POSTGRES_CAPACITY_PORT must be between 1024 and 65535" >&2
  exit 1
fi

case "$CALL_COUNT" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CAPACITY_CALLS must be an integer" >&2
    exit 1
    ;;
esac
case "$PROFILE_ROWS" in
  ''|*[!0-9]*)
    echo "GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS must be an integer" >&2
    exit 1
    ;;
esac
if [ "$CALL_COUNT" -lt 1200 ] || [ "$CALL_COUNT" -gt 5000 ]; then
  echo "GONGCTL_POSTGRES_CAPACITY_CALLS must be between 1200 and 5000" >&2
  exit 1
fi
if [ "$PROFILE_ROWS" -lt 1200 ] || [ "$PROFILE_ROWS" -gt 5000 ]; then
  echo "GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS must be between 1200 and 5000" >&2
  exit 1
fi

if [ -n "${GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR:-}" ]; then
  ARTIFACT_DIR="$GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR"
  case "$ARTIFACT_DIR" in
    /tmp/gongctl-postgres-capacity.*)
      ;;
    *)
      echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must be under /tmp/gongctl-postgres-capacity.*" >&2
      exit 1
      ;;
  esac
  if [ -L "$ARTIFACT_DIR" ]; then
    echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must not be a symlink" >&2
    exit 1
  fi
  mkdir -p "$ARTIFACT_DIR"
else
  ARTIFACT_DIR="$(mktemp -d /tmp/gongctl-postgres-capacity.XXXXXX)"
fi
chmod 700 "$ARTIFACT_DIR"

LOAD_ARTIFACT_DIR="$ARTIFACT_DIR/load-smoke"
CAPACITY_SUMMARY_OUT="$ARTIFACT_DIR/capacity-summary.json"
RUN_OUT="$ARTIFACT_DIR/load-smoke.stdout"
RUN_ERR="$ARTIFACT_DIR/load-smoke.stderr"

rm -rf "$LOAD_ARTIFACT_DIR"
mkdir -p "$LOAD_ARTIFACT_DIR"
chmod 700 "$LOAD_ARTIFACT_DIR"

cd "$ROOT"

start_seconds=$(date +%s)
if ! GONGCTL_POSTGRES_LOAD_COMPOSE_PROJECT="$PROJECT" \
  GONGCTL_POSTGRES_LOAD_PORT="$PORT" \
  GONGCTL_POSTGRES_LOAD_CALLS="$CALL_COUNT" \
  GONGCTL_POSTGRES_LOAD_PROFILE_CACHE_ROWS="$PROFILE_ROWS" \
  GONGCTL_POSTGRES_LOAD_ARTIFACT_DIR="$LOAD_ARTIFACT_DIR" \
  "$LOAD_SMOKE" >"$RUN_OUT" 2>"$RUN_ERR"; then
  echo "postgres capacity drill failed; see $RUN_OUT and $RUN_ERR" >&2
  exit 1
fi
end_seconds=$(date +%s)

python3 - "$ARTIFACT_DIR" "$LOAD_ARTIFACT_DIR" "$CAPACITY_SUMMARY_OUT" "$CALL_COUNT" "$PROFILE_ROWS" "$start_seconds" "$end_seconds" <<'PY'
import json
import re
import sys
from pathlib import Path

artifact_dir = Path(sys.argv[1])
load_dir = Path(sys.argv[2])
summary_out = Path(sys.argv[3])
expected_calls = int(sys.argv[4])
expected_profile_rows = int(sys.argv[5])
start_seconds = int(sys.argv[6])
end_seconds = int(sys.argv[7])

required_files = {
    "load_summary": "summary.json",
    "profile_counts": "profile-cache-counts.txt",
    "profile_backlog": "profile-backlog-sanitized-rows.txt",
    "profile_mcp": "business-pilot-profile-load.jsonl",
    "profile_summary_explain": "explain-profile-lifecycle-summary.json",
    "profile_backlog_explain": "explain-profile-transcript-backlog.json",
    "profile_backlog_dated_explain": "explain-profile-transcript-backlog-dated.json",
    "profile_backlog_index_explain": "explain-profile-transcript-backlog-index-probe.json",
    "transcript_search_explain": "explain-transcript-search.json",
}
paths = {name: load_dir / rel for name, rel in required_files.items()}
missing = [str(path) for path in paths.values() if not path.is_file()]
if missing:
    raise SystemExit(f"missing required load artifacts: {missing}")

load_summary = json.loads(paths["load_summary"].read_text(encoding="utf-8"))
if load_summary.get("status") != "ok":
    raise SystemExit("load summary did not report ok")
if int(load_summary.get("synthetic_calls", -1)) != expected_calls:
    raise SystemExit("load summary synthetic_calls mismatch")
if int(load_summary.get("synthetic_profile_cache_rows", -1)) != expected_profile_rows:
    raise SystemExit("load summary synthetic_profile_cache_rows mismatch")

def parse_counts(path: Path) -> dict[str, int]:
    out = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        if not raw.strip():
            continue
        key, value = raw.split("|", 1)
        out[key] = int(value)
    return out

profile_counts = parse_counts(paths["profile_counts"])
profile_backlog = parse_counts(paths["profile_backlog"])
checks = []

def require(name: str, condition: bool) -> None:
    checks.append({"name": name, "passed": bool(condition)})
    if not condition:
        raise SystemExit(f"capacity drill check failed: {name}")

require("profile_cache_rows_match", profile_counts.get("profile_cache_rows") == expected_profile_rows)
require("direct_helper_cap_rows_1000", profile_counts.get("profile_cache_direct_cap_rows") == 1000)
require("direct_helper_excludes_closed_won_cohort", profile_counts.get("profile_cache_direct_cap_closed_won_rows") == 0)
require("profile_lifecycle_summary_full_count", profile_counts.get("profile_lifecycle_summary_call_count") == expected_profile_rows)
require("profile_backlog_ranked_rows_25", profile_backlog.get("ranked_rows") == 25)
require("profile_backlog_identifier_rows_0", profile_backlog.get("identifier_rows") == 0)
require("profile_backlog_closed_won_rows_25", profile_backlog.get("closed_won_rows") == 25)

for name in (
    "profile_summary_explain",
    "profile_backlog_explain",
    "profile_backlog_dated_explain",
    "profile_backlog_index_explain",
    "transcript_search_explain",
):
    text = paths[name].read_text(encoding="utf-8")
    require(f"{name}_has_plan", '"Plan"' in text)
    require(f"{name}_has_execution_time", '"Execution Time"' in text)
    require(f"{name}_has_actual_rows", '"Actual Rows"' in text)

index_text = paths["profile_backlog_index_explain"].read_text(encoding="utf-8")
require(
    "profile_index_probe_uses_expected_index",
    bool(re.search(r'"Index Name": "(idx_pg_profile_call_fact_cache_started|idx_pg_profile_call_fact_cache_backlog|idx_pg_profile_call_fact_cache_bucket)"', index_text)),
)

profile_mcp_text = paths["profile_mcp"].read_text(encoding="utf-8")
profile_payloads = {}
for raw in profile_mcp_text.splitlines():
    if not raw.strip():
        continue
    message = json.loads(raw)
    request_id = message.get("id")
    if request_id not in {3, 4}:
        continue
    result = message.get("result") or {}
    content = result.get("content") or []
    if not content:
        raise SystemExit(f"profile MCP id {request_id} missing content")
    profile_payloads[request_id] = json.loads(content[0]["text"])

require("profile_mcp_summary_uses_profile_source", profile_payloads.get(3, {}).get("lifecycle_source") == "profile")
require("profile_mcp_backlog_uses_profile_source", profile_payloads.get(4, {}).get("lifecycle_source") == "profile")
require("profile_mcp_redacts_profile_call_ids", "profile-load-call-" not in profile_mcp_text)
require("profile_mcp_redacts_profile_titles", "Synthetic profile load call" not in profile_mcp_text)

secret_values = [
    "postgres://",
    "gongctl_dev_password",
    "gongmcp_reader_dev_password",
    "gongmcp_business_pilot_reader_dev_password",
]
public_text_parts = []
for path in (summary_out, artifact_dir / "load-smoke.stdout", artifact_dir / "load-smoke.stderr"):
    if path.exists():
        public_text_parts.append(path.read_text(encoding="utf-8", errors="replace"))
public_text_parts.append(profile_mcp_text)
public_text = "\n".join(public_text_parts)
for marker in secret_values + [
    "raw_json",
    "crmObjects",
    "profile-load-call-",
    "Synthetic profile load call",
    "This synthetic segment validates",
    "Renewal implementation evidence",
]:
    require(f"public_artifacts_exclude_{marker}", marker not in public_text)

capacity_summary = {
    "status": "ok",
    "artifact_dir": str(artifact_dir),
    "load_artifact_dir": str(load_dir),
    "synthetic_calls": expected_calls,
    "synthetic_profile_cache_rows": expected_profile_rows,
    "started_at_epoch": start_seconds,
    "ended_at_epoch": end_seconds,
    "elapsed_seconds": end_seconds - start_seconds,
    "load_smoke": {
        "bulk_insert_command_seconds": load_summary.get("bulk_insert_command_seconds"),
        "read_model_rebuild_command_seconds": load_summary.get("read_model_rebuild_command_seconds"),
        "decision": load_summary.get("decision"),
    },
    "profile_counts": profile_counts,
    "profile_backlog": profile_backlog,
    "evidence": {name: str(path) for name, path in paths.items()},
    "checks": checks,
    "decision": "Synthetic pre-rollout capacity drill passed at the configured bounded size. This is not production capacity proof; run customer-platform benchmarking and review Postgres settings before broad or high-volume rollout.",
}
tmp = summary_out.with_suffix(".json.tmp")
tmp.write_text(json.dumps(capacity_summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
tmp.replace(summary_out)
PY

grep -q '"status": "ok"' "$CAPACITY_SUMMARY_OUT"
grep -q "\"synthetic_calls\": $CALL_COUNT" "$CAPACITY_SUMMARY_OUT"
grep -q "\"synthetic_profile_cache_rows\": $PROFILE_ROWS" "$CAPACITY_SUMMARY_OUT"

echo "postgres capacity drill passed"
echo "artifact directory: $ARTIFACT_DIR"
echo "capacity summary: $CAPACITY_SUMMARY_OUT"
echo "load smoke artifacts: $LOAD_ARTIFACT_DIR"
