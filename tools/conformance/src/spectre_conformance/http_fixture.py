"""Local HTTP server used by the conformance suite.

Tests that exercise ``Navigate`` (and any future RPC that drives a
real browser) point at this fixture rather than the public internet.
A flaky external host costs the project credibility faster than any
other failure mode; running everything on ``127.0.0.1:<random>``
removes the entire class of network flake.

Routes
------

==============  ========================================================
Path            Behaviour
==============  ========================================================
``GET /ok``     200 with body ``ok``.
``GET /redirect``  302 to ``/ok``.
``GET /status/<n>`` ``<n>`` parsed as an int. Responds with that status
                code and a body of the same digits. ``400`` if ``<n>``
                is not a valid HTTP status integer (100-599).
``GET /slow``   Sleeps 5 s, then 200 with body ``slow``. Used by the
                timeout test with a short client-side timeout.
``GET /elements``   200 with a small HTML page that has a stable
                DOM: ``<h1>``, three ``<li>`` items, an ``<a>`` link
                with a known href, and an element carrying
                ``data-test="primary"``. Used by Query and Extract
                conformance tests.
``GET /elements-2`` 200 with a different HTML page (different
                heading, different list items). Used to test that
                an ElementRef issued against ``/elements`` is
                invalidated after navigating to ``/elements-2``.
==============  ========================================================

Lifecycle
---------

The class is intentionally simple. ``start()`` binds to ``127.0.0.1``
on a port chosen by the OS (port 0). ``stop()`` shuts down the server
and joins its thread. ``base_url`` returns the URL prefix tests
should append paths to.

The pytest fixture in ``tests/conftest.py`` is session-scoped, so a
single server instance serves the whole pytest invocation. The
fixture exposes a ``LocalHttpServer`` instance directly so individual
tests can read its ``base_url``.

See ADR-0009.
"""

from __future__ import annotations

import threading
import time
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import TracebackType

SLOW_ROUTE_DELAY_S: float = 5.0


# Stable HTML pages used by the Query and Extract conformance tests.
# The DOM is intentionally minimal and deterministic so selector and
# extract assertions can reference exact structure. The class names
# and attribute values are referenced by tests; do not rename them.
ELEMENTS_HTML: bytes = (
    b"<!doctype html>"
    b'<html lang="en">'
    b'<head><meta charset="utf-8"><title>elements</title></head>'
    b"<body>"
    b'<h1 id="title">Elements Page</h1>'
    b'<ul id="items">'
    b'<li class="item">first</li>'
    b'<li class="item">second</li>'
    b'<li class="item">third</li>'
    b"</ul>"
    b'<a id="link" href="https://example.com/target">visit</a>'
    b'<div id="badge" data-test="primary">Primary</div>'
    b"</body>"
    b"</html>"
)

ELEMENTS_TWO_HTML: bytes = (
    b"<!doctype html>"
    b'<html lang="en">'
    b'<head><meta charset="utf-8"><title>elements-2</title></head>'
    b"<body>"
    b'<h1 id="title">Second Page</h1>'
    b'<p class="paragraph">A different page entirely.</p>'
    b"</body>"
    b"</html>"
)


class _LocalHandler(BaseHTTPRequestHandler):
    # Silence the default stderr access log so pytest output stays clean.
    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        return

    def do_GET(self) -> None:  # noqa: N802 — http.server contract
        path = self.path.split("?", 1)[0]

        if path == "/ok":
            self._respond(HTTPStatus.OK.value, b"ok")
            return

        if path == "/redirect":
            self.send_response(HTTPStatus.FOUND.value)
            self.send_header("Location", "/ok")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return

        if path.startswith("/status/"):
            tail = path[len("/status/") :]
            try:
                code = int(tail)
            except ValueError:
                self._respond(HTTPStatus.BAD_REQUEST.value, b"bad status")
                return
            if not 100 <= code <= 599:
                self._respond(HTTPStatus.BAD_REQUEST.value, b"out of range")
                return
            self._respond(code, str(code).encode("ascii"))
            return

        if path == "/slow":
            time.sleep(SLOW_ROUTE_DELAY_S)
            self._respond(HTTPStatus.OK.value, b"slow")
            return

        if path == "/elements":
            self._respond_html(ELEMENTS_HTML)
            return

        if path == "/elements-2":
            self._respond_html(ELEMENTS_TWO_HTML)
            return

        self._respond(HTTPStatus.NOT_FOUND.value, b"not found")

    def _respond(self, status: int, body: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def _respond_html(self, body: bytes) -> None:
        self.send_response(HTTPStatus.OK.value)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class LocalHttpServer:
    """A thin wrapper around ``ThreadingHTTPServer`` for tests.

    ``start()`` binds to ``127.0.0.1`` on a random port and serves
    requests on a background thread. ``stop()`` shuts it down. The
    ``base_url`` property returns ``http://127.0.0.1:<port>``.
    """

    def __init__(self) -> None:
        self._server: ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        if self._server is not None:
            raise RuntimeError("LocalHttpServer.start() called twice")
        self._server = ThreadingHTTPServer(("127.0.0.1", 0), _LocalHandler)
        self._thread = threading.Thread(
            target=self._server.serve_forever,
            name="spectre-conf-http",
            daemon=True,
        )
        self._thread.start()

    def stop(self) -> None:
        if self._server is None:
            return
        self._server.shutdown()
        self._server.server_close()
        if self._thread is not None:
            self._thread.join(timeout=2.0)
        self._server = None
        self._thread = None

    @property
    def base_url(self) -> str:
        if self._server is None:
            raise RuntimeError("LocalHttpServer.start() not called")
        addr = self._server.server_address
        host = str(addr[0])
        port = int(addr[1])
        return f"http://{host}:{port}"

    def __enter__(self) -> LocalHttpServer:
        self.start()
        return self

    def __exit__(
        self,
        _exc_type: type[BaseException] | None,
        _exc: BaseException | None,
        _tb: TracebackType | None,
    ) -> None:
        self.stop()
