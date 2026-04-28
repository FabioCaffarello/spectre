# Master Strategy Prompt — Spectre Microservices Refactoring

> Paste this as the first message of every Claude Code session
> involving the microservices refactoring work, regardless of which
> phase (R1-R8) is being executed. This document establishes
> non-negotiable principles, the phase sequence, invariants that
> must hold across all PRs, and the protocol for resuming work
> across sessions. It is paired with phase-specific prompts (R1,
> R2.1, R2.2, etc.) that are loaded after this strategy is
> understood.

---

## Section 1 — What this refactor is, and what it is not

The Spectre repository at `github.com/FabioCaffarello/spectre` has
delivered 18 PRs implementing a driver-agnostic web scraping
protocol with three reference adapters (Playwright, SeleniumBase,
curl-impersonate), an engine, a CLI, and a Kubernetes operator
foundation. Each PR was defensible in isolation. The cumulative
shape, however, drifted toward a monolithic packaging:

- The Kubernetes operator image bundles the engine binary plus
  all three adapter runtimes (~1.5GB)
- Adapter-engine communication uses Unix Domain Sockets (a
  localhost transport)
- The control plane's `SubprocessRunner` shells out to the
  bundled engine, which spawns adapters as further subprocesses
- Output flows via stdout piping; no external sinks

This shape works as a demonstrator. It does NOT match the
project's positioning as a "disruptive microservices web scraping
framework." This refactor brings architecture into alignment with
that positioning.

**The refactor is:**

- A complete architectural transition from subprocess-in-pod to
  service-per-component
- A move from Unix Domain Sockets to TCP/gRPC for all
  inter-service traffic
- The introduction of stateful supporting services (PostgreSQL,
  Kafka, Redis)
- A redesign of local development around `docker compose up`
  as the canonical workflow
- Removal of all legacy code paths — the previous architecture is
  retired, not deprecated

**The refactor is NOT:**

- A rewrite of the driver protocol (v1alpha1 stays frozen)
- A rewrite of capability declarations (the 13/12/6 strict-subset
  chain is preserved exactly)
- A rewrite of the conformance suite's semantics (only its
  transport layer changes)
- An exercise in adding optional fallbacks (no UDS-or-TCP, no
  CLI-or-Compose — one path forward, the other deleted)

These distinctions matter. Violating any of them in a phase PR
should trigger immediate review.

---

## Section 2 — Non-negotiable principles

These principles bind every phase. If a phase PR violates them,
the work is wrong. Each principle exists because of a concrete
failure mode the refactor must avoid.

### 2.1 The driver protocol stays frozen

The wire contract between engine and adapters — the gRPC service
definitions in `proto/spectre/driver/v1alpha1/` — does not change
during the refactor. Only the transport address (UDS path → TCP
host:port) changes.

**Concrete invariant:** at any point during the refactor, taking
the latest `proto/spectre/driver/v1alpha1/` files and the latest
adapter implementations, all conformance tests must produce
identical results to the pre-refactor baseline (modulo the
transport-level setup).

**Why:** the protocol is the project's most distinctive asset.
Touching it during a transport refactor conflates two changes
and corrupts the audit trail.

### 2.2 No legacy paths survive

When a phase replaces a code path, the old path is deleted in the
same PR. No `runner.SubprocessRunner` left behind once
`runner.EngineClientRunner` lands. No UDS fallback once TCP is
in. No `spectre run` CLI mode once Compose is canonical.

**Concrete invariant:** after each phase merges, a grep for the
retired pattern returns zero hits in source code (allowed in
ADRs documenting the retirement, in CHANGELOG entries, in
historical roadmap entries).

**Why:** legacy paths during refactor become permanent. Each
"temporary" fallback adds maintenance burden, dilutes the
architecture, and undermines the narrative coherence the refactor
exists to restore.

### 2.3 Capability divergence is preserved exactly

The capability lists declared by each adapter — Playwright 13,
SeleniumBase 12, curl-impersonate 6 — must remain unchanged
through the refactor. The `driver.yaml` files, the byte-for-byte
conformance assertions, the runtime declarations — all identical
before and after.

**Concrete invariant:** at every PR's merge, running the
conformance test
`test_<adapter>_initialize::test_capabilities_match_manifest_byte_for_byte`
against each of the three adapters passes with the exact same
capability list as before the refactor began.

**Why:** the strict-subset chain is the project's most
architecturally consequential narrative artifact. ADR-0017 §1's
"capability declaration is about cross-driver semantic
equivalence" contract was the project's most sophisticated
design decision. Touching it during a transport refactor would
be unforgivable carelessness.

### 2.4 Each PR is independently reviewable

Phase PRs (R1.1, R2.1, R2.2, etc.) are not bundled. Each one is
opened, reviewed, merged before the next begins. No
"refactor mega-PR" with 50 file changes across all components.

**Concrete invariant:** each phase PR's diff fits within the
project's sustained review budget — typically 500-2000 lines of
substantive change. The Section 7 step structure of each phase
prompt enforces this through commit-per-step granularity.

**Why:** the previous PRs (PR1-PR18) established a cadence of
focused, individually-merged PRs with their own ADRs and
acceptance criteria. The refactor inherits this discipline.

### 2.5 Compose is the development environment

After Phase R6, `docker compose up` brings up the full stack and
is the only supported local development workflow. There is no
"run engine standalone" path, no "run adapter as subprocess"
path, no Devcontainer-without-Compose path.

**Concrete invariant:** after Phase R6 merges, the
`README.md` "Quick start" section's first command after `git
clone` is `docker compose up`. All subsequent dev commands
assume the stack is running.

**Why:** the maintainer's stated goal is microservices, and
microservices are validated by running the full graph locally.
Allowing alternative dev paths reintroduces the monolithic
mental model the refactor exists to retire.

### 2.6 ADR supersession is explicit and recorded

When a phase supersedes an existing ADR, the supersession is
recorded explicitly — both in the new ADR (referencing what it
supersedes) and in the old ADR (a status update pointing to the
superseder). Partial supersessions are allowed (a section of an
old ADR superseded, the rest preserved) and documented per the
pattern ADR-0013 established for ADR-0002.

**Concrete invariant:** at every phase's merge, the
`docs/adr/README.md` index correctly reflects the status of
every ADR. No "Superseded by TBD" placeholders. No undocumented
status drift.

**Why:** ADRs are the project's audit trail. The refactor will
retire significant portions of ADR-0008, ADR-0009, ADR-0019.
Recording the retirement honestly is non-negotiable.

### 2.7 Tests are not weakened to accommodate the refactor

If a test fails because the refactor changed assumptions
(transport, lifecycle, etc.), the test is updated to assert the
new contract — never weakened to "skip" or "accept either old
or new behavior." Quarantining tests is forbidden.

**Concrete invariant:** every phase PR ships with all tests
green (unit, integration, conformance). No `@pytest.mark.skip`,
no `#[ignore]`, no `t.Skip()` introduced during the refactor
without an explicit ADR documenting why.

**Why:** the conformance suite is the project's quality
backbone. Weakening it during refactor means re-establishing
quality from scratch later.

---

## Section 3 — Maintainer's locked decisions

These four decisions were settled before the refactor began.
They are constraints — not options to revisit during phase
execution.

### 3.1 CLI mode (`spectre run`) is retired completely

The `spectre run` CLI command, the engine binary's standalone
execution mode, and the `SubprocessRunner` in the operator are
all retired. After Phase R3, the engine binary exists only as a
gRPC service binary. After Phase R6, all execution flows through
the Compose stack (locally) or the operator (in Kubernetes).

This is not a "deprecation with sunset path." It is a deletion.
ADR-0013 (CLI as engine binary) is superseded by ADR-0020 in
Phase R1.

### 3.2 Driver protocol v1alpha1 is frozen byte-for-byte

The `proto/spectre/driver/v1alpha1/` directory is treated as
read-only during the refactor. The wire contract between engine
and adapters does not evolve. Only the transport (UDS → TCP)
and the discovery (subprocess spawn → service DNS) change.

If the refactor exposes a need for a protocol change, it is
documented as a v1alpha2 candidate and deferred to post-refactor
work.

### 3.3 ScrapeJob CRD breaks to v1alpha2 (no conversion webhook)

The control plane CRD evolves to v1alpha2 in Phase R3. No
conversion webhook is implemented. The reasoning: there is no
production deployment to migrate. Breaking change is cleaner
than maintaining a conversion path that no real user needs.

This decision is documented in ADR-0020 §3 and ADR-0023.

### 3.4 The development environment is Compose, full stop

There is no fallback dev path. After Phase R6, contributors who
cannot run Docker cannot develop the project locally. The
Devcontainer ships with Docker-in-Docker enabled.

This decision is honest about its trade-off: it raises the
contribution barrier slightly. The maintainer accepts this in
exchange for architectural coherence.

---

## Section 4 — Phase sequence and invariants

The refactor is delivered as 17 PRs across 8 phases. The
sequence is fixed. Phases cannot be reordered or skipped.

| Phase | PRs | Focus                                     | ADRs introduced/superseded                  |
|-------|-----|-------------------------------------------|---------------------------------------------|
| R1    | 1   | Architectural supersession (foundation)   | +ADR-0020. Updates ADR-0008/0009/0013/0019 status. |
| R2    | 3   | TCP transport + service discovery         | +ADR-0021, +ADR-0022. Supersedes ADR-0008.  |
| R3    | 2   | Control plane refactor + CRD v1alpha2    | Updates ADR-0019.                           |
| R4    | 4   | Stateful services (Postgres, Kafka, Redis) | +ADR-0023. Updates ADR-0010 (session state). |
| R5    | 1   | Output sinks (S3, webhook)                | +ADR-0024.                                  |
| R6    | 3   | Per-service Dockerfiles + Compose stack   | +ADR-0025.                                  |
| R7    | 2   | Helm chart + production smoke             | +ADR-0026.                                  |
| R8    | 1   | Documentation refresh + narrative closing | None (docs only).                           |

**Invariants between phases:**

- **R1 must merge before R2.** ADR-0020 establishes the
  architectural commitment. Phase R2 PRs reference ADR-0020 as
  upstream context.
- **R2 must complete before R3.** Transport must be TCP before
  control plane can dial engine as a service.
- **R3 must complete before R4.** The refactor's structure
  (engine as service, control plane as gRPC client) must be
  stable before stateful dependencies are added.
- **R4 must complete before R5.** Output sinks beyond stdout
  presume Kafka exists.
- **R6 may begin in parallel with R5** (per-service Dockerfiles
  don't depend on which sinks exist).
- **R7 requires all of R2-R6.** Helm chart packages everything.
- **R8 is the closing PR** — refresh docs after the dust
  settles.

Concretely: at the start of each phase, the agent must verify
the prior phase's PRs have merged and check the working tree is
clean.

---

## Section 5 — Resumption protocol across sessions

The refactor will span many Claude Code sessions across weeks or
months. Sessions end (token limit, end of working day, content
filter blocks, etc.). The next session must resume from a clean
checkpoint.

### 5.1 Per-session startup ritual

At the start of EVERY refactor-related session, before any code:

1. Run `git log --oneline | head -20` and identify the most
   recent PR merge.
2. Run `git status` and confirm the working tree is clean (or
   identify in-progress branch state).
3. Read the current phase's prompt fully (R1, R2.1, etc.).
4. Read this strategy prompt (this document) at least
   skim-level to refresh principles.
5. Read `docs/refactoring-status.md` (created in PR R1.1) to see
   what's complete and what's next.
6. Read the most recently merged PR's description on GitHub for
   surfaced TODOs and follow-up notes.

### 5.2 Per-session sign-off ritual

At the end of EVERY session that produced work:

1. Commit work-in-progress to a feature branch with a clear
   message describing exactly where the work stopped.
2. If the session is mid-PR (a phase PR not yet opened), update
   `docs/refactoring-status.md` with the current sub-step of
   Section 7's checklist.
3. Surface to the maintainer (in chat) precisely what step in
   the phase prompt to resume from in the next session.

### 5.3 Content-policy blocks

The refactor includes ADRs with substantial prose (especially
ADR-0020 supersession narrative, ADR-0023 stateful services
rationale). These can trigger content-policy filters
intermittently.

Standard mitigation:
- Identify the triggering passage
- Rewrite using neutral systems-engineering vocabulary
- Generate in smaller chunks (write Section 4.1, commit; write
  Section 4.2, commit; etc.)
- If repeated blocks, surface to the maintainer and pause for
  guidance — do not "creatively" rewrite to avoid the filter,
  as that risks meaning drift

---

## Section 6 — How the agent should think when stuck

The refactor is more architecturally complex than any prior PR.
The agent will encounter situations not anticipated by the phase
prompts. When that happens, follow this protocol.

### 6.1 First check: does an existing principle apply?

Re-read Section 2's seven principles. The most likely answer is
already there. For example:
- "Should I keep the old runner around for fallback?" — Section
  2.2 says no.
- "Should I parameterize the transport to allow UDS too?" —
  Section 2.2 says no.
- "Can I weaken this conformance assertion to make the test
  pass?" — Section 2.7 says no.

### 6.2 Second check: would this change the protocol?

Section 2.1 forbids protocol changes. If a phase tempts the
agent into modifying `proto/spectre/driver/v1alpha1/`, stop and
revisit. The transport (TCP) does not require protocol changes.
The session lifecycle (Redis-backed) does not require protocol
changes. Output sinks (Kafka) do not require protocol changes.

### 6.3 Third check: does the maintainer need to decide?

Some decisions during refactor surface architectural choices the
maintainer hasn't pre-decided. In those cases, the agent does
NOT pick on the maintainer's behalf. Surface the decision in
chat with:
- Concrete options
- Honest trade-offs
- A recommendation if the agent has one
- The expected next step once decided

Examples of decisions to surface (not exhaustive):
- "Should adapter sessions be fully invalidated on Pod restart,
  or do we attempt warm restart via Redis state?" (Phase R4)
- "Which Kafka client library — `rdkafka` or `rskafka` for
  Rust?" (Phase R4)
- "Helm chart subcharts pinned to specific upstream charts, or
  inline?" (Phase R7)

### 6.4 Fourth check: revisit the phase prompt

Phase prompts have Section 4 (Decisions) and Section 9
(Operational notes). The answer to most situational questions
is in those sections. If they don't address the situation, the
agent surfaces it (per 6.3).

---

## Section 7 — Status tracking

Phase R1.1 creates `docs/refactoring-status.md` as the canonical
source of truth for refactor progress. Every subsequent phase PR
updates it.

Format:

```markdown
# Refactoring Status

Last updated: <date>
Current phase: <phase ID>
Next PR: <phase ID + brief title>

## Completed phases

- [x] R1.1 — ADR-0020 supersession (PR #N, merged YYYY-MM-DD)
- [x] R2.1 — ADR-0021 + ADR-0022 (PR #N+1, merged YYYY-MM-DD)
- [ ] R2.2 — Adapter transport switch (in progress)
- ...

## Current PR's checklist

(if mid-PR, the Section 7 step-by-step from the phase prompt
with checkboxes)

## Surfaced decisions

(Open architectural questions awaiting maintainer)

## Known issues

(Anything broken or quarantined that needs fixing before next
phase)
```

This document is a phase-level commitment. The agent updates it
at the end of each session producing work, and at the close of
each phase PR.

---

## Section 8 — Phase prompts directory

Each phase has a dedicated execution prompt at:

- `/mnt/user-data/outputs/MASTER_PROMPT_R1_1.md` (ADR-0020 supersession)
- `/mnt/user-data/outputs/MASTER_PROMPT_R2_1.md` (ADR-0021 + ADR-0022)
- `/mnt/user-data/outputs/MASTER_PROMPT_R2_2.md` (Adapter transport)
- `/mnt/user-data/outputs/MASTER_PROMPT_R2_3.md` (Engine transport + server)
- `/mnt/user-data/outputs/MASTER_PROMPT_R3_1.md` (EngineClientRunner)
- `/mnt/user-data/outputs/MASTER_PROMPT_R3_2.md` (CRD v1alpha2)
- `/mnt/user-data/outputs/MASTER_PROMPT_R4_1.md` (ADR-0023 stateful)
- `/mnt/user-data/outputs/MASTER_PROMPT_R4_2.md` (PostgreSQL)
- `/mnt/user-data/outputs/MASTER_PROMPT_R4_3.md` (Redis sessions)
- `/mnt/user-data/outputs/MASTER_PROMPT_R4_4.md` (Kafka producer)
- `/mnt/user-data/outputs/MASTER_PROMPT_R5_1.md` (S3 + webhook)
- `/mnt/user-data/outputs/MASTER_PROMPT_R6_1.md` (Per-service Dockerfiles)
- `/mnt/user-data/outputs/MASTER_PROMPT_R6_2.md` (Compose stack)
- `/mnt/user-data/outputs/MASTER_PROMPT_R6_3.md` (Devcontainer + DinD)
- `/mnt/user-data/outputs/MASTER_PROMPT_R7_1.md` (Helm chart)
- `/mnt/user-data/outputs/MASTER_PROMPT_R7_2.md` (Production smoke)
- `/mnt/user-data/outputs/MASTER_PROMPT_R8_1.md` (Docs refresh)

These prompts are generated as the work progresses. Each is
informed by the actual state of the repo at its phase boundary,
not pre-written based on assumptions.

When loading a phase prompt for execution, the agent loads it
AFTER this strategy prompt — not instead of. Both are in
context.

---

## Section 9 — What success looks like

The refactor is complete when ALL of these are true:

1. `git clone` + `docker compose up` produces a running stack of
   six services: control-plane, engine, three adapters,
   postgres, kafka, redis.
2. `kubectl apply -f scrapejob.yaml` against a Helm-installed
   cluster produces JSONL output to the configured sink.
3. The conformance suite passes against the running Compose
   stack with the same 56 tests as before, all green.
4. Every adapter's capability list is byte-for-byte identical to
   pre-refactor.
5. All ADRs have current status (no orphaned "Superseded by
   TBD"). The new architecture has its own ADR set (ADR-0020
   through ADR-0026). Old ADRs (ADR-0008, ADR-0019, ADR-0013)
   are explicitly marked superseded.
6. No legacy code paths remain. Specifically, no UDS support,
   no `SubprocessRunner`, no `spectre run` CLI, no bundled
   operator image with adapters inside.
7. The cross-driver demo works: same `ScrapeJob` YAML with three
   different `driver:` values produces equivalent results
   against the same engine service.
8. README, CONTRIBUTING, all architecture docs reflect
   microservices reality.

When all eight criteria pass, the refactor is done. The roadmap
moves on to Phase 4 (Intelligence layer) on top of the
microservices baseline.

---

## Section 10 — Final guidance

The agent executing this refactor is operating with multiple
months of context across many sessions. Discipline matters more
than cleverness:

- **Read this prompt at every session start.** Refresh the
  principles. Don't rely on memory of prior sessions.
- **Trust the phase sequence.** It was designed to minimize
  risk. Skipping ahead breaks invariants.
- **Surface uncertainty quickly.** Each session has a budget;
  spending it on architectural confusion is wasted.
- **Document as you go.** ADRs and status documents are written
  during work, not after. After-the-fact documentation drifts
  from reality.
- **Honor the no-legacy principle.** Every "temporary" fallback
  becomes permanent. Delete the old path in the same PR that
  introduces the new one.

The refactor is large but bounded. Seventeen PRs. A finite set
of decisions, mostly already made. Execution is the work.

If at any point the agent feels uncertain about scope or
direction beyond what this document or a phase prompt addresses,
the correct action is to stop and surface the uncertainty to the
maintainer — not to improvise.

---

*End of master strategy prompt. Pair with the phase-specific
prompt for the current PR.*
