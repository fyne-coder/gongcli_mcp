# Claude Cowork Local Bridge (`gongcowork`)

Local stdio MCP companion for deterministic Gong Quarterly Review workflow
operations. This is separate from the remote Web Custom Gong connector used for
Gong reads.

`gongcowork` supports exactly one mutually exclusive startup mode at a time:

- `--contract` — capture workflow tool `gong_workflow`
- `--selection-contract` — candidate-selection tool `gong_candidate_selection`

It does not add a general Gong facade (tracked separately as GitHub issue
[#36](https://github.com/fyne-coder/gongcli_mcp/issues/36)) and has no direct
Gong/Postgres access.

## What it does

### Capture mode (`--contract`)

Exposes exactly one tool, `gong_workflow`, with operations:

- `preflight`
- `persist_preflight_response`
- `issue_pre_drilldown_gate`
- `check_freshness` (requires optional `segment_config_path`)
- `persist_response`
- `validate_item`
- `finalize_run`
- `get_run_status`

### Selection mode (`--selection-contract`)

Exposes exactly one tool, `gong_candidate_selection`, with operations:

- `preflight`
- `persist_discovery_response` (exact JSON `response` on module stdin)
- `get_next_query`
- `persist_query_response` (`query_id` + exact JSON `response` as a stdin envelope)
- `finalize_selection`
- `get_selection_status`

Both modes load a frozen contract at startup, reject any path that escapes the
approved root (including via symlinks), pin config bytes and the interpreter
symlink target before each module call, and run only fixed
`gong_quarterly_review` Python modules through `exec.CommandContext` (no shell).
Selection persistence never puts response bodies on argv and never writes raw
temporary body files from the Go bridge.

It does not need a Gong database or credentials.

## Build

```bash
go build -o bin/gongcowork ./cmd/gongcowork
```

## Frozen capture contract

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
  "status_response_path": "evidence/slice4d/rehearsal-run/<quarter>/preflight/gong-status-response.json",
  "capabilities_response_path": "evidence/slice4d/rehearsal-run/<quarter>/preflight/gong-discover-capabilities-response.json",
  "pre_drilldown_gate_path": "evidence/slice4d/rehearsal-run/<quarter>/pre-drilldown-gate.json",
  "quarter_id": "2026-q2",
  "version": "v1",
  "segment_id": "segment-clean-ordering-rehearsal",
  "contract_model_id": "claude-haiku-4-5-20251001",
  "cowork_ui_display_name": "Claude Haiku 4.5",
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
The two preflight response paths and fixed `pre-drilldown-gate.json` path must
also stay under `quarter_root` and cannot collide with any other output. The
bridge enforces status → capabilities → gate before it will write item 1. Gate
issuance runs only `gong_quarterly_review.preflight_gate_cli`; that module
derives MCP version, preset, readiness, and advertised operations from the two
saved response files instead of accepting caller-supplied observations.
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

## Frozen selection contract

Selection mode uses a distinct contract with only these fields:

```json
{
  "schema_version": "1.0",
  "approved_project_root": "/absolute/path/to/Gong Quarterly Review",
  "python_interpreter": ".venv-host/bin/python",
  "selection_config_path": "evidence/slice6/selection/config.json",
  "selection_state_path": "evidence/slice6/selection/state.json",
  "selection_output_path": "evidence/slice6/selection/output.json",
  "readiness_target_dir": "evidence/slice6",
  "readiness_scratch_root": "evidence/slice6/.local-bridge-scratch",
  "contract_model_id": "claude-haiku-4-5-20251001",
  "cowork_ui_display_name": "Claude Haiku 4.5"
}
```

`selection_config_path` must already exist as a regular file and is SHA-256
pinned at startup. State/output/config/readiness/interpreter paths must not
collide. Every module call rechecks config bytes and the interpreter symlink
target, then runs only
`gong_quarterly_review.candidate_selection_workflow` with the frozen
`--config`/`--state`/`--output` paths. Go does not accept caller paths, module
names, environment, filters, theme terms, or query arguments. Candidate/domain
logic stays in Python.

Start selection mode with:

```bash
./bin/gongcowork --selection-contract /absolute/path/to/selection-contract.json
```

## Print Claude Desktop config (dry-run)

**Prerequisite:** `jq` must be installed (not stock on macOS). Install via
Homebrew (`brew install jq`) or another package manager before running the
script.

Capture mode:

```bash
bash scripts/install-claude-cowork-bridge.sh \
  --contract /absolute/path/to/contract.json \
  --binary /absolute/path/to/bin/gongcowork \
  --print
```

Selection mode:

```bash
bash scripts/install-claude-cowork-bridge.sh \
  --selection-contract /absolute/path/to/selection-contract.json \
  --binary /absolute/path/to/bin/gongcowork \
  --print
```

Default mode is `--print`. `--install` is reserved/refused in this slice and
does not mutate Claude configuration. `--contract` and `--selection-contract`
are mutually exclusive.

## Operator notes

1. Keep the remote Gong connector for Gong evidence reads.
2. Use capture-mode `gongcowork` for local readiness, persistence, validation,
   and one-time finalization.
3. Use selection-mode `gongcowork` only for the bounded candidate-index
   selection sequence; do not treat it as a general Gong facade (#36).
4. Do not pass shell commands, module names, interpreters, environment
   variables, or absolute output paths through the tool. The contract freezes
   those values.
5. Persist status and capabilities separately, issue the pre-drilldown gate,
   and proceed only when the local tool returns `ok:true` and the gate exists.
6. `persist_response` fails closed on duplicates and stops the receipt →
   adapter → stage chain on the first failure.
7. Selection persistence forwards JSON on stdin only; module failure returns an
   MCP error and does not advance.
8. Actual Claude Desktop installation remains a separate approved step after
   local synthetic proof.
