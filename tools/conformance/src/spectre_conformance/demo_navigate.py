"""Manual ``Navigate`` demo against a running adapter.

Connects to the TCP gRPC endpoint served by an already-running
adapter (e.g. one started with ``just pw-run``), performs an
``Initialize`` to obtain a session id, then issues a ``Navigate``
to the requested URL and prints the resulting ``NavigateResponse``.

Example::

    just pw-build
    just pw-install-browsers
    just pw-run 19091

    # in a second terminal:
    uv --project tools/conformance run python -m \\
        spectre_conformance.demo_navigate \\
        --endpoint=127.0.0.1:19091 \\
        --url=https://example.com

This script is for human verification, not automated tests. The
conformance suite uses an in-process HTTP fixture (see
``http_fixture.py``) and does not call this module.
"""

from __future__ import annotations

import argparse
import sys

import grpc
from google.protobuf.duration_pb2 import Duration
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc, errors_pb2


def _build_channel(endpoint: str) -> grpc.Channel:
    return grpc.insecure_channel(
        endpoint,
        options=[("grpc.default_authority", "localhost")],
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Drive a single Navigate against a running adapter."
    )
    parser.add_argument(
        "--endpoint",
        required=True,
        help="Adapter TCP endpoint, host:port (e.g. 127.0.0.1:8091).",
    )
    parser.add_argument("--url", required=True, help="URL to navigate to.")
    parser.add_argument(
        "--timeout-ms",
        type=int,
        default=30_000,
        help="Per-navigation timeout (milliseconds).",
    )
    args = parser.parse_args(argv)

    with _build_channel(args.endpoint) as channel:
        stub = driver_pb2_grpc.DriverStub(channel)

        init = stub.Initialize(
            driver_pb2.InitializeRequest(
                protocol_version=str(driver_pb2.DESCRIPTOR.package),
                session=driver_pb2.SessionConfig(),
            ),
            timeout=10.0,
        )
        if init.HasField("error"):
            print(f"Initialize failed: {init.error.message}", file=sys.stderr)
            return 1
        print(f"session_id={init.session_id}")

        seconds, nanos = divmod(args.timeout_ms, 1000)
        response = stub.Navigate(
            driver_pb2.NavigateRequest(
                session_id=init.session_id,
                url=args.url,
                timeout=Duration(seconds=seconds, nanos=nanos * 1_000_000),
            ),
            timeout=args.timeout_ms / 1000.0 + 5.0,
        )

        if response.HasField("error"):
            code_name = errors_pb2.DriverError.Code.Name(response.error.code)
            print(f"Navigate error: {code_name} — {response.error.message}", file=sys.stderr)
            return 1

        elapsed_ms = response.elapsed.seconds * 1000 + response.elapsed.nanos // 1_000_000
        print(f"final_url={response.final_url}")
        print(f"status_code={response.status_code}")
        print(f"elapsed_ms={elapsed_ms}")
        return 0


if __name__ == "__main__":
    sys.exit(main())
