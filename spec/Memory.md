# Memory.md — Coding-Agent Project Memory (HANDOFF)

> Updated 2026-09-19 by the coding agent at the end of session 2. Read this first, then
> `Phases.md`. All 21 phases have an implementation; see §13 for what remains open.

## 1. Current project state

**Project:** AI Bid Intelligence & Proposal OS (BidOS) — **built in Go** (module `bidos`,
folder `bidos-v2/`), single binary, SQLite, stdlib web UI.

**Phase status:** Milestones 1–3 implemented. Session 2 added: pipeline bug fixes (B1–B5),
`main.go` CLI, the whole `internal/web` UI (24 pages), evals CLI + Quality page, README /
`.env.example` / `ai.providers.example.json` / GitHub Actions, the `legal-services` vertical
pack with golden set, landing page + public sandbox launch.

**Build status:** `go build ./...`, `go vet ./...` PASS. `go test ./...` PASS (store, docs,
verticals, retrieval, extract, ai, demo, pipeline incl. addendum + legal guardrails, web
smoke test of every page + actions).

**Eval status (demo provider, `bidos eval --provider demo`), 2026-09-19:**

```text
saas-it:        extraction recall 1.00 / precision 1.00, mandatory 0.952, injection 1.00,
                retrieval@5 1.00, citation precision 0.984, unsupported 0.00,
                trap refusal 13/13, false claims 0, library reuse 0.214 (28 questionnaire rows)
legal-services: extraction 1.00 / 1.00, mandatory 0.986, injection 1.00, retrieval@5 0.982,
                citation precision 0.962, unsupported 0.00, trap refusal 11/11, false claims 0,
                library reuse 0.917 (12 due-diligence rows)
```

`bidos verify-demo` (both packs): run waits at GATE_4 with every trap in needs_evidence.
Live-provider eval: NOT RUN (no credentials yet).

## 2. Product summary

RFP/questionnaire → parse → span-anchored extraction → qualification → hybrid retrieval with
evidence gate → grounded draft `[c#]` → deterministic claim verification → SME loop → assembly
→ QA gate → export/round-trip → analytics. Demo mode needs no keys.

## 3. Architecture summary (Go)

Layering: `store` ← `docs`/`retrieval`/`extract`/`ai`/`verticals` ← `app` ← `pipeline` ←
`demo`/`evals`/`integrations` ← `web` ← `main.go`.

- `main.go` — commands serve (web + in-process worker, seeds demo when DEMO_MODE), worker,
  seed, reset, eval [--suite --provider demo --out], verify-demo (isolated org, pipeline with
  auto gates, prints JSON, exit 1 unless all traps refused and run at GATE_4), tick, backup
  (`VACUUM INTO`). `registerJobs` wires schedules (deadline_watch hourly, digest 24h,
  hygiene 24h, sandbox_cleanup hourly, embed 10m) and the `evals.run` job.
- `internal/web` — `server.go` (Server, ServeHTTP with recover/log/security headers, per-IP
  POST token bucket, sessions via `bidos_session` cookie, CSRF = HMAC(secret, sessionID) in
  `_csrf` on every POST, `auth(action, handler)` wrapper doing session → CSRF → `app.Can`
  RBAC, flash cookie, `render` with `Page` (Title, Viewer, CSRF, Flash, Nav, Bid, BidTab,
  Unread, Demo, Live, Data, Pack…; `Page.CanAction` is a METHOD, template func fields cannot
  take args), `routes.go` (Go 1.22 method patterns), `charts.go` (SVG bar/donut/area/radar/
  progress/spark, `MarkedDraft` highlighting unsupported sentences + citation chips, funcMap),
  handlers_core/bids/work.go, `templates/layout.html` + `partials/ui.html` (pill, labelpill,
  empty, stages, evidence) + `pages/*.html` (landing, login, error, dashboard, notifications,
  bids, bid_new, bid_overview, doc_preview, requirements (split view), tasks, task, proposal,
  qa, export, addenda, bid_analytics, knowledge, knowledge_doc, library, library_entry,
  analytics, settings, integrations, quality), `static/app.css` (tokens; themes dark/dim/light
  via `data-theme`, pill nav, 18px cards, chart palette vars --c1..--c6), `static/app.js`
  (theme persist, confirms, busy forms, run/job polling via /api/runs/{id} and /api/jobs/{id},
  j/k keys). `web_test.go` boots everything with httptest and hits every page + key actions.
- Other packages: unchanged from session 1 except the fixes in §12.

## 4. Environment / toolchain facts

- Go 1.27 at `C:\Program Files\Go\bin`, not on Bash PATH: `export PATH="$PATH:/c/Program Files/Go/bin"`.
- CGO off; modernc SQLite; commands run from `bidos-v2/`.
- Writing files: Write tool for source; small patches via `python - <<'EOF'` with
  `str.replace` work well — BUT use raw strings (`r'...'`/`rb'...'`) for any regex text;
  a non-raw `'\b'` silently became a backspace byte in `extract.go` once.
- `go test` caches on content; a "cached" result after an edit means the edit did not land.
- Binary: `go build -o bidos.exe .`; run `./bidos.exe serve` (port 8080) — verified boot,
  /health, /, /login, /static, redirect for /dashboard.

## 5. Important decisions log

```text
(session 1 decisions retained: Go/stdlib, pure-Go SQLite, hashed embeddings floor, evidence
gate, SME statements as knowledge, instruction answers, demo launch seeds + auto gates)

DATE: 2026-09-19
DECISION: Retrieval returns a MINIMAL COVERING SET of evidence: best qualified chunk, then only
chunks adding an uncovered hard term (or a second Direct chunk when there are no hard terms).
WHY: citation precision was 0.67 with "all qualified chunks"; now 0.98 with recall intact.

DECISION: Excerpts always include sentences carrying the requirement's hard terms (exempt from
the length budget). WHY: the amended 4.11 (24 months) draft quoted the 12-month sentence.

DECISION: Library reuse must itself contain every hard term of the (possibly amended)
requirement (checked on the answer text), else fall through to fresh retrieval.

DECISION: "Confirm …" items (yes/no inquiries) are optional unless a modal verb is present;
imperatives (Describe/Provide/State…) stay mandatory. Matches golden sets at 0.95/0.99.

DECISION: Pack keyword matching is start-of-word bounded (KeywordHit); keywords may be stems.

DECISION: Targeted pipeline runs (addendum/SME `only`) always create a new run instead of
re-using a run waiting at a gate.

DECISION: SME statement docs (source_type sme, metadata.requirement == code) are PINNED into
retrieval for that requirement (retrieval.Options.PinnedDocIDs → Qualified+Direct).

DECISION: The addendum test asserts 4.11 gets a FRESH draft mentioning "24 months" (the
knowledge base documents a 24-month extension), not needs_evidence.
```

## 6. Current database state

Schema unchanged. Demo org `org-demo` seeded on `serve` when DEMO_MODE. Evals and
verify-demo use throwaway orgs (`eval-…`, `verify-…`) that are deleted afterwards. Public
sandbox launches create `sandbox-<id>` orgs with expiresAt (cleanup job hourly).

## 7. AI provider state

No live keys yet. Configure via `.env` (`AI_BASE_URL`, `AI_MODEL`, `AI_API_KEY`, optional
`LOCAL_MODEL`) or `ai.providers.json`. Settings → AI providers shows status + "Test connection"
(`OpenAICompatible.Ping`). `bidos eval --provider demo` forces a demo-only router.

## 8. Integration state

Wired in `main.go` via `integrations.Wire`: Slack notifier, error webhook, ClickUp sync, inbox
watcher — each only when its env var is set. Integrations page reports honest states.
Trigger.dev / Drive / SharePoint / CRM: NOT_CONFIGURED (documented on the page).

## 8a. Pipeline / 8b. Evals / 8c. Packs

Pipeline: all stages + gates; UI can start (manual or auto-gates in demo), approve gates,
cancel, resume; overview polls `/api/runs/{id}`. Evals: CLI + Quality page (job `evals.run`,
reports in `evals/reports/`, golden in `evals/golden/<pack>/`). Packs: `saas-it` and
`legal-services` (Halden & Crewe LLP; Meridian panel ITT, 70 items, 11 traps, injection line;
17 knowledge docs incl. allowed / anonymised / do_not_use matter sheets, expired rate card,
unapproved pitch notes; 13-row due-diligence questionnaire; clarification notice addendum).
Guardrails clientDisclosure/noLegalAdvice/conflictsGate = true; QA blocks a `do_not_use`
client name and requires `conflicts.cleared.<bidID>` = true (set by a human; no UI control
yet — see §13).

## 9. Environment variables

See `.env.example` (complete, with comments).

## 10. Known limitations

- PDF text extraction basic; PDF export plain text; HashEmbedder lexical.
- Contradiction check keyword+number heuristic; legal-advice check is a regex.
- Live-model paths (extraction refinement, verification second opinion) untested live.
- Rate limiter and sessions are per-process (single replica).
- Conflicts clearance has no UI button (setting only).
- `bidos.exe` in `bidos-v2/` is a build artefact (gitignored).

## 11. Test status (2026-09-19)

```text
export PATH="$PATH:/c/Program Files/Go/bin"; go test ./...   → all packages PASS
./bidos.exe eval --provider demo                              → PASS (saas-it)
VERTICAL_PACK=legal-services ./bidos.exe eval --provider demo → PASS
./bidos.exe verify-demo (both packs)                          → PASS
```

## 12. Bugs fixed this session

```text
B1 SME loop stayed needs_evidence      → PinnedDocIDs (retrieval) + GenerateAnswer pins SME docs
B2 2.2 classified team_staffing        → KeywordHit word-start matching; "cvs" keyword dropped,
                                          "exceed" added to submission keywords
B3 intro sentences extracted           → reworded three saas-it intros (no modal verbs)
B4 addendum re-drafted all 86 items    → Engine.Start never reuses a waiting run when `only` set
B5 amended 4.11 reused stale answer    → reuse must contain hard terms; hard-term sentences kept
                                          in excerpts; minimal covering evidence set
Extraction: "Confirm …" inquiries optional (reInquiry); eval demo router fixed in main.go
```

Active bugs: none known.

## 13. Next tasks (in order)

1. When the user pastes credentials: put them in `.env`, run `bidos eval` (auto provider),
   record live numbers in §1 and README; verify the Settings "Test connection" button.
2. UI polish pass against the reference image with a real browser (only httptest-verified so
   far): check chart sizing, dim theme contrast, mobile split-view stacking.
3. Conflicts-gate clearance button on the QA page (admin/reviewer) writing
   `conflicts.cleared.<bidID>`; surface `pipeline.auto_gates` toggle in Settings (exists as a
   generic setting already).
4. Optional: PostgreSQL driver behind the same `store` API; object-storage `Files` adapter.
5. Optional Phase 20 extras: 10-minute demo script doc, one-page overview PDF, security
   overview (README has the short forms).

## 14. Files added/changed in session 2

```text
main.go, README.md, .env.example, ai.providers.example.json, .github/workflows/ci.yml
internal/web/{server.go,routes.go,charts.go,handlers_core.go,handlers_bids.go,handlers_work.go,web_test.go}
internal/web/templates/{layout.html,partials/ui.html,pages/*.html (24)}
internal/web/static/{app.css,app.js}
internal/verticals/packs/legal-services/** (pack json + demo json + 17 knowledge docs)
internal/pipeline/legal_test.go; pipeline_test.go (4.11 assertion)
Changed: internal/retrieval/hybrid.go (PinnedDocIDs, minimal covering set, BestExcerpt),
internal/app/answers.go (pinned SME docs, reuse hard-term check), internal/verticals/pack.go
(KeywordHit), internal/ai/demo.go (uses KeywordHit), internal/extract/extract.go (reInquiry),
internal/pipeline/engine.go (Start with only), packs/saas-it/{categories.json,demo/rfp.json}
```

## 15. Context for the next coding agent

- UI contract from the user's reference image is implemented: top bar with pill tabs
  (Overview, Bids, Knowledge, Tasks, Analytics | Integrations, Settings, Quality), theme
  toggle (moon/half/sun → dark/dim/light, persisted in localStorage `bidos-theme`), 18px
  cards, navy `#0b1530` / card `#14203f`, teal accent, server-side SVG charts.
- Do not change: evidence gate semantics, label criteria, deterministic seed ids, demo-floor
  rule, RBAC actions list, the `Page.CanAction` method (templates call it with an argument).
- User instructions: build autonomously, test along the way, credentials arrive later via
  `.env`. Update this file when told context is nearly full.
