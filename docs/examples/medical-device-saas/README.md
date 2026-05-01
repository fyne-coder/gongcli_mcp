# Pulsaris Medical Platform

Pulsaris Medical Platform is a fictional, fully synthetic example company for
`gongctl` Q1 demo and documentation work. It represents a medical-device SaaS
vendor that helps device manufacturers coordinate compliance, post-market
quality workflows, and launch-readiness operations without exposing any real
tenant data. The demo storyline is generated from richer role-to-role
transcript snippets, not from a disguised customer tenant or lightly edited
real call data.

Mission statement:
Pulsaris exists to help regulated device teams protect patient safety while
reducing complaint-to-remediation cycle time through traceable SaaS workflows.

## Snapshot

- Industry focus: medical devices plus software compliance workflows
- Product shape: cloud platform for complaint intake, CAPA coordination,
  design-history evidence tracking, and audit preparation
- Buyer motion: regulated mid-market and enterprise device manufacturers
- Sales motion: multi-stakeholder sales with quality, regulatory, operations,
  and commercial leadership involved

## Ideal Customer Profile

Pulsaris fits device manufacturers that run complex handoffs between quality,
regulatory, field service, and commercial teams. The strongest fit is a company
with multiple product lines, at least one software-enabled device or connected
device program, and recurring audit pressure from internal quality teams,
notified bodies, or national regulators.

Typical target characteristics:

- 300-5,000 employees
- 2-12 active device families
- mix of hardware and software release processes
- fragmented evidence collection across spreadsheets, shared drives, and email
- executive pressure to shorten remediation cycles without increasing audit risk

## Core Value Proposition

Pulsaris gives regulated device teams one operating layer for compliance work
that usually sits across disconnected systems. The platform reduces the time
between issue detection, owner assignment, evidence capture, and audit-ready
closure while preserving traceability across quality and commercial workflows.

Primary outcomes:

- faster CAPA and complaint-resolution cycles
- clearer release readiness for software-enabled device updates
- fewer manual evidence hunts before audits
- better visibility into quality risk for revenue and operations leaders

## Go-To-Market Shape

Pulsaris runs a software-first enterprise motion with a narrow operational
wedge and a broader platform expansion path.

Entry motion:

- land with a quality or regulatory leader who owns remediation backlog pain
- prove value on one device family or one regional business unit
- expand into post-market surveillance, launch governance, and executive review

Primary personas:

- quality systems leader: needs faster corrective-action closure
- regulatory affairs lead: needs audit-ready evidence and cleaner submissions
- clinical safety lead: needs early detection of patient-safety risk signals and post-market trend clarity
- commercial operations sponsor: needs fewer revenue delays from compliance work

## Sample Use Cases

### 1. CAPA workflow consolidation

A device manufacturer replaces spreadsheet-driven follow-up with role-based task
tracking, evidence checkpoints, and aging views for open remediation items.

### 2. Software release readiness

A connected-device team tracks validation evidence, unresolved risks, and signoff
status across engineering, quality, and regulatory stakeholders before a release.

### 3. Complaint-to-trend review

Regional complaint teams log issues in a consistent workflow so leadership can
spot recurring patterns and prioritize the next quality review.

### 4. Audit preparation

An audit response team assembles evidence packets, closure history, and approval
trails from one system instead of stitching together exports from multiple tools.

## Sample Buyer Objections

- "We already track this in our quality system, so another layer could create duplicate work."
- "Our validation process is strict, and new workflow software can slow approvals."
- "Commercial leadership will not fund this unless it clearly shortens launch or renewal risk."
- "If our field teams cannot adopt the workflow quickly, the data quality will collapse."

## Demo Setup Steps

1. Present Pulsaris as a fictional reference account, not a disguised customer.
2. Pair this profile with synthetic Q1-style calls, richer role-role
   transcript snippets, and CRM-safe metadata only.
3. Keep all participants role-based in demos, such as `quality_systems_leader`
   or `regulatory_affairs_lead`, instead of person labels.
4. Use fake opportunity, account, and pipeline IDs from
   [company-profile.yaml](./company-profile.yaml) when a CLI or MCP example
   needs identifiers.
5. Explain that analysis outputs should focus on lifecycle, stage, scope,
   evidence density, and objection themes rather than tenant-specific values.
6. Point reviewers to [data-notes.md](./data-notes.md) before sharing the
   example outside the repo.
7. Treat [q1-demo-dataset-plan.md](./q1-demo-dataset-plan.md) as the canonical
   16-call storyboard, prompt pack, and walkthrough script for demos.
8. The generated, shareable Q1 demo bundle is in
   `$HOME/gongctl-data/public-example-q1-2026` and includes:
   `gong-example-q1-2026.db`, `example-calls.jsonl`,
   `example-transcript-segments.jsonl`, `q1-corpus-summary.json`,
   `q1-storyboard.md`, and `manifest.json`.
   If `q1-storyboard.md` exists in the generated bundle, treat it as a derived
   operator script. The repo copy of
   [q1-demo-dataset-plan.md](./q1-demo-dataset-plan.md) remains the source of
   truth for exact calls, prompts, commands, and next actions.

## Claude Desktop MCP Setup

Claude can use the synthetic bundle through the existing MCP-only Docker image.
No demo-specific image is required as long as the local MCP-only image or the
published `ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.1` image is current.
Add a separate MCP server entry named `gong-demo` so the synthetic dataset stays
separate from the real Q1 cache:

```json
{
  "mcpServers": {
    "gong-demo": {
      "command": "docker",
      "args": [
        "run",
        "--rm",
        "-i",
        "--network",
        "none",
        "-e",
        "GONGMCP_TOOL_ALLOWLIST=get_sync_status,search_calls,summarize_calls_by_lifecycle,summarize_call_facts,rank_transcript_backlog,search_transcript_segments,missing_transcripts",
        "-v",
        "/ABSOLUTE/PATH/TO/gongctl-data:/data:ro",
        "ghcr.io/fyne-coder/gongcli_mcp/gongmcp:v0.3.1",
        "--db",
        "/data/public-example-q1-2026/gong-example-q1-2026.db"
      ]
    }
  }
}
```

Restart Claude Desktop after changing
`$HOME/Library/Application Support/Claude/claude_desktop_config.json`.
The container runs with no network access, no Gong credentials, and a read-only
mount over the local synthetic SQLite bundle.

## Suggested Q1 Narrative

For Q1-style walkthroughs, frame Pulsaris as a vendor moving upmarket from one
department pilot to a regulated platform sale. Early calls emphasize current
state pain and audit readiness, mid-stage calls emphasize integration and
validation concerns, and late-stage calls emphasize rollout scope, launch timing,
and executive confidence. The shareable public bundle follows that same
synthetic story spine, and now includes transcript coverage across all 16 calls.

The detailed synthetic call list and act structure live in
[q1-demo-dataset-plan.md](./q1-demo-dataset-plan.md).

### 16-Call Walkthrough Quickstart

Run the walkthrough from the repository root against the synthetic bundle:

```bash
export PULSARIS_DB="$HOME/gongctl-data/public-example-q1-2026/gong-example-q1-2026.db"
```

1. Review the flagship new-logo thread:
   `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-hpd-q1-001 --limit 20`
   - Inspect: the storyline from `demo-call-pmp-q1-001` through
     `demo-call-pmp-q1-013`.
   - Next action: decide whether you are telling a discovery-to-close story, a
     validation-risk story, or a launch-readiness story before opening
     transcript snippets.
2. Pull the supporting non-new-logo motions:
   `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-ris-ren-001 --limit 10`
   `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-bmn-exp-001 --limit 10`
   - Inspect: whether renewal and expansion appear as distinct lifecycle
     examples rather than extra calls in the new-logo thread.
   - Next action: decide whether the audience needs retention proof,
     expansion proof, or only flagship pipeline proof.
3. Use the role-specific playbooks below or the act-by-act walkthroughs in
   [q1-demo-dataset-plan.md](./q1-demo-dataset-plan.md).

### Role-Based Action Playbooks

Each playbook is written as a realistic prompt a Sales, RevOps, or Marketing
team could use during a pipeline review, forecast check, or campaign-planning
session. Every command below is valid against `$PULSARIS_DB`.
`search transcripts` returns bounded snippets, so treat the output as evidence
for decisions and follow-up drafts, not as full-call quoting.

#### Sales

Business prompt:
`Walk me from HelioPulse discovery pain to launch-credible close, tell me what
nearly stalled the deal, and give me the two actions the account team should
take in the next seven days.`

- Command: `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-hpd-q1-001 --limit 20`
  Inspect: the call spine from `demo-call-pmp-q1-001` through
  `demo-call-pmp-q1-013`, especially movement into `Demo & Business Case`,
  `Contract Review`, `Closed Won`, and kickoff.
  Expected interpretation: summarize what changed between early pain,
  validation proof, and close-planning so the AE can explain momentum in one
  sentence.
  Actionable output: write the deal story as `complaint pain -> validation
  proof -> launch-readiness commitment`.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "validation" --limit 10`
  Inspect: calls `demo-call-pmp-q1-006`, `demo-call-pmp-q1-007`, and
  `demo-call-pmp-q1-010` for validation packaging and approval-risk language.
  Expected interpretation: identify the real blocker to progression, not just
  the loudest objection.
  Actionable output: capture one blocker statement the AE should confirm in the
  next customer note.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "launch" --limit 10`
  Inspect: late-stage buyer language from calls `demo-call-pmp-q1-008` through
  `demo-call-pmp-q1-011`.
  Expected interpretation: these snippets show when the deal becomes an
  executive risk-reduction decision.
  Actionable output: draft one executive follow-up theme around avoided launch
  delay and one around a tightly scoped wave-one rollout.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "complaint" --limit 10`
  Inspect: early pain around complaint intake, CAPA ownership, and evidence
  collection.
  Expected interpretation: this is the operational pain that made the
  opportunity real.
  Actionable output: use the top two snippets to write the opening paragraph of
  the follow-up email.
- Next actions to leave the demo with:
  - Send an AE recap that ties the validation blocker to the launch-risk
    rationale.
  - Lock the pilot scope and first-30-day success criteria named in
    `demo-call-pmp-q1-011`.

#### RevOps

Business prompt:
`Tell me whether this 16-call quarter is structurally complete enough for a
leadership walkthrough, where the lifecycle evidence is thin, and what cleanup
should happen before anyone treats it like forecast support.`

- Command: `go run ./cmd/gongctl analyze calls --db "$PULSARIS_DB" --group-by lifecycle`
  Inspect: new-logo, renewal, expansion, closed-won review, and
  customer-success distribution.
  Expected interpretation: the sample should be structurally complete enough
  for lifecycle storytelling even though it is intentionally small.
  Actionable output: produce a one-line caveat that this sample is reliable for
  stage-flow and lifecycle demos, not quota-style math.
- Command: `go run ./cmd/gongctl analyze coverage --db "$PULSARIS_DB"`
  Inspect: transcript coverage rate and lifecycle buckets.
  Expected interpretation: this sample should show full coverage across calls, with
  variation driven by topic depth and bucket size, not missing-transcript gaps.
  Actionable output: mark each lifecycle bucket `analysis-ready`,
  `usable but thin`, or `needs more evidence`.
- Command: `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-ris-ren-001 --limit 10`
  Inspect: whether the renewal motion is supported by a distinct call thread.
  Expected interpretation: renewal should be present and distinct from the
  flagship new-logo thread.
  Actionable output: decide whether renewal is safe to show live or should be
  caveated first.
- Command: `go run ./cmd/gongctl search calls --db "$PULSARIS_DB" --crm-object-type Opportunity --crm-object-id opp-bmn-exp-001 --limit 10`
  Inspect: whether expansion evidence is distinct from customer-success
  activity.
  Expected interpretation: expansion should answer an adjacent-workflow
  question, not look like a seat-count upsell.
  Actionable output: note whether expansion is usable as a separate proof lane.
- Command: `go run ./cmd/gongctl analyze transcript-backlog --db "$PULSARIS_DB" --limit 10`
  Inspect: priority score, confidence, lifecycle bucket, and reason.
  Expected interpretation: rank buckets by reporting impact and confidence so cleanup
  prioritizes leadership narrative risk first.
  Actionable output: create a cleanup queue with `sync first` and `can wait`
  ordering.
- Next actions to leave the demo with:
  - Label each lifecycle bucket with a readiness state before reusing it in a
    leadership deck.
  - Put renewal and expansion evidence cleanup ahead of extra new-logo volume.

#### Marketing

Business prompt:
`Extract the repeatable proof spine from this 16-call story, show which buyer
language belongs in positioning, and turn the strongest objection cluster into
one field asset for Sales handoff.`

- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "audit" --limit 10`
  Inspect: audit language from discovery, proposal, and renewal calls.
  Expected interpretation: audit pressure is the cleanest cross-stage theme
  because it shows up in both entry pain and retention value.
  Actionable output: make `audit readiness without approval chaos` the first
  message pillar.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "validation" --limit 10`
  Inspect: objections around validation package, QA signoff, attributable
  approvals, and cloud-software adoption.
  Expected interpretation: the objection is not generic cloud resistance; it is
  fear that validation deliverables and signoff ownership will break.
  Actionable output: write one objection-handling talk track and CTA for a
  validation checklist or signoff-readiness asset.
- Command: `go run ./cmd/gongctl calls show --db "$PULSARIS_DB" --call-id demo-call-pmp-q1-012 --json`
  Inspect: the internal win-review language about audit readiness plus
  launch-risk reduction.
  Expected interpretation: this is the cleanest internal proof of why the deal
  was won.
  Actionable output: turn the win language into the second message pillar.
- Command: `go run ./cmd/gongctl search transcripts --db "$PULSARIS_DB" --query "complaint" --limit 10`
  Inspect: discovery-stage pain language around complaint backlog, CAPA
  ownership, and delayed clinical visibility.
  Expected interpretation: this is the strongest problem-formation evidence for
  why the platform matters before procurement or rollout.
  Actionable output: name the field asset and its promise, for example
  `Complaint-to-CAPA launch-risk checklist`.
- Command: `go run ./cmd/gongctl analyze calls --db "$PULSARIS_DB" --group-by month`
  Inspect: when proof points cluster during the quarter.
  Expected interpretation: January forms the problem, February validates the
  approach, and March provides the strongest commercial proof.
  Actionable output: turn that timing into a three-touch follow-up sequence for
  demo registrants or field sellers.
- Next actions to leave the demo with:
  - Draft one message house with pillars for `audit readiness`,
    `validation-ready workflows`, and `launch-risk reduction`.
  - Produce one Sales-facing asset brief grounded in calls `001-002`,
    `006-007`, `010`, and `012`.
