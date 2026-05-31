# Business User Quickstart

Use this guide when your team has connected a reviewed Gong evidence workspace
to ChatGPT, Claude, or another approved assistant. You do not need Gong
credentials, database access, command-line tools, or raw transcript files.

## Before You Ask

- Ask about the reviewed call dataset your operator made available.
- Treat answers as evidence-assisted analysis, not a live Gong report.
- Use account or prospect names only when you already know the name and are
  approved to ask about it.
- Ask for transcript-backed evidence when you need customer-facing claims.
- If the answer says coverage is missing or stale, ask your operator to refresh
  the data or review the profile.

## What To Ask First

Copy one of these prompts into your approved assistant.

### Sales

```text
What are the most common buyer questions in the reviewed calls? Group them by
topic, show which ones have transcript-backed evidence, and call out where the
data is too thin to trust.
```

```text
What objections are showing up around pricing, implementation timeline,
security review, and integration effort? Use only reviewed call evidence and
separate direct transcript quotes from directional summaries.
```

```text
For the named account I provide, summarize what they have said across reviewed
calls about timeline, implementation risk, buying process, and next steps. Do
not search for or list other account names.
```

### Marketing

```text
What themes from reviewed customer and prospect conversations could support a
thought-leadership post or campaign brief? Include evidence strength,
representative snippets when allowed, and limitations.
```

```text
Which industries or personas show the strongest evidence for problems around
manual work, integrations, reporting, or implementation complexity? Report
missing persona or industry coverage before making recommendations.
```

```text
Build a short evidence-backed brief on one theme I provide. Include what the
calls support, what is only directional, and what we should not claim yet.
```

### Sales Enablement

```text
What recurring questions or objections should sales reps be prepared to handle?
Rank them by evidence quality and include suggested follow-up discovery
questions.
```

```text
Which parts of the reviewed calls would be useful for coaching: discovery,
pricing, implementation, security, or close-plan conversations? Explain what
the available evidence can and cannot prove.
```

### RevOps

```text
Where is transcript coverage weakest by lifecycle stage, call type, or time
period? Prioritize the gaps that most affect sales and marketing analysis.
```

```text
Can we reliably separate open pipeline, closed won, closed lost, renewal,
upsell, and customer-success conversations in the reviewed data? Name any
profile or coverage issues that block confidence.
```

## How To Read The Answer

| Label in the answer | What it means |
| --- | --- |
| Transcript quote | Strongest evidence. Use this for customer-facing claims only when attribution and approval are clear. |
| Gong AI summary or highlight | Directional evidence. Useful for exploration, but weaker than a direct quote. |
| Aggregate or rollup | Good for counts, trends, and coverage, but not a direct customer statement. |
| Coverage caveat | The assistant found missing transcripts, missing CRM fields, stale data, or thin evidence. Treat the answer as incomplete. |

## Good Follow-Ups

```text
Show me only the findings that are supported by direct transcript quotes.
```

```text
What should we avoid claiming because the evidence is only directional?
```

```text
What data refresh or profile review would make this answer more reliable?
```

```text
Turn this into a sales enablement summary, but keep the limitations visible.
```

## What Not To Ask

- Do not ask for raw transcripts, raw Gong exports, full customer lists, object
  IDs, call IDs, or database files.
- Do not ask the assistant to guess missing CRM outcomes, industries,
  personas, titles, or account status.
- Do not ask for live Gong changes. This workspace is read-only for business
  users.
- Do not use the output as final customer-facing language unless the evidence,
  attribution, and approval path are clear.

## When To Ask For Help

Ask the pilot operator or RevOps owner when the assistant says:

- the cache is stale
- transcript coverage is missing
- profile or lifecycle mapping is incomplete
- the requested tool is unavailable
- the answer is based only on directional summaries
- account, persona, industry, or opportunity fields are missing

For the full pilot boundary and operator-facing details, see
[Pilot sponsor and operator guide](pilot-sponsor-and-operator-guide.md).
