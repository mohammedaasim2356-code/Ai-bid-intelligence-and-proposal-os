# PRD.md — AI Bid Intelligence & Proposal OS (v2)

> v2 changes: free-tier real-AI mode, span-anchored extraction, claim-level citation verification, Answer Library flywheel, addendum diffing, questionnaire round-trip export, end-to-end pipeline automation, eval harness, vertical packs. See `CHANGELOG.md`.

## 1. Product identity

**Working name:** BidOS

**Product category:** AI-assisted RFP, proposal, questionnaire, and bid operations platform.

**Primary outcome:** Turn an incoming RFP/RFI/security questionnaire into a structured opportunity, a traceable requirement/compliance matrix, evidence-backed answer drafts, a human review workflow, and an exportable final response.

**Commercial positioning:** This is an enterprise workflow product, not a generic chatbot and not merely a proposal text generator. The future service offer is a high-ticket implementation/customization project with an ongoing support/optimization retainer.

**Research basis:** The market research behind this specification found that RFP/proposal software already supports premium pricing, including published or reported $10K/year-level starting points. The research also found strong buyer interest in reducing manual proposal work, and that source citations, knowledge connectivity, review workflow, and measurable ROI are central to the category. Reported vendor/customer examples include faster turnaround and large time savings. Treat vendor-reported ROI and win-rate claims as marketing claims, not guaranteed outcomes.

Key research references:
- Responsive pricing and Microsoft customer ROI story: https://www.responsive.io/pricing
- Loopio RFP trends / proposal benchmarks and customer story: https://loopio.com/blog/qvidian-vs-loopio/
- Inventive AI pricing: https://www.inventive.ai/inventive-ai-pricing
- Inventive AI product/ROI claims: https://www.inventive.ai/
- RFP.ai pricing and workflow: https://rfp.ai/pricing/
- Arphie product positioning and source-backed answers: https://www.arphie.ai/
- QorusDocs customer story: https://www.qorusdocs.com/success-stories/info-tech-case-study

## 2. Problem statement

Proposal teams repeatedly perform the same expensive sequence:

1. Receive RFP/RFI/questionnaire files.
2. Read long documents manually.
3. Locate questions, requirements, deadlines, page limits, and attachments.
4. Decide whether to bid.
5. Search old proposals, product documentation, security documentation, case studies, CVs, and approved answers.
6. Ask internal subject matter experts for missing information.
7. Draft answers.
8. Review and revise answers repeatedly.
9. Verify that every mandatory requirement is covered.
10. Reformat everything into the client's requested structure.
11. Perform final QA and submit.

This workflow is fragmented across email, Word, Excel, shared drives, chat, CRM, and human memory. The product must centralize the process while preserving human approval.

## 3. Target users

### Primary user — Proposal Manager
Needs a command center for every active bid, deadline, assignment, progress metric, and unresolved requirement.

### Secondary user — Proposal Writer
Needs fast retrieval of approved content, a source-backed draft, an editor, and a way to see why an answer was suggested.

### Secondary user — Subject Matter Expert (SME)
Needs a small task queue showing only the questions requiring their expertise, with easy approve/edit/reject controls.

### Secondary user — Sales / Account Executive
Needs opportunity context, qualification information, buyer/company research, deadline visibility, and a simple view of proposal readiness.

### Secondary user — Reviewer / Compliance Lead
Needs requirement coverage, source traceability, unresolved flags, version history, and approval state.

### Future user — Executive
Needs portfolio-level metrics: proposal volume, cycle time, workload, content reuse, automation rate, and estimated labor savings.

## 4. Ideal customer profile

The future commercial ICP is a B2B organization where formal bids materially affect revenue and proposal work crosses multiple departments.

Strong fit signals:
- Dedicated proposal, pre-sales, bid, or sales-enablement team.
- Repeated RFP/RFI/security questionnaire volume.
- Large or strategically important deal sizes.
- Multiple internal contributors.
- Knowledge spread across drives, documents, CRM, wiki, or chat.
- Measurable manual effort in response preparation.
- Existing content that can be reused.
- Executive pressure to respond faster without increasing headcount.

Initial vertical focus for the demo should be:
- B2B SaaS / enterprise software.
- IT services / cloud consulting.
- Cybersecurity.
- Professional services / consulting.
- Engineering / AEC-style bids.

Do not build industry-specific logic into core services in V1. Use configuration and prompt/rubric packs instead.

## 5. Product promise

> “Turn complex RFPs into qualified opportunities, traceable requirements, evidence-backed response drafts, and review-ready submissions from one workspace.”

The product must never promise guaranteed wins, guaranteed revenue, or autonomous submission. It assists; humans approve.

## 6. MVP scope

### A. Demo workspace
The first screen should clearly offer:
- “Launch Demo Workspace”
- “Create New Bid”
- “Load Synthetic Company”

The demo must work without a paid API key.

### B. Bid creation
User can create a bid with:
- opportunity name
- buyer name
- buyer website (optional in demo)
- bid value estimate
- deadline
- owner
- status
- description

### C. RFP ingestion
Support, at minimum:
- PDF
- DOCX
- XLSX/CSV
- TXT/MD

Users can also load a synthetic RFP from the demo library.

### D. Parsing and requirement extraction
Extract:
- sections
- questions
- mandatory/optional status when inferable
- dates/deadlines
- word/page constraints when detectable
- deliverables
- requested attachments
- evaluation criteria when present
- security/compliance requirements
- buyer instructions

Each extracted item must preserve source location metadata such as document name, page/section, or spreadsheet sheet/cell range whenever available.

### E. Bid qualification
Create a qualification view with structured signals:
- capability fit
- evidence availability
- deadline feasibility
- compliance gaps
- major unknowns
- commercial constraints
- recommended review priority

Do not present an “AI decision” as authoritative. Present evidence and flags. The user owns the final bid/no-bid decision.

### F. Knowledge base
Provide:
- file upload
- synthetic seed library
- document list
- document categories
- approved/unapproved status
- version number
- source metadata
- tags

Demo seed categories:
- company overview
- product documentation
- security policy
- privacy policy
- implementation methodology
- case studies
- customer references
- employee bios
- standard Q&A
- certifications

### G. Answer generation
For each requirement/question:
- retrieve relevant evidence
- generate a draft answer
- show evidence snippets
- show citation links
- show confidence label derived from evidence coverage, not a fabricated probability
- allow regenerate
- allow edit
- allow approve/reject

Supported answer states:
- Not Started
- Drafted
- Needs SME
- In Review
- Approved
- Needs Evidence
- Rejected

### H. Compliance / response matrix
Every extracted requirement becomes a row with:
- requirement ID
- source
- question / requirement text
- category
- mandatory flag
- owner
- response status
- evidence coverage
- citation count
- reviewer
- comments
- last updated

### I. SME workflow
Users can assign questions to a role/person.

Each SME task has:
- question
- context
- current draft
- source evidence
- requested-by date
- status
- comment field
- approve/edit/request-more-info actions

### J. Proposal editor
Create a simple structured editor rather than a full Microsoft Word clone.

Sections:
- Cover
- Executive Summary
- Company Overview
- Understanding of Requirements
- Proposed Solution
- Implementation Plan
- Security / Compliance
- Team
- Relevant Experience
- Case Studies
- Pricing placeholder
- Assumptions
- Appendices

Users can reorder sections and insert approved response blocks.

### K. QA engine
The QA screen should flag:
- unanswered mandatory requirements
- missing evidence
- unsupported factual claims
- contradictory answers
- outdated document sources
- missing attachments
- missing citations
- unresolved SME questions
- response-length constraints
- placeholder text
- inconsistent organization/product names

QA results must link back to exact items requiring attention.

### L. Export
Support:
- DOCX export
- Markdown export
- JSON export
- CSV compliance matrix export
- print-friendly HTML

PDF may be implemented as an optional extension if the local environment supports the chosen renderer.

### N. Answer Library (reuse flywheel)
- Every approved answer can be promoted into a reusable library entry: canonical question,
  answer, citations, owner, tags, approval date, **review-by date**.
- Before generating, the system searches the library for near-duplicate questions.
  A match is proposed as "Reused from <bid>, approved by <person> on <date>" and still
  goes through claim verification against current evidence.
- Library entries past their review-by date cannot be reused without re-approval.

### O. Addenda and clarifications
- Upload an addendum/amendment against an existing bid.
- The system diffs extracted requirements (new / changed / removed) and marks affected
  answers `Needs Re-review`.
- Track clarification questions sent to the buyer, their deadline, and the buyer's reply.

### P. Questionnaire round-trip export
- When the RFP arrived as an XLSX/DOCX questionnaire, export answers **back into the
  buyer's original file**, in the original cells/answer fields, using the stored source
  locators. Formatting outside answer cells is untouched.
- This is the single biggest time-saver for security and vendor questionnaires.

### Q. Pipeline automation
- One action runs the whole flow up to each human gate (see `Automation.md`).
- Automatic SME routing by category→owner map, with a generated, specific SME question.
- Deadline watcher, escalations, and a daily digest.

### M. Analytics
Initial dashboard:
- active bids
- deadline countdowns
- total requirements
- approved answers
- unresolved requirements
- evidence coverage
- estimated hours saved
- automation rate
- reviewer workload

Hours-saved must be a configurable estimate based on baseline hours/question or hours/RFP. Label as “estimated,” never as measured unless the user provides real measurements.

## 7. Demo-mode requirement

The entire project must be usable with:
- no paid API key
- no external SaaS account
- no cloud database
- no background-job vendor

### Default demo stack
- local database
- seeded synthetic company
- seeded synthetic RFP
- deterministic demo AI adapter
- local document fixtures
- local asset storage

The UI must visibly show “DEMO MODE” when synthetic data or deterministic responses are being used.

### Free-tier live AI mode (v2)
Demo mode proves the workflow; it does not prove the AI. The product must also offer a
**$0 live-AI mode**: a local model and/or provider free-tier keys, routed through the
AI Router in `AI-Providers.md`. The same synthetic demo can be run in either mode, and
the UI labels each result `SYNTHETIC RESULT`, `LIVE AI (local)` or `LIVE AI (free tier)`.
If all live providers are unavailable, the system falls back to demo output without
breaking.

### Synthetic company
Use a fictional company, for example:
**Northstar Cloud Systems**

It should never be implied to be a real customer.

Create synthetic documents covering a realistic enterprise SaaS/cloud-services organization.

### Synthetic RFP
Use a fictional enterprise procurement scenario, for example:
**Orion Enterprise Cloud Modernization Program — RFP-2026-014**

Target:
- 60–100 requirements/questions.
- 8–12 sections.
- 3–6 security requirements.
- 3–6 commercial/legal requirements.
- 2–4 case-study/evidence requirements.
- 2–4 mandatory attachments.

## 8. Non-goals for V1

Do not build initially:
- autonomous bid submission to procurement portals
- browser automation for hostile/unstable portals
- legal advice
- autonomous contract negotiation
- real payment processing
- deep CRM bi-directional sync
- enterprise SSO certification
- SOC 2 certification
- multi-region production infrastructure
- model training/fine-tuning
- a full Word-equivalent editor

Architect for these later, but do not let them delay the MVP.

## 9. Success criteria for the demo

A fresh user must be able to:

1. Open the application.
2. Launch synthetic demo mode.
3. Open a sample bid.
4. See extracted requirements.
5. Open one requirement.
6. See a source-backed draft answer.
7. See exact evidence snippets.
8. Assign an SME task.
9. Edit/approve the response.
10. Run QA.
11. See remaining gaps.
12. Export the response/compliance matrix.
13. Review analytics.
14. Click “Run full pipeline” and watch the synthetic RFP travel from intake to
    export-ready, with every gate and AI step visible in the run log.
15. See at least one trap question correctly end in `Needs Evidence`.
16. Open the Quality page and see the latest eval scores.

The full walkthrough should be understandable without developer assistance.

## 10. Commercialization hypothesis

Potential future implementation offer:
- discovery/process mapping
- knowledge-base onboarding
- workflow configuration
- integrations
- deployment
- training
- governance
- reporting
- optimization retainer

Initial agency positioning should be:
> “We implement AI bid operations systems for teams where RFP execution is a high-value revenue workflow.”

Do not claim a fixed $10K price as guaranteed. The build should demonstrate capabilities that can support a premium implementation conversation.

## 11. Vertical packs

Industry variation is delivered as configuration packs (`Verticals.md`). Ship `saas-it`
first. The recommended second pack is `legal-services` (outside-counsel / panel RFPs),
which reuses every core feature with different categories, rubrics and guardrails.

## 12. Product principles

1. Evidence before generation.
2. Human approval before consequential actions.
3. Clear separation between synthetic/demo and real customer data.
4. Every AI-generated answer should explain its evidence.
5. Every long-running task should be observable.
6. Every failure should be recoverable.
7. The system must remain useful without an external AI API.
8. Keep the MVP simple enough for a solo builder to understand and maintain.
9. Reuse before generate: approved human answers beat fresh model output.
10. Quality is measured, not asserted (`Evals.md`).
