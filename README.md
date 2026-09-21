# BidOS — AI Bid Intelligence & Proposal OS

RFPs in. Decision-ready proposals out. BidOS turns an RFP or questionnaire into span-anchored
requirements, retrieves approved evidence, drafts answers with citations, verifies every factual
sentence against the cited text, routes gaps to subject-matter experts, assembles the proposal,
runs a QA gate and exports — including writing answers back into the buyer's original workbook.

One Go binary. SQLite. No keys required: a deterministic demo provider is always the floor.

## Quick start

```bash
cd bidos-v2
go build -o bidos .
./bidos serve            # http://localhost:8080 — seeds the synthetic demo on first start
```

Open the landing page and press **Launch Synthetic Demo**: it signs you in as the proposal
manager and runs the full pipeline (with auto-approved gates, synthetic data only) to Gate 4.
Or sign in at `/login` as any demo role.

Everything in the demo is synthetic: Northstar Cloud Systems answering Orion Logistics' RFP
(83 requirements, 13 trap questions with no evidence, one hidden prompt-injection line) plus a
28-row vendor security questionnaire that round-trips into its original XLSX.

## Commands

```text
bidos serve            web UI + in-process worker (WORKERS=0 to run workers separately)
bidos worker           job worker only
bidos seed | reset     create / recreate the demo workspace
bidos eval [--provider demo] [--suite all|extraction,injection,retrieval,answers,library]
bidos verify-demo      run the pipeline on an isolated copy of the demo and print the result
bidos tick             fire due schedules + drain jobs once (host cron)
bidos backup <file>    consistent SQLite copy
```

Configuration: copy `.env.example` to `.env`. Providers: `LOCAL_MODEL` (Ollama etc.),
`AI_BASE_URL` + `AI_MODEL` + `AI_API_KEY`, or `ai.providers.json` (see
`ai.providers.example.json`). Switch the whole demo to the law-firm vertical with
`VERTICAL_PACK=legal-services`.

## How it stays honest

- **Evidence gate.** A chunk counts as evidence only if it covers the requirement's salient
  terms and mentions its hard terms (numbers, acronyms, proper nouns); the cited set must cover
  every hard term or the answer stops at *Needs evidence* with a generated SME question.
- **Claim verifier.** Every factual sentence must carry a `[c#]` citation, its numbers and hard
  terms must appear in the cited chunk, and lexical overlap must clear a threshold. Unsupported
  sentences are highlighted, counted and block export.
- **Labels from facts.** Strong / Moderate / Weak / Insufficient evidence derive from verifier
  and retrieval facts, never from model confidence.
- **Untrusted buyer documents.** Instructions inside RFPs are filtered, never followed.
- **Router.** local model → configured providers → demo floor; sensitivity gate, cache, token
  budgets, schema validation with one repair, logged fallbacks. Keys never reach the browser.

## Eval results (demo provider, saas-it pack, 2026-09-19)

| Metric | Value | Target |
|---|---|---|
| extraction recall / precision | 1.00 / 1.00 | ≥ 0.90 / ≥ 0.85 |
| mandatory flag accuracy | 0.95 | ≥ 0.90 |
| injection blocked | 1.00 | 1.00 |
| retrieval recall@5 | 1.00 | ≥ 0.85 |
| citation precision | 0.98 | ≥ 0.90 |
| unsupported claim rate | 0.00 | ≤ 0.05 |
| trap refusal (13 traps) | 1.00 | 1.00 |
| false claims | 0 | 0 |
| library reuse on second RFP | 0.21 | report only |

Run `bidos eval --provider demo` to reproduce; reports land in `evals/reports/`. Live-provider
numbers are recorded in `spec/Memory.md` once a provider is configured.

## Layout

```text
main.go                 CLI: serve / worker / seed / reset / eval / verify-demo / tick / backup
internal/config         .env + environment
internal/store          SQLite (modernc, pure Go), schema.sql, jobs table with leases
internal/docs           PDF/DOCX/XLSX/CSV/TXT parsers, writers, round-trip fill, chunking
internal/extract        rule-based span-anchored extraction, injection filter, addendum diff
internal/retrieval      BM25 + hashed n-gram (or remote) embeddings, RRF, evidence gate
internal/ai             prompts, schemas, router, OpenAI-compatible caller, demo floor, verifier
internal/verticals      embedded packs: saas-it, legal-services
internal/app            services (auth/RBAC, bids, knowledge, answers, tasks, proposal, QA, export…)
internal/jobs           DB job runner (leases, retries, schedules)
internal/pipeline       INTAKE → … → GATE_4 → EXPORT → LEARN, idempotent steps
internal/demo           deterministic seeder
internal/evals          golden sets + eval suites + reports
internal/integrations   Slack, ClickUp, error webhook, inbox watcher
internal/web            net/http UI: templates, CSS, SVG charts, sessions, CSRF, rate limit
spec/                   product, architecture, rules, design, phases, memory
```

## Security notes

Sessions are HttpOnly cookies; every POST needs a CSRF token bound to the session; roles are
enforced server-side per action; every query is organization-scoped (cross-org URLs are 404);
POSTs are rate-limited per IP; uploads are size- and type-checked; provider keys stay in the
environment. Set `SESSION_SECRET` in production.
