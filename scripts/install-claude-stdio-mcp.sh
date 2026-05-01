#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Install or print a Claude Desktop stdio MCP config entry for gongmcp.

Usage:
  scripts/install-claude-stdio-mcp.sh --db /absolute/path/to/gong.db [options]

Options:
  --db PATH              Host SQLite DB path. Required.
  --data-dir PATH        Host directory to mount read-only. Defaults to DB directory.
  --server-name NAME     Claude MCP server name. Default: gong.
  --image IMAGE          MCP image. Default: ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0.
  --tool-preset NAME     Optional named tool preset: business-pilot, operator-smoke, analyst, governance-search, all-readonly.
  --tool-allowlist LIST  Optional comma-separated MCP tool allowlist.
  --config PATH          Claude config path. Defaults to macOS Claude Desktop config.
  --install              Merge into Claude config with a timestamped backup.
  --print                Print the JSON server entry. Default behavior.
  -h, --help             Show this help.

Examples:
  scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db" --tool-preset business-pilot
  scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db" --tool-preset all-readonly --install
USAGE
}

abs_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$PWD" "$1" ;;
  esac
}

preset_allowlist() {
  case "$1" in
    business-pilot|strict-business-pilot)
      printf '%s\n' 'get_sync_status,summarize_call_facts,summarize_calls_by_lifecycle,rank_transcript_backlog'
      ;;
    operator-smoke)
      printf '%s\n' 'get_sync_status,search_calls,search_transcript_segments,rank_transcript_backlog'
      ;;
    analyst|analyst-expansion)
      printf '%s\n' 'get_sync_status,list_crm_object_types,list_crm_fields,get_business_profile,list_business_concepts,list_unmapped_crm_fields,analyze_late_stage_crm_signals,opportunities_missing_transcripts,search_transcripts_by_crm_context,opportunity_call_summary,crm_field_population_matrix,list_lifecycle_buckets,summarize_calls_by_lifecycle,prioritize_transcripts_by_lifecycle,compare_lifecycle_crm_fields,summarize_call_facts,rank_transcript_backlog,search_transcript_segments,search_transcripts_by_call_facts,search_transcript_quotes_with_attribution'
      ;;
    governance-search)
      printf '%s\n' 'search_calls,get_call,search_transcripts_by_crm_context,search_calls_by_lifecycle,prioritize_transcripts_by_lifecycle,rank_transcript_backlog,search_transcript_segments,search_transcripts_by_call_facts,search_transcript_quotes_with_attribution,missing_transcripts'
      ;;
    all-readonly|all-tools|all)
      printf '%s\n' 'get_sync_status,search_calls,get_call,list_crm_object_types,list_crm_fields,list_crm_integrations,list_cached_crm_schema_objects,list_cached_crm_schema_fields,list_gong_settings,list_scorecards,get_scorecard,get_business_profile,list_business_concepts,list_unmapped_crm_fields,search_crm_field_values,analyze_late_stage_crm_signals,opportunities_missing_transcripts,search_transcripts_by_crm_context,opportunity_call_summary,crm_field_population_matrix,list_lifecycle_buckets,summarize_calls_by_lifecycle,search_calls_by_lifecycle,prioritize_transcripts_by_lifecycle,compare_lifecycle_crm_fields,summarize_call_facts,rank_transcript_backlog,search_transcript_segments,search_transcripts_by_call_facts,search_transcript_quotes_with_attribution,missing_transcripts'
      ;;
    *)
      echo "unknown tool preset: $1" >&2
      exit 2
      ;;
  esac
}

DB_PATH=""
DATA_DIR=""
SERVER_NAME="gong"
IMAGE="ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.2.0"
TOOL_PRESET=""
TOOL_ALLOWLIST=""
CONFIG_PATH="${HOME}/Library/Application Support/Claude/claude_desktop_config.json"
INSTALL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --db)
      DB_PATH="${2:?--db requires a path}"
      shift 2
      ;;
    --data-dir)
      DATA_DIR="${2:?--data-dir requires a path}"
      shift 2
      ;;
    --server-name)
      SERVER_NAME="${2:?--server-name requires a value}"
      shift 2
      ;;
    --image)
      IMAGE="${2:?--image requires a value}"
      shift 2
      ;;
    --tool-preset)
      TOOL_PRESET="${2:?--tool-preset requires a preset name}"
      shift 2
      ;;
    --tool-allowlist)
      TOOL_ALLOWLIST="${2:?--tool-allowlist requires a comma-separated list}"
      shift 2
      ;;
    --config)
      CONFIG_PATH="${2:?--config requires a path}"
      shift 2
      ;;
    --install)
      INSTALL=1
      shift
      ;;
    --print)
      INSTALL=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$DB_PATH" ]]; then
  echo "--db is required" >&2
  usage >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required to generate and merge Claude Desktop JSON" >&2
  exit 1
fi

if [[ -n "$TOOL_PRESET" && -n "$TOOL_ALLOWLIST" ]]; then
  echo "--tool-preset and --tool-allowlist are mutually exclusive" >&2
  exit 2
fi
if [[ -n "$TOOL_PRESET" ]]; then
  # Emit the expanded allowlist so generated Claude configs stay safe with
  # older pinned images that do not yet understand GONGMCP_TOOL_PRESET.
  TOOL_ALLOWLIST="$(preset_allowlist "$TOOL_PRESET")"
fi

DB_PATH="$(abs_path "$DB_PATH")"
if [[ ! -f "$DB_PATH" ]]; then
  echo "database does not exist: $DB_PATH" >&2
  exit 1
fi

if [[ -z "$DATA_DIR" ]]; then
  DATA_DIR="$(dirname "$DB_PATH")"
else
  DATA_DIR="$(abs_path "$DATA_DIR")"
fi

case "$DB_PATH" in
  "$DATA_DIR"/*) DB_RELATIVE="${DB_PATH#"$DATA_DIR"/}" ;;
  *)
    echo "database must be inside --data-dir" >&2
    echo "db:       $DB_PATH" >&2
    echo "data-dir: $DATA_DIR" >&2
    exit 1
    ;;
esac

CONTAINER_DB="/data/${DB_RELATIVE}"
MOUNT_ARG="${DATA_DIR}:/data:ro"

ENTRY_JSON="$(
  jq -n \
    --arg mount "$MOUNT_ARG" \
    --arg image "$IMAGE" \
    --arg db "$CONTAINER_DB" \
    --arg allowlist "$TOOL_ALLOWLIST" '
      [
        "run", "--rm", "-i",
        "--network", "none",
        "-v", $mount
      ]
      + (if $allowlist == "" then [] else ["-e", ("GONGMCP_TOOL_ALLOWLIST=" + $allowlist)] end)
      + [
        $image,
        "--db", $db
      ]
      | {
          command: "docker",
          args: .
        }
    '
)"

if [[ "$INSTALL" -eq 0 ]]; then
  jq -n --arg name "$SERVER_NAME" --argjson entry "$ENTRY_JSON" \
    '{mcpServers: {($name): $entry}}'
  exit 0
fi

mkdir -p "$(dirname "$CONFIG_PATH")"
if [[ ! -f "$CONFIG_PATH" ]]; then
  printf '{"mcpServers":{}}\n' > "$CONFIG_PATH"
fi

BACKUP_PATH="${CONFIG_PATH}.backup-$(date +%Y%m%d-%H%M%S)-gongmcp"
cp "$CONFIG_PATH" "$BACKUP_PATH"

TMP_PATH="${CONFIG_PATH}.tmp.$$"
jq --arg name "$SERVER_NAME" --argjson entry "$ENTRY_JSON" '
  .mcpServers = (.mcpServers // {}) |
  .mcpServers[$name] = $entry
' "$CONFIG_PATH" > "$TMP_PATH"
mv "$TMP_PATH" "$CONFIG_PATH"

echo "installed Claude Desktop MCP server: $SERVER_NAME"
echo "config: $CONFIG_PATH"
echo "backup: $BACKUP_PATH"
echo "restart Claude Desktop to load the server"
