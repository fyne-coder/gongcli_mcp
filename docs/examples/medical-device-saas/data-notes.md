# Synthetic Data Notes For Pulsaris Medical Platform

## Scope

This example package exists to support privacy-safe Q1 demos in `gongctl`. All
content in this folder is synthetic and should be treated as documentation
scaffolding, not customer evidence, benchmark truth, or sanitized production
data.

## Synthetic Assumptions

- Pulsaris Medical Platform is a fictional vendor created for demo use only.
- The profile assumes a software-first sale into regulated device manufacturers.
- Persona labels are role-based so CLI and MCP examples can show stakeholder
  variety without exposing personal data.
- Lifecycle, stage, and value bands are generic approximations chosen to match a
  realistic enterprise motion rather than any specific tenant.
- Use cases and objections are composed from common medical-device SaaS patterns,
  not copied from calls, transcripts, CRM notes, or scorecards.

## Safety Boundaries

- Do not add real transcript text, real account names, real contact names, real
  email addresses, real call IDs, or direct CRM exports to this folder.
- Do not treat synthetic labels as if they were anonymized customer values.
  Synthesis is the safety boundary; direct redaction is not the target here.
- If future demos need extra fields, keep them generic, distribution-shaped, and
  traceability-safe.
- Keep public examples focused on metadata patterns, business themes, and
  workflow structure rather than specific regulated claims or customer histories.

## Reuse Policy

Reuse this package only as a seed for other synthetic examples, documentation,
or demo walkthroughs. Any derivative file should preserve the same safety model:
fully fictional account context, role-based stakeholders, fake IDs, and no
lifted language from private Gong data. If a downstream workflow needs richer
detail, generate new synthetic content instead of editing in redacted customer
material.

## Fake-Company Safety Statement

Pulsaris Medical Platform is not a disguised tenant, not a sanitized export, and
not a benchmark reference set. It is a fictional shell designed to let
maintainers demonstrate Q1 analysis shapes while staying inside the repo's
public-source boundary.

## Mapping To Q1 Analysis Shapes

This company profile is meant to support the same high-level analysis categories
as `q1-demo-dataset-plan.md` without reproducing private records.

- lifecycle and stage analysis: `late_stage_pipeline` and
  `evaluation_and_validation` create a realistic mid-to-late funnel context
- industry analysis: `medical devices` plus `compliance workflow software`
  provides a clear vertical lens
- scope analysis: the package supports discovery, validation, rollout planning,
  and executive-review call types
- evidence density analysis: `medium_to_high` fits transcript-search and
  objection-summary demos without implying any real snippet source
- theme analysis: CAPA, audit readiness, release governance, and post-market
  visibility map cleanly to common Q1 business themes

## Recommended Demo Constraints

- Prefer role labels over person labels in screenshots, prompts, and examples.
- Prefer rounded value bands over exact pricing or exact forecast numbers.
- Prefer fake identifier prefixes such as `demo-`, `crm-`, and `opp-`.
- Prefer explanation of distributions and themes over any claim of historic
  tenant accuracy.
