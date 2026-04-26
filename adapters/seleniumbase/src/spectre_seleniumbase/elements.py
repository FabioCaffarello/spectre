"""ElementRef registry for the SeleniumBase adapter.

``Query`` allocates UUIDv4 ids for each Selenium ``WebElement`` it
returns; ``Extract`` (and element-scoped ``Screenshot``) looks the
id up to recover the element. Refs are scoped to a session and
tagged with a generation counter that bumps on every ``Navigate``.
An ``Extract`` against a ref whose generation does not match the
session's current generation is rejected with
``CODE_INVALID_ARGUMENT`` — see ADR-0010 §1 (strict invalidation,
fail-loud over fail-silent), carried forward to SeleniumBase by
ADR-0015 §1.

The registry intentionally avoids stable / deterministic ids
(selector + position hashes collide on similar DOMs) and avoids
serialising ``WebElement``s onto the wire (couples the protocol
to Selenium's internal representation). UUIDv4 plus a per-session
map is the cheapest scheme that satisfies the "opaque on the
wire, driver-agnostic protocol" contract.

The shape mirrors the Playwright adapter's ``elements.ts`` from
ADR-0010. The deliberate parallel — same registry contract,
different stored handle type — is recorded in ADR-0015 §2.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Any, Literal


@dataclass(frozen=True)
class RefLookup:
    """Result of :meth:`ElementRegistry.lookup_ref`.

    ``status`` is one of:

    - ``"ok"``      — the element can be returned (``element`` set).
    - ``"unknown"`` — the id was never issued for this session, or
      the session has been forgotten.
    - ``"stale"``   — the id was issued in an earlier generation
      that has since been invalidated by a ``Navigate``.
    """

    status: Literal["ok", "unknown", "stale"]
    element: Any | None = None


@dataclass
class _SessionEntry:
    generation: int = 0
    refs: dict[str, tuple[Any, int]] = field(default_factory=dict)


class ElementRegistry:
    """Per-session UUID → ``WebElement`` registry with generation tagging.

    The outer dict maps ``session_id`` to a ``_SessionEntry``. The
    entry is created lazily on first ``allocate_refs`` /
    ``bump_generation`` and dropped by ``forget_session``. The
    generation counter is a Python ``int``; overflow is irrelevant
    in practice.
    """

    def __init__(self) -> None:
        self._sessions: dict[str, _SessionEntry] = {}

    def current_generation(self, session_id: str) -> int:
        """Return the current generation counter, or zero if the
        session has no registry entry yet."""
        entry = self._sessions.get(session_id)
        return entry.generation if entry is not None else 0

    def bump_generation(self, session_id: str) -> None:
        """Increment the generation counter for a session.

        Prior refs are *not* removed — they remain in the map so a
        subsequent :meth:`lookup_ref` can distinguish *stale* (id
        was issued in an older generation) from *unknown* (id was
        never issued for this session). Entries are dropped only
        when the session is forgotten via :meth:`forget_session`.
        Idempotent on a session that has never allocated.
        """
        entry = self._sessions.get(session_id)
        if entry is None:
            self._sessions[session_id] = _SessionEntry(generation=1)
            return
        entry.generation += 1

    def forget_session(self, session_id: str) -> None:
        """Remove the session's registry entry entirely.

        Called from ``Close``. Idempotent.
        """
        self._sessions.pop(session_id, None)

    def allocate_refs(self, session_id: str, elements: list[Any]) -> list[str]:
        """Allocate a UUIDv4 for each element and store the mapping
        at the session's current generation.

        Returns the allocated ids in the same order as the inputs.
        """
        entry = self._sessions.get(session_id)
        if entry is None:
            entry = _SessionEntry()
            self._sessions[session_id] = entry
        ids: list[str] = []
        for element in elements:
            ref_id = str(uuid.uuid4())
            entry.refs[ref_id] = (element, entry.generation)
            ids.append(ref_id)
        return ids

    def lookup_ref(self, session_id: str, ref_id: str) -> RefLookup:
        """Look up an id.

        Returns a :class:`RefLookup` whose ``status`` distinguishes
        the three cases (see the dataclass docstring).
        """
        entry = self._sessions.get(session_id)
        if entry is None:
            return RefLookup(status="unknown")
        ref = entry.refs.get(ref_id)
        if ref is None:
            return RefLookup(status="unknown")
        element, generation = ref
        if generation != entry.generation:
            return RefLookup(status="stale")
        return RefLookup(status="ok", element=element)
