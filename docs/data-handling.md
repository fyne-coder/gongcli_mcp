# Data Handling

## Open Source Boundary

The repository may be public. Customer data must not be public.

Never commit:

- Gong access keys or secrets
- OAuth client secrets or refresh tokens
- Real transcripts
- Real recordings
- Real customer payload fixtures
- Exported JSONL/CSV from customer Gong accounts

## Local Export Policy

Default raw export locations should be outside the source repo. If local testing needs files inside the checkout, use ignored directories such as `exports/` or `transcripts/`.

For the SQLite-backed Agent E flow:

- keep the SQLite `--db` path outside the repo for real customer data
- keep transcript `--out-dir` outside the repo for real customer data
- treat the SQLite file as customer data because it stores raw call/user/transcript payloads and sync history
- keep real tenant profile YAML files outside the repo when they include customer-specific CRM field names, lifecycle terminology, tracker IDs, or scorecard IDs

Batch commands should record operational state only:

- call ID
- output path
- status
- timestamp
- error message, when needed

Do not log transcript text.

`gongctl sync status` and the SQLite search/show commands should stay JSON-safe and metadata-oriented; logs and checkpoints should keep counts, IDs, and paths instead of transcript body content.

Profile-aware unmapped-field summaries must stay redacted by default. Return field names, type, cardinality, population/null rate, and value-length distribution only. Do not include raw example values unless a future command adds explicit per-field opt-in and applies existing redaction rules.

`search_crm_field_values` is the explicit value-lookup exception. By default it redacts call IDs, object IDs, object names, call titles, and value snippets. `include_call_ids=true` may return matching call IDs, and `include_value_snippets=true` may return bounded snippets and call titles for a specific object type, field name, and value query.

MCP `summarize_call_facts` must stay metadata-only. It only accepts safe business grouping dimensions; arbitrary profile field concepts are not MCP group keys because their values may be customer identifiers or other sensitive CRM values.

## Sanitized Fixtures

Fixtures in `testdata/fixtures/` must be synthetic or fully sanitized. Keep them small and purpose-built for parser/client tests.
