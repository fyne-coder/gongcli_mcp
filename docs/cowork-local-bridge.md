# Claude Cowork Local Bridge (`gongcowork`)

Local stdio MCP companion for deterministic Gong Quarterly Review workflow
operations. This is separate from the remote Web Custom Gong connector used for
Gong reads.

## What it does

`gongcowork` exposes exactly one tool, `gong_workflow`, with operations:

- `preflight`
- `persist_response`
- `validate_item`
- `finalize_run`
- `get_run_status`

It loads a frozen contract at startup, rejects any path that escapes the approved
root (including via symlinks). Symlinks that resolve inside the root are followed
at contract load; runtime writes and presence gates use `os.Root` so a symlink
planted after startup cannot redirect a write or satisfy a gate outside the root.
Response JSON is written with exclusive-create semantics, and only fixed
`gong_quarterly_review` Python modules run through `exec.CommandContext` (no shell).

It does not need a Gong database or credentials.

## Build

```bash
go build -o bin/gongcowork ./cmd/gongcowork
```

## Frozen contract

The contract is the trust anchor. Keep it **outside** any Cowork/model-writable
directory (ideally outside `approved_project_root`), owned by the operator, and
preferably read-only (`chmod 444`). Claude Desktop config argv points at this
path; an in-root contract could be rewritten by the model between restarts.

Create an absolute-path contract JSON:

```json
{
  "schema_version": "1.0",
  "approved_project_root": "/absolute/path/to/Gong Quarterly Review",
  "python_interpreter": ".venv-host/bin/python",
  "run_root": "evidence/slice4d/rehearsal-run",
  "quarter_root": "evidence/slice4d/rehearsal-run/<quarter>",
  "readiness_target_dir": "evidence/slice4d",
  "readiness_scratch_root": "evidence/slice4d/.local-bridge-scratch",
  "finalization_result_path": "evidence/slice4d/rehearsal-run/finalization-result.json",
  "completion_marker_paths": [
    "evidence/slice4d/rehearsal-run/<quarter>/markers/capture-complete.marker.json"
  ],
  "completion_pin_path": "evidence/slice4d/rehearsal-run/completion.pin.json",
  "items": [
    {
      "item_id": "item-1",
      "raw_response_path": "evidence/slice4d/rehearsal-run/haiku-outbox/item-1.response.json",
      "staged_input_path": "evidence/slice4d/rehearsal-run/haiku-outbox/item-1.staged-input.json"
    }
  ]
}
```

All child paths are project-relative and must stay under `approved_project_root`.
`completion_marker_paths` (at least one) and `completion_pin_path` are required
and checked before
`finalize_run`; if any exist, finalization is refused without invoking the
module. The Python verifier remains the authoritative one-time guard for marker
and pin issuance.

### Verifier verdict rule

`validate_item` / previous-item gating parse `verify_ordering_rehearsal` stdout
JSON (bounded). Acceptance requires `ok:true`, `ordering_journal.ok:true` when
that object is present, and the item absent from `pending_item_ids`. Top-level
`ok:true` alone is never sufficient (`ok:true` can coexist with
`stage:"pending-items"`).

### Size limits

`gongcowork` uses a 4 MiB MCP frame cap (`WithMaxFrameBytes`). The contract
response cap is 3 MiB so the tool layer binds first. Default `gongmcp` keeps the
1 MiB frame cap. An oversized stdio Content-Length is discarded, answered with a
JSON-RPC parse error, and the server continues serving.

## Print Claude Desktop config (dry-run)

**Prerequisite:** `jq` must be installed (not stock on macOS). Install via
Homebrew (`brew install jq`) or another package manager before running the
script.

```bash
bash scripts/install-claude-cowork-bridge.sh \
  --contract /absolute/path/to/contract.json \
  --binary /absolute/path/to/bin/gongcowork \
  --print
```

Default mode is `--print`. `--install` is reserved/refused in this slice and
does not mutate Claude configuration.

## Operator notes

1. Keep the remote Gong connector for Gong evidence reads.
2. Use `gongcowork` only for local readiness, persistence, validation, and
   one-time finalization.
3. Do not pass shell commands, module names, interpreters, environment
   variables, or absolute output paths through the tool. The contract freezes
   those values.
4. `persist_response` fails closed on duplicates and stops the receipt →
   adapter → stage chain on the first failure.
5. Actual Claude Desktop installation remains a separate approved step after
   local synthetic proof.
