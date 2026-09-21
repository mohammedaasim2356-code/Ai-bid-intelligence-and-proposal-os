# CHANGELOG — v1 → v2

## New files
- `AI-Providers.md` — free-tier AI router: local model → free tiers → optional paid → demo floor; cache, budgets, sensitivity gate, local embeddings.
- `Automation.md` — end-to-end pipeline state machine with human gates, event triggers, scheduled jobs, optional email intake.
- `Evals.md` — golden sets, trap questions, metrics, targets, CI gate.
- `Verticals.md` — configurable industry packs; `legal-services` pack specified.

## Changed
- **PRD:** added Answer Library, addenda/clarifications, questionnaire round-trip export, pipeline automation, free-tier live-AI mode, vertical packs, three new success criteria, two new principles.
- **Architecture:** hybrid retrieval (BM25 + local embeddings + RRF), span-anchored hybrid extraction, deterministic claim verifier with concrete evidence-label criteria, DB-backed job runner, new entities, prompt-injection boundary, corrected free-hosting paths, per-visitor demo sandboxes.
- **Rules:** Rules 11–16 (untrusted documents, provider data sensitivity, span anchoring, reuse-before-generate, eval gate, free-tier resilience); new feature flags.
- **Phases:** 18 → 21 phases in 3 milestones; AI router and evals moved onto the critical path; merged dashboard/analytics, auth/hardening, portfolio/sales.
- **Memory:** sections for router state, pipeline state, eval results, vertical packs.

## Unchanged by design
Stack, design system, demo-first rule, human approval, honest metrics, non-goals.

## 2026-09-19 — Session 2 (Go build)

- Fixed pipeline bugs B1–B5 (SME statement pinning, word-start keyword classification, intro
  extraction, addendum targeting, stale library reuse); minimal covering evidence set.
- Added `main.go` CLI (serve, worker, seed, reset, eval, verify-demo, tick, backup).
- Added the stdlib web UI (`internal/web`): 24 pages, three themes, SVG charts, sessions,
  CSRF, RBAC, org scoping, rate limiting, run/job polling; httptest smoke test of every page.
- Wired evals to the CLI and the Quality page; demo evals pass for both packs.
- Added the `legal-services` vertical pack with guardrail tests.
- Added README, `.env.example`, `ai.providers.example.json`, GitHub Actions workflow.
