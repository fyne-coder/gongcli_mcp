# CRM Genericity Roadmap

This project now treats the built-in CRM read models as Salesforce-compatible
compatibility defaults, not as a claim that every Gong tenant uses those exact
fields.

## Current Boundary

The reviewed CRM dimension registry in `internal/store/crmdimensions` exposes a
small standard field set through neutral `CRMFieldNames` mappings. Those fields
are still compiled because they are bounded, reviewed, and shared by SQLite,
Postgres, and MCP capability discovery.

Compatibility read models still contain older Salesforce-shaped columns such as
`account_type`, `account_revenue_range`, `account_primary_procurement_system`,
`opportunity_forecast_category`, and `opportunity_procurement_system`. They
exist for backward compatibility with current CLI/MCP analysis paths, Postgres
functions, reader grants, and historical tests. New public positioning should
describe them as compatibility/example fields, not universal Gong fields.

## Target Shape

1. Keep standard reviewed dimensions in the `crmdimensions` registry.
2. Move deployment-specific Account/Opportunity custom fields into profile-owned or
   runtime-configured concepts.
3. Materialize profile concepts into profile fact caches, not global
   compatibility columns, before exposing them to MCP users.
4. Advertise only active profile-backed dimensions in capability discovery.
5. Keep raw CRM field value probing behind explicit, reviewed inventory/search
   tools with row limits and governance checks.

## Migration Slices

### D2a: Inventory And Classification

- Inventory every compatibility column and field-name source in SQLite,
  Postgres, MCP, docs, and tests.
- Classify each field as:
  - standard reviewed dimension
  - profile concept candidate
  - compatibility-only legacy field
  - unsafe/raw identifier that must stay excluded
- Produce a machine-readable table that maps current column, source field names,
  consumers, and proposed destination.
- Treat this inventory table as the sign-off gate before D2b implementation
  begins.

### D2b: Profile-Backed Dimension Advertisement

- Add an MCP capability section for active profile-backed concepts.
- Advertise only concepts backed by a fresh profile fact cache.
- Include profile provenance and cache freshness in every profile-backed
  dimension response.
- Keep unknown or stale profile dimensions fail-closed.

### D2c: Compatibility Column Deprecation

- Mark compatibility columns in docs and capability payloads as
  `compatibility_default`.
- Add warnings when users filter or group by compatibility-only columns without
  an active profile.
- Keep columns physically present for one or more releases to avoid breaking
  existing deployments.

### D2d: Profile/Config Extraction

- Move account type, revenue range, forecast category, procurement system, and
  similar custom fields into reviewed profile concepts or opt-in methodology
  packs.
- Update SQLite and Postgres read paths to resolve profile-backed dimensions
  through the profile fact cache.
- Remove hard-coded custom-field assumptions from new read-model code after
  profile-backed paths have parity.

## Safety Requirements

- No arbitrary CRM field discovery as a business-user MCP feature.
- No raw Account or Opportunity names/websites as dimensions.
- No high-cardinality raw values as default group-by dimensions.
- Profile-backed dimensions must carry profile ID/hash and freshness metadata.
- Postgres reader grants and SECURITY DEFINER functions must be updated before
  a profile-backed dimension is advertised.
- Existing compatibility columns require a deprecation window and upgrade notes
  before removal.

## Verification

Each migration slice should include:

- SQLite and Postgres parity tests.
- Capability-discovery tests for active, stale, and absent profiles.
- Non-Salesforce synthetic fixtures using `Deal`/`Company` object names and
  deployment-specific fields such as `deal_phase` or `customer_status`.
- Public-surface scans proving examples are labeled as Salesforce-compatible or
  synthetic, not universal defaults.
