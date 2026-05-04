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
  --image IMAGE          MCP image. Default: ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.2.
  --tool-preset NAME     Named tool preset. Default: business-pilot. Options: business-pilot, operator-smoke, analyst, governance-search, all-readonly.
  --tool-allowlist LIST  Optional comma-separated MCP tool allowlist.
  --compat-expanded-allowlist
                         Emit GONGMCP_TOOL_ALLOWLIST expanded from the Go preset catalog for older images.
  --preset-catalog-bin PATH
                         Explicit gongmcp binary for preset catalog inspection. Defaults to the Docker image.
  --config PATH          Claude config path. Defaults to macOS Claude Desktop config.
  --install              Merge into Claude config with a timestamped backup.
  --print                Print the JSON server entry. Default behavior.
  -h, --help             Show this help.

Examples:
  scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db" --tool-preset business-pilot
  scripts/install-claude-stdio-mcp.sh --db "$HOME/gongctl-data/gong-mcp-governed.db" --tool-preset all-readonly --install
USAGE
}

canonical_existing_file() {
  local path="$1"
  local resolved="$path"
  local hops=0
  while [[ -L "$resolved" ]]; do
    hops=$((hops + 1))
    if [[ "$hops" -gt 40 ]]; then
      return 1
    fi
    local link_dir
    link_dir="$(cd -P "$(dirname "$resolved")" 2>/dev/null && pwd)" || return 1
    local target
    target="$(readlink "$resolved")" || return 1
    case "$target" in
      /*) resolved="$target" ;;
      *) resolved="$link_dir/$target" ;;
    esac
  done
  local dir
  dir="$(cd -P "$(dirname "$resolved")" 2>/dev/null && pwd)" || return 1
  local base
  base="$(basename "$resolved")"
  [[ -f "$dir/$base" ]] || return 1
  printf '%s/%s\n' "$dir" "$base"
}

canonical_existing_dir() {
  cd -P "$1" 2>/dev/null && pwd
}

script_dir() {
  local source="${BASH_SOURCE[0]}"
  while [[ -L "$source" ]]; do
    local dir
    dir="$(cd -P "$(dirname "$source")" && pwd)"
    source="$(readlink "$source")"
    case "$source" in
      /*) ;;
      *) source="$dir/$source" ;;
    esac
  done
  cd -P "$(dirname "$source")" && pwd
}

preset_catalog_json() {
  local image="$1"
  local catalog_bin="$2"
  if [[ -n "$catalog_bin" ]]; then
    if [[ ! -x "$catalog_bin" ]]; then
      echo "--preset-catalog-bin is not executable: $catalog_bin" >&2
      exit 1
    fi
    "$catalog_bin" --list-tool-presets
    return
  fi
  if command -v docker >/dev/null 2>&1; then
    docker run --rm --network none --entrypoint /usr/local/bin/gongmcp "$image" --list-tool-presets
    return
  fi
  echo "docker is required to read the gongmcp preset catalog; pass --preset-catalog-bin PATH for explicit local catalog inspection" >&2
  exit 1
}

preset_tools_from_catalog() {
  local preset="$1"
  local image="$2"
  local catalog_bin="$3"
  local catalog
  catalog="$(preset_catalog_json "$image" "$catalog_bin")"
  if ! jq -e '
    (.presets | type == "array")
    and all(.presets[]; (.name | type == "string") and (.tools | type == "array") and all(.tools[]; type == "string"))
  ' <<<"$catalog" >/dev/null; then
    echo "invalid preset catalog JSON" >&2
    exit 1
  fi
  local tools
  tools="$(
    jq -r --arg preset "$preset" '
      def norm: ascii_downcase;
      (.presets // [])
      | map(select((.name | norm) == ($preset | norm) or ((.aliases // []) | map(norm) | index($preset | norm))))
      | if length == 0 then "" else (.[0].tools | join(",")) end
    ' <<<"$catalog"
  )"
  if [[ -z "$tools" ]]; then
    local available
    available="$(
      jq -r '
        (.presets // [])
        | map([.name] + (.aliases // []))
        | flatten
        | join(", ")
      ' <<<"$catalog"
    )"
    echo "unknown tool preset: $preset" >&2
    echo "available presets: $available" >&2
    exit 2
  fi
  printf '%s\n' "$tools"
}

DB_PATH=""
DATA_DIR=""
SERVER_NAME="gong"
IMAGE="ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.2"
TOOL_PRESET="business-pilot"
TOOL_PRESET_SET=0
TOOL_ALLOWLIST=""
TOOL_ALLOWLIST_SET=0
COMPAT_EXPANDED_ALLOWLIST=0
PRESET_CATALOG_BIN=""
CONFIG_PATH="${HOME}/Library/Application Support/Claude/claude_desktop_config.json"
INSTALL=0
SCRIPT_DIR="$(script_dir)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

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
      TOOL_PRESET_SET=1
      shift 2
      ;;
    --tool-allowlist)
      TOOL_ALLOWLIST="${2:?--tool-allowlist requires a comma-separated list}"
      TOOL_ALLOWLIST_SET=1
      shift 2
      ;;
    --compat-expanded-allowlist)
      COMPAT_EXPANDED_ALLOWLIST=1
      shift
      ;;
    --preset-catalog-bin)
      PRESET_CATALOG_BIN="${2:?--preset-catalog-bin requires a path}"
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

if [[ "$TOOL_PRESET_SET" -eq 1 && "$TOOL_ALLOWLIST_SET" -eq 1 ]]; then
  echo "--tool-preset and --tool-allowlist are mutually exclusive" >&2
  exit 2
fi
if [[ "$COMPAT_EXPANDED_ALLOWLIST" -eq 1 && "$TOOL_ALLOWLIST_SET" -eq 1 ]]; then
  echo "--compat-expanded-allowlist and --tool-allowlist are mutually exclusive" >&2
  exit 2
fi

TOOL_ENV_NAME=""
TOOL_ENV_VALUE=""
if [[ "$TOOL_ALLOWLIST_SET" -eq 1 ]]; then
  TOOL_ENV_NAME="GONGMCP_TOOL_ALLOWLIST"
  TOOL_ENV_VALUE="$TOOL_ALLOWLIST"
elif [[ -n "$TOOL_PRESET" ]]; then
  PRESET_ALLOWLIST="$(preset_tools_from_catalog "$TOOL_PRESET" "$IMAGE" "$PRESET_CATALOG_BIN")"
  if [[ "$COMPAT_EXPANDED_ALLOWLIST" -eq 1 ]]; then
    TOOL_ENV_NAME="GONGMCP_TOOL_ALLOWLIST"
    TOOL_ENV_VALUE="$PRESET_ALLOWLIST"
  else
    TOOL_ENV_NAME="GONGMCP_TOOL_PRESET"
    TOOL_ENV_VALUE="$TOOL_PRESET"
  fi
fi

if ! DB_PATH="$(canonical_existing_file "$DB_PATH")"; then
  echo "database does not exist: $DB_PATH" >&2
  exit 1
fi

if [[ -z "$DATA_DIR" ]]; then
  DATA_DIR="$(dirname "$DB_PATH")"
else
  if ! DATA_DIR="$(canonical_existing_dir "$DATA_DIR")"; then
    echo "data directory does not exist: $DATA_DIR" >&2
    exit 1
  fi
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
case "$DB_RELATIVE" in
  ""|..|../*|*/../*|/*)
    echo "database relative path is invalid: $DB_RELATIVE" >&2
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
    --arg toolEnvName "$TOOL_ENV_NAME" \
    --arg toolEnvValue "$TOOL_ENV_VALUE" '
      [
        "run", "--rm", "-i",
        "--network", "none",
        "-v", $mount
      ]
      + (if $toolEnvName == "" then [] else ["-e", ($toolEnvName + "=" + $toolEnvValue)] end)
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
