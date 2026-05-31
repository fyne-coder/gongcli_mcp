# gongctl docs — index

This directory has 36+ documents. Use this index to find the ones that match
your role. If you don't see your role here, the closest fit is **IT / DevOps
operator**.

## Business end-user

You ask the assistant questions; you do not run the CLI or manage credentials.

- [Business-user quickstart](business-user-quickstart.md) — start here

## Power analyst

You design the cohort, the profile, and the tool sequence. You read MCP
output and decide what's trustworthy.

- [Analyst orientation](analyst-orientation.md) — start here
- [Analyst tool reference](analyst-tool-reference.md) — `call_filter`
  fields, `gong_analyze` operations, recommended tool sequences
- [Business profiles](profiles.md) — YAML schema, lifecycle/methodology mapping
- [MCP data exposure](mcp-data-exposure.md) — full tool surface and exposure classes
- [Postgres parity matrix](postgres-parity.md) — feature support per backend
- [Postgres question parity](postgres-question-parity.md) — concise "can I
  ask X on backend Y?" matrix
- [Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md) —
  fuller analyst cohort workflow and worked examples

## IT / DevOps operator

You install, secure, schedule, and maintain it.

- [Remote MCP deployment requirements](remote-mcp-deployment-requirements.md)
  — start here for hosted Claude/ChatGPT connectors and deployment path choice
- [Quickstart](quickstart.md) — Docker-first install and first cache
- [Enterprise deployment](enterprise-deployment.md) — deployment modes,
  storage classes, restricted CLI mode
- [Docker deployment](docker.md) — build, GHCR images, Postgres shared,
  HTTP MCP, single-VM Postgres starter
- [Operator sync runbook](runbooks/operator-sync.md) — preflight, refresh,
  backup, restore, decommissioning
- [Postgres client deployment runbook](runbooks/postgres-client-deployment.md)
  — Postgres-specific bootstrap, scoped reader grants, smoke
- [Postgres Kubernetes operator setup](postgres-kubernetes-operator-setup.md)
  — Kubernetes first-run Jobs, recurring sync, image args, MCP smoke
- [Single-VM Postgres starter](../deploy/single-vm-postgres/README.md) —
  practical all-on-one-VM Compose shape for small company setups
- [Kubernetes Postgres pilot starter](../deploy/kubernetes/postgres-pilot/README.md)
  — Kustomize starter for customer-managed Kubernetes and Postgres pilots
- [Scheduling cache refreshes](scheduling.md) — cron / systemd / launchd /
  K8s CronJob templates
- [Configuration surfaces](configuration-surfaces.md) — what is YAML, what
  is flags, what is env
- [Remote MCP auth and connector setup](remote-mcp-auth.md) — OAuth broker
  options and connector configuration

## Security reviewer

You review the data boundary, credential flow, and capability model.

- [Security checklist](security-checklist.md) — single-page reviewer checklist
- [Security model](security-model.md) — trust boundaries, credential flow,
  data classification, capability model
- [Security questionnaire](security-questionnaire.md) — pre-canned answers
  to typical security-review formats
- [Data boundary statement](data-boundary-statement.md)
- [Data handling](data-handling.md)
- [MCP data exposure](mcp-data-exposure.md) — exposure classes per tool
- [Customer-hosted package](customer-hosted-package.md)
- [Remote MCP auth and connector setup](remote-mcp-auth.md)
- [Remote MCP OAuth troubleshooting](runbooks/remote-mcp-oauth-troubleshooting.md)
  — hosted ChatGPT/OpenAI and Claude/Anthropic connector reachability,
  metadata, token, and first-tool-call failures
- [TradeCentric JumpCloud remote MCP RCA](runbooks/tc-jumpcloud-remote-mcp-rca-2026-05-29.md)
  — direct JumpCloud RCA, live failure ladder, and gateway compatibility
  follow-up from the 2026-05-29 deployment debug
- [JumpCloud Claude direct MCP success RCA](runbooks/jumpcloud-claude-direct-success-rca-2026-05-31.md)
  — final lab evidence, `Client Secret POST` fix, stale connector cleanup,
  and current operator checklist
- [TC/lab JumpCloud direct OIDC review](runbooks/tc-jumpcloud-direct-oidc-review-2026-05-30.md)
  — historical pre-success root-cause ranking, code/security findings, and
  support packet for Claude plus JumpCloud escalation

## Pilot sponsor / RevOps owner

You decide pilot scope, approve profiles, and own the business outcome.

- [Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md)
- [Pilot plan](pilot-plan.md)
- [Customer implementation checklist](implementation-checklist.md)
- [AI governance](ai-governance.md)
- [Postgres client onboarding checklist](postgres-client-onboarding-checklist.md)
- [Postgres Kubernetes operator setup](postgres-kubernetes-operator-setup.md)
- [Postgres client pilot release packet](postgres-client-pilot-release-packet.md)

## Developer (writing code in this repo)

- [Developer orientation](developer-orientation.md)
- [Architecture](architecture.md)
- [Release process](release.md)
- [Postgres parity matrix](postgres-parity.md)

## Examples

- [docs/examples/sync-run.example.yaml](examples/sync-run.example.yaml) —
  scheduled refresh config
- [docs/examples/retention-policy.example.yaml](examples/retention-policy.example.yaml)
  — `cache purge --config` policy
- [docs/examples/business-profile.example.yaml](examples/business-profile.example.yaml)
  — synthetic profile shape
- [docs/examples/ai-governance.example.yaml](examples/ai-governance.example.yaml)
  — AI governance config shape
- [deploy/single-vm-postgres/](../deploy/single-vm-postgres/README.md) —
  simple one-VM Postgres deployment scaffold
- [deploy/kubernetes/postgres-pilot/](../deploy/kubernetes/postgres-pilot/README.md)
  — Kustomize scaffold for customer-managed Kubernetes pilots
- [docs/examples/medical-device-saas/](examples/medical-device-saas/) —
  end-to-end synthetic example dataset

## Project planning

- [Pilot plan](pilot-plan.md) — pilot objective, boundary, scope,
  participants, approved questions
- [Postgres client manual-test checklist](postgres-client-manual-test-checklist.md)
  — operator-facing post-deploy smoke checklist
- [Roadmap](roadmap.md)

## Working with a coding agent

Power users, analysts, and IT / DevOps teams will move faster with a coding
agent (Claude Code, Codex, Cursor, or similar) on hand to scaffold cron
units, K8s manifests, profile YAML edits, and analyst tool sequences from
the templates under `docs/` and `docs/examples/`.

Do not paste real Gong credentials, real customer profile YAML, or real
transcript output into a hosted agent unless your company has approved that
data path.
