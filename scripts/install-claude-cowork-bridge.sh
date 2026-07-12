#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Print or install a Claude Desktop stdio MCP entry for gongcowork.

Default behavior is --print (dry-run). This slice must not mutate Claude
configuration unless an operator later passes explicit --install.

Usage:
  scripts/install-claude-cowork-bridge.sh --contract /abs/contract.json --binary /abs/gongcowork [options]
  scripts/install-claude-cowork-bridge.sh --selection-contract /abs/selection-contract.json --binary /abs/gongcowork [options]

Options:
  --contract PATH   Absolute frozen capture contract JSON path.
  --selection-contract PATH
                    Absolute frozen candidate-selection contract JSON path.
                    Mutually exclusive with --contract.
  --binary PATH     Absolute gongcowork binary path. Required.
  --server-name NAME  Claude MCP server name. Default: gongcowork.
  --config PATH     Claude config path. Defaults to macOS Claude Desktop config.
  --install         Reserved/refused in this slice; does not mutate Claude config.
  --print           Print the JSON server entry. Default behavior.
  -h, --help        Show this help.

Prerequisite:
  jq must be installed (not stock on macOS).

Examples:
  scripts/install-claude-cowork-bridge.sh \
    --contract /absolute/example/contract.json \
    --binary /absolute/example/gongcowork \
    --print

  scripts/install-claude-cowork-bridge.sh \
    --selection-contract /absolute/example/selection-contract.json \
    --binary /absolute/example/gongcowork \
    --print
USAGE
}

abs_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *)
      echo "path must be absolute: $1" >&2
      exit 2
      ;;
  esac
}

CONTRACT_PATH=""
SELECTION_CONTRACT_PATH=""
BINARY_PATH=""
SERVER_NAME="gongcowork"
CONFIG_PATH="${HOME}/Library/Application Support/Claude/claude_desktop_config.json"
INSTALL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --contract)
      CONTRACT_PATH="${2:?--contract requires a path}"
      shift 2
      ;;
    --selection-contract)
      SELECTION_CONTRACT_PATH="${2:?--selection-contract requires a path}"
      shift 2
      ;;
    --binary)
      BINARY_PATH="${2:?--binary requires a path}"
      shift 2
      ;;
    --server-name)
      SERVER_NAME="${2:?--server-name requires a value}"
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

if [[ -z "$BINARY_PATH" ]]; then
  echo "--binary is required" >&2
  usage >&2
  exit 2
fi

if [[ -n "$CONTRACT_PATH" && -n "$SELECTION_CONTRACT_PATH" ]]; then
  echo "--contract and --selection-contract are mutually exclusive" >&2
  usage >&2
  exit 2
fi

if [[ -z "$CONTRACT_PATH" && -z "$SELECTION_CONTRACT_PATH" ]]; then
  echo "exactly one of --contract or --selection-contract is required" >&2
  usage >&2
  exit 2
fi

if [[ "$INSTALL" -ne 0 ]]; then
  echo "--install is reserved for a later operator-approved slice; refusing to mutate Claude config in this dry-run companion build." >&2
  echo "Re-run with --print to emit the entry, then install only after explicit operator approval." >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required to generate Claude Desktop JSON (not stock on macOS; install via brew install jq)" >&2
  exit 1
fi

BINARY_PATH="$(abs_path "$BINARY_PATH")"

if [[ -n "$CONTRACT_PATH" ]]; then
  CONTRACT_PATH="$(abs_path "$CONTRACT_PATH")"
  ENTRY_JSON="$(
    jq -n \
      --arg command "$BINARY_PATH" \
      --arg contract "$CONTRACT_PATH" \
      '{
        command: $command,
        args: ["--contract", $contract]
      }'
  )"
else
  SELECTION_CONTRACT_PATH="$(abs_path "$SELECTION_CONTRACT_PATH")"
  ENTRY_JSON="$(
    jq -n \
      --arg command "$BINARY_PATH" \
      --arg contract "$SELECTION_CONTRACT_PATH" \
      '{
        command: $command,
        args: ["--selection-contract", $contract]
      }'
  )"
fi

jq -n --arg name "$SERVER_NAME" --argjson entry "$ENTRY_JSON" '{mcpServers: {($name): $entry}}'
exit 0
