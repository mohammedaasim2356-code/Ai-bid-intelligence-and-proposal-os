# Verticals.md — Configurable Industry Packs (v2)

## 1. Principle

Core services stay industry-agnostic (PRD §4). Industry differences live in a
**vertical pack**: data + prompts + rubrics, loaded by configuration. Adding a vertical
must require zero changes to domain code.

## 2. Pack contents

```text
lib/verticals/<pack-id>/
  pack.json            # id, name, version, description
  categories.json      # requirement categories + keyword hints for the rule extractor
  sections.json        # default proposal section template
  qualification.json   # scorecard criteria + weights
  knowledge-types.json # document categories and their default expiry periods
  prompts/             # optional prompt overrides (versioned, same schemas)
  demo/                # synthetic company, synthetic RFP, synthetic knowledge docs
  evals/golden/        # golden set for this pack
```

## 3. Packs to ship

### `saas-it` (default, v1 scope)
Northstar Cloud Systems + Orion RFP, as already specified.

### `legal-services` (recommended second pack)
Corporate legal departments and public bodies issue RFPs to appoint outside counsel or
panel firms. Law firms answer them with lawyer bios, matter experience, rate structures,
diversity data, and information-security questionnaires — highly repetitive,
evidence-heavy work that maps directly onto this product.

- Synthetic firm: e.g. **Halden & Crewe LLP** (fictional). Synthetic RFP: e.g.
  **Meridian Holdings Outside Counsel Panel Review 2026**.
- Categories: firm overview, practice capability, relevant matters, team and bios,
  rates and alternative fee arrangements, diversity and inclusion, information security,
  conflicts process, insurance, billing guidelines acceptance, references.
- Qualification criteria: practice fit, jurisdiction coverage, relationship history,
  conflict risk flag (human-only), rate pressure, panel size, effort vs. value.
- Knowledge types: lawyer bios (expiry 6 months), matter sheets, credentials,
  security policies, fee templates.

Legal-pack guardrails (enforced in pack prompts and QA checks):
- **Client confidentiality:** matter records carry a `clientDisclosure` field
  (`named` / `anonymised` / `do_not_use`). Drafts may only name a client when it is
  `named`; QA blocks any answer that names a `do_not_use` or `anonymised` client.
- **Conflicts:** the system may flag possible conflict signals from buyer names but never
  clears conflicts; a human conflicts check is a required gate item.
- **No legal advice:** the product drafts marketing and capability responses, not legal
  opinions. Prompts refuse to draft substantive legal analysis for the buyer.

### Later candidates
Cybersecurity vendor questionnaires, AEC/engineering bids, public-sector tenders.
