#!/usr/bin/env python3
"""Container health probe for the Borealis API backend."""

from __future__ import annotations

import os
import sys
import urllib.error
import urllib.request


def main() -> int:
    host = os.environ.get("BOREALIS_API_HEALTH_HOST", "127.0.0.1").strip() or "127.0.0.1"
    port = os.environ.get("BOREALIS_API_HEALTH_PORT", "5000").strip() or "5000"
    url = f"http://{host}:{port}/health"
    try:
        with urllib.request.urlopen(url, timeout=3) as response:
            return 0 if response.status == 200 else 1
    except (OSError, urllib.error.URLError, urllib.error.HTTPError, ValueError):
        return 1


if __name__ == "__main__":
    sys.exit(main())
