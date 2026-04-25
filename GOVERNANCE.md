# Project Governance

This document describes how decisions are made in Spectre.

## Current model

Spectre is currently **maintainer-led**. Fabio Caffarello
([@FabioCaffarello](https://github.com/FabioCaffarello)) is the sole
maintainer and holds final decision authority on all matters.

This is a deliberate choice for the early-stage project. A single
maintainer can move quickly, keep the architecture coherent, and set
quality standards without the overhead of formal consensus processes.
As the project grows and attracts long-term contributors, governance
will evolve toward a maintainer council. This document will be updated
when that transition happens.

## Decision-making process

### Trivial changes

Bug fixes, documentation improvements, dependency bumps, and similar
low-risk changes follow the standard pull request review process.
Approval from the maintainer is sufficient. These are typically
merged within a few days.

### Non-trivial changes

Any change that affects the protocol, the engine architecture, the
control plane interfaces, or introduces a significant new feature
requires an **Architecture Decision Record (ADR)** before
implementation.

The ADR flow is:

1. **Issue.** Open an issue describing the problem and the proposed
   change.
2. **Discussion.** Comments, alternatives, and trade-offs are
   surfaced in the issue thread.
3. **Proposal.** A formal ADR is drafted using the
   [template](docs/adr/0000-template.md) and submitted as a PR
   adding a file to `docs/adr/`.
4. **Review.** The maintainer (and any reviewers tagged) review the
   ADR. Status moves through `proposed` → (`accepted` |
   `rejected` | `superseded`).
5. **Implementation.** Once accepted, implementation proceeds in
   separate PRs that reference the ADR number.

ADR review typically completes within one week.

### Protocol changes

Changes to `proto/spectre/driver/v1alpha1/` (or any future versioned
package) are subject to additional rules:

- **Breaking changes** require a version bump (`v1alpha1` →
  `v1alpha2`, etc.).
- The `proto-check` CI job enforces this automatically using `buf
  breaking`.
- Once a version is marked stable (`v1.0`), it is frozen. All future
  changes go to a new version path.

Drivers declare which protocol version they implement in their
`driver.yaml` manifest. This means the engine can run a mix of drivers
speaking different protocol versions simultaneously, easing migration.

## Roles

### Maintainer

Currently a single role held by Fabio Caffarello. Responsibilities:

- Final decision on ADRs
- PR review and merge
- Release management
- Setting roadmap priorities
- Maintaining the conformance test suite

### Contributor

Anyone who has had a PR merged. No formal recognition beyond commit
history at this stage.

### Driver author

Anyone who maintains a driver listed in the official driver registry.
Driver authors are responsible for keeping their driver compatible
with the latest stable protocol version and passing the conformance
suite. A driver is removed from the registry if it remains incompatible
for more than two consecutive minor releases.

## Future evolution

As the project grows, this governance model will evolve. Triggers
that would prompt a governance update:

- More than three regular contributors with sustained commit history
- More than five active drivers in the official registry
- Adoption by an organization willing to share maintenance burden

Possible future structures (not yet decided):

- **Maintainer council** with shared merge rights and quorum-based
  ADR acceptance
- **Working groups** for protocol, drivers, and tooling
- **Foundation governance** if the project joins an organization like
  the Linux Foundation, Apache Foundation, or CNCF

Any change to this governance document is itself an ADR-worthy
decision and will follow the non-trivial change process above.
