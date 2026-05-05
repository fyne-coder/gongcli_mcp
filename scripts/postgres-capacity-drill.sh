#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOAD_SMOKE="$ROOT/scripts/postgres-load-smoke.sh"

PROJECT="${GONGCTL_POSTGRES_CAPACITY_COMPOSE_PROJECT:-gongctl-postgres-capacity-drill-$$}"
PORT="${GONGCTL_POSTGRES_CAPACITY_PORT:-55445}"
CALL_COUNT="${GONGCTL_POSTGRES_CAPACITY_CALLS:-5000}"
PROFILE_ROWS="${GONGCTL_POSTGRES_CAPACITY_PROFILE_ROWS:-$CALL_COUNT}"
export GONGCTL_POSTGRES_PASSWORD="${GONGCTL_POSTGRES_PASSWORD:-gongctl_dev_password}"
export GONGMCP_READER_PASSWORD="${GONGMCP_READER_PASSWORD:-gongmcp_reader_dev_password}"
export GONGMCP_BUSINESS_PILOT_READER_PASSWORD="${GONGMCP_BUSINESS_PILOT_READER_PASSWORD:-gongmcp_business_pilot_reader_dev_password}"

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
if ! python3 - "$PORT" <<'PY'
import socket
import sys

port = int(sys.argv[1])
sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
try:
    sock.bind(("127.0.0.1", port))
finally:
    sock.close()
PY
then
  echo "GONGCTL_POSTGRES_CAPACITY_PORT is already in use on 127.0.0.1: $PORT" >&2
  exit 1
fi

if [ -n "${GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR:-}" ]; then
  ARTIFACT_DIR="$GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR"
  case "$ARTIFACT_DIR" in
    *..*)
      echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must not contain .. path components" >&2
      exit 1
      ;;
    /tmp/gongctl-postgres-capacity.*)
      ;;
    *)
      echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must be under /tmp/gongctl-postgres-capacity.*" >&2
      exit 1
      ;;
  esac
  if [ "$(dirname "$ARTIFACT_DIR")" != "/tmp" ]; then
    echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must be one directory directly under /tmp" >&2
    exit 1
  fi
  if [ -L "$ARTIFACT_DIR" ]; then
    echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR must not be a symlink" >&2
    exit 1
  fi
  if [ -e "$ARTIFACT_DIR" ] && [ ! -d "$ARTIFACT_DIR" ]; then
    echo "GONGCTL_POSTGRES_CAPACITY_ARTIFACT_DIR exists and is not a directory" >&2
    exit 1
  fi
  mkdir "$ARTIFACT_DIR" 2>/dev/null || [ -d "$ARTIFACT_DIR" ]
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
import hashlib
import os
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
archive_paths = {
    name: path for name, path in paths.items()
    if name != "load_summary"
}

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

def walk_plan_nodes(plan: dict):
    yield plan
    for child in plan.get("Plans") or []:
        yield from walk_plan_nodes(child)

def load_explain_plan(name: str) -> dict:
    raw = json.loads(paths[name].read_text(encoding="utf-8"))
    require(f"{name}_json_array", isinstance(raw, list) and len(raw) > 0)
    first = raw[0]
    require(f"{name}_has_root_plan", isinstance(first, dict) and isinstance(first.get("Plan"), dict))
    require(f"{name}_has_execution_time", isinstance(first.get("Execution Time"), (int, float)))
    plan = first["Plan"]
    nodes = list(walk_plan_nodes(plan))
    require(f"{name}_has_actual_rows", any(isinstance(node.get("Actual Rows"), (int, float)) for node in nodes))
    require(f"{name}_has_actual_time", any(isinstance(node.get("Actual Total Time"), (int, float)) for node in nodes))
    return plan

plans = {
    name: load_explain_plan(name)
    for name in (
        "profile_summary_explain",
        "profile_backlog_explain",
        "profile_backlog_dated_explain",
        "profile_backlog_index_explain",
        "transcript_search_explain",
    )
}

expected_index_names = {
    "idx_pg_profile_call_fact_cache_started",
    "idx_pg_profile_call_fact_cache_backlog",
    "idx_pg_profile_call_fact_cache_bucket",
}
index_names = {
    node.get("Index Name")
    for node in walk_plan_nodes(plans["profile_backlog_index_explain"])
    if node.get("Index Name")
}
require("profile_index_probe_uses_expected_index", bool(index_names & expected_index_names))

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

forbidden_public_markers = [
    ("url_scheme", "postgres://"),
    ("writer_secret", "gongctl_dev_password"),
    ("reader_secret", "gongmcp_reader_dev_password"),
    ("pilot_secret", "gongmcp_business_pilot_reader_dev_password"),
    ("raw_blob_key", "raw_json"),
    ("crm_blob_key", "crmObjects"),
    ("profile_id_pattern", "profile-load-call-"),
    ("profile_title_pattern", "Synthetic profile load call"),
    ("transcript_body_phrase", "This synthetic segment validates"),
    ("crm_context_phrase", "Renewal implementation evidence"),
]
for env_name in (
    "GONGCTL_POSTGRES_PASSWORD",
    "GONGMCP_READER_PASSWORD",
    "GONGMCP_BUSINESS_PILOT_READER_PASSWORD",
):
    value = os.environ.get(env_name, "")
    if value:
        forbidden_public_markers.append((env_name.lower(), value))
def evidence_record(path: Path) -> dict:
    data = path.read_bytes()
    return {
        "path": str(path.relative_to(artifact_dir)),
        "bytes": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
    }

capacity_summary = {
    "status": "ok",
    "artifact_dir": ".",
    "load_artifact_dir": "load-smoke",
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
    "evidence": {name: str(path.relative_to(artifact_dir)) for name, path in archive_paths.items()},
    "evidence_files": {name: evidence_record(path) for name, path in archive_paths.items()},
    "checks": checks,
    "decision": "Synthetic pre-rollout capacity drill passed at the configured bounded size. This is not production capacity proof; run customer-platform benchmarking and review Postgres settings before broad or high-volume rollout.",
}

def serialize_summary() -> str:
    return json.dumps(capacity_summary, indent=2, sort_keys=True) + "\n"

public_text_parts = [serialize_summary()]
for path in (artifact_dir / "load-smoke.stdout", artifact_dir / "load-smoke.stderr", *archive_paths.values()):
    public_text_parts.append(path.read_text(encoding="utf-8", errors="replace"))
public_text = "\n".join(public_text_parts)
for label, marker in forbidden_public_markers:
    require(f"public_artifacts_exclude_{label}", marker not in public_text)

capacity_summary["checks"] = checks
summary_text = serialize_summary()
for label, marker in forbidden_public_markers:
    if marker in summary_text:
        raise SystemExit(f"capacity drill summary contains forbidden marker: {label}")
tmp = summary_out.with_suffix(".json.tmp")
tmp.write_text(summary_text, encoding="utf-8")
tmp.replace(summary_out)
PY

grep -q '"status": "ok"' "$CAPACITY_SUMMARY_OUT"
grep -q "\"synthetic_calls\": $CALL_COUNT" "$CAPACITY_SUMMARY_OUT"
grep -q "\"synthetic_profile_cache_rows\": $PROFILE_ROWS" "$CAPACITY_SUMMARY_OUT"

echo "postgres capacity drill passed"
echo "artifact directory: $ARTIFACT_DIR"
echo "capacity summary: $CAPACITY_SUMMARY_OUT"
echo "load smoke artifacts: $LOAD_ARTIFACT_DIR"
