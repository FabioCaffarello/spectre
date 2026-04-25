"""Driver adapter entry point.

As of v0.1.0a0 this is a placeholder that prints its build identity
and exits. The gRPC Driver server lands in Phase 2 of the project
roadmap.
"""

from __future__ import annotations

import sys

from spectre_seleniumbase import PROTOCOL_VERSION, __version__


def identity() -> str:
    """Return the adapter's build identity string."""
    return f"spectre-seleniumbase {__version__} (driver protocol {PROTOCOL_VERSION})"


def main() -> None:
    """Print the build identity and exit."""
    print(identity(), file=sys.stdout)


if __name__ == "__main__":
    main()
