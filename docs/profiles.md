# Business Profiles

Business profiles make the SQLite read model portable across Gong tenants with different CRM object names, field names, lifecycle stages, and sales or post-sales methodologies.

## Flow

1. Sync calls with CRM context:

   ```bash
   gongctl sync calls --db ~/gongctl-data/gong.db --from 2026-04-01 --to 2026-04-24 --preset business
   ```

2. Sync the profile inputs before discovery. Calls with CRM context are required; CRM schema and settings are strongly recommended because they improve discovery evidence and validation:

   ```bash
   gongctl sync crm-integrations --db ~/gongctl-data/gong.db
   gongctl sync crm-schema --db ~/gongctl-data/gong.db --integration-id CRM_INTEGRATION_ID --object-type DEAL
   gongctl sync settings --db ~/gongctl-data/gong.db --kind trackers
   gongctl sync settings --db ~/gongctl-data/gong.db --kind scorecards
   ```

3. Discover a starter profile:

   ```bash
   gongctl profile discover --db ~/gongctl-data/gong.db --out ~/gongctl-data/gongctl-profile.yaml
   ```

   Discovery is an English-biased heuristic starter, not a finished tenant profile or a universal sales-process model. It looks for common CRM object/field/stage names and then writes editable YAML.

   The discovered YAML includes evidence values from the local cached CRM data to help humans review mappings. Keep `--out -` stdout output out of shared logs and CI artifacts when using a real customer cache.

4. Review and edit the YAML. Keep it outside git when it describes a real customer tenant. The reviewer should confirm lifecycle buckets, post-sales signals, attribution fields, and methodology concepts before using the profile for sales-vs-post-sales or attribution claims.

5. Validate and import:

   ```bash
   gongctl profile validate --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
   gongctl profile import --db ~/gongctl-data/gong.db --profile ~/gongctl-data/gongctl-profile.yaml
   gongctl profile show --db ~/gongctl-data/gong.db
   ```

6. Use profile-aware CLI or MCP analysis. `auto` uses the active profile when present and falls back to builtin compatibility behavior otherwise.

   ```bash
   gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle --lifecycle-source auto
   gongctl analyze calls --db ~/gongctl-data/gong.db --group-by deal_stage --lifecycle-source profile
   gongctl analyze transcript-backlog --db ~/gongctl-data/gong.db --lifecycle-source profile
   ```

Run `gongctl sync status --db ~/gongctl-data/gong.db` after import to see the profile readiness report. It shows whether a profile is active, whether the profile read model is fresh, concept counts, unavailable concepts, and what blocks reliable tenant-specific lifecycle separation. Use `gongctl profile show`, `list_business_concepts`, and `list_unmapped_crm_fields` when you need the detailed mapped/unmapped field view.

## YAML Shape

```yaml
version: 1
name: Example tenant profile
objects:
  deal:
    object_types: [Deal]
  account:
    object_types: [Company]
fields:
  deal_stage:
    object: deal
    names: [DealStage]
  account_industry:
    object: account
    names: [Industry]
lifecycle:
  open:
    order: 10
    rules:
      - field: deal_stage
        op: in
        values: [Discovery, Proposal]
  closed_won:
    order: 20
    rules:
      - field: deal_stage
        op: equals
        value: Closed Won
  closed_lost:
    order: 30
    rules:
      - field: deal_stage
        op: equals
        value: Closed Lost
  post_sales:
    order: 40
  unknown:
    order: 999
methodology:
  pain:
    description: Pain or business problem evidence.
    aliases: [pain, challenge]
```

The required MCP lifecycle core is `open`, `closed_won`, `closed_lost`, `post_sales`, and `unknown`. A partial profile may import with warnings, but profile-aware tools report unavailable concepts explicitly instead of silently substituting a missing mapping.

## Rule Grammar

Profile rules are evaluated in Go against cached SQLite facts. Supported operators are:

- `equals`
- `in`
- `prefix`
- `iprefix`
- `regex`
- `is_set`
- `is_empty`

Profiles cannot include SQL fragments, templates, JSONPath, JMESPath, or arbitrary expressions. Regex rules are length-limited and compiled during validation.

## Runtime State

Import stores profile metadata, canonical hashes, warnings, object aliases, field concepts, lifecycle rules, and methodology concepts in SQLite. Import runs in one transaction, so MCP sees either the previous active profile or the new active profile, not a partial import.

Profile-aware facts are materialized into a SQLite cache keyed by active profile and canonical hash. Writable CLI sync/profile commands rebuild or warm that cache from cached calls, transcripts, and CRM context. The read-only MCP server requires the cache to be current and returns a stale-cache error instead of writing to SQLite.

MCP is read-only. It can inspect the active profile through `get_business_profile`, list concepts through `list_business_concepts`, and use profile-aware lifecycle and fact tools. Profile creation, validation, and import stay in the CLI.

Unmapped CRM field summaries are redacted by default. They include object type, field name, field type, cardinality, population/null rates, and length distribution, but not raw example values.
