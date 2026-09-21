# Architecture.md — AI Bid Intelligence & Proposal OS (v2)

## 1. Architecture objective

Build a modular monolith first. Do not begin with microservices or Kubernetes.

The architecture must support:
- local demo mode
- optional hosted deployment
- optional AI provider integrations
- optional Trigger.dev/background jobs
- optional ClickUp/Slack/CRM integrations
- future migration from SQLite to PostgreSQL
- clear separation between deterministic application logic and probabilistic AI logic

## 2. Recommended stack

### Frontend / application
- Next.js + TypeScript
- React
- Tailwind CSS
- accessible component primitives such as shadcn/ui/Radix where practical
- React Hook Form + Zod for forms/validation

### Backend
Use Next.js route handlers/server actions for the initial modular monolith.

Use separate domain modules rather than a separate backend server unless scale requires it.

### Database
Default:
- SQLite for frictionless local demo
- Prisma ORM

Production path:
- PostgreSQL

Do not write application code that depends on SQLite-only behavior except inside a database adapter.

### Search / retrieval (v2: hybrid, still free)
Pipeline per query:
1. Query build: requirement text + category keywords from the vertical pack.
2. Lexical: SQLite FTS5 BM25 (Postgres: `tsvector` + `ts_rank`) behind a `LexicalIndex` adapter.
3. Semantic: local embeddings (Transformers.js) with brute-force cosine; see `AI-Providers.md` §6.
4. Fusion: Reciprocal Rank Fusion (k = 60) of both lists.
5. Filters: approval state, expiry, latest document version, `clientDisclosure` (legal pack).
6. Optional rerank: a live-model `classify` task scoring top 15 → top 5, only when a live provider is available.
7. Deterministic fallback when embeddings are disabled: BM25 + tag/category boosts.

No vector database in V1.

### AI (v2)
All model access goes through `AiRouter` (`AI-Providers.md`): local model → configured
free tiers → optional paid → `DemoAiProvider`. Domain code depends on task-level functions:

```ts
interface AiProvider {
  extractRequirements(input: DocumentContext): Promise<RequirementExtractionResult>;
  generateAnswer(input: AnswerGenerationContext): Promise<GeneratedAnswer>;
  runQa(input: QaContext): Promise<QaResult>;
  draftSmeQuestion(input: GapContext): Promise<SmeQuestion>;
}
```

`RouterBackedProvider` implements this with `AiRouter`; `DemoAiProvider` implements it
deterministically. Never scatter direct LLM SDK calls throughout the app.

### Requirement extraction (v2: span-anchored hybrid)
1. **Rule pass (deterministic, free):** numbered clauses, modal verbs (shall / must /
   will / required / should / may), question sentences, table rows, and questionnaire
   sheets (detect question/answer columns by header and fill pattern).
2. **Model pass (when a live provider is available):** split compound requirements, merge
   fragments, classify category and mandatory flag. Output must include `sourceSpan`,
   the verbatim source text.
3. **Anchor check:** every item's `sourceSpan` must be found in the parsed document
   (whitespace-normalised). Items that fail are dropped or flagged `UNANCHORED` for human
   review. The model cannot invent requirements.
4. Rule-pass items the model dropped are shown as "possible missed requirements".

### Claim verification (v2)
After drafting, the deterministic `ClaimVerifier` runs on every answer:
1. Split into sentences; mark each as a factual claim or connective wording (rule-based:
   numbers, dates, named certifications/products/clients, capability verbs → factual).
2. Each factual sentence must carry at least one citation marker `[c#]`.
3. For each cited chunk: every number, date, percentage and proper-noun entity in the
   sentence must appear in the chunk (normalised), and lexical overlap must exceed a
   configured threshold.
4. Failing sentences are highlighted `Unsupported`; the answer cannot move past
   `Drafted` until they are fixed, cited, or removed.
5. Optional live-model `verify` task as a second opinion; it can add flags, never remove
   deterministic ones.

Evidence labels (criteria live in code; no fake probabilities):
- **Strong:** every factual sentence verified; ≥ 2 distinct approved sources, or 1 source
  that states the answer directly.
- **Moderate:** every factual sentence verified; single indirect source.
- **Weak:** unsupported sentences were removed, or cited evidence is past expiry.
- **Insufficient:** no approved evidence above the retrieval threshold → `NEEDS_EVIDENCE`.

### Documents
Use mature parsing packages for PDF/DOCX/XLSX/TXT.

Create one normalized internal representation:

```ts
interface ParsedDocument {
  id: string;
  name: string;
  mimeType: string;
  sections: ParsedSection[];
  sourceHash: string;
  metadata: Record<string, unknown>;
}
```

### File storage
Demo:
- local `storage/` directory outside the source-controlled app data.

Production-ready abstraction:
- object storage adapter.

### Background jobs (v2)
`JobRunner` interface; default implementation **`DbJobRunner`**: a `Job` table with
`status`, `runAt`, `leaseUntil`, `attempts`, `maxAttempts`, `idempotencyKey`, `progress`
and `log`. Workers claim jobs with a lease (`UPDATE … WHERE leaseUntil < now()`), so it
works on SQLite and PostgreSQL, survives restarts, and needs no vendor.

- Local: `npm run worker` alongside `npm run dev`.
- Serverless: host cron → `POST /api/cron/tick` drains a bounded batch per tick.
- Optional: Trigger.dev adapter behind the same interface.

States: queued, running, succeeded, failed, retryable, cancelled, waiting_for_human.
Pipeline orchestration on top of jobs is specified in `Automation.md`.

### Integrations
Implement adapters with feature flags:
- Google Drive — future/optional
- SharePoint — future/optional
- Slack — future/optional
- ClickUp — optional task synchronization
- HubSpot/Salesforce — future/optional
- Gmail/Outlook — future/optional

No integration may be required for the demo.

## 3. High-level system flow

```text
                  ┌─────────────────────┐
                  │    Web Application   │
                  │   Next.js + React    │
                  └──────────┬──────────┘
                             │
                  ┌──────────▼──────────┐
                  │  Application Layer  │
                  │ commands / queries  │
                  └──────────┬──────────┘
                             │
        ┌────────────────────┼─────────────────────┐
        │                    │                     │
 ┌──────▼──────┐      ┌──────▼──────┐      ┌─────▼──────┐
 │ Bid Domain  │      │ RFP Domain   │      │ Knowledge  │
 │             │      │              │      │  Domain    │
 └──────┬──────┘      └──────┬───────┘      └─────┬──────┘
        │                    │                     │
        └────────────────────┼─────────────────────┘
                             │
                  ┌──────────▼──────────┐
                  │ AI / Retrieval Layer│
                  │ provider + grounding│
                  └──────────┬──────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
       ┌──────▼─────┐  ┌─────▼──────┐  ┌───▼────────┐
       │  Database  │  │ File Store │  │ Job Runner │
       └────────────┘  └────────────┘  └────────────┘
```

## 4. Core domain model

### Organization
Fields:
- id
- name
- mode (`demo` | `live`)
- createdAt

### User
- id
- organizationId
- name
- email
- role
- createdAt

Roles:
- admin
- proposal_manager
- writer
- sme
- reviewer
- viewer

### Bid
- id
- organizationId
- name
- buyerName
- buyerUrl
- ownerId
- estimatedValue
- deadline
- status
- description
- createdAt
- updatedAt

Statuses:
- intake
- qualification
- drafting
- review
- final_qa
- ready
- closed_won
- closed_lost
- archived

### Document
- id
- organizationId
- bidId nullable
- name
- mimeType
- category
- version
- approvalState
- sourceType
- filePath / objectKey
- hash
- createdAt

### DocumentChunk
- id
- documentId
- sectionPath
- text
- pageNumber nullable
- sheetName nullable
- cellRange nullable
- chunkIndex

### Requirement
- id
- bidId
- requirementCode
- section
- text
- category
- mandatory
- sourceDocumentId
- sourceLocator
- status
- ownerId
- reviewerId
- createdAt
- updatedAt

### Evidence
- id
- requirementId
- documentChunkId
- relevanceScore
- excerpt
- approved

### Answer
- id
- requirementId
- draftText
- finalText
- generationMode
- confidenceLabel
- status
- version
- approvedBy
- approvedAt
- createdAt
- updatedAt

### Task
- id
- bidId
- requirementId nullable
- assigneeId
- type
- status
- dueAt
- description

### Review
- id
- answerId
- reviewerId
- status
- comment
- createdAt

### QAResult
- id
- bidId
- checkType
- severity
- message
- entityType
- entityId
- status
- createdAt

### ExportJob
- id
- bidId
- format
- status
- filePath
- createdAt

### AnswerLibraryEntry (v2)
- id, organizationId, canonicalQuestion, answerText, citationChunkIds, tags, category
- ownerId, approvedById, approvedAt, reviewBy, sourceAnswerId, reuseCount, embedding

### Addendum (v2)
- id, bidId, documentId, receivedAt, diffSummaryJson

### ClarificationQuestion (v2)
- id, bidId, requirementId nullable, question, sentAt, dueAt, buyerResponse, status

### PipelineRun / PipelineStep (v2)
- PipelineRun: id, bidId, status, mode, startedAt, finishedAt
- PipelineStep: id, runId, stage, entityType, entityId, inputHash, status, attempts,
  outputRef, error, startedAt, finishedAt

### Job, ProviderCall, EvalRun (v2)
- Job: see Background jobs. ProviderCall: see `AI-Providers.md` §7.
- EvalRun: id, suite, providerMode, metricsJson, passed, createdAt

### Field additions (v2)
- Requirement: `sourceSpan`, `anchorStatus` (anchored | unanchored | manual),
  `changeState` (unchanged | new | changed | removed) for addenda.
- Answer: `libraryEntryId` nullable, `unsupportedSentenceCount`, `verifiedAt`.
- DocumentChunk: `textHash`, `embedding` (blob), `embeddingModelId`.
- Document: `expiresAt`, `clientDisclosure` (legal pack), `trust` (`internal` | `untrusted_external`).
- Organization: `verticalPackId`, `dataSensitivity`.

### ActivityLog
- id
- organizationId
- userId
- eventType
- entityType
- entityId
- metadataJson
- createdAt

## 5. Folder structure

```text
app/
  (auth)/
  dashboard/
  bids/
    [bidId]/
      overview/
      requirements/
      knowledge/
      answers/
      tasks/
      qa/
      editor/
      analytics/
  demo/
  api/

components/
  ui/
  navigation/
  dashboard/
  bids/
  requirements/
  knowledge/
  editor/
  qa/

lib/
  db/
  auth/
  domain/
    bids/
    requirements/
    answers/
    knowledge/
    qa/
    tasks/
    exports/
  ai/
    router/          # AiRouter, cache, budgets, provider config (v2)
    providers/
    prompts/
    grounding/
    verification/    # ClaimVerifier (v2)
    schemas/
  pipeline/          # state machine, gates, event handlers (v2)
  library/           # Answer Library (v2)
  verticals/         # vertical packs (v2)
  documents/
    parsers/
    chunking/
  retrieval/
  jobs/
  integrations/
    clickup/
    trigger/
    slack/
    drive/
  demo/
  analytics/
  security/
  validation/

prisma/
  schema.prisma
  seed.ts

public/
  demo-assets/

storage/
  demo/

scripts/
  seed-demo.ts
  verify-demo.ts
  worker.ts          # DbJobRunner worker (v2)

evals/               # golden sets, runners, reports (v2)

tests/
  unit/
  integration/
  e2e/

spec/
  PRD.md
  Architecture.md
  Rules.md
  Phases.md
  Design.md
  Memory.md
  AI-Providers.md
  Automation.md
  Evals.md
  Verticals.md
```

## 6. Application workflow

### Flow A — Launch demo
1. User clicks “Launch Demo Workspace.”
2. App checks for seeded demo organization.
3. If absent, creates it and seeds synthetic data.
4. Demo badge is shown globally.
5. Dashboard opens with the synthetic opportunity.

### Flow B — New RFP
1. User creates bid.
2. User uploads documents.
3. Parser creates normalized document sections.
4. Extraction job identifies requirements.
5. Requirements appear in a review queue.
6. User confirms/edits extracted requirements.
7. Bid moves to qualification.

### Flow C — Generate answer
1. User selects requirement.
2. Retrieval collects top evidence chunks.
3. Grounding layer builds context.
4. AI provider generates draft or demo provider returns deterministic draft.
5. Answer record stores draft + evidence references.
6. UI renders draft and citations side by side.
7. User edits/approves.

### Flow D — QA
1. Run QA job.
2. Each check produces a result with severity.
3. Results link to the affected requirement/answer/document.
4. User resolves or dismisses with reason.
5. Dashboard updates readiness.

## 7. Grounding architecture

The most important AI rule is that generation is downstream of evidence retrieval.

Pipeline:

```text
Question
  ↓
Intent/category detection
  ↓
Retrieve relevant document chunks
  ↓
Apply approval/version filters
  ↓
Rank evidence
  ↓
Build grounded context
  ↓
Reuse check (Answer Library near-duplicate)
  ↓
Generate draft with [c#] citation markers
  ↓
ClaimVerifier (sentence-level, deterministic)
  ↓
Assign evidence label → route (review / writer / SME)
  ↓
Persist citations + answer + verification result
```

The system should prefer approved knowledge over generic model knowledge.

If insufficient evidence exists, the answer should say that evidence is insufficient and create a “Needs Evidence” state rather than inventing facts.

## 8. Demo AI architecture

`DemoAiProvider` must not call an external model.

It should:
- inspect the requirement category
- select synthetic evidence using deterministic keyword rules
- use response templates
- inject evidence snippets into the draft
- return predictable output

Example:

```ts
return {
  draftText: `Northstar Cloud Systems addresses the requirement using the controls described in Security Controls Handbook v2.1, Section 4.2. ${evidenceExcerpt}`,
  citations: [evidenceChunkId],
  confidenceLabel: evidence ? 'Strong evidence' : 'Insufficient evidence',
  mode: 'demo'
};
```

This guarantees the demo is stable and free.

## 9. API / server boundaries

Create domain functions first; expose APIs second.

Representative endpoints/actions:

```text
POST   /api/demo/reset
POST   /api/bids
GET    /api/bids/:id
POST   /api/bids/:id/documents
POST   /api/bids/:id/extract
GET    /api/bids/:id/requirements
POST   /api/requirements/:id/generate
PATCH  /api/requirements/:id
POST   /api/answers/:id/approve
POST   /api/requirements/:id/assign
POST   /api/bids/:id/qa
GET    /api/bids/:id/qa
POST   /api/bids/:id/export
```

API validation must use typed schemas.

## 10. Security boundaries

Demo:
- no real secrets in seed data
- synthetic files only
- demo mode visible

Untrusted document content (v2, applies in every mode):
- RFPs, addenda and buyer questionnaires are **untrusted input**. Their text may contain
  instructions aimed at the model ("ignore previous instructions…").
- Document text is always passed inside delimited data blocks, never concatenated into
  instructions; prompts state that document content is data only.
- Model output is schema-constrained; no model output can trigger tool calls, emails,
  exports, approvals or integration writes.
- Extraction runs the anchor check; generation runs the claim verifier. Injected text
  cannot create requirements or supported claims that are not in the sources.
- Include at least one injection fixture in the golden eval set.

Live future mode:
- authentication required
- organization isolation required
- role checks required
- object access authorization required
- audit logs required
- secrets stored server-side only
- uploads validated
- file type and size limits enforced
- AI provider keys never sent to browsers

## 11. Observability

Every long-running job should log:
- job ID
- type
- start time
- completion time
- progress
- failure reason
- retry count

AI calls in live mode should log metadata without logging sensitive prompt content by default.

## 12. Deployment strategy

### Local

```text
npm install
npm run db:push
npm run seed:demo
npm run dev
```

### Free/trial-friendly cloud path (v2, corrected)

v1 paired SQLite, local file storage and an in-process queue with a generic "free host".
On serverless hosts the filesystem is ephemeral and processes do not stay alive, so all
three break. Choose one path explicitly:

**Path A — single persistent container (simplest).** A host that runs a long-lived
container with a persistent volume: app + worker + SQLite + local storage together.

**Path B — serverless.** Managed PostgreSQL free tier, S3-compatible object storage free
tier via the storage adapter, `DbJobRunner` drained by host cron hitting
`/api/cron/tick`. Local embeddings may exceed serverless limits: compute them in a worker
or use the remote embeddings adapter.

**Public demo:** each visitor gets an isolated sandbox org cloned from the seed, expiring
after 24h and removed by a scheduled job, so visitors never see each other's edits.

AI: demo mode by default; free-tier live mode when keys are configured.

Do not hard-code assumptions about current free-tier limits.

## 13. Production evolution

After validated customer demand:
1. SQLite → PostgreSQL.
2. local storage → object storage.
3. local job runner → Trigger.dev or equivalent.
4. brute-force local vectors → managed vector index (only if chunk counts demand it).
5. single-tenant deployment → multi-tenant isolation.
6. demo auth → enterprise identity.
7. manual uploads → connectors.
8. simple editor → rich proposal editor.

Do not implement the entire evolution in V1.
