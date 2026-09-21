# Automation.md — Bid Pipeline Orchestration (v2)

## 1. Goal

v1 described features a user clicks through one by one. v2 turns them into a
**pipeline that runs itself up to each human gate**: drop in an RFP, and the system
parses, extracts, qualifies, reuses or drafts every answer, routes the gaps to the right
SMEs, chases deadlines, and blocks export until QA passes. Humans only do what humans
must: confirm, decide, supply missing facts, approve.

## 2. Pipeline state machine

```text
INTAKE ──► PARSE ──► EXTRACT ──► [GATE 1: confirm requirements]
                                          │
                                          ▼
                                     QUALIFY ──► [GATE 2: bid / no-bid]
                                                       │ bid
                                                       ▼
                     ┌──────────── AUTO_DRAFT (per requirement) ────────────┐
                     │  1. reuse approved library answer if near-duplicate  │
                     │  2. else retrieve → draft → verify claims            │
                     │  3. triage by evidence label                         │
                     └──────────────────────────┬───────────────────────────┘
                                                ▼
                         ROUTE: Strong → In Review; Moderate → Writer;
                                Weak/Insufficient → SME task + generated question
                                                ▼
                             [GATE 3: reviewer approvals]  ◄── SME answers loop back
                                                ▼                (re-draft + re-verify)
                                         ASSEMBLE ──► QA ──► [GATE 4: export sign-off]
                                                                     ▼
                                                        EXPORT ──► LEARN
```

`LEARN` writes newly approved answers into the Answer Library with owner and expiry,
so every bid makes the next one faster. This is the flywheel incumbents sell.

## 3. Execution model

- Each stage is a **job** in a DB-backed queue (see Architecture §Background jobs).
- `PipelineRun` records the run; `PipelineStep` records each stage per entity with
  status, attempts, input hash, output ref, and error.
- **Idempotency:** a step's key is `(stage, entityId, inputHash)`. Re-running the pipeline
  after a crash or an edit skips steps whose inputs did not change.
- **Fan-out:** AUTO_DRAFT fans out one job per requirement with a concurrency limit taken
  from the AI router's budget, so free-tier limits throttle the pipeline instead of
  breaking it.
- **Gates** are explicit `WAITING_FOR_HUMAN` states with an assignee and a due time.
  Nothing passes a gate automatically. Gates can be pre-approved only in demo mode.
- **Resumability:** "Resume pipeline" continues from the first non-succeeded step.

## 4. Event triggers

| Event | Automatic reaction |
|---|---|
| RFP file uploaded | start PipelineRun at PARSE |
| Addendum uploaded | diff against current requirements → mark changed/new/removed → re-run AUTO_DRAFT only for affected items → notify owners |
| Knowledge document approved or updated | find answers citing its old chunks → mark `Needs Re-review` |
| Knowledge document expires | QA warning on every answer citing it |
| SME submits answer | re-draft + re-verify that requirement → move to In Review |
| Task overdue | escalate to proposal manager; include in digest |
| Deadline T-72h / T-24h | readiness summary to owner (blockers, unanswered mandatory items) |
| QA blocker resolved | recompute readiness; unlock export gate if zero blockers |

## 5. Scheduled jobs

- **Deadline watcher** (hourly): overdue tasks, approaching bid deadlines, clarification
  question deadlines.
- **Daily digest** (per user, in-app; optional email/Slack adapter): my tasks, what
  changed, what is blocking.
- **Library hygiene** (weekly): answers past review date, documents past expiry,
  near-duplicate library entries to merge.

On serverless hosts, schedules are driven by the host's cron hitting
`POST /api/cron/tick` (secret-protected). Locally, the worker process runs them.

## 6. Optional intake automation

- **Email intake adapter** (optional, free Gmail/Outlook API): a dedicated inbox label;
  new messages with RFP attachments create a draft Bid in INTAKE awaiting confirmation.
  Never auto-starts drafting for an unconfirmed sender.
- **Folder watch adapter** (local mode): drop files into `storage/inbox/`.

## 7. Pipeline UI

- A horizontal stage tracker on the bid header (stage, % complete, current gate owner).
- A run log drawer: every step, timing, provider mode (demo/local/free-tier), retries.
- A "Run full pipeline" primary action in demo mode that plays the whole flow on the
  synthetic RFP in under two minutes with gates auto-approved and clearly labelled.

## 8. What the automation must never do

- Submit anything outside the system.
- Mark a requirement satisfied without a verified citation.
- Approve on a human's behalf outside demo mode.
- Send confidential content to a provider not cleared for it.
