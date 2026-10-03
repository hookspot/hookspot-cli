# Domain Docs

How the engineering skills consume this repo's domain documentation when exploring the codebase. Single-context layout:

```
/
├── CONTEXT.md     ← glossary of domain terms
└── docs/adr/      ← architecture decision records
```

## Before exploring, read these

- **`CONTEXT.md`** at the repo root.
- **`docs/adr/`**: ADRs that touch the area you're about to work in.

When either is missing, proceed silently. `/domain-modeling` creates them lazily, once a term or decision is actually resolved.

## Use the glossary's vocabulary

When your output names a domain concept (an issue title, a refactor proposal, a hypothesis, a test name), use the term as `CONTEXT.md` defines it, over any synonym it lists as avoided.

A concept missing from the glossary is a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

When your output contradicts an existing ADR, surface it explicitly:

> _Contradicts ADR-0007 (event-sourced orders), but worth reopening because…_
