# Business Profiles

Business profiles make the SQLite read model portable across Gong tenants with different CRM object names, field names, lifecycle stages, and sales or post-sales methodologies.

Profiles are optional for ingestion and basic MCP use. Without one, `gongctl`
still syncs calls, transcripts, users, settings, and CRM context; transcript
search, sync status, basic summaries, and generic CRM inspection still work. The
tradeoff is interpretation quality: lifecycle separation, sales-vs-post-sales
splits, attribution readiness, and tenant-specific methodology concepts use
builtin compatibility behavior or report partial/unavailable readiness until a
reviewed profile is imported.

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

4. Review and edit the YAML. Keep it outside git when it describes a real customer tenant. The reviewer should confirm lifecycle buckets, post-sales signals, attribution fields, and methodology concepts before using the profile for sales-vs-post-sales or attribution claims. A synthetic, import-shape example lives at [docs/examples/business-profile.example.yaml](examples/business-profile.example.yaml).

   Before import, require evidence for every mapped object, field, and lifecycle
   bucket:

   - population count and distinct value count
   - top observed values from the reviewed cache
   - at least two positive examples that should match
   - at least two negative examples that should not match
   - RevOps or process-owner signoff

   Use the worksheet in
   [Customer implementation checklist](implementation-checklist.md#revops-profile-signoff)
   when handing profile review to RevOps or a non-developer process owner.

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

Do not expose profile-aware MCP answers to business users until:

- `profile_readiness.active=true`
- the profile cache is fresh
- the lifecycle core buckets are present
- attribution coverage has been reviewed
- unavailable concepts have either been accepted or fixed
- transcript coverage is sufficient for the intended prompts

Rollback is now staged rather than overwrite-only. Keep reviewed YAML outside
git, import candidate profiles without activation, compare them, and activate
only after RevOps signoff. Use `--lifecycle-source builtin` for CLI analysis
while investigating a suspect active profile.

Suggested profile file naming:

```text
~/gongctl-data/profiles/customer-profile.reviewed-20260501.yaml
~/gongctl-data/profiles/customer-profile.candidate-20260515.yaml
```

Staged review and activation:

```bash
gongctl profile import \
  --db ~/gongctl-data/gong.db \
  --profile ~/gongctl-data/profiles/customer-profile.candidate-20260515.yaml \
  --activate=false

gongctl profile history --db ~/gongctl-data/gong.db

gongctl profile diff \
  --db ~/gongctl-data/gong.db \
  --from active \
  --to ~/gongctl-data/profiles/customer-profile.candidate-20260515.yaml

gongctl profile activate --db ~/gongctl-data/gong.db --id PROFILE_ID
gongctl sync status --db ~/gongctl-data/gong.db
```

Manual rollback:

```bash
gongctl profile history --db ~/gongctl-data/gong.db
gongctl profile activate --db ~/gongctl-data/gong.db --id PRIOR_PROFILE_ID

gongctl sync status --db ~/gongctl-data/gong.db
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle --lifecycle-source profile
```

Investigation mode while a profile is suspect:

```bash
gongctl analyze calls --db ~/gongctl-data/gong.db --group-by lifecycle --lifecycle-source builtin
```

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

Common starting mappings:

| CRM pattern | Deal object | Account object | Stage field | Notes |
| --- | --- | --- | --- | --- |
| Salesforce-style | `Opportunity` | `Account` | `StageName` | `ForecastCategoryName` is useful context but is not the same as lifecycle stage. |
| HubSpot-style | `Deal` | `Company` | `dealstage` | `pipeline` helps segment processes but should not replace the stage field. |

Common bad mappings: `Type`, `Status`, `CreatedDate`, owner fields, source
campaigns, or forecast categories used as the only lifecycle stage. These may
be useful methodology or segmentation fields, but they should not drive the
core lifecycle buckets without reviewer evidence.

## YAML Fields

- `version`: Profile schema version. Use `1`.
- `name`: Human-readable profile name shown in profile metadata.
- `objects`: Local aliases for tenant CRM object types. The alias is the stable
  concept `gongctl` uses, such as `deal` or `account`; `object_types` lists the
  actual CRM object names seen in the Gong cache, such as `Opportunity`,
  `Deal`, `Account`, or `Company`.
- `fields`: Local aliases for CRM fields. Each field concept points at one
  object alias plus one or more real CRM field names. For example,
  `deal_stage` may map to `StageName` in one tenant and `DealPhase__c` in
  another.
- `lifecycle`: Rules that map cached CRM field values into the required
  lifecycle buckets: `open`, `closed_won`, `closed_lost`, `post_sales`, and
  `unknown`. Buckets with lower `order` evaluate first. Rules can reference a
  field concept with `field`, or a raw object/field pair with `object` plus
  `field_name`.
- `methodology`: Optional concepts used for business-specific analysis, such as
  pain, next steps, risk, MEDDICC fields, implementation criteria, or renewal
  signals. Concepts can include plain-language `aliases`, CRM `fields`, tracker
  IDs, or scorecard question IDs.
- `confidence` and `evidence`: Optional discovery metadata. `profile discover`
  may include these to explain why it picked an object, field, or lifecycle
  value. Operators can keep them for review or remove them before import.

`gongctl profile schema` prints a generated JSON Schema for less technical
RevOps handoff and editor validation. `gongctl profile validate` remains the
authoritative machine check because it also validates mappings against the
current tenant cache inventory. It writes a JSON report; automation that needs a
semantic pass/fail gate should inspect the report's `valid` field before
importing.

Common validation failures:

| Failure | What to fix |
| --- | --- |
| Missing lifecycle core bucket | Add `open`, `closed_won`, `closed_lost`, `post_sales`, or `unknown`. |
| Unknown field alias in a rule | Add the alias under `fields`, or use the correct existing alias. |
| Field alias points to unknown object | Add the object alias under `objects`, or correct the field's `object`. |
| Unsupported rule operator | Use one of the operators in [Rule Grammar](#rule-grammar). |
| Regex too broad or invalid | Replace with exact `equals`/`in` rules unless regex is truly needed. |
| `post_sales` catches active pipeline | Add negative examples and tighten customer-success/renewal/support values. |

Review checklist:

- Confirm the CRM object names match the tenant's actual Gong CRM context.
- Confirm `deal_stage` points to the real pipeline stage field, not a created
  date, owner, type, or unrelated status field.
- Confirm `post_sales` rules identify real customers, renewals, support,
  success, or implementation conversations rather than active new-logo deals.
- Confirm `closed_won` and `closed_lost` values match the tenant's exact CRM
  stage names.
- Remove or edit noisy `evidence.values` before sharing the profile with anyone
  who should not see tenant CRM values.
- Run `profile validate` after every edit.

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
