# Design.md — Visual Design System

## 1. Design direction

The product should feel like a high-value enterprise operations console rather than a generic AI chat application.

Reference qualities:
- precise
- calm
- editorial
- intelligent
- operational
- premium
- information-dense without looking cluttered

The interface should communicate:
> “There is a lot of important work here, but the system is under control.”

## 2. Visual concept

Working concept:
**Editorial Command Center**

Combine:
- sophisticated typography
- restrained surfaces
- strong information hierarchy
- subtle motion
- evidence-focused cards
- clear workflow states
- dense but breathable tables

Avoid:
- excessive glassmorphism
- neon cyberpunk styling
- giant gradients everywhere
- chat-first UI
- excessive floating cards
- random rounded containers

## 3. Color system

Use tokens so colors can be changed globally.

### Core
- Ink: near-black blue/charcoal for primary text.
- Canvas: warm off-white/light neutral for primary application background.
- Surface: white/very-light neutral.
- Border: subtle neutral.
- Muted: cool gray.

### Semantic
- Success: restrained green.
- Warning: amber.
- Danger: red.
- Info: blue.
- Demo: violet/indigo accent.

Do not use color alone to communicate meaning.

## 4. Typography

Recommended:
- UI/body: Inter or another highly legible sans-serif.
- Editorial/display headings: Geist/Inter-like modern sans.
- Monospace metadata: JetBrains Mono or system monospace.

Hierarchy:

```text
Display         48–64 px
Page heading    32–40 px
Section heading 20–24 px
Body            14–16 px
Meta            12–13 px
```

Use responsive scaling rather than hard-coded oversized text on mobile.

## 5. Spacing

Use a consistent spacing scale.

Recommended base unit: 4px.

Examples:
- 4
- 8
- 12
- 16
- 24
- 32
- 40
- 48
- 64

## 6. Layout

Desktop:
- left navigation: 240–280px
- main content: flexible
- optional right detail panel: 360–480px

Use a 12-column grid for dense dashboard layouts.

Do not force every screen into cards.

Preferred patterns:
- tables for structured review
- split view for requirement + evidence
- timelines for workflow
- KPI cards for high-level status
- drawer panels for detail

## 7. Navigation

Primary navigation:

```text
Overview
Bids
Knowledge
Tasks
Analytics

----------------
Integrations
Settings
```

Within a bid:

```text
Overview
Requirements
Knowledge
Answers
Tasks
Proposal
QA
Analytics
```

The current location must always be obvious.

## 8. Bid command center

The main bid header should contain:
- bid name
- buyer
- status
- deadline
- estimated value
- owner
- progress indicator
- primary action

Example primary actions:
- Continue extraction
- Generate answers
- Resolve blockers
- Run QA
- Export

## 9. Requirement workspace

This is the most important screen in the product.

Use a split layout:

```text
┌──────────────────────────────┬──────────────────────────────┐
│ Requirement / response list  │ Requirement detail           │
│                              │                              │
│ #4.2  Security     NEEDS    │ Requirement                   │
│ #4.3  Data         APPROVED │ Evidence                      │
│ #4.4  Support      REVIEW   │ Draft                         │
│                              │ Citation                      │
│ Filters                      │ SME task                     │
└──────────────────────────────┴──────────────────────────────┘
```

On narrow screens, stack these sections vertically.

## 10. Evidence panel

Evidence must be visually distinct from AI-generated text.

Recommended structure:

```text
SOURCE
Security Controls Handbook v2.1
Section 4.2

EVIDENCE
“Customer data is encrypted at rest...”

USED IN ANSWER
✓ Citation 1
```

Users should immediately understand:
- what came from source material
- what was generated
- what is still unverified

## 11. AI answer editor

Do not make it look like ChatGPT.

Use:
- structured editor
- evidence side rail
- source badges
- status pill
- review controls

Primary controls:
`Regenerate` `Edit` `Assign SME` `Approve`

Dangerous/consequential controls should require deliberate action.

## 12. QA page

QA should feel like a launch checklist.

Top summary:

```text
READY SCORE
24 of 31 checks passed

BLOCKERS 3
WARNINGS 4
INFO 7
```

Then a fix list grouped by severity.

Clicking a finding must navigate to the exact issue.

## 13. Motion

Motion should reinforce workflow state, not decorate everything.

Examples:
- page transitions: 150–250ms
- drawer open: 180–240ms
- table row hover: very subtle
- progress transitions: 300–500ms
- AI generation state: lightweight progress animation

No constant floating/parallax animations in the core application.

## 14. Micro-interactions

Useful moments:
- “Answer approved” confirmation
- evidence copied
- QA finding resolved
- export completed
- task assigned

Feedback should be brief and accessible.

## 15. Tables

Tables are a core product primitive.

Requirements table columns should support:
- requirement ID
- section
- category
- status
- mandatory
- owner
- evidence
- reviewer

Allow:
- sorting
- filtering
- search
- column visibility

Avoid excessive horizontal scrolling on mobile.

## 16. Status system

Use both icon/text and color.

Examples:

```text
● Draft
✓ Approved
! Needs Evidence
↻ In Review
× Rejected
```

The text must remain understandable without color.

## 17. Empty states

Every empty state should tell the user:
1. what is empty
2. why it matters
3. what action to take

Example:
> No knowledge documents are connected yet.
> Add approved company material so responses can be grounded in evidence.
> [Add document]

## 18. Loading states

Prefer skeletons that match final layout.

For long jobs, show:
- current step
- progress
- elapsed time where useful
- cancel option when supported

Avoid indefinite generic spinners.

## 19. Accessibility

Minimum:
- WCAG-oriented contrast
- keyboard navigation
- focus states
- semantic headings
- labels for controls
- descriptive error messages
- aria labels where needed
- reduced-motion support

Use `prefers-reduced-motion`.

## 20. Responsive behavior

Breakpoints should be based on content rather than device names.

At smaller widths:
- sidebar collapses
- tables become stacked/collapsible
- split panes stack
- action bars wrap
- evidence panel becomes a drawer

## 21. Landing page

Portfolio mode should include:

### Hero
**RFPs in. Decision-ready proposals out.**

Supporting line:
AI-assisted bid operations that connect requirements, evidence, drafting, review, QA, and export in one workflow.

Primary button:
`Launch Synthetic Demo`

Secondary button:
`View Architecture`

### Sections
1. Problem
2. Workflow
3. Product screens
4. Evidence-backed AI
5. QA
6. ROI model
7. Architecture
8. Security principles
9. Demo CTA

## 22. Demo banner

Always show a persistent but elegant demo indicator:

> SYNTHETIC DEMO — No real customer data or live proposal submission.

## 23. Design implementation hierarchy

Build reusable primitives first:
- Button
- Badge
- Tabs
- Card
- Table
- Drawer
- Modal
- Tooltip
- Progress
- Alert
- EmptyState
- Skeleton

Then domain components:
- BidHeader
- RequirementRow
- EvidenceCard
- AnswerEditor
- QAFinding
- TaskCard

Do not style each page independently.
