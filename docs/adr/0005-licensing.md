---
status: accepted
date: 2026-04-25
deciders: [Fabio Caffarello]
---

# Licensing (Apache 2.0)

## Context and Problem Statement

Spectre is intended to be a flagship open-source project. The license
choice influences who will adopt the project, who will contribute, and
what kind of commercial use is permitted. License changes are
effectively irreversible once contributors outside the original author
have signed work in: relicensing requires re-permissioning every
contribution.

## Decision Drivers

- Maximum adoption by individual users and companies of any size.
- OSI-approved license (most enterprise legal departments will refuse
  non-OSI licenses by default).
- Explicit patent grant (browser automation has a litigious history
  around fingerprint-related patents).
- Compatibility with commercial use, including embedding inside
  proprietary products.
- Alignment with the project owner's stated goal of professional
  visibility leading to industry opportunities.

## Considered Options

- **Option A — Apache 2.0**.
- **Option B — MIT**.
- **Option C — BSL 1.1** (Business Source License with delayed open
  conversion).
- **Option D — AGPL 3.0** (strong copyleft, network-use clause).

## Decision Outcome

Chosen option: **Option A — Apache 2.0**.

Apache 2.0 satisfies all decision drivers:

- OSI-approved and DFSG-free.
- Explicit grant of patent rights from contributors to users, with a
  defensive termination clause.
- Permits commercial use, modification, and redistribution.
- Requires only attribution (NOTICE file) and preservation of license
  notices.
- Compatible with most other OSI licenses for downstream use.

### Consequences

- Good, because companies of any size can adopt and integrate Spectre
  without legal review friction.
- Good, because the patent grant is meaningful in this domain; the
  alternative (no patent grant under MIT) is a real concern for
  enterprise adopters.
- Good, because contributors retain copyright in their own
  contributions while granting clear downstream rights.
- Bad, because Apache 2.0 does not require downstream contributions
  back. Spectre will rely on community goodwill and a clear governance
  model (see GOVERNANCE.md) for sustained contribution flow.
- Neutral, because the NOTICE file requirement adds a small ongoing
  maintenance burden as third-party dependencies are integrated.

### Confirmation

- `LICENSE` contains the unmodified Apache 2.0 text.
- `NOTICE` lists the project copyright and any required third-party
  attributions.
- `CONTRIBUTING.md` states clearly that contributions are licensed
  under Apache 2.0.
- New source files include the standard SPDX identifier
  `// SPDX-License-Identifier: Apache-2.0` at the top.

## Pros and Cons of the Options

### Option B — MIT

- Good, because shorter and simpler than Apache 2.0.
- Bad, because no explicit patent grant. In a domain that has seen
  patent litigation around browser-fingerprinting techniques, this is
  a meaningful gap.

### Option C — BSL 1.1

- Good, because allows the project owner to retain commercial control
  during the early years.
- Bad, because not OSI-approved; many enterprise legal departments
  refuse it categorically.
- Bad, because it would shrink the contributor pool dramatically.
- Bad, because it conflicts with the stated career-visibility goal —
  hiring managers value broadly adopted permissive open-source
  contributions.

### Option D — AGPL 3.0

- Good, because the network-use clause prevents proprietary forks
  from extracting value without contributing back.
- Bad, because most commercial adopters have policies against AGPL
  inside their stack. This blocks one of the largest potential
  audiences (companies running automated data acquisition pipelines).
- Bad, because the copyleft requirement for derivative works
  complicates dual-licensing and SDK distribution.

## More Information

- Apache 2.0 text: <https://www.apache.org/licenses/LICENSE-2.0>
- SPDX identifier reference: <https://spdx.dev/learn/handling-license-info/>
- Related: [GOVERNANCE.md](../../GOVERNANCE.md).
