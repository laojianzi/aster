#!/usr/bin/env python3
"""Run a real native window, require a rendered-frame marker and clean exit.

This deliberately does not read kubeconfig, and is not a substitute for IME,
accessibility, signed-installer or GPU-driver qualification.
"""
from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys


def main() -> int:
    if len(sys.argv) != 3:
        raise SystemExit("usage: window_smoke.py BINARY LOG_PATH")
    binary = Path(sys.argv[1]).resolve(strict=True)
    log = Path(sys.argv[2])
    log.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, ASTER_NATIVE_SMOKE="1")
    try:
        result = subprocess.run([str(binary)], env=env, capture_output=True,
                                text=True, encoding="utf-8", errors="replace", timeout=30)
        output = result.stdout + result.stderr
    except subprocess.TimeoutExpired as exc:
        output = str(exc)
        log.write_text(output, encoding="utf-8")
        print(output, file=sys.stderr)
        return 1
    log.write_text(output, encoding="utf-8")
    print(output)
    if result.returncode != 0 or "ASTER_NATIVE_SMOKE_OK" not in output:
        print(f"native window smoke failed: exit={result.returncode}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
