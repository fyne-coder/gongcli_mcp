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

## AI And Subprocessor Review

Treat transcript text, speaker identifiers, call titles, participant metadata,
CRM context, scorecard answers, tracker hits, and bounded snippets as customer
data. Redaction, aliases, and pseudonymized speaker references reduce exposure
but do not make the data anonymous by default.

Before using `gongctl` outputs with an AI provider or hosted model workflow
such as OpenAI, Anthropic, a cloud MCP host, or an internal AI gateway, confirm
the company's approved operating model:

- the customer or company has authorized this category of transcript and CRM
  processing
- the AI provider is covered by the relevant DPA, vendor review, or
  subprocessor approval path
- the agreement covers the actual data flow: prompts, tool results, logs,
  traces, files, support bundles, and any stored conversation history
- retention, training-use, regional processing, access logging, incident
  response, and deletion terms match the company's policy
- support and debugging workflows use sanitized bundles by default, with
  time-limited audited access for raw customer data only when approved

For processor/subprocessor review, separate the business customer/controller
from end prospects or call participants. The usual approval question is whether
the business customer has authorized the vendor and downstream AI recipients for
this processing path; do not assume every end participant receives a separate
tool-specific notice from this project.

If the company hosts, operates, debugs, or sends transcript/CRM/prompt data
through its own accounts, it may be acting as a processor or vendor for the
customer. If the workflow stays inside the customer's controlled environment
and approved AI accounts, the compliance and subprocessor boundary is usually
cleaner, but the data still needs the same minimization and retention controls.

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
