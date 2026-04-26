"""Session manager for the SeleniumBase adapter.

Owns the lazy WebDriver launch and the per-session driver
allocation. The contract:

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
- ``close_session(session_id)`` — quits the WebDriver if one was
  launched, evicts the session entry. Returns ``True`` if the
  session was registered, ``False`` otherwise.
- ``close_all()`` — quits every driver and clears every
  registration. Idempotent and used in the SIGTERM handler so
  Chrome process trees do not outlive the adapter.

The class is constructed with a ``DriverFactory`` so unit tests can
mock the SeleniumBase surface without launching a real browser. PR9
implements only what ``Navigate`` needs; PR10 will extend the
manager with element-ref bookkeeping (mirroring the ``ElementRegistry``
the Playwright adapter introduced in ADR-0010).

See ADR-0014 (this PR) and ADR-0009 for the lifecycle rationale.
"""

from __future__ import annotations

import contextlib
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

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
    a third driver lands.
    """

    factory: DriverFactory
    _sessions: dict[str, _Session] = field(default_factory=dict, init=False, repr=False)

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

    def close_session(self, session_id: str) -> bool:
        """Quit the driver and forget the session.

        Returns ``True`` if the session was registered, ``False`` if the
        id was unknown. Errors raised by ``driver.quit()`` are swallowed —
        the session is forgotten regardless.
        """
        session = self._sessions.pop(session_id, None)
        if session is None:
            return False
        if session.driver is not None:
            # quit() must never crash teardown — a failing driver still has
            # to be evicted so the registry stays consistent.
            with contextlib.suppress(Exception):
                session.driver.quit()
        return True

    def close_all(self) -> None:
        """Quit every driver and clear every registration. Idempotent."""
        sessions = list(self._sessions.values())
        self._sessions.clear()
        for session in sessions:
            if session.driver is not None:
                with contextlib.suppress(Exception):
                    session.driver.quit()
