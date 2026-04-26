"""Session manager for the SeleniumBase adapter.

Owns the lazy WebDriver launch, the per-session driver
allocation, and (PR10) the per-session :class:`ElementRegistry`
that backs ``Query``, ``Extract``, and the element-scoped
``Screenshot``. The contract:

- ``register(session_id)`` — ``Initialize`` calls this to declare
  the id. No browser work happens here; the registration is
  metadata only. Calling it twice with the same id is a no-op.
- ``has(session_id)`` — true once ``register`` has been called for
  the id and ``close_session``/``close_all`` have not yet evicted
  it. Used by every RPC to reject unknown ids with
  ``CODE_INVALID_ARGUMENT``.
- ``get_or_create_driver(session_id)`` — first call for a
  registered id launches the per-session WebDriver. Subsequent
  calls with the same id return the same driver. Raises
  :class:`UnknownSessionError` if the id is not registered.
- ``driver_of(session_id)`` — returns the launched driver or
  ``None`` (no UnknownSessionError; used by handlers that need
  to distinguish "session unknown" from "session known but
  Navigate not yet called").
- ``bump_generation(session_id)`` — invalidates every prior
  ``ElementRef`` for the session by incrementing its generation
  counter. Called after every successful ``Navigate``. See
  ADR-0010 §1, ADR-0015 §1.
- ``current_generation(session_id)`` — returns the session's
  current generation counter; used by ``Query`` and
  ``Extract``.
- ``register_element(session_id, web_element)`` — allocates a
  UUID for one ``WebElement`` in the session's registry.
- ``register_elements(session_id, web_elements)`` — bulk variant
  for the ``Query`` happy path.
- ``lookup_element(session_id, ref_id)`` — resolves a UUID back
  to a ``RefLookup`` whose ``status`` distinguishes *ok*,
  *stale*, and *unknown*.
- ``close_session(session_id)`` — quits the WebDriver if one
  was launched, evicts the session entry, and forgets the
  ElementRegistry entry for the session. Returns ``True`` if
  the session was registered, ``False`` otherwise.
- ``close_all()`` — quits every driver, clears every
  registration, forgets every registry entry. Idempotent and
  used in the SIGTERM handler so Chrome process trees do not
  outlive the adapter.

The class is constructed with a ``DriverFactory`` so unit tests
can mock the SeleniumBase surface without launching a real
browser. The ElementRegistry is owned by the SessionManager and
exposed through delegating methods rather than as a public
attribute, so handlers do not need to know it exists.

See ADR-0014 (PR9), ADR-0015 (PR10) and ADR-0009 for the
lifecycle rationale.
"""

from __future__ import annotations

import contextlib
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from .elements import ElementRegistry, RefLookup

DriverFactory = Callable[[], Any]


class UnknownSessionError(RuntimeError):
    """Raised when an RPC references a ``session_id`` that ``Initialize`` did
    not produce."""

    def __init__(self, session_id: str) -> None:
        super().__init__(f"unknown session_id: {session_id!r}")
        self.session_id = session_id


@dataclass
class _Session:
    driver: Any | None = None


@dataclass
class SessionManager:
    """In-memory registry mapping ``session_id`` → WebDriver instance.

    The Playwright adapter's ``SessionManager`` (TypeScript) and this one
    serve the same role; ADR-0014 records the deliberate decision to
    re-implement the shape rather than extract a shared contract before
    a third driver lands. ADR-0015 §1 carries the same reasoning forward
    to the ElementRegistry integration.
    """

    factory: DriverFactory
    _sessions: dict[str, _Session] = field(default_factory=dict, init=False, repr=False)
    _elements: ElementRegistry = field(default_factory=ElementRegistry, init=False, repr=False)

    def register(self, session_id: str) -> None:
        """Register a new session_id without launching the browser."""
        self._sessions.setdefault(session_id, _Session())

    def has(self, session_id: str) -> bool:
        """Return ``True`` if the session is registered."""
        return session_id in self._sessions

    def get_or_create_driver(self, session_id: str) -> Any:
        """Return the session's driver, launching it on first call.

        Raises :class:`UnknownSessionError` if the id is not registered.
        """
        session = self._sessions.get(session_id)
        if session is None:
            raise UnknownSessionError(session_id)
        if session.driver is None:
            session.driver = self.factory()
        return session.driver

    def driver_of(self, session_id: str) -> Any | None:
        """Return the session's driver if one has been launched, else ``None``."""
        session = self._sessions.get(session_id)
        return session.driver if session is not None else None

    # -- ElementRegistry delegation -------------------------------------

    def bump_generation(self, session_id: str) -> None:
        """Invalidate every prior ElementRef for the session.

        Called after a successful ``Navigate``. See ADR-0010 §1
        and ADR-0015 §1.
        """
        self._elements.bump_generation(session_id)

    def current_generation(self, session_id: str) -> int:
        """Return the session's current generation counter, or zero."""
        return self._elements.current_generation(session_id)

    def register_element(self, session_id: str, web_element: Any) -> str:
        """Allocate a UUID for a single ``WebElement``.

        Convenience wrapper over :meth:`register_elements` for
        callers that already have one element in hand.
        """
        ids = self._elements.allocate_refs(session_id, [web_element])
        return ids[0]

    def register_elements(self, session_id: str, web_elements: list[Any]) -> list[str]:
        """Allocate UUIDs for a list of ``WebElement``s in order.

        The returned ids align positionally with the inputs.
        """
        return self._elements.allocate_refs(session_id, list(web_elements))

    def lookup_element(self, session_id: str, ref_id: str) -> RefLookup:
        """Resolve a UUID back to its ``WebElement``.

        Returns a :class:`RefLookup` whose ``status`` distinguishes
        *ok*, *stale*, and *unknown* (see ``elements.py``).
        """
        return self._elements.lookup_ref(session_id, ref_id)

    # -- Lifecycle ------------------------------------------------------

    def close_session(self, session_id: str) -> bool:
        """Quit the driver, forget the session, and clear its registry.

        Returns ``True`` if the session was registered, ``False`` if the
        id was unknown. Errors raised by ``driver.quit()`` are swallowed —
        the session is forgotten regardless.
        """
        session = self._sessions.pop(session_id, None)
        if session is None:
            return False
        # Forget every ElementRef before tearing down the driver so a
        # late ``lookup_element`` call cannot resolve to a quitting
        # WebElement.
        self._elements.forget_session(session_id)
        if session.driver is not None:
            # quit() must never crash teardown — a failing driver still has
            # to be evicted so the registry stays consistent.
            with contextlib.suppress(Exception):
                session.driver.quit()
        return True

    def close_all(self) -> None:
        """Quit every driver and clear every registration. Idempotent."""
        session_ids = list(self._sessions.keys())
        sessions = list(self._sessions.values())
        self._sessions.clear()
        for session_id in session_ids:
            self._elements.forget_session(session_id)
        for session in sessions:
            if session.driver is not None:
                with contextlib.suppress(Exception):
                    session.driver.quit()
