# Pulsaris Q1 Demo Dataset Plan

This file defines a concrete, fully synthetic Q1 2026 demo dataset for
Pulsaris Medical Platform in a Gong-shaped schema. It is meant to be specific
enough that a maintainer can later generate JSONL, SQLite fixtures, or demo
screens without inventing the storyline from scratch.

## Dataset Envelope

- Quarter: Q1 2026
- Total calls: 16
- Primary storyline: one flagship new-logo deal from first outbound touch to
  kickoff
- Supporting storyline: one renewal thread plus one upsell/expansion thread to
  exercise non-new-logo lifecycle buckets
- Lifecycle buckets covered:
  `outbound_prospecting`, `active_sales_pipeline`, `late_stage_sales`,
  `closed_won_lost_review`, `renewal`, `upsell_expansion`,
  `customer_success_account`
- Scope values used: `External`, `Internal`
- System defaults: `Zoom` for external calls, `Google Meet` for internal calls
- Direction defaults: `Outbound` for prospecting/internal seller prep,
  `Inbound` for buyer-driven reviews and post-sale cadence

## Transcript Density Note

The canonical storyline is generated with role-to-role transcript blocks, not one-line
quotes, and the current public bundle provides full transcript coverage for all 16
calls.

Current synthetic target for this bundle:

- 16 calls
- 78 transcript segments
- 16 calls with transcript excerpts (no missing-transcript rows)
- 6 speakers per call where needed (never named people)

### Example richer transcript block

Use blocks like this when expanding a single call in this plan:

```text
it_validation_lead: We can support cloud software, but QA will reject it if the validation packet is assembled by hand.
solutions_consultant: Wave one ships with a validation packet template, attributable approvals, and traceability from complaint intake to CAPA closure.
quality_systems_leader: Then show me the evidence handoff: who signs, where the record lives, and what changes when an approver changes roles.
solutions_consultant: Every approval and reassignment stays in the same audit-ready chain, so role changes do not break the record.
it_validation_lead: If that holds, this moves from a security concern to a signoff-planning exercise.
```

## Storyboard Acts

### Act I: Trigger And Discovery

Calls `demo-call-pmp-q1-001` through `demo-call-pmp-q1-004`

Story goal:
Show how audit pressure, complaint backlog pain, and release-governance gaps
create a credible entry point for Pulsaris.

### Act II: Validation And Business Case

Calls `demo-call-pmp-q1-005` through `demo-call-pmp-q1-008`

Story goal:
Show the deal moving from interest to proof. The buyer committee tests
traceability, validation packaging, security, and rollout practicality.

### Act III: Commitment And Launch

Calls `demo-call-pmp-q1-009` through `demo-call-pmp-q1-013`

Story goal:
Show proposal narrowing, contracting, win reasons, and a tightly scoped
implementation kickoff.

### Act IV: Land, Renew, Expand

Calls `demo-call-pmp-q1-014` through `demo-call-pmp-q1-016`

Story goal:
Show that the same Q1 demo bundle can answer lifecycle questions beyond new
logo pipeline by including renewal, expansion, and customer-success evidence.

## Storyboard Walkthrough Command Spine

Treat this file as the canonical storyboard for the 16-call Pulsaris dataset.
Run command examples from the repository root with:

```bash
export PULSARIS_DB="$HOME/gongctl-data/public-example-q1-2026/gong-example-q1-2026.db"
```

If a generated `q1-storyboard.md` exists in the public bundle, it should mirror
the command order and next actions defined here rather than inventing a new
storyline.

### Act I: Trigger And Discovery Walkthrough

Business question:
`Why did HelioPulse engage in the first place, and what exact pain language made
the opportunity real?`

- Command:
  `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-hpd-q1-001 --limit 20`
  - Inspect: the early-call cluster `demo-call-pmp-q1-001` through
    `demo-call-pmp-q1-004`.
- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "complaint" --limit 10`
  - Inspect: complaint backlog, traceability, and CAPA pain language from the
    first four calls.
- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "audit" --limit 10`
  - Inspect: audit pressure language that establishes urgency.
- Next action:
  write one discovery-summary sentence that connects complaint backlog pain to
  the March audit deadline and names the first buyer champion role.

### Act II: Validation And Business Case Walkthrough

Business question:
`What proof did the buyer committee need before the deal felt safe to advance?`

- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "validation" --limit 10`
  - Inspect: evidence from `demo-call-pmp-q1-006` and `demo-call-pmp-q1-007`.
- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "approval" --limit 10`
  - Inspect: attributable-approval and governance concerns.
- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "launch" --limit 10`
  - Inspect: where the executive business case shifts from workflow automation
    to launch-risk reduction.
- Next action:
  produce a proof map with three rows: `validation packaging`,
  `approval controls`, and `launch-risk reduction`, each tied to one buyer
  quote or call cluster.

### Act III: Commitment And Launch Walkthrough

Business question:
`How did the opportunity move from proposal to signed scope, and what made the
post-close plan credible?`

- Command:
  `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-hpd-q1-001 --limit 20`
  - Inspect: calls `demo-call-pmp-q1-008` through `demo-call-pmp-q1-013`.
- Command:
  `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "training" --limit 10`
  - Inspect: commercial and implementation conditions from
    `demo-call-pmp-q1-010`.
- Command:
  `go run ./cmd/gongctl calls show --db "$PULSARIS_DB" --call-id demo-call-pmp-q1-011 --json`
  - Inspect: the first-30-day success framing from the close-plan call.
- Command:
  `go run ./cmd/gongctl calls show --db "$PULSARIS_DB" --call-id demo-call-pmp-q1-012 --json`
  - Inspect: the internal win narrative used for handoff and messaging.
- Next action:
  leave the walkthrough with two outputs: one close-plan summary for Sales
  leadership and one kickoff-success checklist for implementation.

### Act IV: Land, Renew, Expand Walkthrough

Business question:
`Does the demo bundle prove more than new-logo pipeline, and can it support a
retention or expansion storyline?`

- Command:
  `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-ris-ren-001 --limit 10`
  - Inspect: the renewal thread in `demo-call-pmp-q1-014`.
- Command:
  `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-bmn-exp-001 --limit 10`
  - Inspect: the expansion thread in `demo-call-pmp-q1-015`.
- Command:
  `go run ./cmd/gongctl calls show --db "$PULSARIS_DB" --call-id demo-call-pmp-q1-016 --json`
  - Inspect: the customer-success follow-through after renewal.
- Command:
  `go run ./cmd/gongctl analyze calls --db "$PULSARIS_DB" --group-by lifecycle`
  - Inspect: whether renewal, expansion, and customer-success are visible as
    first-class buckets.
- Next action:
  decide which non-new-logo example to show in the demo and name the business
  question it answers: retention risk, expansion readiness, or adoption health.

## Gong-Shaped Schema Target

Use this as the target logical shape for later JSON fixtures:

```json
{
  "id": "demo-call-pmp-q1-005",
  "title": "HelioPulse traceability demo",
  "started": "2026-02-05T16:00:00Z",
  "duration": 3000,
  "direction": "Inbound",
  "system": "Zoom",
  "scope": "External",
  "parties": [
    {"speakerId": "speaker-ae-001", "role": "account_executive"},
    {"speakerId": "speaker-sc-001", "role": "solutions_consultant"},
    {"speakerId": "speaker-qs-001", "role": "quality_systems_leader"}
  ],
  "context": {
    "crmObjects": [
      {"type": "Account", "id": "crm-acct-hpd-001", "name": "HelioPulse Devices"},
      {"type": "Opportunity", "id": "opp-hpd-q1-001", "name": "HelioPulse Q1 Platform Purchase"}
    ]
  },
  "highlightSegment": {
    "segmentId": "demo-seg-pmp-005a",
    "speakerRole": "quality_systems_leader",
    "snippet": "Show me how a complaint becomes a CAPA without breaking the validation evidence chain."
  }
}
```

## CRM Object Registry

### Flagship New-Logo Thread

`Account`

- `crm-acct-hpd-001`: `HelioPulse Devices`
- Core fields:
  `Industry=Medical Devices`;
  `Account_Type__c=Prospect` through call `011`, then `Customer - New`;
  `Region__c=North America`;
  `Device_Families__c=Remote cardiac monitoring`;
  `Complaint_Backlog_Band__c=High`;
  `QMS_System__c=Mixed spreadsheets and eQMS`

`Opportunity`

- `opp-hpd-q1-001`: `HelioPulse Q1 Platform Purchase`
- Stable fields:
  `Type=New Business`;
  `Amount=185000`;
  `CloseDate=2026-03-31`;
  `Primary_Lead_Source__c=Referral`;
  `Pilot_Scope__c=Cardiac monitoring device family`;
  `Champion_Role__c=quality_systems_leader`;
  `Economic_Buyer_Role__c=commercial_operations_sponsor`
- Per-call changing fields:
  `StageName`;
  `ForecastCategoryName`;
  `Validation_Plan_Status__c`;
  `Decision_Criteria__c`

`Implementation_Project__c`

- `crm-impl-hpd-001`: `HelioPulse Wave 1 Rollout`
- Fields:
  `Rollout_Wave__c=Wave 1`;
  `Go_Live_Target__c=2026-05-18`;
  `Validated_Workflow_Pack__c=CAPA + Complaint Intake`;
  `Training_Status__c=Planned`

### Renewal Thread

`Account`

- `crm-acct-ris-001`: `RidgeLine Infusion Systems`
- Core fields:
  `Industry=Medical Devices`;
  `Account_Type__c=Customer`;
  `Region__c=North America`;
  `Renewal_Risk__c=Medium`;
  `Live_Product_Lines__c=Infusion pumps`

`Opportunity`

- `opp-ris-ren-001`: `RidgeLine 2026 Renewal`
- Stable fields:
  `Type=Renewal`;
  `Amount=124000`;
  `CloseDate=2026-03-20`;
  `Renewal_Process__c=Standard`;
  `Primary_Lead_Source__c=Customer Base`
- Per-call changing fields:
  `StageName`;
  `ForecastCategoryName`;
  `Adoption_Risk__c`

### Upsell / Expansion Thread

`Account`

- `crm-acct-bmn-001`: `BlueMesa Neurotech`
- Core fields:
  `Industry=Medical Devices`;
  `Account_Type__c=Customer`;
  `Region__c=Western Europe`;
  `Expansion_Readiness__c=High`;
  `Installed_Modules__c=Complaint Intake`

`Opportunity`

- `opp-bmn-exp-001`: `BlueMesa Post-Market Expansion`
- Stable fields:
  `Type=Upsell`;
  `Amount=68000`;
  `CloseDate=2026-03-28`;
  `Expansion_Bookings__c=true`;
  `Primary_Lead_Source__c=Customer Base`
- Per-call changing fields:
  `StageName`;
  `ForecastCategoryName`;
  `Expansion_Target__c`

## Call Plan

### `demo-call-pmp-q1-001`

- Date ET: `2026-01-07`
- Act: `Act I`
- Journey phase: `outbound trigger`
- Title: `HelioPulse audit pressure intro`
- Lifecycle: `outbound_prospecting`
- Scope: `External`
- Direction: `Outbound`
- System: `Zoom`
- Duration seconds: `1500`
- Opportunity stage: `none`
- CRM objects:
  `Account crm-acct-hpd-001`
- Key CRM fields:
  `Account.Account_Type__c=Prospect`;
  `Account.Complaint_Backlog_Band__c=High`
- Highlight segment ID: `demo-seg-pmp-001a`
- Transcript snippet:
  `quality_systems_leader`: "If you can shorten evidence collection before the March audit, we will take a serious look."
- Storyboard use:
  open with a credible external trigger instead of a generic cold call

### `demo-call-pmp-q1-002`

- Date ET: `2026-01-14`
- Act: `Act I`
- Journey phase: `discovery`
- Title: `HelioPulse quality and regulatory discovery`
- Lifecycle: `active_sales_pipeline`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2700`
- Opportunity stage: `Discovery`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Discovery`;
  `Opportunity.ForecastCategoryName=Pipeline`;
  `Opportunity.Validation_Plan_Status__c=Not Started`
- Highlight segment ID: `demo-seg-pmp-002a`
- Transcript snippet:
  `regulatory_affairs_lead`: "We have CAPA owners in one system and release evidence in three others, so nobody trusts the closure date."
- Storyboard use:
  establish fragmented workflow pain and create the first opportunity context

### `demo-call-pmp-q1-003`

- Date ET: `2026-01-21`
- Act: `Act I`
- Journey phase: `current-state workshop`
- Title: `HelioPulse complaint workflow mapping`
- Lifecycle: `active_sales_pipeline`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `3300`
- Opportunity stage: `Discovery`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Discovery`;
  `Opportunity.Decision_Criteria__c=Complaint traceability and faster CAPA closure`
- Highlight segment ID: `demo-seg-pmp-003a`
- Transcript snippet:
  `quality_systems_leader`: "The complaint queue is manageable until a field correction starts, then spreadsheets become the system of record."
- Storyboard use:
  show a richer discovery snippet for transcript search and objection clustering

### `demo-call-pmp-q1-004`

- Date ET: `2026-01-29`
- Act: `Act I`
- Journey phase: `clinical safety alignment`
- Title: `HelioPulse clinical safety trend review`
- Lifecycle: `active_sales_pipeline`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2400`
- Opportunity stage: `Discovery`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Discovery`;
  `Opportunity.Decision_Criteria__c=Earlier safety trend visibility`
- Highlight segment ID: `demo-seg-pmp-004a`
- Transcript snippet:
  `clinical_safety_lead`: "Our clinical safety review happens after the quality meeting, so trend escalation is always a week late."
- Storyboard use:
  widen the buyer committee beyond quality and regulatory

### `demo-call-pmp-q1-005`

- Date ET: `2026-02-05`
- Act: `Act II`
- Journey phase: `solution demo`
- Title: `HelioPulse traceability demo`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `3000`
- Opportunity stage: `Demo & Business Case`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Demo & Business Case`;
  `Opportunity.ForecastCategoryName=Best Case`;
  `Opportunity.Validation_Plan_Status__c=Drafting`
- Highlight segment ID: `demo-seg-pmp-005a`
- Transcript snippet:
  `quality_systems_leader`: "Show me how a complaint becomes a CAPA without breaking the validation evidence chain."
- Storyboard use:
  anchor the main product demo in one memorable business question

### `demo-call-pmp-q1-006`

- Date ET: `2026-02-11`
- Act: `Act II`
- Journey phase: `validation workshop`
- Title: `HelioPulse validation package workshop`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `3600`
- Opportunity stage: `Business Case`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Business Case`;
  `Opportunity.Validation_Plan_Status__c=In Review`
- Highlight segment ID: `demo-seg-pmp-006a`
- Transcript snippet:
  `it_validation_lead`: "We are fine with cloud software, but only if the validation deliverables are packaged in a way QA can sign."
- Storyboard use:
  surface regulated-software validation as a real buying condition

### `demo-call-pmp-q1-007`

- Date ET: `2026-02-18`
- Act: `Act II`
- Journey phase: `security and integration review`
- Title: `HelioPulse identity and approval controls review`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2700`
- Opportunity stage: `Business Case`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Business Case`;
  `Opportunity.Decision_Criteria__c=SSO plus attributable approvals`
- Highlight segment ID: `demo-seg-pmp-007a`
- Transcript snippet:
  `it_validation_lead`: "SSO is table stakes; the harder question is whether release approvals stay attributable when the approver changes roles."
- Storyboard use:
  support integration and governance searches without introducing engineering-heavy jargon

### `demo-call-pmp-q1-008`

- Date ET: `2026-02-25`
- Act: `Act II`
- Journey phase: `executive business case`
- Title: `HelioPulse executive value review`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2100`
- Opportunity stage: `SOW & Proposal`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=SOW & Proposal`;
  `Opportunity.ForecastCategoryName=Commit`;
  `Opportunity.Decision_Criteria__c=Reduce launch delay risk`
- Highlight segment ID: `demo-seg-pmp-008a`
- Transcript snippet:
  `commercial_operations_sponsor`: "If this avoids even one launch slip, the software pays for itself, but start with one device family."
- Storyboard use:
  connect compliance workflow pain to revenue and launch timing

### `demo-call-pmp-q1-009`

- Date ET: `2026-03-03`
- Act: `Act III`
- Journey phase: `proposal review`
- Title: `HelioPulse phased rollout proposal review`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2400`
- Opportunity stage: `SOW & Proposal`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=SOW & Proposal`;
  `Opportunity.Pilot_Scope__c=Cardiac monitoring device family`
- Highlight segment ID: `demo-seg-pmp-009a`
- Transcript snippet:
  `regulatory_affairs_lead`: "Phase one should cover CAPA, complaint intake, and audit packet prep for cardiac monitoring only."
- Storyboard use:
  show scope narrowing before procurement and contracting

### `demo-call-pmp-q1-010`

- Date ET: `2026-03-10`
- Act: `Act III`
- Journey phase: `procurement and legal review`
- Title: `HelioPulse procurement and validation obligations`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `3000`
- Opportunity stage: `Contract Review`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Contract Review`;
  `Opportunity.ForecastCategoryName=Commit`
- Highlight segment ID: `demo-seg-pmp-010a`
- Transcript snippet:
  `procurement_lead`: "We can live with the paper, but we need training commitments and a validation-support clause before signature."
- Storyboard use:
  support late-stage objection mining with a commercial rather than technical blocker

### `demo-call-pmp-q1-011`

- Date ET: `2026-03-17`
- Act: `Act III`
- Journey phase: `last-mile close`
- Title: `HelioPulse launch-readiness close plan`
- Lifecycle: `late_stage_sales`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `1800`
- Opportunity stage: `Contract Signing`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Contract Signing`;
  `Opportunity.ForecastCategoryName=Commit`
- Highlight segment ID: `demo-seg-pmp-011a`
- Transcript snippet:
  `commercial_operations_sponsor`: "Assume we sign this week; what has to be true in the first thirty days for QA to call this a win?"
- Storyboard use:
  bridge the commercial close to implementation expectations

### `demo-call-pmp-q1-012`

- Date ET: `2026-03-19`
- Act: `Act III`
- Journey phase: `internal win review`
- Title: `HelioPulse win handoff`
- Lifecycle: `closed_won_lost_review`
- Scope: `Internal`
- Direction: `Outbound`
- System: `Google Meet`
- Duration seconds: `1500`
- Opportunity stage: `Closed Won`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Opportunity opp-hpd-q1-001`
- Key CRM fields:
  `Opportunity.StageName=Closed Won`;
  `Opportunity.ForecastCategoryName=Closed`
- Highlight segment ID: `demo-seg-pmp-012a`
- Transcript snippet:
  `account_executive`: "The win hinged on audit readiness plus launch-risk reduction, not generic workflow automation."
- Storyboard use:
  provide a clean internal call for win-theme analysis and handoff narratives

### `demo-call-pmp-q1-013`

- Date ET: `2026-03-27`
- Act: `Act III`
- Journey phase: `kickoff`
- Title: `HelioPulse wave-one kickoff`
- Lifecycle: `customer_success_account`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2700`
- Opportunity stage: `none`
- CRM objects:
  `Account crm-acct-hpd-001`;
  `Implementation_Project__c crm-impl-hpd-001`
- Key CRM fields:
  `Account.Account_Type__c=Customer - New`;
  `Implementation_Project__c.Rollout_Wave__c=Wave 1`;
  `Implementation_Project__c.Go_Live_Target__c=2026-05-18`
- Highlight segment ID: `demo-seg-pmp-013a`
- Transcript snippet:
  `implementation_manager`: "Keep the pilot narrow, prove complaint-to-CAPA traceability, then expand after the first internal audit passes."
- Storyboard use:
  end the flagship thread with a concrete post-sale outcome, not just a contract signature

### `demo-call-pmp-q1-014`

- Date ET: `2026-01-23`
- Act: `Act IV`
- Journey phase: `renewal risk review`
- Title: `RidgeLine renewal risk review`
- Lifecycle: `renewal`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2100`
- Opportunity stage: `Contract Review`
- CRM objects:
  `Account crm-acct-ris-001`;
  `Opportunity opp-ris-ren-001`
- Key CRM fields:
  `Opportunity.Type=Renewal`;
  `Opportunity.StageName=Contract Review`;
  `Opportunity.Adoption_Risk__c=Moderate`
- Highlight segment ID: `demo-seg-pmp-014a`
- Transcript snippet:
  `quality_systems_leader`: "We will renew if you can show fewer overdue actions and a cleaner audit packet handoff than last year."
- Storyboard use:
  exercise the renewal lifecycle bucket with a direct outcome-based retention statement

### `demo-call-pmp-q1-015`

- Date ET: `2026-02-26`
- Act: `Act IV`
- Journey phase: `upsell expansion discovery`
- Title: `BlueMesa post-market expansion review`
- Lifecycle: `upsell_expansion`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `2400`
- Opportunity stage: `Demo & Business Case`
- CRM objects:
  `Account crm-acct-bmn-001`;
  `Opportunity opp-bmn-exp-001`
- Key CRM fields:
  `Opportunity.Type=Upsell`;
  `Opportunity.StageName=Demo & Business Case`;
  `Opportunity.Expansion_Target__c=Post-market trend review`
- Highlight segment ID: `demo-seg-pmp-015a`
- Transcript snippet:
  `clinical_safety_lead`: "Complaint intake works well; the expansion question is whether post-market trend review can live in the same operating rhythm."
- Storyboard use:
  show an expansion motion tied to an adjacent workflow, not a seat-count upsell

### `demo-call-pmp-q1-016`

- Date ET: `2026-03-28`
- Act: `Act IV`
- Journey phase: `post-renewal adoption review`
- Title: `RidgeLine regional adoption review`
- Lifecycle: `customer_success_account`
- Scope: `External`
- Direction: `Inbound`
- System: `Zoom`
- Duration seconds: `1800`
- Opportunity stage: `none`
- CRM objects:
  `Account crm-acct-ris-001`
- Key CRM fields:
  `Account.Account_Type__c=Customer`;
  `Account.Renewal_Risk__c=Low`
- Highlight segment ID: `demo-seg-pmp-016a`
- Transcript snippet:
  `customer_success_manager`: "The renewal is done; next we need regional leaders to use one escalation path instead of emailing spreadsheets around."
- Storyboard use:
  give the dataset a clean customer-success anchor after renewal closes

## Recommended Generation Notes

- Keep each call as 4–8 transcript segments so search tools have neighboring
  evidence and context, not just a one-line quote.
- Keep each call to 2–5 role-based speakers; avoid named contacts.
- Reuse the CRM object IDs exactly as written here so CLI, MCP, and docs can
  reference the same synthetic account and opportunity handles.
- Use `context.crmObjects[].type`, `id`, `name`, and `fields[]` with
  `name`, `label`, `type`, `value` when a fixture generator wants exact
  Gong-like shape.
- Preserve the opportunity-stage progression for
  `opp-hpd-q1-001` exactly in order:
  `Discovery -> Demo & Business Case -> Business Case -> SOW & Proposal ->
  Contract Review -> Contract Signing -> Closed Won`.

- If any future regeneration changes IDs or lifecycle shape, record that in the
  accepted reference diff.

## Demo Queries This Plan Should Support

- Late-stage objection search around validation, procurement, and launch risk.
- Lifecycle summaries that separate new-logo, renewal, expansion, and
  customer-success calls.
- CRM-context transcript search by `Opportunity.StageName`, `Opportunity.Type`,
  `Account.Account_Type__c`, and `Implementation_Project__c.Rollout_Wave__c`.
- Storyboard walkthroughs that move from first pain signal to signed scope and
  then to post-sale rollout.

## Prompt Quality Bar

Demo prompts should be written as realistic team questions, not generic CLI
examples. Each prompt should include:

- a business question a Sales, RevOps, or Marketing team would actually ask
- the exact command or commands to run against
  `$PULSARIS_DB`
- the output fields or snippets to inspect
- one concrete next action the team can take from the result

Use these role-specific outcomes when expanding the dataset or storyboard:

- Sales: explain what changed in a deal, identify buyer language that moves
  opportunity forward, and produce a next-step account angle.
- RevOps: separate analysis-ready evidence from thin-evidence buckets, identify
  lifecycle or forecast blind spots, and rank cleanup work.
- Marketing: extract repeatable buyer language, convert objections into message
  pillars, and name one field asset or campaign angle.

- Use one of these three roles when running a live Q1 demo review. In each section,
  keep each command block runnable from the repository root.

### Sales

Business prompt:
`Build me a HelioPulse deal-update brief: three bullets on what changed from
discovery to close plan, two executive follow-up themes for the next email, and
one question the AE should confirm before signature.`

- Command: `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-hpd-q1-001 --limit 20`
  Inspect: the returned call spine, `metaData.opportunityStage`, and how the
  titles move from discovery into proposal, contract, and kickoff.
  Expected interpretation: clean stage movement supports a controlled business-case
  progression rather than a loose set of meetings.
  Actionable output: write the three-bullet stage narrative as
  `complaint pain -> validation confidence -> launch-readiness commitment`.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "launch risk" --limit 10`
  Inspect: late-stage buyer language about launch timing, economic value, and
  executive confidence.
  Expected interpretation: snippets should show buyer movement from feature
  evaluation to avoided launch delay and first-30-day success criteria.
  Actionable output: draft one executive follow-up theme around reduced launch
  delay and one around a narrowly scoped wave-one rollout.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "complaint" --limit 10`
  Inspect: early pain around complaint intake, CAPA ownership, and evidence
  collection.
  Expected interpretation: these calls should anchor why the account engaged and
  what problem it agreed to fix first.
  Actionable output: use the top two snippets to draft the opening paragraph of a
  follow-up email summary.

### RevOps

Business prompt:
`Give me a demo-safe coverage verdict for this quarter: what lifecycle reporting is
reliable now, what is thin, and where should cleanup focus before leadership uses
this sample in a forecast or retention review.`

- Command: `go run ./cmd/gongctl analyze calls --db "$PULSARIS_DB" --group-by lifecycle`
  Inspect: new-logo, renewal, expansion, and customer-success distribution.
  Expected interpretation: the output should show a clear flagship new-logo thread
  plus renewal and expansion support.
  Actionable output: produce a one-line leadership caveat that this sample is
  reliable for stage-flow and lifecycle demos, not for quota-style math.
- Command: `go run ./cmd/gongctl analyze coverage --db "$PULSARIS_DB"`
  Inspect: transcript coverage rate and lifecycle buckets.
  Expected interpretation: all 16 calls should be covered by transcript excerpts.
  Actionable output: assign readiness states (`green`, `yellow`, `red`) for
  leadership review.
- Command: `go run ./cmd/gongctl analyze transcript-backlog --db "$PULSARIS_DB" --limit 10`
  Inspect: priority score, confidence, lifecycle bucket, and reason.
  Expected interpretation: priority should reflect where lifecycle narratives are
  most at risk.
  Actionable output: create a cleanup queue with `sync first` and `can wait`
  ordering and one-line rationale each.

### Marketing

Business prompt:
`Turn this synthetic quarter into a field-ready messaging brief: two message
pillars, one objection-handling angle, and one asset concept Sales can use after
a medical-device SaaS demo.`

- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "audit readiness" --limit 10`
  Inspect: where audit readiness appears across validation, proposal, and win
  language.
  Expected interpretation: `audit readiness` is the cleanest cross-functional theme
  because it appears in both validation and close-stage language.
  Actionable output: set `audit readiness without approval chaos` as the first
  message pillar and link it to quality, regulatory, and executive stakeholders.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "validation" --limit 10`
  Inspect: objections around validation package, QA signoff, attributable
  approvals, and cloud-software adoption.
  Expected interpretation: the objection is fear that validation deliverables and
  signoff ownership will break, not generic cloud resistance.
  Actionable output: write one objection-handling talk track and CTA for a
  validation checklist or signoff-readiness asset.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "complaint" --limit 10`
  Inspect: discovery-stage pain language around complaint backlog and delayed
  clinical visibility.
  Expected interpretation: this is the strongest problem-formation evidence for
  why the platform matters.
  Actionable output: name the field asset and its promise, for example
  `Complaint-to-CAPA launch-risk checklist`, and list the three proof points it
  should cover.
- Command: `go run ./cmd/gongctl analyze calls --db "$PULSARIS_DB" --group-by month`
  Inspect: when proof points cluster during the quarter.
  Expected interpretation: January forms the problem, February validates the
  approach, and March provides the strongest commercial proof.
  Actionable output: turn that timing into a three-touch follow-up sequence for
  demo registrants or field sellers.

- Keep each command block runnable from the repository root.
- Keep each call to `2-5` role-based speakers; avoid named contacts.
- Reuse the CRM object IDs exactly as written here so CLI, MCP, and docs can
  reference the same synthetic account and opportunity handles.
- If a fixture generator wants exact Gong-like shapes, prefer:
  `context.crmObjects[].type`, `id`, `name`, and `fields[]` with
  `name`, `label`, `type`, `value`.
- Preserve stage progression for `opp-hpd-q1-001` exactly in order:
  `Discovery` -> `Demo & Business Case` -> `Business Case` ->
  `SOW & Proposal` -> `Contract Review` -> `Contract Signing` ->
  `Closed Won`.
