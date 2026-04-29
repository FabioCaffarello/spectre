"""Session manager for the SeleniumBase adapter.

Owns the lazy WebDriver launch, the per-session driver
allocation, and the per-session :class:`ElementRegistry`
that backs ``Query``, ``Extract``, and the element-scoped
``Screenshot``. R4.3 externalises session metadata to Redis
(ADR-0023 §4 + §5): ``register`` writes the metadata document,
``validate`` reads it on every non-Initialize RPC, and
``close_session`` deletes the key (best-effort). The runtime
WebDriver objects stay process-local — the §5 restart-invalidation
contract makes Pod restart equivalent to session loss for clients.

Contract (R4.3):

- ``register(session_id)`` — ``Initialize`` calls this. Writes a
  :class:`SessionMetadata` document to Redis with the current
  ``adapter_instance_id`` and adds the id to the local set.
  Raises if Redis is unreachable; the caller maps the failure to
  gRPC ``UNAVAILABLE``.
- ``validate(session_id)`` — every non-Initialize RPC calls this
  before doing any work. Returns one of three :class:`Validation`
  kinds (``ok`` / ``unknown`` / ``different_instance``). The
  ``ok`` path refreshes ``last_active_at`` and the TTL (last-
  write-wins per phase prompt §4.5).
- ``has(session_id)`` — true once ``register`` has been called for
  the id locally and ``close_session`` / ``close_all`` have not
  yet evicted it. Used for cheap membership checks where Redis is
  not authoritative.
- ``get_or_create_driver(session_id)`` — first call for a
  registered id launches the per-session WebDriver. Subsequent
  calls with the same id return the same driver. Raises
  :class:`UnknownSessionError` if the id is not registered.
- ``driver_of(session_id)`` — returns the launched driver or
  ``None`` (no UnknownSessionError; used by handlers that need
  to distinguish "session unknown" from "session known but
  Navigate not yet called").
- ``bump_generation`` / ``current_generation`` /
  ``register_element`` / ``register_elements`` /
  ``lookup_element`` — element-registry delegation per ADR-0010.
- ``close_session(session_id)`` — quits the WebDriver if one was
  launched, evicts the local entry, forgets the ElementRegistry
  entry, and best-effort deletes the Redis key (TTL is the
  safety net; phase prompt §4.6).
- ``close_all()`` — quits every driver, clears every local
  registration, forgets every registry entry. Idempotent.
  Does not enumerate Redis keys for deletion — restart
  invalidation handles abandoned keys via TTL expiry.

The class is constructed with a ``DriverFactory``, a
``RedisClient``, and the adapter's ``instance_id``. Unit tests
pass a ``fakeredis``-backed client and any ``instance_id``
string.

See ADR-0014, ADR-0015, ADR-0023 §4–§5 (R4.3), and ADR-0009 for
the lifecycle rationale.
"""

from __future__ import annotations

import contextlib
import sys
from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Literal

from .elements import ElementRegistry, RefLookup
from .redis_client import ADAPTER_NAME, RedisClient, SessionMetadata

DriverFactory = Callable[[], Any]


class UnknownSessionError(RuntimeError):
    """Raised when an RPC references a ``session_id`` that ``Initialize`` did
    not produce."""

    def __init__(self, session_id: str) -> None:
        super().__init__(f"unknown session_id: {session_id!r}")
        self.session_id = session_id


@dataclass
class Validation:
    """Result of :meth:`SessionManager.validate`.

    ``kind`` is ``"ok"`` when Redis has the session and the
    stored ``adapter_instance_id`` matches, ``"unknown"`` when
    Redis has no entry, and ``"different_instance"`` when the
    stored id belongs to a foreign adapter instance — the §5
    restart-invalidation case the gRPC handler maps to
    ``UNAVAILABLE``.
    """

    kind: Literal["ok", "unknown", "different_instance"]
    metadata: SessionMetadata | None = None
    stored_instance_id: str | None = None


@dataclass
class _Session:
    driver: Any | None = None


@dataclass
class SessionManager:
    """In-memory registry mapping ``session_id`` → WebDriver instance,
    backed by Redis-resident metadata for the §5 restart-invalidation
    contract.

    The Playwright adapter's ``SessionManager`` (TypeScript) and this
    one serve the same role; ADR-0014 records the deliberate decision
    to re-implement the shape rather than extract a shared contract
    before a third driver lands. ADR-0015 §1 carries the same
    reasoning forward to the ElementRegistry integration; ADR-0023 §8
    extends it to per-language Redis libraries.
    """

    factory: DriverFactory
    redis: RedisClient
    instance_id: str
    _sessions: dict[str, _Session] = field(default_factory=dict, init=False, repr=False)
    _elements: ElementRegistry = field(default_factory=ElementRegistry, init=False, repr=False)

    @property
    def adapter_instance_id(self) -> str:
        """Expose the adapter's instance UUID for tests and diagnostics."""
        return self.instance_id

    def register(self, session_id: str) -> None:
        """Write the session metadata to Redis and register the id locally.

        Order matters: a Redis write failure must leave the local
        set unaware of the id so a retry produces a fresh write
        rather than the appearance of a registered-but-not-stored
        session.
        """
        now = _utc_now_iso()
        metadata: SessionMetadata = {
            "session_id": session_id,
            "adapter": ADAPTER_NAME,
            "adapter_instance_id": self.instance_id,
            "created_at": now,
            "last_active_at": now,
            "metadata": {},
        }
        self.redis.set_session(session_id, metadata)
        self._sessions.setdefault(session_id, _Session())

    def has(self, session_id: str) -> bool:
        """Return ``True`` if the session is registered locally."""
        return session_id in self._sessions

    def validate(self, session_id: str) -> Validation:
        """Validate the session against Redis.

        Returns a :class:`Validation` whose ``kind`` distinguishes
        ``ok`` / ``unknown`` / ``different_instance``. The ``ok``
        path refreshes ``last_active_at`` and the TTL via a SET
        round-trip — last-write-wins per phase prompt §4.5.
        """
        metadata = self.redis.get_session(session_id)
        if metadata is None:
            return Validation(kind="unknown")
        stored_id = metadata.get("adapter_instance_id", "")
        if stored_id != self.instance_id:
            return Validation(kind="different_instance", stored_instance_id=stored_id)
        metadata["last_active_at"] = _utc_now_iso()
        self.redis.set_session(session_id, metadata)
        return Validation(kind="ok", metadata=metadata)

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
        """Allocate a UUID for a single ``WebElement``."""
        ids = self._elements.allocate_refs(session_id, [web_element])
        return ids[0]

    def register_elements(self, session_id: str, web_elements: list[Any]) -> list[str]:
        """Allocate UUIDs for a list of ``WebElement``s in order."""
        return self._elements.allocate_refs(session_id, list(web_elements))

    def lookup_element(self, session_id: str, ref_id: str) -> RefLookup:
        """Resolve a UUID back to its ``WebElement``."""
        return self._elements.lookup_ref(session_id, ref_id)

    # -- Lifecycle ------------------------------------------------------

    def close_session(self, session_id: str) -> bool:
        """Tear down the local driver, forget the registry entry, and
        best-effort delete the Redis key.

        Returns ``True`` if the session was registered locally,
        ``False`` if the id was unknown. Errors raised by
        ``driver.quit()`` and by Redis ``DEL`` are swallowed —
        the session is forgotten regardless. The Redis TTL is the
        safety net for the rare delete failure (§4.6).
        """
        session = self._sessions.pop(session_id, None)
        if session is None:
            return False
        self._elements.forget_session(session_id)
        try:
            self.redis.delete_session(session_id)
        except Exception as exc:  # noqa: BLE001 — best-effort delete
            sys.stderr.write(
                f"redis delete failed for session {session_id}: {exc}\n",
            )
            sys.stderr.flush()
        if session.driver is not None:
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


def _utc_now_iso() -> str:
    """Return the current UTC time formatted as ISO-8601 with Z suffix.

    Matches the Playwright adapter's ``new Date().toISOString()``
    output (``YYYY-MM-DDTHH:MM:SS.sssZ``) so the JSON document
    round-trips identically through any adapter.
    """
    now = datetime.now(timezone.utc)
    return now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
