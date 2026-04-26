"""Manual full-cycle demo against a running adapter.

Connects to the Unix domain socket served by an already-running
adapter (e.g. one started with ``just pw-run``), performs the
complete RPC cycle against a target URL, and prints each
response. The cycle is::

    Initialize → Navigate → Query → Extract → Close

This is the canonical manual smoke test for PR5. The conformance
suite (``test_close.py``, ``test_query.py``, ``test_extract.py``)
covers the same surface automatically; this script exists for
human verification when iterating on the adapter.

Example::

    just pw-build
    just pw-install-browsers
    just pw-run -- --socket=/tmp/spectre-demo.sock

    # in a second terminal:
    uv --project tools/conformance run python -m \\
        spectre_conformance.demo_full_cycle \\
        --socket=/tmp/spectre-demo.sock \\
        --url=https://example.com \\
        --selector="h1"
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import grpc
from google.protobuf.duration_pb2 import Duration
from spectre.driver.v1alpha1 import (
    driver_pb2,
    driver_pb2_grpc,
    errors_pb2,
    extraction_pb2,
)


def _build_channel(socket_path: Path) -> grpc.Channel:
    return grpc.insecure_channel(
        f"unix:{socket_path}",
        options=[("grpc.default_authority", "localhost")],
    )


def _print_error(stage: str, error: errors_pb2.DriverError) -> None:
    code_name = errors_pb2.DriverError.Code.Name(error.code)
    print(f"{stage} error: {code_name} — {error.message}", file=sys.stderr)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Drive Initialize → Navigate → Query → Extract → Close against a running adapter."
    )
    parser.add_argument("--socket", required=True, type=Path, help="Adapter socket path.")
    parser.add_argument("--url", required=True, help="URL to navigate to.")
    parser.add_argument(
        "--selector",
        default="body",
        help="CSS selector to query for after navigating (default: body).",
    )
    parser.add_argument(
        "--timeout-ms",
        type=int,
        default=30_000,
        help="Per-navigation timeout (milliseconds).",
    )
    args = parser.parse_args(argv)

    if not args.socket.exists():
        print(f"socket not found: {args.socket}", file=sys.stderr)
        return 2

    with _build_channel(args.socket) as channel:
        stub = driver_pb2_grpc.DriverStub(channel)

        init = stub.Initialize(
            driver_pb2.InitializeRequest(
                protocol_version=str(driver_pb2.DESCRIPTOR.package),
                session=driver_pb2.SessionConfig(),
            ),
            timeout=10.0,
        )
        if init.HasField("error"):
            _print_error("Initialize", init.error)
            return 1
        print(f"[Initialize] session_id={init.session_id}")
        print(f"[Initialize] capabilities={list(init.capabilities.names)}")

        seconds, nanos = divmod(args.timeout_ms, 1000)
        nav = stub.Navigate(
            driver_pb2.NavigateRequest(
                session_id=init.session_id,
                url=args.url,
                timeout=Duration(seconds=seconds, nanos=nanos * 1_000_000),
            ),
            timeout=args.timeout_ms / 1000.0 + 5.0,
        )
        if nav.HasField("error"):
            _print_error("Navigate", nav.error)
            return 1
        elapsed_ms = nav.elapsed.seconds * 1000 + nav.elapsed.nanos // 1_000_000
        print(
            f"[Navigate] final_url={nav.final_url} status_code={nav.status_code} "
            f"elapsed_ms={elapsed_ms}"
        )

        query = stub.Query(
            extraction_pb2.QueryRequest(
                session_id=init.session_id,
                selector=args.selector,
                kind=extraction_pb2.SELECTOR_KIND_CSS,
            ),
            timeout=10.0,
        )
        if query.HasField("error"):
            _print_error("Query", query.error)
            return 1
        print(
            f"[Query] selector={args.selector!r} matches={len(query.elements)}"
        )
        if not query.elements:
            print("[Query] no elements to extract; skipping Extract")
        else:
            element = query.elements[0]
            print(f"[Query] first opaque_id={element.opaque_id}")
            extract = stub.Extract(
                extraction_pb2.ExtractRequest(
                    session_id=init.session_id,
                    element=element,
                    fields=[
                        extraction_pb2.Field(
                            name="text",
                            mode=extraction_pb2.Field.MODE_TEXT_CONTENT,
                        ),
                    ],
                ),
                timeout=10.0,
            )
            if extract.HasField("error"):
                _print_error("Extract", extract.error)
                return 1
            for field in extract.values.fields:
                print(
                    f"[Extract] {field.name}={json.loads(field.json_value)!r}"
                )

        close = stub.Close(
            driver_pb2.CloseRequest(session_id=init.session_id),
            timeout=10.0,
        )
        if close.HasField("error"):
            _print_error("Close", close.error)
            return 1
        print("[Close] ok")
        return 0


if __name__ == "__main__":
    sys.exit(main())
