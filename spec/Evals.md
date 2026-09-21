# Evals.md — Measuring Quality (v2)

## 1. Why

"Crazy good" has to be provable. An eval harness turns the demo from "trust me" into
numbers a buyer can inspect, and it stops regressions when prompts or models change
(which they will, because free-tier model menus change).

## 2. Golden dataset

`evals/golden/` contains, for the synthetic Orion RFP and at least one additional
synthetic RFP in a different format (XLSX questionnaire):

- `requirements.json` — the labelled correct requirement list with verbatim source spans,
  mandatory flags and categories.
- `evidence.json` — for each requirement, the chunk IDs that genuinely support an answer,
  or `[]` where the knowledge base deliberately lacks evidence (trap questions, e.g. an
  ISO certification Northstar does not hold).
- `answers.json` — reference facts each answer must contain and facts it must not claim.

Include at least 10 trap questions. They are the most persuasive part of any demo.

## 3. Metrics

| Metric | Definition | V1 target |
|---|---|---|
| Extraction recall | golden requirements matched by an extracted one (span overlap ≥ 0.6) | ≥ 0.90 |
| Extraction precision | extracted requirements that match a golden one | ≥ 0.85 |
| Mandatory flag accuracy | correct mandatory/optional on matched items | ≥ 0.90 |
| Retrieval recall@5 | golden chunks present in top 5 | ≥ 0.85 |
| Citation precision | cited chunks that are in the golden set | ≥ 0.90 |
| Unsupported-claim rate | sentences failing the claim verifier after generation | ≤ 0.05 |
| Trap-question refusal | trap questions that end in `NEEDS_EVIDENCE` | 1.00 |
| Library reuse rate | requirements answered from library on the second synthetic RFP | report only |

Targets are starting points; record actual values in `Memory.md`, never inflate them.

## 4. Running

```text
npm run eval               # all suites, current AI_PROVIDER
npm run eval -- --provider demo
npm run eval -- --suite extraction
```

Output: `evals/reports/<timestamp>.json` plus a markdown summary. An in-app
**Quality** page renders the latest report per provider mode.

## 5. Gates

- CI runs `eval --provider demo` (deterministic, free) on every change. Any drop below
  target fails the build.
- Remote-provider evals run manually or nightly; results are cached by the AI router so
  re-runs cost almost nothing.
- Prompt changes require a new prompt version and an eval report in the PR/commit notes.
