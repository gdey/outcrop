# RFDs (Request for Discussion)

Design documents live in `docs/rfd/` on the main branch as committed markdown files, not inside issues. This keeps them versioned alongside the code, permanently discoverable, and readable without any tooling.

## Directory Structure

```
docs/
  rfd/
    0001-resolution-roulette.md
    0002-canonical-example-names.md
    0003-alternative-use-cases.md
```

RFD numbers are simple incrementing integers. Unlike issue IDs, RFDs are meant to be referenced by humans in conversation ("see RFD 3") so short integers are more practical than timestamps.

## Linking an Issue to an RFD

When an issue has a corresponding design document, the `rfd` field in `issue.md` front-matter points to it by path from the root of the project:

```yaml
rfd: docs/rfd/0001-resolution-roulette.md
```

If no RFD exists for the issue, the field is set to `~` (YAML null). This keeps the schema consistent and makes it easy to query which issues have associated design docs:

```bash
grep -r "^rfd:" .gitissues/*/issue.md | grep -v "~"
```

## RFD Front-matter

RFD files carry a small front-matter block for status tracking:

```markdown
---
rfd: 0001
title: Resolution Roulette — Initial Design
status: draft
created: 2026-04-11
authors:
  - gdey
tags: [design, atproto, web]
related: [0002, 0003]
superseded-by: ~
---
```

| Field | Required | Values |
|---|---|---|
| `rfd` | yes | incrementing integer |
| `title` | yes | free text |
| `status` | yes | see status lifecycle below |
| `created` | yes | ISO 8601 date |
| `authors` | yes | YAML list of git usernames; first entry is original author, subsequent entries are contributors |
| `tags` | no | YAML list of strings for categorisation |
| `related` | no | YAML list of RFD numbers that are related e.g. depends on, extends |
| `superseded-by` | no | RFD number of the RFD that replaces this one; must be set when `status` is `superseded`, `~` otherwise |

## RFD Status

An RFD moves through a defined lifecycle. Not every RFD passes through every state — some are abandoned early, some move directly from `draft` to `discussion` — but the states are ordered from least to most final:

| Status | Meaning |
|---|---|
| `ideation` | A placeholder capturing the topic and rough scope, but not yet under active development. Anyone may pick it up and advance it to `draft`. |
| `draft` | Actively being written. The RFD is incomplete and not yet ready for broader input. Work happens in a branch. |
| `discussion` | The RFD is open for feedback, typically via a pull request. The content is stable enough to review but not yet decided. |
| `accepted` | The proposal has been agreed upon and is approved for implementation. Work has not necessarily begun. |
| `committed` | The proposal has been fully implemented. The RFD now serves as documentation of how the system works, not a proposal for how it should work. |
| `rejected` | The proposal was formally considered and decided against. The RFD is retained as a record of the decision and its rationale. |
| `abandoned` | Work on the RFD stopped without a formal decision — the author lost interest, priorities changed, or the problem became irrelevant. Distinct from `rejected` in that no explicit decision was made. |
| `superseded` | The RFD has been replaced by a newer RFD. The `superseded-by` field must be set to the number of the replacing RFD. |

A `rejected` or `abandoned` RFD is never deleted — it remains as a permanent record. Future authors can reference it to understand what was tried and why it did not proceed.

## Relationship to Issues

| Artifact | Lives on | Lifespan | Purpose |
|---|---|---|---|
| Issue (`issue.md`) | `issues` branch | opens and closes | track a problem or task |
| RFD (`docs/rfd/`) | `main` branch | permanent | design reference |

The issue is transient — it opens, gets discussed, and closes. The RFD is permanent — it stays discoverable and linkable long after the issue that spawned it is resolved.

## Inspiration

The RFD process is inspired by [Oxide Computer's RFD 1](https://oxide.computer/blog/rfd-1-requests-for-discussion).
