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

It loads a frozen contract at startup, rejects path escapes and symlinks, writes
response JSON with exclusive-create semantics, and runs only fixed
`gong_quarterly_review` Python modules through `exec.CommandContext` (no shell).

It does not need a Gong database or credentials.

## Build

```bash
go build -o bin/gongcowork ./cmd/gongcowork
```

## Frozen contract

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

## Print Claude Desktop config (dry-run)

```bash
bash scripts/install-claude-cowork-bridge.sh \
  --contract /absolute/path/to/contract.json \
  --binary /absolute/path/to/bin/gongcowork \
  --print
```

Default mode is `--print`. `--install` is intentionally blocked in this slice
and reserved for a later operator-approved installation.

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
