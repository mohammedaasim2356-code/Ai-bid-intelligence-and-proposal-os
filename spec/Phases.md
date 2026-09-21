# Phases.md — Step-by-Step Build Plan (v2)

## How the coding agent must use this file

Build one phase at a time. Do not jump ahead because later phases look more interesting.

For each phase:
1. Read the requirements and any referenced spec files.
2. Inspect the existing code.
3. Implement the smallest complete slice.
4. Run the listed checks (and `npm run eval -- --provider demo` from Phase 8 onward).
5. Fix failures.
6. Update `Memory.md`.
7. Only then continue.

### What changed from v1
v1 had 18 phases and put the AI core at Phase 8, evals nowhere, and automation as a
list of manual screens. v2 reorders around a **critical path to a demoable, measurable
core** (Milestone 1), then layers automation and differentiators, then polish.
Old dashboard + analytics phases are merged; old hardening + auth phases are merged;
old portfolio + sales phases are merged.

```text
MILESTONE 1  "It works and it's honest"      Phases 0–8
MILESTONE 2  "It runs itself"                Phases 9–15
MILESTONE 3  "It sells"                      Phases 16–20
```

Stop at each milestone, run the full demo walkthrough, and record results.

---

## MILESTONE 1 — Demoable, measurable core

### Phase 0 — Repository and developer contract
Tasks: Next.js + strict TypeScript, lint/format, Tailwind + UI primitives, Prisma +
SQLite, `.env.example`, README, `/spec` folder with all ten spec files, health route,
empty `scripts/worker.ts`, empty `evals/` skeleton.
Acceptance: install, dev, typecheck and lint pass; home page renders.
Do not build: AI, integrations, dashboards.

### Phase 1 — Data model and deterministic seed
Tasks: Prisma schema for all v1 + v2 entities (Architecture §4); seed script;
Northstar Cloud Systems; users per role; Orion RFP with 60–100 requirements;
synthetic knowledge docs with expiry dates; **≥10 trap questions** with deliberately
absent evidence; **≥1 prompt-injection line** hidden in the synthetic RFP;
`demo:seed`, `demo:reset`.
Acceptance: DB builds from scratch; seed is byte-identical across runs; reset leaves no
stale rows; no real companies.

### Phase 2 — Shell and demo launch
Tasks: global and bid navigation, DEMO banner, breadcrumbs, responsive sidebar,
empty/loading/error states, "Launch Demo Workspace", "Reset Demo", "How this works"
visual, sample RFP preview.
Acceptance: every route loads without external APIs; a first-time visitor reaches the
requirement workspace in ≤ 3 clicks.

### Phase 3 — Document ingestion
Tasks: upload UI, type/size validation, local storage adapter, parsers for
PDF/DOCX/XLSX/CSV/TXT/MD, normalised `ParsedDocument`, chunking with source locators
(page / section / sheet + cell range), `textHash`, parse-failure messages,
`trust = untrusted_external` for buyer files.
Acceptance: a fixture per format passes parsing tests; XLSX locators are cell-accurate
(needed for round-trip export in Phase 14).

### Phase 4 — Span-anchored extraction and requirement matrix
Tasks: extraction schema with `sourceSpan`; deterministic rule pass (Architecture
§Requirement extraction); anchor check; requirement review UI; edit text/category/
mandatory; owners/reviewers; "possible missed requirements" list.
Acceptance: full matrix can be inspected, edited and saved; unanchored items are
visibly flagged; the injection line does not become a requirement.

### Phase 5 — Knowledge base and hybrid retrieval
Tasks: library UI, categories/tags, approval state, expiry; FTS5 BM25 behind
`LexicalIndex`; local embeddings behind `ENABLE_EMBEDDINGS`; RRF fusion; filters;
evidence panel with snippet highlighting.
Acceptance: for any requirement, relevant excerpts appear; unapproved/expired content is
excluded when configured; works with embeddings disabled.

### Phase 6 — AI Router
Tasks: `AiRouter` (`AI-Providers.md`): provider config + validation, sensitivity gate,
cache, token-bucket guard, schema validation + one repair, fallback chain,
`ProviderCall` logging, Settings → AI Providers status page. Implement `DemoAiProvider`,
the generic `OpenAICompatibleProvider`, and verify it against **one** local or free-tier
endpoint.
Acceptance: pulling the key or stopping the local model mid-run falls back cleanly; the
UI labels every result's mode; no key ever reaches the browser.

### Phase 7 — Grounded answers and claim verification
Tasks: context assembly with delimited evidence blocks; draft with `[c#]` markers;
`ClaimVerifier`; evidence labels; `NEEDS_EVIDENCE` path with generated SME question;
citations stored as chunk references; draft + evidence side by side with unsupported
sentences highlighted; regenerate, edit, approve, reject. Live-model extraction pass
(Phase 4 step 2) is enabled here through the router.
Acceptance: every demo answer has stored citations; every trap question ends in
`NEEDS_EVIDENCE`; a hand-inserted false number in a draft is flagged `Unsupported`.

### Phase 8 — Eval harness
Tasks: golden sets (`Evals.md`), runners per suite, JSON + markdown reports, `EvalRun`
records, in-app Quality page, CI gate on the demo provider.
Acceptance: `npm run eval -- --provider demo` passes targets; one live-provider report is
recorded in `Memory.md` with honest numbers.

**MILESTONE 1 CHECK:** walk PRD §9 steps 1–7 and 15–16 end to end, in demo mode and in
one live mode.

---

## MILESTONE 2 — Automation and differentiators

### Phase 9 — Job runner and pipeline orchestration
Tasks: `DbJobRunner` with leases, `npm run worker`, `/api/cron/tick`; `PipelineRun` /
`PipelineStep`; stages and gates from `Automation.md`; idempotency keys; fan-out with
router-aware concurrency; stage tracker and run-log drawer; "Run full pipeline" demo
action.
Acceptance: kill the worker mid-run, restart, and the run resumes without duplicating
work; the synthetic RFP reaches Gate 3 automatically.

### Phase 10 — SME workflow and review
Tasks: category→owner routing map, SME inbox, generated SME questions, comments, due
dates, status transitions, reviewer approval, in-app notifications, SME submission
triggers re-draft + re-verify.
Acceptance: `Needs Evidence → Assigned → SME Working → Answer Updated → Reviewer Review →
Approved` works with no manual re-generation step.

### Phase 11 — Answer Library
Tasks: promote approved answers, review-by dates, near-duplicate matching (embedding +
lexical), "reused from" provenance, reuse still verified, library hygiene job.
Acceptance: running the pipeline on a second synthetic RFP reuses library answers and the
reuse rate appears in the eval report.

### Phase 12 — Proposal editor
Tasks: section model from the vertical pack, section navigation, editable blocks, insert
approved answers, citations as footnotes, reorder, visible placeholders, metadata.
Acceptance: a complete synthetic proposal can be assembled from approved blocks.

### Phase 13 — QA engine
Checks: v1 list (missing mandatory answer, weak/absent evidence, missing citation,
placeholder text, stale evidence, contradictions, missing attachment, length, unresolved
SME task, missing section) **plus** unsupported sentences, expired library entries,
inconsistent names, and pack-specific checks (e.g. legal client disclosure).
Tasks: check interface, deterministic checks, severity, findings UI, resolve/dismiss
with reason, deep links, export gate on zero blockers.
Acceptance: every finding links to the exact entity; export is blocked while blockers exist.

### Phase 14 — Export, round-trip and audit trail
Tasks: DOCX, Markdown, JSON, CSV matrix, print view; **questionnaire round-trip** into the
buyer's original XLSX/DOCX using source locators; export history; activity log;
approval provenance.
Acceptance: exports reopen cleanly; the round-trip XLSX differs from the original only in
answer cells (verified by an automated cell diff).

### Phase 15 — Addenda, clarifications and schedules
Tasks: addendum upload + requirement diff, `changeState`, targeted re-draft, clarification
question tracker, deadline watcher, escalations, daily digest, knowledge-update
re-review trigger.
Acceptance: uploading the synthetic addendum marks exactly the affected answers and
re-drafts only those.

**MILESTONE 2 CHECK:** the full PRD §9 walkthrough, including "Run full pipeline",
addendum handling and round-trip export.

---

## MILESTONE 3 — Portfolio, vertical, production readiness

### Phase 16 — Dashboard and ROI analytics
Tasks: active bids, deadlines, pipeline stage board, requirement completion, evidence
coverage, review queue, QA blockers, automation rate, library reuse rate, estimated
hours saved with editable baseline hours and loaded rate, cycle time when real timestamps
exist.
Acceptance: metrics reflect DB state; every estimate is labelled
`Estimated operational savings`; the formula is visible and editable.

### Phase 17 — Legal-services vertical pack
Tasks: pack structure from `Verticals.md`, Halden & Crewe synthetic firm, synthetic panel
RFP, categories, rubric, sections, `clientDisclosure` enforcement, conflicts gate item,
golden set.
Acceptance: switching `VERTICAL_PACK` swaps the whole demo with no domain-code changes;
legal golden set passes; a `do_not_use` client name is blocked by QA.

### Phase 18 — Optional integrations
Candidates: Trigger.dev, email intake, Slack/digest delivery, ClickUp, Google Drive,
SharePoint, CRM. Each is an adapter with a fallback.
Acceptance: removing credentials never breaks the local demo.

### Phase 19 — Auth, multi-user and hardening
Tasks: auth abstraction, org context, RBAC, server-side authorization, sessions, easy
demo login, public-demo sandbox orgs with expiry, PostgreSQL compatibility test, object
storage adapter, rate limiting, upload hardening, structured logging, error-monitoring
adapter, backups, security and accessibility review.
Acceptance: no cross-org access by URL manipulation; the app deploys on one of the two
paths in Architecture §12 with all critical functionality intact.

### Phase 20 — Portfolio and sales package
Tasks: landing page, interactive architecture diagram, synthetic case study,
before/after workflow, product tour, ROI calculator, the live Quality page as a proof
point, 3-minute and 10-minute demo scripts, one-page overview, security/data-handling
overview, implementation scope and pilot plan.

3-minute demo script:
1. Drop the synthetic RFP in; start the pipeline.
2. Show extraction with source spans (and the ignored injection line).
3. Open a strong answer: evidence side by side, verified sentences.
4. Open a trap question: `Needs Evidence` + generated SME question.
5. Answer as the SME; watch re-verify and move to review. Approve.
6. Upload the addendum; show the targeted re-review.
7. Run QA, fix a blocker, export the round-trip questionnaire.
8. Close on the Quality page and the estimated-savings model.

---

## Definition of done (V1)

- Clean checkout → local setup works.
- Demo mode and one $0 live-AI mode both run the full walkthrough.
- Extraction is span-anchored; answers are claim-verified; traps refuse correctly.
- Pipeline runs to each gate unattended and resumes after a crash.
- SME loop, library reuse, editor, QA gate, all exports and round-trip work.
- Eval targets met and recorded; analytics are transparent and labelled.
- Core tests and accessibility basics pass.
- `Memory.md` reflects reality.
